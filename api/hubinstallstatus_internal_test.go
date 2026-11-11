package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// An installation's status is read from what this server holds, never from what a
// client reported: after each step of a Console install — the application, its
// constants, its release, its catalogues — and after a restart, the record says
// which of the release's components are here (ADR-0499 amendment h, issue #1422).

func hubCall(t *testing.T, ts *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// hubViews lists the installations with their status, as GET answers them.
func hubViews(t *testing.T, ts *httptest.Server) []hubInstallationView {
	t.Helper()
	code, raw := hubCall(t, ts, http.MethodGet, "/api/v1/blueprints/installations", "")
	if code != http.StatusOK {
		t.Fatalf("installations = %d %s", code, raw)
	}
	var out []hubInstallationView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("installations: %v: %s", err, raw)
	}
	return out
}

// componentStates maps kind:id to its status.
func componentStates(v hubInstallationView) map[string]string {
	out := map[string]string{}
	for _, c := range v.Components {
		out[c.Kind+":"+c.ID] = c.Status
	}
	return out
}

func wantComponents(t *testing.T, step string, v hubInstallationView, status string, want map[string]string) {
	t.Helper()
	got := componentStates(v)
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: component %s = %q, want %q (all: %v)", step, k, got[k], w, got)
		}
	}
	if v.Status != status {
		t.Errorf("%s: status = %q, want %q (components %v)", step, v.Status, status, got)
	}
}

// TestHubInstallationStatusFollowsEachStep walks a Console install step by step and
// stops after each, as a crash would: the record never says complete before every
// component is here, and says what is missing.
func TestHubInstallationStatusFollowsEachStep(t *testing.T) {
	dir := t.TempDir()
	_, ts, f, stop := hubImportServerAt(t, dir)
	digest := f.release.Artifact.Digest

	code, out := hubPost(t, ts, "?digest="+digest)
	if code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	appID := out["applicationId"].(string)

	views := hubViews(t, ts)
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	wantComponents(t, "after the application", views[0], hubStatusPartial, map[string]string{
		"application:hub-sample":  hubPresent,
		"process:proc_hub_sample": hubPresent,
		"constant:HUB_TENANT":     hubMissing,
		"release:hub-sample":      hubMissing,
		"catalogue:cat-hub":       hubMissing,
		"product:hub-product":     hubMissing,
	})

	if code, raw := hubCall(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/constants/import",
		`{"constants":[{"name":"HUB_TENANT","type":"string","value":"t-1"}]}`); code != http.StatusOK {
		t.Fatalf("constants = %d %s", code, raw)
	}
	if code, raw := hubCall(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/publish", `{"note":"hub"}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("publish = %d %s", code, raw)
	}
	wantComponents(t, "after the release", hubViews(t, ts)[0], hubStatusPartial, map[string]string{
		"constant:HUB_TENANT": hubPresent,
		"release:hub-sample":  hubPublished,
		"catalogue:cat-hub":   hubMissing,
	})

	if code, raw := hubCall(t, ts, http.MethodPost, "/api/v1/catalogs/import", hubCatalogue); code != http.StatusOK {
		t.Fatalf("catalogue = %d %s", code, raw)
	}
	complete := hubViews(t, ts)[0]
	wantComponents(t, "after the catalogues", complete, hubStatusComplete, map[string]string{
		"catalogue:cat-hub":   hubPublished,
		"product:hub-product": hubPresent,
	})

	// A restart reads the same from disk, with no registry configured.
	ts.Close()
	stop()
	srv, _ := openAt(t, dir)
	again := httptest.NewServer(srv.Handler())
	t.Cleanup(again.Close)
	if v := hubViews(t, again); len(v) != 1 || v[0].Status != hubStatusComplete {
		t.Fatalf("after a restart = %+v", v)
	}

	// A process edited after the release leaves the release behind what is here.
	edited := strings.Replace(waitingProcess("proc_hub_sample"), `<userTask id="u"/>`, `<userTask id="u" name="edited"/>`, 1)
	if code, raw := hubCall(t, again, http.MethodPost, "/api/v1/drafts?projectId="+appID, edited); code >= 300 {
		t.Fatalf("edit = %d %s", code, raw)
	}
	// Saves and releases are stamped in seconds, and a save in the release's own
	// second counts as released; the edit is moved a second on rather than waiting
	// for the clock.
	srv.do(func() {
		d, _, _ := srv.drafts.Get("proc_hub_sample")
		d.SavedAt += 2
		_ = srv.drafts.Save(d)
	})
	wantComponents(t, "after an edit", hubViews(t, again)[0], hubStatusPartial, map[string]string{
		"release:hub-sample": hubUnpublished,
	})

	// The application deleted, the record says so instead of describing it.
	if code, raw := hubCall(t, again, http.MethodDelete, "/api/v1/applications/"+appID, ""); code >= 300 {
		t.Fatalf("delete = %d %s", code, raw)
	}
	if v := hubViews(t, again); len(v) != 1 || v[0].Status != hubStatusRemoved {
		t.Fatalf("after the delete = %+v", v)
	}
}

// A record left pending says pending, whatever is there: the import's outcome is
// not known, and the components are listed so a person sees what is.
func TestPendingHubInstallationStaysPendingWithItsComponents(t *testing.T) {
	srv, ts, _, _ := hubImportServerAt(t, t.TempDir())
	left := hubInstallation{ApplicationKey: "hub-sample", State: hubPending, BlueprintID: "org.example.hub-sample",
		Version: "1.0.0", Expected: &hubExpected{Processes: []string{"proc_hub_sample"}, Catalogues: []string{"cat-hub"}}}
	if err := srv.hubInstalls.Save(left); err != nil {
		t.Fatal(err)
	}
	v := hubViews(t, ts)
	if len(v) != 1 {
		t.Fatalf("views = %+v", v)
	}
	wantComponents(t, "pending", v[0], hubStatusPending, map[string]string{
		"process:proc_hub_sample": hubMissing,
		"catalogue:cat-hub":       hubMissing,
	})
}

// The listing is for anybody who may model, narrowed to the applications they may
// see; who installed is an administrator's to read.
func TestHubInstallationsShowOnlyWhatTheCallerMaySee(t *testing.T) {
	srv, ts, f, _ := hubImportServerAt(t, t.TempDir())
	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	var app project
	srv.do(func() {
		apps, _ := srv.projects.LoadAll()
		for _, a := range apps {
			if a.Key == "hub-sample" {
				app = a
			}
		}
		app.OwnerID, app.Visibility = "usr_ann", VisibilityPrivate
		_ = srv.projects.Save(app)
		rec, _, _ := srv.hubInstalls.Get("hub-sample")
		rec.InstalledBy = "usr_ann"
		_ = srv.hubInstalls.Save(rec)
	})
	srv.authEnabled = true
	defer func() { srv.authEnabled = false }()

	list := func(p *httpapi.Principal) []hubInstallationView {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/installations", nil)
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
		w := httptest.NewRecorder()
		srv.handleListHubInstallations(w, req)
		var out []hubInstallationView
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return out
	}
	if got := list(&httpapi.Principal{UserID: "usr_bob", Roles: []string{RoleModeler}}); len(got) != 0 {
		t.Fatalf("a modeler who cannot see the application sees its installation: %+v", got)
	}
	owner := list(&httpapi.Principal{UserID: "usr_ann", Roles: []string{RoleModeler}})
	if len(owner) != 1 || owner[0].InstalledBy != "" || owner[0].BlueprintID != "org.example.hub-sample" {
		t.Fatalf("the owner's view = %+v, want the record without who installed it", owner)
	}
	admin := list(&httpapi.Principal{UserID: "usr_root", Roles: []string{RoleAdmin}})
	if len(admin) != 1 || admin[0].InstalledBy != "usr_ann" {
		t.Fatalf("the administrator's view = %+v", admin)
	}
}
