package device

import (
	"context"
	"time"

	"smartwatch/protocol"
)

// SetTime sends a time-sync command to the paired device.
func (m *Manager) SetTime(ctx context.Context, t time.Time) error {
	m.logger.Info("setting time", "time", t.Format(time.RFC3339))
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	return m.SendV1(ctx, protocol.SetTimePacket(t))
}

// FindDevice sends a find-device (vibrate) command to the paired device.
func (m *Manager) FindDevice(ctx context.Context) error {
	m.logger.Info("finding device")
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	return m.SendV1(ctx, protocol.FindDevicePacket())
}