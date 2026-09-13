package api

import (
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// group is a named set of users. A project can be shared with a group as a scope
// member, granting the group's role to every user in it, so a team is shared with
// once instead of person by person (ADR-0180). Like a user
// (ADR-0044) it is operator/config data: a durable sidecar store, off the six
// engine invariants, and managing it is admin-gated. Members holds the ids of the
// users in the group.
type group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`

	// Source and ExternalID say where the group came from. Empty Source is a group
	// somebody made here; SourceEntra marks one mirrored out of a directory, and
	// ExternalID is then that directory's group object id — lower-cased, for the
	// reason User.DirectoryID is (ADR-draft-entra-directory-provisioning).
	Source     string `json:"source,omitempty"`
	ExternalID string `json:"externalId,omitempty"`

	// ExternalMembers is the directory's own membership: the object ids the mirror
	// last saw in this group, whether or not each of them resolves to an account
	// here. Members is derived from it — the subset that resolves — and is recomputed
	// on every synchronisation.
	//
	// Keeping the unresolved ids rather than discarding them is what makes the order
	// of a run irrelevant. A person can appear in a group's membership before their
	// own account has been read, and a change-tracking read never mentions that
	// membership again; an id kept here simply resolves on a later run, with nothing
	// to remember it by and no retry to schedule. It is also what makes a repeated
	// delivery harmless: the field is a set the message replaces or amends, not a
	// counter it advances.
	//
	// The consequence, stated because it is a real one: for a mirrored group the
	// directory decides the membership, so a member added here by hand is removed
	// again on the next run. The report says so, by name, every time it happens.
	ExternalMembers []string `json:"externalMembers,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// hasMember reports whether userID is in the group.
func (g group) hasMember(userID string) bool {
	for _, m := range g.Members {
		if m == userID {
			return true
		}
	}
	return false
}

// groupStore is a durable store for groups, one JSON file per id under a single
// directory — the same sidecar pattern as the user store (ADR-0044). It adds the
// two lookups groups need on top of the shared store: a name-uniqueness scan, and
// the reverse "which groups is this user in?" that a login snapshot asks.
type groupStore struct {
	*sidecar.Store[group]
}

// newGroupStore opens (creating if needed) the groups directory. Groups list
// oldest first, tie-broken by id so the order is deterministic.
func newGroupStore(dir string) (*groupStore, error) {
	s, err := sidecar.NewStore(dir, "groupstore",
		func(rec group) string { return rec.ID },
		sidecar.Order(func(a, b group) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &groupStore{s}, nil
}

// byName finds a group by name, case-insensitively, ignoring excludeID (so a
// rename may keep its own name). An empty name never matches. A scan is fine at
// the scale a single Atlas serves, and there is no second index to keep in step.
func (s *groupStore) byName(name, excludeID string) (group, bool, error) {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return group{}, false, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return group{}, false, err
	}
	for _, g := range all {
		if g.ID != excludeID && strings.ToLower(g.Name) == target {
			return g, true, nil
		}
	}
	return group{}, false, nil
}

// idsForUser returns the ids of every group a user belongs to. A login snapshots
// this into the session so effectiveRole can resolve a group grant as a pure
// slice check, never a store read (ADR-0180).
func (s *groupStore) idsForUser(userID string) ([]string, error) {
	if userID == "" {
		return nil, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, g := range all {
		if g.hasMember(userID) {
			ids = append(ids, g.ID)
		}
	}
	return ids, nil
}
