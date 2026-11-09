package docstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failingBackend is a backend whose every call fails, for the error paths.
type failingBackend struct{ name string }

var errBackend = errors.New("backend down")

func (f failingBackend) Name() string { return f.name }
func (failingBackend) Put(context.Context, string, string, int64, string) error {
	return errBackend
}
func (failingBackend) Open(context.Context, string) (io.ReadCloser, error) { return nil, errBackend }
func (failingBackend) Delete(context.Context, string) error                { return errBackend }

func TestRecordsAndNames(t *testing.T) {
	s, dir := openTest(t, Options{})
	if s.MaxBytes() != 35<<20 || s.DefaultBackend() != "local" {
		t.Fatalf("defaults = %d %s", s.MaxBytes(), s.DefaultBackend())
	}
	for _, id := range []string{"", "doc_", "doc_UPPERCASEUPPERCASEUPPERC", "doc_" + strings.Repeat("a", 25), "x_" + strings.Repeat("a", 26), "doc_" + strings.Repeat("1", 26)} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
		if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v", id, err)
		}
	}
	// A corrupt record is an error, not an empty one; a stray file is not a record.
	bad := "doc_" + strings.Repeat("b", 26)
	_ = os.WriteFile(filepath.Join(dir, "meta", bad+".json"), []byte("{"), 0o644)
	if _, err := s.Get(bad); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt record = %v", err)
	}
	if _, err := s.All(); err == nil {
		t.Fatal("All read past a corrupt record")
	}
	_ = os.Remove(filepath.Join(dir, "meta", bad+".json"))
	_ = os.WriteFile(filepath.Join(dir, "meta", "notes.txt"), []byte("x"), 0o644)
	if recs, err := s.All(); err != nil || len(recs) != 0 {
		t.Fatalf("All = %v, %v", recs, err)
	}

	for in, want := range map[string]string{
		"":                       "document",
		"..":                     "document",
		"a/b/c.csv":              "c.csv",
		`he said "hi".txt`:       "he said hi.txt",
		"tab\there.csv":          "tabhere.csv",
		strings.Repeat("x", 300): strings.Repeat("x", 255),
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	for name, want := range map[string]string{
		"a.json": "application/json", "b.md": "text/markdown; charset=utf-8", "c.txt": "text/plain; charset=utf-8",
	} {
		if got := DetectType([]byte("plain words"), name); got != want {
			t.Errorf("DetectType(%s) = %q", name, got)
		}
	}
	if got := DetectType([]byte("%PDF-1.4"), "x.csv"); got != "application/pdf" {
		t.Errorf("a PDF named .csv = %q; the bytes decide", got)
	}
	if RefID("doc_x") != "" || RefID(map[string]any{RefKey: 7}) != "" {
		t.Error("RefID accepted a non-reference")
	}
	if ids := FindRefsInJSON(""); ids != nil {
		t.Error("FindRefsInJSON of nothing")
	}
	deep := map[string]any{}
	cur := deep
	for i := 0; i < maxRefDepth+3; i++ {
		next := map[string]any{}
		cur["n"] = next
		cur = next
	}
	cur[RefKey] = "doc_" + strings.Repeat("c", 26)
	if ids := FindRefs(map[string]any{"v": deep}); len(ids) != 0 {
		t.Errorf("a reference past the depth bound was found: %v", ids)
	}
}

func TestBackendFailures(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t, Options{Default: failingBackend{name: "broken"}})
	if _, err := s.Put(ctx, "a.csv", "", "", strings.NewReader("x")); !errors.Is(err, errBackend) {
		t.Fatalf("Put on a failing backend = %v", err)
	}
	if _, err := s.OpenBytes(ctx, Record{Backend: "nowhere"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenBytes on an unconfigured backend = %v", err)
	}
	if _, err := s.OpenBytes(ctx, Record{Backend: "broken", SHA256: strings.Repeat("a", 64)}); !errors.Is(err, errBackend) {
		t.Fatalf("OpenBytes on a failing backend = %v", err)
	}
	if ok, err := s.Available(ctx, Record{Backend: "nowhere"}); ok || err != nil {
		t.Fatalf("Available on an unconfigured backend = %v, %v", ok, err)
	}
	if ok, err := s.Available(ctx, Record{Backend: "broken"}); !ok || err != nil {
		t.Fatalf("Available on a backend that cannot tell = %v, %v; it is assumed", ok, err)
	}
	if err := s.Seal(ctx, "doc_"+strings.Repeat("a", 26), "K-1"); err == nil {
		t.Fatal("Seal without a cipher succeeded")
	}
	if res, err := s.Sweep(ctx, time.Hour, func(uint64) (bool, error) { return false, errBackend }); err != nil || res != (SweepResult{}) {
		t.Fatalf("a sweep of an empty store = %+v, %v", res, err)
	}
}

func TestSealFailures(t *testing.T) {
	ctx := context.Background()
	c := &testCipher{gone: map[string]bool{}}
	s, dir := openTest(t, Options{Cipher: c})
	if err := s.Seal(ctx, "doc_"+strings.Repeat("a", 26), ""); err == nil {
		t.Fatal("Seal with no subject succeeded")
	}
	if err := s.Seal(ctx, "doc_"+strings.Repeat("a", 26), "K-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Seal of an unknown document = %v", err)
	}
	rec, _ := s.Put(ctx, "a.csv", "", "", strings.NewReader("x,y\n"))
	_ = os.Remove(filepath.Join(dir, "blobs", rec.SHA256))
	if err := s.Seal(ctx, rec.ID, "K-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Seal of a document whose bytes are gone = %v", err)
	}
	// A sealed document read by a store without the key is unavailable, not plaintext.
	rec2, _ := s.Put(ctx, "b.csv", "", "", strings.NewReader("p,q\n"))
	if err := s.Seal(ctx, rec2.ID, "K-1"); err != nil {
		t.Fatal(err)
	}
	sealed, _ := s.Get(rec2.ID)
	keyless, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyless.OpenBytes(ctx, sealed); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a keyless read of a sealed document = %v", err)
	}
}

func TestLocalBackendRefusesWhatIsNotAHash(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.Put(ctx, "../evil", "/dev/null", 0, ""); err == nil {
		t.Error("Put accepted a path as a hash")
	}
	if _, err := l.Open(ctx, "../evil"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open of a non-hash = %v", err)
	}
	if ok, err := l.Has(ctx, "../evil"); ok || err != nil {
		t.Errorf("Has of a non-hash = %v, %v", ok, err)
	}
	if err := l.Delete(ctx, "../evil"); err != nil {
		t.Errorf("Delete of a non-hash = %v", err)
	}
	if err := l.Delete(ctx, strings.Repeat("a", 64)); err != nil {
		t.Errorf("Delete of an absent hash = %v", err)
	}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocal(filepath.Join(file, "x")); err == nil {
		t.Error("NewLocal under a file succeeded")
	}
}

func TestBucketBackendFailures(t *testing.T) {
	f := &fakeObjects{}
	b, _ := NewBucket(f, "docs", "atlas", "", errFakeMissing)
	ctx := context.Background()
	if err := b.Put(ctx, "h", "/no/such/file", 0, ""); err == nil {
		t.Error("Put of a missing temp file succeeded")
	}
	if _, err := b.Open(ctx, "h"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open of a missing object = %v", err)
	}
	if err := b.Delete(ctx, "h"); err != nil {
		t.Errorf("Delete = %v", err)
	}
	other, _ := NewBucket(failingObjects{}, "docs", "atlas", "p/", errFakeMissing)
	if _, err := other.Open(ctx, "h"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open on a failing store = %v", err)
	}
}

type failingObjects struct{}

func (failingObjects) PutDocument(context.Context, string, string, string, string, string, []byte, map[string]string) (map[string]any, error) {
	return nil, errBackend
}
func (failingObjects) GetDocument(context.Context, string, string, string) ([]byte, error) {
	return nil, errBackend
}
func (failingObjects) DeleteDocument(context.Context, string, string, string) error {
	return errBackend
}
