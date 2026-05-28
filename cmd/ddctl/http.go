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
	resp, err := client.Get("http://localhost/health")
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
	resp, err := client.Get("http://localhost/info")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var info infoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("PID:       %d\n", info.PID)
	fmt.Printf("Started:   %s\n", info.StartTime.Format(time.RFC3339))
	fmt.Printf("Uptime:    %s\n", info.Uptime)
	fmt.Printf("Device:    %v\n", info.DeviceConnected)
}

func doShutdown(client *http.Client) {
	resp, err := client.Post("http://localhost/shutdown", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doScan(client *http.Client) {
	resp, err := client.Get("http://localhost/device/scan")
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

	var results []scanResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

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

	body, _ := json.Marshal(map[string]string{"addr": addr, "name": name})
	resp, err := client.Post("http://localhost/device/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doUnpair(client *http.Client) {
	resp, err := client.Post("http://localhost/device/unpair", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doStatus(client *http.Client) {
	resp, err := client.Get("http://localhost/device/status")
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

	var st deviceStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("State:      %s\n", st.State)
	fmt.Printf("Paired:     %s", st.PairedAddr)
	if st.PairedName != "" {
		fmt.Printf(" (%s)", st.PairedName)
	}
	fmt.Println()
	fmt.Printf("Connected:  %v\n", st.Connected)
	if st.Battery != nil {
		fmt.Printf("Battery:    %d%%", st.Battery.Level)
		if st.Battery.Charging {
			fmt.Print(" (charging)")
		}
		fmt.Println()
	}
}

func doConnected(client *http.Client) {
	resp, err := client.Get("http://localhost/device/connected")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doBattery(client *http.Client) {
	resp, err := client.Get("http://localhost/device/battery")
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
	var battery batteryResponse
	if err := json.NewDecoder(resp.Body).Decode(&battery); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Battery: %d%%", battery.Level)
	if battery.Charging {
		fmt.Print(" (charging)")
	}
	fmt.Println()
}

func doSetTime(client *http.Client) {
	resp, err := client.Post("http://localhost/device/time", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func doFind(client *http.Client) {
	resp, err := client.Post("http://localhost/device/find", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}
