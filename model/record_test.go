package model

import "testing"

func TestKeyRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		partition uint16
		counter   uint64
	}{
		{"zero", 0, 0},
		{"partition only", 7, 0},
		{"counter only", 0, 12345},
		{"both", 42, 9_000_000},
		{"max partition", 0xFFFF, 1},
		{"max counter", 1, counterMask},
		{"counter overflows mask", 3, counterMask + 1}, // high bits must be dropped
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := NewKey(tt.partition, tt.counter)
			if got := PartitionOf(key); got != tt.partition {
				t.Errorf("PartitionOf = %d, want %d", got, tt.partition)
			}
			wantCounter := tt.counter & counterMask
			if got := CounterOf(key); got != wantCounter {
				t.Errorf("CounterOf = %d, want %d", got, wantCounter)
			}
		})
	}
}

func TestKeyPartitionDoesNotLeakIntoCounter(t *testing.T) {
	// A full partition with a full counter must not corrupt each other.
	key := NewKey(0xFFFF, counterMask)
	if PartitionOf(key) != 0xFFFF {
		t.Errorf("PartitionOf = %d, want 65535", PartitionOf(key))
	}
	if CounterOf(key) != counterMask {
		t.Errorf("CounterOf = %d, want %d", CounterOf(key), counterMask)
	}
}

func TestStringerFallbacks(t *testing.T) {
	if got := RecordType(200).String(); got != "RecordType(?)" {
		t.Errorf("unknown RecordType String = %q", got)
	}
	if got := ValueType(200).String(); got != "ValueType(?)" {
		t.Errorf("unknown ValueType String = %q", got)
	}
	if got := Intent(200).String(); got != "Intent(?)" {
		t.Errorf("unknown Intent String = %q", got)
	}
	// A couple of known values for good measure.
	if got := RecordEvent.String(); got != "Event" {
		t.Errorf("RecordEvent String = %q", got)
	}
	if got := VTJob.String(); got != "Job" {
		t.Errorf("VTJob String = %q", got)
	}
	if got := IntentActivated.String(); got != "Activated" {
		t.Errorf("IntentActivated String = %q", got)
	}
}
