package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"smartwatch/internal/api"
)

func doRealtime(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(api.RealtimeRequest{Type: rtType})
	result := postAction(client, "realtime-read", body)
	if result.RealtimeReading == nil {
		fmt.Fprintln(os.Stderr, "error: no realtime reading returned")
		os.Exit(1)
	}
	fmt.Printf("%s: %d\n", realtimeName(int(result.RealtimeReading.Kind)), result.RealtimeReading.Value)
}

func doRealtimeStart(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(api.RealtimeRequest{Type: rtType})
	result := postAction(client, "realtime-start", body)
	printActionMessage(result)
}

func doRealtimeStop(client *http.Client) {
	rtType := parseRealtimeArg()
	body, _ := json.Marshal(api.RealtimeRequest{Type: rtType})
	result := postAction(client, "realtime-stop", body)
	printActionMessage(result)
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
