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
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
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
