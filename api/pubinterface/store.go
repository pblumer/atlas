package pubinterface

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// SchemaVersion identifies the durable interface-document format.
const SchemaVersion = 1

// Audited actions. Each change to an interface — a contract published, a version
// rebound or deprecated, a grant changed — is one immutable entry in its history,
// in the sense ADR-0184 established for rights.
const (
	ActionPublish   = "publish"
	ActionRebind    = "rebind"
	ActionDeprecate = "deprecate"
	ActionGrants    = "grants"
)

// maxAudience bounds one grant's audience, as a start grant's is (ADR-0516).
const maxAudience = 256

// maxVersions bounds how many contract versions one interface keeps. A version is
// immutable and never deleted — a consumer may still be bound to it — so the bound
// is what keeps one document from growing without limit.
const maxVersions = 256

// Version is one published contract version: the immutable contract, the binding
// that realizes it now, and whether the publisher deprecated it.
type Version struct {
	Version     int      `json:"version"`
	PublishedAt int64    `json:"publishedAt"`
	PublishedBy string   `json:"publishedBy"`
	Contract    Contract `json:"contract"`
	Binding     Binding  `json:"binding"`
	BoundAt     int64    `json:"boundAt"`
	BoundBy     string   `json:"boundBy"`
	Deprecated  bool     `json:"deprecated,omitempty"`
	// DeprecatedAt, DeprecatedBy and DeprecationNote say when, by whom and why, so a
	// consumer told at deploy can read what to move to.
	DeprecatedAt    int64  `json:"deprecatedAt,omitempty"`
	DeprecatedBy    string `json:"deprecatedBy,omitempty"`
	DeprecationNote string `json:"deprecationNote,omitempty"`
}

// Change is one audited mutation of an interface.
type Change struct {
	Revision uint64 `json:"revision"`
	At       int64  `json:"at"`
	ActorID  string `json:"actorId"`
	Action   string `json:"action"`
	// Version is the contract version the change concerns; zero for a grant change.
	Version int `json:"version,omitempty"`
	// DefinitionKey is the definition a publish or rebind bound the version to.
	DefinitionKey uint64 `json:"definitionKey,omitempty"`
	// Grants is the whole of both rights as the change left them, for a grant change:
	// an audit that said only that something changed could not answer who could send.
	Grants *Grants `json:"grants,omitempty"`
	Note   string  `json:"note,omitempty"`
}

// Interface is one published interface as the store holds it.
type Interface struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	// ProcessID is the BPMN process the interface belongs to, fixed at its first
	// publication. ApplicationID is the application that process was filed under
	// then; every later act must find the bound definition filed under it too.
	ProcessID     string    `json:"processId"`
	ApplicationID string    `json:"applicationId"`
	Revision      uint64    `json:"revision"`
	Versions      []Version `json:"versions"`
	Grants        Grants    `json:"grants"`
	History       []Change  `json:"history"`
}

func (in Interface) clone() Interface {
	out := in
	out.Versions = make([]Version, len(in.Versions))
	for i, v := range in.Versions {
		v.Contract = v.Contract.clone()
		v.Binding = v.Binding.clone()
		out.Versions[i] = v
	}
	out.Grants = in.Grants.clone()
	out.History = make([]Change, len(in.History))
	for i, c := range in.History {
		if c.Grants != nil {
			g := c.Grants.clone()
			c.Grants = &g
		}
		out.History[i] = c
	}
	return out
}

// VersionOf answers one contract version.
func (in Interface) VersionOf(v int) (Version, bool) {
	if v < 1 || v > len(in.Versions) {
		return Version{}, false
	}
	return in.Versions[v-1], true
}

// Latest answers the newest contract version; an interface always has one.
func (in Interface) Latest() Version { return in.Versions[len(in.Versions)-1] }

// MayView reports whether a principal may read the contract: anybody when it is
// public, else the view audience. It does not consider the publishing application's
// own members; the service adds those, because only it knows the application.
func (in Interface) MayView(p Principal) bool {
	return in.Grants.Public || in.Grants.View.admits(p)
}

// MaySend reports whether a principal may deliver to the interface's entry points.
// Public never implies it, and neither does view.
func (in Interface) MaySend(p Principal) bool { return in.Grants.Send.admits(p) }

// Errors a write can answer.
var (
	ErrStaleRevision   = errors.New("published interface revision changed")
	ErrUncertainWrite  = errors.New("published interface store requires recovery after write error")
	ErrNotFound        = errors.New("no published interface with that id")
	ErrNoVersion       = errors.New("no such contract version")
	ErrOtherProcess    = errors.New("the interface belongs to another process")
	ErrSameContract    = errors.New("the contract is the newest version's; rebind it instead of publishing it again")
	ErrTooManyVersions = errors.New("the interface holds the most contract versions it may")
	ErrDeprecated      = errors.New("the version is already deprecated")
)

// Store owns the published interfaces, with single-writer run-loop access. Never
// call it concurrently or from the processor hot path.
type Store struct {
	files     *sidecar.Store[Interface]
	current   map[string]Interface
	uncertain bool
}

// NewStore loads every interface document and validates it. A corrupt document
// fails startup rather than granting from partial data.
func NewStore(dir string) (*Store, error) {
	files, err := sidecar.NewStore(dir, "pubinterfacestore", func(in Interface) string { return in.ID })
	if err != nil {
		return nil, err
	}
	all, err := files.LoadAll()
	if err != nil {
		return nil, err
	}
	s := &Store{files: files, current: make(map[string]Interface, len(all))}
	for _, in := range all {
		if err := validateInterface(in); err != nil {
			return nil, fmt.Errorf("pubinterfacestore: invalid %q: %w", in.ID, err)
		}
		if _, dup := s.current[in.ID]; dup {
			return nil, errors.New("pubinterfacestore: duplicate interface id")
		}
		s.current[in.ID] = in
	}
	return s, nil
}

func validID(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}

// CheckAudience refuses an oversized, malformed or repeating audience, and answers
// it sorted, so a grant is stored and compared in one canonical order.
func CheckAudience(a Audience) (Audience, error) {
	if len(a.UserIDs)+len(a.GroupIDs) > maxAudience {
		return Audience{}, fmt.Errorf("a grant names at most %d user and group ids", maxAudience)
	}
	check := func(kind string, ids []string) ([]string, error) {
		out := append([]string{}, ids...)
		sort.Strings(out)
		for i, id := range out {
			if !validID(id) {
				return nil, fmt.Errorf("invalid %s id %q", kind, id)
			}
			if i > 0 && out[i-1] == id {
				return nil, fmt.Errorf("duplicate %s id %q", kind, id)
			}
		}
		return out, nil
	}
	users, err := check("user", a.UserIDs)
	if err != nil {
		return Audience{}, err
	}
	groups, err := check("group", a.GroupIDs)
	if err != nil {
		return Audience{}, err
	}
	return Audience{UserIDs: users, GroupIDs: groups}, nil
}

func validateInterface(in Interface) error {
	if in.SchemaVersion != SchemaVersion || !ValidID(in.ID) || !validID(in.ProcessID) || !validID(in.ApplicationID) {
		return errors.New("unsupported schema or identity")
	}
	if in.Revision == 0 || uint64(len(in.History)) != in.Revision {
		return errors.New("incomplete audit history")
	}
	if len(in.Versions) == 0 || len(in.Versions) > maxVersions {
		return errors.New("no or too many contract versions")
	}
	for i, v := range in.Versions {
		if v.Version != i+1 || v.Binding.ProcessID != in.ProcessID || v.Binding.DefinitionKey == 0 ||
			len(v.Contract.EntryPoints) == 0 {
			return errors.New("contract version out of order or unbound")
		}
	}
	for i, c := range in.History {
		if c.Revision != uint64(i)+1 || c.At <= 0 || !validID(c.ActorID) {
			return errors.New("audit revision, time or actor invalid")
		}
		switch c.Action {
		case ActionPublish, ActionRebind, ActionDeprecate, ActionGrants:
		default:
			return errors.New("unknown audit action")
		}
	}
	for _, a := range []Audience{in.Grants.View, in.Grants.Send} {
		if _, err := CheckAudience(a); err != nil {
			return err
		}
	}
	return nil
}

// Uncertain reports whether a failed write left the store's durable state unknown.
// While it does, every positive answer and every further write is refused until a
// restart re-reads what actually reached the disk.
func (s *Store) Uncertain() bool { return s.uncertain }

// Get answers a defensive copy of one interface. Nothing is answered while the
// store is uncertain.
func (s *Store) Get(id string) (Interface, bool) {
	if s.uncertain {
		return Interface{}, false
	}
	in, ok := s.current[id]
	if !ok {
		return Interface{}, false
	}
	return in.clone(), true
}

// All answers every interface, ordered by id.
func (s *Store) All() []Interface {
	out := []Interface{}
	if s.uncertain {
		return out
	}
	for _, in := range s.current {
		out = append(out, in.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// MaySend answers the binding of one contract version when the principal holds the
// interface's send grant, for the peer endpoint of ADR-0372 to deliver through. It
// does not know whether the bound definition is still active; the caller checks.
func (s *Store) MaySend(id string, version int, p Principal) (Binding, bool) {
	in, ok := s.Get(id)
	if !ok || !in.MaySend(p) {
		return Binding{}, false
	}
	v, ok := in.VersionOf(version)
	if !ok {
		return Binding{}, false
	}
	return v.Binding, true
}

// Meta is what every write is stamped with: the actor and the time are the
// server's, never the client's, and ExpectedRevision is the revision the actor read
// (zero for an interface that does not exist yet).
type Meta struct {
	ActorID          string
	At               int64
	ExpectedRevision uint64
}

func (m Meta) valid() bool { return validID(m.ActorID) && m.At > 0 }

// mutate applies one change to a copy of the interface, appends its audit entry and
// saves the whole document in one fsync-backed replacement; only then does the
// store's own view move.
func (s *Store) mutate(id string, m Meta, create func() (Interface, error), apply func(*Interface, *Change) error) (Interface, error) {
	if s.uncertain {
		return Interface{}, ErrUncertainWrite
	}
	if !m.valid() {
		return Interface{}, errors.New("a change names its actor and time")
	}
	cur, exists := s.current[id]
	if cur.Revision != m.ExpectedRevision {
		return Interface{}, ErrStaleRevision
	}
	var next Interface
	if exists {
		next = cur.clone()
	} else {
		if create == nil {
			return Interface{}, ErrNotFound
		}
		var err error
		if next, err = create(); err != nil {
			return Interface{}, err
		}
	}
	change := Change{Revision: next.Revision + 1, At: m.At, ActorID: m.ActorID}
	if err := apply(&next, &change); err != nil {
		return Interface{}, err
	}
	next.Revision = change.Revision
	next.History = append(next.History, change)
	if err := validateInterface(next); err != nil {
		return Interface{}, err
	}
	if err := s.files.Save(next); err != nil {
		s.uncertain = true
		return Interface{}, err
	}
	s.current[id] = next
	return next.clone(), nil
}

// Publish adds a new contract version, creating the interface on its first one. The
// caller has checked that the actor may publish the process and derived the contract
// and its binding from a deployed definition.
func (s *Store) Publish(id, applicationID string, contract Contract, binding Binding, m Meta) (Interface, error) {
	if !ValidID(id) || !validID(applicationID) || !validID(binding.ProcessID) {
		return Interface{}, errors.New("invalid interface, application or process id")
	}
	contract = contract.clone()
	contract.canonical()
	return s.mutate(id, m, func() (Interface, error) {
		return Interface{SchemaVersion: SchemaVersion, ID: id, ProcessID: binding.ProcessID, ApplicationID: applicationID,
			Grants: Grants{View: Audience{UserIDs: []string{}, GroupIDs: []string{}}, Send: Audience{UserIDs: []string{}, GroupIDs: []string{}}}}, nil
	}, func(in *Interface, c *Change) error {
		switch {
		case in.ProcessID != binding.ProcessID:
			return ErrOtherProcess
		case len(in.Versions) >= maxVersions:
			return ErrTooManyVersions
		case len(in.Versions) > 0 && in.Latest().Contract.Equal(contract):
			return ErrSameContract
		}
		v := Version{
			Version: len(in.Versions) + 1, PublishedAt: m.At, PublishedBy: m.ActorID,
			Contract: contract, Binding: binding.clone(), BoundAt: m.At, BoundBy: m.ActorID,
		}
		in.Versions = append(in.Versions, v)
		c.Action, c.Version, c.DefinitionKey = ActionPublish, v.Version, binding.DefinitionKey
		return nil
	})
}

// Rebind points one contract version at another definition. The caller has derived
// the contract that definition realizes and found it equal to the version's own:
// the contract is immutable, the binding is what a refactoring moves.
func (s *Store) Rebind(id string, version int, binding Binding, m Meta) (Interface, error) {
	return s.mutate(id, m, nil, func(in *Interface, c *Change) error {
		if version < 1 || version > len(in.Versions) {
			return ErrNoVersion
		}
		if in.ProcessID != binding.ProcessID {
			return ErrOtherProcess
		}
		v := &in.Versions[version-1]
		v.Binding, v.BoundAt, v.BoundBy = binding.clone(), m.At, m.ActorID
		c.Action, c.Version, c.DefinitionKey = ActionRebind, version, binding.DefinitionKey
		return nil
	})
}

// Deprecate marks one contract version deprecated. It stays readable and bound —
// deprecation tells a consumer to move, it does not take anything away — and it is
// not undone: a consumer that moved must not be told to move back.
func (s *Store) Deprecate(id string, version int, note string, m Meta) (Interface, error) {
	return s.mutate(id, m, nil, func(in *Interface, c *Change) error {
		if version < 1 || version > len(in.Versions) {
			return ErrNoVersion
		}
		v := &in.Versions[version-1]
		if v.Deprecated {
			return ErrDeprecated
		}
		v.Deprecated, v.DeprecatedAt, v.DeprecatedBy, v.DeprecationNote = true, m.At, m.ActorID, note
		c.Action, c.Version, c.Note = ActionDeprecate, version, note
		return nil
	})
}

// SetGrants wholly replaces both rights and the public flag.
func (s *Store) SetGrants(id string, g Grants, m Meta) (Interface, error) {
	view, err := CheckAudience(g.View)
	if err != nil {
		return Interface{}, err
	}
	send, err := CheckAudience(g.Send)
	if err != nil {
		return Interface{}, err
	}
	g = Grants{Public: g.Public, View: view, Send: send}
	return s.mutate(id, m, nil, func(in *Interface, c *Change) error {
		in.Grants = g.clone()
		snap := g.clone()
		c.Action, c.Grants = ActionGrants, &snap
		return nil
	})
}
