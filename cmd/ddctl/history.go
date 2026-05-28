package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

func doHRLog(client *http.Client) {
	day := "today"
	if len(os.Args) > 2 {
		day = os.Args[2]
	}
	resp, err := client.Get("http://localhost/device/hr-log?day=" + url.QueryEscape(day))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	var samples []hrSample
	if err := json.NewDecoder(resp.Body).Decode(&samples); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(samples) == 0 {
		fmt.Println("No heart rate data.")
		return
	}
	fmt.Printf("%-20s  %s\n", "TIME", "BPM")
	for _, s := range samples {
		fmt.Printf("%-20s  %d\n", s.Time.Local().Format("2006-01-02 15:04"), s.BPM)
	}
}

func doSteps(client *http.Client) {
	offset := 0
	if len(os.Args) > 2 {
		var err error
		offset, err = strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid offset")
			os.Exit(1)
		}
	}
	resp, err := client.Get(fmt.Sprintf("http://localhost/device/steps?offset=%d", offset))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	var details []sportDetail
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(details) == 0 {
		fmt.Println("No step data.")
		return
	}
	fmt.Printf("%-20s  %6s  %8s  %6s\n", "TIME", "STEPS", "KCAL", "DIST")
	for _, d := range details {
		fmt.Printf("%-20s  %6d  %8.2f  %6d\n", fmt.Sprintf("%d-%02d-%02d %02d:%02d", d.Year, d.Month, d.Day, d.Hour, d.Minute), d.Steps, d.Calories, d.Distance)
	}
}

func doSleep(client *http.Client) {
	resp, err := client.Get("http://localhost/device/sleep")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	var sessions []sleepSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
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
	daysAgo := 0
	if len(os.Args) > 2 {
		var err error
		daysAgo, err = strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid days_ago")
			os.Exit(1)
		}
	}
	resp, err := client.Get(fmt.Sprintf("http://localhost/device/spo2?days_ago=%d", daysAgo))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	var days []spO2Day
	if err := json.NewDecoder(resp.Body).Decode(&days); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(days) == 0 {
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
