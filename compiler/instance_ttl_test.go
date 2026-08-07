package compiler

import (
	"strings"
	"testing"
)

// The per-definition instance TTL (atlas:instanceTtl, an ISO-8601 duration, ADR-0085)
// is parsed to nanoseconds and carried onto the compiled process. Absent yields 0
// (no TTL — the opt-in default). A malformed or non-positive value fails the deploy.
func TestProcessInstanceTTL(t *testing.T) {
	valid := []struct {
		name string
		attr string // the instanceTtl attribute, "" to omit it
		want int64
	}{
		{"seconds", ` instanceTtl="PT30S"`, int64(30e9)},
		{"days", ` instanceTtl="P7D"`, int64(7*24*3600) * 1e9},
		{"hours-minutes", ` instanceTtl="PT2H30M"`, int64((2*3600 + 30*60)) * 1e9},
		{"absent", "", 0},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cp.InstanceTtlNanos(); got != tc.want {
				t.Errorf("InstanceTtlNanos() = %d, want %d", got, tc.want)
			}
		})
	}

	invalid := []struct {
		name string
		attr string
	}{
		{"malformed", ` instanceTtl="7 days"`},
		{"empty-duration", ` instanceTtl="P"`},
		{"zero", ` instanceTtl="PT0S"`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr))); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", tc.attr)
			}
		})
	}
}

func ttlXML(attr string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p"` + attr + `>
    <bpmn:startEvent id="start"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
}
