package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-0134 Phase 4, end to end: an application's source travels as a curated tree
// — a manifest plus native .bpmn and .form.json files — and is identified across
// servers by a portable key rather than by the random per-server id.
//
// The headline case is a clone: export from one server, import into a second that
// has never seen the application, export again, and get the same bytes. That is
// the round-trip stability ADR-0134 promises as a test rather than a hope.

// sourceImportResult mirrors the import response.
type sourceImportResult struct {
	ApplicationID string   `json:"applicationId"`
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	Created       bool     `json:"created"`
	Processes     int      `json:"processes"`
	Forms         int      `json:"forms"`
	Decisions     int      `json:"decisions"`
	Untracked     []string `json:"untracked"`
}

// saveDraftIn saves a BPMN draft into an application.
func saveDraftIn(t *testing.T, ts *httptest.Server, appID, processID, name string) {
	t.Helper()
	xml := fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="%s" name="%s" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`, processID, name)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID, xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
}

// saveFormIn saves a form definition into an application.
func saveFormIn(t *testing.T, ts *httptest.Server, appID, id, name string) {
	t.Helper()
	payload := fmt.Sprintf(`{"id":%q,"name":%q,"projectId":%q,"schema":{"components":[],"type":"default"}}`, id, name, appID)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", payload, "application/json")
	if code != http.StatusOK {
		t.Fatalf("save form status=%d body=%s", code, body)
	}
}

// addDecisionIn registers a DMN reference in an application.
func addDecisionIn(t *testing.T, ts *httptest.Server, appID, name, modelRef string) {
	t.Helper()
	payload := fmt.Sprintf(`{"name":%q,"modelRef":%q,"projectId":%q}`, name, modelRef, appID)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/dmnrefs", payload, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create dmn ref status=%d body=%s", code, body)
	}
}

// exportSource downloads an application's source archive.
func exportSource(t *testing.T, ts *httptest.Server, appID string) []byte {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/source", "", "")
	if code != http.StatusOK {
		t.Fatalf("export source status=%d body=%s", code, body)
	}
	return body
}

// importSource uploads a source archive and decodes the result.
func importSource(t *testing.T, ts *httptest.Server, archive []byte) (int, sourceImportResult, []byte) {
	t.Helper()
	code, body := doReqBytes(t, ts, http.MethodPost, "/api/v1/applications/source", archive, "application/gzip")
	var res sourceImportResult
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("decode import result: %v (body=%s)", err, body)
		}
	}
	return code, res, body
}

// TestApplicationSourceRoundTripAcrossServers is the clone: a repository written
// by one server, read by another that has never seen the application, exports back
// to the same bytes.
func TestApplicationSourceRoundTripAcrossServers(t *testing.T) {
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Mitarbeiter Onboarding")
	saveDraftIn(t, origin, appID, "onboarding", "Onboarding")
	saveDraftIn(t, origin, appID, "it_ausstattung", "IT Ausstattung")
	saveFormIn(t, origin, appID, "neuer-mitarbeiter", "Neuer Mitarbeiter")
	addDecisionIn(t, origin, appID, "Freigabestufe", "temis://models/freigabe")

	archive := exportSource(t, origin, appID)

	clone := newTestServer(t)
	code, res, body := importSource(t, clone, archive)
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	if !res.Created {
		t.Errorf("import into a fresh server did not create the application: %+v", res)
	}
	if res.Key != "mitarbeiter-onboarding" || res.Name != "Mitarbeiter Onboarding" {
		t.Errorf("imported identity = %q/%q", res.Key, res.Name)
	}
	if res.Processes != 2 || res.Forms != 1 || res.Decisions != 1 {
		t.Errorf("imported counts = %+v", res)
	}
	if res.ApplicationID == appID {
		t.Errorf("the clone reused the publisher's application id %q; ids are per-server", appID)
	}

	// The property the whole layout rests on.
	again := exportSource(t, clone, res.ApplicationID)
	if !bytes.Equal(archive, again) {
		t.Errorf("export → import → export is not byte-stable (%d vs %d bytes)", len(archive), len(again))
	}

	// And the artifacts really landed, not just their manifest entries.
	code, listed := doReq(t, clone, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts status=%d", code)
	}
	for _, want := range []string{"onboarding", "it_ausstattung"} {
		if !strings.Contains(string(listed), want) {
			t.Errorf("draft %q is missing from the clone: %s", want, listed)
		}
	}
	code, xml := doReq(t, clone, http.MethodGet, "/api/v1/drafts/onboarding/xml", "", "")
	if code != http.StatusOK || !strings.Contains(string(xml), `<startEvent id="s"/>`) {
		t.Errorf("imported XML status=%d body=%s", code, xml)
	}
}

// TestApplicationSourceReimportUpdatesInPlace: the same key twice is the same
// application, updated — not a second copy.
func TestApplicationSourceReimportUpdatesInPlace(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erste")
	archive := exportSource(t, ts, appID)

	code, res, body := importSource(t, ts, archive)
	if code != http.StatusOK {
		t.Fatalf("re-import status=%d body=%s", code, body)
	}
	if res.Created {
		t.Errorf("re-import created a second application: %+v", res)
	}
	if res.ApplicationID != appID {
		t.Errorf("re-import landed in %q, want %q", res.ApplicationID, appID)
	}
	code, list := doReq(t, ts, http.MethodGet, "/api/v1/applications", "", "")
	if code != http.StatusOK {
		t.Fatalf("list applications status=%d", code)
	}
	apps := ownApplications(t, list)
	if len(apps) != 1 {
		t.Fatalf("got %d applications, want 1: %s", len(apps), list)
	}
	if apps[0].Key != "onboarding" {
		t.Errorf("application key = %q, want the derived slug", apps[0].Key)
	}
}

// TestApplicationSourceImportNeverDeletes: a tree that omits a local artifact
// reports it rather than removing it. A tree is not proof that something was
// deleted on purpose, and a silently dropped diagram is not recoverable.
func TestApplicationSourceImportNeverDeletes(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "tracked", "Tracked")
	archive := exportSource(t, ts, appID)

	// A draft the exported tree knows nothing about.
	saveDraftIn(t, ts, appID, "later", "Später hinzugefügt")

	code, res, body := importSource(t, ts, archive)
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	if len(res.Untracked) != 1 || res.Untracked[0] != "process:later" {
		t.Errorf("untracked = %v, want [process:later]", res.Untracked)
	}
	code, _ = doReq(t, ts, http.MethodGet, "/api/v1/drafts/later/xml", "", "")
	if code != http.StatusOK {
		t.Errorf("the untracked draft was deleted by the import (status=%d)", code)
	}
}

// TestApplicationSourceImportRefusesForeignArtifact: adopting a process that
// belongs to another application would move it out from under whoever is working
// in it. Refuse, and change nothing.
func TestApplicationSourceImportRefusesForeignArtifact(t *testing.T) {
	ts := newTestServer(t)
	mine := mkApp(t, ts, "Meine App")
	saveDraftIn(t, ts, mine, "shared-id", "Meins")
	archive := exportSource(t, ts, mine)

	// A second application already owns that process id here.
	other := mkApp(t, ts, "Andere App")
	code, body := doReq(t, ts, http.MethodPatch, "/api/v1/drafts/shared-id",
		`{"projectId":"`+other+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("move draft status=%d body=%s", code, body)
	}

	code, _, body = importSource(t, ts, archive)
	if code != http.StatusConflict {
		t.Fatalf("import status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "shared-id") {
		t.Errorf("refusal does not name the artifact: %s", body)
	}
	// Nothing moved: the draft still belongs to the other application.
	code, drafts := doReq(t, ts, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts status=%d", code)
	}
	if !strings.Contains(string(drafts), other) {
		t.Errorf("the refused import moved the draft anyway: %s", drafts)
	}
}

// TestApplicationSourceImportRefusesBadArchives: every unreadable body is a 400
// with a reason, not a 500 and not a partial import.
func TestApplicationSourceImportRefusesBadArchives(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"not gzip", []byte("hello"), "gzip"},
		{"empty", nil, "gzip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := doReqBytes(t, ts, http.MethodPost, "/api/v1/applications/source", c.body, "application/gzip")
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", code, body)
			}
			if !strings.Contains(string(body), c.want) {
				t.Errorf("body=%s, want a mention of %q", body, c.want)
			}
		})
	}
}

// TestApplicationSourceExportHeaders: the download is an attachment named after
// the application's key, so an operator ends up with a recognisable file.
func TestApplicationSourceExportHeaders(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Order to Cash")
	res, err := http.Get(ts.URL + "/api/v1/applications/" + appID + "/source")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type=%q, want application/gzip", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "order-to-cash-source.tar.gz") {
		t.Errorf("Content-Disposition=%q, want the key-derived filename", cd)
	}
}

// TestApplicationKeyIsDerivedOnceAndStaysUnique: the key is assigned lazily, kept
// afterwards, and never handed to two applications — it is the identity, so a
// collision would fork one application into two on the next import.
func TestApplicationKeyIsDerivedOnceAndStaysUnique(t *testing.T) {
	ts := newTestServer(t)
	first := mkApp(t, ts, "Onboarding")
	second := mkApp(t, ts, "Onboarding") // same name, deliberately

	exportSource(t, ts, first)
	exportSource(t, ts, second)

	code, list := doReq(t, ts, http.MethodGet, "/api/v1/applications", "", "")
	if code != http.StatusOK {
		t.Fatalf("list applications status=%d", code)
	}
	keys := map[string]string{}
	for _, a := range ownApplications(t, list) {
		if a.Key == "" {
			t.Errorf("application %q has no key after export", a.ID)
			continue
		}
		if prev, dup := keys[a.Key]; dup {
			t.Errorf("applications %q and %q share the key %q", prev, a.ID, a.Key)
		}
		keys[a.Key] = a.ID
	}
	if keys["onboarding"] != first || keys["onboarding-2"] != second {
		t.Errorf("keys = %v, want onboarding for %q and onboarding-2 for %q", keys, first, second)
	}

	// Renaming must not move the key: it is the identity a clone is matched on, so
	// a key that followed the display name would fork the application in two on the
	// next import.
	code, body := doReq(t, ts, http.MethodPatch, "/api/v1/applications/"+first,
		`{"name":"Ganz anderer Name"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", code, body)
	}
	exportSource(t, ts, first)
	code, list2 := doReq(t, ts, http.MethodGet, "/api/v1/applications", "", "")
	if code != http.StatusOK {
		t.Fatalf("list applications status=%d", code)
	}
	if !strings.Contains(string(list2), `"key":"onboarding"`) {
		t.Errorf("the key changed on a second export: %s", list2)
	}
}

// TestApplicationSourceRenamesFromTheTree: the repository is the source of truth
// for the application's name too; the key, not the name, is what identifies it.
func TestApplicationSourceRenamesFromTheTree(t *testing.T) {
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Onboarding")
	saveDraftIn(t, origin, appID, "p1", "Eins")
	archive := exportSource(t, origin, appID)

	// Rename locally, then re-import the tree.
	code, body := doReq(t, origin, http.MethodPatch, "/api/v1/applications/"+appID,
		`{"name":"Umbenannt"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", code, body)
	}
	code, res, body := importSource(t, origin, archive)
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	if res.ApplicationID != appID || res.Name != "Onboarding" {
		t.Errorf("import result = %+v, want %q renamed back to Onboarding", res, appID)
	}
}

// TestApplicationSourceExportUnknownApplication: a 404 rather than an empty tree.
func TestApplicationSourceExportUnknownApplication(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/applications/nope/source", "", "")
	if code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", code, body)
	}
}

// ownApplications decodes an application listing and drops the protected system
// project (ADR-0122), which every server bootstraps and which no source tree ever
// touches.
func ownApplications(t *testing.T, body []byte) []struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Protected bool   `json:"protected"`
} {
	t.Helper()
	var all []struct {
		ID        string `json:"id"`
		Key       string `json:"key"`
		Protected bool   `json:"protected"`
	}
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode applications: %v (body=%s)", err, body)
	}
	out := all[:0]
	for _, a := range all {
		if !a.Protected {
			out = append(out, a)
		}
	}
	return out
}

// TestApplicationSourceUnderAuth: a source tree is the whole application, so who
// may download or write one follows the ADR-0071 sharing rules exactly — and a
// key that names an application the caller has no role on hides that it exists.
func TestApplicationSourceUnderAuth(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for _, u := range []string{"alice", "bob"} {
		if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+u+`","password":"password1","roles":["modeler","operator","user"]}`); code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", u, code, body)
		}
	}
	alice, bob := newClient(t), newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK ||
		login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("login failed")
	}

	code, body := cReq(t, alice, ts, "POST", "/api/v1/applications", `{"name":"Geheim"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	if app.Key != "geheim" {
		t.Errorf("new application key = %q, want a key assigned at creation", app.Key)
	}

	// Alice can download her own source; Bob cannot even learn it exists.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/applications/"+app.ID+"/source", ""); code != http.StatusOK {
		t.Fatalf("alice export = %d, want 200", code)
	}
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/applications/"+app.ID+"/source", ""); code != http.StatusNotFound {
		t.Fatalf("bob export = %d, want 404", code)
	}

	archive := func(c *http.Client) []byte {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/applications/"+app.ID+"/source", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return data
	}(alice)

	// Bob holds the tree but no role on the application its key names: refused,
	// without confirming that the key is taken.
	code, body = cReqBytes(t, bob, ts, http.MethodPost, "/api/v1/applications/source", archive, "application/gzip")
	if code != http.StatusNotFound {
		t.Fatalf("bob import = %d %s, want 404", code, body)
	}
	if !strings.Contains(string(body), "no application with that key") {
		t.Errorf("bob import body = %s", body)
	}

	// Alice re-imports her own tree: allowed, and it lands in the same application.
	code, body = cReqBytes(t, alice, ts, http.MethodPost, "/api/v1/applications/source", archive, "application/gzip")
	if code != http.StatusOK {
		t.Fatalf("alice import = %d %s", code, body)
	}
	var res sourceImportResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if res.ApplicationID != app.ID || res.Created {
		t.Errorf("alice import = %+v, want an in-place update of %q", res, app.ID)
	}

	// Bob imports a tree for an application nobody has: created, and owned by him.
	bobsTree := retagArchive(t, archive, "bobs-app", "Bobs App")
	code, body = cReqBytes(t, bob, ts, http.MethodPost, "/api/v1/applications/source", bobsTree, "application/gzip")
	if code != http.StatusOK {
		t.Fatalf("bob import of a new key = %d %s", code, body)
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if !res.Created {
		t.Fatalf("bob's import did not create the application: %+v", res)
	}
	// Owned by Bob: Alice must not see it.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/applications/"+res.ApplicationID+"/source", ""); code != http.StatusNotFound {
		t.Errorf("alice can read bob's new application (%d); the importer should own it", code)
	}
}

// cReqBytes is cReq for a binary body through a logged-in client.
func cReqBytes(t *testing.T, c *http.Client, ts *httptest.Server, method, path string, body []byte, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// retagArchive rewrites the manifest's key and name in a source archive, standing
// in for a repository somebody created for a different application.
func retagArchive(t *testing.T, archive []byte, key, name string) []byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gzr.Close()
	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		if hdr.Name == "atlas.json" {
			var man map[string]any
			if err := json.Unmarshal(data, &man); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			man["key"], man["name"] = key, name
			if data, err = json.MarshalIndent(man, "", "  "); err != nil {
				t.Fatalf("encode manifest: %v", err)
			}
			data = append(data, '\n')
		}
		hdr.Size = int64(len(data))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return out.Bytes()
}

// TestApplicationSourceImportRefusesTreeWithoutManifest: a readable archive that
// is not a source tree is a 400 naming what is missing, not a silent no-op.
func TestApplicationSourceImportRefusesTreeWithoutManifest(t *testing.T) {
	ts := newTestServer(t)
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{Name: "processes/x.bpmn", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("<x/>\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	code, body := doReqBytes(t, ts, http.MethodPost, "/api/v1/applications/source", buf.Bytes(), "application/gzip")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", code, body)
	}
	if !strings.Contains(string(body), "atlas.json") {
		t.Errorf("body=%s, want it to name the missing manifest", body)
	}
}
