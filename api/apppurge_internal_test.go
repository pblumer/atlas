package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Purging an application deletes everything it owns on this server
// (ADR-0468). These tests need the
// decision store and a restart, so they drive the whole stack from inside the
// package.

// purgePreview is the JSON shape of GET /applications/{id}/deletion-preview, as far
// as these tests read it.
type purgePreview struct {
	Processes []struct {
		ProcessID string `json:"processId"`
		Versions  []struct {
			Key     uint64 `json:"key"`
			Running int    `json:"running"`
		} `json:"versions"`
	} `json:"processes"`
	RunningInstances    int `json:"runningInstances"`
	DecisionDeployments []struct {
		Key uint64 `json:"key"`
	} `json:"decisionDeployments"`
	Drafts             []struct{ ID string } `json:"drafts"`
	DecisionReferences []struct{ ID string } `json:"decisionReferences"`
	Forms              []struct{ ID string } `json:"forms"`
	DecisionDrafts     []struct{ ID string } `json:"decisionDrafts"`
	Scenarios          []struct{ ID string } `json:"scenarios"`
	DependentProcesses []struct {
		ProcessID string `json:"processId"`
	} `json:"dependentProcesses"`
	Refusal string `json:"refusal"`
}

func readPurgePreview(t *testing.T, x deployTestHarness, appID string) purgePreview {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/applications/"+appID+"/deletion-preview", "")
	if code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, b)
	}
	var p purgePreview
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("decode preview: %v (%s)", err, b)
	}
	return p
}

// TestPurgeRemovesEverythingTheApplicationOwns is the reported scenario end to end:
// an application with a published decision, a deployed process holding a running
// instance, a draft, a form and a decision reference. The preview lists all of it;
// the purge removes all of it, the instance included, and a restart does not bring
// any of it back. Another application's draft is untouched.
func TestPurgeRemovesEverythingTheApplicationOwns(t *testing.T) {
	dir := t.TempDir()
	st := bootDecisionStack(t, dir)
	x := st.x

	appID, _ := publishDecisionApp(t, x, "Purged", "purge-elig", eligibilityDMN("approve"))
	x.saveDraft(appID, eligibilityProcess("purge-proc", "latest"))
	if code, b := x.do(http.MethodPost, "/api/v1/forms",
		`{"id":"purge-form","name":"Purge form","projectId":"`+appID+`","schema":{"type":"default"}}`); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, b)
	}
	draftBody, _ := json.Marshal(map[string]string{"projectId": appID, "xml": eligibilityDMN("work in progress")})
	if code, b := x.do(http.MethodPost, "/api/v1/dmn-drafts", string(draftBody)); code != http.StatusOK {
		t.Fatalf("save decision draft: %d %s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/playground/scenarios",
		`{"id":"purge-scenario","name":"Purge scenario","processId":"purge-proc","projectId":"`+appID+`",`+
			`"spec":{"open":{"source":"draft","ref":"purge-proc"},"run":{"cases":[{"n":1}]}}}`); code != http.StatusOK {
		t.Fatalf("save scenario: %d %s", code, b)
	}
	publishApp(t, x, appID)

	var procKey uint64
	st.srv.do(func() {
		for k, d := range st.srv.deployments {
			if d.ProcessID == "purge-proc" {
				procKey = k
			}
		}
	})
	if procKey == 0 {
		t.Fatal("purge-proc was not deployed by the publish")
	}
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", procKey), "{}"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, b)
	}

	kept := x.mkProject("Kept")
	x.saveDraft(kept, strings.Replace(deployableBPMN, `id="approve"`, `id="kept-proc"`, 1))

	p := readPurgePreview(t, x, appID)
	if len(p.Processes) != 1 || p.Processes[0].ProcessID != "purge-proc" || p.RunningInstances != 1 {
		t.Fatalf("preview processes = %+v running = %d, want purge-proc with 1 running", p.Processes, p.RunningInstances)
	}
	if len(p.DecisionDeployments) == 0 || len(p.Drafts) != 1 || len(p.Forms) != 1 || len(p.DecisionReferences) != 1 ||
		len(p.DecisionDrafts) != 1 || len(p.Scenarios) != 1 {
		t.Fatalf("preview = %+v, want decision deployments and one draft, form, reference, decision draft and scenario", p)
	}
	if p.Refusal != "" {
		t.Fatalf("preview refuses: %s", p.Refusal)
	}

	if code, b := x.do(http.MethodDelete, "/api/v1/applications/"+appID+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", code, b)
	}

	assertGone := func(st *decisionStack, when string) {
		t.Helper()
		st.srv.do(func() {
			if _, ok, _ := st.srv.projects.Get(appID); ok {
				t.Errorf("%s: the application is still there", when)
			}
			for _, d := range st.srv.deployments {
				if d.ProjectID == appID {
					t.Errorf("%s: deployment %s v%d is still there", when, d.ProcessID, d.Version)
				}
			}
			decisions, _ := st.srv.decisionDeploys.LoadAll()
			for _, rec := range decisions {
				if rec.ApplicationID == appID {
					t.Errorf("%s: decision deployment %d is still there", when, rec.Key)
				}
			}
			if _, ok, _ := st.srv.drafts.Get("purge-proc"); ok {
				t.Errorf("%s: the draft is still there", when)
			}
			if _, ok, _ := st.srv.forms.Get("purge-form"); ok {
				t.Errorf("%s: the form is still there", when)
			}
			if _, ok, _ := st.srv.playgroundScenarios.Get("purge-scenario"); ok {
				t.Errorf("%s: the scenario is still there", when)
			}
			dmnDrafts, _ := st.srv.dmnDrafts.LoadAll()
			for _, d := range dmnDrafts {
				if d.ProjectID == appID {
					t.Errorf("%s: decision draft %s is still there", when, d.ID)
				}
			}
			refs, _ := st.srv.dmnrefs.LoadAll()
			for _, rec := range refs {
				if rec.ProjectID == appID {
					t.Errorf("%s: reference %s is still there", when, rec.ID)
				}
			}
			if _, ok, _ := st.srv.drafts.Get("kept-proc"); !ok {
				t.Errorf("%s: another application's draft went too", when)
			}
		})
		if _, b := st.x.do(http.MethodGet, "/api/v1/dmn-models", ""); strings.Contains(string(b), "purge-elig") {
			t.Errorf("%s: the model file is still listed: %s", when, b)
		}
		_, b := st.x.do(http.MethodGet, "/api/v1/instances", "")
		if strings.Contains(string(b), `"state":"active"`) {
			t.Errorf("%s: an instance is still active: %s", when, b)
		}
	}
	assertGone(st, "after the purge")

	st.shutdown()
	again := bootDecisionStack(t, dir)
	defer again.shutdown()
	assertGone(again, "after a restart")
}

// TestPurgeEndsOnlyTheProcessesItTakesTheLastVersionOf: an order starts the newest
// surviving version of a process id, so the preview counts what the catalogue binds
// to a process as stopping only when every deployed version of it goes. A version
// outside the set keeps the process startable. (Every version of a process id is
// filed under one application, so the set here is built by hand, the way records
// written before that rule can still leave it.)
func TestPurgeEndsOnlyTheProcessesItTakesTheLastVersionOf(t *testing.T) {
	st := bootDecisionStack(t, t.TempDir())
	defer st.shutdown()
	first := deployProcess(t, st.x, deployableBPMN)
	second := deployProcess(t, st.x, deployableBPMN)

	st.srv.do(func() {
		one := applicationInventory{versions: []*deployment{st.srv.deployments[first]}}
		if ending := st.srv.lastVersionsOnLoop(one); len(ending) != 0 {
			t.Errorf("with a version left, ending = %v, want none", ending)
		}
		both := applicationInventory{versions: []*deployment{st.srv.deployments[first], st.srv.deployments[second]}}
		if ending := st.srv.lastVersionsOnLoop(both); !ending["approve"] {
			t.Errorf("with every version going, ending = %v, want approve", ending)
		}
	})

	// A key already gone by the time the removal runs is what was asked for.
	if n, err := st.srv.removeDeployments([]uint64{987654321}, true); n != 0 || err != nil {
		t.Fatalf("removing an absent key = (%d, %v), want (0, nil)", n, err)
	}
}

// TestPurgeNamesTheProcessesOutsideThatDependOnIt: a process of no application
// bound to version 1 of the decision the application published (ADR-0423) is pinned
// to it, while one bound to latest carries its own copy and is not. The preview
// names the first only; the purge goes through anyway, and leaves both deployed.
func TestPurgeNamesTheProcessesOutsideThatDependOnIt(t *testing.T) {
	dir := t.TempDir()
	st := bootDecisionStack(t, dir)
	x := st.x

	appID, _ := publishDecisionApp(t, x, "Provider", "provider-elig", eligibilityDMN("approve"))
	outsider := deployProcess(t, x, eligibilityProcessAt("outsider", 1))
	deployProcess(t, x, eligibilityProcess("self-sufficient", "latest"))

	p := readPurgePreview(t, x, appID)
	if len(p.DependentProcesses) != 1 || p.DependentProcesses[0].ProcessID != "outsider" {
		t.Fatalf("dependent processes = %+v, want outsider", p.DependentProcesses)
	}
	if code, b := x.do(http.MethodDelete, "/api/v1/applications/"+appID+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", code, b)
	}
	st.srv.do(func() {
		if _, ok := st.srv.deployments[outsider]; !ok {
			t.Error("the outside process was deleted with the application")
		}
	})

	// A definition pinned to a decision that is gone must not stop the server from
	// starting: every stored definition is reloaded at boot (ADR-0177).
	st.shutdown()
	again := bootDecisionStack(t, dir)
	defer again.shutdown()
	again.srv.do(func() {
		if _, ok := again.srv.deployments[outsider]; !ok {
			t.Error("the outside process did not come back after a restart")
		}
	})
}

// TestPurgeRefusesToReuseADecisionVersion: one decision id, v1 published from one
// application and v2 from another. Purging the second would lower the version
// counter under the surviving v1, so the next deploy would mint v2 again — refused,
// before anything is deleted. Purging the first removes only an older version and
// goes through.
func TestPurgeRefusesToReuseADecisionVersion(t *testing.T) {
	st := bootDecisionStack(t, t.TempDir())
	defer st.shutdown()
	x := st.x

	older, _ := publishDecisionApp(t, x, "Older", "elig-older", eligibilityDMN("v1"))
	newer, _ := publishDecisionApp(t, x, "Newer", "elig-newer", eligibilityDMN("v2"))

	if p := readPurgePreview(t, x, newer); !strings.Contains(p.Refusal, "eligibility v2") {
		t.Fatalf("preview refusal = %q, want it to name eligibility v2", p.Refusal)
	}
	code, b := x.do(http.MethodDelete, "/api/v1/applications/"+newer+"?purge=true", "")
	if code != http.StatusConflict || !strings.Contains(string(b), "eligibility v2") {
		t.Fatalf("purge newer: %d %s, want 409 naming eligibility v2", code, b)
	}
	st.srv.do(func() {
		if _, ok, _ := st.srv.projects.Get(newer); !ok {
			t.Error("a refused purge deleted the application")
		}
	})

	if code, b := x.do(http.MethodDelete, "/api/v1/applications/"+older+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("purge older: %d %s", code, b)
	}
}

// TestDecisionVersionConflicts pins the arithmetic of the one guard a purge keeps.
func TestDecisionVersionConflicts(t *testing.T) {
	rec := func(key uint64, ds ...deployedDecisionEntry) persistedDecision {
		return persistedDecision{Key: key, Decisions: ds}
	}
	all := []persistedDecision{
		rec(1, deployedDecisionEntry{ID: "a", Version: 1}),
		rec(2, deployedDecisionEntry{ID: "a", Version: 2}, deployedDecisionEntry{ID: "b", Version: 1}),
		rec(3, deployedDecisionEntry{ID: "c", Version: 1}),
	}
	cases := []struct {
		name  string
		going map[uint64]bool
		want  string
	}{
		{"newest of a lineage with a survivor", map[uint64]bool{2: true}, "a v2"},
		{"an older version", map[uint64]bool{1: true}, ""},
		{"a whole lineage", map[uint64]bool{1: true, 2: true, 3: true}, ""},
		{"a lineage of its own", map[uint64]bool{3: true}, ""},
	}
	for _, c := range cases {
		got := strings.Join(decisionVersionConflicts(c.going, all), ", ")
		if got != c.want {
			t.Errorf("%s: conflicts = %q, want %q", c.name, got, c.want)
		}
	}
}
