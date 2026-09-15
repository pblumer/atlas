package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The two routes a directory synchronisation uses
// (ADR-0332): one that says where to resume from, and
// one that reports what was read.
//
// This is the most powerful pair in the API — the second one creates accounts — so it
// is confined on both of the axes Atlas confines anything on, and on a third that
// only applies here:
//
//   - Scope. apiScopeDirectory reaches these two patterns and nothing else, so the
//     credential a scheduled process carries cannot deploy, cannot start an instance,
//     and cannot read the log (ADR-0194).
//   - Role. Declared in the route table like every other route (ADR-0209).
//   - Authentication itself. Both routes refuse outright when authentication is off,
//     which no other route does. An installation running open is one where every
//     caller is nobody in particular, and "nobody in particular may create accounts"
//     is not a sentence with a defensible exception.

// directorySyncStateView is what the state route answers: where to resume, and what
// to pin the report to.
type directorySyncStateView struct {
	// Revision is what a run sends back as fromRevision. It is the whole of the
	// at-least-once defence: a message pinned to a revision that is no longer current
	// is decided and reported but never written.
	Revision int64 `json:"revision"`

	// UsersDeltaLink and GroupsDeltaLink are Graph's cursors, handed straight to the
	// delta tasks. Empty means "enumerate the collection", which is what a first run
	// does — and is why a first run is the one that must not write.
	UsersDeltaLink  string `json:"usersDeltaLink"`
	GroupsDeltaLink string `json:"groupsDeltaLink"`

	// EverApplied and LastAppliedAt are here so the answer itself says whether this
	// installation has ever written a synchronisation. A mirror that has been
	// reporting for a month looks exactly like a mirror with nothing to do unless
	// somebody is told otherwise.
	EverApplied   bool  `json:"everApplied"`
	LastAppliedAt int64 `json:"lastAppliedAt,omitempty"`
}

// directoryAuthRequired refuses the route when authentication is off, and says why.
//
// Every other route is reachable in an open installation, because an open
// installation is a deliberate single-user mode and the alternative would be a
// product that half works. This pair is the exception: an unauthenticated caller here
// would be able to create an account and then sign in as it the moment authentication
// was turned on, which is not a smaller version of the feature but a different one.
func (s *Server) directoryAuthRequired(w http.ResponseWriter, r *http.Request) bool {
	if s.authEnabled {
		return true
	}
	auditRefusal(r, logging.AuthDirectorySync,
		"directory synchronisation refused: this server runs without authentication")
	httpapi.Error(w, http.StatusForbidden,
		"directory synchronisation needs authentication to be enabled: start Atlas with authentication "+
			"on and give the scheduled process an API token of the \"directory\" scope. There is no "+
			"unauthenticated path into account provisioning, deliberately")
	return false
}

// handleDirectorySyncState answers where the next run resumes from.
func (s *Server) handleDirectorySyncState(w http.ResponseWriter, r *http.Request) {
	if !s.directoryAuthRequired(w, r) {
		return
	}
	var (
		state   directorySyncState
		ran     bool
		loadErr error
	)
	s.do(func() {
		ran = true
		state, loadErr = s.directorySync.current()
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "directory sync: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "directory sync state: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, directorySyncStateView{
		Revision:        state.Revision,
		UsersDeltaLink:  state.UsersDeltaLink,
		GroupsDeltaLink: state.GroupsDeltaLink,
		EverApplied:     state.AppliedAt != 0,
		LastAppliedAt:   state.AppliedAt,
	})
}

// handleDirectorySync takes one change set and answers with what it decided.
//
// The decision and, where the message asked for it, the write happen in a single
// run-loop turn. That is a deliberate cost: the loop is Atlas's single writer, so a
// large batch stalls process execution for as long as it takes to write. It is what
// the DirectoryObjects budget bounds, and it is the reason a message above that
// budget is refused whole rather than truncated — a short change set would be written
// down as a complete one by advancing the cursor past what it did not carry.
func (s *Server) handleDirectorySync(w http.ResponseWriter, r *http.Request) {
	if !s.directoryAuthRequired(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().DirectorySync))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var msg directorySyncMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if n := len(msg.Users) + len(msg.Groups); n > int(s.budgets().DirectoryObjects) {
		httpapi.Error(w, http.StatusRequestEntityTooLarge, directoryTooManyObjects(n, int(s.budgets().DirectoryObjects)))
		return
	}

	now := time.Now().Unix()
	var (
		plan     directoryPlan
		state    directorySyncState
		applied  bool
		ran      bool
		runErr   error
		writeErr error
	)
	s.do(func() {
		// Do returns without running the closure when the loop is shutting down, and
		// every variable above then still holds its zero value. Zero here would read as
		// a perfectly ordinary report — nothing decided, nothing written — which a
		// scheduled run would record as a clean pass over a change set it never saw.
		ran = true
		var users []User
		var groups []group
		if state, runErr = s.directorySync.current(); runErr != nil {
			return
		}
		if users, runErr = s.users.LoadAll(); runErr != nil {
			return
		}
		if groups, runErr = s.groups.LoadAll(); runErr != nil {
			return
		}
		if plan, runErr = decideDirectorySync(msg, state, users, groups, now, newUserID, newGroupID); runErr != nil {
			return
		}
		// The one branch that separates the two modes. Everything above it ran
		// identically either way, and everything below it is reporting.
		if msg.Apply && !plan.Stale {
			applied = true
			writeErr = s.applyDirectoryPlan(plan, msg, state, now)
		}
	})
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable,
			"directory sync: this server is shutting down and did not look at the change set")
		return
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "directory sync: "+runErr.Error())
		return
	case writeErr != nil:
		// A write that failed part way leaves the cursor where it was — the state record
		// is the last thing written — so the next run reads the same change set again and
		// finishes the job. That is why the failure is reported rather than repaired.
		httpapi.Error(w, http.StatusInternalServerError, "directory sync: "+writeErr.Error())
		return
	}

	if applied {
		s.enforceDirectoryPlan(plan)
	}
	rep := directoryReportOf(plan, msg, state, applied, s.budgets())
	auditDirectorySync(r, rep, plan, applied)
	httpapi.JSON(w, http.StatusOK, rep)
}

// enforceDirectoryPlan makes a written plan take effect on the people who are
// already signed in.
//
// Writing the record is not the whole of disabling somebody, and this is the half
// that is easy to forget because nothing fails without it. A session that is already
// open is not re-checked against the user store on every request, and an OAuth grant
// can stand for months (ADR-0200) — so an account the directory says has left would
// keep working until its session expired. The administration API has always done both
// (handlePatchUser); a mirror that only saved the record would be a quieter way to
// disable somebody than the button that says so.
//
// Group membership is the same shape for the same reason: a session carries the group
// ids it was opened with (ADR-0185), so a person removed from a
// mirrored group keeps its grants until they next sign in unless the change is pushed
// into the session that is already open.
//
// It runs after the run-loop turn, never inside it. Two of these calls take locks of
// their own and revokeUserGrants dispatches onto the loop itself, which from inside a
// turn is a deadlock rather than a slow path.
func (s *Server) enforceDirectoryPlan(plan directoryPlan) {
	for _, dec := range plan.Users {
		if !dec.Disabling {
			continue
		}
		s.sessions.destroyUser(dec.Record.ID)
		s.revokeUserGrants(dec.Record.ID)
	}
	for _, dec := range plan.Groups {
		for _, id := range dec.Joined {
			s.sessions.setUserGroupMembership(id, dec.Record.ID, true)
		}
		for _, id := range dec.Left {
			s.sessions.setUserGroupMembership(id, dec.Record.ID, false)
		}
	}
}

// directoryTooManyObjects is the refusal a batch above the budget gets, written out
// so it names the way out rather than only the wall.
func directoryTooManyObjects(got, limit int) string {
	return fmt.Sprintf("this message carries %d directory objects and the batch budget is %d. "+
		"It is refused whole rather than truncated, because a short change set written down as a "+
		"complete one loses the rest for good. Narrow the delta query's $select, set maxUsers on the "+
		"task so an oversized read fails at the worker, or raise ATLAS_LIMIT_DIRECTORY_OBJECTS", got, limit)
}

// auditDirectorySync writes the trail.
//
// A reporting run is audited too. Somebody read a whole tenant out of a directory and
// this server holds the answer; that it wrote nothing makes it a smaller event, not a
// non-event, and an audit that only recorded the writes would not show the reads that
// preceded them.
//
// The per-object lines are written after the run-loop turn, never inside it: logging
// is a side effect, and the single writer's turn is not where side effects belong.
// The cursors are not logged, in any line. They are not credentials, but a URL naming
// a tenant, shipped to wherever the logs go, is a disclosure nobody chose.
func auditDirectorySync(r *http.Request, rep directoryReport, plan directoryPlan, applied bool) {
	audit(r, logging.AuthDirectorySync, "directory synchronisation",
		slog.String("mode", rep.Mode),
		slog.Bool("applied", applied),
		slog.Int64("revision", rep.Revision),
		slog.Bool("ever_applied", rep.EverApplied),
		slog.Int("users_read", rep.Counts.UsersRead),
		slog.Int("users_created", rep.Counts.UsersCreated),
		slog.Int("users_merged", rep.Counts.UsersMerged),
		slog.Int("users_updated", rep.Counts.UsersUpdated),
		slog.Int("users_disabled", rep.Counts.UsersDisabled),
		slog.Int("users_refused", rep.Counts.UsersRefused),
		slog.Int("groups_read", rep.Counts.GroupsRead),
		slog.Int("groups_created", rep.Counts.GroupsCreated),
		slog.Int("groups_updated", rep.Counts.GroupsUpdated),
		slog.Int("groups_cleared", rep.Counts.GroupsCleared),
		slog.Int("members_unresolved", rep.Counts.MembersUnresolved))
	if !applied {
		return
	}
	for _, dec := range plan.Users {
		switch dec.Action {
		case dirUserCreate:
			audit(r, logging.AuthUserCreated, "account created by a directory synchronisation",
				slog.String("username", dec.Record.Username), slog.String("user_id", dec.Record.ID),
				slog.String("source", SourceEntra), slog.Any("roles", dec.Record.Roles))
		case dirUserMerge, dirUserUpdate, dirUserDisable:
			audit(r, logging.AuthUserUpdated, "account changed by a directory synchronisation",
				slog.String("username", dec.Record.Username), slog.String("user_id", dec.Record.ID),
				slog.String("action", dec.Action), slog.Bool("disabled", dec.Record.Disabled),
				slog.Any("roles", dec.Record.Roles))
		}
	}
	for _, dec := range plan.Groups {
		if dec.Action == dirGroupUnchanged {
			continue
		}
		audit(r, logging.AuthDirectoryGroupSynced, "group changed by a directory synchronisation",
			slog.String("group", dec.Record.Name), slog.String("group_id", dec.Record.ID),
			slog.String("action", dec.Action), slog.Int("members", len(dec.Record.Members)))
	}
}
