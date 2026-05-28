// Package protocol implements the Y25/RS25 (Colmi R02 family) BLE protocol.
// Pure functions and byte twiddling — no concurrency, no side effects.
package protocol

import (
	"encoding/binary"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// BLE UUIDs
// ---------------------------------------------------------------------------

const (
	UARTService = "6e40fff0-b5a3-f393-e0a9-e50e24dcca9e"
	UARTRX      = "6e400002-b5a3-f393-e0a9-e50e24dcca9e"
	UARTTX      = "6e400003-b5a3-f393-e0a9-e50e24dcca9e"

	ServiceV2     = "de5bf728-d711-4e47-af26-65e3012a5dc7"
	CharCommandV2 = "de5bf72a-d711-4e47-af26-65e3012a5dc7"
	CharNotifyV2  = "de5bf729-d711-4e47-af26-65e3012a5dc7"

	DeviceInfoService = "0000180a-0000-1000-8000-00805f9b34fb"
	HWChar            = "00002a27-0000-1000-8000-00805f9b34fb"
	FWChar            = "00002a26-0000-1000-8000-00805f9b34fb"

	// AdvService is the UUID advertised in BLE advertisements by Y25/RS25
	// watches. The UART and V2 services are only discoverable after connecting.
	AdvService = "0000fe00-0000-1000-8000-00805f9b34fb"
)

// ---------------------------------------------------------------------------
// Big Data V2
// ---------------------------------------------------------------------------

const MagicV2 = 0xBC

type V2Cmd byte

const (
	V2Sleep             V2Cmd = 0x27
	V2BTMAC             V2Cmd = 0x2E
	V2SyncTime          V2Cmd = 0x40
	V2DeviceControl     V2Cmd = 0x41
	V2Battery           V2Cmd = 0x42
	V2DeviceInfo        V2Cmd = 0x43
	V2AIVoice           V2Cmd = 0x44
	V2Heartbeat         V2Cmd = 0x45
	V2DeviceWear        V2Cmd = 0x46
	V2WearSupport       V2Cmd = 0x47
	V2VoiceStatus       V2Cmd = 0x48
	V2BTConnect         V2Cmd = 0x49
	V2VolumeControl     V2Cmd = 0x51
	V2SpeakSoundSwitch  V2Cmd = 0x52
	V2GPTUpload         V2Cmd = 0x59
	V2DataReporting     V2Cmd = 0x73
	V2OTASOC            V2Cmd = 0xFC
	V2PictureThumbnails V2Cmd = 0xFD
)

func CRC16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func BuildV2Packet(cmdID byte, payload []byte) []byte {
	length := len(payload)
	var crc uint16
	if len(payload) > 0 {
		crc = CRC16Modbus(payload)
	} else {
		crc = 0xFFFF
	}
	header := []byte{
		MagicV2,
		cmdID,
		byte(length & 0xFF), byte((length >> 8) & 0xFF),
		byte(crc & 0xFF), byte((crc >> 8) & 0xFF),
	}
	return append(header, payload...)
}

// V2Reassembler buffers fragmented Big Data V2 frames across BLE notifications.
type V2Reassembler struct {
	buf      []byte
	expected int
}

func NewV2Reassembler() *V2Reassembler {
	return &V2Reassembler{}
}

func (r *V2Reassembler) Feed(data []byte) [][]byte {
	r.buf = append(r.buf, data...)
	var frames [][]byte
	for {
		if r.expected == 0 {
			if len(r.buf) < 6 {
				break
			}
			if r.buf[0] != MagicV2 {
				r.buf = nil
				break
			}
			payloadLen := binary.LittleEndian.Uint16(r.buf[2:4])
			r.expected = 6 + int(payloadLen)
		}
		if len(r.buf) < r.expected {
			break
		}
		frame := make([]byte, r.expected)
		copy(frame, r.buf[:r.expected])
		frames = append(frames, frame)
		r.buf = r.buf[r.expected:]
		r.expected = 0
	}
	return frames
}

// ---------------------------------------------------------------------------
// Command bytes (V1 service)
// ---------------------------------------------------------------------------

type Cmd byte

const (
	CmdSetTime        Cmd = 0x01
	CmdBattery        Cmd = 0x03
	CmdPhoneName      Cmd = 0x04
	CmdPreferences    Cmd = 0x0A
	CmdSyncHR         Cmd = 0x15
	CmdAutoHRPref     Cmd = 0x16
	CmdGoals          Cmd = 0x21
	CmdAutoSpO2Pref   Cmd = 0x2C
	CmdPacketSize     Cmd = 0x2F
	CmdAutoStressPref Cmd = 0x36
	CmdSyncStress     Cmd = 0x37
	CmdAutoHRVPref    Cmd = 0x38
	CmdSyncHRV        Cmd = 0x39
	CmdAutoTempPref   Cmd = 0x3A
	CmdSyncActivity   Cmd = 0x43
	CmdFindDevice     Cmd = 0x50
	CmdManualHR       Cmd = 0x69
	CmdNotification   Cmd = 0x73
	CmdRealtimeHR     Cmd = 0x1E
	CmdBigDataV2      Cmd = 0xBC
	CmdFactoryReset   Cmd = 0xFF
)

// ---------------------------------------------------------------------------
// Notification sub-types (CMD 0x73)
// ---------------------------------------------------------------------------

type Notify byte

const (
	NotifyNewHR        Notify = 0x01
	NotifyNewSpO2      Notify = 0x03
	NotifyNewSteps     Notify = 0x04
	NotifyBattery      Notify = 0x0C
	NotifyLiveActivity Notify = 0x12
)

// ---------------------------------------------------------------------------
// Sleep stages
// ---------------------------------------------------------------------------

type SleepStage byte

const (
	SleepLight SleepStage = 0x02
	SleepDeep  SleepStage = 0x03
	SleepREM   SleepStage = 0x04
	SleepAwake SleepStage = 0x05
)

var SleepStageNames = map[SleepStage]string{
	SleepLight: "light",
	SleepDeep:  "deep",
	SleepREM:   "REM",
	SleepAwake: "awake",
}

// ---------------------------------------------------------------------------
// Packet helpers
// ---------------------------------------------------------------------------

const PacketLen = 16

func BuildPacket(command byte, payload []byte) []byte {
	buf := make([]byte, PacketLen)
	buf[0] = command
	if len(payload) > 0 {
		if len(payload) > PacketLen-2 {
			panic("payload too long")
		}
		copy(buf[1:], payload)
	}
	buf[PacketLen-1] = Checksum(buf)
	return buf
}

func Checksum(buf []byte) byte {
	var sum byte
	for i := 0; i < len(buf)-1 && i < PacketLen-1; i++ {
		sum += buf[i]
	}
	return sum
}

func BCDByte(val int) byte {
	return byte(((val / 10) << 4) | (val % 10))
}

func Uint16LE(b0, b1 byte) uint16 {
	return uint16(b0) | (uint16(b1) << 8)
}

func BCDToDecimal(b byte) int {
	return (((int(b) >> 4) & 0x0F) * 10) + (int(b) & 0x0F)
}

// ---------------------------------------------------------------------------
// Battery
// ---------------------------------------------------------------------------

type BatteryInfo struct {
	Level    int  `json:"level"`
	Charging bool `json:"charging"`
}

func ParseBattery(packet []byte) (*BatteryInfo, bool) {
	if len(packet) < 3 || packet[0] != byte(CmdBattery) {
		return nil, false
	}
	return &BatteryInfo{Level: int(packet[1]), Charging: packet[2] == 1}, true
}

// ---------------------------------------------------------------------------
// Device notifications (CMD 0x73)
// ---------------------------------------------------------------------------

type LiveActivity struct {
	Steps    int     `json:"steps"`
	Calories float64 `json:"calories"`
	Distance int     `json:"distance"`
}

// ParseNotification returns (notifyType, extraData, ok).
// extraData is either an int, *BatteryInfo, or *LiveActivity.
func ParseNotification(packet []byte) (Notify, any, bool) {
	if len(packet) < 2 || packet[0] != byte(CmdNotification) {
		return 0, nil, false
	}
	ntype := Notify(packet[1])
	switch ntype {
	case NotifyNewHR:
		if len(packet) >= 3 {
			return ntype, int(packet[2]), true
		}
	case NotifyNewSpO2:
		if len(packet) >= 3 {
			return ntype, int(packet[2]), true
		}
	case NotifyBattery:
		if len(packet) >= 4 {
			return ntype, &BatteryInfo{Level: int(packet[2]), Charging: packet[3] == 1}, true
		}
	case NotifyLiveActivity:
		if len(packet) >= 11 {
			steps := (int(packet[2]) << 16) | (int(packet[3]) << 8) | int(packet[4])
			calories := float64((int(packet[5])<<16)|(int(packet[6])<<8)|int(packet[7])) / 10.0
			distance := (int(packet[8]) << 16) | (int(packet[9]) << 8) | int(packet[10])
			return ntype, LiveActivity{Steps: steps, Calories: calories, Distance: distance}, true
		}
	}
	return ntype, nil, true
}

// ---------------------------------------------------------------------------
// Real-time HR (CMD 105 / DataRequest)
// ---------------------------------------------------------------------------

type RtAction byte

const (
	RtStart    RtAction = 1
	RtPause    RtAction = 2
	RtContinue RtAction = 3
	RtStop     RtAction = 4
)

type RtType byte

const (
	RtHeartRate     RtType = 1
	RtBloodPressure RtType = 2
	RtSpO2          RtType = 3
	RtFatigue       RtType = 4
	RtHealthCheck   RtType = 5
	RtECG           RtType = 7
	RtPressure      RtType = 8
	RtBloodSugar    RtType = 9
	RtHRV           RtType = 10
)

func (t RtType) String() string {
	switch t {
	case RtHeartRate:
		return "heart_rate"
	case RtBloodPressure:
		return "blood_pressure"
	case RtSpO2:
		return "spo2"
	case RtFatigue:
		return "fatigue"
	case RtHealthCheck:
		return "health_check"
	case RtECG:
		return "ecg"
	case RtPressure:
		return "pressure"
	case RtBloodSugar:
		return "blood_sugar"
	case RtHRV:
		return "hrv"
	default:
		return fmt.Sprintf("unknown_%d", byte(t))
	}
}

func RtStartPacket(reading RtType) []byte {
	return BuildPacket(105, []byte{byte(reading), byte(RtStart)})
}

func RtContinuePacket(reading RtType) []byte {
	return BuildPacket(105, []byte{byte(reading), byte(RtContinue)})
}

func RtStopPacket(reading RtType) []byte {
	return BuildPacket(106, []byte{byte(reading), 0, 0})
}

type RtReading struct {
	Kind  RtType `json:"kind"`
	Value int    `json:"value"`
}

type RtError struct {
	Kind RtType `json:"kind"`
	Code int    `json:"code"`
}

func ParseRealtime(packet []byte) (any, bool) {
	if len(packet) < 4 || packet[0] != byte(CmdManualHR) {
		return nil, false
	}
	kind := RtType(packet[1])
	errCode := packet[2]
	if errCode != 0 {
		return RtError{Kind: kind, Code: int(errCode)}, true
	}
	return RtReading{Kind: kind, Value: int(packet[3])}, true
}

// ---------------------------------------------------------------------------
// Sleep data (Big Data V2, CMD 0xBC / type 0x27)
// ---------------------------------------------------------------------------

type SleepSession struct {
	Start  time.Time          `json:"start"`
	End    time.Time          `json:"end"`
	Stages []SleepStageRecord `json:"stages"`
}

type SleepStageRecord struct {
	Stage   SleepStage `json:"stage"`
	Minutes int        `json:"minutes"`
}

func ReadHRLogPacket(target time.Time) []byte {
	ts := uint32(target.Unix())
	return BuildPacket(byte(CmdSyncHR), binary.LittleEndian.AppendUint32(nil, ts))
}

func ReadStepsPacket(dayOffset int) []byte {
	sub := []byte{byte(dayOffset), 0x0F, 0x00, 0x5F, 0x01}
	return BuildPacket(byte(CmdSyncActivity), sub)
}

func ParseSleepData(raw []byte) []SleepSession {
	if len(raw) < 7 || raw[0] != byte(CmdBigDataV2) || raw[1] != byte(V2Sleep) {
		return nil
	}

	payloadLen := Uint16LE(raw[2], raw[3])
	if payloadLen < 2 {
		return nil
	}

	days := raw[6]
	var sessions []SleepSession
	idx := 7
	now := time.Now()
	baseToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	for i := 0; i < int(days); i++ {
		if idx >= len(raw) {
			break
		}
		daysAgo := int(raw[idx])
		idx++
		dayBytes := int(raw[idx])
		idx++
		if idx+4 > len(raw) {
			break
		}
		sStart := Uint16LE(raw[idx], raw[idx+1])
		idx += 2
		sEnd := Uint16LE(raw[idx], raw[idx+1])
		idx += 2

		baseDay := baseToday.AddDate(0, 0, -daysAgo)
		var start, end time.Time
		if sStart > sEnd {
			start = baseDay.Add(-time.Duration(1440-int(sStart)) * time.Minute)
		} else {
			start = baseDay.Add(time.Duration(sStart) * time.Minute)
		}
		end = baseDay.Add(time.Duration(sEnd) * time.Minute)

		var stages []SleepStageRecord
		numStages := (dayBytes - 4) / 2
		for j := 0; j < numStages; j++ {
			if idx+1 >= len(raw) {
				break
			}
			st := SleepStage(raw[idx])
			dur := int(raw[idx+1])
			idx += 2
			if dur > 0 {
				stages = append(stages, SleepStageRecord{Stage: st, Minutes: dur})
			}
		}

		sessions = append(sessions, SleepSession{Start: start, End: end, Stages: stages})
	}

	return sessions
}

// ---------------------------------------------------------------------------
// High-level request builders
// ---------------------------------------------------------------------------

func SetTimePacket(now time.Time) []byte {
	return BuildPacket(byte(CmdSetTime), []byte{
		BCDByte(now.Year() % 2000),
		BCDByte(int(now.Month())),
		BCDByte(now.Day()),
		BCDByte(now.Hour()),
		BCDByte(now.Minute()),
		BCDByte(now.Second()),
	})
}

func BatteryPacket() []byte {
	return BuildPacket(byte(CmdBattery), nil)
}

func PhoneNamePacket(name string) []byte {
	if len(name) > 10 {
		name = name[:10]
	}
	payload := append([]byte{0x02, 0x0A}, []byte(name)...)
	return BuildPacket(byte(CmdPhoneName), payload)
}

func FindDevicePacket() []byte {
	return BuildPacket(byte(CmdFindDevice), []byte{0x55, 0xAA})
}

func SleepRequestPacket() []byte {
	return BuildV2Packet(byte(V2Sleep), []byte{0xFF})
}

func BatteryV2Packet() []byte {
	return BuildV2Packet(byte(V2Battery), nil)
}

func DeviceInfoV2Packet() []byte {
	return BuildV2Packet(byte(V2DeviceInfo), nil)
}

// JSONString returns a JSON string for a sleep session slice.
// IsErrorResponse checks if a V1 packet has the error bit set
// (MSB of the command byte = 1, i.e. command >= 128).
func IsErrorResponse(pkt []byte) bool {
	return len(pkt) > 0 && pkt[0] >= 128
}

// ---------------------------------------------------------------------------
// Day helper
// ---------------------------------------------------------------------------

// Day represents a calendar day for history queries.
type Day struct {
	Year  int
	Month time.Month
	Day   int
}

// Time returns local midnight for the day. The watch stores and reports
// history in local wall-clock time, so history requests should use local days
// rather than UTC midnights.
func (d Day) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.Local)
}

// Today returns the current local day.
func Today() Day {
	now := time.Now()
	return Day{Year: now.Year(), Month: now.Month(), Day: now.Day()}
}

// DayOffset returns the number of local calendar days between d and today.
func (d Day) DayOffset() int {
	today := Today().Time()
	target := d.Time()
	return int(today.Sub(target).Hours() / 24)
}

// ---------------------------------------------------------------------------
// HR log (V1 CMD 0x15) — multi-packet response
// ---------------------------------------------------------------------------

// HRSample is a single heart rate reading at a point in time.
type HRSample struct {
	Time     time.Time `json:"time"`
	BPM      int       `json:"bpm"`
	Interval int       `json:"interval"` // minutes between readings (typically 5)
}

// HRLogParser accumulates V1 heart rate log packets (CMD 0x15)
// which arrive as a multi-packet split-array response.
type HRLogParser struct {
	raw       []int
	timestamp time.Time
	size      int
	index     int
	rangeMin  int
	done      bool
}

func NewHRLogParser() *HRLogParser  { return &HRLogParser{} }
func (p *HRLogParser) Reset()       { *p = HRLogParser{} }
func (p *HRLogParser) IsDone() bool { return p.done }

// Feed processes a V1 heart rate log packet. It returns (done, result, error).
// When done=true, result is []HRSample.
func (p *HRLogParser) Feed(pkt []byte) (bool, any, error) {
	if len(pkt) < 2 || pkt[0] != byte(CmdSyncHR) {
		return true, nil, fmt.Errorf("invalid HR log packet: cmd=0x%02X len=%d", pkt[0], len(pkt))
	}

	sub := pkt[1]

	// Error response
	if sub == 0xFF {
		return true, nil, nil // no data available
	}

	// Header packet (sub_type == 0)
	if sub == 0 {
		p.Reset()
		if len(pkt) < 4 {
			return true, nil, fmt.Errorf("HR log header too short")
		}
		p.size = int(pkt[2])
		p.rangeMin = int(pkt[3])
		p.raw = make([]int, p.size*13)
		p.done = false
		return false, nil, nil
	}

	// First data packet (sub_type == 1)
	if sub == 1 {
		if len(pkt) < 7 {
			return true, nil, fmt.Errorf("HR log data packet too short")
		}
		p.timestamp = time.Unix(int64(binary.LittleEndian.Uint32(pkt[2:6])), 0).In(time.Local)
		n := min(len(pkt)-6, 9) // bytes 6..14 usable, but last byte is checksum
		for i := 0; i < n && p.index < len(p.raw); i++ {
			p.raw[p.index] = int(pkt[6+i])
			p.index++
		}
		return false, nil, nil
	}

	// Continuation packets (sub_type > 1)
	for i := 2; i < len(pkt)-1 && p.index < len(p.raw); i++ {
		p.raw[p.index] = int(pkt[i])
		p.index++
	}

	// Final packet indicator
	if sub >= byte(p.size)-1 || sub == 23 {
		p.done = true
		samples := p.buildSamples()
		return true, samples, nil
	}

	return false, nil, nil
}

func (p *HRLogParser) buildSamples() []HRSample {
	if p.raw == nil || p.timestamp.IsZero() {
		return nil
	}
	n := len(p.raw)
	if n > 288 {
		n = 288
	}
	samples := make([]HRSample, 0, n)
	base := p.timestamp
	interval := p.rangeMin
	if interval <= 0 {
		interval = 5
	}
	for i := 0; i < n; i++ {
		if p.raw[i] > 0 {
			samples = append(samples, HRSample{
				Time:     base.Add(time.Duration(i*interval) * time.Minute),
				BPM:      p.raw[i],
				Interval: interval,
			})
		}
	}
	return samples
}

// ---------------------------------------------------------------------------
// Steps / sport detail (V1 CMD 0x43) — multi-packet response
// ---------------------------------------------------------------------------

// SportDetail is one 15-minute activity record.
type SportDetail struct {
	Year     int     `json:"year"`
	Month    int     `json:"month"`
	Day      int     `json:"day"`
	Hour     int     `json:"hour"`
	Minute   int     `json:"minute"`
	Steps    int     `json:"steps"`
	Calories float64 `json:"calories"`
	Distance int     `json:"distance"` // meters
}

func (s SportDetail) Time() time.Time {
	return time.Date(s.Year, time.Month(s.Month), s.Day, s.Hour, s.Minute, 0, 0, time.Local)
}

// StepsParser accumulates V1 sport/activity packets (CMD 0x43).
type StepsParser struct {
	index   int
	details []SportDetail
	newCal  bool // new calorie protocol detected
	done    bool
}

func NewStepsParser() *StepsParser  { return &StepsParser{} }
func (p *StepsParser) Reset()       { *p = StepsParser{} }
func (p *StepsParser) IsDone() bool { return p.done }

// Feed processes a V1 steps packet. Returns (done, result, error).
func (p *StepsParser) Feed(pkt []byte) (bool, any, error) {
	if len(pkt) != PacketLen || pkt[0] != byte(CmdSyncActivity) {
		return true, nil, fmt.Errorf("invalid steps packet: cmd=0x%02X len=%d", pkt[0], len(pkt))
	}

	sub := pkt[1]

	// No data response
	if p.index == 0 && sub == 0xFF {
		p.done = true
		return true, nil, nil
	}

	// Header packet
	if p.index == 0 && sub == 0xF0 {
		if pkt[3] == 1 {
			p.newCal = true
		}
		p.index++
		return false, nil, nil
	}

	// Data packet
	year := BCDToDecimal(pkt[1]) + 2000
	month := BCDToDecimal(pkt[2])
	day := BCDToDecimal(pkt[3])
	timeIdx := int(pkt[4])
	hour := timeIdx / 4
	minute := (timeIdx % 4) * 15

	// pkt[5] = current index, pkt[6] = total packets
	calRaw := float64(Uint16LE(pkt[7], pkt[8]))
	calories := calRaw
	if p.newCal {
		// Newer firmware reports centi-kcal. The previous parser multiplied by
		// ten, producing values like 21150 for a 494-step bucket; that is 21.15 kcal.
		calories = calRaw / 100.0
	}
	steps := int(Uint16LE(pkt[9], pkt[10]))
	distance := int(Uint16LE(pkt[11], pkt[12]))

	p.details = append(p.details, SportDetail{
		Year: year, Month: month, Day: day,
		Hour: hour, Minute: minute,
		Steps: steps, Calories: calories, Distance: distance,
	})

	// Check if this is the last packet
	if pkt[5] == pkt[6]-1 || pkt[5] >= pkt[6] {
		p.done = true
		result := p.details
		return true, result, nil
	}

	p.index++
	return false, nil, nil
}

// ---------------------------------------------------------------------------
// SpO2 history (V2 Big Data cmd 0x2A)
// ---------------------------------------------------------------------------

const V2SpO2 V2Cmd = 0x2A

// SpO2Sample is a min/max blood oxygen reading for a day.
type SpO2Sample struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// SpO2Day holds SpO2 data for a single day.
type SpO2Day struct {
	DaysAgo int          `json:"days_ago"`
	Samples []SpO2Sample `json:"samples"`
}

func SpO2RequestPacket(daysAgo int) []byte {
	return BuildV2Packet(byte(V2SpO2), []byte{0xFF, byte(daysAgo)})
}

func ParseSpO2Data(frame []byte) []SpO2Day {
	if len(frame) < 8 || frame[0] != MagicV2 || frame[1] != byte(V2SpO2) {
		return nil
	}
	payloadLen := int(Uint16LE(frame[2], frame[3]))
	if payloadLen < 2 || len(frame) < 6+payloadLen {
		return nil
	}

	payload := frame[6 : 6+payloadLen]
	if len(payload) < 2 {
		return nil
	}

	// BigData 0x2A payload is: unknown byte, daysAgo, then (min,max) pairs.
	// There is no sample-count byte; treating the first sample as a count caused
	// misalignment and impossible values like max=1 or min=0.
	daysAgo := int(payload[1])
	data := payload[2:]
	samples := make([]SpO2Sample, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		minVal := int(data[i])
		maxVal := int(data[i+1])
		if !validSpO2(minVal) || !validSpO2(maxVal) {
			continue
		}
		if minVal > maxVal {
			minVal, maxVal = maxVal, minVal
		}
		samples = append(samples, SpO2Sample{Min: minVal, Max: maxVal})
	}
	if len(samples) == 0 {
		return []SpO2Day{{DaysAgo: daysAgo, Samples: []SpO2Sample{}}}
	}
	return []SpO2Day{{DaysAgo: daysAgo, Samples: samples}}
}

func validSpO2(v int) bool {
	return v >= 50 && v <= 100
}

func SleepSessionsJSON(sessions []SleepSession) string {
	if len(sessions) == 0 {
		return "[]"
	}
	// Build JSON manually to avoid reflection overhead and keep package pure.
	var b []byte
	b = append(b, '[')
	for i, s := range sessions {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, fmt.Sprintf(`{"start":%q,"end":%q,"stages":[`, s.Start.Format(time.RFC3339), s.End.Format(time.RFC3339))...)
		for j, st := range s.Stages {
			if j > 0 {
				b = append(b, ',')
			}
			b = append(b, fmt.Sprintf(`{"stage":%d,"minutes":%d}`, st.Stage, st.Minutes)...)
		}
		b = append(b, ']', '}')
	}
	b = append(b, ']')
	return string(b)
}
