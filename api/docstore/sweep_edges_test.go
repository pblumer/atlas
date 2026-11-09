package docstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// flakyBackend holds bytes like memBackend and fails the calls it is told to.
type flakyBackend struct {
	memBackend
	name                         string
	failPut, failOpen, failRead  bool
	failDelete, failList, listed bool
}

func (f *flakyBackend) Name() string { return f.name }
func (f *flakyBackend) Put(ctx context.Context, hash, path string, n int64, ct string) error {
	if f.failPut {
		return errBackend
	}
	return f.memBackend.Put(ctx, hash, path, n, ct)
}
func (f *flakyBackend) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	if f.failOpen {
		return nil, errBackend
	}
	if f.failRead {
		return io.NopCloser(io.MultiReader(strings.NewReader("x"), errReader{})), nil
	}
	return f.memBackend.Open(ctx, hash)
}
func (f *flakyBackend) Delete(ctx context.Context, hash string) error {
	if f.failDelete {
		return errBackend
	}
	return f.memBackend.Delete(ctx, hash)
}

// listingBackend is a flakyBackend that can also list what it holds.
type listingBackend struct{ *flakyBackend }

func (l listingBackend) Hashes() ([]string, error) {
	if l.failList {
		return nil, errBackend
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for h := range l.objects {
		out = append(out, h)
	}
	return out, nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBackend }

// failingCipher refuses to seal and cannot say whether a key exists.
type failingCipher struct{ testCipher }

func (failingCipher) SealBytes(string, string, []byte) ([]byte, error) { return nil, errBackend }
func (failingCipher) HasKey(string) (bool, error)                      { return false, errBackend }

func TestPutOfABodyThatFailsIsRefused(t *testing.T) {
	s, _ := openTest(t, Options{})
	if _, err := s.Put(context.Background(), "a.csv", "", "", io.MultiReader(strings.NewReader("x"), errReader{})); !errors.Is(err, errBackend) {
		t.Fatalf("Put of a failing body = %v", err)
	}
}

// A store opened with further backends reads documents written to them, and a seal
// reports each way the bytes can fail to be resealed.
func TestSealThroughABackendThatFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mem := &flakyBackend{name: "mem"}
	s, err := Open(dir, Options{Default: mem, Cipher: &testCipher{gone: map[string]bool{}}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Put(ctx, "a.csv", "", "", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	// Reopened with mem as a further backend, not the default, it still reads.
	other, err := Open(dir, Options{Others: []Backend{mem}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := other.Get(rec.ID); readAll(t, other, got) != csv {
		t.Fatal("a document of a further backend does not read")
	}
	// Reopened without it, the document's backend is not configured.
	without, _ := Open(dir, Options{Cipher: &testCipher{gone: map[string]bool{}}})
	if err := without.Seal(ctx, rec.ID, "K-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Seal through an unconfigured backend = %v", err)
	}
	for _, c := range []struct {
		name string
		set  func()
	}{
		{"open", func() { mem.failOpen = true }},
		{"read", func() { mem.failOpen, mem.failRead = false, true }},
		{"put", func() { mem.failRead, mem.failPut = false, true }},
	} {
		c.set()
		if err := s.Seal(ctx, rec.ID, "K-1"); !errors.Is(err, errBackend) {
			t.Errorf("Seal when %s fails = %v", c.name, err)
		}
	}
	mem.failPut = false
	refusing, _ := Open(dir, Options{Default: mem, Cipher: &failingCipher{}})
	if err := refusing.Seal(ctx, rec.ID, "K-1"); !errors.Is(err, errBackend) {
		t.Errorf("Seal when the cipher refuses = %v", err)
	}
	// The same bytes again are journalled once.
	if _, err := s.Put(ctx, "b.csv", "", "", strings.NewReader(csv)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "written.json"))
	if bytes.Count(data, []byte(rec.SHA256)) != 1 {
		t.Errorf("journal = %s", data)
	}
}

// A sweep stops, with the reason, at the first thing it cannot decide or do.
func TestSweepStopsAtWhatItCannotDo(t *testing.T) {
	ctx := context.Background()
	never := func(uint64) (bool, error) { return false, nil }

	s, dir := openTest(t, Options{Cipher: &testCipher{gone: map[string]bool{}}})
	rec, _ := s.Put(ctx, "a.csv", "", "", strings.NewReader(csv))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Sweep(cancelled, time.Hour, never); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled sweep = %v", err)
	}
	if err := s.Bind(rec.ID, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(ctx, time.Hour, func(uint64) (bool, error) { return false, errBackend }); !errors.Is(err, errBackend) {
		t.Errorf("a sweep that cannot tell an instance exists = %v", err)
	}
	if err := s.Seal(ctx, rec.ID, "K-1"); err != nil {
		t.Fatal(err)
	}
	unsure, _ := Open(dir, Options{Cipher: &failingCipher{}})
	if _, err := unsure.Sweep(ctx, time.Hour, never); !errors.Is(err, errBackend) {
		t.Errorf("a sweep that cannot tell a key exists = %v", err)
	}
	// A record that does not parse stops the listing.
	if err := os.WriteFile(filepath.Join(dir, "meta", "doc_"+strings.Repeat("b", 26)+".json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.All(); err == nil {
		t.Error("a record that does not parse was listed")
	}
	if _, err := s.Sweep(ctx, time.Hour, never); err == nil {
		t.Error("a sweep over a record that does not parse succeeded")
	}
}

// Blob collection reports a backend that cannot list or delete, and the journal of a
// non-listing backend keeps what is named or belongs to a backend not configured.
func TestBlobCollectionFailures(t *testing.T) {
	ctx := context.Background()
	never := func(uint64) (bool, error) { return false, nil }

	lb := listingBackend{&flakyBackend{name: "list"}}
	s, _ := openTest(t, Options{Default: lb})
	if _, err := s.Put(ctx, "a.csv", "", "", strings.NewReader(csv)); err != nil {
		t.Fatal(err)
	}
	lb.failList = true
	if _, err := s.Sweep(ctx, -time.Hour, never); !errors.Is(err, errBackend) {
		t.Errorf("a sweep whose backend cannot list = %v", err)
	}
	lb.failList, lb.failDelete = false, true
	if _, err := s.Sweep(ctx, -time.Hour, never); !errors.Is(err, errBackend) {
		t.Errorf("a sweep whose backend cannot delete = %v", err)
	}

	mem := &flakyBackend{name: "mem"}
	s2, dir := openTest(t, Options{Default: mem})
	kept, _ := s2.Put(ctx, "k.csv", "", "", strings.NewReader("k\n"))
	if err := s2.Bind(kept.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Put(ctx, "g.csv", "", "", strings.NewReader("g\n")); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dir, "written.json")
	data, _ := os.ReadFile(journal)
	withForeign := strings.Replace(string(data), "[", `["elsewhere/`+strings.Repeat("c", 64)+`",`, 1)
	if err := os.WriteFile(journal, []byte(withForeign), 0o644); err != nil {
		t.Fatal(err)
	}
	mem.failDelete = true
	alive := func(uint64) (bool, error) { return true, nil }
	if _, err := s2.Sweep(ctx, -time.Hour, alive); !errors.Is(err, errBackend) {
		t.Errorf("a journal delete that fails = %v", err)
	}
	mem.failDelete = false
	if _, err := s2.Sweep(ctx, -time.Hour, alive); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(journal)
	if !strings.Contains(string(data), "elsewhere/") || !strings.Contains(string(data), kept.SHA256) {
		t.Errorf("journal after the sweep = %s", data)
	}
	if err := os.WriteFile(journal, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Sweep(ctx, -time.Hour, alive); err == nil || !strings.Contains(err.Error(), "journal") {
		t.Errorf("a journal that does not parse = %v", err)
	}
}
