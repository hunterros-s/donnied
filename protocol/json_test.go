package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHistoryTypesUseLowercaseJSONKeys(t *testing.T) {
	hr, err := json.Marshal(HRSample{Time: time.Unix(0, 0).UTC(), BPM: 72, Interval: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(hr); !strings.Contains(got, `"bpm":72`) || strings.Contains(got, `"BPM"`) {
		t.Fatalf("HRSample JSON keys = %s", got)
	}

	steps, err := json.Marshal(SportDetail{Year: 2026, Month: 5, Day: 28, Hour: 9, Minute: 15, Steps: 123, Calories: 45, Distance: 67})
	if err != nil {
		t.Fatal(err)
	}
	got := string(steps)
	for _, key := range []string{`"year"`, `"month"`, `"day"`, `"hour"`, `"minute"`, `"steps"`, `"calories"`, `"distance"`} {
		if !strings.Contains(got, key) {
			t.Fatalf("SportDetail JSON missing %s: %s", key, got)
		}
	}
	if strings.Contains(got, `"Year"`) || strings.Contains(got, `"Calories"`) {
		t.Fatalf("SportDetail JSON used exported field keys: %s", got)
	}
}
