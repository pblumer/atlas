package processdoc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samplePDF is the smallest byte string the upload guard accepts as a document.
var samplePDF = []byte("%PDF-1.7\nnot really a document\n")

// call drives one handler directly, with the path values the mux would have
// extracted. The service is reachable without a server, which is the property
// ADR-0147 was after — these tests are the evidence for it.
func call(t *testing.T, h http.HandlerFunc, method, body string, vals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/", nil)
	}
	for k, v := range vals {
		r.SetPathValue(k, v)
	}
	r.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// publish uploads one documentation version and returns its decoded response.
func publish(t *testing.T, svc *Service, processID, title string) docResp {
	t.Helper()
	body, err := json.Marshal(createReq{
		Title:     title,
		PDFBase64: base64.StdEncoding.EncodeToString(samplePDF),
		Elements:  []Element{{ID: "task_1", Name: "Prüfen", Documentation: "wird geprüft"}},
		XML:       "<definitions/>",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := call(t, svc.HandleCreate, http.MethodPost, string(body), map[string]string{"processId": processID})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body)
	}
	var out docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, rec.Body)
	}
	return out
}

// TestPublishAndReadBack is the area's core round trip: a published version is
// listed, fetched in full, and its document downloaded.
func TestPublishAndReadBack(t *testing.T) {
	svc, _ := newService(t)
	got := publish(t, svc, "reisebuchung", "Reisebuchung")

	if got.Version != 1 || got.ProcessID != "reisebuchung" || got.Title != "Reisebuchung" {
		t.Fatalf("create response = %+v", got)
	}
	if got.ElementCount != 1 {
		t.Errorf("elementCount = %d, want 1", got.ElementCount)
	}
	if got.ShareToken != "" {
		t.Error("a new version is shared; want private until asked")
	}

	// The listing is a summary: no element prose, no BPMN source.
	rec := call(t, svc.HandleList, http.MethodGet, "", map[string]string{"processId": "reisebuchung"})
	var list []docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rec.Body)
	}
	if len(list) != 1 || list[0].ID != got.ID {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].Elements) != 0 || list[0].XML != "" {
		t.Error("the listing carries the detail payload; want a summary")
	}

	// The single fetch is the detailed one.
	rec = call(t, svc.HandleGet, http.MethodGet, "", map[string]string{"id": got.ID})
	var full docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode get: %v (%s)", err, rec.Body)
	}
	if len(full.Elements) != 1 || full.XML != "<definitions/>" {
		t.Errorf("detail fetch lost the element prose or the source: %+v", full)
	}

	// And the document itself comes back byte for byte.
	rec = call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("pdf = %d", rec.Code)
	}
	if rec.Body.String() != string(samplePDF) {
		t.Errorf("downloaded document differs from what was uploaded")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the download is missing nosniff")
	}
}

// TestVersionsIncrementPerProcess proves the counter is per process, not global:
// two processes each start at v1 and advance independently.
func TestVersionsIncrementPerProcess(t *testing.T) {
	svc, _ := newService(t)
	if v := publish(t, svc, "a", "A").Version; v != 1 {
		t.Errorf("first version of a = %d, want 1", v)
	}
	if v := publish(t, svc, "a", "A again").Version; v != 2 {
		t.Errorf("second version of a = %d, want 2", v)
	}
	if v := publish(t, svc, "b", "B").Version; v != 1 {
		t.Errorf("first version of b = %d, want 1", v)
	}
}

// TestCreateRecordsTheDeployment proves a documented process that is deployed
// records which running version it described — the injected lookup is what
// supplies it, and it is read on the loop.
func TestCreateRecordsTheDeployment(t *testing.T) {
	svc, _ := newService(t)
	svc.deployed = func(processID string) (Deployment, bool) {
		if processID != "reisebuchung" {
			return Deployment{}, false
		}
		return Deployment{Key: 42, Version: 7}, true
	}
	got := publish(t, svc, "reisebuchung", "R")
	if got.DeploymentKey != 42 || got.DeploymentVersion != 7 {
		t.Errorf("deployment = %d/%d, want 42/7", got.DeploymentKey, got.DeploymentVersion)
	}

	// An undeployed process simply records nothing.
	if other := publish(t, svc, "entwurf", "E"); other.DeploymentKey != 0 {
		t.Errorf("an undeployed process recorded deployment %d", other.DeploymentKey)
	}
}

// TestCreateRejectsBadUploads covers the guards before anything is stored: a
// missing, non-base64, or non-PDF payload is a 400 and leaves no record.
func TestCreateRejectsBadUploads(t *testing.T) {
	svc, store := newService(t)
	for _, tc := range []struct{ name, body string }{
		{"invalid JSON", `{`},
		{"no pdf", `{"title":"x"}`},
		{"not base64", `{"title":"x","pdfBase64":"!!!!"}`},
		{"not a PDF", `{"title":"x","pdfBase64":"aGVsbG8="}`},
	} {
		rec := call(t, svc.HandleCreate, http.MethodPost, tc.body, map[string]string{"processId": "p"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", tc.name, rec.Code)
		}
	}
	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("a rejected upload stored %d record(s)", len(all))
	}
}

// TestCreateDefaultsTheTitleToTheProcessID proves an untitled export is still
// identifiable in a history listing.
func TestCreateDefaultsTheTitleToTheProcessID(t *testing.T) {
	svc, _ := newService(t)
	if got := publish(t, svc, "reisebuchung", "   "); got.Title != "reisebuchung" {
		t.Errorf("title = %q, want the process id", got.Title)
	}
}

// TestCreateUnreadableBody covers the read failure branch: a body that errors
// mid-read is a 400, not a stored half-document.
func TestCreateUnreadableBody(t *testing.T) {
	svc, _ := newService(t)
	r := httptest.NewRequest(http.MethodPost, "/", errReader{})
	r.SetPathValue("processId", "p")
	rec := httptest.NewRecorder()
	svc.HandleCreate(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable body = %d, want 400", rec.Code)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// TestShareIsIdempotent proves re-sharing returns the token already in readers'
// hands rather than rotating it, and that revoking kills exactly that URL.
func TestShareIsIdempotent(t *testing.T) {
	svc, _ := newService(t)
	rec := publish(t, svc, "p", "P")

	first := shareOnce(t, svc, rec.ID)
	if first.ShareToken == "" || first.ShareURL != PublicPath+first.ShareToken {
		t.Fatalf("share = %+v", first)
	}
	if second := shareOnce(t, svc, rec.ID); second.ShareToken != first.ShareToken {
		t.Errorf("re-sharing rotated the token: %q → %q", first.ShareToken, second.ShareToken)
	}

	// The public route serves it while shared…
	pub := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": first.ShareToken})
	if pub.Code != http.StatusOK || pub.Body.String() != string(samplePDF) {
		t.Fatalf("public fetch = %d (%d bytes)", pub.Code, pub.Body.Len())
	}

	// …and stops the moment it is revoked.
	un := call(t, svc.HandleUnshare, http.MethodDelete, "", map[string]string{"id": rec.ID})
	if un.Code != http.StatusOK {
		t.Fatalf("unshare = %d", un.Code)
	}
	if strings.Contains(un.Body.String(), "shareUrl") {
		t.Errorf("the revoke response still carries a share URL: %s", un.Body)
	}
	pub = call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": first.ShareToken})
	if pub.Code != http.StatusNotFound {
		t.Errorf("a revoked token still resolved: %d", pub.Code)
	}
}

func shareOnce(t *testing.T, svc *Service, id string) docResp {
	t.Helper()
	rec := call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("share = %d, body %s", rec.Code, rec.Body)
	}
	var out docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	return out
}

// TestUnshareAlreadyPrivateIsANoOp proves a double-click on Revoke cannot fail.
func TestUnshareAlreadyPrivateIsANoOp(t *testing.T) {
	svc, _ := newService(t)
	rec := publish(t, svc, "p", "P")
	got := call(t, svc.HandleUnshare, http.MethodDelete, "", map[string]string{"id": rec.ID})
	if got.Code != http.StatusOK {
		t.Fatalf("revoking an unshared version = %d, want 200", got.Code)
	}
}

// TestPublicRouteHidesEverything proves the anonymous surface is one
// indistinguishable 404: an unknown token, a malformed one, and a revoked one all
// answer the same, so a probe cannot learn whether a document ever existed.
func TestPublicRouteHidesEverything(t *testing.T) {
	svc, _ := newService(t)
	publish(t, svc, "p", "P") // a real, unshared version exists
	for _, tok := range []string{"deadbeef", "../secret", "not-hex", ""} {
		rec := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": tok})
		if rec.Code != http.StatusNotFound {
			t.Errorf("public fetch of %q = %d, want 404", tok, rec.Code)
		}
	}
}

// TestPublicRouteHidesAStoreFault proves an anonymous reader cannot tell a broken
// store from an unknown token: both are the same 404. Reporting a 500 here would
// confirm to a probe that the token space is backed by something real.
func TestPublicRouteHidesAStoreFault(t *testing.T) {
	svc, store := newService(t)
	rec := publish(t, svc, "p", "P")
	shared := shareOnce(t, svc, rec.ID)
	removeDir(t, store)

	got := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": shared.ShareToken})
	if got.Code != http.StatusNotFound {
		t.Fatalf("public fetch over a broken store = %d, want 404", got.Code)
	}
}

// TestPublicRouteIsRateLimited proves the injected limiter actually gates the
// anonymous route — the collaborator is wired, not merely held.
func TestPublicRouteIsRateLimited(t *testing.T) {
	svc, _ := newService(t)
	svc.allow = func(string) bool { return false }
	rec := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": "deadbeef"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("public fetch past the budget = %d, want 429", rec.Code)
	}
}

// TestShareTokenMintFailureIsReported proves a failing minter stops the share
// rather than storing an empty token, which would make the version readable at
// PublicPath with no token at all.
func TestShareTokenMintFailureIsReported(t *testing.T) {
	svc, store := newService(t)
	rec := publish(t, svc, "p", "P")
	svc.newToken = func() (string, error) { return "", errors.New("no entropy") }

	got := call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": rec.ID})
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("share with a failing minter = %d, want 500", got.Code)
	}
	stored, _, err := store.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ShareToken != "" {
		t.Errorf("a failed mint stored token %q", stored.ShareToken)
	}
}

// TestMissingVersionIsNotFound covers the by-id handlers' 404 branch.
func TestMissingVersionIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	id := "00112233445566778899aabbccddeeff"
	for name, h := range map[string]http.HandlerFunc{
		"get":     svc.HandleGet,
		"pdf":     svc.HandleGetPDF,
		"share":   svc.HandleShare,
		"unshare": svc.HandleUnshare,
	} {
		rec := call(t, h, http.MethodGet, "", map[string]string{"id": id})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s of an unknown version = %d, want 404", name, rec.Code)
		}
	}
}

// TestDeleteIsIdempotent proves pruning a version that is not there is a success,
// and that deleting a real one takes its document with it.
func TestDeleteIsIdempotent(t *testing.T) {
	svc, _ := newService(t)
	rec := publish(t, svc, "p", "P")

	for i := 0; i < 2; i++ {
		got := call(t, svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": rec.ID})
		if got.Code != http.StatusNoContent {
			t.Fatalf("delete #%d = %d, want 204", i+1, got.Code)
		}
	}
	if got := call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": rec.ID}); got.Code != http.StatusNotFound {
		t.Errorf("the document survived its record: %d", got.Code)
	}
}

// TestPruneKeepsTheNewest proves retention keeps the newest `keep` versions and
// reports exactly which ones it removed.
func TestPruneKeepsTheNewest(t *testing.T) {
	svc, _ := newService(t)
	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, publish(t, svc, "p", "P").ID)
	}

	rec := call(t, svc.HandlePrune, http.MethodPost, `{"keep":2}`, map[string]string{"processId": "p"})
	if rec.Code != http.StatusOK {
		t.Fatalf("prune = %d, body %s", rec.Code, rec.Body)
	}
	var out pruneResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode prune: %v", err)
	}
	if len(out.Deleted) != 2 || out.Kept != 2 {
		t.Fatalf("prune removed %v, kept %d", out.Deleted, out.Kept)
	}
	// The two oldest went; the two newest stayed.
	gone := map[string]bool{out.Deleted[0]: true, out.Deleted[1]: true}
	if !gone[ids[0]] || !gone[ids[1]] {
		t.Errorf("prune removed %v, want the two oldest %v", out.Deleted, ids[:2])
	}

	// Idempotent: pruning an already-short history removes nothing, and reports an
	// empty list rather than null.
	rec = call(t, svc.HandlePrune, http.MethodPost, `{"keep":2}`, map[string]string{"processId": "p"})
	if !strings.Contains(rec.Body.String(), `"deleted":[]`) {
		t.Errorf("a no-op prune reported %s, want an empty list", rec.Body)
	}
}

// TestPruneValidatesKeep proves the retention limit must be stated and
// non-negative — nothing is pruned by default, because a document is an artifact
// somebody chose to publish.
func TestPruneValidatesKeep(t *testing.T) {
	svc, _ := newService(t)
	for _, tc := range []struct{ name, body string }{
		{"invalid JSON", `{`},
		{"keep omitted", `{}`},
		{"keep negative", `{"keep":-1}`},
	} {
		rec := call(t, svc.HandlePrune, http.MethodPost, tc.body, map[string]string{"processId": "p"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", tc.name, rec.Code)
		}
	}
}

// TestPruneUnreadableBody covers the read-failure branch of the prune handler.
func TestPruneUnreadableBody(t *testing.T) {
	svc, _ := newService(t)
	r := httptest.NewRequest(http.MethodPost, "/", errReader{})
	r.SetPathValue("processId", "p")
	rec := httptest.NewRecorder()
	svc.HandlePrune(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable prune body = %d, want 400", rec.Code)
	}
}

// TestStoreFailuresSurfaceAs500 drives every handler against a store whose
// directory is gone. A fault must be reported, never rendered as an empty
// history or a successful publish that stored nothing.
func TestStoreFailuresSurfaceAs500(t *testing.T) {
	svc, store := newService(t)
	rec := publish(t, svc, "p", "P")
	removeDir(t, store)

	body, _ := json.Marshal(createReq{PDFBase64: base64.StdEncoding.EncodeToString(samplePDF)})
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		body string
		vals map[string]string
	}{
		{"create", svc.HandleCreate, string(body), map[string]string{"processId": "p"}},
		{"list", svc.HandleList, "", map[string]string{"processId": "p"}},
		{"prune", svc.HandlePrune, `{"keep":1}`, map[string]string{"processId": "p"}},
	} {
		got := call(t, tc.h, http.MethodPost, tc.body, tc.vals)
		if got.Code != http.StatusInternalServerError {
			t.Errorf("%s over a broken store = %d, want 500", tc.name, got.Code)
		}
	}

	// A by-id read of a store that is gone is a genuine not-found, not a fault:
	// the record is absent, and the handlers say so.
	if got := call(t, svc.HandleGet, http.MethodGet, "", map[string]string{"id": rec.ID}); got.Code != http.StatusNotFound {
		t.Errorf("get over a broken store = %d, want 404", got.Code)
	}
}

// TestCorruptRecordSurfacesAsAFault proves an undecodable record is a 500 rather
// than a 404: losing a published document silently is worse than reporting it.
func TestCorruptRecordSurfacesAsAFault(t *testing.T) {
	svc, store := newService(t)
	id := "00112233445566778899aabbccddeeff"
	if err := os.WriteFile(store.FileFor(id), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}
	for name, h := range map[string]http.HandlerFunc{
		"get":     svc.HandleGet,
		"pdf":     svc.HandleGetPDF,
		"share":   svc.HandleShare,
		"unshare": svc.HandleUnshare,
	} {
		rec := call(t, h, http.MethodPost, "", map[string]string{"id": id})
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s over a corrupt record = %d, want 500", name, rec.Code)
		}
	}
}

// TestMissingDocumentIsNotFound covers the record-without-its-PDF branch: a
// reader gets a 404 rather than a zero-byte file served as if it were the
// document.
func TestMissingDocumentIsNotFound(t *testing.T) {
	svc, store := newService(t)
	id := "00112233445566778899aabbccddeeff"
	if err := store.SaveRecord(Doc{ID: id, ProcessID: "p", Version: 1}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	if rec := call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": id}); rec.Code != http.StatusNotFound {
		t.Fatalf("download with no stored document = %d, want 404", rec.Code)
	}
}

// TestDeleteFailureSurfacesAs500 covers the delete handler's failure branch: an
// undeletable record must not report a prune that did not happen.
func TestDeleteFailureSurfacesAs500(t *testing.T) {
	svc, store := newService(t)
	id := "00112233445566778899aabbccddeeff"
	if err := os.MkdirAll(filepath.Join(store.FileFor(id), "child"), 0o755); err != nil {
		t.Fatalf("make undeletable record: %v", err)
	}
	if rec := call(t, svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": id}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete an undeletable record = %d, want 500", rec.Code)
	}
}
