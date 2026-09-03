package sidecar

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store is a durable store for one kind of design-time record: one JSON file per
// key under a single directory, written with the durability discipline above.
//
// Atlas has sixteen of these — drafts, projects, forms, workers, deployments,
// releases, users, deploy tokens and the rest. They differ in what they store,
// how a record names itself, and what order a listing comes back in. They agreed
// on everything else, which is what this type now holds: create the directory,
// encode the key into a safe filename, write atomically, read back, ignore files
// that are not ours, delete idempotently.
//
// Like the stores it replaces, a Store does no locking of its own: it is owned by
// the server's run-loop goroutine, the single writer of design-time state.
type Store[T any] struct {
	dir  string
	name string
	key  func(T) string
	enc  func(string) string
	ok   func(string) bool
	less func(a, b T) bool
}

// Option configures a Store at construction. The defaults cover the common case
// (hex-encoded filenames, listing in filename order); an option is only needed
// where a store genuinely differs.
type Option[T any] func(*Store[T])

// Names replaces the default filename scheme. enc maps a record's key to its
// filename stem, and ok reports whether a stem found in the directory is one of
// this store's records — the guard that lets an unrelated file share the
// directory without breaking a listing.
//
// The default hex-encodes the key, which is what makes an arbitrary BPMN id or
// email address a safe, unique, deterministic filename. Override it only when
// the key is already filename-safe *and* recognizable: a hex token, a decimal
// entity key.
func Names[T any](enc func(string) string, ok func(string) bool) Option[T] {
	return func(s *Store[T]) { s.enc, s.ok = enc, ok }
}

// Order sets the order LoadAll returns records in. Without it a listing comes
// back in filename order, which is stable but arbitrary; with it a store states
// the order its UI expects — newest-first for drafts, creation order for
// projects. less has sort.Slice semantics.
func Order[T any](less func(a, b T) bool) Option[T] {
	return func(s *Store[T]) { s.less = less }
}

// NewStore opens (creating if needed) the directory backing a store. name is the
// prefix every error from this store carries, so a failure names the store it
// came from; key extracts a record's identity, which is what Save files it under.
func NewStore[T any](dir, name string, key func(T) string, opts ...Option[T]) (*Store[T], error) {
	s := &Store[T]{
		dir:  dir,
		name: name,
		key:  key,
		enc:  func(k string) string { return hex.EncodeToString([]byte(k)) },
		ok:   func(stem string) bool { _, err := hex.DecodeString(stem); return err == nil },
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("%s: create dir: %w", name, err)
	}
	return s, nil
}

// Dir is the directory the store's records live in. Callers that keep sibling
// artifacts beside a record — a documentation PDF (ADR-0143) — need it.
func (s *Store[T]) Dir() string { return s.dir }

// FileFor maps a key to its record path. It is a pure path helper and does not
// check the key — the entry points below do that; call it only with a key you
// have already addressed the store with.
func (s *Store[T]) FileFor(key string) string {
	return filepath.Join(s.dir, s.enc(key)+".json")
}

// addressable reports whether key names a record this store can reach. It is the
// same predicate a listing uses to decide whether a file in the directory is one
// of ours, applied on the way in: a key that could not have produced a record
// name must not address one either.
//
// Under the default hex encoding every key passes, because any string
// hex-encodes to valid hex. It earns its keep for a store that names files by the
// key itself — there, an unchecked key is a path, and a request-supplied one
// ("../secret") would escape the directory.
func (s *Store[T]) addressable(key string) bool { return s.ok(s.enc(key)) }

// Save writes a record durably, overwriting any record already filed under the
// same key. On return with nil error it is on disk (I2 / ADR-0005). A record
// whose key could not name a file in this store is refused rather than written.
func (s *Store[T]) Save(rec T) error {
	key := s.key(rec)
	if !s.addressable(key) {
		return fmt.Errorf("%s: refusing unsafe key %q", s.name, key)
	}
	return WriteJSON(s.dir, s.FileFor(key), rec)
}

// Get returns the record filed under key, or ok=false if there is none. A
// missing record is a normal state, not an error, and so is a key that could
// never name one.
func (s *Store[T]) Get(key string) (T, bool, error) {
	var zero T
	if !s.addressable(key) {
		return zero, false, nil
	}
	data, err := os.ReadFile(s.FileFor(key))
	if err != nil {
		if os.IsNotExist(err) {
			return zero, false, nil
		}
		return zero, false, fmt.Errorf("%s: read: %w", s.name, err)
	}
	var rec T
	if err := json.Unmarshal(data, &rec); err != nil {
		return zero, false, fmt.Errorf("%s: decode: %w", s.name, err)
	}
	return rec, true, nil
}

// Delete removes the record filed under key. A missing record — or a key that
// could never name one — is not an error, so cleanup is idempotent.
func (s *Store[T]) Delete(key string) error {
	if !s.addressable(key) {
		return nil // nothing this key could name exists here
	}
	if err := os.Remove(s.FileFor(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("%s: remove: %w", s.name, err)
	}
	return FsyncDir(s.dir)
}

// LoadAll reads every record in the store, in the order set by [Order]. Files
// that are not this store's records — a stray temp file, a sibling artifact, a
// subdirectory — are skipped rather than failing the listing.
func (s *Store[T]) LoadAll() ([]T, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("%s: read dir: %w", s.name, err)
	}
	var out []T
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if !s.ok(strings.TrimSuffix(name, ".json")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, fmt.Errorf("%s: read %s: %w", s.name, name, err)
		}
		var rec T
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("%s: decode %s: %w", s.name, name, err)
		}
		out = append(out, rec)
	}
	if s.less != nil {
		sort.Slice(out, func(i, j int) bool { return s.less(out[i], out[j]) })
	}
	return out, nil
}
