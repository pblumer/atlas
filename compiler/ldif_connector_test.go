package compiler

import (
	"strings"
	"testing"
)

// ldifTaskBPMN builds a one-task model from raw <atlas:ldifConnector> attributes.
func ldifTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:ldifConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func TestParseLdifConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(ldifTaskBPMN(
		`format="ldif" source="ldifText" resultVariable="eintraege"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != LdifJobType {
		t.Errorf("jobType = %q, want %q", got, LdifJobType)
	}
	if d.JobType != LdifJobTypeIndex {
		t.Errorf("jobType index = %d, want %d", d.JobType, LdifJobTypeIndex)
	}
	if got := cp.Intern(d.LdifFormat); got != "ldif" {
		t.Errorf("format = %q", got)
	}
	// Reading is the default direction.
	if got := cp.Intern(d.LdifOperation); got != "read" {
		t.Errorf("operation = %q, want read", got)
	}
	if cp.Intern(d.LdifSource) != "ldifText" || cp.Intern(d.LdifResult) != "eintraege" {
		t.Errorf("source/result = %q / %q", cp.Intern(d.LdifSource), cp.Intern(d.LdifResult))
	}
	// A file carries no endpoint and no credential.
	if d.Connector != -1 || d.Auth != -1 {
		t.Errorf("connector/auth = %d/%d, want -1/-1", d.Connector, d.Auth)
	}
}

func TestLdifConnectorValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		// There is deliberately no default format: guessing from the bytes is how a
		// malformed file becomes a plausible-looking empty result.
		{"no format", `source="f" resultVariable="r"`, "needs a format"},
		{"unknown format", `format="csv" source="f" resultVariable="r"`, "unknown format"},
		{"unknown operation", `format="ldif" operation="append" source="f" resultVariable="r"`, "unknown operation"},
		{"no source", `format="ldif" resultVariable="r"`, "source variable"},
		{"no result", `format="ldif" source="f"`, "resultVariable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(ldifTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The reserved index is baked into jobs already on disk, so it may never move.
func TestLdifJobTypeIndexIsReserved(t *testing.T) {
	if LdifJobTypeIndex != 24 {
		t.Errorf("LdifJobTypeIndex = %d, want 24", LdifJobTypeIndex)
	}
	if got := reservedJobTypes[LdifJobTypeIndex]; got != LdifJobType {
		t.Errorf("reservedJobTypes[%d] = %q, want %q", LdifJobTypeIndex, got, LdifJobType)
	}
}
