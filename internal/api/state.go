package api

import (
	"time"

	"smartwatch/internal/sleep"
	"smartwatch/protocol"
)

// Canonical API DTOs. HTTP, SSE, CLI, and the eventual web UI should all speak
// these shapes instead of maintaining endpoint-specific duplicates.
type AppState struct {
	Process ProcessState `json:"process"`
	Device  DeviceState  `json:"device"`
	Sleep   SleepState   `json:"sleep"`
	History HistoryState `json:"history"`
}

type ProcessState struct {
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	Uptime    string    `json:"uptime"`
}

type DeviceState struct {
	State        string        `json:"state"`
	Connected    bool          `json:"connected"`
	PairedAddr   string        `json:"paired_addr"`
	PairedName   string        `json:"paired_name"`
	LastSeen     *time.Time    `json:"last_seen,omitempty"`
	Battery      *BatteryInfo  `json:"battery,omitempty"`
	HeartRate    int           `json:"heart_rate,omitempty"`
	SpO2         int           `json:"spo2,omitempty"`
	Steps        int           `json:"steps,omitempty"`
	Calories     float64       `json:"calories,omitempty"`
	Distance     int           `json:"distance,omitempty"`
	LiveActivity *LiveActivity `json:"live_activity,omitempty"`
}

type SleepState struct {
	State    string     `json:"state"`
	Stage    string     `json:"stage,omitempty"`
	StageRaw int        `json:"stage_raw,omitempty"`
	Since    *time.Time `json:"since,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
	Source   string     `json:"source,omitempty"`
	Stale    bool       `json:"stale"`
	Error    string     `json:"error,omitempty"`
}

type HistoryState struct {
	SelectedDay   string            `json:"selected_day"`
	SleepSessions []SleepSession    `json:"sleep_sessions,omitempty"`
	DeviceSleep   []DeviceSleep     `json:"device_sleep,omitempty"`
	HRSamples     []HRSample        `json:"hr_samples,omitempty"`
	StepDetails   []StepDetail      `json:"step_details,omitempty"`
	SpO2Days      []SpO2Day         `json:"spo2_days,omitempty"`
	Errors        map[string]string `json:"errors,omitempty"`
}

// StateOptions controls which bounded/current-view history window is populated
// inside AppState. The shape remains the same whether history is populated or
// not; callers can request more expensive watch-backed history explicitly.
type StateOptions struct {
	Day                 time.Time
	From                time.Time
	To                  time.Time
	IncludeHistory      bool
	IncludeWatchHistory bool
	WatchHistoryKinds   map[string]bool
}

type ActionResult struct {
	Message         string           `json:"message,omitempty"`
	State           *AppState        `json:"state,omitempty"`
	ScanResults     []ScanResult     `json:"scan_results,omitempty"`
	RealtimeReading *RealtimeReading `json:"realtime_reading,omitempty"`
}

type PairRequest struct {
	Addr string `json:"addr"`
	Name string `json:"name"`
}

type RealtimeRequest struct {
	Type int `json:"type"`
}

// Shared domain/protocol DTOs used directly in canonical API responses.
type BatteryInfo = protocol.BatteryInfo
type LiveActivity = protocol.LiveActivity
type DeviceSleep = protocol.SleepSession
type HRSample = protocol.HRSample
type StepDetail = protocol.SportDetail
type SpO2Day = protocol.SpO2Day
type SpO2Sample = protocol.SpO2Sample
type RealtimeReading = protocol.RtReading
type SleepSession = sleep.Session

type ScanResult struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	RSSI    int16  `json:"rssi"`
	HasUART bool   `json:"has_uart"`
}
