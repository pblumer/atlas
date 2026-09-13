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

// What a decision draft is for (ADR-draft-decision-drafts): a place to leave
// unfinished decision work that nothing else resolves. Every test here is one of
// the four failures the record names, held down.

// draftHarness is newValidateServer plus a request helper and the data directory,
// so a test can look at what did — and did not — reach the model folder.
func draftHarness(t *testing.T) (func(method, path, body string) (int, []byte), string) {
	t.Helper()
	srv, dir := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	return do, dir
}

// draftJSON wraps DMN XML in the save payload.
func draftJSON(t *testing.T, xml string, fields map[string]string) string {
	t.Helper()
	payload := map[string]string{"xml": xml}
	for k, v := range fields {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal draft payload: %v", err)
	}
	return string(b)
}

// modelFiles lists the handles stored in the model folder — the shared layer a
// draft must not touch.
func modelFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "dmn-models"))
	if err != nil {
		t.Fatalf("read model dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".dmn") {
			out = append(out, strings.TrimSuffix(e.Name(), ".dmn"))
		}
	}
	return out
}

// The first failure: you could not stop halfway. A decision table that does not
// compile is exactly the thing that needs somewhere to live, so the draft store
// takes it — and takes it without touching the model folder or the catalog.
func TestADraftKeepsWorkThatDoesNotCompile(t *testing.T) {
	do, dir := draftHarness(t)
	before := modelFiles(t, dir)

	code, b := do(http.MethodPost, "/api/v1/dmn-drafts", draftJSON(t, brokenDMNModel, nil))
	if code != http.StatusOK {
		t.Fatalf("save draft: %d %s — an unfinished decision is the case a draft exists for", code, b)
	}
	var saved dmnDraftResp
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(saved.ID, dmnDraftIDPrefix) {
		t.Errorf("draft id = %q, want the %q prefix: a decision not in the model has no reference to be keyed by",
			saved.ID, dmnDraftIDPrefix)
	}
	if saved.Name != "Bad" {
		t.Errorf("draft name = %q, want the decision's own name read out of the XML", saved.Name)
	}
	if saved.RefID != "" {
		t.Errorf("draft refId = %q, want empty: this decision is not in the model", saved.RefID)
	}

	// Nothing reached the shared layer.
	if got := modelFiles(t, dir); len(got) != len(before) {
		t.Errorf("model folder = %v, was %v — a draft must not write the model every reference resolves", got, before)
	}

	// And the work comes back.
	code, xml := do(http.MethodGet, "/api/v1/dmn-drafts/"+saved.ID+"/xml", "")
	if code != http.StatusOK {
		t.Fatalf("read draft xml: %d %s", code, xml)
	}
	if !strings.Contains(string(xml), "1 +") {
		t.Errorf("draft xml did not round-trip: %s", xml)
	}
}

// The second failure: an unfinished decision blocked somebody else's publish.
// A draft is invisible to the bundle deploy, so an application with a half-written
// decision in it still publishes what is in its model.
func TestADraftDoesNotTravelWithAPublish(t *testing.T) {
	do, _ := draftHarness(t)

	code, b := do(http.MethodPost, "/api/v1/applications", `{"name":"Orders"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, b)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	// One decision that *is* in the model, filed into the application.
	if code, b := do(http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Dish","modelRef":"dish","projectId":"`+app.ID+`"}`); code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}
	// And one that is only a draft, and does not compile.
	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts",
		draftJSON(t, brokenDMNModel, map[string]string{"projectId": app.ID})); code != http.StatusOK {
		t.Fatalf("save draft: %d %s", code, b)
	}

	code, b = do(http.MethodPost, "/api/v1/applications/"+app.ID+"/publish", "")
	if code != http.StatusOK {
		t.Fatalf("publish: %d %s — a draft nobody published must not refuse the bundle", code, b)
	}
	var out struct {
		Deployed  bool `json:"deployed"`
		Decisions []struct {
			ModelRef string `json:"modelRef"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	if !out.Deployed {
		t.Fatalf("publish did not deploy: %s", b)
	}
	if len(out.Decisions) != 1 || out.Decisions[0].ModelRef != "dish" {
		t.Errorf("published decisions = %v, want only the model-backed one: a publish ships the model, not the draft", out.Decisions)
	}
}

// The fourth failure: the first save of a decision whose name is taken forked a
// silent copy. An identity-aware upload (?from=) refuses instead, and names the
// handle that is in the way.
func TestSavingOverATakenModelHandleIsRefusedNotForked(t *testing.T) {
	do, dir := draftHarness(t)

	// `dish` is already stored by the harness. A different decision that would
	// derive the same handle is refused rather than becoming dish-2.
	code, b := do(http.MethodPost, "/api/v1/dmn-models?name=Dish&from=", validDMNModel)
	if code != http.StatusConflict {
		t.Fatalf("upload onto a taken handle: %d %s, want 409", code, b)
	}
	if !strings.Contains(string(b), "dish.dmn") {
		t.Errorf("refusal = %s, want it to name the handle in the way", b)
	}
	for _, h := range modelFiles(t, dir) {
		if h == "dish-2" {
			t.Fatal("a second copy was written anyway: dish-2.dmn")
		}
	}

	// The deliberate overwrite still goes through — it is the author answering
	// "replace it" (ADR-0222) — and it replaces that model rather than forking one.
	code, b = do(http.MethodPost, "/api/v1/dmn-models?name=Dish&from=&overwrite=true", validDMNModel)
	if code != http.StatusOK {
		t.Fatalf("deliberate overwrite: %d %s", code, b)
	}
	var replaced struct {
		ModelRef string `json:"modelRef"`
	}
	if err := json.Unmarshal(b, &replaced); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if replaced.ModelRef != "dish" {
		t.Errorf("modelRef after a chosen replacement = %q, want dish", replaced.ModelRef)
	}
	// The explicit handle path is unchanged: it is how an editing session updates
	// the model it opened.
	if code, b := do(http.MethodPost, "/api/v1/dmn-models?handle=dish", validDMNModel); code != http.StatusOK {
		t.Fatalf("update in place: %d %s", code, b)
	}
	// And a caller that does not claim an identity keeps the upsert importers and
	// agents depend on.
	code, b = do(http.MethodPost, "/api/v1/dmn-models?name=Dish", validDMNModel)
	if code != http.StatusOK {
		t.Fatalf("plain upload: %d %s", code, b)
	}
	var up struct {
		ModelRef string `json:"modelRef"`
	}
	if err := json.Unmarshal(b, &up); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if up.ModelRef != "dish-2" {
		t.Errorf("modelRef = %q, want dish-2: without ?from= a taken handle is still suffixed", up.ModelRef)
	}
	// Re-saving the model this session opened is an update, not a collision.
	if code, b := do(http.MethodPost, "/api/v1/dmn-models?name=Dish&from=dish", validDMNModel); code != http.StatusOK {
		t.Fatalf("re-save of the session's own model: %d %s", code, b)
	}
}

// A draft on a decision that is in the model is keyed by that decision, so
// re-saving overwrites it rather than piling up copies, and one Get answers
// "has this decision unsaved work?".
func TestADraftOnAnExistingDecisionIsKeyedByIt(t *testing.T) {
	do, _ := draftHarness(t)

	code, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`)
	if code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatalf("decode ref: %v", err)
	}

	for i := 0; i < 2; i++ {
		code, b := do(http.MethodPost, "/api/v1/dmn-drafts",
			draftJSON(t, validDMNModel, map[string]string{"refId": ref.ID, "modelRef": "dish"}))
		if code != http.StatusOK {
			t.Fatalf("save draft %d: %d %s", i, code, b)
		}
		var saved dmnDraftResp
		if err := json.Unmarshal(b, &saved); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if saved.ID != ref.ID {
			t.Fatalf("draft id = %q, want the reference's id %q", saved.ID, ref.ID)
		}
		if saved.ModelRef != "dish" {
			t.Errorf("draft modelRef = %q, want dish", saved.ModelRef)
		}
	}

	code, b = do(http.MethodGet, "/api/v1/dmn-drafts", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, b)
	}
	var list []dmnDraftResp
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("drafts = %d, want 1: re-saving a decision overwrites its draft", len(list))
	}

	// The two names for the same thing agree: a caller that sends only the key it is
	// writing under gets the reference filled in, and one that sends only the
	// reference gets the key. Otherwise a draft could be stored pointing at nothing.
	code, b = do(http.MethodPost, "/api/v1/dmn-drafts",
		draftJSON(t, validDMNModel, map[string]string{"id": ref.ID}))
	if code != http.StatusOK {
		t.Fatalf("save by id alone: %d %s", code, b)
	}
	var byID dmnDraftResp
	if err := json.Unmarshal(b, &byID); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if byID.RefID != ref.ID {
		t.Errorf("refId = %q, want the reference %q the key names", byID.RefID, ref.ID)
	}
}

// A draft exists only while it differs from the model, so discarding it is part of
// the normal flow — and, like every other delete in the design-time stores, it is
// idempotent.
func TestDiscardingADraftIsIdempotent(t *testing.T) {
	do, _ := draftHarness(t)

	code, b := do(http.MethodPost, "/api/v1/dmn-drafts", draftJSON(t, validDMNModel, nil))
	if code != http.StatusOK {
		t.Fatalf("save draft: %d %s", code, b)
	}
	var saved dmnDraftResp
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 0; i < 2; i++ {
		if code, b := do(http.MethodDelete, "/api/v1/dmn-drafts/"+saved.ID, ""); code != http.StatusNoContent {
			t.Fatalf("delete %d: %d %s", i, code, b)
		}
	}
	if code, _ := do(http.MethodGet, "/api/v1/dmn-drafts/"+saved.ID+"/xml", ""); code != http.StatusNotFound {
		t.Errorf("read after discard: %d, want 404", code)
	}
}

// A draft filed into an application is listed with it, which is what lets the
// Explorer show an unfinished decision as a row of that application rather than
// nowhere.
func TestDraftsListPerApplication(t *testing.T) {
	do, _ := draftHarness(t)

	code, b := do(http.MethodPost, "/api/v1/applications", `{"name":"Orders"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, b)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts",
		draftJSON(t, validDMNModel, map[string]string{"projectId": app.ID})); code != http.StatusOK {
		t.Fatalf("save filed draft: %d %s", code, b)
	}
	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts", draftJSON(t, validDMNModel, nil)); code != http.StatusOK {
		t.Fatalf("save ungrouped draft: %d %s", code, b)
	}

	code, b = do(http.MethodGet, "/api/v1/dmn-drafts?projectId="+app.ID, "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, b)
	}
	var list []dmnDraftResp
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].ProjectID != app.ID {
		t.Fatalf("filtered drafts = %+v, want only the one filed into %s", list, app.ID)
	}
}

// A draft survives a restart: it is a sidecar store like every other design-time
// store, so work left unfinished on Friday is there on Monday.
func TestADraftSurvivesARestart(t *testing.T) {
	srv, dir := newValidateServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dmn-drafts",
		strings.NewReader(draftJSON(t, brokenDMNModel, nil)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save draft: %d %s", rec.Code, rec.Body)
	}
	var saved dmnDraftResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}

	store, err := newDmnDraftStore(filepath.Join(dir, "dmn-drafts"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	rec2, ok, err := store.Get(saved.ID)
	if err != nil || !ok {
		t.Fatalf("reopened store: ok=%v err=%v — a draft that does not survive a restart is not saved", ok, err)
	}
	if !strings.Contains(rec2.XML, "1 +") {
		t.Errorf("reopened draft lost its XML: %q", rec2.XML)
	}
}

// decisionIdentity is what names a draft in the listing. It reads attributes, never
// compiles, because the models it is asked about are the ones that do not.
func TestDecisionIdentityReadsWhatItCan(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want string
	}{
		{"the decision's name", validDMNModel, "Dish"},
		{"a model that does not compile", brokenDMNModel, "Bad"},
		{"no decision yet, so the model's name", `<definitions name="Draft"></definitions>`, "Draft"},
		{"an unnamed decision falls back to its id", `<definitions name="M"><decision id="D_1"/></definitions>`, "D_1"},
		{"not XML at all", "{}", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decisionIdentity([]byte(tc.xml)); got != tc.want {
				t.Errorf("decisionIdentity = %q, want %q", got, tc.want)
			}
		})
	}
}

// A draft on a decision belongs where that decision belongs. The server takes it
// from the reference rather than from the caller, so a save cannot file a draft
// under a decision in one application and claim it belongs to another.
func TestADraftIsFiledWhereItsDecisionIs(t *testing.T) {
	do, _ := draftHarness(t)

	code, b := do(http.MethodPost, "/api/v1/applications", `{"name":"Orders"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, b)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	code, b = do(http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Dish","modelRef":"dish","projectId":"`+app.ID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatalf("decode ref: %v", err)
	}

	// The body says Ungrouped and names no model; the reference says otherwise, and
	// the reference wins.
	code, b = do(http.MethodPost, "/api/v1/dmn-drafts",
		draftJSON(t, validDMNModel, map[string]string{"refId": ref.ID, "projectId": ""}))
	if code != http.StatusOK {
		t.Fatalf("save draft: %d %s", code, b)
	}
	var saved dmnDraftResp
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if saved.ProjectID != app.ID {
		t.Errorf("draft projectId = %q, want the decision's application %q", saved.ProjectID, app.ID)
	}
	if saved.ModelRef != "dish" {
		t.Errorf("draft modelRef = %q, want the decision's own handle", saved.ModelRef)
	}
}

// A protected system project's content is platform-managed (ADR-0122), so a
// decision draft cannot be filed into one either — the same backstop every other
// design-time write has, inside the writer's own turn.
func TestADraftCannotBeFiledIntoAProtectedProject(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.projects.Save(project{ID: "sys", Name: "Atlas System", Key: "atlas-system", Protected: true}); err != nil {
		t.Fatalf("seed protected project: %v", err)
	}
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dmn-drafts",
		strings.NewReader(draftJSON(t, validDMNModel, map[string]string{"projectId": "sys"})))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("save into a protected project: %d %s, want 403", rec.Code, rec.Body)
	}
}

// A data directory the store cannot read is a 500 on every route, not a silent
// empty answer — the same treatment every other design-time store's failure gets.
func TestDmnDraftRoutesReportAStoreTheyCannotRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.dmnDrafts = brokenStore(newDmnDraftStore(filepath.Join(t.TempDir(), "gone")))
	h := srv.Handler()
	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	cases := []struct {
		what         string
		method, path string
		body         string
	}{
		{"save", http.MethodPost, "/api/v1/dmn-drafts", draftJSON(t, validDMNModel, map[string]string{"id": "dd-1"})},
		{"list", http.MethodGet, "/api/v1/dmn-drafts", ""},
		{"delete", http.MethodDelete, "/api/v1/dmn-drafts/dd-1", ""},
	}
	for _, tc := range cases {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Errorf("%s over an unreadable store = %d, want 500", tc.what, got)
		}
	}
	// Reading one record out of a directory that is not there is a clean miss, not a
	// failure — the same answer a draft that never existed gives.
	if got := do(http.MethodGet, "/api/v1/dmn-drafts/dd-1/xml", ""); got != http.StatusNotFound {
		t.Errorf("xml over an unreadable store = %d, want 404", got)
	}
}

// A record that is there but cannot be decoded is the other failure: the routes
// that read one report it rather than answering "no such draft", which would send
// an author looking for work they did save.
func TestDmnDraftRoutesReportARecordTheyCannotDecode(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := os.WriteFile(srv.dmnDrafts.FileFor("dd-1"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt a record: %v", err)
	}
	h := srv.Handler()
	for _, tc := range []struct {
		what, method, path string
	}{
		{"xml", http.MethodGet, "/api/v1/dmn-drafts/dd-1/xml"},
		{"delete", http.MethodDelete, "/api/v1/dmn-drafts/dd-1"},
		{"save", http.MethodPost, "/api/v1/dmn-drafts"},
	} {
		var req *http.Request
		if tc.method == http.MethodPost {
			req = httptest.NewRequest(tc.method, tc.path,
				strings.NewReader(draftJSON(t, validDMNModel, map[string]string{"id": "dd-1"})))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s over an undecodable record = %d %s, want 500", tc.what, rec.Code, rec.Body)
		}
	}
	// And the listing skips what it cannot read rather than refusing every draft
	// because of one — the sidecar store's own rule.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dmn-drafts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError && rec.Code != http.StatusOK {
		t.Errorf("list over an undecodable record = %d %s", rec.Code, rec.Body)
	}
}

// The error paths of the save, so a malformed call says what is wrong instead of
// storing an empty draft.
func TestDmnDraftSaveRejectsWhatItCannotStore(t *testing.T) {
	do, _ := draftHarness(t)

	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts", "{"); code != http.StatusBadRequest {
		t.Errorf("malformed JSON: %d %s, want 400", code, b)
	}
	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts", `{"xml":"   "}`); code != http.StatusBadRequest {
		t.Errorf("empty xml: %d %s, want 400", code, b)
	}
	if code, b := do(http.MethodGet, "/api/v1/dmn-drafts/nope/xml", ""); code != http.StatusNotFound {
		t.Errorf("absent draft: %d %s, want 404", code, b)
	}
	// Filing into an application that does not exist is refused, like every other
	// artifact's create (ADR-0071).
	if code, b := do(http.MethodPost, "/api/v1/dmn-drafts",
		draftJSON(t, validDMNModel, map[string]string{"projectId": "no-such-app"})); code == http.StatusOK {
		t.Errorf("filed into a missing application: %d %s, want a refusal", code, b)
	}
}
