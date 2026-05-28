package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

func doRealtime(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(map[string]int{"type": rtType})
	resp, err := client.Post("http://localhost/device/realtime/read", "application/json", bytes.NewReader(body))
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
	var reading realtimeReading
	if err := json.NewDecoder(resp.Body).Decode(&reading); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d\n", realtimeName(reading.Kind), reading.Value)
}

func doRealtimeStart(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(map[string]int{"type": rtType})
	resp, err := client.Post("http://localhost/device/realtime/start", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doRealtimeStop(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(map[string]int{"type": rtType})
	resp, err := client.Post("http://localhost/device/realtime/stop", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func parseRealtimeArg() int {
	if len(os.Args) <= 2 {
		return 1 // heart rate
	}
	switch os.Args[2] {
	case "hr", "heart-rate", "heart_rate":
		return 1
	case "spo2", "sp02", "oxygen":
		return 3
	}
	rtType, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid type (use hr, spo2, or numeric type)")
		os.Exit(1)
	}
	return rtType
}

func realtimeName(kind int) string {
	switch kind {
	case 1:
		return "heart_rate"
	case 2:
		return "blood_pressure"
	case 3:
		return "spo2"
	case 4:
		return "fatigue"
	case 5:
		return "health_check"
	case 7:
		return "ecg"
	case 8:
		return "pressure"
	case 9:
		return "blood_sugar"
	case 10:
		return "hrv"
	default:
		return fmt.Sprintf("unknown_%d", kind)
	}
}
