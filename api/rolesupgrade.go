package api

import (
	"log/slog"

	"github.com/pblumer/atlas/logging"
)

// The upgrade half of the role model (ADR-0209).
//
// Before it, an account's roles said one thing: admin, or not. Every other route
// was reachable by anyone signed in — deploying a model included. So a record
// written back then carries no statement about authoring or operating, and reading
// it as one would take away, on a restart nobody asked for, what every account on
// every running installation could do the day before.
//
// Hence: an account that predates the model keeps exactly what it had, spelled out
// as the three roles that say it, and narrowing is then an operator's deliberate
// act on a screen. That is the opposite of what ADR-0205 chose for ownerless
// connectors, and the difference is the point — there the old behaviour was a hole
// that a grandfathering rule would have left open, here it is a documented,
// accepted risk (R-04) on installations that are running work.

// withLegacyRoles returns roles plus every legacy role it does not already carry,
// preserving what is there — an admin stays an admin, and a role somebody invented
// stays too, because this upgrade takes nothing away.
func withLegacyRoles(roles []string) []string {
	out := append([]string(nil), roles...)
	for _, want := range legacyRoles() {
		held := false
		for _, have := range out {
			if have == want {
				held = true
				break
			}
		}
		if !held {
			out = append(out, want)
		}
	}
	return out
}

// upgradeLegacyRoles gives every account written before the role model the roles
// that describe what it could already do, and marks it so this never runs on it
// again.
//
// It runs on the constructing goroutine before the run loop serves traffic — the
// same discipline as bootstrapAdmin — so it touches the stores directly rather
// than through s.do, which nothing is servicing yet.
func (s *Server) upgradeLegacyRoles(now int64) error {
	users, err := s.users.LoadAll()
	if err != nil {
		return err
	}
	upgraded := map[string][]string{}
	for _, u := range users {
		if u.RolesUpgradedAt != 0 {
			continue
		}
		u.Roles = withLegacyRoles(u.Roles)
		u.RolesUpgradedAt = now
		if err := s.users.Save(u); err != nil {
			return err
		}
		upgraded[u.ID] = u.Roles
	}
	if len(upgraded) == 0 {
		return nil
	}
	// A standing OAuth approval carries its own snapshot of the person's roles
	// (ADR-0200), and the maintenance that keeps it honest is exactly this: a role
	// change rewrites it. Skipping that would leave somebody's connector able to do
	// less than they can, for a change they never made.
	if err := s.refreshGrantRoles(upgraded); err != nil {
		return err
	}
	logging.Info(logging.AuthRolesUpgraded,
		"accounts written before roles were enforced keep what they could already do: "+
			"modeler, operator and user; narrow them under Organization",
		slog.Int("accounts", len(upgraded)))
	return nil
}

// refreshGrantRoles rewrites the role snapshot on the grants of the given users.
// Same goroutine and same reason as upgradeLegacyRoles: it cannot use
// rewriteAllGrants, which runs inside a run-loop turn.
func (s *Server) refreshGrantRoles(roles map[string][]string) error {
	grants, err := s.oauthGrantStore.LoadAll()
	if err != nil {
		return err
	}
	for _, g := range grants {
		next, ok := roles[g.UserID]
		if !ok {
			continue
		}
		g.Roles = append([]string(nil), next...)
		if err := s.oauthGrantStore.Save(g); err != nil {
			return err
		}
		s.oauthGrants.put(g)
	}
	return nil
}
