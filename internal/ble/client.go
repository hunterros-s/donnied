package ble

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"smartwatch/protocol"
	"tinygo.org/x/bluetooth"
)

func svcStrings(uuids []bluetooth.UUID) []string {
	out := make([]string, len(uuids))
	for i, u := range uuids {
		out[i] = u.String()
	}
	return out
}

const scanStopTimeout = 1 * time.Second

// knownAdvertisementUUIDs are service UUIDs that Y25/RS25 (Colmi R02 family)
// watches use in their BLE advertisements. The UART and V2 services are only
// discoverable after connecting.
var knownAdvertisementUUIDs = map[string]bool{
	protocol.UARTService: true,
	protocol.AdvService:  true,
}

// Client manages BLE adapter operations: scanning and connecting.
type Client struct {
	adapter *bluetooth.Adapter
	logger  *slog.Logger
}

// New creates a Client using the default BLE adapter.
func New(logger *slog.Logger) *Client {
	return &Client{
		adapter: bluetooth.DefaultAdapter,
		logger:  logger,
	}
}

// Enable enables the BLE adapter. Must be called before Scan or Connect.
func (c *Client) Enable() error {
	if err := c.adapter.Enable(); err != nil {
		return fmt.Errorf("enable ble adapter: %w", err)
	}
	return nil
}

// Scan discovers BLE peripherals advertising known watch service UUIDs.
// It blocks until ctx is cancelled, an error occurs, or the scan times out.
// Returns all matching peripherals seen during the scan.
func (c *Client) Scan(ctx context.Context) ([]ScanResult, error) {
	c.logger.Info("ble scan starting")

	var adCount atomic.Int64 // total advertisements received (any device)

	found := make(chan bluetooth.ScanResult, 32)
	scanDone := make(chan error, 1)

	var logged sync.Map // addr -> struct{}, to log each device once

	go func() {
		err := c.adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
			adCount.Add(1)

			if result.AdvertisementPayload == nil {
				return
			}

			svcs := result.AdvertisementPayload.ServiceUUIDs()

			// Log every unique device once at debug level.
			key := result.Address.String()
			if _, seen := logged.LoadOrStore(key, struct{}{}); !seen {
				c.logger.Debug("ble device",
					"addr", key,
					"name", result.LocalName(),
					"rssi", result.RSSI,
					"services", svcStrings(svcs),
				)
			}

			// Match devices advertising any known watch UUID.
			for _, uuid := range svcs {
				if knownAdvertisementUUIDs[uuid.String()] {
					select {
					case found <- result:
					default:
					}
					return
				}
			}
		})
		scanDone <- err
	}()

	stopScan := func() error {
		c.adapter.StopScan()
		select {
		case err := <-scanDone:
			return err
		case <-time.After(scanStopTimeout):
			return fmt.Errorf("timed out waiting for scan to stop")
		}
	}

	seen := make(map[string]ScanResult)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			err := stopScan()
			c.logger.Info("ble scan finished", "found", len(seen), "advertisements", adCount.Load(), "err", err)
			return mapToSlice(seen), err

		case err := <-scanDone:
			c.logger.Info("ble scan finished", "found", len(seen), "advertisements", adCount.Load(), "err", err)
			return mapToSlice(seen), err

		case res := <-found:
			key := res.Address.String()
			if _, ok := seen[key]; ok {
				continue
			}
			name := res.LocalName()
			if name == "" {
				name = "(unnamed)"
			}
			c.logger.Info("watch found", "addr", key, "name", name, "rssi", res.RSSI)
			seen[key] = ScanResult{
				Address: key,
				Name:    name,
				RSSI:    res.RSSI,
			}

		case <-ticker.C:
			// Allow extra results to accumulate.
		}
	}
}

func mapToSlice(m map[string]ScanResult) []ScanResult {
	out := make([]ScanResult, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}