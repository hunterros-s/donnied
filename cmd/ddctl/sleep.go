package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"smartwatch/internal/api"
)

func doSleepStatus(client *http.Client) {
	state := fetchState(client, "")
	printSleepStatus(state.Sleep)
}

func doSleepHistory(client *http.Client) {
	q := url.Values{}
	q.Set("history", "true")
	if len(os.Args) > 2 {
		q.Set("from", os.Args[2])
	}
	if len(os.Args) > 3 {
		q.Set("to", os.Args[3])
	}
	state := fetchState(client, q.Encode())
	sessions := state.History.SleepSessions
	if len(sessions) == 0 {
		printHistoryErrors(state, "sleep_sessions")
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

func doSync(client *http.Client) {
	var body []byte
	if len(os.Args) > 2 {
		body, _ = json.Marshal(api.SyncRequest{Kinds: os.Args[2:]})
	}
	result := postAction(client, "sync", body)
	printActionMessage(result)
}

func printSleepStatus(status api.SleepState) {
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
	if status.Error != "" {
		fmt.Printf("error: %s\n", status.Error)
	}
}

func printNormalizedSleepSession(n int, s api.SleepSession) {
	start := s.Start.Local()
	end := s.End.Local()
	fmt.Printf("Sleep session %d [%s]\n", n, s.Source)
	fmt.Printf("  %s → %s  (%s)\n", start.Format("Mon 2006-01-02 15:04"), end.Format("Mon 15:04"), formatMinutes(s.TotalMinutes))
	fmt.Println("  segments:")
	for _, seg := range s.Segments {
		fmt.Printf("    %s  %-7s  %-6s  %s\n", seg.Start.Local().Format("15:04"), seg.StageName, seg.State, formatMinutes(seg.Minutes))
	}
}
