package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func openTest(t *testing.T, o Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func readAll(t *testing.T, s *Store, rec Record) string {
	t.Helper()
	rc, err := s.OpenBytes(context.Background(), rec)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

const csv = "personalnummer;vorname\nP-1001;Anna\nP-1002;Luca\n"

func TestPutGetRoundTrip(t *testing.T) {
	s, _ := openTest(t, Options{})
	rec, err := s.Put(context.Background(), `C:\Users\anna\mitarbeitende.csv`, "application/vnd.ms-excel", "usr_anna", strings.NewReader(csv))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !ValidID(rec.ID) || rec.Name != "mitarbeitende.csv" || rec.Size != int64(len(csv)) {
		t.Fatalf("record = %+v", rec)
	}
	// The bytes say text; the extension says which text. The uploader's claim is kept
	// beside it, not believed.
	if rec.ContentType != "text/csv; charset=utf-8" || rec.DeclaredType != "application/vnd.ms-excel" {
		t.Fatalf("types = %q declared %q", rec.ContentType, rec.DeclaredType)
	}
	if !rec.Pending() || rec.Uploader != "usr_anna" || rec.Backend != "local" {
		t.Fatalf("state = %+v", rec)
	}
	got, err := s.Get(rec.ID)
	if err != nil || got.SHA256 != rec.SHA256 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if body := readAll(t, s, got); body != csv {
		t.Fatalf("bytes = %q", body)
	}
	if ref := rec.Ref(); RefID(ref) != rec.ID || ref["size"] != json.Number(strconv.Itoa(len(csv))) {
		t.Fatalf("ref = %v", ref)
	}
}

func TestPutLimits(t *testing.T) {
	s, dir := openTest(t, Options{MaxBytes: 10})
	if _, err := s.Put(context.Background(), "a.txt", "", "", strings.NewReader("0123456789")); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := s.Put(context.Background(), "a.txt", "", "", strings.NewReader("0123456789x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one byte over: %v, want ErrTooLarge", err)
	}
	if _, err := s.Put(context.Background(), "a.txt", "", "", strings.NewReader("")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v, want ErrEmpty", err)
	}
	// A refused upload leaves nothing behind.
	if left, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %d", len(left))
	}
	recs, _ := s.All()
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
}

func TestSameBytesTwoDocumentsOneBlob(t *testing.T) {
	s, dir := openTest(t, Options{})
	a, _ := s.Put(context.Background(), "a.csv", "", "usr_a", strings.NewReader(csv))
	b, _ := s.Put(context.Background(), "b.csv", "", "usr_b", strings.NewReader(csv))
	if a.ID == b.ID || a.SHA256 != b.SHA256 {
		t.Fatalf("ids %s %s, hashes %s %s", a.ID, b.ID, a.SHA256, b.SHA256)
	}
	blobs, _ := os.ReadDir(filepath.Join(dir, "blobs"))
	if len(blobs) != 1 {
		t.Fatalf("blobs = %d, want 1", len(blobs))
	}
}

func TestOpenRemovesInterruptedUploads(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "tmp", "upload-123")
	if err := os.WriteFile(stray, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted upload survived a reopen")
	}
}

func TestBindIsIdempotentAndOrdered(t *testing.T) {
	s, _ := openTest(t, Options{})
	rec, _ := s.Put(context.Background(), "a.csv", "", "", strings.NewReader(csv))
	for _, k := range []uint64{30, 10, 30, 20} {
		if err := s.Bind(rec.ID, k); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Get(rec.ID)
	if len(got.BoundTo) != 3 || got.BoundTo[0] != 10 || got.BoundTo[2] != 30 || got.Pending() {
		t.Fatalf("boundTo = %v", got.BoundTo)
	}
	if err := s.Bind("doc_nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind unknown: %v", err)
	}
}

func TestSweep(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s, dir := openTest(t, Options{Now: c.now})
	ctx := context.Background()
	old, _ := s.Put(ctx, "old.csv", "", "", strings.NewReader("old\n"))
	shared1, _ := s.Put(ctx, "s1.csv", "", "", strings.NewReader(csv))
	shared2, _ := s.Put(ctx, "s2.csv", "", "", strings.NewReader(csv))
	_ = s.Bind(shared1.ID, 1)
	_ = s.Bind(shared2.ID, 2)
	c.t = c.t.Add(2 * time.Hour)
	fresh, _ := s.Put(ctx, "fresh.csv", "", "", strings.NewReader("fresh\n"))

	alive := map[uint64]bool{1: false, 2: true}
	exists := func(k uint64) (bool, error) { return alive[k], nil }
	res, err := s.Sweep(ctx, time.Hour, exists)
	if err != nil {
		t.Fatal(err)
	}
	// The pending one past its TTL goes, the fresh one stays; the document whose only
	// instance is gone goes, but its bytes stay because another record names them.
	if res.Expired != 1 || res.Released != 1 || res.Blobs != 1 {
		t.Fatalf("sweep = %+v, want 1 expired, 1 released, 1 blob (old's)", res)
	}
	for id, want := range map[string]bool{old.ID: false, shared1.ID: false, shared2.ID: true, fresh.ID: true} {
		if _, err := s.Get(id); (err == nil) != want {
			t.Fatalf("record %s present=%v, want %v", id, err == nil, want)
		}
	}
	if body := readAll(t, s, shared2); body != csv {
		t.Fatalf("shared bytes lost: %q", body)
	}
	// Again: nothing more to do.
	if res, err := s.Sweep(ctx, time.Hour, exists); err != nil || res != (SweepResult{}) {
		t.Fatalf("second sweep = %+v, %v", res, err)
	}
	// The last instance goes: record and bytes go.
	alive[2] = false
	if res, _ := s.Sweep(ctx, 24*time.Hour, exists); res.Released != 1 || res.Blobs != 1 {
		t.Fatalf("final sweep = %+v", res)
	}
	if blobs, _ := os.ReadDir(filepath.Join(dir, "blobs")); len(blobs) != 1 { // fresh's
		t.Fatalf("blobs left = %d, want 1", len(blobs))
	}
}

func TestOpenBytesOfMissingBlobIsUnavailable(t *testing.T) {
	s, dir := openTest(t, Options{})
	rec, _ := s.Put(context.Background(), "a.csv", "", "", strings.NewReader(csv))
	if ok, err := s.Available(context.Background(), rec); !ok || err != nil {
		t.Fatalf("Available before removal = %v, %v", ok, err)
	}
	_ = os.Remove(filepath.Join(dir, "blobs", rec.SHA256))
	if _, err := s.OpenBytes(context.Background(), rec); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing bytes: %v, want ErrUnavailable", err)
	}
	if ok, _ := s.Available(context.Background(), rec); ok {
		t.Fatal("Available after removal = true")
	}
}

// memBackend is a backend that cannot list what it holds, like an S3 bucket the store
// does not enumerate.
type memBackend struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted []string
}

func (m *memBackend) Name() string { return "mem" }
func (m *memBackend) Put(_ context.Context, hash, path string, _ int64, _ string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects == nil {
		m.objects = map[string][]byte{}
	}
	m.objects[hash] = b
	return nil
}
func (m *memBackend) Open(_ context.Context, hash string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[hash]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memBackend) Delete(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, hash)
	m.deleted = append(m.deleted, hash)
	return nil
}

func TestNonListingBackendIsCleanedFromItsJournal(t *testing.T) {
	mem := &memBackend{}
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s, dir := openTest(t, Options{Default: mem, Now: c.now})
	ctx := context.Background()
	rec, err := s.Put(ctx, "a.csv", "", "", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Backend != "mem" || readAll(t, s, rec) != csv {
		t.Fatalf("record %+v", rec)
	}
	// The local backend holds nothing: the bytes went to the default.
	if blobs, _ := os.ReadDir(filepath.Join(dir, "blobs")); len(blobs) != 0 {
		t.Fatalf("local blobs = %d", len(blobs))
	}
	c.t = c.t.Add(48 * time.Hour)
	if _, err := s.Sweep(ctx, 24*time.Hour, func(uint64) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if len(mem.deleted) != 1 || mem.deleted[0] != rec.SHA256 {
		t.Fatalf("deleted = %v, want the document's hash", mem.deleted)
	}
}

func TestADocumentWrittenBeforeABackendSwitchStaysReadable(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, Options{})
	rec, _ := s.Put(context.Background(), "a.csv", "", "", strings.NewReader(csv))
	s2, err := Open(dir, Options{Default: &memBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s2.Get(rec.ID)
	if got.Backend != "local" || readAll(t, s2, got) != csv {
		t.Fatalf("after switch: %+v", got)
	}
}

func TestFindRefs(t *testing.T) {
	id1 := "doc_" + strings.Repeat("a", 26)
	id2 := "doc_" + strings.Repeat("b", 26)
	vars := map[string]any{
		"csv":   map[string]any{RefKey: id1, "name": "a.csv"},
		"files": []any{map[string]any{RefKey: id2}, map[string]any{RefKey: id1}},
		"fake":  map[string]any{RefKey: "doc_../../etc/passwd"},
		"text":  "doc_" + strings.Repeat("c", 26),
	}
	got := FindRefs(vars)
	if len(got) != 2 {
		t.Fatalf("FindRefs = %v, want two ids once each", got)
	}
	if ids := FindRefsInJSON(`{"a":{"$document":"` + id2 + `"}}`); len(ids) != 1 || ids[0] != id2 {
		t.Fatalf("FindRefsInJSON = %v", ids)
	}
	if ids := FindRefsInJSON(`not json`); ids != nil {
		t.Fatalf("FindRefsInJSON(text) = %v", ids)
	}
}

func TestRendersAsPage(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html; charset=utf-8": true, "image/svg+xml": true, "application/xml": true,
		"text/csv; charset=utf-8": false, "application/pdf": false, "image/png": false,
	} {
		if RendersAsPage(ct) != want {
			t.Errorf("RendersAsPage(%q) = %v", ct, !want)
		}
	}
}

// fakeObjects stands in for an S3 Worker.
type fakeObjects struct {
	objects map[string][]byte
	types   map[string]string
}

var errFakeMissing = errors.New("missing")

func (f *fakeObjects) PutDocument(_ context.Context, worker, bucket, key, contentType, _ string, data []byte, _ map[string]string) (map[string]any, error) {
	if f.objects == nil {
		f.objects, f.types = map[string][]byte{}, map[string]string{}
	}
	f.objects[worker+"|"+bucket+"|"+key] = data
	f.types[key] = contentType
	return nil, nil
}
func (f *fakeObjects) GetDocument(_ context.Context, worker, bucket, key string) ([]byte, error) {
	b, ok := f.objects[worker+"|"+bucket+"|"+key]
	if !ok {
		return nil, errFakeMissing
	}
	return b, nil
}
func (f *fakeObjects) DeleteDocument(_ context.Context, worker, bucket, key string) error {
	delete(f.objects, worker+"|"+bucket+"|"+key)
	return nil
}

func TestBucketBackend(t *testing.T) {
	if _, err := NewBucket(&fakeObjects{}, "", "b", "", nil); err == nil {
		t.Fatal("a bucket backend without a Worker was accepted")
	}
	f := &fakeObjects{}
	b, err := NewBucket(f, "docs", "atlas", "documents", errFakeMissing)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, Options{Default: b, Now: c.now})
	ctx := context.Background()
	rec, err := s.Put(ctx, "a.csv", "", "", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objects["docs|atlas|documents/"+rec.SHA256]; !ok || rec.Backend != "s3" {
		t.Fatalf("object not under the prefix: %v", f.objects)
	}
	if readAll(t, s, rec) != csv {
		t.Fatal("read back differs")
	}
	// A page is stored as an attachment, so a link to it downloads it instead of running it.
	page, _ := s.Put(ctx, "x.html", "", "", strings.NewReader("<html><body>hi</body></html>"))
	if !RendersAsPage(page.ContentType) {
		t.Fatalf("html detected as %q", page.ContentType)
	}
	delete(f.objects, "docs|atlas|documents/"+rec.SHA256)
	if _, err := s.OpenBytes(ctx, rec); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing object: %v, want ErrUnavailable", err)
	}
}
