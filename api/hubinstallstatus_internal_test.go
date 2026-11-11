package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// view reads forms and decisions in the application, holds a release that does not
// name a decision of the package to be behind, and reads a record kept before
// amendment h — which knows only its application and catalogues — with what it has.
func TestHubViewReadsEveryKindAndARecordKeptBefore(t *testing.T) {
	srv, _ := openAt(t, t.TempDir())
	var err error
	srv.do(func() {
		for _, save := range []func() error{
			func() error { return srv.projects.Save(project{ID: "app-1", Key: "kinds", Name: "Kinds"}) },
			func() error { return srv.drafts.Save(draft{ProcessID: "p1", ProjectID: "app-1", SavedAt: 10}) },
			func() error { return srv.forms.Save(form{ID: "f1", ProjectID: "app-1"}) },
			func() error { return srv.forms.Save(form{ID: "f-elsewhere", ProjectID: "app-2"}) },
			func() error { return srv.dmnrefs.Save(dmnRef{ID: "d1", ProjectID: "app-1"}) },
			func() error {
				return srv.releases.Save(applicationRelease{ID: "rel-1", ApplicationID: "app-1", Version: 3, PublishedAt: 20,
					Members: []releaseMember{{Kind: "process", Ref: "p1"}}})
			},
		} {
			if err = save(); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	holdings, err := srv.hubHoldings()
	if err != nil {
		t.Fatal(err)
	}
	v, err := holdings.view(hubInstallation{ApplicationKey: "kinds", ApplicationID: "app-1", State: hubInstalled,
		Expected: &hubExpected{Processes: []string{"p1"}, Forms: []string{"f1", "f-elsewhere"}, Decisions: []string{"d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	wantComponents(t, "kinds", v, hubStatusPartial, map[string]string{
		"process:p1":       hubPresent,
		"form:f1":          hubPresent,
		"form:f-elsewhere": hubMissing, // in another application, not this one
		"decision:d1":      hubPresent,
		"release:kinds":    hubUnpublished, // version 3 names p1 but not d1
	})

	legacy, err := holdings.view(hubInstallation{ApplicationKey: "kinds", ApplicationID: "app-1", State: hubInstalled,
		CataloguesNotImported: []string{"cat-x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := componentStates(legacy); len(got) != 2 || got["application:kinds"] != hubPresent || got["catalogue:cat-x"] != hubMissing {
		t.Fatalf("a record kept before amendment h = %v", got)
	}
}

// releaseCovers asks for every process and every decision of the package.
func TestReleaseCoversEveryProcessAndDecision(t *testing.T) {
	rel := applicationRelease{Members: []releaseMember{{Kind: "process", Ref: "p1"}, {Kind: "decision", Ref: "d1"}}}
	for _, c := range []struct {
		name string
		exp  hubExpected
		want bool
	}{
		{"both named", hubExpected{Processes: []string{"p1"}, Decisions: []string{"d1"}}, true},
		{"a process it lacks", hubExpected{Processes: []string{"p1", "p2"}}, false},
		{"a decision it lacks", hubExpected{Decisions: []string{"d2"}}, false},
		{"a decision named as a process", hubExpected{Processes: []string{"d1"}}, false},
	} {
		if got := releaseCovers(rel, c.exp); got != c.want {
			t.Errorf("%s: releaseCovers = %v, want %v", c.name, got, c.want)
		}
	}
}

// expectTree takes the tree's identities and the manifest's constants beside the
// package's, each list sorted and without repeats.
func TestExpectTreeTakesTheTreeSortedAndOnce(t *testing.T) {
	j := &hubJournal{rec: hubInstallation{Expected: &hubExpected{Constants: []string{"B", "A"}, Catalogues: []string{"c2", "c1", "c2"}}}}
	j.expectTree(sourceManifest{
		Processes: []sourceProcess{{ID: "p2"}, {ID: "p1"}},
		Forms:     []sourceForm{{ID: "f1"}},
		Decisions: []sourceDecision{{ID: "d1"}, {ID: "d1"}},
	}, []heldConstant{{Name: "A"}, {Name: "C"}})
	got, _ := json.Marshal(j.rec.Expected)
	want := `{"processes":["p1","p2"],"forms":["f1"],"decisions":["d1"],"constants":["A","B","C"],"catalogues":["c1","c2"],"products":[]}`
	if string(got) != want {
		t.Fatalf("expected = %s\nwant       %s", got, want)
	}
}

// packageConstantNames reads the names from konstanten.json and nothing else.
func TestPackageConstantNames(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []sourceFile
		want  string
	}{
		{"no file", []sourceFile{{Path: "atlas.json", Data: []byte(`{}`)}}, "[]"},
		{"unreadable", []sourceFile{{Path: "konstanten.json", Data: []byte(`{`)}}, "[]"},
		{"named, and one without a name", []sourceFile{{Path: "konstanten.json",
			Data: []byte(`{"constants":[{"name":"A"},{"name":""},{"name":"B"}]}`)}}, `["A","B"]`},
	} {
		got, _ := json.Marshal(append([]string{}, packageConstantNames(c.files)...))
		if string(got) != c.want {
			t.Errorf("%s: names = %s, want %s", c.name, got, c.want)
		}
	}
}

// A record store it cannot read is an error, never an empty listing: "nothing was
// installed from the hub" is an answer, and a broken store must not give it.
func TestHubInstallationsWithAnUnreadableStoreAnswerAnError(t *testing.T) {
	dir := t.TempDir()
	_, ts, _, _ := hubImportServerAt(t, dir)
	store := filepath.Join(dir, "blueprint-installations")
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, raw := hubCall(t, ts, http.MethodGet, "/api/v1/blueprints/installations", ""); code != http.StatusInternalServerError {
		t.Fatalf("listing over a broken store = %d %s", code, raw)
	}
}
