package compiler

import (
	"strings"
	"testing"
)

// A process's isExecutable flag is carried onto the compiled process so the API
// can refuse to start a descriptive-only process. Absent defaults to executable
// (BPMN's convention and Atlas's existing behavior — every deployed process ran),
// so only an explicit isExecutable="false" makes a process non-executable.
func TestProcessExecutableFlag(t *testing.T) {
	cases := []struct {
		name string
		attr string // the isExecutable attribute text, "" to omit it entirely
		want bool
	}{
		{"explicit true", ` isExecutable="true"`, true},
		{"explicit false", ` isExecutable="false"`, false},
		{"absent defaults to executable", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <bpmn:process id="p"` + tc.attr + `>
    <bpmn:startEvent id="start"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
			cp, err := Parse(10, 1, strings.NewReader(xml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cp.IsExecutable(); got != tc.want {
				t.Errorf("IsExecutable() = %v, want %v", got, tc.want)
			}
		})
	}
}
