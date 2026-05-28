package protocol

import "testing"

func TestParseSpO2DataSingleDayPairs(t *testing.T) {
	frame := BuildV2Packet(byte(V2SpO2), []byte{
		0x00, 0x00, // unknown, daysAgo
		97, 98,
		98, 99,
		99, 97, // normalize min/max ordering defensively
		0, 98, // invalid/missing sample, skipped
	})

	days := ParseSpO2Data(frame)
	if len(days) != 1 {
		t.Fatalf("len(days) = %d, want 1", len(days))
	}
	if days[0].DaysAgo != 0 {
		t.Fatalf("daysAgo = %d, want 0", days[0].DaysAgo)
	}
	want := []SpO2Sample{{Min: 97, Max: 98}, {Min: 98, Max: 99}, {Min: 97, Max: 99}}
	if len(days[0].Samples) != len(want) {
		t.Fatalf("samples = %#v, want %#v", days[0].Samples, want)
	}
	for i := range want {
		if days[0].Samples[i] != want[i] {
			t.Fatalf("sample %d = %#v, want %#v", i, days[0].Samples[i], want[i])
		}
	}
}
