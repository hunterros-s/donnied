package device

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smartwatch/internal/ble"
	"smartwatch/internal/config"
	"smartwatch/protocol"
)

const (
	reconnectInitialInterval = 5 * time.Second
	reconnectMaxInterval     = 60 * time.Second
	scanTimeout              = 15 * time.Second
	cmdTimeout               = 5 * time.Second
	heartbeatInterval        = 60 * time.Second

	// If scan gating keeps missing the paired watch, occasionally try a direct
	// connect anyway. Some devices/BlueZ states do not expose useful adverts.
	directConnectAfterScanMisses = 5
)

// errConfigChanged is returned by doScan when a wake signal is received
// during scanning, signalling that the outer loop should reload config.
var errConfigChanged = errors.New("device config changed")

// Manager handles BLE pairing, persistent reconnect, and protocol dispatch.
// Only the Run goroutine owns mutable state. Public methods send typed
// requests or mutate config directly (Pair/Unpair).
type Manager struct {
	client *ble.Client
	logger *slog.Logger
	config *config.Store

	wake     chan struct{}
	reqs     chan request
	snapshot atomic.Value // stores Snapshot
	nextGen  uint64

	// txnMu serialises transactional requests so that only one
	// request/response exchange is in flight at a time.
	txnMu sync.Mutex

	// Multi-packet parsers (reused across requests within a session).
	hrLogParser *protocol.HRLogParser
	stepsParser *protocol.StepsParser

	// Unsolicited raw device events for higher layers.
	events chan Event
}

// New creates a Manager. It does not start BLE operations until Run is called.
func New(cfg *config.Store, logger *slog.Logger) *Manager {
	m := &Manager{
		client:      ble.New(logger),
		logger:      logger,
		config:      cfg,
		wake:        make(chan struct{}, 1),
		reqs:        make(chan request, 8),
		hrLogParser: protocol.NewHRLogParser(),
		stepsParser: protocol.NewStepsParser(),
		events:      make(chan Event, 32),
	}
	m.publish(Snapshot{State: StateIdle})
	return m
}

// Run blocks until ctx is cancelled. It manages the persistent connection lifecycle.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.client.Enable(); err != nil {
		return err
	}

	for {
		cfg := m.config.Load()
		if cfg.PairedAddr == "" {
			if err := m.runIdle(ctx); err != nil {
				return err
			}
			continue
		}

		if err := m.runPaired(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			m.logger.Warn("paired lifecycle ended", "err", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Idle loop (no paired device)
// ---------------------------------------------------------------------------

func (m *Manager) runIdle(ctx context.Context) error {
	m.publish(Snapshot{State: StateIdle})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-m.wake:
			// Config changed (pair). Exit so outer loop re-reads config.
			return nil

		case req := <-m.reqs:
			switch r := req.(type) {
			case scanReq:
				results, err := m.doScan(ctx)
				select {
				case r.reply <- scanReply{results: results, err: err}:
				case <-ctx.Done():
					return ctx.Err()
				}
				if errors.Is(err, errConfigChanged) {
					return nil
				}

			case sendReq:
				select {
				case r.reply <- errors.New("not connected"):
				case <-ctx.Done():
					return ctx.Err()
				}

			default:
				m.replyWithError(req, errors.New("not connected"))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scan (interruptible by wake)
// ---------------------------------------------------------------------------

func (m *Manager) doScan(ctx context.Context) ([]ScanResult, error) {
	m.logger.Info("scan requested")
	prev := m.Snapshot()
	m.publish(Snapshot{
		State:      StateScanning,
		PairedAddr: prev.PairedAddr,
		PairedName: prev.PairedName,
	})
	defer func() {
		// Restore the durable pairing view after scans. Scans can now be used
		// while paired-but-disconnected to repair a stale OS/CoreBluetooth
		// peripheral address, so publishing idle unconditionally would make the
		// app briefly look unpaired.
		if m.config != nil {
			cfg := m.config.Load()
			if cfg.PairedAddr != "" {
				m.publish(Snapshot{State: StateDisconnected, PairedAddr: cfg.PairedAddr, PairedName: cfg.PairedName})
				return
			}
		}
		m.publish(Snapshot{State: StateIdle})
	}()

	scanCtx, scanCancel := context.WithTimeout(ctx, scanTimeout)
	defer scanCancel()

	type scanResult struct {
		results []ble.ScanResult
		err     error
	}

	ch := make(chan scanResult, 1)
	go func() {
		results, err := m.client.Scan(scanCtx)
		m.logger.Info("scan goroutine completed", "found", len(results), "err", err)
		ch <- scanResult{results: results, err: err}
	}()

	select {
	case r := <-ch:
		m.logger.Info("scan finished", "found", len(r.results), "err", r.err)
		return translateResults(r.results), r.err

	case <-m.wake:
		m.logger.Info("scan interrupted by config change")
		scanCancel()
		r := <-ch
		return translateResults(r.results), errConfigChanged

	case <-ctx.Done():
		m.logger.Info("scan interrupted by context cancellation")
		scanCancel()
		r := <-ch
		return translateResults(r.results), r.err
	}
}

func translateResults(in []ble.ScanResult) []ScanResult {
	out := make([]ScanResult, len(in))
	for i, r := range in {
		out[i] = ScanResult{
			Address: r.Address,
			Name:    r.Name,
			RSSI:    r.RSSI,
			HasUART: true,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Paired lifecycle loop
// ---------------------------------------------------------------------------

// runPaired manages the connect/reconnect cycle for a paired device.
// It returns when the device is unpaired or the context is cancelled.
func (m *Manager) runPaired(ctx context.Context) error {
	backoff := reconnectInitialInterval
	failures := 0
	requireAdvertisement := false
	scanMisses := 0

	for {
		// Check context before each reconnect attempt.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		cfg := m.config.Load()
		if cfg.PairedAddr == "" {
			return nil // unpaired, exit to idle
		}

		var repairExpected config.Config
		pendingRepair := false

		if requireAdvertisement {
			adv, err := m.waitForPairedAdvertisement(ctx, cfg)
			if err != nil {
				if errors.Is(err, errConfigChanged) {
					continue
				}
				return err
			}
			if adv.repaired {
				// Try the newly-advertised address, but only persist it after a
				// successful connection so a false/failed name match does not make the
				// stored pairing worse.
				repairExpected = cfg
				cfg = config.Config{PairedAddr: adv.address, PairedName: adv.name}
				pendingRepair = true
			}
			if !adv.found {
				scanMisses++
				if scanMisses < directConnectAfterScanMisses {
					m.logger.Info("paired watch not seen; delaying reconnect", "addr", cfg.PairedAddr, "retry_in", backoff)
					m.publish(Snapshot{State: StateDisconnected, PairedAddr: cfg.PairedAddr, PairedName: cfg.PairedName})
					if err := m.waitBeforeReconnect(ctx, backoff); err != nil {
						return err
					}
					backoff = nextReconnectDelay(backoff)
					continue
				}
				m.logger.Info("paired watch not seen; trying direct reconnect anyway", "addr", cfg.PairedAddr, "scan_misses", scanMisses)
				scanMisses = 0
			} else {
				scanMisses = 0
			}
		}

		m.publish(Snapshot{
			State:      StateConnecting,
			PairedAddr: cfg.PairedAddr,
			PairedName: cfg.PairedName,
		})

		gen := m.nextGen
		m.nextGen++

		link, err := m.client.Connect(ctx, cfg.PairedAddr, gen)
		if err != nil {
			failures++
			if failures == 1 || failures%12 == 0 {
				m.logger.Warn("connect failed; will retry", "err", err, "addr", cfg.PairedAddr, "retry_in", backoff, "failures", failures)
			} else {
				m.logger.Debug("connect failed; will retry", "err", err, "addr", cfg.PairedAddr, "retry_in", backoff, "failures", failures)
			}
			publishCfg := cfg
			if pendingRepair {
				publishCfg = repairExpected
			}
			m.publish(Snapshot{
				State:      StateDisconnected,
				PairedAddr: publishCfg.PairedAddr,
				PairedName: publishCfg.PairedName,
			})
			requireAdvertisement = true
			if err := m.waitBeforeReconnect(ctx, backoff); err != nil {
				return err
			}
			backoff = nextReconnectDelay(backoff)
			continue
		}

		if pendingRepair {
			repairedCfg, ok, err := m.repairPairedAddress(repairExpected, cfg.PairedAddr, cfg.PairedName)
			if err != nil {
				m.logger.Warn("failed to save repaired paired address", "old_addr", repairExpected.PairedAddr, "new_addr", cfg.PairedAddr, "err", err)
			} else if !ok {
				m.logger.Info("config changed while repaired address was connecting; restarting session")
				link.Close()
				continue
			} else {
				cfg = repairedCfg
			}
		}

		failures = 0
		backoff = reconnectInitialInterval
		requireAdvertisement = false
		m.logger.Info("session connected", "addr", cfg.PairedAddr, "v1_notif", link.V1NotifEnabled(), "v2_notif", link.V2NotifEnabled())

		err = m.runSession(ctx, link, cfg)
		link.Close()

		if errors.Is(err, context.Canceled) {
			return err
		}
		if err == nil {
			continue // likely config changed; loop will re-read config immediately
		}

		m.logger.Info("session ended; will reconnect", "err", err, "addr", cfg.PairedAddr, "retry_in", backoff)
		m.publish(Snapshot{State: StateDisconnected, PairedAddr: cfg.PairedAddr, PairedName: cfg.PairedName})
		requireAdvertisement = true
		if err := m.waitBeforeReconnect(ctx, backoff); err != nil {
			return err
		}
		backoff = nextReconnectDelay(backoff)
	}
}

func nextReconnectDelay(current time.Duration) time.Duration {
	if current <= 0 {
		return reconnectInitialInterval
	}
	next := current * 2
	if next > reconnectMaxInterval {
		return reconnectMaxInterval
	}
	return next
}

type pairedAdvertisement struct {
	found    bool
	address  string
	name     string
	repaired bool
}

func (m *Manager) waitForPairedAdvertisement(ctx context.Context, cfg config.Config) (pairedAdvertisement, error) {
	scanCtx, scanCancel := context.WithTimeout(ctx, scanTimeout)
	defer scanCancel()

	type scanResult struct {
		results []ble.ScanResult
		err     error
	}
	ch := make(chan scanResult, 1)
	go func() {
		results, err := m.client.Scan(scanCtx)
		ch <- scanResult{results: results, err: err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			m.logger.Debug("paired watch advertisement scan failed", "addr", cfg.PairedAddr, "err", r.err)
		}
		return m.matchPairedAdvertisement(cfg, r.results), nil

	case <-m.wake:
		scanCancel()
		<-ch
		return pairedAdvertisement{}, errConfigChanged

	case <-ctx.Done():
		scanCancel()
		<-ch
		return pairedAdvertisement{}, ctx.Err()
	}
}

func (m *Manager) matchPairedAdvertisement(cfg config.Config, results []ble.ScanResult) pairedAdvertisement {
	var nameMatches []ble.ScanResult
	for _, result := range results {
		if sameBLEAddress(result.Address, cfg.PairedAddr) {
			m.logger.Info("paired watch advertisement seen", "addr", cfg.PairedAddr, "rssi", result.RSSI)
			return pairedAdvertisement{found: true, address: result.Address, name: result.Name}
		}
		if pairedNameMatches(cfg.PairedName, result.Name) {
			nameMatches = append(nameMatches, result)
		}
	}

	if len(nameMatches) == 1 {
		match := nameMatches[0]
		m.logger.Warn("paired watch appears under a different BLE address; repairing app pairing", "old_addr", cfg.PairedAddr, "new_addr", match.Address, "name", match.Name)
		return pairedAdvertisement{found: true, address: match.Address, name: match.Name, repaired: true}
	}
	if len(nameMatches) > 1 {
		m.logger.Warn("paired watch name matched multiple advertisements; not repairing address", "addr", cfg.PairedAddr, "name", cfg.PairedName, "matches", len(nameMatches))
	}
	return pairedAdvertisement{}
}

func (m *Manager) repairPairedAddress(expected config.Config, addr, name string) (config.Config, bool, error) {
	latest := m.config.Load()
	if latest.PairedAddr != expected.PairedAddr || latest.PairedName != expected.PairedName {
		return latest, false, nil
	}
	if normalizedPairName(name) == "" {
		name = latest.PairedName
	}
	if err := m.config.Save(config.Config{PairedAddr: addr, PairedName: name}); err != nil {
		return latest, false, err
	}
	return config.Config{PairedAddr: addr, PairedName: name}, true, nil
}

func sameBLEAddress(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func pairedNameMatches(pairedName, advertisedName string) bool {
	paired := normalizedPairName(pairedName)
	advertised := normalizedPairName(advertisedName)
	return paired != "" && paired == advertised
}

func normalizedPairName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "", "(unnamed)", "unknown", "unnamed":
		return ""
	default:
		return name
	}
}

// waitBeforeReconnect waits for the reconnect interval, but remains
// responsive to context cancellation and config changes (wake).
// Operational requests are answered with errors.
func (m *Manager) waitBeforeReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-m.wake:
			return nil

		case req := <-m.reqs:
			switch r := req.(type) {
			case sendReq:
				select {
				case r.reply <- errors.New("not connected"):
				case <-ctx.Done():
					return ctx.Err()
				}

			case scanReq:
				// Allow scans while paired but disconnected. This lets the UI repair or
				// switch devices after the OS BLE stack has forgotten/renumbered the
				// peripheral, without requiring a separate unpair first.
				results, err := m.doScan(ctx)
				select {
				case r.reply <- scanReply{results: results, err: err}:
				case <-ctx.Done():
					return ctx.Err()
				}
				if errors.Is(err, errConfigChanged) {
					return nil
				}

			default:
				m.replyWithError(req, errors.New("not connected"))
			}

		case <-timer.C:
			return nil
		}
	}
}

// ---------------------------------------------------------------------------
// Connected session loop
// ---------------------------------------------------------------------------

type sessionLink interface {
	Write(context.Context, ble.Protocol, []byte) error
	Notifications() <-chan ble.Notification
	Gen() uint64
}

func (m *Manager) runSession(ctx context.Context, link sessionLink, cfg config.Config) error {
	data := DeviceData{LastSeen: time.Now()}

	m.publish(Snapshot{
		State:      StateConnected,
		Connected:  true,
		Data:       cloneData(data),
		PairedAddr: cfg.PairedAddr,
		PairedName: cfg.PairedName,
	})

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	// At most one transaction in flight; nil when idle.
	var current *txnReq
	v2reasm := protocol.NewV2Reassembler()

	// failCurrent sends an error result to the in-flight transaction
	// and clears it. Safe to call when current is nil.
	failCurrent := func(err error) {
		if current != nil {
			current.result <- txnResult{err: err}
			current = nil
		}
	}
	defer failCurrent(errors.New("session ended"))

	m.logger.Info("requesting initial battery level")
	if err := link.Write(ctx, ble.V1, protocol.BatteryPacket()); err != nil {
		m.logger.Warn("initial battery request failed", "err", err)
	}

	for {
		// Only arm the cancel case when a transaction is in flight.
		var curDone <-chan struct{}
		if current != nil {
			curDone = current.ctx.Done()
		}

		select {
		case <-ctx.Done():
			m.logger.Info("session shutting down", "reason", ctx.Err())
			return ctx.Err()

		case <-m.wake:
			m.logger.Info("config changed, restarting session")
			return nil

		case <-curDone:
			// Caller gave up (timeout or cancel). Clear so a late
			// response can't be misrouted to the next same-cmd request.
			m.logger.Debug("transaction cancelled", "cmd", fmt.Sprintf("0x%02X", current.cmd), "err", current.ctx.Err())
			failCurrent(current.ctx.Err())

		case req := <-m.reqs:
			switch r := req.(type) {
			case sendReq:
				m.logger.Debug("sending packet", "protocol", r.target, "len", len(r.packet))
				err := link.Write(ctx, r.target, r.packet)
				if err != nil {
					m.logger.Warn("write failed", "protocol", r.target, "err", err)
				}
				select {
				case r.reply <- err:
				case <-ctx.Done():
					return ctx.Err()
				}

			case txnReq:
				if current != nil {
					if err := current.ctx.Err(); err != nil {
						m.logger.Debug("clearing cancelled transaction before new request", "cmd", fmt.Sprintf("0x%02X", current.cmd), "err", err)
						failCurrent(err)
					} else {
						// Shouldn't happen: txnMu serialises callers.
						// Fail the new request rather than clobber the old one.
						r.result <- txnResult{err: errors.New("transaction already in flight")}
						continue
					}
				}
				if err := r.ctx.Err(); err != nil {
					r.result <- txnResult{err: err}
					continue
				}
				m.logger.Debug("sending txn packet", "protocol", r.protocol, "cmd", fmt.Sprintf("0x%02X", r.cmd), "len", len(r.packet))
				m.resetParserFor(r.protocol, r.cmd)
				err := link.Write(r.ctx, r.protocol, r.packet)
				if err != nil {
					m.logger.Warn("txn write failed", "protocol", r.protocol, "err", err)
					r.result <- txnResult{err: err}
					continue
				}
				current = &r

			case scanReq:
				select {
				case r.reply <- scanReply{err: errors.New("cannot scan while connected")}:
				case <-ctx.Done():
					return ctx.Err()
				}

			default:
				m.replyWithError(req, errors.New("not connected"))
			}

		case n := <-link.Notifications():
			if n.Gen != link.Gen() {
				continue
			}
			if current != nil {
				if err := current.ctx.Err(); err != nil {
					m.logger.Debug("clearing cancelled transaction before dispatch", "cmd", fmt.Sprintf("0x%02X", current.cmd), "err", err)
					failCurrent(err)
				}
			}
			switch n.Protocol {
			case ble.V1:
				pkt := n.Data
				if current != nil && current.protocol == ble.V1 && len(pkt) > 0 && current.cmd == pkt[0] {
					done, result, err := current.feed(pkt)
					if done || err != nil {
						current.result <- txnResult{data: result, err: err}
						current = nil
					}
				} else {
					m.handleV1(&data, pkt)
				}

			case ble.V2:
				frames := v2reasm.Feed(n.Data)
				for _, frame := range frames {
					if len(frame) < 2 {
						continue
					}
					cmd := frame[1]
					if current != nil && current.protocol == ble.V2 && current.cmd == cmd {
						done, result, err := current.feed(frame)
						if done || err != nil {
							current.result <- txnResult{data: result, err: err}
							current = nil
						}
					} else {
						m.handleV2(&data, frame, cfg.PairedAddr)
					}
				}
			}
			m.publishData(cfg, data)

		case <-heartbeat.C:
			// Any notification proves the link is alive. Avoid extra writes while the
			// watch is actively streaming live activity; on macOS/TinyGo this can
			// otherwise hit "write without response timed out waiting for buffer space".
			if time.Since(data.LastSeen) < heartbeatInterval {
				m.logger.Debug("skipping heartbeat; recent notification", "last_seen", data.LastSeen)
				continue
			}
			if err := link.Write(ctx, ble.V1, protocol.BatteryPacket()); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
		}
	}
} // Pair / Unpair: direct config writes + wake signal
// ---------------------------------------------------------------------------

// SetPairedDevice saves the paired device configuration and signals the
// owner loop to connect. It returns immediately without waiting for
// the BLE connection to be established.
func (m *Manager) SetPairedDevice(_ context.Context, addr, name string) error {
	m.logger.Info("set paired device", "addr", addr, "name", name)
	if err := m.config.Save(config.Config{PairedAddr: addr, PairedName: name}); err != nil {
		return err
	}
	m.wakeup()
	return nil
}

// Pair saves the paired device configuration, signals the owner loop,
// and waits until the device is connected or the context is cancelled.
func (m *Manager) Pair(ctx context.Context, addr, name string) error {
	m.logger.Info("pairing", "addr", addr, "name", name)
	if err := m.SetPairedDevice(ctx, addr, name); err != nil {
		return err
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		snap := m.Snapshot()
		if snap.PairedAddr == addr && snap.Connected {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Unpair clears the paired device configuration and signals the owner loop
// to disconnect. It returns immediately.
func (m *Manager) Unpair(_ context.Context) error {
	m.logger.Info("unpairing")
	if err := m.config.Save(config.Config{}); err != nil {
		return err
	}
	m.wakeup()
	return nil
}

// wakeup signals the owner loop to re-read config. It is non-blocking;
// coalesced wakeups are safe because the loop always reloads config on wake.
func (m *Manager) wakeup() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// ---------------------------------------------------------------------------
// Snapshot publishing / reading
// ---------------------------------------------------------------------------

func (m *Manager) publish(s Snapshot) {
	m.snapshot.Store(s)
}

func (m *Manager) publishData(cfg config.Config, data DeviceData) {
	m.publish(Snapshot{
		State:      StateConnected,
		Connected:  true,
		Data:       cloneData(data),
		PairedAddr: cfg.PairedAddr,
		PairedName: cfg.PairedName,
	})
}

// Snapshot returns the current immutable state snapshot.
func (m *Manager) Snapshot() Snapshot {
	v := m.snapshot.Load()
	if v == nil {
		return Snapshot{State: StateIdle}
	}
	s := v.(Snapshot)
	s.Data = cloneData(s.Data)
	return s
}

// State returns the current durable state.
func (m *Manager) State() State {
	return m.Snapshot().State
}

// IsConnected reports whether a BLE session is currently active.
func (m *Manager) IsConnected() bool {
	return m.Snapshot().Connected
}

// Events returns unsolicited raw device events. Consumers should keep reading;
// events are dropped if the channel buffer fills.
func (m *Manager) Events() <-chan Event {
	return m.events
}

// PairedDevice returns the persisted pair information.
func (m *Manager) PairedDevice() (addr, name string) {
	cfg := m.config.Load()
	return cfg.PairedAddr, cfg.PairedName
}
