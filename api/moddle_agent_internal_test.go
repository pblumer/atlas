package api

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestModdleKnowsAnAgentsDeclarations guards the two declarations an agent's element
// carries beside its <atlas:agentConnector>: a tool's parameters and an answer's fields.
// Neither is a *Connector element, so the connector drift test does not see them — and
// bpmn-js drops an extension element or attribute it has no type for, which would strip
// an authored answer shape the first time the model is saved from the Modeler
// (ADR-0567).
func TestModdleKnowsAnAgentsDeclarations(t *testing.T) {
	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var moddle struct {
		Types []struct {
			Name       string `json:"name"`
			Properties []struct {
				Name   string `json:"name"`
				IsAttr bool   `json:"isAttr"`
			} `json:"properties"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &moddle); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}
	// The attributes compiler/parse.go's xmlAgentParam reads for each element.
	want := map[string][]string{
		"AgentParam":  {"name", "type", "description", "required"},
		"AgentOutput": {"name", "type", "description", "required", "values"},
	}
	for typ, attrs := range want {
		var have []string
		found := false
		for _, t := range moddle.Types {
			if t.Name != typ {
				continue
			}
			found = true
			for _, p := range t.Properties {
				if p.IsAttr {
					have = append(have, p.Name)
				}
			}
		}
		if !found {
			t.Errorf("atlas-moddle.json declares no %s: a Modeler round trip would strip the element", typ)
			continue
		}
		for _, a := range attrs {
			if !slices.Contains(have, a) {
				t.Errorf("atlas-moddle.json's %s lacks the attribute %q the compiler reads", typ, a)
			}
		}
	}
}
