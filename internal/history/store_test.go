package history

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"smartwatch/protocol"
)

func TestStorePersistsMetricHistory(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	observed := time.Date(2026, 5, 29, 12, 0, 0, 0, time.Local)
	deviceAddr := "watch-1"

	hrTime := time.Date(2026, 5, 29, 9, 0, 0, 0, time.Local)
	if err := store.SaveHR(ctx, deviceAddr, SourcePoll, observed, []protocol.HRSample{{Time: hrTime, BPM: 72, Interval: 5}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSteps(ctx, deviceAddr, SourcePoll, observed, []protocol.SportDetail{{Year: 2026, Month: 5, Day: 29, Hour: 9, Minute: 0, Steps: 123, Calories: 4.5, Distance: 67}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSpO2(ctx, deviceAddr, SourcePoll, observed, []protocol.SpO2Day{{DaysAgo: 0, Samples: []protocol.SpO2Sample{{Min: 97, Max: 99}}}}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 5, 29, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 1)

	hr, err := store.HRSamples(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(hr) != 1 || hr[0].BPM != 72 || hr[0].Interval != 5 {
		t.Fatalf("hr = %#v", hr)
	}

	steps, err := store.StepDetails(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Steps != 123 || steps[0].Distance != 67 {
		t.Fatalf("steps = %#v", steps)
	}

	spo2, err := store.SpO2Days(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(spo2) != 1 || len(spo2[0].Samples) != 1 || spo2[0].Samples[0].Min != 97 || spo2[0].Samples[0].Max != 99 {
		t.Fatalf("spo2 = %#v", spo2)
	}
}
