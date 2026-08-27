package api

import (
	"fmt"
	"path/filepath"
	"strings"
)

// This file backs the `atlas reset-password` command: an operator-level password
// reset that runs directly against the on-disk user store, without a running
// server or an authenticated session.
//
// It exists because password recovery on a self-hosted, admin-managed instance
// is deliberately an operator action, not a self-service flow (ADR-0044): there
// is no transactional mail sender, email is optional per user, and the HTTP
// set-password endpoint requires an already-authenticated admin — useless to the
// locked-out admin who needs it most. MCP is no escape hatch either: it is gated
// like the rest of the API and a tool call carries its caller's own credential, so
// the locked-out admin has nothing to present there either
// (ADR-draft-authenticated-mcp-transport). So recovery lives here, reachable from
// a shell (e.g. a one-line `docker exec` against the data volume).

// ResetPasswordOptions configures a single operator-driven password reset.
type ResetPasswordOptions struct {
	// DataDir is the server's data directory (the same --data-dir the server
	// runs with). The user store lives in its "users" subdirectory.
	DataDir string
	// Username identifies the account to reset, matched case-insensitively.
	Username string
	// NewPassword is the replacement password. It must meet the same minimum
	// length the API enforces.
	NewPassword string
	// CreateAdmin, when set, creates a fresh enabled admin with Username if no
	// such user exists yet — a bootstrap escape hatch for an instance that has
	// users but no reachable admin. It never duplicates or re-roles an account
	// that already exists.
	CreateAdmin bool
	// Now is the unix-seconds timestamp stamped into the record, matching the
	// server's own UpdatedAt/CreatedAt unit. Injected so the operation is
	// deterministic and testable.
	Now int64
}

// ResetPasswordResult reports what a reset did.
type ResetPasswordResult struct {
	UserID   string
	Username string
	// Created is true when a new admin account was created (CreateAdmin), false
	// when an existing account's password was replaced.
	Created bool
}

// ResetPassword sets a new local password for a user in the store under
// opts.DataDir. It replaces only the password (and UpdatedAt); roles, email, and
// the Disabled flag are left as they are, so a reset re-grants access without
// silently changing what the account can do. With CreateAdmin and no matching
// user, it instead creates a fresh enabled admin.
//
// It touches the same files the running server uses. Login reads the store from
// disk on every attempt, so a reset takes effect on the next sign-in without a
// restart; running it against a live server is safe (writes are atomic), though
// stopping the server first is never wrong.
func ResetPassword(opts ResetPasswordOptions) (ResetPasswordResult, error) {
	username := strings.TrimSpace(opts.Username)
	if username == "" {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: username is required")
	}
	if len(opts.NewPassword) < minPasswordLen {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: password must be at least %d characters", minPasswordLen)
	}
	hash, err := hashPassword(opts.NewPassword)
	if err != nil {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: %w", err)
	}

	store, err := newUserStore(filepath.Join(opts.DataDir, "users"))
	if err != nil {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: open user store: %w", err)
	}

	u, ok, err := store.byUsername(username)
	if err != nil {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: look up %q: %w", username, err)
	}

	if !ok {
		if !opts.CreateAdmin {
			return ResetPasswordResult{}, fmt.Errorf("reset-password: no user named %q (pass --create-admin to create an admin with this name)", username)
		}
		id, err := newUserID()
		if err != nil {
			return ResetPasswordResult{}, fmt.Errorf("reset-password: %w", err)
		}
		u = User{
			ID:           id,
			Username:     username,
			Roles:        []string{RoleAdmin},
			Source:       SourceLocal,
			PasswordHash: hash,
			CreatedAt:    opts.Now,
			UpdatedAt:    opts.Now,
		}
		if err := store.Save(u); err != nil {
			return ResetPasswordResult{}, fmt.Errorf("reset-password: save: %w", err)
		}
		return ResetPasswordResult{UserID: u.ID, Username: u.Username, Created: true}, nil
	}

	u.PasswordHash = hash
	u.UpdatedAt = opts.Now
	if err := store.Save(u); err != nil {
		return ResetPasswordResult{}, fmt.Errorf("reset-password: save: %w", err)
	}
	return ResetPasswordResult{UserID: u.ID, Username: u.Username, Created: false}, nil
}
