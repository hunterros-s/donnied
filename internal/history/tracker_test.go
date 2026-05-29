package history

import (
	"testing"

	"smartwatch/protocol"
)

func TestNormalizeSpO2DaysUsesRequestedDay(t *testing.T) {
	in := []protocol.SpO2Day{{DaysAgo: 98, Samples: []protocol.SpO2Sample{{Min: 97, Max: 99}}}}
	out := normalizeSpO2Days(in, 2)
	if out[0].DaysAgo != 2 {
		t.Fatalf("DaysAgo = %d, want 2", out[0].DaysAgo)
	}
	if in[0].DaysAgo != 98 {
		t.Fatalf("input mutated: %#v", in)
	}
}
