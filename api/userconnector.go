package api

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// This file implements the user-provisioning connector worker (ADR-0123): the
// in-process side of an <atlas:userConnector> task. It create/set-password/disables
// an Atlas login through the internal user store.
//
// It deliberately, and narrowly, reopens the ADR-0044/0049 boundary that no
// automated identity may manage users — but only for the protected system project
// (ADR-0122), only for these three bounded operations, and only when an operator
// opts in (WithUserProvisioning). It reuses the same store rails the admin API uses
// (password length, uniqueness, the last-enabled-admin lockout guard), so the
// connector can never provision a weaker or more dangerous account than an admin
// could by hand.
//
// Execution model: the single-binary server drives jobs synchronously on the
// run-loop goroutine, so this handler runs ON the loop and mutates the
// run-loop-owned user store directly — never through s.do, which would deadlock
// (server.go loop/do).

// userConnectorHandler builds the job handler for user-provisioning connector tasks.
// Register it for the reserved compiler.UserConnectorJobTypeIndex only when
// provisioning is enabled.
func (s *Server) userConnectorHandler() job.Handler {
	return func(j job.Job) error {
		ei, ok, err := s.store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return err
		}
		if !ok {
			return nil // element instance gone; nothing to do
		}
		cp := s.processLookup(ei.ProcessDefKey)
		if cp == nil {
			return fmt.Errorf("user connector: no compiled process for def %d", ei.ProcessDefKey)
		}
		// Gate: only the protected system project's processes may provision users
		// (ADR-0122/0123). A tenant model that declared the connector fails here.
		if !s.systemPIDs[cp.ProcessId()] {
			return fmt.Errorf("user connector: process %q is not a system process; user provisioning is restricted to the system project", cp.ProcessId())
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return fmt.Errorf("user connector: %w", err)
		}
		scope := ei.ProcessInstanceKey
		scopeVars, err := userReadScopeVars(s.store, scope)
		if err != nil {
			return fmt.Errorf("user connector: read variables: %w", err)
		}
		op := cp.Intern(detail.UserOp)
		username := strings.TrimSpace(userResolve(detail.UserName, scope, scopeVars))
		if username == "" {
			return fmt.Errorf("user connector: resolved an empty username")
		}
		now := time.Now().Unix()
		switch op {
		case "create":
			email := strings.TrimSpace(userResolve(detail.UserEmail, scope, scopeVars))
			displayName := strings.TrimSpace(userResolve(detail.UserDisplayName, scope, scopeVars))
			roles := splitRoles(userResolve(detail.UserRoles, scope, scopeVars))
			password := userResolve(detail.UserPassword, scope, scopeVars)
			id, created, err := s.provisionCreateUser(username, email, displayName, roles, password, now)
			if err != nil {
				return err
			}
			log.Printf("user provisioning: instance %d create user %q (id=%s created=%v)", scope, username, id, created)
			return nil
		case "set-password":
			if err := s.provisionSetPassword(username, userResolve(detail.UserPassword, scope, scopeVars), now); err != nil {
				return err
			}
			log.Printf("user provisioning: instance %d set password for %q", scope, username)
			return nil
		case "disable":
			if err := s.provisionDisableUser(username, now); err != nil {
				return err
			}
			log.Printf("user provisioning: instance %d disable user %q", scope, username)
			return nil
		default:
			return fmt.Errorf("user connector: unknown operation %q", op)
		}
	}
}

// provisionCreateUser creates a local user, idempotently: an existing username is
// treated as success (the account is already provisioned) so an at-least-once retry
// never double-creates. It reuses the admin API's rails — password length and
// username/email uniqueness — so a provisioned account is exactly what an admin
// could create. Runs on the run-loop goroutine.
func (s *Server) provisionCreateUser(username, email, displayName string, roles []string, password string, now int64) (string, bool, error) {
	if existing, ok, err := s.users.byUsername(username); err != nil {
		return "", false, err
	} else if ok {
		return existing.ID, false, nil // idempotent: already provisioned
	}
	if len(password) < minPasswordLen {
		return "", false, fmt.Errorf("user connector: password must be at least %d characters", minPasswordLen)
	}
	if email != "" {
		if _, ok, err := s.users.byEmail(email); err != nil {
			return "", false, err
		} else if ok {
			return "", false, fmt.Errorf("user connector: email %q already taken", email)
		}
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", false, err
	}
	id, err := newUserID()
	if err != nil {
		return "", false, err
	}
	rec := User{
		ID:           id,
		Username:     username,
		Email:        email,
		DisplayName:  displayName,
		Roles:        normalizeRoles(roles),
		Source:       SourceLocal,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.users.save(rec); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// provisionSetPassword replaces an existing user's password (the set-password
// operation). The user must exist; the minimum length applies.
func (s *Server) provisionSetPassword(username, password string, now int64) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("user connector: password must be at least %d characters", minPasswordLen)
	}
	u, ok, err := s.users.byUsername(username)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("user connector: no user named %q", username)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	u.UpdatedAt = now
	return s.users.save(u)
}

// provisionDisableUser disables a user, idempotently (already-disabled is success).
// It keeps the ADR-0044 lockout guard — disabling the last enabled admin is refused —
// and ends the user's live sessions on success.
func (s *Server) provisionDisableUser(username string, now int64) error {
	u, ok, err := s.users.byUsername(username)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("user connector: no user named %q", username)
	}
	if u.Disabled {
		return nil // idempotent
	}
	if u.hasRole(RoleAdmin) {
		all, err := s.users.loadAll()
		if err != nil {
			return err
		}
		if enabledAdminCount(all, u.ID) == 0 {
			return fmt.Errorf("user connector: cannot disable the last enabled admin")
		}
	}
	u.Disabled = true
	u.UpdatedAt = now
	if err := s.users.save(u); err != nil {
		return err
	}
	s.sessions.destroyUser(u.ID)
	return nil
}

// splitRoles turns a comma-separated roles field into a trimmed list, dropping empty
// entries (normalizeRoles later de-dupes and defaults to the base user role).
func splitRoles(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if r := strings.TrimSpace(p); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// userBuiltinProcessInstanceKey is the reserved FEEL name binding to the instance's
// own key, so a field expression can reference processInstanceKey (mirrors the mail
// and REST workers).
const userBuiltinProcessInstanceKey = "processInstanceKey"

// userResolve turns a connector field value into a string: a literal verbatim, or a
// FEEL expression evaluated over the scope's variables and coerced to its string
// form. A FEEL null (absent variable or failed evaluation) becomes "" — the same
// null-propagating contract the mail/REST workers use.
func userResolve(rv compiler.RestExpr, scope uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(userBindVars(scope, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// userReadScopeVars reads all of a scope's variables into a map keyed by name.
func userReadScopeVars(store *state.Store, scope uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
		vars[v.Name] = *v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vars, nil
}

// userBindVars binds the named variables from a scope into a FEEL binding; an absent
// name is left unbound (FEEL null), and the reserved processInstanceKey binds to the
// scope's own key as a string.
func userBindVars(scope uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == userBuiltinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(userToExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// userToExprKind maps a stored variable kind to the expr kind for binding it.
func userToExprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}
