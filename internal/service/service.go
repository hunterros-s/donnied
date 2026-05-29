package service

import (
	"context"
	"errors"
	"os"
	"time"

	"smartwatch/internal/api"
	"smartwatch/internal/device"
	"smartwatch/internal/sleep"
	"smartwatch/protocol"
)

// Device is the subset of device.Manager that service needs.
type Device interface {
	IsConnected() bool
	SetTime(ctx context.Context, t time.Time) error
	FindDevice(ctx context.Context) error
	Scan(ctx context.Context) ([]device.ScanResult, error)
	SetPairedDevice(ctx context.Context, addr, name string) error
	Unpair(ctx context.Context) error
	Snapshot() device.Snapshot
	GetHRLog(ctx context.Context, day protocol.Day) ([]protocol.HRSample, error)
	GetSteps(ctx context.Context, dayOffset int) ([]protocol.SportDetail, error)
	GetSleep(ctx context.Context) ([]protocol.SleepSession, error)
	GetSpO2(ctx context.Context, daysAgo int) ([]protocol.SpO2Day, error)
	ReadRealtime(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error)
	StartRealtime(ctx context.Context, rt protocol.RtType) error
	StopRealtime(ctx context.Context, rt protocol.RtType) error
}

type SleepTracker interface {
	Status(ctx context.Context) (*sleep.Status, error)
	Sessions(ctx context.Context, from, to time.Time) ([]sleep.Session, error)
	SyncNow(ctx context.Context) error
}

type Service struct {
	startTime time.Time
	shutdown  func()
	device    Device
	sleep     SleepTracker
}

func New(device Device, sleepTracker SleepTracker, shutdown func()) *Service {
	return &Service{
		startTime: time.Now(),
		shutdown:  shutdown,
		device:    device,
		sleep:     sleepTracker,
	}
}

func (s *Service) Health(_ context.Context) error { return nil }

func (s *Service) TriggerShutdown(_ context.Context) error {
	if s.shutdown != nil {
		s.shutdown()
	}
	return nil
}

func (s *Service) State(ctx context.Context, opts api.StateOptions) (*api.AppState, error) {
	if opts.Day.IsZero() {
		opts.Day = time.Now()
	}
	selectedDay := localDay(opts.Day)

	state := &api.AppState{
		Process: api.ProcessState{
			PID:       os.Getpid(),
			StartTime: s.startTime,
			Uptime:    time.Since(s.startTime).Round(time.Second).String(),
		},
		Device:  s.deviceState(),
		Sleep:   s.sleepState(ctx),
		History: api.HistoryState{SelectedDay: selectedDay.Format("2006-01-02")},
	}

	if opts.IncludeHistory {
		s.populateHistory(ctx, &state.History, selectedDay, opts)
	}

	return state, nil
}

func (s *Service) DeviceScan(ctx context.Context) ([]api.ScanResult, error) {
	results, err := s.device.Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ScanResult, len(results))
	for i, r := range results {
		out[i] = api.ScanResult(r)
	}
	return out, nil
}

func (s *Service) DeviceSetPairedDevice(ctx context.Context, addr, name string) error {
	return s.device.SetPairedDevice(ctx, addr, name)
}

func (s *Service) DeviceUnpair(ctx context.Context) error { return s.device.Unpair(ctx) }

func (s *Service) DeviceSetTime(ctx context.Context, t time.Time) error {
	return s.device.SetTime(ctx, t)
}

func (s *Service) DeviceFind(ctx context.Context) error { return s.device.FindDevice(ctx) }

func (s *Service) DeviceRealtimeRead(ctx context.Context, rt protocol.RtType) (*protocol.RtReading, error) {
	return s.device.ReadRealtime(ctx, rt)
}

func (s *Service) DeviceRealtimeStart(ctx context.Context, rt protocol.RtType) error {
	return s.device.StartRealtime(ctx, rt)
}

func (s *Service) DeviceRealtimeStop(ctx context.Context, rt protocol.RtType) error {
	return s.device.StopRealtime(ctx, rt)
}

func (s *Service) SleepSync(ctx context.Context) error {
	if s.sleep == nil {
		return errors.New("sleep tracker not available")
	}
	return s.sleep.SyncNow(ctx)
}

func (s *Service) deviceState() api.DeviceState {
	snap := s.device.Snapshot()

	var lastSeen *time.Time
	if !snap.Data.LastSeen.IsZero() {
		v := snap.Data.LastSeen
		lastSeen = &v
	}

	return api.DeviceState{
		State:        snap.State.String(),
		Connected:    snap.Connected,
		PairedAddr:   snap.PairedAddr,
		PairedName:   snap.PairedName,
		LastSeen:     lastSeen,
		Battery:      snap.Data.Battery,
		HeartRate:    snap.Data.HeartRate,
		SpO2:         snap.Data.SpO2,
		Steps:        snap.Data.Steps,
		Calories:     snap.Data.Calories,
		Distance:     snap.Data.Distance,
		LiveActivity: snap.Data.LiveActivity,
	}
}

func (s *Service) sleepState(ctx context.Context) api.SleepState {
	if s.sleep == nil {
		return api.SleepState{State: string(sleep.StateUnknown), Error: "sleep tracker not available"}
	}
	status, err := s.sleep.Status(ctx)
	if err != nil {
		return api.SleepState{State: string(sleep.StateUnknown), Error: err.Error()}
	}
	return api.SleepState{
		State:    string(status.State),
		Stage:    status.Stage,
		StageRaw: status.StageRaw,
		Since:    status.Since,
		LastSync: status.LastSync,
		Source:   string(status.Source),
		Stale:    status.Stale,
	}
}

func (s *Service) populateHistory(ctx context.Context, h *api.HistoryState, selectedDay time.Time, opts api.StateOptions) {
	from, to := opts.From, opts.To
	if from.IsZero() && to.IsZero() {
		from = selectedDay
		to = selectedDay.AddDate(0, 0, 1)
	}

	if s.sleep == nil {
		addHistoryError(h, "sleep_sessions", "sleep tracker not available")
	} else if sessions, err := s.sleep.Sessions(ctx, from, to); err != nil {
		addHistoryError(h, "sleep_sessions", err.Error())
	} else {
		h.SleepSessions = sessions
	}

	if !opts.IncludeWatchHistory {
		return
	}
	if !s.device.IsConnected() {
		addHistoryError(h, "watch_history", "watch not connected")
		return
	}

	day := protocol.Day{Year: selectedDay.Year(), Month: selectedDay.Month(), Day: selectedDay.Day()}
	offset := day.DayOffset()

	if wantsWatchHistory(opts, "hr") {
		if samples, err := s.device.GetHRLog(ctx, day); err != nil {
			addHistoryError(h, "hr_samples", err.Error())
		} else {
			h.HRSamples = samples
		}
	}

	if wantsWatchHistory(opts, "steps") {
		if details, err := s.device.GetSteps(ctx, offset); err != nil {
			addHistoryError(h, "step_details", err.Error())
		} else {
			h.StepDetails = details
		}
	}

	if wantsWatchHistory(opts, "device_sleep") || wantsWatchHistory(opts, "sleep") {
		if sessions, err := s.device.GetSleep(ctx); err != nil {
			addHistoryError(h, "device_sleep", err.Error())
		} else {
			h.DeviceSleep = sessions
		}
	}

	if wantsWatchHistory(opts, "spo2") {
		if days, err := s.device.GetSpO2(ctx, offset); err != nil {
			addHistoryError(h, "spo2_days", err.Error())
		} else {
			h.SpO2Days = days
		}
	}
}

func wantsWatchHistory(opts api.StateOptions, kind string) bool {
	return opts.IncludeWatchHistory && (opts.WatchHistoryKinds == nil || opts.WatchHistoryKinds[kind])
}

func addHistoryError(h *api.HistoryState, key, msg string) {
	if h.Errors == nil {
		h.Errors = make(map[string]string)
	}
	h.Errors[key] = msg
}

func localDay(t time.Time) time.Time {
	lt := t.In(time.Local)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.Local)
}
