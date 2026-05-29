package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func doSleepStatus(client *http.Client) {
	resp, err := client.Get("http://localhost/sleep/status")
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
	var status normalizedSleepStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	printSleepStatus(status)
}

func doSleepHistory(client *http.Client) {
	q := url.Values{}
	if len(os.Args) > 2 {
		q.Set("from", os.Args[2])
	}
	if len(os.Args) > 3 {
		q.Set("to", os.Args[3])
	}
	endpoint := "http://localhost/sleep/sessions"
	if enc := q.Encode(); enc != "" {
		endpoint += "?" + enc
	}
	resp, err := client.Get(endpoint)
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
	var sessions []normalizedSleepSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Println("No persisted sleep data.")
		return
	}
	for i, s := range sessions {
		if i > 0 {
			fmt.Println()
		}
		printNormalizedSleepSession(i+1, s)
	}
}

func doSleepSync(client *http.Client) {
	resp, err := client.Post("http://localhost/sleep/sync", "text/plain", strings.NewReader(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Println()
		os.Exit(1)
	}
	fmt.Println()
}

func printSleepStatus(status normalizedSleepStatus) {
	fmt.Printf("state: %s\n", status.State)
	if status.Stage != "" {
		fmt.Printf("stage: %s\n", status.Stage)
	}
	if status.Since != nil {
		fmt.Printf("since: %s\n", status.Since.Local().Format("2006-01-02 15:04"))
	}
	if status.LastSync != nil {
		fmt.Printf("last sync: %s\n", status.LastSync.Local().Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("stale: %v\n", status.Stale)
}

func printNormalizedSleepSession(n int, s normalizedSleepSession) {
	start := s.Start.Local()
	end := s.End.Local()
	fmt.Printf("Sleep session %d [%s]\n", n, s.Source)
	fmt.Printf("  %s → %s  (%s)\n", start.Format("Mon 2006-01-02 15:04"), end.Format("Mon 15:04"), formatMinutes(s.TotalMinutes))
	fmt.Println("  segments:")
	for _, seg := range s.Segments {
		fmt.Printf("    %s  %-7s  %-6s  %s\n", seg.Start.Local().Format("15:04"), seg.Stage, seg.State, formatMinutes(seg.Minutes))
	}
}
