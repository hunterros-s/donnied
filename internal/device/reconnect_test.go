package device

import (
	"io"
	"log/slog"
	"testing"

	"smartwatch/internal/ble"
	"smartwatch/internal/config"
)

func TestMatchPairedAdvertisementExactAddress(t *testing.T) {
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	adv := m.matchPairedAdvertisement(config.Config{PairedAddr: "AA:BB", PairedName: "R02"}, []ble.ScanResult{
		{Address: "aa:bb", Name: "Different", RSSI: -60},
	})
	if !adv.found || adv.repaired || adv.address != "aa:bb" {
		t.Fatalf("adv = %#v, want exact non-repaired match", adv)
	}
}

func TestMatchPairedAdvertisementRepairsSingleNameMatch(t *testing.T) {
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	adv := m.matchPairedAdvertisement(config.Config{PairedAddr: "old", PairedName: "R02"}, []ble.ScanResult{
		{Address: "new", Name: "R02", RSSI: -55},
	})
	if !adv.found || !adv.repaired || adv.address != "new" || adv.name != "R02" {
		t.Fatalf("adv = %#v, want repaired name match", adv)
	}
}

func TestMatchPairedAdvertisementDoesNotRepairAmbiguousOrUnnamed(t *testing.T) {
	m := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ambiguous := m.matchPairedAdvertisement(config.Config{PairedAddr: "old", PairedName: "R02"}, []ble.ScanResult{
		{Address: "new-1", Name: "R02", RSSI: -55},
		{Address: "new-2", Name: "r02", RSSI: -70},
	})
	if ambiguous.found || ambiguous.repaired {
		t.Fatalf("ambiguous adv = %#v, want no repair", ambiguous)
	}

	unnamed := m.matchPairedAdvertisement(config.Config{PairedAddr: "old", PairedName: "(unnamed)"}, []ble.ScanResult{
		{Address: "new", Name: "(unnamed)", RSSI: -55},
	})
	if unnamed.found || unnamed.repaired {
		t.Fatalf("unnamed adv = %#v, want no repair", unnamed)
	}
}
