package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A definition deployed under ADR-0319 keeps the decision version it was frozen on
// (TestARecordFrozenAtDeployTimeKeepsItsVersion). These hold the other half: the
// listings say so, and say it about no other definition.

// listedProcesses reads GET /api/v1/processes keyed by definition key.
func listedProcesses(t *testing.T, x deployTestHarness) map[uint64]processResp {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: %d %s", code, b)
	}
	var list []processResp
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, b)
	}
	out := make(map[uint64]processResp, len(list))
	for _, p := range list {
		out[p.Key] = p
	}
	return out
}

// TestOnlyAFrozenDefinitionIsMarked: of four latest-or-version-bound definitions,
// only the one whose record ADR-0319 wrote reports frozen decisions — and it
// reports the version it evaluates, the version latest resolves to, and whether
// the two differ, before and after a newer version is deployed.
func TestOnlyAFrozenDefinitionIsMarked(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)
	appID, v1 := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))
	v1Key := v1.Decisions[0].Key

	frozen := deployProcess(t, first.x, eligibilityProcess("frozen", "latest"))
	following := deployProcess(t, first.x, eligibilityProcess("following", "latest"))
	fixed := deployProcess(t, first.x, eligibilityProcessAt("fixed", 1))
	legacy := deployProcess(t, first.x, eligibilityProcess("legacy", "latest"))
	first.shutdown()

	freezeDeployment(t, dir, frozen, "eligibility", v1Key)
	agePinnedDeployment(t, dir, legacy)

	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	procs := listedProcesses(t, second.x)
	got := procs[frozen].FrozenDecisions
	want := frozenDecisionResp{DecisionID: "eligibility", Key: v1Key, Version: 1, LatestKey: v1Key, LatestVersion: 1}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("frozen definition while v1 is newest: frozenDecisions = %+v, want [%+v]", got, want)
	}
	// A definition that follows latest, one that names its version on purpose and
	// one that resolves latest at activation all do what their binding reads, and
	// saying otherwise about them would be the false alarm that teaches the owner to
	// ignore the true one.
	for _, key := range []uint64{following, fixed, legacy} {
		if f := procs[key].FrozenDecisions; len(f) != 0 {
			t.Fatalf("definition %d (%s): frozenDecisions = %+v, want none", key, procs[key].ProcessID, f)
		}
	}

	uploadModel(t, second.x, "eligibility", eligibilityDMN("vip"))
	v2 := publishApp(t, second.x, appID)
	v2Key := v2.Decisions[0].Key

	got = listedProcesses(t, second.x)[frozen].FrozenDecisions
	want = frozenDecisionResp{DecisionID: "eligibility", Key: v1Key, Version: 1, LatestKey: v2Key, LatestVersion: 2, Behind: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("frozen definition after v2: frozenDecisions = %+v, want [%+v]", got, want)
	}
	// And the mark is the truth about what runs, not a label beside it.
	if verdict := runAndReadVerdict(t, second.x, frozen, "frozen"); verdict != "approve" {
		t.Fatalf("frozen definition after v2: verdict = %q, want approve (v1)", verdict)
	}
}

// TestAFrozenBundleIsMarkedOnceADecisionIsDeployed: under ADR-0319 a decision
// never deployed on its own froze latest on the model bundled with the process.
// That is what latest would still mean until the decision is deployed on its own;
// from then on the definition is behind.
func TestAFrozenBundleIsMarkedOnceADecisionIsDeployed(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)
	uploadModel(t, first.x, "eligibility", eligibilityDMN("approve"))
	first.x.addRef("", "Eligibility", "eligibility")
	key := deployProcess(t, first.x, eligibilityProcess("orders", "latest"))
	first.shutdown()

	freezeDeployment(t, dir, key, "eligibility", key)

	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	got := listedProcesses(t, second.x)[key].FrozenDecisions
	want := frozenDecisionResp{DecisionID: "eligibility", Key: key}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("frozen on its bundle, no decision deployment: frozenDecisions = %+v, want [%+v]", got, want)
	}

	_, rep := publishDecisionApp(t, second.x, "Order Management", "eligibility2", eligibilityDMN("vip"))
	got = listedProcesses(t, second.x)[key].FrozenDecisions
	want = frozenDecisionResp{DecisionID: "eligibility", Key: key, LatestKey: rep.Decisions[0].Key, LatestVersion: 1, Behind: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("frozen on its bundle after v1 was deployed: frozenDecisions = %+v, want [%+v]", got, want)
	}
}

// TestAVersionListingTellsAFrozenHolderFromAChosenOne: both hold a decision
// version against deletion, and they are released differently — a frozen one by
// deploying the process again, a chosen one only by changing the task. The
// listing says which, so the Console can say how.
func TestAVersionListingTellsAFrozenHolderFromAChosenOne(t *testing.T) {
	dir := t.TempDir()
	first := bootDecisionStack(t, dir)
	_, v1 := publishDecisionApp(t, first.x, "Order Management", "eligibility", eligibilityDMN("approve"))
	v1Key := v1.Decisions[0].Key
	frozen := deployProcess(t, first.x, eligibilityProcess("frozen", "latest"))
	fixed := deployProcess(t, first.x, eligibilityProcessAt("fixed", 1))
	first.shutdown()

	freezeDeployment(t, dir, frozen, "eligibility", v1Key)

	second := bootDecisionStack(t, dir)
	defer second.shutdown()

	rows := listDecisionDeployments(t, second.x, "?decisionId=eligibility")
	if len(rows) != 1 {
		t.Fatalf("listing = %+v, want v1 alone", rows)
	}
	bindings := map[uint64]string{}
	for _, p := range rows[0].PinnedBy {
		bindings[p.Key] = p.Binding
	}
	if bindings[frozen] != pinBindingLatest || bindings[fixed] != pinBindingVersion || len(bindings) != 2 {
		t.Fatalf("pinnedBy = %+v, want %d as %q and %d as %q", rows[0].PinnedBy, frozen, pinBindingLatest, fixed, pinBindingVersion)
	}
}
