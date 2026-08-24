package processdoc

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/pblumer/atlas/api/token"
)

func newProcessDocs(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "process-docs"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// TestProcessDocStoreRoundTrip proves a saved version comes back with its
// metadata and its PDF bytes intact — the two are stored as separate files, so
// the round trip has to prove they stay bound to one id (ADR-0143).
func TestProcessDocStoreRoundTrip(t *testing.T) {
	s := newProcessDocs(t)
	pdf := []byte("%PDF-1.4\nfake\n%%EOF\n")
	rec := Doc{
		ID: "aabb", ProcessID: "order", ProcessName: "Order to cash",
		Version: 1, Title: "Order to cash", CreatedAt: 100, CreatedBy: "ada",
		PDFSize: int64(len(pdf)),
		Elements: []Element{
			{ID: "Task_1", Type: "bpmn:ServiceTask", Name: "Charge card",
				Documentation: "Charges the customer.", Annotations: []string{"needs PCI scope"}},
		},
	}
	if err := s.Save(rec, pdf); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, ok, err := s.Get("aabb")
	if err != nil || !ok {
		t.Fatalf("get = (_, %v, %v), want the saved record", ok, err)
	}
	if got.ProcessID != "order" || got.Version != 1 || got.CreatedBy != "ada" {
		t.Errorf("get = %+v, want the saved metadata", got)
	}
	if len(got.Elements) != 1 || got.Elements[0].Documentation != "Charges the customer." {
		t.Errorf("elements = %+v, want the element prose preserved", got.Elements)
	}
	if len(got.Elements[0].Annotations) != 1 || got.Elements[0].Annotations[0] != "needs PCI scope" {
		t.Errorf("annotations = %+v, want the attached annotation preserved", got.Elements[0].Annotations)
	}

	blob, err := s.PDF("aabb")
	if err != nil {
		t.Fatalf("pdf: %v", err)
	}
	if !bytes.Equal(blob, pdf) {
		t.Errorf("pdf = %q, want the bytes that were saved", blob)
	}
}

// TestProcessDocStoreGetMissing proves a missing id is reported as not-found
// rather than as an error, so a handler can turn it into a 404.
func TestProcessDocStoreGetMissing(t *testing.T) {
	s := newProcessDocs(t)
	if _, ok, err := s.Get("dead"); ok || err != nil {
		t.Fatalf("get(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	if _, err := s.PDF("dead"); err == nil {
		t.Fatal("pdf(missing) = nil error, want an error")
	}
}

// TestProcessDocStoreRejectsUnsafeID proves an id that is not bare hex can never
// name a file — the guard that keeps a crafted id from walking out of the store
// directory, mirroring publicLinkStore's token.IsHex check.
func TestProcessDocStoreRejectsUnsafeID(t *testing.T) {
	s := newProcessDocs(t)
	for _, id := range []string{"", "../../etc/passwd", "not-hex", "abc"} {
		if _, ok, err := s.Get(id); ok || err != nil {
			t.Errorf("get(%q) = (_, %v, %v), want (_, false, nil)", id, ok, err)
		}
		if err := s.Save(Doc{ID: id, ProcessID: "p"}, []byte("x")); err == nil {
			t.Errorf("save(%q) = nil error, want a refusal", id)
		}
		if err := s.SaveRecord(Doc{ID: id, ProcessID: "p"}); err == nil {
			t.Errorf("saveRecord(%q) = nil error, want a refusal", id)
		}
		if _, err := s.PDF(id); err == nil {
			t.Errorf("pdf(%q) = nil error, want a refusal", id)
		}
	}
}

// TestNewProcessDocStoreRefusesUnusableDir proves a store whose directory cannot
// be created reports it at construction, rather than failing later on the first
// export.
func TestNewProcessDocStoreRefusesUnusableDir(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := NewStore(filepath.Join(blocked, "docs")); err == nil {
		t.Fatal("NewStore under a file = nil error, want a failure")
	}
}

// TestProcessDocStoreForProcessNewestFirst proves one process's history reads
// newest version first, which is the order the history view wants, and that
// another process's versions never leak into it.
func TestProcessDocStoreForProcessNewestFirst(t *testing.T) {
	s := newProcessDocs(t)
	for _, rec := range []Doc{
		{ID: "01", ProcessID: "order", Version: 1, CreatedAt: 100},
		{ID: "02", ProcessID: "order", Version: 3, CreatedAt: 300},
		{ID: "03", ProcessID: "order", Version: 2, CreatedAt: 200},
		{ID: "04", ProcessID: "invoice", Version: 1, CreatedAt: 400},
	} {
		if err := s.Save(rec, []byte("%PDF")); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	got, err := s.ForProcess("order")
	if err != nil {
		t.Fatalf("forProcess: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("forProcess returned %d versions, want 3", len(got))
	}
	for i, want := range []int32{3, 2, 1} {
		if got[i].Version != want {
			t.Errorf("position %d = v%d, want v%d (not newest first)", i, got[i].Version, want)
		}
	}
}

// TestProcessDocStoreOrderIsDeterministic proves two records that tie on process
// and version still read in a fixed order, so a history listing does not shuffle
// between requests.
func TestProcessDocStoreOrderIsDeterministic(t *testing.T) {
	s := newProcessDocs(t)
	for _, id := range []string{"cc", "aa", "bb"} {
		if err := s.Save(Doc{ID: id, ProcessID: "order", Version: 1}, []byte("%PDF")); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := s.ForProcess("order")
	if err != nil {
		t.Fatalf("forProcess: %v", err)
	}
	if len(got) != 3 || got[0].ID != "aa" || got[1].ID != "bb" || got[2].ID != "cc" {
		t.Fatalf("tie-break order = %+v, want aa, bb, cc", got)
	}
}

// TestProcessDocStoreLoadAllSkipsForeignFiles proves a stray file or directory in
// the store is ignored rather than failing the load, the parity of the draft and
// form stores' stray-file tests.
func TestProcessDocStoreLoadAllSkipsForeignFiles(t *testing.T) {
	s := newProcessDocs(t)
	if err := s.Save(Doc{ID: "0a", ProcessID: "real", Version: 1}, []byte("%PDF")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Mkdir(filepath.Join(s.Dir(), "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "zz-not-hex.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write foreign json: %v", err)
	}
	got, err := s.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].ProcessID != "real" {
		t.Fatalf("loadAll = %+v, want only the real record", got)
	}
}

// TestProcessDocStoreByShareToken proves a shared version is reachable by its
// token and an unshared one is not, so the public route cannot serve a document
// nobody published (ADR-0143 sharing is off by default).
func TestProcessDocStoreByShareToken(t *testing.T) {
	s := newProcessDocs(t)
	if err := s.Save(Doc{ID: "0a", ProcessID: "order", Version: 1, ShareToken: "beef"}, []byte("%PDF")); err != nil {
		t.Fatalf("save shared: %v", err)
	}
	if err := s.Save(Doc{ID: "0b", ProcessID: "order", Version: 2}, []byte("%PDF")); err != nil {
		t.Fatalf("save private: %v", err)
	}
	got, ok, err := s.ByShareToken("beef")
	if err != nil || !ok || got.ID != "0a" {
		t.Fatalf("byShareToken(beef) = (%+v, %v, %v), want the shared version", got, ok, err)
	}
	if _, ok, err := s.ByShareToken("cafe"); ok || err != nil {
		t.Fatalf("byShareToken(unknown) = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	// An empty token must never match the many records that carry no token.
	if _, ok, err := s.ByShareToken(""); ok || err != nil {
		t.Fatalf("byShareToken(\"\") = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

// TestProcessDocStoreSaveRecordKeepsPDF proves a metadata-only update (minting or
// revoking a share token) rewrites the sidecar without disturbing the stored PDF.
func TestProcessDocStoreSaveRecordKeepsPDF(t *testing.T) {
	s := newProcessDocs(t)
	pdf := []byte("%PDF-1.4 body")
	rec := Doc{ID: "0a", ProcessID: "order", Version: 1, PDFSize: int64(len(pdf))}
	if err := s.Save(rec, pdf); err != nil {
		t.Fatalf("save: %v", err)
	}
	rec.ShareToken = "beef"
	if err := s.SaveRecord(rec); err != nil {
		t.Fatalf("saveRecord: %v", err)
	}
	got, ok, err := s.Get("0a")
	if err != nil || !ok || got.ShareToken != "beef" {
		t.Fatalf("get = (%+v, %v, %v), want the token recorded", got, ok, err)
	}
	blob, err := s.PDF("0a")
	if err != nil || !bytes.Equal(blob, pdf) {
		t.Fatalf("pdf = (%q, %v), want the original bytes untouched", blob, err)
	}
}

// TestProcessDocStoreDelete proves deleting a version removes both of its files
// and is idempotent, so cleanup never fails on an already-gone record.
func TestProcessDocStoreDelete(t *testing.T) {
	s := newProcessDocs(t)
	if err := s.Save(Doc{ID: "0a", ProcessID: "order", Version: 1}, []byte("%PDF")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Delete("0a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.Get("0a"); ok {
		t.Error("get after delete found the record, want it gone")
	}
	if _, err := os.Stat(s.pdfFileFor("0a")); !os.IsNotExist(err) {
		t.Errorf("pdf file still present after delete (stat err %v)", err)
	}
	if err := s.Delete("0a"); err != nil {
		t.Fatalf("delete again: %v, want idempotent", err)
	}
	if err := s.Delete("not-hex"); err != nil {
		t.Fatalf("delete(unsafe id): %v, want a silent no-op", err)
	}
}

// TestProcessDocStoreCodeRoundTrip proves an element's code-bearing fields survive
// the save/load round trip with their whitespace intact — a script whose
// indentation is mangled is a script a reader cannot trust (ADR-0143).
func TestProcessDocStoreCodeRoundTrip(t *testing.T) {
	s := newProcessDocs(t)
	script := "if ($budget -gt 1000) {\n    Approve-Request\n}"
	rec := Doc{
		ID: "c0de", ProcessID: "order", Version: 1,
		Elements: []Element{{
			ID: "Task_run", Type: "bpmn:ScriptTask", Name: "Decide",
			Code: []Code{
				{Label: "Script", Language: "powershell", Source: script},
				{Label: "Condition", Language: "feel", Source: "= budget > 1000"},
			},
		}},
	}
	if err := s.Save(rec, []byte("%PDF")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := s.Get("c0de")
	if err != nil || !ok {
		t.Fatalf("get = (_, %v, %v), want the saved record", ok, err)
	}
	code := got.Elements[0].Code
	if len(code) != 2 {
		t.Fatalf("code fields = %d, want 2", len(code))
	}
	if code[0].Language != "powershell" || code[0].Source != script {
		t.Errorf("script field = %+v, want the PowerShell source verbatim", code[0])
	}
	if code[1].Label != "Condition" || code[1].Source != "= budget > 1000" {
		t.Errorf("condition field = %+v, want the FEEL expression preserved", code[1])
	}
}

// TestProcessDocStorePrune proves retention keeps the newest `keep` versions and
// removes the older ones, PDF and record together — ADR-0143's bounded-growth
// follow-up. A prune that keeps at least everything removes nothing, and the
// returned ids are exactly the versions that went.
func TestProcessDocStorePrune(t *testing.T) {
	s := newProcessDocs(t)
	// Five versions of one process, plus one of another that pruning must not touch.
	for _, rec := range []Doc{
		{ID: "a1", ProcessID: "order", Version: 1, CreatedAt: 100},
		{ID: "a2", ProcessID: "order", Version: 2, CreatedAt: 200},
		{ID: "a3", ProcessID: "order", Version: 3, CreatedAt: 300},
		{ID: "a4", ProcessID: "order", Version: 4, CreatedAt: 400},
		{ID: "a5", ProcessID: "order", Version: 5, CreatedAt: 500},
		{ID: "b1", ProcessID: "invoice", Version: 1, CreatedAt: 600},
	} {
		if err := s.Save(rec, []byte("%PDF")); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}

	pruned, err := s.PruneProcess("order", 2)
	if err != nil {
		t.Fatalf("pruneProcess: %v", err)
	}
	// The three oldest go; the ids are reported so the caller can tell the reader.
	sort.Strings(pruned)
	if len(pruned) != 3 || pruned[0] != "a1" || pruned[1] != "a2" || pruned[2] != "a3" {
		t.Fatalf("pruned = %v, want the three oldest [a1 a2 a3]", pruned)
	}
	// The two newest of the process, and the untouched other process, remain.
	left, err := s.ForProcess("order")
	if err != nil {
		t.Fatalf("forProcess: %v", err)
	}
	if len(left) != 2 || left[0].Version != 5 || left[1].Version != 4 {
		t.Fatalf("kept = %+v, want v5 and v4", left)
	}
	if _, err := os.Stat(s.pdfFileFor("a1")); !os.IsNotExist(err) {
		t.Errorf("pruned version's PDF still present (stat err %v)", err)
	}
	if other, _ := s.ForProcess("invoice"); len(other) != 1 {
		t.Errorf("other process history = %d, want the prune to leave it alone", len(other))
	}

	// Pruning to a limit no smaller than the history removes nothing, and a
	// negative limit is clamped rather than deleting what it was asked to keep.
	if got, _ := s.PruneProcess("order", 5); got != nil {
		t.Errorf("prune keep>=len = %v, want nothing pruned", got)
	}
	if got, err := s.PruneProcess("order", -1); err != nil || len(got) != 2 {
		t.Errorf("prune keep=-1 = (%v, %v), want both remaining pruned after clamp to 0", got, err)
	}
}

// TestProcessDocStoreSaveRefusesUnwritableDir proves a store whose directory has
// been removed reports the failure rather than reporting a save that did not
// happen — durable before visible (I2).
func TestProcessDocStoreSaveRefusesUnwritableDir(t *testing.T) {
	s := newProcessDocs(t)
	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := s.Save(Doc{ID: "0a", ProcessID: "order", Version: 1}, []byte("%PDF")); err == nil {
		t.Fatal("save into a removed directory = nil error, want a failure")
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("loadAll on a removed directory = nil error, want a failure")
	}
}

// TestProcessDocStoreLoadAllRejectsCorruptRecord proves a sidecar that is not a
// valid record surfaces as an error rather than being silently dropped — a
// truncated write is a fault to report, not history to lose.
func TestProcessDocStoreLoadAllRejectsCorruptRecord(t *testing.T) {
	s := newProcessDocs(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), "0a.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("loadAll over a corrupt record = nil error, want a failure")
	}
	if _, ok, err := s.Get("0a"); ok || err == nil {
		t.Fatalf("get over a corrupt record = (_, %v, %v), want an error", ok, err)
	}
}

// TestNewProcessDocID proves minted ids are bare hex (so they are their own
// filenames) and unique across mints.
func TestNewProcessDocID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !token.IsHex(id) {
			t.Fatalf("NewID = %q, want bare hex", id)
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
	}
}
