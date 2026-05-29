package app

import (
	"context"
	"log/slog"
	"time"

	"smartwatch/internal/device"
	"smartwatch/internal/sleep"
	"smartwatch/protocol"
)

// SleepRecorder is implemented by the sleep tracker. It consumes sleep-domain
// observations, not watch-protocol structs.
type SleepRecorder interface {
	RecordRealtime(ctx context.Context, deviceAddr string, sessions []sleep.ObservedSession, observedAt time.Time) error
}

// RunDeviceEvents routes generic device events into higher-level application
// services. Device stays transport-focused, sleep stays domain-focused, and this
// bridge owns the event-to-protocol-parser glue.
func RunDeviceEvents(ctx context.Context, events <-chan device.Event, sleepRecorder SleepRecorder, logger *slog.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			handleDeviceEvent(ctx, ev, sleepRecorder, logger)
		}
	}
}

func handleDeviceEvent(ctx context.Context, ev device.Event, sleepRecorder SleepRecorder, logger *slog.Logger) {
	if ev.Type != device.EventV2Frame || ev.V2Cmd != protocol.V2Sleep {
		return
	}

	sessions := protocol.ParseSleepData(ev.Data)
	if sessions == nil {
		logger.Warn("realtime sleep frame parse failed", "device_addr", ev.DeviceAddr, "len", len(ev.Data))
		return
	}

	observed := convertSleepSessions(sessions)
	logger.Info("realtime sleep frame received", "device_addr", ev.DeviceAddr, "sessions", len(observed), "len", len(ev.Data))
	if err := sleepRecorder.RecordRealtime(ctx, ev.DeviceAddr, observed, ev.ReceivedAt); err != nil {
		logger.Warn("realtime sleep persist failed", "device_addr", ev.DeviceAddr, "err", err)
	}
}
