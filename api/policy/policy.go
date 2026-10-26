// Package policy is the policy administration point
// (ADR-0475 §9): administrators write policies as
// drafts, publish them as immutable numbered versions, switch them on and off, and
// try them against a simulated request. What it publishes is compiled once into an
// immutable [authz.Set] that the enforcement point reads without a lock.
//
// Design-time state in two sidecar stores owned by the run loop: one record per
// policy (its draft, its current version, whether it is enabled) and one file per
// published version, written once. Nothing here reaches the event log; a decision
// records the policy id and version it was made under, and this store is where that
// version can be read back.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/authz"
	"github.com/pblumer/atlas/limits"
)

var (
	ErrNotFound   = errors.New("policy: no such policy")
	ErrNoDraft    = errors.New("policy: there is no draft to publish")
	ErrIDMismatch = errors.New("policy: the document's id is not the policy's")
	// ErrInvalid marks a document that does not parse or does not compile: the
	// caller's to fix, as against a store that could not be read or written.
	ErrInvalid = errors.New("policy: invalid document")
)

// record is one policy's administrative state.
type record struct {
	ID string `json:"id"`
	// Current is the newest published version, 0 before the first.
	Current int  `json:"current"`
	Enabled bool `json:"enabled"`
	// Draft is the next version as it is being written; nil when there is none.
	Draft         json.RawMessage `json:"draft,omitempty"`
	DraftWarnings []string        `json:"draftWarnings,omitempty"`
	UpdatedAt     int64           `json:"updatedAt"`
	UpdatedBy     string          `json:"updatedBy"`
}

// versionRecord is one published version. It is written once and never again.
type versionRecord struct {
	ID          string          `json:"id"`
	Version     int             `json:"version"`
	Document    json.RawMessage `json:"document"`
	PublishedAt int64           `json:"publishedAt"`
	PublishedBy string          `json:"publishedBy"`
}

func versionKey(id string, v int) string { return id + "@" + strconv.Itoa(v) }

// Service is the policy area. Build it with [Open].
type Service struct {
	loop     *runloop.Loop
	policies *sidecar.Store[record]
	versions *sidecar.Store[versionRecord]
	now      func() time.Time
	// set is the compiled published set. A nil set is one that could not be built
	// from the stores, which the PDP answers Indeterminate — never "no policies".
	set     atomic.Pointer[authz.Set]
	loadErr atomic.Pointer[error]
	// resolve fills a simulated subject's attributes from the user store when the
	// simulation names only an id. Optional.
	resolve func(id string) (authz.Subject, bool)

	// Limits are the installation's resource budgets. Open sets them to
	// [limits.Default]; the server overwrites them with its own.
	Limits limits.Limits
}

// budgets is how this service reads a ceiling, defaulting the zero Limits (which
// admits nothing) to [limits.Default].
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}

// Open opens the stores under dir and compiles what is published. It reads the
// stores directly rather than through the loop: it runs while the server is being
// built, before the loop serves anybody, and the loop is not yet running to ask.
func Open(loop *runloop.Loop, dir string, now func() time.Time) (*Service, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	pols, err := sidecar.NewStore(filepath.Join(dir, "policies"), "policy", func(r record) string { return r.ID })
	if err != nil {
		return nil, err
	}
	vers, err := sidecar.NewStore(filepath.Join(dir, "versions"), "policy version",
		func(v versionRecord) string { return versionKey(v.ID, v.Version) })
	if err != nil {
		return nil, err
	}
	s := &Service{loop: loop, policies: pols, versions: vers, now: now, Limits: limits.Default()}
	// A set that will not build must not stop the server: every other process would
	// go down with the governed ones. It leaves the set nil instead — every governed
	// action Indeterminate, so refused — and says why where an administrator looks.
	if err := s.rebuild(); err != nil {
		s.loadErr.Store(&err)
	}
	return s, nil
}

// LoadError is why the published set could not be built at startup or at the last
// publication, or nil.
func (s *Service) LoadError() error {
	if p := s.loadErr.Load(); p != nil {
		return *p
	}
	return nil
}

// SetSubjectResolver installs the lookup a simulation uses for a subject it names by
// id alone.
func (s *Service) SetSubjectResolver(fn func(id string) (authz.Subject, bool)) { s.resolve = fn }

// Set is the compiled published set the enforcement point decides with.
func (s *Service) Set() *authz.Set { return s.set.Load() }

// rebuild compiles every enabled policy's current version. A failure leaves the set
// nil, so every governed action is Indeterminate until somebody fixes it: a set that
// silently lost a policy would permit what that policy denied. Runs on the loop.
func (s *Service) rebuild() error {
	recs, err := s.policies.LoadAll()
	if err != nil {
		s.set.Store(nil)
		return err
	}
	var published []authz.Published
	for _, r := range recs {
		if !r.Enabled || r.Current == 0 {
			continue
		}
		v, ok, err := s.versions.Get(versionKey(r.ID, r.Current))
		if err != nil || !ok {
			s.set.Store(nil)
			return fmt.Errorf("policy %q: version %d is missing: %v", r.ID, r.Current, err)
		}
		doc, err := authz.ParseDocument(v.Document)
		if err != nil {
			s.set.Store(nil)
			return err
		}
		published = append(published, authz.Published{Document: doc, Version: v.Version, PublishedAt: v.PublishedAt, PublishedBy: v.PublishedBy})
	}
	set, err := authz.CompileSet(context.Background(), published)
	if err != nil {
		s.set.Store(nil)
		return err
	}
	s.set.Store(set)
	s.loadErr.Store(nil)
	return nil
}

// Summary is a policy as the listing shows it.
type Summary struct {
	ID            string          `json:"id"`
	Current       int             `json:"current"`
	Enabled       bool            `json:"enabled"`
	Draft         json.RawMessage `json:"draft,omitempty"`
	DraftWarnings []string        `json:"draftWarnings,omitempty"`
	UpdatedAt     int64           `json:"updatedAt"`
	UpdatedBy     string          `json:"updatedBy"`
}

func summary(r record) Summary {
	return Summary{ID: r.ID, Current: r.Current, Enabled: r.Enabled, Draft: r.Draft,
		DraftWarnings: r.DraftWarnings, UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy}
}

// List returns every policy, by id.
func (s *Service) List() ([]Summary, error) {
	var (
		out []Summary
		err error
	)
	s.loop.Do(func() {
		var recs []record
		if recs, err = s.policies.LoadAll(); err != nil {
			return
		}
		for _, r := range recs {
			out = append(out, summary(r))
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []Summary{}
	}
	return out, err
}

// SaveDraft validates a document and keeps it as the policy's next version. A draft
// that does not compile is refused here, where its author is.
func (s *Service) SaveDraft(id string, src []byte, by string) (Summary, error) {
	doc, err := authz.ParseDocument(src)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if doc.ID != id {
		return Summary{}, fmt.Errorf("%w: %w: %q in a document saved as %q", ErrInvalid, ErrIDMismatch, doc.ID, id)
	}
	_, warnings, err := authz.CompilePolicy(context.Background(), doc, 1)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return Summary{}, err
	}
	var out record
	s.loop.Do(func() {
		r, _, gerr := s.policies.Get(id)
		if gerr != nil {
			err = gerr
			return
		}
		r.ID, r.Draft, r.DraftWarnings = id, canonical, warnings
		r.UpdatedAt, r.UpdatedBy = s.now().UnixNano(), by
		if err = s.policies.Save(r); err == nil {
			out = r
		}
	})
	return summary(out), err
}

// Publish makes the draft the policy's next version: written once to its own file,
// then recorded as current and enabled, then compiled into the set. A version file a
// crash left unrecorded is skipped, never overwritten.
func (s *Service) Publish(id, by string) (Summary, error) {
	var (
		out record
		err error
	)
	s.loop.Do(func() {
		r, ok, gerr := s.policies.Get(id)
		switch {
		case gerr != nil:
			err = gerr
			return
		case !ok || r.Draft == nil:
			err = ErrNoDraft
			return
		}
		next := r.Current + 1
		for {
			_, exists, gerr := s.versions.Get(versionKey(id, next))
			if gerr != nil {
				err = gerr
				return
			}
			if !exists {
				break
			}
			next++
		}
		now := s.now().UnixNano()
		if err = s.versions.Save(versionRecord{ID: id, Version: next, Document: r.Draft, PublishedAt: now, PublishedBy: by}); err != nil {
			return
		}
		r.Current, r.Enabled, r.Draft, r.DraftWarnings = next, true, nil, nil
		r.UpdatedAt, r.UpdatedBy = now, by
		if err = s.policies.Save(r); err != nil {
			return
		}
		out = r
		err = s.rebuild()
	})
	return summary(out), err
}

// SetEnabled switches a published policy on or off.
func (s *Service) SetEnabled(id string, enabled bool, by string) (Summary, error) {
	var (
		out record
		err error
	)
	s.loop.Do(func() {
		r, ok, gerr := s.policies.Get(id)
		switch {
		case gerr != nil:
			err = gerr
			return
		case !ok || r.Current == 0:
			err = ErrNotFound
			return
		}
		r.Enabled, r.UpdatedAt, r.UpdatedBy = enabled, s.now().UnixNano(), by
		if err = s.policies.Save(r); err != nil {
			return
		}
		out = r
		err = s.rebuild()
	})
	return summary(out), err
}

// Version reads one published version back.
func (s *Service) Version(id string, v int) (versionRecord, bool, error) {
	var (
		out versionRecord
		ok  bool
		err error
	)
	s.loop.Do(func() { out, ok, err = s.versions.Get(versionKey(id, v)) })
	return out, ok, err
}

// detail is one policy with its version history.
type detail struct {
	Summary
	Versions []versionRecord `json:"versions"`
}

func (s *Service) get(id string) (detail, bool, error) {
	var (
		out detail
		ok  bool
		err error
	)
	s.loop.Do(func() {
		var r record
		if r, ok, err = s.policies.Get(id); err != nil || !ok {
			return
		}
		out.Summary = summary(r)
		out.Versions = []versionRecord{}
		for v := 1; v <= r.Current; v++ {
			if vr, vok, verr := s.versions.Get(versionKey(id, v)); verr == nil && vok {
				out.Versions = append(out.Versions, vr)
			}
		}
	})
	return out, ok, err
}

// compact keeps a raw document readable in a response without re-indenting it.
func compact(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}
