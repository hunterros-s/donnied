package main

import (
	"fmt"
	"net/http"
	"os"
)

const usage = "usage: ddctl <health|info|shutdown|scan|pair|unpair|status|connected|battery|time|find|hr-log|steps|sleep|sleep-status|sleep-history|sync|spo2|realtime|realtime-start|realtime-stop>"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	dispatch(os.Args[1], newHTTPClient())
}

func dispatch(command string, client *http.Client) {
	switch command {
	case "health":
		doHealth(client)
	case "info":
		doInfo(client)
	case "shutdown":
		doShutdown(client)
	case "scan":
		doScan(client)
	case "pair":
		doPair(client)
	case "unpair":
		doUnpair(client)
	case "status":
		doStatus(client)
	case "connected":
		doConnected(client)
	case "battery":
		doBattery(client)
	case "time":
		doSetTime(client)
	case "find":
		doFind(client)
	case "hr-log":
		doHRLog(client)
	case "steps":
		doSteps(client)
	case "sleep":
		doSleep(client)
	case "sleep-status":
		doSleepStatus(client)
	case "sleep-history":
		doSleepHistory(client)
	case "sync":
		doSync(client)
	case "spo2":
		doSpO2(client)
	case "realtime":
		doRealtime(client)
	case "realtime-start":
		doRealtimeStart(client)
	case "realtime-stop":
		doRealtimeStop(client)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}
}
