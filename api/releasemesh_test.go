package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-0495 slice F: the release lens answers a release's snapshot as Panorama's starmap
// graph, redacted as the architecture routes are, with the live state of the release's
// own processes and workers laid over it on request.

type lensRead struct {
	Nodes []struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Application string `json:"application"`
		State       string `json:"state"`
		Severity    string `json:"severity"`
		Runtime     *struct {
			Running int64 `json:"running"`
		} `json:"runtime"`
	} `json:"nodes"`
	Edges []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	} `json:"edges"`
	Restricted int   `json:"restricted"`
	ObservedAt int64 `json:"observedAt"`
	Status     struct {
		Unknown int `json:"unknown"`
	} `json:"status"`
	Lens struct {
		ApplicationID   string   `json:"applicationId"`
		Application     string   `json:"application"`
		Version         int32    `json:"version"`
		TakenAt         int64    `json:"takenAt"`
		Recorded        []string `json:"recorded"`
		Overlay         bool     `json:"overlay"`
		OverlayWithheld bool     `json:"overlayWithheld"`
	} `json:"lens"`
}

func (l lensRead) edges() map[string]bool {
	out := map[string]bool{}
	for _, e := range l.Edges {
		out[e.From+" "+e.Kind+" "+e.To] = true
	}
	return out
}

func TestTheReleaseLensDrawsTheReleaseAsAStarmapGraph(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	var other struct{ ID string }
	if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Mahnwesen"}`, "application/json", http.StatusOK), &other); err != nil {
		t.Fatal(err)
	}
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnwesenBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnstufeBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications/"+other.ID+"/publish", `{}`, "application/json", http.StatusOK)

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Bestellwesen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, bestellungBPMN, "application/xml", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)

	base := ts.URL + "/api/v1/applications/" + app.ID + "/releases/1/mesh"
	var whole lensRead
	if err := json.Unmarshal(mustCall(t, admin, http.MethodGet, base, "", "", http.StatusOK), &whole); err != nil {
		t.Fatal(err)
	}
	if l := whole.Lens; l.ApplicationID != app.ID || l.Application != "Bestellwesen" || l.Version != 1 || l.Overlay ||
		l.TakenAt == 0 || strings.Join(l.Recorded, ",") != "strategy,business,application,data,technology" {
		t.Errorf("lens = %+v", l)
	}
	if whole.ObservedAt != whole.Lens.TakenAt {
		t.Errorf("observedAt = %d, want the snapshot's moment %d without the overlay", whole.ObservedAt, whole.Lens.TakenAt)
	}
	process := ""
	for _, n := range whole.Nodes {
		if n.Kind == "process" && n.Name == "bestellung" {
			process = n.ID
		}
		if n.State != "unbound" {
			t.Errorf("%s is %s without the overlay", n.ID, n.State)
		}
	}
	if !strings.HasPrefix(process, "process:") {
		t.Fatalf("no process node for bestellung in %+v", whole.Nodes)
	}
	edges := whole.edges()
	for _, want := range []string{
		"application:" + app.ID + " contains " + process,
		"message:bestellt receives " + process,
		process + " throws message:versandt",
		process + " throws signal:storno",
		process + " calls definition:mahnstufe",
		"definition:mahnlauf throws message:bestellt",
		"message:versandt receives definition:mahnlauf",
		"application:" + other.ID + " contains definition:mahnlauf",
	} {
		if !edges[want] {
			t.Errorf("the administrator's lens lacks %s", want)
		}
	}
	if whole.Restricted != 0 || whole.Status.Unknown != len(whole.Nodes) {
		t.Errorf("restricted = %d, status = %+v", whole.Restricted, whole.Status)
	}

	// The overlay: the release's process version, as it runs now.
	var live lensRead
	if err := json.Unmarshal(mustCall(t, admin, http.MethodGet, base+"?overlay=1", "", "", http.StatusOK), &live); err != nil {
		t.Fatal(err)
	}
	if !live.Lens.Overlay || live.ObservedAt < live.Lens.TakenAt {
		t.Errorf("overlay lens = %+v, observedAt %d", live.Lens, live.ObservedAt)
	}
	for _, n := range live.Nodes {
		switch {
		case n.ID == process && (n.State != "healthy" || n.Runtime == nil):
			t.Errorf("the overlay's process = %+v, want healthy with its instance counts", n)
		case n.Kind != "process" && n.Kind != "application" && n.State != "unbound":
			t.Errorf("the overlay observes %s (%s), which the release does not run", n.ID, n.State)
		}
	}

	// Anna may not see Mahnwesen: its name, processes and id stay on the server, and
	// placeholders stand where they were.
	body := mustCall(t, anna, http.MethodGet, base+"?overlay=1", "", "", http.StatusOK)
	for _, leak := range []string{"Mahnwesen", "mahnlauf", other.ID} {
		if strings.Contains(string(body), leak) {
			t.Errorf("anna's lens shows %q", leak)
		}
	}
	var hers lensRead
	if err := json.Unmarshal(body, &hers); err != nil {
		t.Fatal(err)
	}
	// The call target, and one placeholder per name and direction a peer was withheld on.
	if hers.Restricted != 4 {
		t.Errorf("anna's restricted = %d, want 4", hers.Restricted)
	}
	if he := hers.edges(); !he[process+" calls restricted:1"] {
		t.Errorf("anna's call does not end at a placeholder: %v", hers.Edges)
	}

	// The overlay is the operators' (ADR-0495 §5): a member who is not one is answered
	// the graph as recorded, and told why the state is missing.
	brunoID := createUserWithRoles(t, admin, ts.URL, "bruno", `["modeler","user"]`)
	bruno := signInAs(t, ts.URL, "bruno", "a-password-that-is-long")
	mustCall(t, anna, http.MethodPut, ts.URL+"/api/v1/applications/"+app.ID+"/members/"+brunoID, `{"role":"viewer"}`, "application/json", http.StatusOK)
	var his lensRead
	if err := json.Unmarshal(mustCall(t, bruno, http.MethodGet, base+"?overlay=1", "", "", http.StatusOK), &his); err != nil {
		t.Fatal(err)
	}
	if his.Lens.Overlay || !his.Lens.OverlayWithheld || his.ObservedAt != his.Lens.TakenAt {
		t.Errorf("bruno's lens = %+v, want the overlay withheld", his.Lens)
	}
	for _, n := range his.Nodes {
		if n.State != "unbound" || n.Runtime != nil {
			t.Errorf("bruno is shown the live state of %s", n.ID)
		}
	}

	// The refusals the architecture routes give, and the application's sharing scope.
	if code, _ := call(t, admin, http.MethodGet, ts.URL+"/api/v1/applications/"+app.ID+"/releases/2/mesh", "", ""); code != http.StatusNotFound {
		t.Errorf("an unknown release answers %d, want 404", code)
	}
	if code, _ := call(t, admin, http.MethodGet, ts.URL+"/api/v1/applications/"+app.ID+"/releases/x/mesh", "", ""); code != http.StatusBadRequest {
		t.Errorf("a version that is no number answers %d, want 400", code)
	}
	if code, _ := call(t, anna, http.MethodGet, ts.URL+"/api/v1/applications/"+other.ID+"/releases/1/mesh", "", ""); code == http.StatusOK {
		t.Error("anna reads the lens of an application she may not see")
	}
}
