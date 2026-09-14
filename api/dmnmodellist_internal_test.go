package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The acceptance suite for
// ADR-0330: a model whose last reference
// is deleted is still in the store, and now still in the product.
//
// The defect it closes is not a crash, it is a disappearance, so what these assert
// is that the model is *listed* — and that deleting the reference is what makes it
// show up as unreferenced rather than as gone.

// listModels reads the model store listing.
func listModels(t *testing.T, x deployTestHarness) []dmnModelResp {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/dmn-models", "")
	if code != http.StatusOK {
		t.Fatalf("list models: %d %s", code, b)
	}
	var out []dmnModelResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode models: %v (%s)", err, b)
	}
	return out
}

func modelByHandle(rows []dmnModelResp, handle string) *dmnModelResp {
	for i := range rows {
		if rows[i].Handle == handle {
			return &rows[i]
		}
	}
	return nil
}

// TestDeletingTheLastReferenceLeavesTheModelListed is the headline: before the
// deletion the model is referenced, after it the model is still there and says
// nothing points at it. That row is the only place in the product it appears.
func TestDeletingTheLastReferenceLeavesTheModelListed(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	seedReferencedDecision(t, x, "eligibility", eligibilityDMN("approve"))

	before := modelByHandle(listModels(t, x), "eligibility")
	if before == nil || !before.Referenced || len(before.References) != 1 {
		t.Fatalf("model = %+v, want it listed as referenced once", before)
	}
	if !before.Valid || before.ModelName != "Eligibility" || len(before.Decisions) != 1 {
		t.Fatalf("model = %+v, want it to describe what the model declares", before)
	}

	refID := theRefFor(t, x, "eligibility")
	if code, b := x.do(http.MethodDelete, "/api/v1/dmnrefs/"+refID, ""); code != http.StatusNoContent {
		t.Fatalf("delete ref: %d %s", code, b)
	}

	// The catalog, which reads references, has forgotten it entirely.
	code, cat := x.do(http.MethodGet, "/api/v1/decisions", "")
	if code != http.StatusOK || strings.Contains(string(cat), "eligibility") {
		t.Fatalf("catalog = %s, want the decision gone with its reference", cat)
	}
	// The store has not.
	after := modelByHandle(listModels(t, x), "eligibility")
	if after == nil {
		t.Fatal("model store = no eligibility, want the file still listed after its reference went")
	}
	if after.Referenced || len(after.References) != 0 {
		t.Fatalf("model = %+v, want it listed as pointed at by nothing", after)
	}
	if after.ModelName != "Eligibility" || len(after.Decisions) != 1 {
		t.Errorf("model = %+v, want it still to say what it is — that is how it is recognised", after)
	}
}

// TestAModelThatDoesNotCompileIsStillListed: an author who has to fix a broken
// model has to find it first, so it is listed as invalid rather than skipped.
func TestAModelThatDoesNotCompileIsStillListed(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	broken := modelByHandle(listModels(t, x), "broken") // seeded by newValidateServer
	if broken == nil {
		t.Fatal("model store = no broken model, want the file listed so it can be fixed")
	}
	if broken.Valid {
		t.Errorf("model = %+v, want valid=false", broken)
	}
	if len(broken.Decisions) != 0 {
		t.Errorf("model = %+v, want no decisions claimed for a model that does not compile", broken)
	}
}

// TestTheModelStoreListsEveryStoredHandleOnce: the listing is the inverse of the
// write path's naming. Both extensions the resolver accepts are one model, and
// anything else in the folder is not one at all.
func TestTheModelStoreListsEveryStoredHandleOnce(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	models := filepath.Join(dir, "dmn-models")
	// The same handle under both extensions resolves to one model, so it lists once.
	if err := os.WriteFile(filepath.Join(models, "dish.xml"), []byte(validDMNModel), 0o644); err != nil {
		t.Fatalf("write dish.xml: %v", err)
	}
	// Things that are not models are not listed.
	if err := os.WriteFile(filepath.Join(models, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(models, "archive"), 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}

	rows := listModels(t, x)
	seen := map[string]int{}
	for _, m := range rows {
		seen[m.Handle]++
	}
	if seen["dish"] != 1 {
		t.Errorf("dish listed %d times, want once across both extensions", seen["dish"])
	}
	if seen["notes"] != 0 || seen["archive"] != 0 {
		t.Errorf("listing = %+v, want neither a text file nor a directory", rows)
	}
	// Sorted by handle, which is the name a reference resolves by.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Handle > rows[i].Handle {
			t.Fatalf("listing is not sorted by handle: %+v", rows)
		}
	}
}

// TestAReferenceSomebodyElseHoldsStillCountsAsOne: "referenced" is a fact about the
// model and is computed over every reference; the named ones are filtered to what
// the caller may see (ADR-0071). Reporting a model as unreferenced because of who
// is looking would invite a second reference to a model that already has one.
func TestAReferenceSomebodyElseHoldsStillCountsAsOne(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	h := srv.Handler()
	session := func(id string) string {
		t.Helper()
		tok, err := srv.sessions.create(User{ID: id, Username: id, Roles: []string{RoleModeler, RoleUser}}, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return tok
	}
	as := func(tok, method, path, body string) (int, []byte) {
		t.Helper()
		var rd *strings.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		var req *http.Request
		if rd != nil {
			req = httptest.NewRequest(method, path, rd)
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	owner, stranger := session("usr_owner"), session("usr_stranger")
	if code, b := as(owner, http.MethodPost, "/api/v1/dmn-models?name=eligibility&handle=eligibility", eligibilityDMN("approve")); code != http.StatusOK {
		t.Fatalf("upload model: %d %s", code, b)
	}
	// The reference is ungrouped and owned by its creator, so it is their personal
	// space and the stranger cannot see it.
	if code, b := as(owner, http.MethodPost, "/api/v1/dmnrefs", `{"name":"Eligibility","modelRef":"eligibility"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}

	code, b := as(stranger, http.MethodGet, "/api/v1/dmn-models", "")
	if code != http.StatusOK {
		t.Fatalf("list models: %d %s", code, b)
	}
	var rows []dmnModelResp
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode models: %v (%s)", err, b)
	}
	got := modelByHandle(rows, "eligibility")
	if got == nil || !got.Referenced {
		t.Fatalf("model = %+v, want referenced=true even though the reference is not the caller's to see", got)
	}
	if len(got.References) != 0 {
		t.Fatalf("references = %+v, want none named from a space the caller cannot see", got.References)
	}
}
