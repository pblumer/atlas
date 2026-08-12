package conformance

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestGoldenRendersEmpty covers the no-path / no-vars branches of the golden
// serializer, which the self-completing scenarios never hit.
func TestGoldenRendersEmpty(t *testing.T) {
	got := RunResult{State: model.PIActive}.Golden()
	want := "state: active\npath: (none)\nvars: (none)\n"
	if got != want {
		t.Errorf("empty Golden()\n got: %q\nwant: %q", got, want)
	}
}

// TestEffectDropsPath proves the metamorphic projection ignores control flow: two
// results with identical state and variables but different paths are equal under
// Effect, while a variable difference separates them.
func TestEffectDropsPath(t *testing.T) {
	a := RunResult{State: model.PICompleted, Path: []string{"start", "x", "end"}, Variables: map[string]string{"v": "1"}}
	b := RunResult{State: model.PICompleted, Path: []string{"start", "fork", "x", "join", "end"}, Variables: map[string]string{"v": "1"}}
	if a.Effect() != b.Effect() {
		t.Errorf("Effect should ignore path: %q vs %q", a.Effect(), b.Effect())
	}
	c := RunResult{State: model.PICompleted, Variables: map[string]string{"v": "2"}}
	if a.Effect() == c.Effect() {
		t.Errorf("Effect should reflect variables: both %q", a.Effect())
	}
}

// TestCoverageGapsReported checks that a feature or pattern with no covering
// scenario surfaces in the Gaps section with a not-covered mark.
func TestCoverageGapsReported(t *testing.T) {
	features := []Feature{
		{"covered", "Covered feature", []string{"P-1"}},
		{"orphan", "Uncovered feature", []string{"P-2"}},
	}
	patterns := []Pattern{{"P-1", "One"}, {"P-2", "Two"}}
	scenarios := []Scenario{{Name: "s1", Model: "m", Features: []string{"covered"}}}

	negatives := []NegativeModel{{Name: "neg-x", Model: "x.bpmn", Reason: "x is invalid"}}
	report := buildCoverage(features, patterns, scenarios, negatives)
	if !strings.Contains(report, "- feature: orphan") {
		t.Errorf("missing uncovered feature in Gaps:\n%s", report)
	}
	if !strings.Contains(report, "- pattern: P-2") {
		t.Errorf("missing uncovered pattern in Gaps:\n%s", report)
	}
	if strings.Contains(report, "- feature: covered") {
		t.Errorf("covered feature must not appear in Gaps:\n%s", report)
	}
	if !strings.Contains(report, tick(false)) {
		t.Errorf("expected a not-covered mark in the report:\n%s", report)
	}
	if !strings.Contains(report, "neg-x") || !strings.Contains(report, "x is invalid") {
		t.Errorf("expected the negative model listed with its reason:\n%s", report)
	}
}

// TestRegisterConsistency guards the register itself: every scenario names a
// registered feature and an embedded model, and every feature's patterns exist.
func TestRegisterConsistency(t *testing.T) {
	featureIDs := map[string]bool{}
	for _, f := range Features {
		featureIDs[f.ID] = true
	}
	patternIDs := map[string]bool{}
	for _, p := range Patterns {
		patternIDs[p.ID] = true
	}
	for _, f := range Features {
		for _, p := range f.Patterns {
			if !patternIDs[p] {
				t.Errorf("feature %q references unknown pattern %q", f.ID, p)
			}
		}
	}
	for _, sc := range Scenarios {
		if _, err := sc.load(); err != nil {
			t.Errorf("scenario %q: model not embedded: %v", sc.Name, err)
		}
		for _, f := range sc.Features {
			if !featureIDs[f] {
				t.Errorf("scenario %q references unknown feature %q", sc.Name, f)
			}
		}
	}
}
