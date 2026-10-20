package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// simpleBPMN is a start-to-end process with the given id: nothing runs, but it lists.
func simpleBPMN(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
}

// listedApplications reads GET /api/v1/processes as one client and returns, per
// process id, the application id and the application name the row carries.
func listedApplications(t *testing.T, c *http.Client, ts *httptest.Server) map[string][2]string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes = %d: %s", code, body)
	}
	var rows []struct {
		ProcessID       string `json:"processId"`
		ProjectID       string `json:"projectId"`
		ApplicationName string `json:"applicationName"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	out := map[string][2]string{}
	for _, r := range rows {
		out[r.ProcessID] = [2]string{r.ProjectID, r.ApplicationName}
	}
	return out
}

// TestProcessListNamesTheApplicationTheCallerMaySee: the Tasks app's Start view
// groups the startable processes by the application they were deployed under, so
// the listing says which one by name — but only to somebody who may see that
// application. A private application's name is as hidden as the application
// itself (ADR-0071): the row still carries its opaque id, and the Start view files
// it under "other" rather than under a name the caller was never shown.
func TestProcessListNamesTheApplicationTheCallerMaySee(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	olaID := createUserWithRoles(t, admin, ts.URL, "ola", `["operator","user"]`)

	_, b := cReq(t, admin, ts, "POST", "/api/v1/applications", `{"name":"Benutzerverwaltung"}`)
	shared := decodeProject(t, b).ID
	_, b = cReq(t, admin, ts, "POST", "/api/v1/applications", `{"name":"Geheim"}`)
	private := decodeProject(t, b).ID
	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/applications/"+shared, `{"visibility":"shared"}`); code != http.StatusOK {
		t.Fatalf("share application = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/applications/"+shared+"/members/"+olaID, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("add member = %d: %s", code, body)
	}

	for path, pid := range map[string]string{
		"/api/v1/deployments?projectId=" + shared:  "onboarding",
		"/api/v1/deployments?projectId=" + private: "restructuring",
		"/api/v1/deployments":                      "loose",
	} {
		if code, body := cReq(t, admin, ts, "POST", path, simpleBPMN(pid)); code != http.StatusOK {
			t.Fatalf("deploy %s = %d: %s", pid, code, body)
		}
	}

	// The owner sees every application, so every filed row carries its name.
	got := listedApplications(t, admin, ts)
	want := map[string][2]string{
		"onboarding":    {shared, "Benutzerverwaltung"},
		"restructuring": {private, "Geheim"},
		"loose":         {"", ""},
	}
	for pid, w := range want {
		if got[pid] != w {
			t.Errorf("admin: %s = %v, want %v", pid, got[pid], w)
		}
	}

	// A viewer of the shared application reads its name, and not the private one's.
	ola := signInAs(t, ts.URL, "ola", "a-password-that-is-long")
	got = listedApplications(t, ola, ts)
	want = map[string][2]string{
		"onboarding":    {shared, "Benutzerverwaltung"},
		"restructuring": {private, ""},
		"loose":         {"", ""},
	}
	for pid, w := range want {
		if got[pid] != w {
			t.Errorf("ola: %s = %v, want %v", pid, got[pid], w)
		}
	}
}

// TestProcessListSurvivesAnUnreadableApplicationStore: the names are a convenience on
// a list every view reads — Operations, the Modeler home, Start — so an application
// store that cannot be read costs the names and nothing else. Failing the whole
// listing over it would take every one of those views down with the one label.
func TestProcessListSurvivesAnUnreadableApplicationStore(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	code, body := doReq(t, ts, "POST", "/api/v1/applications", `{"name":"Reisen"}`, "application/json")
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create application = %d: %s", code, body)
	}
	var app struct{ ID string }
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	if code, body := doReq(t, ts, "POST", "/api/v1/deployments?projectId="+app.ID, simpleBPMN("travel"), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}

	// The directory becomes a file: listing it fails, whoever reads it.
	projects := filepath.Join(dir, "projects")
	if err := os.RemoveAll(projects); err != nil {
		t.Fatalf("remove projects: %v", err)
	}
	if err := os.WriteFile(projects, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace projects: %v", err)
	}

	code, body = doReq(t, ts, "GET", "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes = %d: %s", code, body)
	}
	if !strings.Contains(string(body), `"processId":"travel"`) || !strings.Contains(string(body), `"projectId":"`+app.ID+`"`) {
		t.Fatalf("the process went missing with its application's name: %s", body)
	}
	if strings.Contains(string(body), "applicationName") {
		t.Fatalf("a name was listed that could not have been read: %s", body)
	}
}

// TestStartFavouritesFollowThePersonAcrossSessions: the star on the Start view is
// kept on the server per person, so it is there again from a second browser — here a
// second sign-in — and an operator who holds no `user` role reaches it, since roles do
// not nest and the people who start processes hold `operator`.
func TestStartFavouritesFollowThePersonAcrossSessions(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	createUserWithRoles(t, admin, ts.URL, "ola", `["operator"]`)
	createUserWithRoles(t, admin, ts.URL, "ben", `["operator"]`)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments", simpleBPMN("travel")); code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}

	ola := signInAs(t, ts.URL, "ola", "a-password-that-is-long")
	if code, body := cReq(t, ola, ts, "PUT", "/api/v1/me/start-favourites/travel", ""); code != http.StatusOK || !strings.Contains(string(body), `"processIds":["travel"]`) {
		t.Fatalf("mark = %d: %s", code, body)
	}
	if code, body := cReq(t, ola, ts, "PUT", "/api/v1/me/start-favourites/no-such-process", ""); code != http.StatusNotFound {
		t.Fatalf("mark of an undeployed process = %d: %s, want 404", code, body)
	}

	again := signInAs(t, ts.URL, "ola", "a-password-that-is-long")
	if code, body := cReq(t, again, ts, "GET", "/api/v1/me/start-favourites", ""); code != http.StatusOK || !strings.Contains(string(body), `"processIds":["travel"]`) {
		t.Fatalf("second session reads = %d: %s", code, body)
	}
	ben := signInAs(t, ts.URL, "ben", "a-password-that-is-long")
	if code, body := cReq(t, ben, ts, "GET", "/api/v1/me/start-favourites", ""); code != http.StatusOK || !strings.Contains(string(body), `"processIds":[]`) {
		t.Fatalf("another person reads = %d: %s, want an empty list", code, body)
	}
	if code, body := cReq(t, ola, ts, "DELETE", "/api/v1/me/start-favourites/travel", ""); code != http.StatusOK || !strings.Contains(string(body), `"processIds":[]`) {
		t.Fatalf("unmark = %d: %s", code, body)
	}
}

// TestStartFavouritesAreBrowserSideWithoutAuthentication: with nobody signed in there
// is no person to keep them for, and the 404 is what tells the console to keep the
// stars in the browser instead.
func TestStartFavouritesAreBrowserSideWithoutAuthentication(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, "GET", "/api/v1/me/start-favourites", "", ""); code != http.StatusNotFound {
		t.Fatalf("GET without authentication = %d: %s, want 404", code, body)
	}
}
