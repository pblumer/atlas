package openapimocks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/connector/rest/openapimock"
	"github.com/pblumer/atlas/limits"
)

// petstore is the smallest document that proves the whole path: a stated example (so
// the body is the document's own bytes rather than a generated one), a base path on
// the server URL (so the base URL the Console prints is exercised), and a second
// status nobody asks for.
const petstore = `
openapi: 3.0.0
info: {title: Pet Store, version: "1.2"}
servers: [{url: "https://api.example.com/v1"}]
paths:
  /pets/{petId}:
    get:
      operationId: getPet
      responses:
        "200":
          content:
            application/json:
              schema: {type: object}
              example: {id: 7, name: Rex}
        "404":
          content:
            application/json:
              example: {error: no such pet}
`

// newService builds a service over a temp store with a running loop. That it needs
// nothing but a loop, a store, a clock and a budget is ADR-0147's property.
func newService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	// A fixed clock in unix seconds, which is what the stored timestamps are: a wall
	// clock would make asserting on them a flake (the determinism rule in AGENTS.md).
	return New(loop, store, func() int64 { return 1700000000 }), store
}

func call(t *testing.T, h http.HandlerFunc, method, path, body string, vals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for k, v := range vals {
		r.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// createBody is a create request for a document, as the Console sends it.
func createBody(name, doc string) string {
	b, _ := json.Marshal(saveParams{Name: name, Document: doc, Filename: "petstore.yaml"})
	return string(b)
}

func create(t *testing.T, svc *Service, name string) view {
	t.Helper()
	rec := call(t, svc.HandleCreate, "POST", "/api/v1/mock-apis", createBody(name, petstore), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s: status = %d, body %s", name, rec.Code, rec.Body)
	}
	var got view
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// TestCreateCompilesAndServes is the whole feature in one test: an uploaded document
// is compiled, described from what compiling found, and answering on its own base URL
// without anything else having happened.
func TestCreateCompilesAndServes(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")

	if got.Title != "Pet Store" || got.Version != "1.2" {
		t.Errorf("title/version = %q/%q, want the document's own", got.Title, got.Version)
	}
	if got.Operations != 1 {
		t.Errorf("operations = %d, want 1", got.Operations)
	}
	// The base URL carries the document's own base path: a task's URL changes by its
	// base and the rest of the path stays exactly as the real API states it.
	if got.BaseURL != "/mock-api/petstore/v1" {
		t.Errorf("baseUrl = %q, want /mock-api/petstore/v1", got.BaseURL)
	}
	if got.ServerURL != "https://api.example.com/v1" {
		t.Errorf("serverUrl = %q, want the real API's base", got.ServerURL)
	}
	if !got.Enabled || !got.Serving {
		t.Errorf("enabled/serving = %v/%v, want both true: omitting enabled switches it on", got.Enabled, got.Serving)
	}
	if got.Filename != "petstore.yaml" || got.Size != len(petstore) {
		t.Errorf("filename/size = %q/%d, want the upload's own", got.Filename, got.Size)
	}

	res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("mock call: status = %d, body %s", res.Code, res.Body)
	}
	// The document stated an example, so the mock answers it rather than a generated
	// body — ADR-0217's "the document wins", reached through the stored document.
	if !strings.Contains(res.Body.String(), `"name"`) || !strings.Contains(res.Body.String(), "Rex") {
		t.Errorf("body = %s, want the document's stated example", res.Body)
	}
}

// TestDocumentThatCannotBeServedIsRefused holds ADR-0217's rule at the upload, where
// the person who chose the document is standing. Storing it and failing later is the
// failure mode the rule exists to prevent.
func TestDocumentThatCannotBeServedIsRefused(t *testing.T) {
	svc, store := newService(t)
	for _, tc := range []struct{ name, doc, want string }{
		{"not a document", "this is not: [a document", "cannot be served"},
		{"swagger 2", "swagger: \"2.0\"\ninfo: {title: Old, version: \"1\"}\n", "cannot be served"},
		{"empty", "   ", "document is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, svc.HandleCreate, "POST", "/api/v1/mock-apis", createBody("x", tc.doc), nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body = %s, want it to mention %q", rec.Body, tc.want)
			}
		})
	}
	// Nothing was stored by any of them: a refused upload leaves no record behind.
	recs, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("stored %d records, want none after refusals", len(recs))
	}
}

func TestNameIsValidatedAndUnique(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "petstore")

	for _, tc := range []struct {
		name string
		in   string
		code int
	}{
		{"taken", "petstore", http.StatusConflict},
		{"not a url segment", "Pet Store", http.StatusBadRequest},
		{"empty", "  ", http.StatusBadRequest},
		{"leading hyphen", "-pets", http.StatusBadRequest},
		{"too long", strings.Repeat("a", maxNameLen+1), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, svc.HandleCreate, "POST", "/api/v1/mock-apis", createBody(tc.in, petstore), nil)
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.code, rec.Body)
			}
		})
	}
}

// TestSwitchedOffIsInert proves the state the record calls inert: off serves nothing
// and the stored document is still there to switch back on.
func TestSwitchedOffIsInert(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")

	off := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":false}`, map[string]string{"id": got.ID})
	if off.Code != http.StatusOK {
		t.Fatalf("switch off: status = %d, body %s", off.Code, off.Body)
	}
	var offView view
	_ = json.Unmarshal(off.Body.Bytes(), &offView)
	if offView.Enabled || offView.Serving {
		t.Errorf("enabled/serving = %v/%v, want both false", offView.Enabled, offView.Serving)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusNotFound {
		t.Errorf("mock call while off: status = %d, want 404", res.Code)
	}

	// Back on, with no document in the body: the stored one is compiled again.
	on := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":true}`, map[string]string{"id": got.ID})
	if on.Code != http.StatusOK {
		t.Fatalf("switch on: status = %d, body %s", on.Code, on.Body)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusOK {
		t.Errorf("mock call after switching on: status = %d, want 200", res.Code)
	}
}

// TestRenameStopsServingTheOldAddress is the bug a copy-on-write map invites: a rename
// that adds the new name and leaves the old one answering.
func TestRenameStopsServingTheOldAddress(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")

	rec := call(t, svc.HandleUpdate, "PUT", "/", `{"name":"pets"}`, map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body %s", rec.Code, rec.Body)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusNotFound {
		t.Errorf("old address: status = %d, want 404", res.Code)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/pets/v1/pets/7", "", nil); res.Code != http.StatusOK {
		t.Errorf("new address: status = %d, want 200", res.Code)
	}
}

// TestUpdateWithoutDocumentKeepsTheStoredOne: a rename must not need the document
// re-uploaded, and must not lose it.
func TestUpdateWithoutDocumentKeepsTheStoredOne(t *testing.T) {
	svc, store := newService(t)
	got := create(t, svc, "petstore")

	if rec := call(t, svc.HandleUpdate, "PUT", "/", `{"name":"pets"}`, map[string]string{"id": got.ID}); rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body %s", rec.Code, rec.Body)
	}
	doc, err := store.Document(got.ID)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if string(doc) != petstore {
		t.Errorf("stored document changed on a rename")
	}
}

// TestReplacingTheDocumentRedescribesTheRecord proves the stored description follows
// the document rather than going stale beside it.
func TestReplacingTheDocumentRedescribesTheRecord(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")

	replacement := `
openapi: 3.0.0
info: {title: Orders, version: "9"}
paths:
  /orders:
    get: {responses: {"200": {content: {application/json: {example: [1]}}}}}
    post: {responses: {"201": {content: {application/json: {example: {id: 1}}}}}}
`
	body, _ := json.Marshal(saveParams{Document: replacement, Filename: "orders.yaml"})
	rec := call(t, svc.HandleUpdate, "PUT", "/", string(body), map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: status = %d, body %s", rec.Code, rec.Body)
	}
	var now view
	_ = json.Unmarshal(rec.Body.Bytes(), &now)
	if now.Title != "Orders" || now.Operations != 2 {
		t.Errorf("title/operations = %q/%d, want Orders/2", now.Title, now.Operations)
	}
	// The document had no server URL, so the base URL loses the old base path with it.
	if now.BaseURL != "/mock-api/petstore" {
		t.Errorf("baseUrl = %q, want /mock-api/petstore", now.BaseURL)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/orders", "", nil); res.Code != http.StatusOK {
		t.Errorf("new document's path: status = %d, want 200", res.Code)
	}
	// And the old document's path is gone rather than still answering beside it.
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code == http.StatusOK {
		t.Errorf("the replaced document's path still answers")
	}
}

// TestJournalIsNotPublic is the decision this record makes against ADR-0217: the
// journal holds request bodies from real instances, so it is not on the public route.
func TestJournalIsNotPublic(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "petstore")

	for _, path := range []string{"/mock-api/petstore/__mock/calls", "/mock-api/petstore/__mock/report"} {
		rec := call(t, svc.HandleMock, "GET", path, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not public") {
			t.Errorf("%s: body = %s, want it to say the journal is not public", path, rec.Body)
		}
	}
}

// TestReportsCarryTheJournal is the other half: what the public route withholds is
// readable through the Mockups view, in ADR-0216's envelope.
func TestReportsCarryTheJournal(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "petstore")
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusOK {
		t.Fatalf("mock call: status = %d", res.Code)
	}

	reports := svc.Reports()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	rep := reports[0]
	if rep.Kind != "openapi" {
		t.Errorf("kind = %q, want openapi", rep.Kind)
	}
	if rep.Source != "petstore" {
		t.Errorf("source = %q, want the mock's own name", rep.Source)
	}
	var data struct {
		Calls []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(rep.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(data.Calls) != 1 || data.Calls[0].Status != http.StatusOK {
		t.Fatalf("calls = %+v, want the one served call", data.Calls)
	}
	if !strings.Contains(data.Calls[0].Path, "/pets/7") {
		t.Errorf("journalled path = %q, want the path below the mock's prefix", data.Calls[0].Path)
	}
}

// TestUnknownNameSaysNothingAboutWhatIsStored: the public route must not become a way
// to enumerate what an operator has uploaded but not switched on.
func TestUnknownNameSaysNothingAboutWhatIsStored(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")
	if rec := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":false}`, map[string]string{"id": got.ID}); rec.Code != http.StatusOK {
		t.Fatalf("switch off: %d", rec.Code)
	}
	stored := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil)
	absent := call(t, svc.HandleMock, "GET", "/mock-api/never-existed/v1/pets/7", "", nil)
	if stored.Code != absent.Code {
		t.Errorf("stored-but-off = %d, absent = %d; want the same answer", stored.Code, absent.Code)
	}
}

func TestListAndDeleteAndDocument(t *testing.T) {
	svc, store := newService(t)
	got := create(t, svc, "petstore")

	list := call(t, svc.HandleList, "GET", "/api/v1/mock-apis", "", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: status = %d", list.Code)
	}
	var listed struct{ Mocks []view }
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if len(listed.Mocks) != 1 || listed.Mocks[0].Name != "petstore" {
		t.Fatalf("listed = %+v, want the one mock", listed.Mocks)
	}

	doc := call(t, svc.HandleDocument, "GET", "/", "", map[string]string{"id": got.ID})
	if doc.Code != http.StatusOK || doc.Body.String() != petstore {
		t.Errorf("document: status = %d, body differs from the upload", doc.Code)
	}
	if cd := doc.Header().Get("Content-Disposition"); !strings.Contains(cd, "petstore.openapi") {
		t.Errorf("Content-Disposition = %q, want the mock's name", cd)
	}

	del := call(t, svc.HandleDelete, "DELETE", "/", "", map[string]string{"id": got.ID})
	if del.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body %s", del.Code, del.Body)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", res.Code)
	}
	recs, _ := store.LoadAll()
	if len(recs) != 0 {
		t.Errorf("stored %d records after delete, want none", len(recs))
	}
	// The document went with the record rather than being left behind as an orphan.
	if _, err := store.Document(got.ID); err == nil {
		t.Errorf("the document survived the record")
	}
}

func TestUnknownIDIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	id := "0123456789abcdef0123456789abcdef"
	for name, h := range map[string]http.HandlerFunc{
		"update":   svc.HandleUpdate,
		"delete":   svc.HandleDelete,
		"document": svc.HandleDocument,
	} {
		t.Run(name, func(t *testing.T) {
			rec := call(t, h, "PUT", "/", `{"enabled":true}`, map[string]string{"id": id})
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestInvalidBodyIsRefused(t *testing.T) {
	svc, _ := newService(t)
	for name, h := range map[string]http.HandlerFunc{"create": svc.HandleCreate, "update": svc.HandleUpdate} {
		t.Run(name, func(t *testing.T) {
			rec := call(t, h, "POST", "/", "{not json", map[string]string{"id": "x"})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// TestReloadCompilesWhatIsEnabled proves a restart comes back serving: the records are
// the only state, so the compiled set is rebuilt from them.
func TestReloadCompilesWhatIsEnabled(t *testing.T) {
	svc, _ := newService(t)
	on := create(t, svc, "petstore")
	off := create(t, svc, "orders")
	if rec := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":false}`, map[string]string{"id": off.ID}); rec.Code != http.StatusOK {
		t.Fatalf("switch off: %d", rec.Code)
	}

	// A second service over the same store is the restart.
	store, err := NewStore(svc.store.Dir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })
	fresh := New(loop, store, func() int64 { return 1700000001 })
	if problems := fresh.Reload(); len(problems) != 0 {
		t.Fatalf("Reload: %v", problems)
	}

	if res := call(t, fresh.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusOK {
		t.Errorf("enabled mock after reload: status = %d, want 200", res.Code)
	}
	if res := call(t, fresh.HandleMock, "GET", "/mock-api/orders/v1/pets/7", "", nil); res.Code != http.StatusNotFound {
		t.Errorf("disabled mock after reload: status = %d, want 404", res.Code)
	}
	_ = on
}

// TestReloadSkipsADocumentThatNoLongerCompiles: a dev aid left switched on must not be
// able to stop the engine, so the record is named and skipped rather than fatal.
func TestReloadSkipsADocumentThatNoLongerCompiles(t *testing.T) {
	svc, store := newService(t)
	good := create(t, svc, "petstore")
	bad := create(t, svc, "broken")

	// Corrupt the stored document behind the service's back, which is what a hand-edit
	// or a half-finished write looks like on the next start.
	rec, _, err := store.Get(bad.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := store.Save(rec, []byte("not: [a document")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	problems := svc.Reload()
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly the broken one", problems)
	}
	if !strings.Contains(problems[0].Error(), "broken") {
		t.Errorf("problem = %v, want it to name the mock", problems[0])
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil); res.Code != http.StatusOK {
		t.Errorf("the good mock: status = %d, want 200 — one bad document must not take the rest down", res.Code)
	}
	if res := call(t, svc.HandleMock, "GET", "/mock-api/broken/v1/pets/7", "", nil); res.Code != http.StatusNotFound {
		t.Errorf("the broken mock: status = %d, want 404", res.Code)
	}
	_ = good
}

// TestSwitchingOnAStoredDocumentThatRottedIsRefused: the flip answers with the reason
// rather than reporting success and serving 404s.
func TestSwitchingOnAStoredDocumentThatRottedIsRefused(t *testing.T) {
	svc, store := newService(t)
	got := create(t, svc, "petstore")
	if rec := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":false}`, map[string]string{"id": got.ID}); rec.Code != http.StatusOK {
		t.Fatalf("switch off: %d", rec.Code)
	}
	rec, _, _ := store.Get(got.ID)
	if err := store.Save(rec, []byte("not: [a document")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	on := call(t, svc.HandleUpdate, "PUT", "/", `{"enabled":true}`, map[string]string{"id": got.ID})
	if on.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", on.Code, on.Body)
	}
	if !strings.Contains(on.Body.String(), "stored document cannot be served") {
		t.Errorf("body = %s, want it to say the stored document cannot be served", on.Body)
	}
}

// TestStoreRefusesAnUnsafeID guards the one thing a key-addressed store must: a key
// from a request cannot address a file outside it.
func TestStoreRefusesAnUnsafeID(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, id := range []string{"../escape", "not-hex", ""} {
		if err := store.Save(Mock{ID: id}, []byte("x")); err == nil {
			t.Errorf("Save(%q) was allowed", id)
		}
		if err := store.SaveRecord(Mock{ID: id}); err == nil {
			t.Errorf("SaveRecord(%q) was allowed", id)
		}
		if _, err := store.Document(id); err == nil {
			t.Errorf("Document(%q) was allowed", id)
		}
	}
}

// TestPreferHeaderTravels proves the mock reached through this service is the same
// mock ADR-0217 built, not a reduced one: a stated status can still be asked for.
func TestPreferHeaderTravels(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "petstore")

	r := httptest.NewRequest("GET", "/mock-api/petstore/v1/pets/7", nil)
	r.Header.Set("Prefer", "code=404")
	rec := httptest.NewRecorder()
	svc.HandleMock(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the preferred 404; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no such pet") {
		t.Errorf("body = %s, want the document's 404 example", rec.Body)
	}
}

// TestValidateName covers the normalization the handlers rely on.
func TestValidateName(t *testing.T) {
	// An upper-case name is normalized rather than refused: the URL has to be
	// lower-case, and the response carries the name that was actually stored, so the
	// Console shows the author the address they got.
	for in, want := range map[string]string{"PetStore": "petstore", " pets ": "pets", "a-b-9": "a-b-9",
		"pet_store": "", "pets/7": "", "": ""} {
		got, err := ValidateName(in)
		if want == "" {
			if err == nil {
				t.Errorf("ValidateName(%q) = %q, want an error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("ValidateName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// TestConcurrentCallsAndChanges is the race the atomic pointer exists for: serving
// while the set is being swapped. Run under -race it is the evidence that a mocked
// call and a Console change do not contend.
func TestConcurrentCallsAndChanges(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				call(t, svc.HandleMock, "GET", "/mock-api/petstore/v1/pets/7", "", nil)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 10; j++ {
			body := fmt.Sprintf(`{"enabled":%v}`, j%2 == 0)
			call(t, svc.HandleUpdate, "PUT", "/", body, map[string]string{"id": got.ID})
		}
	}()
	wg.Wait()
}

// TestConcurrentCreatesAreBothServed is the end-to-end property: every mock a caller
// created is one a caller can reach. It passes through the loop, which serializes the
// store writes and so hides the swap's own race — TestPublishKeepsEveryConcurrentChange
// below is the one that exercises that.
func TestConcurrentCreatesAreBothServed(t *testing.T) {
	svc, _ := newService(t)

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("api-%d", i)
			rec := call(t, svc.HandleCreate, "POST", "/api/v1/mock-apis", createBody(name, petstore), nil)
			if rec.Code != http.StatusCreated {
				t.Errorf("create %s: status = %d, body %s", name, rec.Code, rec.Body)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/mock-api/api-%d/v1/pets/7", i)
		if res := call(t, svc.HandleMock, "GET", path, "", nil); res.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 — every created mock must be served", path, res.Code)
		}
	}
	if got := len(svc.Reports()); got != n {
		t.Errorf("Reports() = %d, want %d", got, n)
	}
}

// TestPublishKeepsEveryConcurrentChange covers the defect the publishing mutex fixes.
// The swap is read-copy-write, so without the lock two callers each copy the same map
// and the second stores one missing the first — a record stored and a mock never served,
// with nothing anywhere saying so.
//
// It drives publish directly rather than through a handler on purpose. Through the
// handlers the run loop serializes the store writes and all but closes the window, so
// the end-to-end test above passes with the lock removed; this one fails without it.
// Note that -race cannot find this either way: an atomic load and an atomic store are
// not a data race, and what is lost here is an update, not memory safety.
func TestPublishKeepsEveryConcurrentChange(t *testing.T) {
	svc, _ := newService(t)
	spec, err := openapimock.Load([]byte(petstore))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("api-%d", i)
			<-start // all of them inside the read-copy-write window together
			svc.publish(map[string]*openapimock.Server{name: openapimock.New(spec, openapimock.WithID(name))})
		}(i)
	}
	close(start)
	wg.Wait()

	live := *svc.live.Load()
	if len(live) != n {
		var missing []string
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("api-%d", i)
			if _, ok := live[name]; !ok {
				missing = append(missing, name)
			}
		}
		t.Fatalf("live holds %d of %d mocks; lost %v", len(live), n, missing)
	}
}

// TestReportsAreOrderedByName: the Mockups view polls, so an order that came from a
// map would reshuffle the cards between two polls.
func TestReportsAreOrderedByName(t *testing.T) {
	svc, _ := newService(t)
	for _, name := range []string{"zebra", "alpha", "middle"} {
		create(t, svc, name)
	}
	var got []string
	for _, r := range svc.Reports() {
		got = append(got, r.Source)
	}
	want := []string{"alpha", "middle", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("Reports() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Reports() = %v, want %v", got, want)
		}
	}
}

// TestTimestampsAreUnixSeconds pins the unit. The engine's own clock is nanoseconds and
// is one argument away, so a record carrying nanoseconds where every other design-time
// store carries seconds is a mistake nothing else would catch — until a card rendered a
// date fifty thousand years out.
func TestTimestampsAreUnixSeconds(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, "petstore")
	if got.CreatedAt != 1700000000 || got.UpdatedAt != 1700000000 {
		t.Fatalf("createdAt/updatedAt = %d/%d, want the injected clock's 1700000000", got.CreatedAt, got.UpdatedAt)
	}
}

// TestSkippedResponsesAreRecorded: ADR-0217 drops a response the document describes only
// in a media type it cannot generate, and says so in the standalone mock's startup
// banner. A stored mock has no startup to say it at, so the record carries it and the
// Console shows it — otherwise the hole is silent and the 406 is found weeks later.
func TestSkippedResponsesAreRecorded(t *testing.T) {
	svc, _ := newService(t)
	// A response described as JSON *and* XML, which is how the Swagger Petstore does it.
	const bothMedia = `
openapi: 3.0.0
info: {title: Pets, version: "1"}
paths:
  /pets:
    get:
      responses:
        "200":
          content:
            application/json: {example: [1]}
            application/xml: {schema: {type: object}}
`
	rec := call(t, svc.HandleCreate, "POST", "/api/v1/mock-apis", createBody("pets", bothMedia), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body)
	}
	var got view
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Skipped) == 0 {
		t.Fatalf("skipped = %v, want the XML response named", got.Skipped)
	}
	if !strings.Contains(strings.Join(got.Skipped, " "), "xml") {
		t.Errorf("skipped = %v, want it to name the media type it dropped", got.Skipped)
	}
	// The JSON half still answers: dropping a media type must not drop the operation.
	if res := call(t, svc.HandleMock, "GET", "/mock-api/pets/pets", "", nil); res.Code != http.StatusOK {
		t.Errorf("the JSON response: status = %d, want 200", res.Code)
	}
}

// TestAServiceLiteralStillHasBudgets pins what makes the zero value safe. The zero
// Limits is every ceiling at zero, and a ceiling of zero admits nothing — a service
// built as a struct literal would refuse every uploaded document as empty, which reads
// as a bad request rather than as missing configuration. New always sets them, so this
// branch only ever fires for a literal, which is exactly why it needs a test of its own.
func TestAServiceLiteralStillHasBudgets(t *testing.T) {
	if got := (&Service{}).budgets(); got != limits.Default() {
		t.Errorf("a Service literal has %+v, want the defaults", got)
	}
	want := limits.Default()
	want.Import = 4242
	if got := (&Service{Limits: want}).budgets(); got != want {
		t.Errorf("budgets = %+v, want the configured %+v", got, want)
	}
}
