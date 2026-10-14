package api

import (
	"strings"

	"github.com/pblumer/atlas/api/catalog"
)

// Resolving an approval rule's reference, the way the task surface resolves it.
//
// The catalogue package owns the report and knows nothing about accounts or
// groups; this is the half that does. It exists as its own type so that the two
// matching rules are written once, beside the stores they read, and can be held
// against [Server.holdsTask] — which is what actually decides, at the moment an
// approval lands, whether the person reading it is allowed to.

// approverLookup answers the catalogue's two questions from the user and group
// stores. It reads them on the run loop, like everything else that touches a
// sidecar store, so the report is taken from one consistent moment.
type approverLookup struct{ s *Server }

// Account matches an assignee: a username, ignoring case. A disabled account is
// not an approver — it cannot sign in, so an approval addressed to it is an
// approval nobody can decide, which is exactly what this report is for.
func (l approverLookup) Account(username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	found := false
	l.s.do(func() {
		users, err := l.s.users.LoadAll()
		if err != nil {
			return
		}
		for _, u := range users {
			if !u.Disabled && strings.EqualFold(u.Username, username) {
				found = true
				return
			}
		}
	})
	return found
}

// Group matches a candidate group: an id first and a name after, both ignoring
// case. Both are accepted because both work — holdsTask tries ids across every
// group the caller carries before it reads a single record to compare names — and
// a report stricter than the thing it reports on would name working rules as
// broken.
func (l approverLookup) Group(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	found := false
	l.s.do(func() {
		groups, err := l.s.groups.LoadAll()
		if err != nil {
			return
		}
		for _, g := range groups {
			if strings.EqualFold(g.ID, ref) || strings.EqualFold(g.Name, ref) {
				found = true
				return
			}
		}
	})
	return found
}

// compile-time proof that this answers what the catalogue asks.
var _ catalog.ApproverLookup = approverLookup{}
