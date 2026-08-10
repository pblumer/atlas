package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file is the HTTP surface for identity (ADR-0044): the auth endpoints
// (login, logout, me) and the user-management CRUD. Every store access funnels
// through s.do onto the run-loop goroutine, the same discipline the other sidecar
// handlers follow, so the user store is only ever touched by a single owner.

// maxUserBytes caps a user request body. User records are tiny; this refuses a
// runaway upload without constraining any real request.
const maxUserBytes = 64 << 10 // 64 KiB

// decodeJSONBody reads a size-limited JSON body into dst, writing a 400 and
// returning false on a read or parse error. Centralizing it keeps every identity
// handler's body handling identical and in one place.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUserBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// normalizeRoles trims, drops empties, and de-duplicates a role list, preserving
// order. An empty result defaults to the base user role, so every account has at
// least one role.
func normalizeRoles(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{RoleUser}
	}
	return out
}

// enabledAdminCount counts enabled admins in all, excluding excludeID (the user
// being changed). It backs the lockout guard: an operation that would drop this
// to zero — deleting, disabling, or de-admining the last admin — is refused so an
// instance can never lock every operator out.
func enabledAdminCount(all []User, excludeID string) int {
	n := 0
	for _, u := range all {
		if u.ID == excludeID || u.Disabled {
			continue
		}
		if u.hasRole(RoleAdmin) {
			n++
		}
	}
	return n
}

// handleLogin verifies a username/password and opens a session, setting the
// session cookie. The failure response is uniform ("invalid credentials") whether
// the user is unknown, disabled, or the password is wrong, so it does not leak
// which usernames exist.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	username := strings.TrimSpace(payload.Username)
	if username == "" || payload.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	var (
		u       User
		ok      bool
		lookErr error
	)
	s.do(func() { u, ok, lookErr = s.users.byUsername(username) })
	if lookErr != nil {
		writeError(w, http.StatusInternalServerError, "login: "+lookErr.Error())
		return
	}
	if !ok || u.Disabled || !checkPassword(u.PasswordHash, payload.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := s.sessions.create(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login: "+err.Error())
		return
	}
	setSessionCookie(w, r, token, s.sessions.ttl)
	writeJSON(w, http.StatusOK, u.toPublic())
}

// handleLogout ends the caller's session and clears the cookie. It is idempotent:
// logging out without a session still succeeds.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.destroy(c.Value)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe reports whether auth is enforced and who, if anyone, is logged in. The
// UI reads it on load to decide between the login screen and the app. When auth
// is enabled the route is gated, so a nil principal only occurs with auth off (or
// a mid-session deletion), reported as {authEnabled, user:null}.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": nil})
		return
	}
	var (
		u       User
		ok      bool
		loadErr error
	)
	s.do(func() { u, ok, loadErr = s.users.get(p.UserID) })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "me: "+loadErr.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authEnabled": s.authEnabled, "user": u.toPublic()})
}

// handleChangePassword lets the signed-in user change their own password. Body:
// {"currentPassword":"...","newPassword":"..."}. Unlike handleSetUserPassword
// (admin-only, any user) this is self-service — no admin role — but it verifies
// the caller's current password first, so a merely-open session cannot silently
// take over the account. It enforces the same minimum length as everywhere else
// and touches only the caller's own record. Sessions are token-based and hold no
// password material, so the current session stays valid after the change.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var payload struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	if len(payload.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := hashPassword(payload.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "change password: "+err.Error())
		return
	}
	var (
		found      bool
		badCurrent bool
		opErr      error
	)
	s.do(func() {
		u, ok, e := s.users.get(p.UserID)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		// checkPassword fails on an empty hash too, so an external-identity user
		// with no local password is simply rejected here — no special case.
		if !checkPassword(u.PasswordHash, payload.CurrentPassword) {
			badCurrent = true
			return
		}
		u.PasswordHash = hash
		u.UpdatedAt = time.Now().Unix()
		opErr = s.users.save(u)
	})
	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "change password: "+opErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no such user")
	case badCurrent:
		writeError(w, http.StatusForbidden, "current password is incorrect")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// handleListUsers returns every account (public projection), oldest first.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list := []publicUser{}
	var loadErr error
	s.do(func() {
		var recs []User
		recs, loadErr = s.users.loadAll()
		for _, u := range recs {
			list = append(list, u.toPublic())
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list users: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleListAssignableUsers returns the accounts a task can be assigned to — a
// minimal projection (username + display name) of every enabled user. Unlike the
// admin-gated management list, it is available to any authenticated caller (and
// open when auth is off): assigning work is an everyday Tasks action, not user
// administration (ADR-0045).
func (s *Server) handleListAssignableUsers(w http.ResponseWriter, _ *http.Request) {
	type assignable struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName,omitempty"`
	}
	list := []assignable{}
	var loadErr error
	s.do(func() {
		var recs []User
		recs, loadErr = s.users.loadAll()
		for _, u := range recs {
			if u.Disabled {
				continue
			}
			list = append(list, assignable{Username: u.Username, DisplayName: u.DisplayName})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list assignable users: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCreateUser creates a local user. Body:
// {"username","email","displayName","password","roles":[...]}. Username is
// required and unique (case-insensitive); email, if given, is unique too; the
// password must meet the minimum length. Roles default to ["user"].
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var payload struct {
		Username    string   `json:"username"`
		Email       string   `json:"email"`
		DisplayName string   `json:"displayName"`
		Password    string   `json:"password"`
		Roles       []string `json:"roles"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	username := strings.TrimSpace(payload.Username)
	email := strings.TrimSpace(payload.Email)
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if len(payload.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := hashPassword(payload.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create user: "+err.Error())
		return
	}
	id, err := newUserID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create user: "+err.Error())
		return
	}
	now := time.Now().Unix()
	rec := User{
		ID:           id,
		Username:     username,
		Email:        email,
		DisplayName:  strings.TrimSpace(payload.DisplayName),
		Roles:        normalizeRoles(payload.Roles),
		Source:       SourceLocal,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	// The uniqueness check and the write happen in one run-loop turn, so no
	// concurrent create can slip a duplicate username/email between them.
	var (
		conflict string
		saveErr  error
	)
	s.do(func() {
		if _, ok, e := s.users.byUsername(username); e != nil {
			saveErr = e
			return
		} else if ok {
			conflict = "username already taken"
			return
		}
		if email != "" {
			if _, ok, e := s.users.byEmail(email); e != nil {
				saveErr = e
				return
			} else if ok {
				conflict = "email already taken"
				return
			}
		}
		saveErr = s.users.save(rec)
	})
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "create user: "+saveErr.Error())
		return
	}
	if conflict != "" {
		writeError(w, http.StatusConflict, conflict)
		return
	}
	writeJSON(w, http.StatusCreated, rec.toPublic())
}

// handleGetUser returns one account by id, or 404.
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var (
		u       User
		ok      bool
		loadErr error
	)
	s.do(func() { u, ok, loadErr = s.users.get(id) })
	switch {
	case loadErr != nil:
		writeError(w, http.StatusInternalServerError, "read user: "+loadErr.Error())
	case !ok:
		writeError(w, http.StatusNotFound, "no user with that id")
	default:
		writeJSON(w, http.StatusOK, u.toPublic())
	}
}

// handlePatchUser updates mutable fields of a user. Absent fields are left
// unchanged (pointers distinguish "omitted" from "set to empty"). Username and
// source are immutable here; the password has its own endpoint. Changes that
// would remove the last enabled admin are refused (409) so no instance locks
// itself out; disabling a user also ends their live sessions.
func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var payload struct {
		Email       *string   `json:"email"`
		DisplayName *string   `json:"displayName"`
		Roles       *[]string `json:"roles"`
		Disabled    *bool     `json:"disabled"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	var (
		updated   User
		found     bool
		conflict  string
		lockout   bool
		opErr     error
		disabling bool
	)
	s.do(func() {
		u, ok, e := s.users.get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.Email != nil {
			email := strings.TrimSpace(*payload.Email)
			if email != "" {
				if other, ok, e := s.users.byEmail(email); e != nil {
					opErr = e
					return
				} else if ok && other.ID != id {
					conflict = "email already taken"
					return
				}
			}
			u.Email = email
		}
		if payload.DisplayName != nil {
			u.DisplayName = strings.TrimSpace(*payload.DisplayName)
		}
		if payload.Roles != nil {
			u.Roles = normalizeRoles(*payload.Roles)
		}
		if payload.Disabled != nil {
			u.Disabled = *payload.Disabled
		}
		// Guard against locking everyone out: if the result is not an enabled admin
		// and no other enabled admin remains, refuse.
		if !(u.hasRole(RoleAdmin) && !u.Disabled) {
			all, e := s.users.loadAll()
			if e != nil {
				opErr = e
				return
			}
			if enabledAdminCount(all, id) == 0 {
				// Only a problem if this user *was* the last enabled admin.
				if prev, _, _ := s.users.get(id); prev.hasRole(RoleAdmin) && !prev.Disabled {
					lockout = true
					return
				}
			}
		}
		u.UpdatedAt = time.Now().Unix()
		if opErr = s.users.save(u); opErr != nil {
			return
		}
		updated = u
		disabling = payload.Disabled != nil && *payload.Disabled
	})
	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "update user: "+opErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no user with that id")
	case conflict != "":
		writeError(w, http.StatusConflict, conflict)
	case lockout:
		writeError(w, http.StatusConflict, "cannot remove the last enabled admin")
	default:
		if disabling {
			s.sessions.destroyUser(id)
		}
		writeJSON(w, http.StatusOK, updated.toPublic())
	}
}

// handleSetUserPassword replaces a user's password. Body: {"password":"..."}.
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var payload struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	if len(payload.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := hashPassword(payload.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "set password: "+err.Error())
		return
	}
	var (
		found bool
		opErr error
	)
	s.do(func() {
		u, ok, e := s.users.get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		u.PasswordHash = hash
		u.UpdatedAt = time.Now().Unix()
		opErr = s.users.save(u)
	})
	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "set password: "+opErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no user with that id")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	}
}

// handleDeleteUser removes a user. Deleting the last enabled admin is refused
// (409) so an instance can't lock itself out; a successful delete also ends the
// user's live sessions.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	var (
		found   bool
		lockout bool
		opErr   error
	)
	s.do(func() {
		u, ok, e := s.users.get(id)
		if e != nil {
			opErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if u.hasRole(RoleAdmin) && !u.Disabled {
			all, e := s.users.loadAll()
			if e != nil {
				opErr = e
				return
			}
			if enabledAdminCount(all, id) == 0 {
				lockout = true
				return
			}
		}
		opErr = s.users.delete(id)
	})
	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "delete user: "+opErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no user with that id")
	case lockout:
		writeError(w, http.StatusConflict, "cannot delete the last enabled admin")
	default:
		s.sessions.destroyUser(id)
		w.WriteHeader(http.StatusNoContent)
	}
}
