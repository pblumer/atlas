package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A worker that names the workers it holds is leased only their jobs. Two workers of
// one Worker Type — Exchange Online and an Exchange Server, two mail providers — each
// serve their own name; before, a job for one went to whichever polled first and
// failed there with "no worker registered as …" (found against a real Exchange Server
// beside Exchange Online). A worker that names nothing is leased as before.

func pulledVersions(t *testing.T, srv *Server, body string) []int64 {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []struct {
			ProcessDefKey int64 `json:"processDefKey"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode pull: %v (%s)", err, raw)
	}
	var v []int64
	for _, j := range out.Jobs {
		v = append(v, j.ProcessDefKey)
	}
	return v
}

// twoProviders deploys the notify model naming office365 (definition 1) and then
// internal-relay (definition 2), with one waiting mail job on each.
func twoProviders(t *testing.T) *Server {
	t.Helper()
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	deployNotify(t, srv, "office365")
	deployNotify(t, srv, "internal-relay")
	startNotify(t, srv, 1)
	startNotify(t, srv, 2)
	return srv
}

func TestAWorkerIsLeasedOnlyTheJobsOfTheNamesItHolds(t *testing.T) {
	srv := twoProviders(t)

	// The relay's worker asks for everything there is and gets its own job only.
	got := pulledVersions(t, srv, `{"type":"io.atlas.mail.send","worker":"relay","maxJobs":10,"connectors":["internal-relay"]}`)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("the internal-relay worker was leased definitions %v, want [2] only", got)
	}
	// Nothing of its own is left; office365's job is not handed to it instead.
	if got := pulledVersions(t, srv, `{"type":"io.atlas.mail.send","worker":"relay","maxJobs":10,"connectors":["internal-relay"]}`); len(got) != 0 {
		t.Fatalf("a second poll leased %v to the internal-relay worker, want nothing", got)
	}
	got = pulledVersions(t, srv, `{"type":"io.atlas.mail.send","worker":"o365","maxJobs":10,"connectors":["office365"]}`)
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("the office365 worker was leased definitions %v, want [1]", got)
	}
}

func TestAWorkerThatNamesNothingIsLeasedAsBefore(t *testing.T) {
	srv := twoProviders(t)
	if got := pulledVersions(t, srv, `{"type":"io.atlas.mail.send","worker":"any","maxJobs":10}`); len(got) != 2 {
		t.Fatalf("a worker naming no worker was leased %v, want both jobs", got)
	}
}
