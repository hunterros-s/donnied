package main

import "time"

type infoResponse struct {
	PID             int       `json:"pid"`
	StartTime       time.Time `json:"start_time"`
	Uptime          string    `json:"uptime"`
	DeviceConnected bool      `json:"device_connected"`
}

type batteryResponse struct {
	Level    int  `json:"level"`
	Charging bool `json:"charging"`
}

type scanResult struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	RSSI    int16  `json:"rssi"`
	HasUART bool   `json:"has_uart"`
}

type deviceStatus struct {
	State      string           `json:"state"`
	PairedAddr string           `json:"paired_addr"`
	PairedName string           `json:"paired_name"`
	Connected  bool             `json:"connected"`
	Battery    *batteryResponse `json:"battery,omitempty"`
}

type hrSample struct {
	Time     time.Time `json:"time"`
	BPM      int       `json:"bpm"`
	Interval int       `json:"interval"`
}

type sportDetail struct {
	Year     int     `json:"year"`
	Month    int     `json:"month"`
	Day      int     `json:"day"`
	Hour     int     `json:"hour"`
	Minute   int     `json:"minute"`
	Steps    int     `json:"steps"`
	Calories float64 `json:"calories"`
	Distance int     `json:"distance"`
}

type realtimeReading struct {
	Kind  int `json:"kind"`
	Value int `json:"value"`
}

type spO2Day struct {
	DaysAgo int          `json:"days_ago"`
	Samples []spO2Sample `json:"samples"`
}

type spO2Sample struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type sleepSession struct {
	Start  time.Time    `json:"start"`
	End    time.Time    `json:"end"`
	Stages []sleepStage `json:"stages"`
}

type sleepStage struct {
	Stage   int `json:"stage"`
	Minutes int `json:"minutes"`
}
