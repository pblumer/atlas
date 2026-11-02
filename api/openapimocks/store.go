// Package openapimocks serves mock REST APIs an operator turned on in the Console
// (ADR-draft-console-managed-openapi-mock).
//
// [ADR-0217](../../docs/adr/0217-openapi-mock-server.md) built the mock itself:
// `connector/rest/openapimock` compiles an OpenAPI document into matchers and finished
// response bytes, and `atlas mock-openapi` serves it from a shell. This package is the
// other half — the document uploaded into Atlas, switched on with one click, and served
// on the address Atlas already listens on, so an author part-way through a draft needs
// no host, no port, no file and no restart (ADR-0193's finding).
//
// Two rules shape it.
//
// **Atlas holds the document, never a path to it** (ADR-0202). A filename typed into a
// browser is a promise about a filesystem the browser cannot see; the bytes are stored
// beside the record, the way a documentation version's PDF is.
//
// **Serving never touches the run loop.** The compiled set is behind an atomic pointer,
// so a mock call is a pointer load, a path match and a write of bytes already in
// memory. It cannot queue behind the processor and the processor cannot queue behind it,
// which is why this may live in the engine's process at all when a connector call may
// not (ADR-0164, on the precedent ADR-0484 set). Compiling, which is the part that
// costs real time on a 4 MB document, happens off the loop; the loop is taken only for
// the store write and the pointer swap.
package openapimocks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/token"
)

// MockPath is the prefix the served mocks sit under. It is public and
// unauthenticated, as a mock must be — a model that carried an Atlas credential to
// reach its mock would be a model that changes between a mockup run and a real one,
// which is the one thing ADR-0181 forbids.
const MockPath = "/mock-api/"

// maxNameLen bounds a mock's name. It is a path segment and a label in a table, and
// 63 is what a DNS label allows — long enough for every API anyone names, short
// enough that the base URL stays something a person can read back off a screen.
const maxNameLen = 63

// namePattern is what a mock may be called. It is lower-case, digits and hyphens
// because the name is a URL segment: a name needing escaping would make the base URL
// Atlas prints differ from the one an author pastes into a task.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Mock is one Console-managed mock API as it is stored. The document itself is not
// here — it sits beside this record, keyed by the same id — because a 4 MB Stripe
// document in every listing would make reading the list cost what reading one costs.
type Mock struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Enabled is whether this document is being served. Off is inert: nothing is
	// compiled and nothing answers, so a stored record that is off costs the engine
	// nothing but the bytes on disk.
	Enabled bool `json:"enabled"`
	// Filename is what the uploaded document was called on the operator's own
	// computer. It names nothing Atlas can reach and is never resolved — it is here so
	// the Console can say *which* document is loaded, which is the question somebody
	// asks six weeks later.
	Filename string `json:"filename,omitempty"`
	// Title, Version, Operations, Files and Skipped are what compiling the document
	// found, recorded at upload so the list can describe a mock without recompiling
	// it. Skipped names the responses that lost a media type this mock cannot
	// generate — ADR-0217 says that out loud at startup, and a stored mock has no
	// startup to say it at.
	Title      string   `json:"title,omitempty"`
	Version    string   `json:"version,omitempty"`
	Operations int      `json:"operations"`
	Files      int      `json:"files,omitempty"`
	Skipped    []string `json:"skipped,omitempty"`
	// SpecBasePath is the path component of the document's own first server URL. It
	// is part of the base URL a task must call, so it is stored rather than recomputed
	// from a document the list does not read.
	SpecBasePath string `json:"specBasePath,omitempty"`
	// ServerURL is the document's first server URL whole. It is what the *real* API's
	// base is, which is the other half of the substitution an author is making.
	ServerURL string `json:"serverUrl,omitempty"`
	Size      int    `json:"size"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Store holds the mock records and their documents: one JSON record per id under a
// single directory, each beside the document it describes. It wraps the shared store
// rather than being one for the reason the documentation store does — a mock is two
// files, and both have to go together.
type Store struct {
	*sidecar.Store[Mock]
}

// NewStore opens (creating if needed) the directory backing the store.
func NewStore(dir string) (*Store, error) {
	s, err := sidecar.NewStore(dir, "openapimockstore",
		func(rec Mock) string { return rec.ID },
		sidecar.Names[Mock](func(id string) string { return id }, token.IsHex),
		sidecar.Order(func(a, b Mock) bool { return a.Name < b.Name }),
	)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}

// NewID mints a record id. It is hex so it is its own filename, which is what lets
// the document sit beside the record under a name derived from nothing else.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("openapimockstore: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// documentFileFor maps a record id to its document's path, beside the record's own.
func (s *Store) documentFileFor(id string) string {
	return filepath.Join(s.Dir(), id+".openapi")
}

// Save writes a record and its document durably: the document first, then the
// record. The order matters — the record is what a reader discovers, so it must never
// point at a document that is not yet on disk (I2).
func (s *Store) Save(rec Mock, document []byte) error {
	if !token.IsHex(rec.ID) {
		return fmt.Errorf("openapimockstore: refusing unsafe id %q", rec.ID)
	}
	if err := sidecar.WriteFile(s.Dir(), s.documentFileFor(rec.ID), document); err != nil {
		return err
	}
	return s.Store.Save(rec)
}

// SaveRecord rewrites only the metadata sidecar, leaving the stored document alone.
// Renaming a mock or switching it off is such an update: what is served changes, the
// document does not.
func (s *Store) SaveRecord(rec Mock) error {
	if !token.IsHex(rec.ID) {
		return fmt.Errorf("openapimockstore: refusing unsafe id %q", rec.ID)
	}
	return s.Store.Save(rec)
}

// Document reads back the document stored for a record.
func (s *Store) Document(id string) ([]byte, error) {
	if !token.IsHex(id) {
		return nil, fmt.Errorf("openapimockstore: refusing unsafe id %q", id)
	}
	return os.ReadFile(s.documentFileFor(id))
}

// Delete removes a record and its document. The record goes first: it is what a
// reader discovers, so the window between the two calls must not be one in which a
// record points at a document that is gone.
func (s *Store) Delete(id string) error {
	if err := s.Store.Delete(id); err != nil {
		return err
	}
	if err := os.Remove(s.documentFileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("openapimockstore: remove document: %w", err)
	}
	return nil
}

// ValidateName checks a proposed mock name and returns it normalized, or the reason
// it cannot be used. The message is written for the person typing it, not for a log.
func ValidateName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case name == "":
		return "", fmt.Errorf("a name is required: it is the mock's address, as in %spetstore", MockPath)
	case len(name) > maxNameLen:
		return "", fmt.Errorf("the name is longer than %d characters", maxNameLen)
	case !namePattern.MatchString(name):
		return "", fmt.Errorf("the name %q is not usable in a URL: use lower-case letters, digits and hyphens, starting with a letter or digit", raw)
	}
	return name, nil
}
