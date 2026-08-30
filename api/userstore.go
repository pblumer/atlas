package api

import (
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// Identity sources. A user authenticates either against a locally stored
// password (SourceLocal) or, in a future enterprise build, against an external
// identity provider (OIDC/SAML/LDAP) that maps its subject onto User.ExternalID.
// Storing the source now — rather than assuming "local" everywhere — is what lets
// external identities coexist later without a migration (ADR-0044).
const (
	SourceLocal = "local"

	// SourceOIDC marks an account an OpenID Connect provider vouches for
	// (ADR-draft-federated-authentication). Its ExternalID is that provider's
	// subject, it carries no password hash, and the pair (source, external id) is
	// what a federated login resolves by.
	SourceOIDC = "oidc"
)

// Well-known roles. Roles are a free-form list on the user, not a single "admin"
// bool, so richer RBAC can grow here without reshaping the record (ADR-0044).
//
// Four of them, and each route says which one it needs
// (ADR-0209). They are a list, not a lattice: an account
// carries several, and the question asked at the boundary is only "does this
// principal hold the role this route names". So a modeller who is also to start
// test instances holds `modeler` *and* `operator` — deliberately, because the
// alternative is a rank order in which every widening of one role silently widens
// the ones above it.
const (
	// RoleAdmin administers the instance: accounts, groups, credentials, secrets,
	// settings, backup and restore. It is the one role that is a superset — an admin
	// reaches every route, because every route a role names is one an admin may need
	// on the day the person who normally does it is unreachable.
	RoleAdmin = "admin"

	// RoleModeler authors: drafts, forms, decisions, documentation, projects and
	// applications — and deploys them. Deploying is code execution (risk R-09), which
	// is why it sits behind a role at all rather than behind being signed in.
	RoleModeler = "modeler"

	// RoleOperator runs what is deployed: start, cancel, terminate and repair
	// instances, work incidents, read runtime data.
	RoleOperator = "operator"

	// RoleUser works on tasks and reads what it is given. It is what a new account
	// gets, and on its own it reaches nothing that changes a definition or an
	// instance.
	RoleUser = "user"
)

// legacyRoles is what an identity that predates the role model holds: everything a
// signed-in account could do before there were roles, which was everything except
// what requireAdmin guarded.
//
// It is the upgrade rule in one place, used for three kinds of identity — an
// account written before this shipped, an API token minted before it, and the
// server's own internal credential. Existing installations are running work, and an
// upgrade that stops that work is one nobody applies, which protects nobody. The
// narrowing is then an operator's deliberate act on a screen
// (ADR-0209).
func legacyRoles() []string { return []string{RoleModeler, RoleOperator, RoleUser} }

// User is a person (or, later, an external identity) known to this Atlas
// instance. It is operator/config data, not engine state: it never flows through
// the WAL or the processor, so it lives in a durable sidecar store like forms and
// projects (ADR-0019/0028/0044) and touches none of the six engine invariants.
//
// The field set is deliberately chosen so the enterprise trajectory (SSO, RBAC,
// deactivation, later multi-tenancy) needs no breaking change:
//
//   - ID is a stable, opaque, never-reused primary key, decoupled from Username
//     and Email so either can change (or be reassigned by an external IdP)
//     without rewriting references to the user.
//   - Roles is a list (RBAC-ready), not a boolean flag.
//   - Source + ExternalID are the hook for external identity providers.
//   - Disabled deactivates a user for lockout/offboarding without destroying the
//     record (and the audit trail it anchors).
//   - PasswordHash is a bcrypt hash for local users and empty for external ones.
type User struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	Email        string   `json:"email,omitempty"`
	DisplayName  string   `json:"displayName,omitempty"`
	Roles        []string `json:"roles"`
	Disabled     bool     `json:"disabled,omitempty"`
	Source       string   `json:"source"`
	ExternalID   string   `json:"externalId,omitempty"`
	PasswordHash string   `json:"passwordHash,omitempty"`
	CreatedAt    int64    `json:"createdAt"`
	UpdatedAt    int64    `json:"updatedAt"`

	// RolesUpgradedAt is when this record's Roles were last written under the role
	// model (ADR-0209). Zero means the record predates it,
	// and its roles are therefore not a statement about anything except admin —
	// nothing else was enforced when they were written.
	//
	// The marker is on the record rather than instance-wide because it describes one
	// account and has to travel with it: a full snapshot carries users and settings
	// together, but a design-time backup carries neither, and an instance-wide flag
	// restored without the accounts it describes would silently skip the upgrade for
	// them. Set at creation from then on, so an account deliberately narrowed to
	// `user` is never re-widened on the next start (upgradeLegacyRoles).
	RolesUpgradedAt int64 `json:"rolesUpgradedAt,omitempty"`
}

// hasRole reports whether the user carries the given role.
func (u User) hasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// publicUser is the outward projection of a User: everything the UI and API need,
// minus the secret. PasswordHash must never leave the server, so responses are
// always built from this, never from User directly.
type publicUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Roles       []string `json:"roles"`
	Disabled    bool     `json:"disabled"`
	Source      string   `json:"source"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// toPublic strips the secret and normalizes Roles to a non-nil slice so the JSON
// is always an array, never null.
func (u User) toPublic() publicUser {
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}
	return publicUser{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Roles:       roles,
		Disabled:    u.Disabled,
		Source:      u.Source,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

// userStore is a durable store for user accounts, one JSON file per user id
// under a single directory (ADR-0044). It adds the lookups authentication needs
// on top of the shared store: a login resolves a username, an invite an email.
type userStore struct {
	*sidecar.Store[User]
}

// newUserStore opens (creating if needed) the users directory. Users list oldest
// first, tie-broken by id so the order is deterministic.
func newUserStore(dir string) (*userStore, error) {
	s, err := sidecar.NewStore(dir, "userstore",
		func(rec User) string { return rec.ID },
		sidecar.Order(func(a, b User) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &userStore{s}, nil
}

// byUsername finds a user by username, case-insensitively — usernames identify a
// human, so they must not depend on how one typed their name at the login prompt.
// A scan is fine at the scale a single Atlas serves; there is no second index to
// keep consistent.
func (s *userStore) byUsername(username string) (User, bool, error) {
	return s.findBy(username, func(u User) string { return u.Username })
}

// byEmail finds a user by email address, case-insensitively. A user without an
// email never matches, so an empty needle cannot resolve to one.
func (s *userStore) byEmail(email string) (User, bool, error) {
	return s.findBy(email, func(u User) string { return u.Email })
}

// byExternalID finds the account an identity provider's subject is linked to.
//
// The match is exact and case-sensitive, unlike the lookups above: a subject is an
// opaque identifier the provider chose, not a name a person types, and folding its
// case would be inventing an equivalence the provider never claimed. Both halves
// must match, so a subject from one provider can never resolve to an account
// created by another.
func (s *userStore) byExternalID(source, externalID string) (User, bool, error) {
	if source == "" || externalID == "" {
		return User{}, false, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return User{}, false, err
	}
	for _, u := range all {
		if u.Source == source && u.ExternalID == externalID {
			return u, true, nil
		}
	}
	return User{}, false, nil
}

// findBy is the shared scan behind the lookups: normalize the needle, and return
// the first user whose field matches it case-insensitively. An empty needle — or
// an empty field — never matches.
func (s *userStore) findBy(needle string, field func(User) string) (User, bool, error) {
	target := strings.ToLower(strings.TrimSpace(needle))
	if target == "" {
		return User{}, false, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return User{}, false, err
	}
	for _, u := range all {
		if v := field(u); v != "" && strings.ToLower(v) == target {
			return u, true, nil
		}
	}
	return User{}, false, nil
}

// count reports how many users exist. The first-run bootstrap asks this to decide
// whether anyone can still claim the initial admin account (ADR-0044).
func (s *userStore) count() (int, error) {
	all, err := s.LoadAll()
	if err != nil {
		return 0, err
	}
	return len(all), nil
}
