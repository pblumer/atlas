package dmn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestDecisionDeploymentIsItsOwnLineage is the registry half of
// ADR-0319: a decision deployed on its own
// (published from an application, with or without a process) is what
// LatestDecisionKey answers with, and a model bundled with a *process* deployment
// never moves that pointer. Before this the two were the same map, so deploying a
// process silently became "the newest version of the decision".
func TestDecisionDeploymentIsItsOwnLineage(t *testing.T) {
	reg := dmn.NewRegistry()

	// No decision deployed yet: nothing to select, so a deploy-time latest lookup
	// has to say so rather than guess.
	if key, ok := reg.LatestDecisionKey("Dish"); ok {
		t.Fatalf("LatestDecisionKey before any decision deployment = %d, true; want ok=false", key)
	}

	// A model bundled with a process deployment is reachable under that process's
	// key, but it is not a decision deployment.
	if err := reg.Deploy(7, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if key, ok := reg.LatestDecisionKey("Dish"); ok {
		t.Fatalf("a bundled model made itself the latest decision (key %d); want ok=false", key)
	}

	// Deploying the decision on its own is what establishes the lineage.
	if err := reg.DeployDecision(101, []byte(dishModel)); err != nil {
		t.Fatalf("DeployDecision v1: %v", err)
	}
	if key, ok := reg.LatestDecisionKey("Dish"); !ok || key != 101 {
		t.Fatalf("LatestDecisionKey = %d, %v; want 101, true", key, ok)
	}
	if err := reg.DeployDecision(102, []byte(dishModelV2)); err != nil {
		t.Fatalf("DeployDecision v2: %v", err)
	}
	if key, ok := reg.LatestDecisionKey("Dish"); !ok || key != 102 {
		t.Fatalf("LatestDecisionKey after v2 = %d, %v; want 102, true", key, ok)
	}

	// A process deployed after both decisions still does not move the pointer.
	if err := reg.Deploy(103, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy after decisions: %v", err)
	}
	if key, _ := reg.LatestDecisionKey("Dish"); key != 102 {
		t.Fatalf("LatestDecisionKey after a later process deploy = %d; want 102", key)
	}

	// Every version stays evaluable under its own key — that is what a pinned
	// binding resolves against.
	winter := map[string]any{"Season": "Winter"}
	for _, tc := range []struct {
		key  uint64
		want string
	}{{101, "Roastbeef"}, {102, "Steak"}, {7, "Roastbeef"}} {
		out, err := reg.Evaluate(context.Background(), tc.key, "Dish", winter)
		if err != nil || out["Dish"] != tc.want {
			t.Errorf("Evaluate(%d) = %v, %v; want %s", tc.key, out, err, tc.want)
		}
	}
}

// TestDecisionDeploymentKeepsLegacyLatest pins the compatibility half: definitions
// deployed before deploy-time pinning still call EvaluateLatest at activation, and
// that pointer must keep meaning what ADR-0063 said — the newest model registered,
// of either kind. A decision deployment is a newly deployed model providing the
// decision, so it moves it too.
func TestDecisionDeploymentKeepsLegacyLatest(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(1, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	winter := map[string]any{"Season": "Winter"}
	if out, err := reg.EvaluateLatest(context.Background(), "Dish", winter); err != nil || out["Dish"] != "Roastbeef" {
		t.Fatalf("EvaluateLatest after process deploy = %v, %v; want Roastbeef", out, err)
	}
	if err := reg.DeployDecision(2, []byte(dishModelV2)); err != nil {
		t.Fatalf("DeployDecision: %v", err)
	}
	if out, err := reg.EvaluateLatest(context.Background(), "Dish", winter); err != nil || out["Dish"] != "Steak" {
		t.Fatalf("EvaluateLatest after decision deploy = %v, %v; want Steak", out, err)
	}
}

// TestDeployDecisionRejectsBrokenModel proves the deploy-time gate: a model temis
// cannot compile is refused, and nothing about it becomes reachable. Publish
// depends on this — a refused decision must leave no lineage behind.
func TestDeployDecisionRejectsBrokenModel(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.DeployDecision(9, []byte("<definitions")); err == nil {
		t.Fatal("DeployDecision of malformed XML: got nil error, want a refusal")
	}
	// A model that parses but whose decision logic does not compile is refused too:
	// unlike a reload, a deploy has somewhere to send the author back to.
	broken := strings.Replace(dishModel, `<text>Season</text>`, `<text>Season +</text>`, 1)
	if err := reg.DeployDecision(10, []byte(broken)); err == nil {
		t.Fatal("DeployDecision of a model with error diagnostics: got nil error, want a refusal")
	}
	if _, ok := reg.LatestDecisionKey("Dish"); ok {
		t.Fatal("a refused decision deployment left a latest pointer behind")
	}
}

// TestReloadDecisionSurvivesNewDiagnostics mirrors Reload's ADR-0177 split for the
// decision store: a model that has stopped compiling *cleanly* since it was
// deployed comes back with its diagnostics reported rather than keeping the server
// from starting, because refusing it here undeploys nothing.
func TestReloadDecisionSurvivesNewDiagnostics(t *testing.T) {
	reg := dmn.NewRegistry()
	problems, err := reg.ReloadDecision(5, []byte(dishModel))
	if err != nil || problems != "" {
		t.Fatalf("ReloadDecision(clean) = %q, %v; want no problems and no error", problems, err)
	}
	if key, ok := reg.LatestDecisionKey("Dish"); !ok || key != 5 {
		t.Fatalf("LatestDecisionKey after reload = %d, %v; want 5, true", key, ok)
	}
	// Hard, unparseable XML is still an error: there is no model to bring back.
	if _, err := reg.ReloadDecision(6, []byte("<definitions")); err == nil {
		t.Fatal("ReloadDecision of malformed XML: got nil error, want an error")
	}
	// A model whose decision logic no longer compiles is registered with its
	// diagnostics rendered for the operator.
	broken := strings.Replace(dishModel, `<text>Season</text>`, `<text>Season +</text>`, 1)
	problems, err = reg.ReloadDecision(7, []byte(broken))
	if err != nil {
		t.Fatalf("ReloadDecision(broken logic): %v", err)
	}
	if problems == "" {
		t.Fatal("ReloadDecision(broken logic) reported no problems; want the diagnostics rendered")
	}
}
