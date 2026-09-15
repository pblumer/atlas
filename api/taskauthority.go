package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// Who may act on a user task.
//
// [Server.instanceAccessFor] is the read half of this axis: ADR-0275 closed it
// after an external review walked in as an unrelated account and read the
// variables of somebody else's instance (audit F11). The finding was about
// reading, so reading is what it closed. The write half was left as it was —
// every task command took the role gate and nothing else, and `user` is the role
// every signed-in person has.
//
// The consequence only became visible when the portal gave a *customer* an
// account. An approval is a user task assigned to a line manager or offered to a
// named group; with no object question asked, the person whose order is waiting
// could complete that task themselves. Approving one's own order is not a subtle
// failure of an authorization model, it is the absence of one.
//
// # The line this draws
//
// A task the model *addressed* — it names an assignee, or candidate groups —
// belongs to whoever it was addressed to. A task that names neither is unassigned
// work: by BPMN convention anybody may pick it up, and the shared inbox
// (ADR-0042) has always worked that way. Narrowing that too would change what
// existing installations do without anybody asking for it, and it closes nothing:
// a task nobody was addressed about has no holder to impersonate.
//
// Operators and administrators keep every task, as they keep every instance. They
// can already cancel the instance underneath it; withholding the task would be a
// gate in front of an open door.

// taskAuthority is the answer to "may this request act on this task".
type taskAuthority struct {
	// known is false when no open user task answers to the key — reported 404 by
	// the caller, exactly as before this check existed.
	known bool
	// allowed is whether the caller may complete, claim or release it.
	allowed bool
}

// mayWorkTask decides whether the caller may act on the user task with this job
// key. It runs on the run loop, because deciding needs the compiled process the
// task's assignment metadata lives in.
func (s *Server) mayWorkTask(r *http.Request, key uint64) (taskAuthority, error) {
	if !s.authEnabled {
		// Single-user mode is a single user, as everywhere else.
		return taskAuthority{known: true, allowed: true}, nil
	}
	pr := httpapi.PrincipalFrom(r.Context())

	var (
		out taskAuthority
		err error
	)
	s.do(func() {
		jv, ok, gErr := s.store.GetJob(key)
		if gErr != nil {
			err = gErr
			return
		}
		if !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
			return
		}
		out.known = true
		if pr == nil {
			return
		}
		if pr.HasRole(RoleOperator) || pr.HasRole(RoleAdmin) {
			out.allowed = true
			return
		}
		tr := s.enrichTask(key, jv)
		// A task the model addressed belongs to whoever it addressed; one it did
		// not is open work.
		if tr.Assignee == "" && tr.CandidateGroups == "" {
			out.allowed = true
			return
		}
		out.allowed = s.holdsTask(pr, tr.Assignee, tr.CandidateGroups)
	})
	return out, err
}

// refuseTaskWork writes the answer for a caller who may not act, and reports
// whether it wrote anything. 404 and 403 say different things on purpose: the task
// list already tells any signed-in caller which tasks exist, so hiding one here
// would withhold nothing and explain nothing, where "this one is not yours" is
// something the person can act on.
func (s *Server) refuseTaskWork(w http.ResponseWriter, a taskAuthority, err error) bool {
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
	case !a.known:
		httpapi.Error(w, http.StatusNotFound, "no open task with that key")
	case !a.allowed:
		httpapi.Error(w, http.StatusForbidden,
			"this task is assigned to somebody else; ask them, or an operator, to act on it")
	default:
		return false
	}
	return true
}
