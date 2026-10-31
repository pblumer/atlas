package safetyinspection

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The demo is an executable BPMN example, not just the synthetic E2E form fixture.
func TestSafetyInspectionCompiles(t *testing.T) {
	model, err := os.ReadFile("safety-inspection.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Parse(1, 1, bytes.NewReader(model)); err != nil {
		t.Fatalf("safety inspection does not compile: %v", err)
	}
}

func TestSafetyInspectionFormAndFailClosedRouting(t *testing.T) {
	raw, err := os.ReadFile("safety-inspection.form.json")
	if err != nil {
		t.Fatal(err)
	}
	var form struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Components []struct {
			Key      string `json:"key"`
			Validate struct {
				Required bool `json:"required"`
			} `json:"validate"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &form); err != nil {
		t.Fatal(err)
	}
	if form.ID != "mfe1-safety-inspection" || form.Type != "default" {
		t.Fatalf("unexpected form identity: %q (%s)", form.ID, form.Type)
	}
	required := map[string]bool{
		"site": false, "asset": false, "inspector": false, "safetyCheckResult": false,
	}
	for _, field := range form.Components {
		if _, ok := required[field.Key]; ok {
			required[field.Key] = field.Validate.Required
		}
	}
	for key, valid := range required {
		if !valid {
			t.Errorf("required safety-inspection field %q missing or optional", key)
		}
	}
	model, err := os.ReadFile("safety-inspection.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	xml := string(model)
	for _, want := range []string{
		`formId="mfe1-safety-inspection"`,
		`default="flow_finding"`,
		`= safetyCheckResult = "ok"`,
		`sourceRef="gateway_result" targetRef="end_passed"`,
		`sourceRef="gateway_result" targetRef="end_finding"`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("process missing %s", want)
		}
	}
}
