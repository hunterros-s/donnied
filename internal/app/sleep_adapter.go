package app

import (
	"context"

	"smartwatch/internal/sleep"
	"smartwatch/protocol"
)

// ProtocolSleepDevice is the low-level device shape that returns watch-protocol
// sleep structs. SleepDevice adapts it to sleep.Device so internal/sleep does
// not depend on the protocol package.
type ProtocolSleepDevice interface {
	IsConnected() bool
	GetSleep(ctx context.Context) ([]protocol.SleepSession, error)
	PairedDevice() (addr, name string)
}

type SleepDevice struct {
	device ProtocolSleepDevice
}

func NewSleepDevice(device ProtocolSleepDevice) *SleepDevice {
	return &SleepDevice{device: device}
}

func (d *SleepDevice) IsConnected() bool {
	return d.device.IsConnected()
}

func (d *SleepDevice) PairedDevice() (addr, name string) {
	return d.device.PairedDevice()
}

func (d *SleepDevice) GetSleep(ctx context.Context) ([]sleep.ObservedSession, error) {
	sessions, err := d.device.GetSleep(ctx)
	if err != nil {
		return nil, err
	}
	return convertSleepSessions(sessions), nil
}

func convertSleepSessions(in []protocol.SleepSession) []sleep.ObservedSession {
	out := make([]sleep.ObservedSession, 0, len(in))
	for _, s := range in {
		obs := sleep.ObservedSession{
			Start:  s.Start,
			End:    s.End,
			Stages: make([]sleep.ObservedStage, 0, len(s.Stages)),
		}
		for _, st := range s.Stages {
			obs.Stages = append(obs.Stages, sleep.ObservedStage{
				StageRaw: int(st.Stage),
				Minutes:  st.Minutes,
			})
		}
		out = append(out, obs)
	}
	return out
}
