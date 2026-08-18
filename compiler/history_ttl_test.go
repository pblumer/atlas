package compiler

import (
	"strings"
	"testing"
)

// The per-definition history TTL (atlas:historyTtl, an ISO-8601 duration, ADR-0144)
// is parsed to nanoseconds and carried onto the compiled process, where the retention
// sweep reads it as this definition's own max age. Absent yields 0 (fall back to the
// server-wide max age — the opt-in default). A malformed or non-positive value fails
// the deploy, exactly as the instance TTL does.
func TestProcessHistoryTTL(t *testing.T) {
	valid := []struct {
		name string
		attr string // the historyTtl attribute, "" to omit it
		want int64
	}{
		{"seconds", ` historyTtl="PT30S"`, int64(30e9)},
		{"days", ` historyTtl="P7D"`, int64(7*24*3600) * 1e9},
		{"hours-minutes", ` historyTtl="PT2H30M"`, int64((2*3600 + 30*60)) * 1e9},
		// The Modeler authors the attribute in the atlas namespace, like instanceTtl.
		{"namespaced", ` atlas:historyTtl="P30D"`, int64(30*24*3600) * 1e9},
		{"absent", "", 0},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cp.HistoryTtlNanos(); got != tc.want {
				t.Errorf("HistoryTtlNanos() = %d, want %d", got, tc.want)
			}
		})
	}

	invalid := []struct {
		name string
		attr string
	}{
		{"malformed", ` historyTtl="30 days"`},
		{"empty-duration", ` historyTtl="P"`},
		{"zero", ` historyTtl="PT0S"`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr))); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", tc.attr)
			}
		})
	}
}

// The two TTLs are orthogonal knobs (ADR-0144): one bounds how long an instance may
// run, the other how long its finished record is kept. A definition may carry either,
// both, or neither, and each compiles independently of the other.
func TestProcessTTLsAreIndependent(t *testing.T) {
	cp, err := Parse(10, 1, strings.NewReader(ttlXML(` instanceTtl="PT1M" historyTtl="P7D"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := cp.InstanceTtlNanos(), int64(60e9); got != want {
		t.Errorf("InstanceTtlNanos() = %d, want %d", got, want)
	}
	if got, want := cp.HistoryTtlNanos(), int64(7*24*3600)*1e9; got != want {
		t.Errorf("HistoryTtlNanos() = %d, want %d", got, want)
	}
}
