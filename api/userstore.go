package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
)

// Well-known roles. Roles are a free-form list on the user, not a single "admin"
// bool, so richer RBAC can grow here without reshaping the record (ADR-0044). The
// MVP only enforces RoleAdmin (managing users requires it); every other role is
// stored and returned but not yet consulted.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

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

// userStore is a durable store for users, one JSON file per user id under a
// single directory. Like the form store (ADR-0028) it reuses the on-disk sidecar
// approach of the deployment store (ADR-0019) and is owned solely by the server's
// run-loop goroutine, so it needs no locking of its own.
type userStore struct {
	dir string
}

// newUserStore opens (creating if needed) the users directory.
func newUserStore(dir string) (*userStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("userstore: create dir: %w", err)
	}
	return &userStore{dir: dir}, nil
}

// fileFor maps a user id to its record path. The id is hex-encoded so any id
// yields a safe, unique, deterministic filename; the real id is also stored
// inside the record.
func (s *userStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a user durably (atomic write + directory fsync), overwriting any
// existing user with the same id.
func (s *userStore) save(rec User) error {
	return sidecar.WriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

// get returns the user for an id, or ok=false if none is stored.
func (s *userStore) get(id string) (User, bool, error) {
	data, err := os.ReadFile(s.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return User{}, false, nil
		}
		return User{}, false, fmt.Errorf("userstore: read: %w", err)
	}
	var rec User
	if err := json.Unmarshal(data, &rec); err != nil {
		return User{}, false, fmt.Errorf("userstore: decode: %w", err)
	}
	return rec, true, nil
}

// byUsername finds a user by username, case-insensitively (login handles are not
// case-sensitive). It scans loadAll — fine for the modest user counts a single
// Atlas instance holds; an index can replace it if that ever changes.
func (s *userStore) byUsername(username string) (User, bool, error) {
	target := strings.ToLower(strings.TrimSpace(username))
	if target == "" {
		return User{}, false, nil
	}
	all, err := s.loadAll()
	if err != nil {
		return User{}, false, err
	}
	for _, u := range all {
		if strings.ToLower(u.Username) == target {
			return u, true, nil
		}
	}
	return User{}, false, nil
}

// byEmail finds a user by email, case-insensitively. An empty email never
// matches, so users without an email can't be found by "".
func (s *userStore) byEmail(email string) (User, bool, error) {
	target := strings.ToLower(strings.TrimSpace(email))
	if target == "" {
		return User{}, false, nil
	}
	all, err := s.loadAll()
	if err != nil {
		return User{}, false, err
	}
	for _, u := range all {
		if u.Email != "" && strings.ToLower(u.Email) == target {
			return u, true, nil
		}
	}
	return User{}, false, nil
}

// delete removes a user. A missing user is not an error (idempotent cleanup).
func (s *userStore) delete(id string) error {
	if err := os.Remove(s.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("userstore: remove: %w", err)
	}
	return sidecar.FsyncDir(s.dir)
}

// count returns the number of stored users. Bootstrap uses it to decide whether a
// fresh instance needs a seed admin.
func (s *userStore) count() (int, error) {
	all, err := s.loadAll()
	if err != nil {
		return 0, err
	}
	return len(all), nil
}

// loadAll reads every user, oldest first (stable CreatedAt order), so listings
// read like an account roster rather than filesystem order. Files that are not
// user records are ignored.
func (s *userStore) loadAll() ([]User, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("userstore: read dir: %w", err)
	}
	var out []User
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue // not a hex-named record
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("userstore: read %s: %w", e.Name(), err)
		}
		var rec User
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("userstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
