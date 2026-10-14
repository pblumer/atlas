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
	"github.com/pblumer/atlas/limits"
)

// The store's guards and the paths a failing filesystem takes. The service tests
// next door drive the happy path; these are the ones a reader has to be able to
// trust without reproducing them by hand.

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// A record and its PDF are two files that go together: saved together, read back
// together, removed together.
func TestStoreRoundTrip(t *testing.T) {
	s := newStore(t)
	rec := Doc{ID: "abcdef0123456789", DecisionID: "eligibility", Version: 1, Decisions: []Decision{eligibility}}
	if err := s.Save(rec, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Get(rec.ID)
	if err != nil || !ok {
		t.Fatalf("Get = %v %v", ok, err)
	}
	if len(got.Decisions) != 1 || len(got.Decisions[0].Rules) != 2 {
		t.Fatalf("read back = %+v, want the rules intact", got.Decisions)
	}
	pdf, err := s.PDF(rec.ID)
	if err != nil || string(pdf) != string(samplePDF) {
		t.Fatalf("PDF = %q %v", pdf, err)
	}
}

// An id that is not one of ours never reaches the filesystem, in either
// direction: it is a clean miss on read and a refusal on write.
func TestStoreRefusesAnUnsafeID(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"../escape", "not hex", ""} {
		if err := s.Save(Doc{ID: id}, samplePDF); err == nil {
			t.Errorf("Save(%q) was accepted", id)
		}
		if err := s.SaveRecord(Doc{ID: id}); err == nil {
			t.Errorf("SaveRecord(%q) was accepted", id)
		}
		if _, ok, err := s.Get(id); ok || err != nil {
			t.Errorf("Get(%q) = %v %v, want a clean miss", id, ok, err)
		}
		if _, err := s.PDF(id); err == nil {
			t.Errorf("PDF(%q) was served", id)
		}
		if err := s.Delete(id); err != nil {
			t.Errorf("Delete(%q) = %v, want a no-op", id, err)
		}
		if _, ok, err := s.ByShareToken(id); ok || err != nil {
			t.Errorf("ByShareToken(%q) = %v %v, want a clean miss", id, ok, err)
		}
	}
}

// A directory the store cannot use is a startup error, not a store that silently
// answers empty.
func TestNewStoreRefusesAnUnusableDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "occupied")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewStore(file); err == nil {
		t.Fatal("NewStore over a file was accepted")
	}
}

// A history is newest version first, and deterministic among equal versions, so
// the panel does not reshuffle between reads.
func TestForDecisionIsNewestFirst(t *testing.T) {
	s := newStore(t)
	for _, rec := range []Doc{
		{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1},
		{ID: strings.Repeat("b", 16), DecisionID: "eligibility", Version: 3},
		{ID: strings.Repeat("c", 16), DecisionID: "eligibility", Version: 2},
		{ID: strings.Repeat("d", 16), DecisionID: "discount", Version: 9},
	} {
		if err := s.Save(rec, samplePDF); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := s.ForDecision("eligibility")
	if err != nil {
		t.Fatalf("ForDecision: %v", err)
	}
	if len(got) != 3 || got[0].Version != 3 || got[1].Version != 2 || got[2].Version != 1 {
		t.Fatalf("history = %+v, want v3, v2, v1 and no other decision's versions", got)
	}
}

// A share token finds its version, and only a version that carries one — the many
// unshared records must never match.
func TestByShareTokenOnlyMatchesAShared(t *testing.T) {
	s := newStore(t)
	shared := Doc{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1, ShareToken: strings.Repeat("f", 32)}
	if err := s.Save(shared, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(Doc{ID: strings.Repeat("b", 16), DecisionID: "eligibility", Version: 2}, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.ByShareToken(shared.ShareToken)
	if err != nil || !ok || got.ID != shared.ID {
		t.Fatalf("ByShareToken = %+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.ByShareToken(strings.Repeat("e", 32)); ok {
		t.Error("an unknown token matched a version")
	}
}

// Minting or revoking a token rewrites the record and leaves the document alone:
// what a version says is immutable, who may read it is not.
func TestSaveRecordKeepsTheDocument(t *testing.T) {
	s := newStore(t)
	rec := Doc{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1}
	if err := s.Save(rec, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec.ShareToken = strings.Repeat("f", 32)
	if err := s.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	pdf, err := s.PDF(rec.ID)
	if err != nil || string(pdf) != string(samplePDF) {
		t.Fatalf("the document changed under a metadata rewrite: %q %v", pdf, err)
	}
}

// A store whose directory has gone reports the failure rather than pretending the
// write happened.
func TestSaveReportsAnUnusableDirectory(t *testing.T) {
	s := newStore(t)
	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := s.Save(Doc{ID: strings.Repeat("a", 16)}, samplePDF); err == nil {
		t.Fatal("a save into a directory that is gone reported success")
	}
}

// A record that cannot be decoded is an error, not a silently shorter history.
func TestLoadAllRejectsACorruptRecord(t *testing.T) {
	s := newStore(t)
	rec := Doc{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1}
	if err := s.Save(rec, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(s.FileFor(rec.ID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("a corrupt record was read as an empty history")
	}
}

// PruneDecision clamps a negative limit rather than deleting the history it was
// asked to keep.
func TestPruneClampsANegativeLimit(t *testing.T) {
	s := newStore(t)
	for i, id := range []string{strings.Repeat("a", 16), strings.Repeat("b", 16)} {
		if err := s.Save(Doc{ID: id, DecisionID: "eligibility", Version: int32(i + 1)}, samplePDF); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	pruned, err := s.PruneDecision("eligibility", -5)
	if err != nil {
		t.Fatalf("PruneDecision: %v", err)
	}
	if len(pruned) != 2 {
		t.Fatalf("pruned %d, want everything — a negative keep is zero, not a licence to keep", len(pruned))
	}
}

// Every id the store mints is distinct and filename-safe, because an id is its
// own store key.
func TestNewIDIsSafeAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !token.IsHex(id) {
			t.Fatalf("NewID = %q, which is not bare hex", id)
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
	}
}

// A Service built as a struct literal still has ceilings: a zero Limits is every
// ceiling at zero, and a ceiling of zero admits nothing.
func TestAServiceLiteralStillHasBudgets(t *testing.T) {
	var svc Service
	if svc.budgets() != limits.Default() {
		t.Fatal("a zero-valued service has no budgets; a request would be refused as too large")
	}
}

// A store the service cannot read is a 500 on every route that reads it, not an
// empty answer that reads like "nothing published yet".
func TestRoutesReportAStoreTheyCannotRead(t *testing.T) {
	store := newStore(t)
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	defer func() { close(quit); wg.Wait() }()
	svc := New(loop, store, func(string) bool { return true }, token.New)

	// A record that cannot be decoded is what a broken store looks like from here.
	rec := Doc{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1}
	if err := store.Save(rec, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(store.FileFor(rec.ID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		vals map[string]string
		body string
	}{
		{"list", svc.HandleList, map[string]string{"decisionId": "eligibility"}, ""},
		{"get", svc.HandleGet, map[string]string{"id": rec.ID}, ""},
		{"prune", svc.HandlePrune, map[string]string{"decisionId": "eligibility"}, `{"keep":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r *http.Request
			if tc.body != "" {
				r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			} else {
				r = httptest.NewRequest(http.MethodGet, "/", nil)
			}
			for k, v := range tc.vals {
				r.SetPathValue(k, v)
			}
			w := httptest.NewRecorder()
			tc.h(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("%s = %d, want the read failure reported", tc.name, w.Code)
			}
		})
	}
}

// A version whose PDF has gone is a 404 on the download rather than a zero-byte
// document served as if it were real.
func TestAMissingDocumentIsNotServedEmpty(t *testing.T) {
	svc, store := newService(t)
	got := publish(t, svc, "eligibility", "")
	if err := os.Remove(filepath.Join(store.Dir(), got.ID+".pdf")); err != nil {
		t.Fatalf("remove pdf: %v", err)
	}
	rec := call(t, svc.HandleGetPDF, http.MethodGet, "", map[string]string{"id": got.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("download of a missing document = %d, want 404", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("the 404 carries no reason")
	}
}

// errReader stands in for a request body that fails mid-read — a dropped
// connection, a client that gave up — which every body-reading route has to
// report rather than treat as an empty request.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestABodyThatCannotBeReadIsRefused(t *testing.T) {
	svc, _ := newService(t)
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		vals map[string]string
	}{
		{"create", svc.HandleCreate, map[string]string{"decisionId": "eligibility"}},
		{"prune", svc.HandlePrune, map[string]string{"decisionId": "eligibility"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", errReader{})
			for k, v := range tc.vals {
				r.SetPathValue(k, v)
			}
			w := httptest.NewRecorder()
			tc.h(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d, want 400", tc.name, w.Code)
			}
		})
	}
}

// Every route that addresses a version by id says so when there is none, rather
// than answering as if the version existed and was empty.
func TestAnUnknownVersionIs404Everywhere(t *testing.T) {
	svc, _ := newService(t)
	const missing = "0123456789abcdef"
	for _, tc := range []struct {
		name   string
		h      http.HandlerFunc
		method string
	}{
		{"get", svc.HandleGet, http.MethodGet},
		{"pdf", svc.HandleGetPDF, http.MethodGet},
		{"share", svc.HandleShare, http.MethodPost},
		{"unshare", svc.HandleUnshare, http.MethodDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, tc.h, tc.method, "", map[string]string{"id": missing})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s = %d %s, want 404", tc.name, rec.Code, rec.Body)
			}
		})
	}
}

// Unsharing something already private, and sharing something already shared, both
// answer with the version as it stands — the operations are idempotent, so a
// double click is not an error.
func TestShareAndUnshareAreIdempotent(t *testing.T) {
	svc, _ := newService(t)
	got := publish(t, svc, "eligibility", "")

	if rec := call(t, svc.HandleUnshare, http.MethodDelete, "", map[string]string{"id": got.ID}); rec.Code != http.StatusOK {
		t.Fatalf("unshare of a private version = %d %s, want it to be a no-op", rec.Code, rec.Body)
	}
	if rec := call(t, svc.HandleShare, http.MethodPost, "", map[string]string{"id": got.ID}); rec.Code != http.StatusOK {
		t.Fatalf("share = %d %s", rec.Code, rec.Body)
	}
	rec := call(t, svc.HandleUnshare, http.MethodDelete, "", map[string]string{"id": got.ID})
	var out docResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ShareToken != "" || out.ShareURL != "" {
		t.Fatalf("revoked version = %+v, want no link", out)
	}
}

// LoadVersions over a store it cannot read is a startup error, not a counter
// silently reset to zero — which would let the next publish overwrite history.
func TestLoadVersionsReportsABrokenStore(t *testing.T) {
	store := newStore(t)
	rec := Doc{ID: strings.Repeat("a", 16), DecisionID: "eligibility", Version: 1}
	if err := store.Save(rec, samplePDF); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(store.FileFor(rec.ID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	defer func() { close(quit); wg.Wait() }()

	svc := New(loop, store, func(string) bool { return true }, token.New)
	if err := svc.LoadVersions(); err == nil {
		t.Fatal("LoadVersions over an unreadable store reported success")
	}
}

// A publish that cannot be written is reported, and burns no version number: the
// counter advances only once the record is durable.
func TestAFailedSaveBurnsNoVersion(t *testing.T) {
	svc, store := newService(t)
	publish(t, svc, "eligibility", "")
	if err := os.RemoveAll(store.Dir()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	body, err := json.Marshal(createReq{PDFBase64: base64.StdEncoding.EncodeToString(samplePDF)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := call(t, svc.HandleCreate, http.MethodPost, string(body), map[string]string{"decisionId": "eligibility"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create over a broken store = %d, want the failure reported", rec.Code)
	}
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := publish(t, svc, "eligibility", "").Version; got != 2 {
		t.Fatalf("next version = %d, want 2 — a failed save must not spend a number", got)
	}
}

// Deleting a version the store cannot remove is reported rather than answered as
// a successful cleanup.
func TestADeleteThatCannotHappenIsReported(t *testing.T) {
	svc, store := newService(t)
	got := publish(t, svc, "eligibility", "")
	// A directory where the PDF should be is something os.Remove refuses.
	pdf := filepath.Join(store.Dir(), got.ID+".pdf")
	if err := os.Remove(pdf); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pdf, "occupied"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := call(t, svc.HandleDelete, http.MethodDelete, "", map[string]string{"id": got.ID})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete = %d, want the failure reported", rec.Code)
	}
}
