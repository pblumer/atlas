package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// What a provider's claims are allowed to decide here
// (ADR-0210, step two).
//
// Step one made a federated login possible and deliberately let it grant nothing:
// a first login produced an account with `user` and an administrator granted the
// rest by hand. That is the safe half of federation and not the useful one. The
// useful half is this: a person's groups at the provider decide what they may do
// here, so onboarding is a group membership somebody already maintains and
// offboarding is the same membership going away.
//
// It is off until an operator turns it on, and the record is explicit about why
// that switch is a real decision rather than a formality: from the day it is on,
// whoever administers the provider's groups administers this instance's roles.
//
// Two properties keep it from being a trapdoor:
//
//   - **A claim grants; it never grants by absence.** Every rule is an exact match
//     on a value the token carries. A person the provider says nothing about
//     matches nothing and is granted nothing.
//   - **`user` is a floor, not a grant.** What the mapping decides is `admin`,
//     `modeler` and `operator` — the roles that change what somebody may do to the
//     instance. Anybody who can sign in at all keeps `user`, so a group that goes
//     away does not leave a person unable to open their own task list.
//   - **It owns the groups it names, and no others.** Roles are a closed set of
//     four that Atlas defines, so "the mapping decides the roles" is a complete
//     sentence and a hand-granted role does not survive the next login. Groups are
//     an open set that people create for their own reasons, and a mapping that
//     never mentions a group has said nothing about it — so a membership an
//     administrator added by hand to a group no rule names is left alone.

// oidcMapRule is one claim value and what holding it grants.
type oidcMapRule struct {
	// Value is matched against the claim exactly. Providers write group names,
	// object ids and role names here; Atlas compares, it does not interpret.
	Value string `json:"value"`

	// Roles the value grants, from the four Atlas enforces (ADR-0209).
	Roles []string `json:"roles,omitempty"`

	// Groups the value puts the person in, by Atlas group id (ADR-0180). Ids rather
	// than names because a group can be renamed and the mapping should survive it.
	Groups []string `json:"groups,omitempty"`
}

// oidcMapping is the whole policy: which claim to read, and what its values grant.
type oidcMapping struct {
	// Enabled is the switch. Off — the default, and what an installation upgrading
	// into this has — means claims grant nothing and roles stay whatever they are in
	// Atlas.
	Enabled bool `json:"enabled"`

	// Claim is where to look in the token: a claim name, or a dotted path into a
	// nested object for the providers that put it there (`realm_access.roles`).
	Claim string `json:"claim"`

	// Rules are matched in order; everything that matches contributes.
	Rules []oidcMapRule `json:"rules"`
}

// claimValues reads a claim out of a decoded token as a list of strings.
//
// A claim is a string when there is one value and a list when there are several,
// and neither form is more correct — so both are read, and anything else reads as
// nothing. "Anything else" includes a list of numbers and an object: a mapping that
// coerced those into strings would match on text no operator wrote.
func claimValues(raw map[string]any, path string) []string {
	if len(raw) == 0 || strings.TrimSpace(path) == "" {
		return nil
	}
	var node any = raw
	for _, part := range strings.Split(path, ".") {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node, ok = obj[part]
		if !ok {
			return nil
		}
	}
	switch v := node.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || s == "" {
				continue
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

// namedGroups is every group any rule mentions: the groups this mapping owns, and
// therefore the only ones a login may remove somebody from. See the file comment.
func (m oidcMapping) namedGroups() []string {
	seen := map[string]bool{}
	var out []string
	for _, rule := range m.Rules {
		for _, g := range rule.Groups {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// apply turns the claim's values into the roles and groups they grant, in the
// order the rules are written and without repeats.
func (m oidcMapping) apply(values []string) (roles, groups []string) {
	held := map[string]bool{}
	for _, v := range values {
		held[v] = true
	}
	seenRole, seenGroup := map[string]bool{}, map[string]bool{}
	for _, rule := range m.Rules {
		if !held[rule.Value] {
			continue
		}
		for _, role := range rule.Roles {
			if !seenRole[role] {
				seenRole[role] = true
				roles = append(roles, role)
			}
		}
		for _, g := range rule.Groups {
			if !seenGroup[g] {
				seenGroup[g] = true
				groups = append(groups, g)
			}
		}
	}
	return roles, groups
}

// validate reports why a mapping could not do what it says, given a way to ask
// whether a group exists.
//
// Everything here fails at write time rather than at login time, and that is the
// whole point of the function: a rule naming a role Atlas does not enforce, or a
// group that was deleted, grants nothing — silently, on every login, until
// somebody works out why the new colleague cannot deploy. A disabled mapping is
// not checked, so an operator can write one against groups they are about to
// create.
func (m oidcMapping) validate(groupExists func(id string) bool) error {
	if !m.Enabled {
		return nil
	}
	if strings.TrimSpace(m.Claim) == "" {
		return fmt.Errorf("a mapping that is on needs a claim to read")
	}
	if len(m.Rules) == 0 {
		return fmt.Errorf("a mapping that is on needs at least one rule")
	}
	for i, rule := range m.Rules {
		if strings.TrimSpace(rule.Value) == "" {
			return fmt.Errorf("rule %d has no claim value to match", i+1)
		}
		if len(rule.Roles) == 0 && len(rule.Groups) == 0 {
			return fmt.Errorf("rule %d (%q) grants nothing", i+1, rule.Value)
		}
		for _, role := range rule.Roles {
			if !isGrantableRole(role) {
				return fmt.Errorf("rule %d (%q) names role %q, which Atlas does not enforce",
					i+1, rule.Value, role)
			}
		}
		for _, g := range rule.Groups {
			if !groupExists(g) {
				return fmt.Errorf("rule %d (%q) names group %q, which does not exist",
					i+1, rule.Value, g)
			}
		}
	}
	return nil
}

// withUserFloor is the mapped roles plus `user`, which every account that can sign
// in holds. See the file comment for why that one is a floor rather than something
// the provider grants.
func withUserFloor(mapped []string) []string {
	out := append([]string(nil), mapped...)
	for _, r := range out {
		if r == RoleUser {
			return out
		}
	}
	return append(out, RoleUser)
}

// maxOIDCMappingBytes bounds the mapping body. A mapping is a handful of rules; a
// body larger than this is a mistake or somebody testing what the endpoint does
// with a large one.
const maxOIDCMappingBytes = 1 << 16

// handleGetOIDCMapping returns the stored claim mapping (admin-only).
//
// Admin-only in both directions, unlike the theme or the registration link: the
// rules name the provider's group identifiers, which say something about how the
// organisation is arranged, and nothing on the login screen needs them.
func (s *Server) handleGetOIDCMapping(w http.ResponseWriter, _ *http.Request) {
	var (
		m   oidcMapping
		err error
	)
	s.do(func() { m, err = s.settings.getOIDCMapping() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read oidc mapping: "+err.Error())
		return
	}
	if m.Rules == nil {
		m.Rules = []oidcMapRule{}
	}
	httpapi.JSON(w, http.StatusOK, m)
}

// handleSetOIDCMapping stores the claim mapping (admin-only).
//
// It refuses a mapping that could not do what it says — a role Atlas does not
// enforce, a group that does not exist — because the alternative is a rule that
// grants nothing on every login until somebody works out why. The check runs
// against the groups as they are at this moment, inside the same run-loop turn
// that writes the record.
func (s *Server) handleSetOIDCMapping(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxOIDCMappingBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var m oidcMapping
	if err := json.Unmarshal(body, &m); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	m.Claim = strings.TrimSpace(m.Claim)
	if m.Rules == nil {
		m.Rules = []oidcMapRule{}
	}
	var (
		invalid  error
		storeErr error
	)
	s.do(func() {
		groups, e := s.groups.LoadAll()
		if e != nil {
			storeErr = fmt.Errorf("read groups: %w", e)
			return
		}
		exists := func(id string) bool {
			for _, g := range groups {
				if g.ID == id {
					return true
				}
			}
			return false
		}
		if invalid = m.validate(exists); invalid != nil {
			return
		}
		storeErr = s.settings.saveOIDCMapping(m)
	})
	switch {
	case invalid != nil:
		httpapi.Error(w, http.StatusBadRequest, invalid.Error())
	case storeErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "store oidc mapping: "+storeErr.Error())
	default:
		// Worth a line of its own: from the moment this is on, whoever administers the
		// provider's groups administers this instance's roles, and an audit that could
		// not say when that started would be missing the change that explains every
		// role change after it.
		audit(r, logging.AuthOIDCMappingSet, "oidc claim mapping saved",
			slog.Bool("enabled", m.Enabled), slog.String("claim", m.Claim),
			slog.Int("rules", len(m.Rules)))
		httpapi.JSON(w, http.StatusOK, m)
	}
}
