package device

import (
	"time"

	"smartwatch/protocol"
)

// State is the durable connection state.
type State int

const (
	StateIdle State = iota
	StateScanning
	StateConnecting
	StateConnected
	StateDisconnected
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateScanning:
		return "scanning"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// ScanResult is a discovered BLE peripheral (public API type).
type ScanResult struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	RSSI    int16  `json:"rssi"`
	HasUART bool   `json:"has_uart"`
}

// EventType identifies unsolicited device events surfaced above the BLE layer.
type EventType string

const (
	EventV2Frame EventType = "v2_frame"
)

// Event is a generic unsolicited device event. The device package only exposes
// transport/protocol facts; higher layers decide whether a V2 frame is sleep,
// SpO2, or something else worth persisting.
type Event struct {
	Type       EventType
	DeviceAddr string
	ReceivedAt time.Time
	V2Cmd      protocol.V2Cmd
	Data       []byte
}

// DeviceData holds the latest cached telemetry from the watch.
type DeviceData struct {
	LastSeen     time.Time
	Battery      *protocol.BatteryInfo
	HeartRate    int
	SpO2         int
	Steps        int
	Calories     float64
	Distance     int
	LiveActivity *protocol.LiveActivity
}

// Snapshot is an immutable view of manager state for cheap concurrent reads.
type Snapshot struct {
	State      State
	Connected  bool
	Data       DeviceData
	PairedAddr string
	PairedName string
}

// cloneData returns a deep copy of DeviceData, ensuring no shared pointers.
func cloneData(d DeviceData) DeviceData {
	if d.Battery != nil {
		cp := *d.Battery
		d.Battery = &cp
	}
	if d.LiveActivity != nil {
		cp := *d.LiveActivity
		d.LiveActivity = &cp
	}
	return d
}
