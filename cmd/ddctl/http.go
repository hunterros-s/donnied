package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"smartwatch/internal/api"
)

const socketPath = "/tmp/smartwatch.sock"

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 45 * time.Second,
	}
}

func doHealth(client *http.Client) {
	resp, err := client.Get("http://localhost/api/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		fmt.Println("healthy")
	} else {
		fmt.Println("unhealthy")
	}
}

func doInfo(client *http.Client) {
	state := fetchState(client, "")
	fmt.Printf("PID:       %d\n", state.Process.PID)
	fmt.Printf("Started:   %s\n", state.Process.StartTime.Format(time.RFC3339))
	fmt.Printf("Uptime:    %s\n", state.Process.Uptime)
	fmt.Printf("Device:    %v\n", state.Device.Connected)
}

func doShutdown(client *http.Client) {
	result := postAction(client, "shutdown", nil)
	printActionMessage(result)
}

func doScan(client *http.Client) {
	result := postAction(client, "scan", nil)
	results := result.ScanResults
	if len(results) == 0 {
		fmt.Println("No devices found.")
		return
	}

	fmt.Printf("%-40s  %-20s  %6s\n", "ADDRESS", "NAME", "RSSI")
	fmt.Println(string(bytes.Repeat([]byte("-"), 72)))
	for _, r := range results {
		fmt.Printf("%-40s  %-20s  %6d\n", r.Address, r.Name, r.RSSI)
	}
}

func doPair(client *http.Client) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ddctl pair <addr> [name]")
		os.Exit(1)
	}
	addr := os.Args[2]
	name := ""
	if len(os.Args) > 3 {
		name = os.Args[3]
	}

	body, _ := json.Marshal(api.PairRequest{Addr: addr, Name: name})
	result := postAction(client, "pair", body)
	printActionMessage(result)
}

func doUnpair(client *http.Client) {
	result := postAction(client, "unpair", nil)
	printActionMessage(result)
}

func doStatus(client *http.Client) {
	st := fetchState(client, "")
	printDeviceStatus(st)
}

func doConnected(client *http.Client) {
	st := fetchState(client, "")
	fmt.Printf("{\"connected\":%v}\n", st.Device.Connected)
}

func doBattery(client *http.Client) {
	st := fetchState(client, "")
	if st.Device.Battery == nil {
		fmt.Fprintln(os.Stderr, "error: no battery data available")
		os.Exit(1)
	}
	fmt.Printf("Battery: %d%%", st.Device.Battery.Level)
	if st.Device.Battery.Charging {
		fmt.Print(" (charging)")
	}
	fmt.Println()
}

func doSetTime(client *http.Client) {
	result := postAction(client, "sync-time", nil)
	printActionMessage(result)
}

func doFind(client *http.Client) {
	result := postAction(client, "find-device", nil)
	printActionMessage(result)
}

func fetchState(client *http.Client, query string) api.AppState {
	url := "http://localhost/api/state"
	if query != "" {
		url += "?" + query
	}
	resp, err := client.Get(url)
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
	var state api.AppState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return state
}

func postAction(client *http.Client, action string, body []byte) api.ActionResult {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	resp, err := client.Post("http://localhost/api/actions/"+action, "application/json", r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	var result api.ActionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return result
}

func printActionMessage(result api.ActionResult) {
	if result.Message != "" {
		fmt.Println(result.Message)
	}
}

func printDeviceStatus(st api.AppState) {
	dev := st.Device
	fmt.Printf("State:      %s\n", dev.State)
	fmt.Printf("Paired:     %s", dev.PairedAddr)
	if dev.PairedName != "" {
		fmt.Printf(" (%s)", dev.PairedName)
	}
	fmt.Println()
	fmt.Printf("Connected:  %v\n", dev.Connected)
	if dev.LastSeen != nil {
		fmt.Printf("Last Seen:  %s\n", dev.LastSeen.Local().Format(time.RFC3339))
	}
	if dev.Battery != nil {
		fmt.Printf("Battery:    %d%%", dev.Battery.Level)
		if dev.Battery.Charging {
			fmt.Print(" (charging)")
		}
		fmt.Println()
	}
	if dev.HeartRate > 0 {
		fmt.Printf("Heart Rate: %d bpm\n", dev.HeartRate)
	}
	if dev.SpO2 > 0 {
		fmt.Printf("SpO2:       %d%%\n", dev.SpO2)
	}
	if dev.Steps > 0 || dev.Calories > 0 || dev.Distance > 0 {
		fmt.Printf("Activity:   %d steps, %.1f kcal, %dm\n", dev.Steps, dev.Calories, dev.Distance)
	}
}
