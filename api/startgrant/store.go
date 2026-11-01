package startgrant

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// SchemaVersion identifies the durable grant-document format.
const SchemaVersion = 1

// ActionGrant and ActionRevoke describe immutable audited audience changes.
const (
	ActionGrant  = "grant"
	ActionRevoke = "revoke"
)

const maxAudience = 256

// Change is one immutable audit entry in a process-scoped grant document.
// Revision increases per application/process, not globally.
type Change struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      uint64 `json:"revision"`
	At            int64  `json:"at"`
	ActorID       string `json:"actorId"`
	Action        string `json:"action"`
	Grant         Grant  `json:"grant"`
}

// ChangeRequest is prepared by a future admin-only HTTP service. The actor and
// timestamp must come from the authenticated server context, not the client.
type ChangeRequest struct {
	Grant            Grant
	ActorID          string
	At               int64
	ExpectedRevision uint64
}

// ErrStaleRevision means another admin change won before this request.
var ErrStaleRevision = errors.New("start grant revision changed")

// ErrUncertainWrite requires restart/recovery before any positive authorization.
var ErrUncertainWrite = errors.New("start grant store requires recovery after write error")

type scope struct {
	applicationID string
	processID     string
}

// document is one atomic sidecar record per application/process. The latest
// policy and its complete immutable logical audit trail share one fsync-backed
// replacement. A missing record denies access; losing a separate tail-revoke
// file cannot resurrect a grant, because there is no separate tail file.
type document struct {
	SchemaVersion int      `json:"schemaVersion"`
	ApplicationID string   `json:"applicationId"`
	ProcessID     string   `json:"processId"`
	Revision      uint64   `json:"revision"`
	History       []Change `json:"history"`
}

// Store owns durable process-start grants, with single-writer run-loop access.
// Never call its mutable methods concurrently or from the processor hot path.
type Store struct {
	files     *sidecar.Store[document]
	current   map[scope]document
	uncertain bool
}

// NewStore loads all scoped grant documents and validates their audit chains.
// A corrupt record fails startup instead of granting access from partial data.
func NewStore(dir string) (*Store, error) {
	files, err := sidecar.NewStore(dir, "startgrantstore", func(d document) string {
		return d.ApplicationID + "\x00" + d.ProcessID
	})
	if err != nil {
		return nil, err
	}
	all, err := files.LoadAll()
	if err != nil {
		return nil, err
	}
	s := &Store{files: files, current: make(map[scope]document, len(all))}
	for _, d := range all {
		if err := validateDocument(d); err != nil {
			return nil, fmt.Errorf("startgrantstore: invalid %q/%q: %w", d.ApplicationID, d.ProcessID, err)
		}
		key := scope{d.ApplicationID, d.ProcessID}
		if _, duplicate := s.current[key]; duplicate {
			return nil, errors.New("startgrantstore: duplicate scope")
		}
		s.current[key] = d
	}
	return s, nil
}

func validID(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}

func validateGrant(g Grant) error {
	if !validID(g.ApplicationID) || !validID(g.ProcessID) {
		return errors.New("application and process identities are required")
	}
	if len(g.UserIDs)+len(g.GroupIDs) > maxAudience {
		return errors.New("start grant audience exceeds limit")
	}
	seenUsers := make(map[string]bool, len(g.UserIDs))
	for _, id := range g.UserIDs {
		if !validID(id) || seenUsers[id] {
			return errors.New("invalid or duplicate user ID")
		}
		seenUsers[id] = true
	}
	seenGroups := make(map[string]bool, len(g.GroupIDs))
	for _, id := range g.GroupIDs {
		if !validID(id) || seenGroups[id] {
			return errors.New("invalid or duplicate group ID")
		}
		seenGroups[id] = true
	}
	return nil
}

func validateChange(c Change) error {
	if c.SchemaVersion != SchemaVersion || c.Revision == 0 || c.At <= 0 || !validID(c.ActorID) {
		return errors.New("unsupported schema, revision, timestamp or actor")
	}
	if err := validateGrant(c.Grant); err != nil {
		return err
	}
	empty := len(c.Grant.UserIDs)+len(c.Grant.GroupIDs) == 0
	if (empty && c.Action != ActionRevoke) || (!empty && c.Action != ActionGrant) {
		return errors.New("action does not match grant audience")
	}
	return nil
}

func validateDocument(d document) error {
	if d.SchemaVersion != SchemaVersion || !validID(d.ApplicationID) || !validID(d.ProcessID) ||
		d.Revision == 0 || uint64(len(d.History)) != d.Revision {
		return errors.New("unsupported schema or incomplete audit history")
	}
	for i, c := range d.History {
		if err := validateChange(c); err != nil {
			return err
		}
		if c.Revision != uint64(i)+1 || c.Grant.ApplicationID != d.ApplicationID || c.Grant.ProcessID != d.ProcessID {
			return errors.New("audit revision or scope mismatch")
		}
	}
	return nil
}

func cloneChange(c Change) Change {
	c.Grant.UserIDs = append([]string(nil), c.Grant.UserIDs...)
	c.Grant.GroupIDs = append([]string(nil), c.Grant.GroupIDs...)
	return c
}

// Set atomically replaces the effective policy and its complete audit trail.
// Callers MUST first enforce admin authorization and validate the target
// application, BPMN process and audience against current server-side records.
// The store is not itself a route guard. A failed write poisons authorization
// until recovery because a failed fsync can have an unknown outcome.
func (s *Store) Set(req ChangeRequest) (Change, error) {
	if s.uncertain {
		return Change{}, ErrUncertainWrite
	}
	if err := validateGrant(req.Grant); err != nil {
		return Change{}, err
	}
	key := scope{req.Grant.ApplicationID, req.Grant.ProcessID}
	current := s.current[key]
	if current.Revision != req.ExpectedRevision {
		return Change{}, ErrStaleRevision
	}
	action := ActionGrant
	if len(req.Grant.UserIDs)+len(req.Grant.GroupIDs) == 0 {
		action = ActionRevoke
	}
	change := Change{
		SchemaVersion: SchemaVersion,
		Revision:      current.Revision + 1,
		At:            req.At,
		ActorID:       req.ActorID,
		Action:        action,
		Grant:         cloneChange(Change{Grant: req.Grant}).Grant,
	}
	if err := validateChange(change); err != nil {
		return Change{}, err
	}
	next := document{
		SchemaVersion: SchemaVersion,
		ApplicationID: key.applicationID,
		ProcessID:     key.processID,
		Revision:      change.Revision,
		History:       make([]Change, len(current.History), len(current.History)+1),
	}
	copy(next.History, current.History)
	next.History = append(next.History, change)
	if err := s.files.Save(next); err != nil {
		s.uncertain = true
		return Change{}, err
	}
	s.current[key] = next
	return cloneChange(change), nil
}

// Current returns a defensive copy of the latest audited mutation. It is
// unavailable after an uncertain write until restart/recovery.
func (s *Store) Current(applicationID, processID string) (Change, bool) {
	if s.uncertain {
		return Change{}, false
	}
	d, ok := s.current[scope{applicationID, processID}]
	if !ok || len(d.History) == 0 {
		return Change{}, false
	}
	return cloneChange(d.History[len(d.History)-1]), true
}

// Allowed checks only the exact last durable audience, never project viewer,
// editor, task assignment or operator privileges. Call it on the run-loop.
func (s *Store) Allowed(applicationID, processID string, principal Principal) bool {
	rec, ok := s.Current(applicationID, processID)
	if !ok || rec.Action != ActionGrant {
		return false
	}
	return Allowed([]Grant{rec.Grant}, applicationID, processID, principal)
}

// History returns defensive copies of all audited changes, oldest first.
func (s *Store) History(applicationID, processID string) []Change {
	d, ok := s.current[scope{applicationID, processID}]
	out := []Change{}
	if !ok {
		return out
	}
	for _, rec := range d.History {
		out = append(out, cloneChange(rec))
	}
	return out
}
