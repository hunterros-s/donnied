package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"smartwatch/internal/api"
)

func doHRLog(client *http.Client) {
	day := "today"
	if len(os.Args) > 2 {
		day = os.Args[2]
	}
	state := fetchState(client, historyQuery(day, "hr"))
	samples := state.History.HRSamples
	if len(samples) == 0 {
		printHistoryErrors(state, "hr_samples")
		fmt.Println("No heart rate data.")
		return
	}
	fmt.Printf("%-20s  %s\n", "TIME", "BPM")
	for _, s := range samples {
		fmt.Printf("%-20s  %d\n", s.Time.Local().Format("2006-01-02 15:04"), s.BPM)
	}
}

func doSteps(client *http.Client) {
	day := dayArgFromOffset()
	state := fetchState(client, historyQuery(day, "steps"))
	details := state.History.StepDetails
	if len(details) == 0 {
		printHistoryErrors(state, "step_details")
		fmt.Println("No step data.")
		return
	}
	fmt.Printf("%-20s  %6s  %8s  %6s\n", "TIME", "STEPS", "KCAL", "DIST")
	for _, d := range details {
		fmt.Printf("%-20s  %6d  %8.2f  %6d\n", fmt.Sprintf("%d-%02d-%02d %02d:%02d", d.Year, d.Month, d.Day, d.Hour, d.Minute), d.Steps, d.Calories, d.Distance)
	}
}

func doSleep(client *http.Client) {
	state := fetchState(client, historyQuery("today", "device_sleep"))
	sessions := state.History.DeviceSleep
	if len(sessions) == 0 {
		printHistoryErrors(state, "device_sleep")
		fmt.Println("No sleep data.")
		return
	}

	for i, s := range sessions {
		if i > 0 {
			fmt.Println()
		}
		printSleepSession(i+1, s)
	}
}

func doSpO2(client *http.Client) {
	day := dayArgFromOffset()
	state := fetchState(client, historyQuery(day, "spo2"))
	days := state.History.SpO2Days
	if len(days) == 0 {
		printHistoryErrors(state, "spo2_days")
		fmt.Println("No SpO2 data.")
		return
	}
	for i, day := range days {
		if i > 0 {
			fmt.Println()
		}
		printSpO2Day(day)
	}
}

func historyQuery(day, watchKind string) string {
	q := url.Values{}
	q.Set("history", "true")
	q.Set("watch_history", watchKind)
	q.Set("day", day)
	return q.Encode()
}

func dayArgFromOffset() string {
	offset := 0
	if len(os.Args) > 2 {
		var err error
		offset, err = strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid offset")
			os.Exit(1)
		}
	}
	if offset == 0 {
		return "today"
	}
	day := time.Now().AddDate(0, 0, -offset)
	return day.Format("2006-01-02")
}

func printHistoryErrors(state api.AppState, key string) {
	if state.History.Errors == nil {
		return
	}
	if msg := state.History.Errors[key]; msg != "" {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", key, msg)
		return
	}
	if msg := state.History.Errors["watch_history"]; msg != "" {
		fmt.Fprintf(os.Stderr, "warning: watch_history: %s\n", msg)
	}
}
