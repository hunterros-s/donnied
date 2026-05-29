package sleep

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	DefaultPollInterval   = 5 * time.Minute
	DefaultStaleAfter     = 15 * time.Minute
	connectionCheckPeriod = 1 * time.Second
	syncTimeout           = 45 * time.Second
)

// Device is the subset of the watch manager needed by the sleep tracker.
type Device interface {
	IsConnected() bool
	GetSleep(ctx context.Context) ([]ObservedSession, error)
	PairedDevice() (addr, name string)
}

// Tracker periodically syncs sleep data and persists normalized sessions.
type Tracker struct {
	device       Device
	store        *Store
	logger       *slog.Logger
	pollInterval time.Duration
	staleAfter   time.Duration
}

func NewTracker(device Device, store *Store, logger *slog.Logger) *Tracker {
	return &Tracker{
		device:       device,
		store:        store,
		logger:       logger,
		pollInterval: DefaultPollInterval,
		staleAfter:   DefaultStaleAfter,
	}
}

// Run starts the persistent sleep sync loop. Sync errors are logged and stored
// as metadata, but do not stop the daemon. The tracker waits for a connected
// watch before syncing, then syncs immediately on connection/reconnection and
// every poll interval while connected.
func (t *Tracker) Run(ctx context.Context) error {
	t.logger.Info("sleep tracker started", "interval", t.pollInterval, "stale_after", t.staleAfter)
	defer t.logger.Info("sleep tracker stopped")

	pollTicker := time.NewTicker(t.pollInterval)
	defer pollTicker.Stop()
	connTicker := time.NewTicker(connectionCheckPeriod)
	defer connTicker.Stop()

	connected := t.device.IsConnected()
	if connected {
		t.logger.Info("watch already connected; syncing sleep")
		t.syncAndLog(ctx, SourcePoll)
	} else {
		t.logger.Debug("sleep tracker waiting for watch connection")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-connTicker.C:
			nowConnected := t.device.IsConnected()
			if nowConnected && !connected {
				t.logger.Info("watch connected; syncing sleep")
				t.syncAndLog(ctx, SourcePoll)
			} else if !nowConnected && connected {
				t.logger.Info("watch disconnected; pausing sleep sync")
			}
			connected = nowConnected

		case <-pollTicker.C:
			if !t.device.IsConnected() {
				t.logger.Debug("sleep sync skipped; watch not connected")
				connected = false
				continue
			}
			connected = true
			t.syncAndLog(ctx, SourcePoll)
		}
	}
}

// SyncNow performs a foreground poll, used by HTTP/CLI manual sync.
func (t *Tracker) SyncNow(ctx context.Context) error {
	return t.sync(ctx, SourcePoll)
}

// RecordRealtime persists already-parsed realtime sleep data from the watch.
func (t *Tracker) RecordRealtime(ctx context.Context, deviceAddr string, sessions []ObservedSession, observedAt time.Time) error {
	if deviceAddr == "" {
		deviceAddr, _ = t.device.PairedDevice()
	}
	if deviceAddr == "" {
		deviceAddr = "unknown"
	}
	normalized := NormalizeSessions(deviceAddr, SourceRealtime, observedAt, sessions)
	t.logger.Info("persisting realtime sleep update", "sessions", len(sessions), "normalized", len(normalized), "device_addr", deviceAddr)
	return t.store.SaveObservation(ctx, deviceAddr, SourceRealtime, observedAt, sessions, normalized)
}

func (t *Tracker) Status(ctx context.Context) (*Status, error) {
	return t.store.Status(ctx, time.Now(), t.staleAfter)
}

func (t *Tracker) Sessions(ctx context.Context, from, to time.Time) ([]Session, error) {
	return t.store.Sessions(ctx, from, to)
}

func (t *Tracker) syncAndLog(ctx context.Context, source Source) {
	if err := t.sync(ctx, source); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		t.logger.Warn("sleep sync failed", "source", source, "err", err)
	}
}

func (t *Tracker) sync(ctx context.Context, source Source) error {
	observedAt := time.Now()
	if !t.device.IsConnected() {
		err := errors.New("watch not connected")
		_ = t.store.RecordSyncError(ctx, observedAt, err)
		return err
	}

	addr, _ := t.device.PairedDevice()
	if addr == "" {
		addr = "unknown"
	}

	t.logger.Info("sleep sync starting", "source", source, "device_addr", addr)
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	sessions, err := t.device.GetSleep(syncCtx)
	if err != nil {
		_ = t.store.RecordSyncError(context.Background(), observedAt, err)
		return err
	}
	normalized := NormalizeSessions(addr, source, observedAt, sessions)
	if err := t.store.SaveObservation(ctx, addr, source, observedAt, sessions, normalized); err != nil {
		_ = t.store.RecordSyncError(context.Background(), observedAt, err)
		return err
	}
	t.logger.Info("sleep sync stored", "source", source, "sessions", len(sessions), "normalized", len(normalized), "device_addr", addr)
	return nil
}
