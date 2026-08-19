package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

func newInstalled(t *testing.T) *marketplaceStore {
	t.Helper()
	ms, err := newMarketplaceStore(filepath.Join(t.TempDir(), "marketplace"))
	if err != nil {
		t.Fatalf("newMarketplaceStore: %v", err)
	}
	return ms
}

func rawTemplate(name string) json.RawMessage {
	return json.RawMessage(`{"name":"` + name + `","appliesTo":["bpmn:ServiceTask"],"properties":[]}`)
}

// TestMarketplaceStoreRoundTripAndOrder saves records out of order and reads them
// back oldest-install-first, and proves get finds a specific one.
func TestMarketplaceStoreRoundTripAndOrder(t *testing.T) {
	ms := newInstalled(t)
	save := func(id string, at int64) {
		if err := ms.Save(installedTemplate{ID: id, PackageID: id, Kind: packageKindConnector, Template: rawTemplate(id), InstalledAt: at}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	save("b", 200)
	save("a", 100)
	save("c", 300)
	got, err := ms.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"a", "b", "c"} // InstalledAt ascending
	if len(got) != len(want) {
		t.Fatalf("loadAll returned %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, w)
		}
	}
	rec, ok, err := ms.Get("a")
	if err != nil || !ok || rec.PackageID != "a" {
		t.Fatalf("get a = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestMarketplaceStoreTieBreakByID proves records installed in the same second
// sort deterministically by id.
func TestMarketplaceStoreTieBreakByID(t *testing.T) {
	ms := newInstalled(t)
	for _, id := range []string{"c", "a", "b"} {
		if err := ms.Save(installedTemplate{ID: id, Template: rawTemplate(id), InstalledAt: 42}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := ms.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	for i, w := range []string{"a", "b", "c"} {
		if got[i].ID != w {
			t.Errorf("position %d = %q, want %q (tie-break by id)", i, got[i].ID, w)
		}
	}
}

// TestMarketplaceStoreOverwriteAndDelete proves re-installing an id replaces the
// record and uninstall is idempotent.
func TestMarketplaceStoreOverwriteAndDelete(t *testing.T) {
	ms := newInstalled(t)
	if err := ms.Save(installedTemplate{ID: "p", Version: "1.0.0", Template: rawTemplate("p"), InstalledAt: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ms.Save(installedTemplate{ID: "p", Version: "2.0.0", Template: rawTemplate("p"), InstalledAt: 2}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, err := ms.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Version != "2.0.0" {
		t.Fatalf("after overwrite want one v2.0.0 record, got %+v", got)
	}
	if err := ms.Delete("p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := ms.Get("p"); ok {
		t.Fatal("record still present after delete")
	}
	if err := ms.Delete("p"); err != nil {
		t.Errorf("delete of absent record = %v, want nil (idempotent)", err)
	}
}

// TestMarketplaceStoreErrorPaths covers mkdir, decode, stray-file, and missing-dir
// branches, mirroring the other sidecar stores' error coverage.
func TestMarketplaceStoreErrorPaths(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := newMarketplaceStore(filepath.Join(f, "marketplace")); err == nil {
		t.Fatal("newMarketplaceStore under a file: want error")
	}

	dir := filepath.Join(t.TempDir(), "marketplace")
	ms, err := newMarketplaceStore(dir)
	if err != nil {
		t.Fatalf("newMarketplaceStore: %v", err)
	}

	bad := ms.FileFor("p")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := ms.LoadAll(); err == nil {
		t.Fatal("loadAll of invalid JSON: want error")
	}
	if _, _, err := ms.Get("p"); err == nil {
		t.Fatal("get of invalid JSON: want error")
	}

	if err := os.Remove(bad); err != nil {
		t.Fatalf("remove bad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	got, err := ms.LoadAll()
	if err != nil {
		t.Fatalf("loadAll after cleanup: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stray .json should be ignored, got %d records", len(got))
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := ms.Save(installedTemplate{ID: "p"}); err == nil {
		t.Fatal("save with no dir: want error")
	}
	if err := ms.Delete("p"); err == nil {
		t.Fatal("delete with no dir: want error")
	}
	if _, err := ms.LoadAll(); err == nil {
		t.Fatal("loadAll with no dir: want error")
	}
}

// TestMarketplaceChecksumDeterministic proves the checksum ignores key order and
// whitespace but changes when the template content changes.
func TestMarketplaceChecksumDeterministic(t *testing.T) {
	a, err := marketplaceChecksum(json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("checksum a: %v", err)
	}
	b, err := marketplaceChecksum(json.RawMessage(`{ "b": 2,  "a": 1 }`))
	if err != nil {
		t.Fatalf("checksum b: %v", err)
	}
	if a != b {
		t.Errorf("checksum should ignore key order/whitespace: %s != %s", a, b)
	}
	c, err := marketplaceChecksum(json.RawMessage(`{"a":1,"b":3}`))
	if err != nil {
		t.Fatalf("checksum c: %v", err)
	}
	if a == c {
		t.Error("checksum should change when content changes")
	}
	if _, err := marketplaceChecksum(json.RawMessage(`{nope`)); err == nil {
		t.Error("checksum of invalid JSON: want error")
	}
}

// TestValidatePackage covers each rejection branch and the happy path.
func TestValidatePackage(t *testing.T) {
	good := marketplacePackage{ID: "x", Version: "1.0.0", Kind: packageKindConnector, Title: "X", Template: rawTemplate("x")}
	if err := validatePackage(good); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(p *marketplacePackage)
	}{
		{"no id", func(p *marketplacePackage) { p.ID = " " }},
		{"no version", func(p *marketplacePackage) { p.Version = "" }},
		{"no title", func(p *marketplacePackage) { p.Title = "" }},
		{"bad kind", func(p *marketplacePackage) { p.Kind = "widget" }},
		{"empty template", func(p *marketplacePackage) { p.Template = nil }},
		{"template not object", func(p *marketplacePackage) { p.Template = json.RawMessage(`[1,2]`) }},
		{"checksum mismatch", func(p *marketplacePackage) { p.Checksum = "sha256:deadbeef" }},
		{"embedded secret", func(p *marketplacePackage) {
			p.Template = json.RawMessage(`{"properties":[{"password":"hunter2"}]}`)
		}},
	}
	for _, tc := range cases {
		p := good
		tc.mut(&p)
		if err := validatePackage(p); err == nil {
			t.Errorf("%s: want validation error, got nil", tc.name)
		}
	}
}

// TestScanForSecrets pins the reference-vs-literal distinction the no-secrets guard
// draws: references and expressions pass, hard-coded credential values do not.
func TestScanForSecrets(t *testing.T) {
	ok := []string{
		`{"credentialsRef":"prod-smtp"}`,               // a reference is fine
		`{"secretRef":"vault-key"}`,                    // ...however the key is spelled
		`{"password":""}`,                              // empty is not a literal secret
		`{"token":"=secrets.apiToken"}`,                // a FEEL expression is not a literal
		`{"apiKey":"${env.KEY}"}`,                      // a placeholder is not a literal
		`{"label":"Password","binding":{"name":"pw"}}`, // a label/name is not a secret key
	}
	for _, s := range ok {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		if err := scanForSecrets(v); err != nil {
			t.Errorf("scanForSecrets(%s) = %v, want nil", s, err)
		}
	}
	bad := []string{
		`{"password":"hunter2"}`,
		`{"apiKey":"AKIA1234"}`,
		`{"nested":[{"clientSecret":"s3cr3t"}]}`,
		`{"passphrase":"correct horse"}`,
	}
	for _, s := range bad {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		if err := scanForSecrets(v); err == nil {
			t.Errorf("scanForSecrets(%s) = nil, want a secret-value error", s)
		}
	}
}

// TestBundledCatalogValid loads the curated packages compiled into the binary and
// asserts every one validates (checksum, kind, no secrets) and all three kinds are
// represented — the guard that keeps a bad bundled package from shipping.
func TestBundledCatalogValid(t *testing.T) {
	catalog, err := loadBundledCatalog()
	if err != nil {
		t.Fatalf("loadBundledCatalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("bundled catalog is empty")
	}
	kinds := map[string]bool{}
	for _, p := range catalog {
		if err := validatePackage(p); err != nil {
			t.Errorf("bundled package %s invalid: %v", p.ID, err)
		}
		if p.Checksum == "" {
			t.Errorf("bundled package %s has no checksum", p.ID)
		}
		kinds[p.Kind] = true
	}
	for _, k := range []string{packageKindConnector, packageKindServiceTask, packageKindScriptTask} {
		if !kinds[k] {
			t.Errorf("bundled catalog covers no %q package", k)
		}
	}
}

// TestLoadCatalogErrors covers loadCatalog's failure branches against an in-memory
// filesystem: a missing directory, undecodable JSON, an invalid package, and a
// duplicate id. Non-json and directory entries are skipped.
func TestLoadCatalogErrors(t *testing.T) {
	if _, err := loadCatalog(fstest.MapFS{}, "missing"); err == nil {
		t.Error("loadCatalog of a missing dir: want error")
	}

	good := `{"id":"a","version":"1.0.0","kind":"connector","title":"A","template":{"x":1}}`
	dupB := `{"id":"a","version":"1.0.0","kind":"connector","title":"B","template":{"y":2}}`

	badJSON := fstest.MapFS{"cat/x.json": &fstest.MapFile{Data: []byte(`{nope`)}}
	if _, err := loadCatalog(badJSON, "cat"); err == nil {
		t.Error("loadCatalog of undecodable json: want error")
	}

	invalid := fstest.MapFS{"cat/x.json": &fstest.MapFile{Data: []byte(`{"id":"a","version":"1","kind":"widget","title":"A","template":{}}`)}}
	if _, err := loadCatalog(invalid, "cat"); err == nil {
		t.Error("loadCatalog of an invalid package: want error")
	}

	dup := fstest.MapFS{
		"cat/a.json":    &fstest.MapFile{Data: []byte(good)},
		"cat/b.json":    &fstest.MapFile{Data: []byte(dupB)},
		"cat/notes.txt": &fstest.MapFile{Data: []byte("ignored")},
		"cat/sub":       &fstest.MapFile{Mode: os.ModeDir},
	}
	if _, err := loadCatalog(dup, "cat"); err == nil {
		t.Error("loadCatalog with a duplicate id: want error")
	}

	okFS := fstest.MapFS{"cat/a.json": &fstest.MapFile{Data: []byte(good)}}
	got, err := loadCatalog(okFS, "cat")
	if err != nil {
		t.Fatalf("loadCatalog of a valid package: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" || got[0].Checksum == "" {
		t.Fatalf("loadCatalog = %+v, want one checksummed package 'a'", got)
	}
}

// TestMarketplaceHandlerErrorPaths covers the install/list/uninstall handler error
// branches: an empty install list, a validation failure, and store I/O failures.
func TestMarketplaceHandlerErrorPaths(t *testing.T) {
	srv := newServerForErrors(t)

	// A fresh server has nothing installed: the nil-slice-to-[] branch, 200 with [].
	rr := httptest.NewRecorder()
	srv.handleListInstalled(rr, httptest.NewRequest("GET", "/api/v1/marketplace/installed", nil))
	if rr.Code != 200 || rr.Body.String() != "[]\n" {
		t.Errorf("empty installed list = %d %q, want 200 \"[]\\n\"", rr.Code, rr.Body.String())
	}

	// Install of a package that fails validation → 422. Inject an invalid connector
	// (a data-only kind, so no admin gate) carrying an embedded secret.
	srv.marketplace = append(srv.marketplace, marketplacePackage{
		ID: "bad.secret", Version: "1.0.0", Kind: packageKindConnector, Title: "Bad",
		Template: json.RawMessage(`{"password":"hunter2"}`),
	})
	req := httptest.NewRequest("POST", "/api/v1/marketplace/packages/bad.secret/install", nil)
	req.SetPathValue("id", "bad.secret")
	rr = httptest.NewRecorder()
	srv.handleInstallMarketplacePackage(rr, req)
	if rr.Code != 422 {
		t.Errorf("install of an invalid package = %d, want 422", rr.Code)
	}

	// Point the store at a path that cannot be written/read so save/loadAll/delete
	// surface I/O errors as 500s.
	srv.marketplaceStore = brokenStore(newMarketplaceStore(filepath.Join(t.TempDir(), "gone")))

	req = httptest.NewRequest("POST", "/api/v1/marketplace/packages/"+"atlas.rest-outbound"+"/install", nil)
	req.SetPathValue("id", "atlas.rest-outbound")
	rr = httptest.NewRecorder()
	srv.handleInstallMarketplacePackage(rr, req)
	if rr.Code != 500 {
		t.Errorf("install with a broken store = %d, want 500", rr.Code)
	}

	rr = httptest.NewRecorder()
	srv.handleListInstalled(rr, httptest.NewRequest("GET", "/api/v1/marketplace/installed", nil))
	if rr.Code != 500 {
		t.Errorf("list with a broken store = %d, want 500", rr.Code)
	}

	req = httptest.NewRequest("DELETE", "/api/v1/marketplace/installed/"+"atlas.rest-outbound", nil)
	req.SetPathValue("id", "atlas.rest-outbound")
	rr = httptest.NewRecorder()
	srv.handleUninstall(rr, req)
	if rr.Code != 500 {
		t.Errorf("uninstall with a broken store = %d, want 500", rr.Code)
	}
}

// scriptTaskID returns the id of the first script-task package in the catalog.
func scriptTaskID(t *testing.T, srv *Server) string {
	t.Helper()
	for _, p := range srv.marketplace {
		if p.carriesCode() {
			return p.ID
		}
	}
	t.Fatal("no script-task package in catalog")
	return ""
}

// TestInstallScriptTaskAdminGate proves that, with auth on, installing a
// code-bearing (script-task) package requires the admin role, while a non-admin is
// refused — the ADR-0081 trust split enforced at the handler.
func TestInstallScriptTaskAdminGate(t *testing.T) {
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
	srv, err := New(proc, store, dir, WithAuth())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	id := scriptTaskID(t, srv)

	call := func(p *Principal) int {
		req := httptest.NewRequest("POST", "/api/v1/marketplace/packages/"+id+"/install", nil)
		req.SetPathValue("id", id)
		if p != nil {
			req = req.WithContext(withPrincipal(context.Background(), p))
		}
		rr := httptest.NewRecorder()
		srv.handleInstallMarketplacePackage(rr, req)
		return rr.Code
	}

	if code := call(&Principal{UserID: "u1", Roles: []string{"user"}}); code != 403 {
		t.Errorf("non-admin script-task install = %d, want 403", code)
	}
	if code := call(nil); code != 403 {
		t.Errorf("unauthenticated script-task install = %d, want 403", code)
	}
	if code := call(&Principal{UserID: "admin", Roles: []string{RoleAdmin}}); code != 200 {
		t.Errorf("admin script-task install = %d, want 200", code)
	}
}
