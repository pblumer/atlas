package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/blueprintregistry"
)

// hubImportServer is a full server whose operator-pinned registry offers one
// release: an application with one process, and a catalogue beside it.
func hubImportServer(t *testing.T) (*httptest.Server, *fakeBlueprintSource) {
	t.Helper()
	_, ts, f, _ := hubImportServerAt(t, t.TempDir())
	return ts, f
}

// hubImportServerAt is hubImportServer over dir, answering the server and a stop
// func too, so a test can reach the server's stores or start another over the same
// data.
func hubImportServerAt(t *testing.T, dir string) (*Server, *httptest.Server, *fakeBlueprintSource, func()) {
	t.Helper()
	srv, stop := openAt(t, dir)
	f := &fakeBlueprintSource{origin: blueprintregistry.Origin{
		RegistryID: "org.example.hub", IndexURL: "https://hub.example.org/index.json",
		SignedBy: "sha256:" + strings.Repeat("ab", 32),
	}}
	f.offer(t, waitingProcess("proc_hub_sample"))
	srv.blueprints = f
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, f, stop
}

// offer makes the registry serve release 1.0.0 of org.example.hub-sample as an
// application whose one process is processXML.
func (f *fakeBlueprintSource) offer(t *testing.T, processXML string) {
	t.Helper()
	var archive bytes.Buffer
	if err := writeSourceArchive(&archive, []sourceFile{
		{Path: "atlas.json", Data: []byte(`{"formatVersion":1,"key":"hub-sample","name":"Hub sample","processes":[{"id":"proc_hub_sample","name":"Hub sample","path":"processes/hub.bpmn"}],"forms":[],"decisions":[]}`)},
		{Path: "processes/hub.bpmn", Data: []byte(processXML)},
		{Path: "katalog.json", Data: []byte(`{"catalogs":[{"id":"cat-hub","items":[]}],"products":[]}`)},
	}); err != nil {
		t.Fatal(err)
	}
	payload := archive.Bytes()
	sum := sha256.Sum256(payload)
	release := blueprintregistry.Release{
		BlueprintID: "org.example.hub-sample", PublisherID: "org.example", Version: "1.0.0",
		Title: "Hub sample", Description: "A sample", License: "AGPL-3.0-only",
		Atlas: blueprintregistry.Compatibility{MinVersion: "0.1.0"},
		Artifact: blueprintregistry.Artifact{Path: "packages/hub.tar.gz", SizeBytes: int64(len(payload)),
			Digest: "sha256:" + hex.EncodeToString(sum[:])},
		Needs: blueprintregistry.Needs{WorkerTypes: []string{}, ServerConstants: []string{}},
	}
	f.index = blueprintregistry.Index{SchemaVersion: 1, RegistryID: f.origin.RegistryID, Releases: []blueprintregistry.Release{release}}
	f.release, f.packageBytes = release, payload
}

func hubPost(t *testing.T, ts *httptest.Server, query string) (int, map[string]any) {
	t.Helper()
	resp, err := ts.Client().Post(ts.URL+"/api/v1/blueprints/org.example.hub-sample/releases/1.0.0/import"+query, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("answer is not JSON (%d): %s", resp.StatusCode, raw)
	}
	return resp.StatusCode, out
}

// TestHubImportInstallsTheVerifiedRelease: the server-side hub import is bound to
// the previewed digest, plans without writing on a dry run, installs the
// application through the ordinary source import, names the catalogues it leaves
// to the Console, and changes nothing the second time.
func TestHubImportInstallsTheVerifiedRelease(t *testing.T) {
	ts, f := hubImportServer(t)
	digest := f.release.Artifact.Digest

	if code, out := hubPost(t, ts, ""); code != http.StatusPreconditionRequired || out["error"] == nil {
		t.Fatalf("import without a digest = %d %v", code, out)
	}
	if code, out := hubPost(t, ts, "?digest=sha256:"+strings.Repeat("0", 64)); code != http.StatusPreconditionFailed {
		t.Fatalf("import with another digest = %d %v", code, out)
	}

	code, plan := hubPost(t, ts, "?digest="+digest+"&dryRun=true")
	if code != http.StatusOK || plan["dryRun"] != true || plan["key"] != "hub-sample" {
		t.Fatalf("dry run = %d %v", code, plan)
	}
	if resp, err := ts.Client().Get(ts.URL + "/api/v1/applications"); err != nil {
		t.Fatal(err)
	} else {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(raw), "hub-sample") {
			t.Fatalf("a dry run created the application: %s", raw)
		}
	}

	code, first := hubPost(t, ts, "?digest="+digest)
	if code != http.StatusOK || first["created"] != true || first["blueprintId"] != "org.example.hub-sample" || first["digest"] != digest {
		t.Fatalf("import = %d %v", code, first)
	}
	if cats, _ := first["cataloguesNotImported"].([]any); len(cats) != 1 || cats[0] != "cat-hub" {
		t.Fatalf("catalogues not named: %v", first["cataloguesNotImported"])
	}
	if processes, _ := first["processes"].(float64); processes != 1 {
		t.Fatalf("processes = %v", first["processes"])
	}

	code, again := hubPost(t, ts, "?digest="+digest)
	if code != http.StatusOK || again["created"] == true {
		t.Fatalf("second import = %d %v", code, again)
	}
	raw, _ := json.Marshal(again["artifacts"])
	if strings.Contains(string(raw), `"new"`) || strings.Contains(string(raw), `"changed"`) {
		t.Fatalf("a second import of the same release planned changes: %s", raw)
	}
}

// A process this server holds differently, never imported before, needs a decision:
// the hub import stops with 409, the plan and the undecided refs — and the error
// envelope keeps "error" at the top, with the release beside it.
func TestHubImportStopsOnAChangeNobodyDecided(t *testing.T) {
	ts, f := hubImportServer(t)
	other := strings.Replace(waitingProcess("proc_hub_sample"), `<userTask id="u"/>`, `<userTask id="u" name="edited here"/>`, 1)
	// The same application, its process edited on this server and never imported:
	// created and saved through the API, so there is no import baseline to compare to.
	resp, err := ts.Client().Post(ts.URL+"/api/v1/applications", "application/json", strings.NewReader(`{"name":"Hub sample"}`))
	if err != nil {
		t.Fatal(err)
	}
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK || app.ID == "" {
		t.Fatalf("creating the application = %d %+v", resp.StatusCode, app)
	}
	resp, err = ts.Client().Post(ts.URL+"/api/v1/drafts?projectId="+app.ID, "application/xml", strings.NewReader(other))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("saving the edited draft = %d", resp.StatusCode)
	}

	code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest)
	if code != http.StatusConflict || out["error"] == nil || out["undecided"] == nil || out["blueprintId"] != "org.example.hub-sample" {
		t.Fatalf("undecided import = %d %v", code, out)
	}
	code, kept := hubPost(t, ts, "?digest="+f.release.Artifact.Digest+"&keep=*")
	if code != http.StatusOK || kept["blueprintId"] == nil {
		t.Fatalf("import keeping the local edit = %d %v", code, kept)
	}
}

func TestHubImportNeedsAConfiguredRegistry(t *testing.T) {
	srv, _ := openAt(t, t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	code, _ := hubPost(t, ts, "?digest=sha256:"+strings.Repeat("a", 64))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("import without a registry = %d", code)
	}
}

// An embedder's preconfigured registry is the one the server serves.
func TestWithBlueprintRegistrySuppliesTheOperatorsRegistry(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := blueprintregistry.NewPublicClient("https://registry.example/index.json", pub)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	WithBlueprintRegistry(client)(s)
	if s.blueprints == nil {
		t.Fatal("the option did not set the registry")
	}
}
