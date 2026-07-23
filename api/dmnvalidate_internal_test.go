package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// A valid DMN model (a UNIQUE decision table) and a broken one (bad FEEL) used to
// exercise deploy-time resolution + validation over the filesystem resolver.
const validDMNModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="dish" namespace="http://atlas/dmn">
  <inputData id="id_season" name="Season"/>
  <decision id="Dish" name="Dish">
    <informationRequirement><requiredInput href="#id_season"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1" label="Season"><inputExpression id="ie1" typeRef="string"><text>Season</text></inputExpression></input>
      <output id="out1" label="Dish" name="Dish" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>"Winter"</text></inputEntry><outputEntry id="o1"><text>"Roastbeef"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

const brokenDMNModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d" name="broken" namespace="http://atlas/dmn">
  <decision id="Bad" name="Bad"><literalExpression id="le"><text>1 +</text></literalExpression></decision>
</definitions>`

// newValidateServer builds a Server over a known data dir (so the test can seed
// its dmn-models folder) and returns both.
func newValidateServer(t *testing.T) (*Server, string) {
	t.Helper()
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
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	// Seed the model source the DirResolver reads from.
	models := filepath.Join(dir, "dmn-models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatalf("mkdir models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(models, "dish.dmn"), []byte(validDMNModel), 0o644); err != nil {
		t.Fatalf("write dish: %v", err)
	}
	if err := os.WriteFile(filepath.Join(models, "broken.dmn"), []byte(brokenDMNModel), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	return srv, dir
}

func TestValidateDmnRefEndpoints(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()

	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	mkRef := func(name, modelRef, projectID string) string {
		body := `{"name":"` + name + `","modelRef":"` + modelRef + `"`
		if projectID != "" {
			body += `,"projectId":"` + projectID + `"`
		}
		body += `}`
		code, b := do(http.MethodPost, "/api/v1/dmnrefs", body)
		if code != http.StatusOK {
			t.Fatalf("create ref %s: status=%d body=%s", name, code, b)
		}
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("decode ref: %v", err)
		}
		return r.ID
	}

	// A project holding a valid, an unresolved, and an invalid reference.
	code, b := do(http.MethodPost, "/api/v1/projects", `{"name":"Bundle"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d", code)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	validID := mkRef("Dish decision", "dish", proj.ID)
	mkRef("Missing", "ghost", proj.ID)
	mkRef("Broken", "broken", proj.ID)

	// Single-ref validate: the valid one resolves and compiles, exposing Dish.
	code, b = do(http.MethodPost, "/api/v1/dmnrefs/"+validID+"/validate", "")
	if code != http.StatusOK {
		t.Fatalf("validate valid ref status=%d body=%s", code, b)
	}
	var vr dmnRefValidationResp
	if err := json.Unmarshal(b, &vr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !vr.Resolved || !vr.Valid || vr.ModelName != "dish" {
		t.Fatalf("valid ref result = %+v, want resolved+valid dish", vr)
	}
	found := false
	for _, d := range vr.Decisions {
		if d == "Dish" {
			found = true
		}
	}
	if !found {
		t.Errorf("decisions = %v, want Dish", vr.Decisions)
	}

	// Project preflight: three references, not all valid → OK false.
	code, b = do(http.MethodPost, "/api/v1/projects/"+proj.ID+"/validate", "")
	if code != http.StatusOK {
		t.Fatalf("validate project status=%d body=%s", code, b)
	}
	var pv projectValidationResp
	if err := json.Unmarshal(b, &pv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pv.OK {
		t.Fatalf("preflight OK = true, want false (an unresolved + an invalid ref)")
	}
	if len(pv.References) != 3 {
		t.Fatalf("preflight references = %d, want 3", len(pv.References))
	}
	var resolved, valid int
	for _, r := range pv.References {
		if r.Resolved {
			resolved++
		}
		if r.Valid {
			valid++
		}
	}
	if resolved != 2 || valid != 1 { // dish + broken resolve; only dish is valid
		t.Fatalf("preflight counts resolved=%d valid=%d, want 2 and 1", resolved, valid)
	}

	// 404s.
	if code, _ := do(http.MethodPost, "/api/v1/dmnrefs/nope/validate", ""); code != http.StatusNotFound {
		t.Fatalf("validate unknown ref = %d, want 404", code)
	}
	if code, _ := do(http.MethodPost, "/api/v1/projects/nope/validate", ""); code != http.StatusNotFound {
		t.Fatalf("validate unknown project = %d, want 404", code)
	}
}

// TestValidateEmptyProjectPasses proves a project with no DMN references passes
// the preflight (OK true, no references), so a deploy is not blocked.
func TestValidateEmptyProjectPasses(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Empty"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatalf("decode: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+proj.ID+"/validate", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status=%d", rec.Code)
	}
	var pv projectValidationResp
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !pv.OK || len(pv.References) != 0 {
		t.Fatalf("empty project preflight = %+v, want OK with no references", pv)
	}
}

// TestValidateStoreErrors covers the 500 branches of the validate handlers: a
// broken record read, a broken project read, a broken reference list, and a
// resolver infrastructure failure.
func TestValidateStoreErrors(t *testing.T) {
	srv, dir := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	realProjects := srv.projects
	realDmnrefs := srv.dmnrefs

	// Seed a real project and a real reference pointing at the broken model, so
	// the resolver-infra case has a record to read.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"P"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var proj struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &proj)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/dmnrefs", strings.NewReader(`{"name":"R","modelRef":"busy","projectId":"`+proj.ID+`"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var ref struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ref)

	// Single-ref validate: a directory at the record path makes get() fail with a
	// real read error (not "not found"), so the handler returns 500.
	badGet, err := newDmnRefStore(filepath.Join(t.TempDir(), "dmnrefs"))
	if err != nil {
		t.Fatalf("newDmnRefStore: %v", err)
	}
	if err := os.MkdirAll(badGet.fileFor(ref.ID), 0o755); err != nil {
		t.Fatalf("mkdir record: %v", err)
	}
	srv.dmnrefs = badGet
	if got := do(http.MethodPost, "/api/v1/dmnrefs/"+ref.ID+"/validate"); got != http.StatusInternalServerError {
		t.Fatalf("validate ref with broken record = %d, want 500", got)
	}
	// Project preflight can't list references when the dmn-refs directory is gone
	// (loadAll's ReadDir fails).
	srv.dmnrefs = &dmnRefStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(http.MethodPost, "/api/v1/projects/"+proj.ID+"/validate"); got != http.StatusInternalServerError {
		t.Fatalf("preflight with broken dmn store = %d, want 500", got)
	}
	srv.dmnrefs = realDmnrefs

	// Broken projects store: preflight can't read the project (dir-record errors,
	// so it's a read error rather than a clean not-found).
	psDir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(psDir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.fileFor(proj.ID), 0o755); err != nil {
		t.Fatalf("mkdir project record: %v", err)
	}
	srv.projects = ps
	if got := do(http.MethodPost, "/api/v1/projects/"+proj.ID+"/validate"); got != http.StatusInternalServerError {
		t.Fatalf("preflight with broken project read = %d, want 500", got)
	}
	srv.projects = realProjects

	// Resolver infrastructure failure: point the models dir's "busy" model at a
	// directory so ReadFile fails with a real I/O error; validate must 500.
	if err := os.MkdirAll(filepath.Join(dir, "dmn-models", "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir busy model: %v", err)
	}
	if got := do(http.MethodPost, "/api/v1/dmnrefs/"+ref.ID+"/validate"); got != http.StatusInternalServerError {
		t.Fatalf("validate ref with broken resolver = %d, want 500", got)
	}
	if got := do(http.MethodPost, "/api/v1/projects/"+proj.ID+"/validate"); got != http.StatusInternalServerError {
		t.Fatalf("preflight with broken resolver = %d, want 500", got)
	}
}
