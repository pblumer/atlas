package compiler

import (
	"strings"
	"testing"
)

// The process's version tag (atlas:versionTag, an optional revision label authored
// in the Modeler) is carried onto the compiled process so Operations can show it
// beside the deploy version. Absent yields "".
func TestProcessVersionTag(t *testing.T) {
	cases := []struct {
		name string
		attr string // the atlas:versionTag attribute, "" to omit it
		want string
	}{
		{"set", ` atlas:versionTag="1.4.0"`, "1.4.0"},
		{"absent", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
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
			if got := cp.VersionTag(); got != tc.want {
				t.Errorf("VersionTag() = %q, want %q", got, tc.want)
			}
		})
	}
}
