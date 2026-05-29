package history

import "time"

// Source identifies how persisted metric history was observed.
type Source string

const (
	SourcePoll Source = "poll"
)

// Status summarizes the metric history tracker/store state.
type Status struct {
	LastSync *time.Time        `json:"last_sync,omitempty"`
	Errors   map[string]string `json:"errors,omitempty"`
}
