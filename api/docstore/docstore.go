// Package docstore keeps the files processes receive, so that a process variable can
// hold a reference to a file instead of the file (ADR-draft-a-file-is-a-document-not-a-variable).
//
// A variable is a business record: every write of one is an event in the log, a value
// in state and a full copy in the per-step variable history, and the variable budget
// is a mebibyte (ADR-0294). A CSV upload or a PDF does not belong there. This package
// stores it once, outside the log, and hands back a small reference the process carries
// instead:
//
//	{"$document": "doc_…", "name": "…", "contentType": "…", "size": 1234, "sha256": "…"}
//
// # What is where
//
// The metadata of every document is a small JSON record under <dir>/meta, written with
// the sidecar discipline (temp, fsync, rename, fsync the directory). The bytes are kept
// by a [Backend]: the local one stores them under <dir>/blobs, one file per SHA-256, and
// an installation may choose an S3 bucket instead. A record names the backend its bytes
// were written to, so changing the default never strands a document.
//
// # What it never does
//
// It is written by the API server, never by the processor, and nothing in it is replayed:
// no event carries a document's bytes and applyToState never opens this store (I4). A
// document's durability is the write discipline here, which is why a reference is only
// handed out once its bytes and its record are both on disk.
//
// # Lifecycle
//
// A document is pending until a variable that names it enters an instance; then it is
// bound to that instance's root. [Store.Sweep] removes a pending document after its time
// to live and a bound one once no instance it is bound to exists any more, and deletes
// the bytes when no record names them. Deciding *who* may read a document is the API
// server's job (it knows the caller and the instance rules); this package only records
// the facts that decision needs.
package docstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/internal/sharedread"
	"github.com/pblumer/atlas/limits"
)

// RefKey is the key that marks a JSON object as a document reference.
const RefKey = "$document"

// idPrefix starts every document id, so a reference reads as one wherever it is copied.
const idPrefix = "doc_"

var (
	// ErrTooLarge is returned by Put for a document over the size limit.
	ErrTooLarge = errors.New("document is larger than the document limit")
	// ErrNotFound is returned for an id no record exists for.
	ErrNotFound = errors.New("no such document")
	// ErrEmpty is returned by Put for a document with no bytes.
	ErrEmpty = errors.New("document is empty")
	// ErrUnavailable is returned by Open when a record exists but its bytes do not —
	// a restore from a snapshot taken without documents, or a bucket that lost them.
	ErrUnavailable = errors.New("document is not available")
)

// Record is what the store knows about one document.
type Record struct {
	ID string `json:"id"`
	// Name is the file name the uploader gave, without any directory part.
	Name string `json:"name"`
	// DeclaredType is the content type the uploader claimed; ContentType is what the
	// first bytes say, which is what the document is served as.
	DeclaredType string `json:"declaredType,omitempty"`
	ContentType  string `json:"contentType"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	// Backend names where the bytes are, so a later change of the default does not
	// strand this document.
	Backend string `json:"backend"`
	// Uploader is the principal that uploaded it, "" with authentication off.
	Uploader string `json:"uploader,omitempty"`
	// CreatedAt is unix nanoseconds.
	CreatedAt int64 `json:"createdAt"`
	// BoundTo are the root process instances the document entered, ascending. Empty
	// means pending.
	BoundTo []uint64 `json:"boundTo,omitempty"`
	// Subject is the data subject whose key enciphers the bytes, when a variable the
	// process declares personal named the document (ADR-0314); "" when the bytes are
	// plain. Blob is then the SHA-256 of the ciphertext, the key the backend holds it
	// under — SHA256 stays the content's hash, which is what the reference states.
	Subject string `json:"subject,omitempty"`
	Blob    string `json:"blob,omitempty"`
}

// blobKey is where a record's bytes are in its backend.
func (r Record) blobKey() string {
	if r.Blob != "" {
		return r.Blob
	}
	return r.SHA256
}

// Cipher enciphers a document under a data subject's key and opens it again — the
// vault's SealBytes and OpenBytes. Opening an erased subject's document answers an
// error for which Erased is true.
type Cipher interface {
	SealBytes(subject, name string, plain []byte) ([]byte, error)
	OpenBytes(subject, name string, sealed []byte) ([]byte, error)
	Erased(err error) bool
	// HasKey reports whether a subject's key still exists, so a sweep can remove the
	// documents an erasure made unreadable.
	HasKey(subject string) (bool, error)
}

// ErrErased is OpenBytes's answer for a document whose data subject was erased: its
// bytes are permanently unreadable, which is the erasure working.
var ErrErased = errors.New("the document belonged to a data subject who was erased; it is permanently unreadable")

// cipherName binds a document's ciphertext to its id.
func cipherName(id string) string { return "document:" + id }

// Pending reports whether no instance holds the document yet.
func (r Record) Pending() bool { return len(r.BoundTo) == 0 }

// Ref is the reference a variable holds. The size is a json.Number, the type every
// number in a decoded variable has: a bare int64 would not survive the conversion into
// a FEEL value (expr.FromJSON) and would arrive as null.
func (r Record) Ref() map[string]any {
	return map[string]any{
		RefKey:        r.ID,
		"name":        r.Name,
		"contentType": r.ContentType,
		"size":        json.Number(strconv.FormatInt(r.Size, 10)),
		"sha256":      r.SHA256,
	}
}

// Backend keeps a document's bytes, addressed by their SHA-256 (hex).
type Backend interface {
	// Name is the backend's identity in a record ("local", "s3").
	Name() string
	// Put stores the bytes of the file at path under hash. It may be called for a
	// hash it already holds, and must then leave the stored bytes as they are.
	Put(ctx context.Context, hash, path string, size int64, contentType string) error
	// Open reads the bytes stored under hash. A hash it does not hold is os.ErrNotExist.
	Open(ctx context.Context, hash string) (io.ReadCloser, error)
	// Delete removes the bytes stored under hash; a hash it does not hold is no error.
	Delete(ctx context.Context, hash string) error
}

// Store is the document store rooted at one directory.
type Store struct {
	dir      string
	metaDir  string
	tmpDir   string
	maxBytes int64
	def      Backend
	backends map[string]Backend
	now      func() time.Time
	cipher   Cipher

	// mu serialises every change to a record and every decision to delete bytes, so
	// a sweep cannot delete the bytes an upload of the same content is about to name.
	// Reads of a record take no lock: a record is replaced by an atomic rename.
	mu sync.Mutex
}

// Options configure a Store.
type Options struct {
	// MaxBytes is the largest document accepted, the installation's limits.Document;
	// zero or less means the default document budget.
	MaxBytes int64
	// Default is the backend new documents are written to; nil is the local backend.
	Default Backend
	// Others are further backends existing records may name (the local backend is
	// always among them, so a document written before a switch stays readable).
	Others []Backend
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Cipher enciphers the documents of personal variables; nil leaves Seal refusing.
	Cipher Cipher
}

// Open opens (creating) the store under dir.
func Open(dir string, o Options) (*Store, error) {
	s := &Store{
		dir:      dir,
		metaDir:  filepath.Join(dir, "meta"),
		tmpDir:   filepath.Join(dir, "tmp"),
		maxBytes: o.MaxBytes,
		now:      o.Now,
		backends: map[string]Backend{},
		cipher:   o.Cipher,
	}
	if s.maxBytes <= 0 {
		s.maxBytes = limits.Default().Document
	}
	if s.now == nil {
		s.now = time.Now
	}
	for _, d := range []string{s.metaDir, s.tmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("docstore: %w", err)
		}
	}
	local, err := NewLocal(filepath.Join(dir, "blobs"))
	if err != nil {
		return nil, err
	}
	s.backends[local.Name()] = local
	for _, b := range o.Others {
		s.backends[b.Name()] = b
	}
	s.def = local
	if o.Default != nil {
		s.def = o.Default
		s.backends[o.Default.Name()] = o.Default
	}
	// A temp file left by an upload a crash interrupted is nobody's document.
	if entries, err := os.ReadDir(s.tmpDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(s.tmpDir, e.Name()))
		}
	}
	return s, nil
}

// MaxBytes is the largest document Put accepts.
func (s *Store) MaxBytes() int64 { return s.maxBytes }

// DefaultBackend names the backend new documents are written to.
func (s *Store) DefaultBackend() string { return s.def.Name() }

// Put reads one document from body and stores it. name is the uploader's file name,
// declaredType the content type the uploader claimed. It returns the record once the
// bytes and the record are both durable.
func (s *Store) Put(ctx context.Context, name, declaredType, uploader string, body io.Reader) (Record, error) {
	tmp, err := os.CreateTemp(s.tmpDir, "upload-*")
	if err != nil {
		return Record{}, fmt.Errorf("docstore: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // a no-op once the local backend renamed it away

	h := sha256.New()
	head := &headBuffer{max: 512}
	// limit is the installation's document budget (Options.MaxBytes, resolved by the
	// caller from limits.Document); one byte past it tells an over-large upload apart
	// from one exactly at the limit.
	limit := s.maxBytes
	n, err := io.Copy(io.MultiWriter(tmp, h, head), io.LimitReader(body, limit+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Record{}, fmt.Errorf("docstore: write: %w", err)
	}
	if n > s.maxBytes {
		return Record{}, ErrTooLarge
	}
	if n == 0 {
		return Record{}, ErrEmpty
	}

	rec := Record{
		ID:           newID(),
		Name:         cleanName(name),
		DeclaredType: strings.TrimSpace(declaredType),
		ContentType:  DetectType(head.b, name),
		Size:         n,
		SHA256:       hex.EncodeToString(h.Sum(nil)),
		Backend:      s.def.Name(),
		Uploader:     uploader,
		CreatedAt:    s.now().UnixNano(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.def.Put(ctx, rec.SHA256, tmpPath, n, rec.ContentType); err != nil {
		return Record{}, fmt.Errorf("docstore: store bytes: %w", err)
	}
	if err := s.write(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Get reads the record for id.
func (s *Store) Get(id string) (Record, error) {
	if !ValidID(id) {
		return Record{}, ErrNotFound
	}
	data, err := sharedread.ReadFile(s.metaPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("docstore: %w", err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, fmt.Errorf("docstore: record %s: %w", id, err)
	}
	return rec, nil
}

// OpenBytes reads a document's bytes.
func (s *Store) OpenBytes(ctx context.Context, rec Record) (io.ReadCloser, error) {
	b, ok := s.backends[rec.Backend]
	if !ok {
		return nil, fmt.Errorf("%w: backend %q is not configured", ErrUnavailable, rec.Backend)
	}
	rc, err := b.Open(ctx, rec.blobKey())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrUnavailable
	}
	if err != nil || rec.Subject == "" {
		return rc, err
	}
	// Enciphered: read the ciphertext whole (it is at most the document budget plus the
	// AEAD's overhead) and open it under the subject's key.
	defer rc.Close()
	if s.cipher == nil {
		return nil, fmt.Errorf("%w: it is enciphered and this store holds no key", ErrUnavailable)
	}
	sealed, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	plain, err := s.cipher.OpenBytes(rec.Subject, cipherName(rec.ID), sealed)
	if err != nil {
		if s.cipher.Erased(err) {
			return nil, ErrErased
		}
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(plain)), nil
}

// Seal enciphers a document's bytes under a data subject's key: a variable the process
// declares personal names it (ADR-0314, ADR-draft-a-file-is-a-document-not-a-variable).
// The plain bytes stay where they were until a sweep finds no record naming them — at
// once, unless another document holds the same content. Sealing a document already
// sealed for the same subject changes nothing; for another subject it is refused, since
// one key cannot be erased for two people.
func (s *Store) Seal(ctx context.Context, id, subject string) error {
	if s.cipher == nil {
		return errors.New("docstore: personal documents need the vault, and this store has none")
	}
	if subject == "" {
		return errors.New("docstore: seal: no data subject")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.Get(id)
	if err != nil {
		return err
	}
	if rec.Subject == subject {
		return nil
	}
	if rec.Subject != "" {
		return fmt.Errorf("docstore: document %s is already personal data of another subject", id)
	}
	b, ok := s.backends[rec.Backend]
	if !ok {
		return fmt.Errorf("%w: backend %q is not configured", ErrUnavailable, rec.Backend)
	}
	rc, err := b.Open(ctx, rec.blobKey())
	if errors.Is(err, os.ErrNotExist) {
		return ErrUnavailable
	}
	if err != nil {
		return err
	}
	plain, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return err
	}
	sealed, err := s.cipher.SealBytes(subject, cipherName(id), plain)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.tmpDir, "seal-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, err = tmp.Write(sealed)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(sealed)
	key := hex.EncodeToString(sum[:])
	if err := b.Put(ctx, key, tmpPath, int64(len(sealed)), "application/octet-stream"); err != nil {
		return err
	}
	rec.Subject, rec.Blob = subject, key
	return s.write(rec)
}

// Available reports whether a document's bytes are where its record says. A backend
// that can tell without reading them (the local one) is asked; for one that cannot, the
// answer is true — reading a whole object to answer a yes-or-no question is the cost
// this exists to avoid, and a download that finds the bytes missing says so itself.
func (s *Store) Available(ctx context.Context, rec Record) (bool, error) {
	b, ok := s.backends[rec.Backend]
	if !ok {
		return false, nil
	}
	if h, ok := b.(interface {
		Has(ctx context.Context, hash string) (bool, error)
	}); ok {
		return h.Has(ctx, rec.blobKey())
	}
	return true, nil
}

// Bind records that the document entered the root instance root. Binding a document
// to an instance it is already bound to changes nothing.
func (s *Store) Bind(id string, root uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.Get(id)
	if err != nil {
		return err
	}
	i := sort.Search(len(rec.BoundTo), func(i int) bool { return rec.BoundTo[i] >= root })
	if i < len(rec.BoundTo) && rec.BoundTo[i] == root {
		return nil
	}
	rec.BoundTo = append(rec.BoundTo, 0)
	copy(rec.BoundTo[i+1:], rec.BoundTo[i:])
	rec.BoundTo[i] = root
	return s.write(rec)
}

// SweepResult says what one sweep removed.
type SweepResult struct {
	Expired  int // pending documents past their time to live
	Released int // bound documents whose instances are all gone
	Erased   int // personal documents whose data subject was erased
	Blobs    int // byte blobs no record named any more
}

// Sweep removes pending documents older than pendingTTL, bound documents none of whose
// instances exists any more (exists answers that), and then the bytes no remaining
// record names. It is safe to run at any time and again: a removal already made is not
// made twice, and a document an upload is writing is not seen until its record is.
func (s *Store) Sweep(ctx context.Context, pendingTTL time.Duration, exists func(root uint64) (bool, error)) (SweepResult, error) {
	var res SweepResult
	recs, err := s.All()
	if err != nil {
		return res, err
	}
	cutoff := s.now().Add(-pendingTTL).UnixNano()
	for _, rec := range recs {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		remove, erased := false, false
		if rec.Subject != "" && s.cipher != nil {
			// An erased subject's document can never be read again; keeping its
			// ciphertext only costs the disk.
			has, err := s.cipher.HasKey(rec.Subject)
			if err != nil {
				return res, err
			}
			erased = !has
		}
		if erased {
			remove = true
		} else if rec.Pending() {
			remove = rec.CreatedAt < cutoff
		} else {
			alive := false
			for _, k := range rec.BoundTo {
				ok, err := exists(k)
				if err != nil {
					return res, err
				}
				if ok {
					alive = true
					break
				}
			}
			remove = !alive
		}
		if !remove {
			continue
		}
		removed, err := s.removeIf(rec.ID, func(cur Record) bool {
			if erased {
				return cur.Subject == rec.Subject
			}
			// Re-checked under the lock: a bind between the listing and now keeps it.
			if rec.Pending() {
				return cur.Pending()
			}
			return len(cur.BoundTo) == len(rec.BoundTo)
		})
		if err != nil {
			return res, err
		}
		if removed {
			if erased {
				res.Erased++
			} else if rec.Pending() {
				res.Expired++
			} else {
				res.Released++
			}
		}
	}
	blobs, err := s.collectBlobs(ctx)
	res.Blobs = blobs
	return res, err
}

// removeIf deletes the record for id when keep(current record) holds.
func (s *Store) removeIf(id string, ok func(Record) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.Get(id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ok(cur) {
		return false, nil
	}
	if err := os.Remove(s.metaPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("docstore: %w", err)
	}
	return true, sidecar.FsyncDir(s.metaDir)
}

// collectBlobs deletes the bytes of every hash that a backend holds for a record that
// no longer exists. It works from the records rather than listing a bucket: the set of
// hashes still named is read under the lock, and compared with the hashes records
// named before this sweep removed them.
func (s *Store) collectBlobs(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.All()
	if err != nil {
		return 0, err
	}
	named := map[string]bool{}
	for _, r := range recs {
		named[r.Backend+"/"+r.blobKey()] = true
	}
	n := 0
	for name, b := range s.backends {
		lister, ok := b.(interface{ Hashes() ([]string, error) })
		if !ok {
			continue // a backend that cannot list keeps its bytes; see Forget
		}
		hashes, err := lister.Hashes()
		if err != nil {
			return n, err
		}
		for _, h := range hashes {
			if named[name+"/"+h] {
				continue
			}
			if err := b.Delete(ctx, h); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, s.forgetOrphans(ctx, named)
}

// forgetOrphans deletes, from a backend that cannot list what it holds, the bytes of
// the hashes in the orphan journal no record names. The journal is how an S3 bucket
// learns which objects the store wrote and no longer needs, without a bucket listing.
func (s *Store) forgetOrphans(ctx context.Context, named map[string]bool) error {
	path := filepath.Join(s.dir, "written.json")
	data, err := sharedread.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var written []string
	if err := json.Unmarshal(data, &written); err != nil {
		return fmt.Errorf("docstore: journal: %w", err)
	}
	keep := written[:0]
	for _, w := range written {
		backend, hash, _ := strings.Cut(w, "/")
		if named[w] {
			keep = append(keep, w)
			continue
		}
		b, ok := s.backends[backend]
		if !ok {
			keep = append(keep, w)
			continue
		}
		if err := b.Delete(ctx, hash); err != nil {
			return err
		}
	}
	return sidecar.WriteJSON(s.dir, path, keep)
}

// noteWritten adds backend/hash to the journal of bytes a non-listing backend holds.
// Called under mu.
func (s *Store) noteWritten(backend, hash string) error {
	path := filepath.Join(s.dir, "written.json")
	var written []string
	if data, err := sharedread.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &written)
	}
	key := backend + "/" + hash
	for _, w := range written {
		if w == key {
			return nil
		}
	}
	return sidecar.WriteJSON(s.dir, path, append(written, key))
}

// All reads every record.
func (s *Store) All() ([]Record, error) {
	entries, err := os.ReadDir(s.metaDir)
	if err != nil {
		return nil, fmt.Errorf("docstore: %w", err)
	}
	out := make([]Record, 0, len(entries))
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) {
			continue
		}
		rec, err := s.Get(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// write stores rec durably. Called under mu.
func (s *Store) write(rec Record) error {
	if _, listing := s.backends[rec.Backend].(interface{ Hashes() ([]string, error) }); !listing {
		if err := s.noteWritten(rec.Backend, rec.blobKey()); err != nil {
			return err
		}
	}
	if err := sidecar.WriteJSON(s.metaDir, s.metaPath(rec.ID), rec); err != nil {
		return fmt.Errorf("docstore: write record: %w", err)
	}
	return nil
}

func (s *Store) metaPath(id string) string { return filepath.Join(s.metaDir, id+".json") }

// newID mints an unguessable document id. It is not the hash: two people uploading the
// same bytes get two documents with their own bindings, while the bytes are stored once.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("docstore: no randomness: " + err.Error())
	}
	return idPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

// ValidID reports whether id has the shape newID produces, which is also what makes it
// safe as a file name.
func ValidID(id string) bool {
	rest, ok := strings.CutPrefix(id, idPrefix)
	if !ok || len(rest) != 26 {
		return false
	}
	for _, c := range rest {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// cleanName keeps the last path element of an uploaded file name and drops control
// characters, so a name can be shown and used in a download header as it is.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "document"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

// DetectType says what a document is from its first bytes. The extension refines a
// text the sniffer can only call text — a CSV is text/csv, not text/plain — and never
// overrides a binary format the bytes announce.
func DetectType(head []byte, name string) string {
	sniffed := http.DetectContentType(head)
	if !strings.HasPrefix(sniffed, "text/plain") {
		return sniffed
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown; charset=utf-8"
	}
	return sniffed
}

// RendersAsPage reports whether a browser would run a document of this type as a page
// rather than show it, so it must be served as an attachment (as ADR-0478 does for
// exported mail attachments).
func RendersAsPage(contentType string) bool {
	ct := strings.ToLower(contentType)
	for _, p := range []string{"text/html", "image/svg", "text/xml", "application/xml", "application/xhtml", "text/xsl"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

// headBuffer keeps the first max bytes written to it, for content sniffing.
type headBuffer struct {
	b   []byte
	max int
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := h.max - len(h.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		h.b = append(h.b, p[:room]...)
	}
	return len(p), nil
}
