package decisiondoc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/token"
)

// What publishing a decision as a document buys
// (ADR-0324), driven against the service directly — no
// server needed, which is the property ADR-0147's per-area shape is for.

// samplePDF is the smallest byte string the upload guard accepts as a document.
var samplePDF = []byte("%PDF-1.7\nnot really a document\n")

// eligibility is what a documented decision carries: the prose, what it reads,
// and the rules themselves.
var eligibility = Decision{
	ID: "eligibility", Name: "Eligibility",
	Description:   "Whether the applicant qualifies.",
	Inputs:        []Column{{Expression: "amount", Type: "number"}},
	HitPolicy:     "UNIQUE",
	InputColumns:  []Column{{Label: "Amount", Expression: "amount", Type: "number"}},
	OutputColumns: []Column{{Label: "Verdict", Expression: "verdict", Type: "string"}},
	Rules: []Rule{
		{Inputs: []string{">= 100"}, Outputs: []string{`"approve"`}, Description: "Big enough"},
		{Inputs: []string{"< 100"}, Outputs: []string{`"reject"`}},
	},
}

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

	return New(loop, store, func(string) bool { return true }, token.New), store
}

// call drives one handler directly, with the path values the mux would have
// extracted.
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
func publish(t *testing.T, svc *Service, decisionID, title string) docResp {
	t.Helper()
	body, err := json.Marshal(createReq{
		Title:     title,
		ModelName: "Eligibility",
		ModelRef:  "eligibility",
		PDFBase64: base64.StdEncoding.EncodeToString(samplePDF),
		Decisions: []Decision{eligibility},
		XML:       "<definitions/>",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := call(t, svc.HandleCreate, http.MethodPost, string(body), map[string]string{"decisionId": decisionID})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body)
	}
	var out docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, rec.Body)
	}
	return out
}

// The core round trip: a published version is listed, fetched in full with its
// rules intact, and its document downloaded.
func TestPublishAndReadBack(t *testing.T) {
	svc, _ := newService(t)
	got := publish(t, svc, "eligibility", "Eligibility")

	if got.Version != 1 || got.DecisionID != "eligibility" || got.Title != "Eligibility" {
		t.Fatalf("create response = %+v", got)
	}
	if got.DecisionCount != 1 || got.ModelRef != "eligibility" {
		t.Errorf("create response = %+v, want one documented decision and its model handle", got)
	}
	if got.ShareToken != "" {
		t.Error("a new version is shared; want private until asked")
	}

	// The listing is a summary: no rules, no DMN source. A history of a much-edited
	// decision would otherwise haul every version's whole model through the common
	// read.
	rec := call(t, svc.HandleList, http.MethodGet, "", map[string]string{"decisionId": "eligibility"})
	var list []docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rec.Body)
	}
	if len(list) != 1 || list[0].ID != got.ID {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].Decisions) != 0 || list[0].XML != "" {
		t.Error("the listing carries the detail payload; want a summary")
	}

	// The single fetch is the detailed one, and the rules come back as published.
	rec = call(t, svc.HandleGet, http.MethodGet, "", map[string]string{"id": got.ID})
	var full docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode get: %v (%s)", err, rec.Body)
	}
	if len(full.Decisions) != 1 || len(full.Decisions[0].Rules) != 2 {
		t.Fatalf("detail = %+v, want the documented decision with both rules", full.Decisions)
	}
	if full.Decisions[0].Rules[0].Description != "Big enough" {
		t.Errorf("rule annotation = %q, want it kept: it is why the rule exists", full.Decisions[0].Rules[0].Description)
	}
	if full.XML == "" {
		t.Error("the detail fetch carries no DMN source; a reader cannot recover what a version describes")
	}

	// The document itself downloads, under a filename naming the decision and the
	// version.
	rec = call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("pdf = %d, body %q", rec.Code, rec.Body.String()[:min(12, rec.Body.Len())])
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "eligibility-v1.pdf") {
		t.Errorf("Content-Disposition = %q, want it to name the decision and version", cd)
	}
}

// A second publish is the next version of that decision, and a different decision
// starts its own line — the counter is per decision, not global.
func TestVersionsAreCountedPerDecision(t *testing.T) {
	svc, _ := newService(t)

	if got := publish(t, svc, "eligibility", "").Version; got != 1 {
		t.Fatalf("first version = %d, want 1", got)
	}
	if got := publish(t, svc, "eligibility", "").Version; got != 2 {
		t.Fatalf("second version = %d, want 2", got)
	}
	if got := publish(t, svc, "discount", "").Version; got != 1 {
		t.Fatalf("another decision's first version = %d, want 1 — the counter is per decision", got)
	}
}

// The counter survives a restart: a service rebuilt over the same records
// continues the sequence rather than minting a second v1 over the history.
func TestLoadVersionsRebuildsTheCounter(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	defer func() { close(quit); wg.Wait() }()

	first := New(loop, store, func(string) bool { return true }, token.New)
	publish(t, first, "eligibility", "")
	publish(t, first, "eligibility", "")

	second := New(loop, store, func(string) bool { return true }, token.New)
	if err := second.LoadVersions(); err != nil {
		t.Fatalf("LoadVersions: %v", err)
	}
	if got := publish(t, second, "eligibility", "").Version; got != 3 {
		t.Fatalf("version after a restart = %d, want 3 — the history must not be overwritten", got)
	}
}

// Sharing is per version, idempotent, and revocable — and the public route serves
// exactly the same bytes to a reader with no account.
func TestSharingIsPerVersionAndRevocable(t *testing.T) {
	svc, _ := newService(t)
	v1 := publish(t, svc, "eligibility", "")
	v2 := publish(t, svc, "eligibility", "")

	rec := call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": v1.ID})
	var shared docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &shared); err != nil {
		t.Fatalf("decode share: %v (%s)", err, rec.Body)
	}
	if shared.ShareToken == "" || !strings.HasPrefix(shared.ShareURL, PublicPath) {
		t.Fatalf("share = %+v, want a token and a public URL", shared)
	}

	// Re-sharing must not rotate a URL readers already hold.
	rec = call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": v1.ID})
	var again docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatalf("decode re-share: %v", err)
	}
	if again.ShareToken != shared.ShareToken {
		t.Errorf("re-sharing rotated the token: %q → %q", shared.ShareToken, again.ShareToken)
	}

	// The other version stays private: sharing one does not publish the history.
	rec = call(t, svc.HandleGet, http.MethodGet, "", map[string]string{"id": v2.ID})
	var other docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode other: %v", err)
	}
	if other.ShareToken != "" {
		t.Error("publishing one version shared another")
	}

	// The public route serves it without a login.
	rec = call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": shared.ShareToken})
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("public = %d, want the document", rec.Code)
	}

	// Revoking kills the URL.
	if rec = call(t, svc.HandleUnshare, http.MethodDelete, "", map[string]string{"id": v1.ID}); rec.Code != http.StatusOK {
		t.Fatalf("unshare = %d %s", rec.Code, rec.Body)
	}
	rec = call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": shared.ShareToken})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoked link = %d, want 404", rec.Code)
	}
}

// An unknown, malformed or revoked token is one indistinguishable 404: the
// response must not say whether a document ever existed behind it.
func TestAnUnknownPublicTokenIsIndistinguishable(t *testing.T) {
	svc, _ := newService(t)
	publish(t, svc, "eligibility", "")

	for _, tok := range []string{"", "not-hex", "deadbeef", strings.Repeat("a", 64)} {
		rec := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": tok})
		if rec.Code != http.StatusNotFound {
			t.Errorf("token %q = %d, want 404", tok, rec.Code)
		}
	}
}

// The public route is rate-limited, because it is the one surface a reader
// without an account can reach.
func TestThePublicRouteIsRateLimited(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	defer func() { close(quit); wg.Wait() }()

	svc := New(loop, store, func(string) bool { return false }, token.New)
	rec := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": "deadbeef"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled request = %d, want 429", rec.Code)
	}
}

// Deleting a version takes its PDF with it, so no orphaned document is left on
// disk — and the delete is idempotent, so cleanup can be repeated.
func TestDeletingAVersionTakesItsDocument(t *testing.T) {
	svc, store := newService(t)
	got := publish(t, svc, "eligibility", "")
	pdfPath := filepath.Join(store.Dir(), got.ID+".pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		t.Fatalf("the published PDF is not on disk: %v", err)
	}

	if rec := call(t, svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": got.ID}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(pdfPath); !os.IsNotExist(err) {
		t.Fatalf("the document outlived its record: %v", err)
	}
	if rec := call(t, svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": got.ID}); rec.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d, want it idempotent", rec.Code)
	}
}

// Retention keeps the newest and removes the rest, PDFs and all. Nothing is
// pruned without being asked, and asking for more than there is prunes nothing.
func TestPruningKeepsTheNewest(t *testing.T) {
	svc, store := newService(t)
	for i := 0; i < 4; i++ {
		publish(t, svc, "eligibility", "")
	}

	rec := call(t, svc.HandlePrune, http.MethodPost, `{"keep":2}`, map[string]string{"decisionId": "eligibility"})
	if rec.Code != http.StatusOK {
		t.Fatalf("prune = %d %s", rec.Code, rec.Body)
	}
	var out pruneResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode prune: %v", err)
	}
	if len(out.Deleted) != 2 {
		t.Fatalf("pruned %d versions, want the two oldest", len(out.Deleted))
	}
	left, err := store.ForDecision("eligibility")
	if err != nil {
		t.Fatalf("ForDecision: %v", err)
	}
	if len(left) != 2 || left[0].Version != 4 || left[1].Version != 3 {
		t.Fatalf("kept = %+v, want v4 and v3", left)
	}
	for _, id := range out.Deleted {
		if _, err := os.Stat(filepath.Join(store.Dir(), id+".pdf")); !os.IsNotExist(err) {
			t.Errorf("pruned version %s left its PDF behind", id)
		}
	}

	// Pruning an already-short history removes nothing.
	rec = call(t, svc.HandlePrune, http.MethodPost, `{"keep":5}`, map[string]string{"decisionId": "eligibility"})
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode second prune: %v", err)
	}
	if len(out.Deleted) != 0 {
		t.Fatalf("pruned %v from a shorter history", out.Deleted)
	}
}

// The upload is guarded: a caller's mistake is a 400 naming it, not a stored
// record pointing at bytes no reader can open.
func TestTheUploadRefusesWhatIsNotADocument(t *testing.T) {
	svc, _ := newService(t)

	for _, tc := range []struct{ name, body string }{
		{"not json", "<definitions/>"},
		{"no document at all", `{"title":"x"}`},
		{"not base64", `{"pdfBase64":"!!!!"}`},
		{"not a pdf", `{"pdfBase64":"` + base64.StdEncoding.EncodeToString([]byte("just text")) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, svc.HandleCreate, http.MethodPost, tc.body, map[string]string{"decisionId": "eligibility"})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("create = %d %s, want 400", rec.Code, rec.Body)
			}
		})
	}
	// And a refused upload burned no version number.
	if got := publish(t, svc, "eligibility", "").Version; got != 1 {
		t.Fatalf("version after refused uploads = %d, want 1", got)
	}
}

// A prune without a limit is refused rather than guessed at: a document is a real
// artifact somebody chose to publish.
func TestPruningNeedsAnExplicitLimit(t *testing.T) {
	svc, _ := newService(t)
	for _, body := range []string{`{}`, `{"keep":-1}`, `nonsense`} {
		rec := call(t, svc.HandlePrune, http.MethodPost, body, map[string]string{"decisionId": "eligibility"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("prune %s = %d, want 400", body, rec.Code)
		}
	}
}

// An id that is not one of ours is a clean miss everywhere, and never a path the
// store follows onto the filesystem.
func TestAnUnsafeIdIsACleanMiss(t *testing.T) {
	svc, _ := newService(t)
	for _, id := range []string{"../../etc/passwd", "not-hex", ""} {
		if rec := call(t, svc.HandleGet, http.MethodGet, "", map[string]string{"id": id}); rec.Code != http.StatusNotFound {
			t.Errorf("get %q = %d, want 404", id, rec.Code)
		}
		if rec := call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": id}); rec.Code != http.StatusNotFound {
			t.Errorf("pdf %q = %d, want 404", id, rec.Code)
		}
	}
}

// A token that cannot be minted is reported rather than swallowed into a version
// that looks shared and is not.
func TestAShareThatCannotMintATokenSaysSo(t *testing.T) {
	svc, _ := newService(t)
	got := publish(t, svc, "eligibility", "")
	svc.newToken = func() (string, error) { return "", errors.New("no entropy") }

	rec := call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": got.ID})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("share = %d, want the failure reported", rec.Code)
	}
}

// A decision id with a path separator or a quote in it cannot carry either into
// the Content-Disposition header.
func TestTheDownloadFilenameIsSanitized(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"eligibility", "eligibility-v1.pdf"},
		{"../../etc/passwd", "------etc-passwd-v1.pdf"},
		{`a"b`, "a-b-v1.pdf"},
		{"", "decision-v1.pdf"},
	} {
		if got := filenameFor(Doc{DecisionID: tc.id, Version: 1}); got != tc.want {
			t.Errorf("filenameFor(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
