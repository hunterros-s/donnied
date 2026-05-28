package ble

// Protocol identifies which BLE characteristic set to use.
type Protocol int

const (
	V1 Protocol = iota
	V2
)

// Notification carries bytes received from a BLE characteristic notification,
// tagged with the generation of the link that produced it.
type Notification struct {
	Gen      uint64
	Protocol Protocol
	Data     []byte
}

// ScanResult is a discovered BLE peripheral advertising the UART service.
type ScanResult struct {
	Address string
	Name    string
	RSSI    int16
}