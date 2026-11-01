package startgrant

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// SchemaVersion identifies the durable change record format.
const SchemaVersion = 1

// ActionGrant and ActionRevoke are immutable changes to a process-start audience.
const (
	ActionGrant  = "grant"
	ActionRevoke = "revoke"
)

const maxAudience = 256

// Change is both an immutable audit entry and the effective grant snapshot
// at its revision. A single atomic sidecar write commits both facts together.
// This is installation-specific authorization data, never a portable app export.
type Change struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      uint64 `json:"revision"`
	At            int64  `json:"at"`
	ActorID       string `json:"actorId"`
	Action        string `json:"action"`
	Grant         Grant  `json:"grant"`
}

// ChangeRequest is a mutation prepared by an admin-only handler. ExpectedRevision
// prevents stale administration screens from overwriting a newer grant.
type ChangeRequest struct {
	Grant            Grant
	ActorID          string
	At               int64
	ExpectedRevision uint64
}

// ErrStaleRevision means another mutation won before this request.
var ErrStaleRevision = errors.New("start grant revision changed")

// ErrUncertainWrite means a sidecar write failed; its durability is unknown.
// Until restart/reload, no further writes or positive authorization are allowed.
var ErrUncertainWrite = errors.New("start grant store requires recovery after write error")

type scope struct {
	applicationID string
	processID     string
}

// Store owns append-only, fsync-backed grant revisions and their audit history.
// It is single-writer state: mutate and query it on the server's run-loop only.
// HTTP route role checks and session authentication are not implemented here.
type Store struct {
	files     *sidecar.Store[Change]
	history   []Change
	current   map[scope]Change
	revision  uint64
	uncertain bool
}

// NewStore reconstructs the effective grants from immutable change records.
// A malformed or incomplete audit chain refuses startup instead of granting
// permissions from an ambiguous snapshot.
func NewStore(dir string) (*Store, error) {
	files, err := sidecar.NewStore(dir, "startgrantstore", func(c Change) string {
		return fmt.Sprintf("%020d", c.Revision)
	})
	if err != nil {
		return nil, err
	}
	all, err := files.LoadAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Revision < all[j].Revision })
	s := &Store{files: files, current: make(map[scope]Change), history: all}
	for i, change := range all {
		if change.Revision != uint64(i)+1 {
			return nil, fmt.Errorf("startgrantstore: missing or duplicate revision at %d", i+1)
		}
		if err := validateChange(change); err != nil {
			return nil, fmt.Errorf("startgrantstore: invalid revision %d: %w", change.Revision, err)
		}
		s.current[scope{change.Grant.ApplicationID, change.Grant.ProcessID}] = change
		s.revision = change.Revision
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

func cloneChange(c Change) Change {
	c.Grant.UserIDs = append([]string(nil), c.Grant.UserIDs...)
	c.Grant.GroupIDs = append([]string(nil), c.Grant.GroupIDs...)
	return c
}

// Set commits a grant replacement or revocation as one immutable audited file.
// Callers MUST authorize an administrator and validate application/process and
// audience existence before calling Set; this is not an HTTP permission check.
// A failed Save poisons the store because an fsync error can have an unknown
// outcome, and retrying the same revision could overwrite audit evidence.
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
		Revision:      s.revision + 1,
		At:            req.At,
		ActorID:       req.ActorID,
		Action:        action,
		Grant:         cloneChange(Change{Grant: req.Grant}).Grant,
	}
	if err := validateChange(change); err != nil {
		return Change{}, err
	}
	if err := s.files.Save(change); err != nil {
		s.uncertain = true
		return Change{}, err
	}
	s.revision = change.Revision
	s.history = append(s.history, change)
	s.current[key] = change
	return cloneChange(change), nil
}

// Current returns a copy of the last durable mutation, including a revocation.
// An uncertain write makes the snapshot unavailable until restart.
func (s *Store) Current(applicationID, processID string) (Change, bool) {
	if s.uncertain {
		return Change{}, false
	}
	rec, ok := s.current[scope{applicationID, processID}]
	return cloneChange(rec), ok
}

// Allowed evaluates the latest durable grant without inferring any operator,
// project-viewer or task-assignment privilege. Call only on the run-loop.
func (s *Store) Allowed(applicationID, processID string, principal Principal) bool {
	rec, ok := s.Current(applicationID, processID)
	if !ok || rec.Action != ActionGrant {
		return false
	}
	return Allowed([]Grant{rec.Grant}, applicationID, processID, principal)
}

// History returns the immutable audit trail for one application and process,
// ordered oldest first. It never grants permission by itself.
func (s *Store) History(applicationID, processID string) []Change {
	out := []Change{}
	for _, rec := range s.history {
		if rec.Grant.ApplicationID == applicationID && rec.Grant.ProcessID == processID {
			out = append(out, cloneChange(rec))
		}
	}
	return out
}
