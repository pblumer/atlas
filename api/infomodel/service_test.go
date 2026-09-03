package infomodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/runloop"
)

const testModelID = "0123456789abcdef0123456789abcdef"

type fixture struct {
	service *Service
	store   *Store
	access  map[string]ApplicationAccess
	// minted hands out deterministic ids: the model's, then one per class or
	// association the canvas posts without one.
	minted int
	idErr  error
	// accessErr makes the application-scope resolver fail, which is how a test
	// reaches the branches where authorization itself is broken.
	accessErr error
}

func newFixture(t *testing.T) *fixture {
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

	fx := &fixture{store: store, access: map[string]ApplicationAccess{
		"app-1":     {Exists: true, CanView: true, CanEdit: true},
		"hidden":    {Exists: true},
		"viewer":    {Exists: true, CanView: true},
		"protected": {Exists: true, CanView: true, CanEdit: true, Protected: true},
	}}
	fx.service = New(loop, store,
		func(_ *http.Request, applicationID string) (ApplicationAccess, error) {
			return fx.access[applicationID], fx.accessErr
		},
		func() (string, error) {
			if fx.idErr != nil {
				return "", fx.idErr
			}
			if fx.minted == 0 {
				fx.minted++
				return testModelID, nil
			}
			fx.minted++
			return fmt.Sprintf("%032x", fx.minted), nil
		},
		func() time.Time { return time.Unix(1_700_000_000, 0) },
	)
	return fx
}

func (fx *fixture) create(t *testing.T, app, name string) Summary {
	t.Helper()
	rec := requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"applicationId": app, "name": name}, http.StatusCreated)
	var out Summary
	decodeResponse(t, rec, &out)
	return out
}

// putModel replaces a model's content and returns the raw recorder, so a test can
// assert on a refusal as easily as on a success.
func (fx *fixture) putModel(t *testing.T, id string, body map[string]any, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/infomodel/models/"+id, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", id)
	return invoke(t, fx.service.HandleUpdate, req, status)
}

func (fx *fixture) get(t *testing.T, id string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/infomodel/models/"+id, nil)
	req.SetPathValue("id", id)
	return invoke(t, fx.service.HandleGet, req, status)
}

// classesBody is the fixture model's content in the shape the canvas posts.
func classesBody() map[string]any {
	m := orderModel()
	return map[string]any{"classes": m.Classes, "associations": m.Associations}
}

// TestServiceLifecycle walks create → list → update → read → delete, which is the
// whole of what the canvas does.
func TestServiceLifecycle(t *testing.T) {
	fx := newFixture(t)
	created := fx.create(t, "app-1", "Sales")
	if created.ID != testModelID || created.Revision != 1 {
		t.Fatalf("created = %#v", created)
	}
	if created.Classes != 0 {
		t.Errorf("a new model starts with %d classes, want 0", created.Classes)
	}

	listed := request(t, fx.service.HandleList, http.MethodGet, "/api/v1/infomodel/models", nil, http.StatusOK)
	var list []Summary
	decodeResponse(t, listed, &list)
	if len(list) != 1 || list[0].ID != testModelID {
		t.Fatalf("list = %#v", list)
	}
	// A listing is for a library row, so it must not carry the whole graph.
	if bytes.Contains(listed.Body.Bytes(), []byte(`"attributes"`)) {
		t.Error("the listing carries class content; it should carry counts")
	}

	body := classesBody()
	body["revision"] = 1
	updated := fx.putModel(t, testModelID, body, http.StatusOK)
	var got modelResponse
	decodeResponse(t, updated, &got)
	if got.Revision != 2 {
		t.Errorf("revision = %d, want 2", got.Revision)
	}
	if len(got.Classes) != 5 || len(got.Associations) != 2 {
		t.Errorf("saved %d classes / %d associations, want 5 / 2", len(got.Classes), len(got.Associations))
	}
	if !got.Validation.Valid {
		t.Errorf("a valid model came back with findings: %v", got.Validation.Findings)
	}

	read := fx.get(t, testModelID, http.StatusOK)
	var reread modelResponse
	decodeResponse(t, read, &reread)
	if len(reread.Classes) != 5 || !reread.Validation.Valid {
		t.Errorf("re-read = %d classes, valid=%v", len(reread.Classes), reread.Validation.Valid)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/infomodel/models/"+testModelID, nil)
	delReq.SetPathValue("id", testModelID)
	invoke(t, fx.service.HandleDelete, delReq, http.StatusNoContent)
	fx.get(t, testModelID, http.StatusNotFound)
}

// TestServiceRefusesAnInvalidModel is the store's guarantee: every model on disk is
// one the subset accepts, so a deploy resolving itemSubjectRef against it never
// meets a half-model. The refusal carries the findings, because "invalid" with no
// list is not something a modeler can act on.
func TestServiceRefusesAnInvalidModel(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "app-1", "Sales")

	m := orderModel()
	m.Classes[1].Attributes[3].Type = "Adress"
	rec := fx.putModel(t, testModelID, map[string]any{
		"classes": m.Classes, "associations": m.Associations, "revision": 1,
	}, http.StatusBadRequest)

	var out struct {
		Error    string    `json:"error"`
		Findings []Finding `json:"findings"`
	}
	decodeResponse(t, rec, &out)
	if len(out.Findings) == 0 {
		t.Fatalf("refused with no findings: %s", rec.Body.String())
	}
	if out.Findings[0].Code != CodeUnknownType {
		t.Errorf("finding = %+v, want the unresolvable type", out.Findings[0])
	}
	// And nothing was written: the stored model is still the empty one.
	read := fx.get(t, testModelID, http.StatusOK)
	var stored modelResponse
	decodeResponse(t, read, &stored)
	if len(stored.Classes) != 0 || stored.Revision != 1 {
		t.Errorf("a refused write changed the store: %d classes at revision %d", len(stored.Classes), stored.Revision)
	}
}

// TestServiceRevisionConflict covers two people with the same diagram open: the
// second save is refused rather than silently discarding the first one's classes.
func TestServiceRevisionConflict(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "app-1", "Sales")

	body := classesBody()
	body["revision"] = 1
	fx.putModel(t, testModelID, body, http.StatusOK) // now at revision 2

	stale := classesBody()
	stale["revision"] = 1
	rec := fx.putModel(t, testModelID, stale, http.StatusConflict)
	if !bytes.Contains(rec.Body.Bytes(), []byte("reload")) {
		t.Errorf("conflict message does not say what to do: %s", rec.Body.String())
	}

	// Omitting the revision is the deliberate escape hatch for a caller that has no
	// editor open — a script, or the canvas after a reload.
	forced := classesBody()
	fx.putModel(t, testModelID, forced, http.StatusOK)
}

// TestServiceMintsIDsForNewShapes covers the canvas posting a box it just drew: it
// has no id to give, and ids are what associations point at, so the server hands
// them out.
func TestServiceMintsIDsForNewShapes(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "app-1", "Sales")

	rec := fx.putModel(t, testModelID, map[string]any{
		"classes": []Class{{Name: "Invoice", Stereotype: StereotypeBusinessObject,
			Attributes: []Attribute{{Name: "number", Type: TypeString, Multiplicity: MultOne}}}},
		"revision": 1,
	}, http.StatusOK)
	var got modelResponse
	decodeResponse(t, rec, &got)
	if len(got.Classes) != 1 || got.Classes[0].ID == "" {
		t.Fatalf("class came back without an id: %+v", got.Classes)
	}
	if got.Classes[0].ID == testModelID {
		t.Error("the class reused the model's id")
	}
}

// TestServiceAccessRules covers the four answers the application scope produces.
// A model in an application the caller cannot see must be indistinguishable from
// one that does not exist — a 403 would confirm it is there.
func TestServiceAccessRules(t *testing.T) {
	fx := newFixture(t)

	// Creating into an application the caller cannot reach is a bad request, not a
	// 404: the caller named the application, so denying its existence teaches nothing.
	requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"applicationId": "nope", "name": "X"}, http.StatusBadRequest)

	id := fx.create(t, "app-1", "Sales").ID
	fx.access["app-1"] = ApplicationAccess{Exists: true} // access revoked
	fx.get(t, id, http.StatusNotFound)
	fx.putModel(t, id, classesBody(), http.StatusNotFound)

	fx.access["app-1"] = ApplicationAccess{Exists: true, CanView: true} // read-only
	fx.get(t, id, http.StatusOK)
	fx.putModel(t, id, classesBody(), http.StatusForbidden)

	fx.access["app-1"] = ApplicationAccess{Exists: true, CanView: true, CanEdit: true, Protected: true}
	fx.putModel(t, id, classesBody(), http.StatusForbidden)

	// The listing filters by the same rule, so a model the caller cannot view is
	// absent rather than redacted.
	fx.access["app-1"] = ApplicationAccess{Exists: true}
	listed := request(t, fx.service.HandleList, http.MethodGet, "/api/v1/infomodel/models", nil, http.StatusOK)
	var list []Summary
	decodeResponse(t, listed, &list)
	if len(list) != 0 {
		t.Errorf("list = %#v, want empty", list)
	}
}

// TestServiceSchemaProjection covers the derived contract: it names a class, it
// comes back as a standard schema, and it reports what it dropped.
func TestServiceSchemaProjection(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "app-1", "Sales")
	body := classesBody()
	body["revision"] = 1
	fx.putModel(t, testModelID, body, http.StatusOK)

	schemaReq := func(query string, status int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/infomodel/models/"+testModelID+"/schema"+query, nil)
		req.SetPathValue("id", testModelID)
		return invoke(t, fx.service.HandleSchema, req, status)
	}

	rec := schemaReq("?class=Order", http.StatusOK)
	var p Projection
	decodeResponse(t, rec, &p)
	if p.Class != "Order" || p.Schema["type"] != "object" {
		t.Fatalf("projection = %+v", p)
	}
	if len(p.Loss) == 0 {
		t.Error("a projection that reports no loss has not been asked what it dropped")
	}

	// A schema describes one class, so the route must say so rather than guess.
	schemaReq("", http.StatusBadRequest)
	schemaReq("?class=Invoice", http.StatusBadRequest)
}

// TestServiceSubsetIsServed pins that the browser is handed the one table rather
// than carrying its own copy.
func TestServiceSubsetIsServed(t *testing.T) {
	fx := newFixture(t)
	rec := request(t, fx.service.HandleSubset, http.MethodGet, "/api/v1/infomodel/subset", nil, http.StatusOK)
	var s Subset
	decodeResponse(t, rec, &s)
	if s.Version != SubsetVersion || len(s.Matrix) == 0 {
		t.Fatalf("subset = %+v", s)
	}
	// The matrix is what lets the canvas refuse a line mid-drag without a round trip.
	if got := s.Matrix[StereotypeValueType+">"+StereotypeBusinessObject]; len(got) != 1 || got[0] != KindAssociation {
		t.Errorf("matrix[valueType>businessObject] = %v, want only a plain association", got)
	}
}

// TestServiceRejectsMalformedRequests covers the input guards.
func TestServiceRejectsMalformedRequests(t *testing.T) {
	fx := newFixture(t)
	request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		bytes.NewReader([]byte("{")), http.StatusBadRequest)
	requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"applicationId": "app-1"}, http.StatusBadRequest)
	requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"name": "X"}, http.StatusBadRequest)

	fx.create(t, "app-1", "Sales")
	empty := ""
	fx.putModel(t, testModelID, map[string]any{"name": empty}, http.StatusBadRequest)
	fx.putModel(t, "0000000000000000000000000000dead", classesBody(), http.StatusNotFound)
}

// TestVocabularyOnLoop covers the read a deploy uses to resolve itemSubjectRef:
// every class an application owns, by name, with the inherited attributes folded
// in — and the distinction between an application that models nothing and one that
// simply has no class of that name.
func TestVocabularyOnLoop(t *testing.T) {
	fx := newFixture(t)
	fx.create(t, "app-1", "Sales")
	body := classesBody()
	body["revision"] = 1
	fx.putModel(t, testModelID, body, http.StatusOK)

	vocab, err := fx.service.VocabularyOnLoop("app-1")
	if err != nil {
		t.Fatalf("VocabularyOnLoop: %v", err)
	}
	if !vocab.Modeled() {
		t.Fatal("an application with a model reports itself unmodeled")
	}
	order, ok := vocab.Class("Order")
	if !ok {
		t.Fatal("Order is missing")
	}
	if got := order.Identity; len(got) != 1 || got[0] != "id" {
		t.Errorf("Order identity = %v, want [id]", got)
	}

	// Another application, and no application at all, both model nothing — so the
	// type checks stay silent rather than warning about every data object.
	for _, id := range []string{"app-2", ""} {
		other, err := fx.service.VocabularyOnLoop(id)
		if err != nil {
			t.Fatalf("VocabularyOnLoop(%q): %v", id, err)
		}
		if other.Modeled() {
			t.Errorf("application %q reports itself modeled", id)
		}
		if _, ok := other.Class("Order"); ok {
			t.Errorf("application %q sees another's classes", id)
		}
	}
}

// --- request helpers, mirroring api/panorama's ---

func requestJSON(t *testing.T, handler http.HandlerFunc, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, handler, method, path, jsonBody(t, body), status)
}

func jsonBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(raw)
}

func request(t *testing.T, handler http.HandlerFunc, method, path string, body io.Reader, status int) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
	}
	return invoke(t, handler, req, status)
}

func invoke(t *testing.T, handler http.HandlerFunc, req *http.Request, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != status {
		t.Fatalf("%s %s status = %d, want %d; body=%s", req.Method, req.URL.Path, w.Code, status, w.Body.String())
	}
	return w
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

// TestServiceRemapsCanvasLocalIDs is the case the canvas actually produces: it draws
// two new boxes and a line between them, and it has no way to name a box the server
// has not issued an id for yet — so it makes up local handles and points the line at
// those. The server must mint real ids *and* rewrite the ends, or the relationship
// arrives pointing at classes that no longer exist under those names.
func TestServiceRemapsCanvasLocalIDs(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID

	rec := fx.putModel(t, id, map[string]any{
		"classes": []Class{
			{ID: "new-abc", Name: "Customer", Stereotype: StereotypeBusinessObject},
			{ID: "new-def", Name: "Order", Stereotype: StereotypeBusinessObject},
		},
		"associations": []Association{{ID: "new-xyz", Kind: KindAssociation,
			From: End{ClassID: "new-abc", Multiplicity: MultOne},
			To:   End{ClassID: "new-def", Role: "orders", Multiplicity: MultMany}}},
		"revision": 1,
	}, http.StatusOK)

	var got modelResponse
	decodeResponse(t, rec, &got)
	if !got.Validation.Valid {
		t.Fatalf("the remapped model does not validate: %v", got.Validation.Findings)
	}
	for _, c := range got.Classes {
		if strings.HasPrefix(c.ID, "new-") {
			t.Errorf("class %s kept its local handle %q", c.Name, c.ID)
		}
	}
	a := got.Associations[0]
	if strings.HasPrefix(a.ID, "new-") {
		t.Errorf("association kept its local handle %q", a.ID)
	}
	customer, _ := got.ClassByName("Customer")
	order, _ := got.ClassByName("Order")
	if a.From.ClassID != customer.ID || a.To.ClassID != order.ID {
		t.Errorf("association ends = %s → %s, want %s → %s",
			a.From.ClassID, a.To.ClassID, customer.ID, order.ID)
	}
	// The reading survives the remap: it is the part a person wrote.
	if a.To.Role != "orders" || a.To.Multiplicity != MultMany {
		t.Errorf("end reading lost: %+v", a.To)
	}
}

// TestServiceKeepsMintedIDsStable pins the other half: an id this server issued is
// not reissued on the next save, because associations and — later — a BPMN model's
// resolved type point at it.
func TestServiceKeepsMintedIDsStable(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID
	body := classesBody()
	body["revision"] = 1
	first := fx.putModel(t, id, body, http.StatusOK)
	var before modelResponse
	decodeResponse(t, first, &before)

	again := map[string]any{"classes": before.Classes, "associations": before.Associations}
	second := fx.putModel(t, id, again, http.StatusOK)
	var after modelResponse
	decodeResponse(t, second, &after)
	for i := range before.Classes {
		if before.Classes[i].ID != after.Classes[i].ID {
			t.Errorf("class %s was reissued: %s → %s", before.Classes[i].Name,
				before.Classes[i].ID, after.Classes[i].ID)
		}
	}
}

// TestServiceSavesDataStores covers the third collection the document carries: a
// store arrives with a local handle like everything else, is minted an id, and is
// refused when it says something the model cannot support.
func TestServiceSavesDataStores(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID

	m := storeModel()
	rec := fx.putModel(t, id, map[string]any{
		"classes": m.Classes, "associations": m.Associations,
		"stores": []DataStore{{ID: "new-store", Name: "Orders", Class: "Order",
			Worker: "clio-main", Mode: StoreModeRead}},
		"revision": 1,
	}, http.StatusOK)
	var got modelResponse
	decodeResponse(t, rec, &got)
	if len(got.Stores) != 1 || got.Stores[0].ID == "" || strings.HasPrefix(got.Stores[0].ID, "new-") {
		t.Fatalf("stores = %+v, want one with a minted id", got.Stores)
	}
	if !got.Validation.Valid {
		t.Errorf("a well-formed store came back with findings: %v", got.Validation.Findings)
	}
	// The listing counts it, so a library row says how much of the model is there.
	listed := request(t, fx.service.HandleList, http.MethodGet, "/api/v1/infomodel/models", nil, http.StatusOK)
	var list []Summary
	decodeResponse(t, listed, &list)
	if len(list) != 1 || list[0].Stores != 1 {
		t.Errorf("listing = %+v, want one store counted", list)
	}

	// A store over a class that cannot be addressed by identity is refused with the
	// finding, and nothing is written.
	bad := fx.putModel(t, id, map[string]any{
		"classes": m.Classes, "associations": m.Associations,
		"stores": []DataStore{{Name: "Addresses", Class: "Address", Mode: StoreModeRead}},
	}, http.StatusBadRequest)
	var refusal struct {
		Findings []Finding `json:"findings"`
	}
	decodeResponse(t, bad, &refusal)
	if len(refusal.Findings) == 0 || refusal.Findings[0].Code != CodeStoreClassNotStorable {
		t.Errorf("findings = %+v, want the unstorable class named", refusal.Findings)
	}
}

// TestServiceReadsAnUnstatedStoreModeAsRead is for the caller that has no mode to
// state — an agent writing through MCP, say, where read is the only thing on offer.
// It gets a read store, not a refusal naming a mode it never chose.
func TestServiceReadsAnUnstatedStoreModeAsRead(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID

	m := storeModel()
	rec := fx.putModel(t, id, map[string]any{
		"classes": m.Classes, "associations": m.Associations,
		"stores":   []map[string]any{{"name": "Orders", "class": "Order", "worker": "clio-main"}},
		"revision": 1,
	}, http.StatusOK)
	var got modelResponse
	decodeResponse(t, rec, &got)
	if len(got.Stores) != 1 || got.Stores[0].Mode != StoreModeRead {
		t.Fatalf("stores = %+v, want one read store", got.Stores)
	}
	if !got.Validation.Valid {
		t.Errorf("a store with no stated mode came back with findings: %v", got.Validation.Findings)
	}
}
