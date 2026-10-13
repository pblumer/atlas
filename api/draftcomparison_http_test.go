package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// draftComparison is the answer GET /api/v1/drafts/{id}/comparison gives: how the
// draft a modeller has open relates to a deployed version of its process.
type draftComparison struct {
	ProcessID      string `json:"processId"`
	OwnerID        string `json:"ownerId"`
	OwnerName      string `json:"ownerName"`
	SavedAt        int64  `json:"savedAt"`
	SavedBy        string `json:"savedBy"`
	SavedByName    string `json:"savedByName"`
	Deployed       bool   `json:"deployed"`
	Key            uint64 `json:"key"`
	Version        int32  `json:"version"`
	LatestVersion  int32  `json:"latestVersion"`
	DeployedAt     int64  `json:"deployedAt"`
	DeployedBy     string `json:"deployedBy"`
	DeployedByName string `json:"deployedByName"`
	SameModel      bool   `json:"sameModel"`
	Difference     string `json:"difference"`
}

// getComparison reads a draft's comparison on an open server.
func getComparison(t *testing.T, ts *httptest.Server, path string) draftComparison {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, path, "", "")
	return decodeComparison(t, code, body)
}

func decodeComparison(t *testing.T, code int, body []byte) draftComparison {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("comparison = %d %s", code, body)
	}
	var c draftComparison
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode comparison: %v (%s)", err, body)
	}
	return c
}

// renamedTask is deployableBPMN with its one task carrying a name — the smallest
// change a modeller makes that the engine can see.
func renamedTask(procID, name string) string {
	return strings.Replace(deployableBPMN(procID), `<bpmn:serviceTask id="t">`,
		fmt.Sprintf(`<bpmn:serviceTask id="t" name=%q>`, name), 1)
}

// TestADraftDeployedAsIsReadsAsThatVersion: the ordinary round — save, deploy the
// same diagram — has to say "this draft is v1", or the Modeler cannot tell anybody
// which version they are looking at.
func TestADraftDeployedAsIsReadsAsThatVersion(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", deployableBPMN("order"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	c := getComparison(t, ts, "/api/v1/drafts/order/comparison")
	if c.Deployed || c.SameModel || c.Version != 0 {
		t.Fatalf("a draft nothing was deployed from = %+v, want deployed=false", c)
	}
	if c.ProcessID != "order" || c.SavedAt == 0 {
		t.Fatalf("comparison = %+v, want the draft's own id and save time", c)
	}

	key := deployKey(t, ts, ts.Client(), deployableBPMN("order"))
	c = getComparison(t, ts, "/api/v1/drafts/order/comparison")
	if !c.Deployed || c.Key != key || c.Version != 1 || c.LatestVersion != 1 {
		t.Fatalf("comparison = %+v, want v1 (key %d) as the newest", c, key)
	}
	if !c.SameModel || c.Difference != "" {
		t.Fatalf("a draft deployed as is = %+v, want the same model and no difference", c)
	}
	if c.DeployedAt == 0 {
		t.Fatalf("comparison = %+v, want when v1 was deployed", c)
	}
}

// TestADeployNotSavedToTheDraftIsCaught is the report this exists for: a change was
// deployed from the Modeler without pressing Save, so the deployed version carries it
// and the draft the application opens does not. The comparison must say that the
// newest version differs from the draft, where, and that it was deployed after the
// draft was last saved — which is the whole of what a modeller needs in order to stop
// editing the stale draft (and to stop the next Publish from deploying it over the
// change).
func TestADeployNotSavedToTheDraftIsCaught(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", deployableBPMN("order"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	v1 := deployKey(t, ts, ts.Client(), deployableBPMN("order"))
	// The change goes straight to a deploy; the draft is never saved again.
	v2 := deployKey(t, ts, ts.Client(), renamedTask("order", "Ship it"))

	c := getComparison(t, ts, "/api/v1/drafts/order/comparison")
	if c.Key != v2 || c.Version != 2 || c.LatestVersion != 2 {
		t.Fatalf("comparison = %+v, want the newest version, v2 (key %d)", c, v2)
	}
	if c.SameModel {
		t.Fatalf("a draft without the deployed change reads as the same model: %+v", c)
	}
	if !strings.Contains(c.Difference, `serviceTask id="t"`) {
		t.Fatalf("difference = %q, want the task that changed named", c.Difference)
	}
	if c.DeployedAt < c.SavedAt {
		t.Fatalf("comparison = %+v, want v2 deployed no earlier than the draft's last save", c)
	}

	// An older version can be named, which is how the Modeler asks about the version
	// it has open rather than about the newest.
	c = getComparison(t, ts, fmt.Sprintf("/api/v1/drafts/order/comparison?key=%d", v1))
	if c.Key != v1 || c.Version != 1 || c.LatestVersion != 2 || !c.SameModel {
		t.Fatalf("comparison with v1 = %+v, want v1 of 2 and the same model", c)
	}

	// Saving the deployed change into the draft settles it.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", renamedTask("order", "Ship it"), "application/xml"); code != http.StatusOK {
		t.Fatalf("re-save draft = %d %s", code, body)
	}
	c = getComparison(t, ts, "/api/v1/drafts/order/comparison")
	if !c.SameModel || c.Difference != "" {
		t.Fatalf("a draft saved from v2 = %+v, want the same model", c)
	}
}

// TestALayoutOnlyDifferenceIsTheSameModel: what the engine runs is the question, so
// a draft whose shapes were moved since the deploy still reads as that version.
func TestALayoutOnlyDifferenceIsTheSameModel(t *testing.T) {
	ts := newTestServer(t)
	// The deployed version has no diagram at all; the draft has the one Atlas lays
	// out on the way in. Only the picture differs.
	deployKey(t, ts, ts.Client(), deployableBPMN("order"))
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", deployableBPMN("order"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/order/xml", "", ""); !strings.Contains(string(xml), "BPMNDiagram") {
		t.Fatalf("fixture: the draft carries no diagram, so the layouts do not differ:\n%s", xml)
	}
	c := getComparison(t, ts, "/api/v1/drafts/order/comparison")
	if !c.SameModel {
		t.Fatalf("a draft differing only in its diagram = %+v, want the same model", c)
	}
}

// TestDraftComparisonRefusals: a key that is not a number, names nothing, or names a
// version of another process is the caller's mistake, said as such.
func TestDraftComparisonRefusals(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/drafts/nope/comparison", "", ""); code != http.StatusNotFound {
		t.Fatalf("comparison of a missing draft = %d, want 404", code)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", deployableBPMN("order"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	other := deployKey(t, ts, ts.Client(), deployableBPMN("invoice"))

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?key=abc", http.StatusBadRequest},
		{"?key=987654321", http.StatusNotFound},
		{fmt.Sprintf("?key=%d", other), http.StatusBadRequest},
	} {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts/order/comparison"+tc.query, "", "")
		if code != tc.want {
			t.Errorf("comparison%s = %d %s, want %d", tc.query, code, body, tc.want)
		}
	}
}

// TestWhoCreatedAndWhoLastChangedAreNamed: the Modeler names the person behind
// what it shows — who created the draft, who saved it last, who deployed the
// version — by the name a colleague knows them by, not by an opaque id. A move is
// not an edit, so it leaves "last saved by" alone.
func TestWhoCreatedAndWhoLastChangedAreNamed(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	mkUser := func(username, display string) string {
		code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+username+`","displayName":"`+display+`","password":"password1","roles":["modeler","operator","user"]}`)
		if code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", username, code, body)
		}
		return idOf(t, body)
	}
	aliceID := mkUser("alice", "Alice Adams")
	bobID := mkUser("bob", "Bob Brown")
	mkUser("carol", "Carol Clark")
	alice, bob, carol := newClient(t), newClient(t), newClient(t)
	for name, c := range map[string]*http.Client{"alice": alice, "bob": bob, "carol": carol} {
		if login(t, c, ts, name, "password1") != http.StatusOK {
			t.Fatalf("%s login", name)
		}
	}

	_, pbody := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Orders"}`)
	pid := decodeProject(t, pbody).ID
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("share with bob: %d", code)
	}

	// Alice creates the draft; Bob saves it next.
	if code, body := cReqTyped(t, alice, ts, "POST", "/api/v1/drafts?projectId="+pid, "application/xml", deployableBPMN("order")); code != http.StatusOK {
		t.Fatalf("alice save = %d %s", code, body)
	}
	code, body := cReqTyped(t, bob, ts, "POST", "/api/v1/drafts", "application/xml", deployableBPMN("order"))
	if code != http.StatusOK {
		t.Fatalf("bob save = %d %s", code, body)
	}
	type authored struct {
		ProcessID   string `json:"processId"`
		OwnerID     string `json:"ownerId"`
		OwnerName   string `json:"ownerName"`
		SavedBy     string `json:"savedBy"`
		SavedByName string `json:"savedByName"`
	}
	var saved authored
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, body)
	}
	want := authored{ProcessID: "order", OwnerID: aliceID, OwnerName: "Alice Adams", SavedBy: bobID, SavedByName: "Bob Brown"}
	if saved != want {
		t.Fatalf("save answered %+v, want %+v", saved, want)
	}

	listed := func() authored {
		t.Helper()
		_, body := cReq(t, alice, ts, "GET", "/api/v1/drafts", "")
		var list []authored
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("decode drafts: %v (%s)", err, body)
		}
		for _, d := range list {
			if d.ProcessID == "order" {
				return d
			}
		}
		t.Fatalf("draft order not listed: %s", body)
		return authored{}
	}
	if got := listed(); got != want {
		t.Fatalf("listed %+v, want %+v", got, want)
	}

	// Moving the draft is not editing it: whoever last saved it still did.
	code, body = cReq(t, alice, ts, "PATCH", "/api/v1/drafts/order", `{"projectId":""}`)
	if code != http.StatusOK {
		t.Fatalf("move = %d %s", code, body)
	}
	var moved authored
	if err := json.Unmarshal(body, &moved); err != nil {
		t.Fatalf("decode move: %v (%s)", err, body)
	}
	if moved != want {
		t.Fatalf("move answered %+v, want %+v", moved, want)
	}
	if got := listed(); got != want {
		t.Fatalf("after the move, listed %+v, want %+v", got, want)
	}
	// Back into the application, so Bob — an editor there — may deploy into it.
	if code, body := cReq(t, alice, ts, "PATCH", "/api/v1/drafts/order", `{"projectId":"`+pid+`"}`); code != http.StatusOK {
		t.Fatalf("move back = %d %s", code, body)
	}

	// Bob deploys it: the version names him.
	if code, body := cReqTyped(t, bob, ts, "POST", "/api/v1/deployments?projectId="+pid, "application/xml", deployableBPMN("order")); code != http.StatusOK {
		t.Fatalf("bob deploy = %d %s", code, body)
	}
	_, body = cReq(t, alice, ts, "GET", "/api/v1/processes", "")
	var procs []struct {
		ProcessID      string `json:"processId"`
		DeployedBy     string `json:"deployedBy"`
		DeployedByName string `json:"deployedByName"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	var found bool
	for _, p := range procs {
		if p.ProcessID == "order" {
			found = true
			if p.DeployedBy != bobID || p.DeployedByName != "Bob Brown" {
				t.Fatalf("process listed as deployed by %q (%q), want Bob Brown (%s)", p.DeployedByName, p.DeployedBy, bobID)
			}
		}
	}
	if !found {
		t.Fatalf("order not among the processes: %s", body)
	}

	// The comparison carries both people, so an agent reading it alone can say who
	// to ask.
	code, body = cReq(t, alice, ts, "GET", "/api/v1/drafts/order/comparison", "")
	c := decodeComparison(t, code, body)
	if c.SavedBy != bobID || c.SavedByName != "Bob Brown" || c.DeployedBy != bobID || c.DeployedByName != "Bob Brown" {
		t.Fatalf("comparison = %+v, want Bob as last saver and deployer", c)
	}
	if c.OwnerID != aliceID || c.OwnerName != "Alice Adams" {
		t.Fatalf("comparison = %+v, want Alice as the draft's creator", c)
	}

	// The comparison reads the draft, so it is as hidden as the draft is.
	if code, _ := cReq(t, carol, ts, "GET", "/api/v1/drafts/order/comparison", ""); code != http.StatusNotFound {
		t.Fatalf("a non-member's comparison = %d, want 404", code)
	}
}
