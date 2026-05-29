package sleep

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"
)

// Source identifies how a sleep record was observed.
type Source string

const (
	SourcePoll     Source = "poll"
	SourceRealtime Source = "realtime"
)

// State is the high-level awake/asleep state used by dashboards and alerts.
type State string

const (
	StateAsleep  State = "asleep"
	StateAwake   State = "awake"
	StateUnknown State = "unknown"
)

// ObservedSession is decoded sleep data from any upstream source. It is not
// tied to the watch protocol package; app-level adapters translate protocol
// structs into this shape before sleep normalization/persistence.
type ObservedSession struct {
	Start  time.Time       `json:"start"`
	End    time.Time       `json:"end"`
	Stages []ObservedStage `json:"stages"`
}

// ObservedStage is a decoded sleep-stage duration. Stage values currently use
// the watch's raw numeric stage IDs, but sleep treats them as domain input, not
// BLE bytes.
type ObservedStage struct {
	StageRaw int `json:"stage_raw"`
	Minutes  int `json:"minutes"`
}

// Session is a normalized sleep session derived from observed sleep data.
type Session struct {
	ID           string    `json:"id"`
	DeviceAddr   string    `json:"device_addr"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	TotalMinutes int       `json:"total_minutes"`
	Source       Source    `json:"source"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	Segments     []Segment `json:"segments"`
}

// Segment is a normalized interval within a sleep session.
type Segment struct {
	Index     int       `json:"index"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Minutes   int       `json:"minutes"`
	StageRaw  int       `json:"stage_raw"`
	StageName string    `json:"stage"`
	State     State     `json:"state"`
	Source    Source    `json:"source"`
}

// Status is the current derived awake/asleep state.
type Status struct {
	State    State      `json:"state"`
	Stage    string     `json:"stage,omitempty"`
	StageRaw int        `json:"stage_raw,omitempty"`
	Since    *time.Time `json:"since,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
	Source   Source     `json:"source,omitempty"`
	Stale    bool       `json:"stale"`
}

// NormalizeSessions converts observed sleep sessions into timestamped,
// dashboard-friendly sessions and segments. Session IDs are stable across
// repeated polling/realtime updates for the same device and sleep start time,
// so an in-progress realtime session can grow without creating duplicates.
func NormalizeSessions(deviceAddr string, source Source, observedAt time.Time, in []ObservedSession) []Session {
	out := make([]Session, 0, len(in))
	for _, s := range in {
		if s.Start.IsZero() || s.End.IsZero() || !s.End.After(s.Start) {
			continue
		}

		total := int(s.End.Sub(s.Start).Round(time.Minute).Minutes())
		ns := Session{
			ID:           sessionID(deviceAddr, s.Start),
			DeviceAddr:   deviceAddr,
			Start:        s.Start,
			End:          s.End,
			TotalMinutes: total,
			Source:       source,
			FirstSeen:    observedAt,
			LastSeen:     observedAt,
		}

		cursor := s.Start
		for _, st := range s.Stages {
			if st.Minutes <= 0 {
				continue
			}
			segEnd := cursor.Add(time.Duration(st.Minutes) * time.Minute)
			if segEnd.After(s.End) {
				segEnd = s.End
			}
			if !segEnd.After(cursor) {
				continue
			}
			name, state := classifyStage(st.StageRaw)
			ns.Segments = append(ns.Segments, Segment{
				Index:     len(ns.Segments),
				Start:     cursor,
				End:       segEnd,
				Minutes:   int(segEnd.Sub(cursor).Round(time.Minute).Minutes()),
				StageRaw:  st.StageRaw,
				StageName: name,
				State:     state,
				Source:    source,
			})
			cursor = segEnd
			if !cursor.Before(s.End) {
				break
			}
		}

		out = append(out, ns)
	}
	return out
}

func sessionID(deviceAddr string, start time.Time) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s", deviceAddr, start.UTC().Format(time.RFC3339Nano))))
	return hex.EncodeToString(sum[:])
}

func classifyStage(stage int) (string, State) {
	switch stage {
	case 2:
		return "light", StateAsleep
	case 3:
		return "deep", StateAsleep
	case 4:
		return "rem", StateAsleep
	case 5:
		return "awake", StateAwake
	default:
		return fmt.Sprintf("unknown_%d", stage), StateUnknown
	}
}
