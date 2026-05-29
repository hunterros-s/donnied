package main

import (
	"fmt"
	"time"

	"smartwatch/internal/api"
)

func printSleepSession(n int, s api.DeviceSleep) {
	start := s.Start.Local()
	end := s.End.Local()
	dur := end.Sub(start).Round(time.Minute)

	fmt.Printf("Sleep session %d\n", n)
	fmt.Printf("  %s → %s  (%s)\n",
		start.Format("Mon 2006-01-02 15:04"),
		end.Format("Mon 15:04"),
		formatMinutes(int(dur.Minutes())),
	)

	totals := map[int]int{}
	for _, st := range s.Stages {
		totals[int(st.Stage)] += st.Minutes
	}
	fmt.Printf("  totals: light %s, deep %s, REM %s, awake %s\n",
		formatMinutes(totals[2]),
		formatMinutes(totals[3]),
		formatMinutes(totals[4]),
		formatMinutes(totals[5]),
	)

	fmt.Println("  stages:")
	cursor := start
	for _, st := range s.Stages {
		fmt.Printf("    %s  %-5s  %s\n", cursor.Format("15:04"), sleepStageName(int(st.Stage)), formatMinutes(st.Minutes))
		cursor = cursor.Add(time.Duration(st.Minutes) * time.Minute)
	}
}

func sleepStageName(stage int) string {
	switch stage {
	case 2:
		return "light"
	case 3:
		return "deep"
	case 4:
		return "REM"
	case 5:
		return "awake"
	default:
		return fmt.Sprintf("%d", stage)
	}
}

func formatMinutes(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
}

func printSpO2Day(day api.SpO2Day) {
	label := "today"
	if day.DaysAgo == 1 {
		label = "yesterday"
	} else if day.DaysAgo > 1 {
		label = fmt.Sprintf("%d days ago", day.DaysAgo)
	}

	fmt.Printf("SpO2 %s (%d samples)\n", label, len(day.Samples))
	if len(day.Samples) == 0 {
		return
	}
	fmt.Printf("%-6s  %3s  %3s  %3s\n", "SAMPLE", "MIN", "MAX", "AVG")
	for i, sample := range day.Samples {
		avg := float64(sample.Min+sample.Max) / 2.0
		fmt.Printf("%-6d  %3d  %3d  %5.1f\n", i+1, sample.Min, sample.Max, avg)
	}
}
