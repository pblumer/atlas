package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// What this server observes about its own resources for a model overlay
// (ADR-0189 §6): who may see which observation, a process filed in no
// application, a release member that is not a process, and a server that cannot
// count its incidents.

// panoramaObservationsPathsFacts collects the facts as one caller.
func panoramaObservationsPathsFacts(s *Server, pr *httpapi.Principal) (panorama.Facts, error) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if pr != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), pr))
	}
	return s.collectFacts(req)
}

// TestPanoramaObservationsAProcessInNoApplicationIsStillObserved. It has an
// observation of its own and adds to no application's totals.
func TestPanoramaObservationsAProcessInNoApplicationIsStillObserved(t *testing.T) {
	srv := newServerForErrors(t)
	panoramaMeshPathsDeploy(t, srv, pendingWorkPathsTaskBPMN("loose"))

	facts, err := panoramaObservationsPathsFacts(srv, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := facts.Processes["loose"]; !ok {
		t.Errorf("processes = %v, want the unfiled process observed", facts.Processes)
	}
	for id, app := range facts.Applications {
		if app.Detail["processes"] != "" && app.Detail["processes"] != "0" {
			t.Errorf("application %s counts processes %q; the unfiled process belongs to none", id, app.Detail["processes"])
		}
	}
}

// TestPanoramaObservationsAViewerIsShownOnlyWhatTheyMaySee. An observation is a
// statement about a resource, so a process, an application and a worker the caller
// cannot see are not observed for them either — not even as "unknown".
func TestPanoramaObservationsAViewerIsShownOnlyWhatTheyMaySee(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	// Deployed by an administrator, into nobody's application.
	mux, _ := srv.mountRoutes()
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(pendingWorkPathsTaskBPMN("roots")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(httpapi.WithPrincipal(req.Context(), admin)))
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body.String())
	}
	var err error
	srv.do(func() {
		if err = srv.projects.Save(project{ID: "p-bo", Name: "Bo's", OwnerID: "usr_bo"}); err != nil {
			return
		}
		err = srv.connectors.Save(connector{ID: "w-bo", Name: "bo-mail", Kind: connectorKindMail,
			OwnerID: "usr_bo", Enabled: true, CreatedAt: 1})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada", Roles: []string{RoleUser}}
	facts, err := panoramaObservationsPathsFacts(srv, ada)
	if err != nil {
		t.Fatalf("collect as ada: %v", err)
	}
	if _, ok := facts.Processes["roots"]; ok {
		t.Error("ada is shown root's process")
	}
	if _, ok := facts.Applications["p-bo"]; ok {
		t.Error("ada is shown Bo's application")
	}
	if _, ok := facts.Connectors["w-bo"]; ok {
		t.Error("ada is shown Bo's worker")
	}

	// The same reading as the administrator shows all three, so the absence above
	// is the scope and not a failure to read.
	facts, err = panoramaObservationsPathsFacts(srv, admin)
	if err != nil {
		t.Fatalf("collect as admin: %v", err)
	}
	_, p := facts.Processes["roots"]
	_, a := facts.Applications["p-bo"]
	_, w := facts.Connectors["w-bo"]
	if !p || !a || !w {
		t.Errorf("admin sees process=%v application=%v worker=%v, want all three", p, a, w)
	}
}

// TestPanoramaObservationsAReleaseIsJudgedByItsProcesses. A form or a decision in
// a release has no deployed version to compare against today; counting it as
// matched would overstate what was checked, and counting it as absent would call a
// healthy release not ready.
func TestPanoramaObservationsAReleaseIsJudgedByItsProcesses(t *testing.T) {
	srv := newServerForErrors(t)
	panoramaMeshPathsDeploy(t, srv, pendingWorkPathsTaskBPMN("shipped"))

	var fact panorama.Fact
	srv.do(func() {
		fact = srv.releaseFact(applicationRelease{ID: "rel-1", Version: 1, Members: []releaseMember{
			{Kind: "form", Ref: "intake", ArtifactVer: 7},
			{Kind: "process", Ref: "shipped", ArtifactVer: 1},
		}})
	})
	if fact.State != panorama.StateHealthy {
		t.Errorf("state = %s (%s), want healthy", fact.State, fact.Reason)
	}
	if fact.Detail["members"] != "2" || fact.Detail["absent"] != "0" || fact.Detail["superseded"] != "0" {
		t.Errorf("detail = %v, want two members, none absent or superseded", fact.Detail)
	}
}

// TestPanoramaObservationsAServerThatCannotCountIncidentsSaysSo, rather than
// observing every job type as idle.
func TestPanoramaObservationsAServerThatCannotCountIncidentsSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	if facts, err := panoramaObservationsPathsFacts(srv, nil); err == nil {
		t.Errorf("collect = %+v, nil; want an error", facts)
	}
}
