package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Per-target status (ADR-0129): what does each peer currently run for this
// application? This is a separate endpoint from the local deployments view on
// purpose — it makes outbound calls and can partially fail, and folding that into
// the fast local read would make every page load wait on the slowest peer.

type targetStatus struct {
	TargetID    string `json:"targetId"`
	TargetName  string `json:"targetName"`
	BaseURL     string `json:"baseUrl"`
	Bound       bool   `json:"bound"`
	Reachable   bool   `json:"reachable"`
	Error       string `json:"error"`
	Version     int32  `json:"version"`
	Running     int    `json:"running"`
	Finished    int    `json:"finished"`
	Definitions int    `json:"definitions"`
}

func targetStatuses(t *testing.T, ts *httptest.Server, appID string) (int, []targetStatus) {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/targets", "", "")
	var out []targetStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode target statuses: %v (%s)", err, raw)
	}
	return code, out
}

// TestTargetStatusReadsEachBoundPeer is the core of the view: for an application
// promoted to a peer, the peer is asked what it runs, and the answer is reported
// against that target.
func TestTargetStatusReadsEachBoundPeer(t *testing.T) {
	// A real receiver, so the status is read from a real deployments endpoint.
	receiver, _ := newAuthServer(t, "admin", "password1")
	adminB := newClient(t)
	if code := login(t, adminB, receiver, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("receiver login = %d", code)
	}
	_, secret := mintToken(t, adminB, receiver, "From A")

	sender := newTestServer(t)
	if code, _ := doReq(t, sender, http.MethodPut, "/api/v1/secrets/b-token", `{"value":"`+secret+`"}`, "application/json"); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatal("store credential")
	}
	app := seedPublishedApp(t, sender, "Watched", "watchproc")
	target := mkTarget(t, sender, "Produktion", receiver.URL, "b-token")

	// Before promoting, the target is listed but not bound: nothing has shipped
	// there, which is different from "shipped and unreachable".
	code, before := targetStatuses(t, sender, app)
	if code != http.StatusOK {
		t.Fatalf("target status = %d", code)
	}
	if len(before) != 1 || before[0].Bound {
		t.Fatalf("before promotion = %+v, want one unbound target", before)
	}

	if code, res := promote(t, sender, app, 1, target); code != http.StatusOK || !res.Results[0].OK {
		t.Fatalf("promote = %d %+v", code, res.Results)
	}

	code, after := targetStatuses(t, sender, app)
	if code != http.StatusOK {
		t.Fatalf("target status after promotion = %d", code)
	}
	if len(after) != 1 {
		t.Fatalf("statuses = %+v, want one", after)
	}
	got := after[0]
	if !got.Bound || !got.Reachable {
		t.Fatalf("status = %+v, want bound and reachable", got)
	}
	if got.TargetName != "Produktion" || got.BaseURL != receiver.URL {
		t.Errorf("status identity = %+v, want the target's name and URL", got)
	}
	if got.Version != 1 {
		t.Errorf("remote version = %d, want the promoted v1", got.Version)
	}
	if got.Definitions != 1 {
		t.Errorf("remote definitions = %d, want 1", got.Definitions)
	}
	if got.Running != 0 || got.Finished != 0 {
		t.Errorf("remote counts = %d/%d, want 0/0 with nothing started", got.Running, got.Finished)
	}
}

// TestTargetStatusDegradesWhenAPeerIsDown pins the honesty rule: an unreachable
// peer renders as unreachable, and does not fail the view or hide the others.
func TestTargetStatusDegradesWhenAPeerIsDown(t *testing.T) {
	sender := newTestServer(t)
	dead := newFakeRemote(t)
	deadURL := dead.URL

	app := seedPublishedApp(t, sender, "Mixed", "mixedproc")
	up := newFakeRemote(t)
	upTarget := mkTarget(t, sender, "Up", up.URL, "")
	downTarget := mkTarget(t, sender, "Down", deadURL, "")

	// Promote to both while they are alive, so both become bound.
	if code, res := promote(t, sender, app, 1, upTarget, downTarget); code != http.StatusOK {
		t.Fatalf("promote = %d %+v", code, res.Results)
	}
	dead.Close() // now one peer is gone

	code, statuses := targetStatuses(t, sender, app)
	if code != http.StatusOK {
		t.Fatalf("target status = %d, want 200 even with a peer down", code)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v, want one per target", statuses)
	}
	for _, st := range statuses {
		switch st.TargetName {
		case "Down":
			if st.Reachable || st.Error == "" {
				t.Errorf("down peer = %+v, want unreachable with a reason", st)
			}
			if !st.Bound {
				t.Error("down peer lost its binding; unreachable is not unbound")
			}
		case "Up":
			if !st.Bound {
				t.Errorf("up peer = %+v, want bound", st)
			}
		}
	}
}

// TestTargetStatusUnknownApplication is the 404 path.
func TestTargetStatusUnknownApplication(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/applications/nope/targets", "", ""); code != http.StatusNotFound {
		t.Error("target status of an unknown application: want 404")
	}
}

// TestTargetStatusWithNoTargets returns an empty list rather than null, so the UI
// renders "no targets" instead of failing to iterate.
func TestTargetStatusWithNoTargets(t *testing.T) {
	ts := newTestServer(t)
	app := seedPublishedApp(t, ts, "Lonely", "lonelyproc")
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+app+"/targets", "", "")
	if code != http.StatusOK {
		t.Fatalf("target status = %d", code)
	}
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("statuses = %s, want []", raw)
	}
}
