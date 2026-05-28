package ble

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"smartwatch/protocol"
	"tinygo.org/x/bluetooth"
)

const (
	maxChunkSize    = 20
	connTimeout     = 30 * time.Second
	notifBufferSize = 128
)

// Link is an established BLE connection to a smartwatch.
// It is single-owner: only the Manager goroutine should use a Link.
type Link struct {
	device   bluetooth.Device
	gen      uint64
	v1rx     bluetooth.DeviceCharacteristic // host → device
	v1tx     bluetooth.DeviceCharacteristic // device → host
	v2cmd    bluetooth.DeviceCharacteristic // host → device
	v2notify bluetooth.DeviceCharacteristic // device → host

	v1NotifEnabled bool
	v2NotifEnabled bool
	notifs         chan Notification
	logger         *slog.Logger
}

// Connect establishes a BLE connection, discovers required characteristics,
// and attempts to enable notifications. Both the UART and V2 services are
// discovered in a single DiscoverServices call to avoid TinyGo's Darwin
// backend clearing the internal service map between calls.
func (c *Client) Connect(ctx context.Context, addr string, gen uint64) (*Link, error) {
	c.logger.Info("connecting", "addr", addr)

	// Bail out immediately if context is already cancelled (e.g. shutdown).
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var btAddr bluetooth.Address
	btAddr.Set(addr)

	dev, err := c.adapter.Connect(btAddr, bluetooth.ConnectionParams{
		ConnectionTimeout: bluetooth.NewDuration(connTimeout),
	})
	if err != nil {
		c.logger.Error("connect failed", "addr", addr, "err", err)
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}

	c.logger.Info("connected", "addr", addr)

	ok := false
	defer func() {
		if !ok {
			_ = dev.Disconnect()
		}
	}()

	// Discover both services in one call. TinyGo's Darwin backend clears
	// the internal service map on each DiscoverServices call, so calling
	// it twice would wipe the first service's map entry, breaking V1
	// notification dispatch.
	chars, err := discoverLinkChars(dev)
	if err != nil {
		return nil, err
	}

	l := &Link{
		device:   dev,
		gen:      gen,
		v1rx:     chars.v1rx,
		v1tx:     chars.v1tx,
		v2cmd:    chars.v2cmd,
		v2notify: chars.v2notify,
		notifs:   make(chan Notification, notifBufferSize),
		logger:   c.logger,
	}

	// Enable V1 first — it's the command-response path used for battery,
	// time, find-device, etc.
	c.logger.Info("enabling V1 notifications")
	if err := l.v1tx.EnableNotifications(func(data []byte) {
		l.enqueue(V1, data)
	}); err != nil {
		c.logger.Warn("v1 notifications not available (non-fatal)", "err", err)
	} else {
		l.v1NotifEnabled = true
		c.logger.Info("v1 notifications enabled")
	}

	c.logger.Info("enabling V2 notifications")
	if err := l.v2notify.EnableNotifications(func(data []byte) {
		l.enqueue(V2, data)
	}); err != nil {
		c.logger.Warn("v2 notifications not available (non-fatal)", "err", err)
	} else {
		l.v2NotifEnabled = true
		c.logger.Info("v2 notifications enabled")
	}

	ok = true
	return l, nil
}

// V1NotifEnabled reports whether V1 (UART TX) notifications were enabled.
func (l *Link) V1NotifEnabled() bool { return l.v1NotifEnabled }

// V2NotifEnabled reports whether V2 (Big Data) notifications were enabled.
func (l *Link) V2NotifEnabled() bool { return l.v2NotifEnabled }

// Write sends a packet over the specified protocol, chunking as needed.
func (l *Link) Write(ctx context.Context, p Protocol, packet []byte) error {
	switch p {
	case V1:
		return chunkedWrite(ctx, l.v1rx, packet)
	case V2:
		return chunkedWrite(ctx, l.v2cmd, packet)
	default:
		return fmt.Errorf("unknown protocol %d", p)
	}
}

// Notifications returns the channel of incoming BLE notifications.
func (l *Link) Notifications() <-chan Notification {
	return l.notifs
}

// Gen returns the generation number of this link.
func (l *Link) Gen() uint64 { return l.gen }

// Close disconnects the BLE device. Buffered notifications are discarded.
func (l *Link) Close() {
	_ = l.device.Disconnect()
}

func (l *Link) enqueue(p Protocol, data []byte) {
	select {
	case l.notifs <- Notification{Gen: l.gen, Protocol: p, Data: bytes.Clone(data)}:
	default:
		l.logger.Warn("dropping notification; queue full", "protocol", p)
	}
}

// ---------------------------------------------------------------------------
// Service + characteristic discovery (single DiscoverServices call)
// ---------------------------------------------------------------------------

// linkChars holds the four BLE characteristics needed for smartwatch
// communication.
type linkChars struct {
	v1rx     bluetooth.DeviceCharacteristic
	v1tx     bluetooth.DeviceCharacteristic
	v2cmd    bluetooth.DeviceCharacteristic
	v2notify bluetooth.DeviceCharacteristic
}

// discoverLinkChars discovers both the UART and V2 services in a single
// DiscoverServices call, then discovers their characteristics.
func discoverLinkChars(dev bluetooth.Device) (linkChars, error) {
	uartSvcUUID, err := bluetooth.ParseUUID(protocol.UARTService)
	if err != nil {
		return linkChars{}, fmt.Errorf("parse UART service uuid: %w", err)
	}
	v2SvcUUID, err := bluetooth.ParseUUID(protocol.ServiceV2)
	if err != nil {
		return linkChars{}, fmt.Errorf("parse V2 service uuid: %w", err)
	}

	services, err := dev.DiscoverServices([]bluetooth.UUID{uartSvcUUID, v2SvcUUID})
	if err != nil {
		return linkChars{}, fmt.Errorf("discover services: %w", err)
	}
	if len(services) < 2 {
		return linkChars{}, fmt.Errorf("expected 2 services, found %d", len(services))
	}

	byUUID := make(map[string]bluetooth.DeviceService, len(services))
	for _, svc := range services {
		byUUID[uuidKey(svc.UUID().String())] = svc
	}

	uartSvc, ok := byUUID[uuidKey(protocol.UARTService)]
	if !ok {
		return linkChars{}, fmt.Errorf("UART service %s not found", protocol.UARTService)
	}
	v2Svc, ok := byUUID[uuidKey(protocol.ServiceV2)]
	if !ok {
		return linkChars{}, fmt.Errorf("V2 service %s not found", protocol.ServiceV2)
	}

	v1, err := discoverChars(uartSvc, protocol.UARTRX, protocol.UARTTX)
	if err != nil {
		return linkChars{}, fmt.Errorf("discover UART characteristics: %w", err)
	}
	v2, err := discoverChars(v2Svc, protocol.CharCommandV2, protocol.CharNotifyV2)
	if err != nil {
		return linkChars{}, fmt.Errorf("discover V2 characteristics: %w", err)
	}

	return linkChars{
		v1rx:     v1[uuidKey(protocol.UARTRX)],
		v1tx:     v1[uuidKey(protocol.UARTTX)],
		v2cmd:    v2[uuidKey(protocol.CharCommandV2)],
		v2notify: v2[uuidKey(protocol.CharNotifyV2)],
	}, nil
}

// discoverChars discovers specific characteristics within a service.
func discoverChars(svc bluetooth.DeviceService, charUUIDs ...string) (map[string]bluetooth.DeviceCharacteristic, error) {
	parsed := make([]bluetooth.UUID, 0, len(charUUIDs))
	for _, raw := range charUUIDs {
		uuid, err := bluetooth.ParseUUID(raw)
		if err != nil {
			return nil, fmt.Errorf("parse characteristic uuid %s: %w", raw, err)
		}
		parsed = append(parsed, uuid)
	}

	chars, err := svc.DiscoverCharacteristics(parsed)
	if err != nil {
		return nil, fmt.Errorf("discover characteristics: %w", err)
	}

	found := make(map[string]bluetooth.DeviceCharacteristic, len(chars))
	for _, ch := range chars {
		found[uuidKey(ch.UUID().String())] = ch
	}

	for _, want := range charUUIDs {
		if _, ok := found[uuidKey(want)]; !ok {
			return nil, fmt.Errorf("characteristic %s not found", want)
		}
	}

	return found, nil
}

// uuidKey normalises a UUID string for map lookups (case-insensitive).
func uuidKey(s string) string {
	return strings.ToLower(s)
}

func chunkedWrite(ctx context.Context, char bluetooth.DeviceCharacteristic, data []byte) error {
	for len(data) > 0 {
		n := min(len(data), maxChunkSize)
		chunk := data[:n]
		data = data[n:]

		if _, err := char.WriteWithoutResponse(chunk); err != nil {
			return fmt.Errorf("ble write: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}