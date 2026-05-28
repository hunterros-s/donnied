package service

import (
	"context"
	"errors"
	"os"
	"time"

	"smartwatch/internal/device"
	"smartwatch/protocol"
)

type Info struct {
	PID             int       `json:"pid"`
	StartTime       time.Time `json:"start_time"`
	Uptime          string    `json:"uptime"`
	DeviceConnected bool      `json:"device_connected"`
}

type BatteryInfo struct {
	Level    int  `json:"level"`
	Charging bool `json:"charging"`
}

// ScanResult mirrors device.ScanResult for clean server boundaries.
type ScanResult struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	RSSI    int16  `json:"rssi"`
	HasUART bool   `json:"has_uart"`
}

// DeviceStatus is the full state-machine snapshot.
type DeviceStatus struct {
	State      string       `json:"state"`
	PairedAddr string       `json:"paired_addr"`
	PairedName string       `json:"paired_name"`
	Connected  bool         `json:"connected"`
	Battery    *BatteryInfo `json:"battery,omitempty"`
}

// Device is the subset of device.Manager that service needs.
type Device interface {
	IsConnected() bool
	Battery() *protocol.BatteryInfo
	SetTime(ctx context.Context, t time.Time) error
	FindDevice(ctx context.Context) error
	Scan(ctx context.Context) ([]device.ScanResult, error)
	SetPairedDevice(ctx context.Context, addr, name string) error
	Unpair(ctx context.Context) error
	State() device.State
	PairedDevice() (addr, name string)
	Snapshot() device.Snapshot
	GetBattery(ctx context.Context) (*protocol.BatteryInfo, error)
	GetHRLog(ctx context.Context, day protocol.Day) ([]protocol.HRSample, error)
	GetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error)
	GetSleep(ctx context.Context) ([]protocol.SleepSession, error)
	GetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error)
	ReadRealtime(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error)
	StartRealtime(ctx context.Context, rt protocol.RtType) error
	StopRealtime(ctx context.Context, rt protocol.RtType) error
}

type Service struct {
	startTime time.Time
	shutdown  func()
	device    Device
}

func New(device Device, shutdown func()) *Service {
	return &Service{
		startTime: time.Now(),
		shutdown:  shutdown,
		device:    device,
	}
}

func (s *Service) Health(_ context.Context) error {
	return nil
}

func (s *Service) Info(_ context.Context) (*Info, error) {
	return &Info{
		PID:             os.Getpid(),
		StartTime:       s.startTime,
		Uptime:          time.Since(s.startTime).Round(time.Second).String(),
		DeviceConnected: s.device.IsConnected(),
	}, nil
}

func (s *Service) TriggerShutdown(_ context.Context) error {
	if s.shutdown != nil {
		s.shutdown()
	}
	return nil
}

func (s *Service) DeviceConnected(_ context.Context) bool {
	return s.device.IsConnected()
}

func (s *Service) DeviceBattery(_ context.Context) (*BatteryInfo, error) {
	info := s.device.Battery()
	if info == nil {
		return nil, errors.New("no battery data available")
	}
	return &BatteryInfo{
		Level:    info.Level,
		Charging: info.Charging,
	}, nil
}

func (s *Service) DeviceSetTime(ctx context.Context, t time.Time) error {
	return s.device.SetTime(ctx, t)
}

func (s *Service) DeviceFind(ctx context.Context) error {
	return s.device.FindDevice(ctx)
}

func (s *Service) DeviceScan(ctx context.Context) ([]ScanResult, error) {
	results, err := s.device.Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScanResult, len(results))
	for i, r := range results {
		out[i] = ScanResult(r)
	}
	return out, nil
}

func (s *Service) DeviceSetPairedDevice(ctx context.Context, addr, name string) error {
	return s.device.SetPairedDevice(ctx, addr, name)
}

func (s *Service) DeviceUnpair(ctx context.Context) error {
	return s.device.Unpair(ctx)
}

func (s *Service) DeviceStatus(_ context.Context) (*DeviceStatus, error) {
	snap := s.device.Snapshot()

	var bi *BatteryInfo
	if snap.Data.Battery != nil {
		bi = &BatteryInfo{Level: snap.Data.Battery.Level, Charging: snap.Data.Battery.Charging}
	}

	return &DeviceStatus{
		State:      snap.State.String(),
		PairedAddr: snap.PairedAddr,
		PairedName: snap.PairedName,
		Connected:  snap.Connected,
		Battery:    bi,
	}, nil
}

func (s *Service) DeviceGetHRLog(ctx context.Context, day protocol.Day) ([]protocol.HRSample, error) {
	return s.device.GetHRLog(ctx, day)
}

func (s *Service) DeviceGetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error) {
	return s.device.GetSteps(ctx, dayOffset)
}

func (s *Service) DeviceGetSleep(ctx context.Context) ([]protocol.SleepSession, error) {
	return s.device.GetSleep(ctx)
}

func (s *Service) DeviceGetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error) {
	return s.device.GetSpO2(ctx, daysAgo)
}

func (s *Service) DeviceRealtimeRead(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error) {
	return s.device.ReadRealtime(ctx, rt)
}

func (s *Service) DeviceRealtimeStart(ctx context.Context, rt protocol.RtType) error {
	return s.device.StartRealtime(ctx, rt)
}

func (s *Service) DeviceRealtimeStop(ctx context.Context, rt protocol.RtType) error {
	return s.device.StopRealtime(ctx, rt)
}
