package api

import (
	"strings"
	"testing"
)

// TestMimImpactSummary covers the one-line reason a caller shows when it renders
// nothing else: each combination has to say something true.
func TestMimImpactSummary(t *testing.T) {
	draft := &mimDraftImpact{Name: "Onboarding"}
	deployed := &mimDeployedImpact{Version: 3}
	for _, tc := range []struct {
		name string
		in   mimImpact
		want string
	}{
		{"both", mimImpact{ProcessID: "p", Draft: draft, Deployed: deployed},
			"p: replaces an existing draft and shares its id with deployed version 3"},
		{"draft only", mimImpact{ProcessID: "p", Draft: draft},
			"p: replaces an existing draft"},
		{"deployed only", mimImpact{ProcessID: "p", Deployed: deployed},
			"p: shares its id with deployed version 3"},
		{"free", mimImpact{ProcessID: "p"}, "p: nothing holds this id"},
	} {
		if got := mimImpactSummary(tc.in); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestConflictReasonNamesTheInstancesAtRisk: the running instances are the part
// worth putting in a single line, and only when elements they stand on are gone.
func TestConflictReasonNamesTheInstancesAtRisk(t *testing.T) {
	safe := mimImpact{ProcessID: "p", Deployed: &mimDeployedImpact{Version: 1, ActiveInstances: 4, KeptElements: 9}}
	if got := conflictReason([]mimImpact{safe}); got != "p: shares its id with deployed version 1" {
		t.Errorf("instances that keep their elements are not at risk: %q", got)
	}

	risky := mimImpact{ProcessID: "p", Deployed: &mimDeployedImpact{
		Version: 1, ActiveInstances: 4, DroppedElements: []string{"a"}}}
	got := conflictReason([]mimImpact{risky})
	if want := "4 running instance(s) stand on elements this model does not have"; !strings.Contains(got, want) {
		t.Errorf("conflictReason = %q, want it to mention %q", got, want)
	}

	many := conflictReason([]mimImpact{risky, risky})
	if want := "2 of the imported workflows"; !strings.Contains(many, want) {
		t.Errorf("conflictReason = %q, want it to count them", many)
	}
}

// TestDuplicateProcessID covers the collision inside one import: two workflows
// that would be saved onto the same id.
func TestDuplicateProcessID(t *testing.T) {
	if got := duplicateProcessID([]candidateOf{{pid: "a"}, {pid: "b"}}); got != "" {
		t.Errorf("distinct ids are no collision, got %q", got)
	}
	if got := duplicateProcessID([]candidateOf{{pid: "a"}, {pid: "b"}, {pid: "a"}}); got != "a" {
		t.Errorf("duplicateProcessID = %q, want a", got)
	}
}

// TestLatestDeploymentOfNothing: an id nothing is deployed under has no impact to
// report, which is the common case on a first import.
func TestLatestDeploymentOfNothing(t *testing.T) {
	s := &Server{deployments: map[uint64]*deployment{}}
	if d := s.latestDeploymentOf("nobody"); d != nil {
		t.Errorf("latestDeploymentOf = %+v, want nil", d)
	}
}

// TestIdentifyModelRefusesRubbish: a model that does not compile declares nothing
// this preflight can credit it with, and says so rather than guessing.
func TestIdentifyModelRefusesRubbish(t *testing.T) {
	if _, err := identifyModel([]byte("not bpmn at all <<<")); err == nil {
		t.Error("expected an error for a model that does not compile")
	}
	id, err := identifyModel([]byte(sampleBPMNForPreflight))
	if err != nil {
		t.Fatalf("identifyModel: %v", err)
	}
	if !id.elements["task"] || !id.elements["start"] {
		t.Errorf("elements = %v, want the model's own ids", id.elements)
	}
}

const sampleBPMNForPreflight = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="start"/><task id="task"/><endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`
