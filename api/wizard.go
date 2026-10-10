package api

import (
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// Carrying a person through a wizard (ADR-0449, its second step).
//
// A wizard is an embedded subprocess marked atlas:wizard: one sitting of one person,
// whose user tasks are the screens of a multi-step form. The engine runs it like any
// subprocess. What this file adds is the answer a caller needs the moment a screen is
// submitted, and the moment a process starts: where the sitting now stands, and, when
// the next screen is theirs, that screen, already claimed for them.
//
// Everything here is read after the drive that followed the command, so after the
// batch's one fsync (I2), and on the run loop (I3). It writes no event of its own: the
// one write is the claim, which is the ordinary assign command (ADR-0042).

// wizardResp is where a sitting stands.
type wizardResp struct {
	// State is one of the wizard* states below.
	State string `json:"state"`
	// ScopeKey is the element instance of the wizard subprocess: the sitting itself.
	ScopeKey           uint64 `json:"scopeKey"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	// Task is the next screen, set only in the step state.
	Task *taskResp `json:"task,omitempty"`
}

const (
	// wizardStep: the next screen is open and is the caller's, claimed for them where
	// it was open to claim.
	wizardStep = "step"
	// wizardHandedOver: the next screen is addressed to somebody the caller is not. The
	// answer does not say to whom.
	wizardHandedOver = "handedOver"
	// wizardPending: the sitting is live, and no screen is open in it. A worker or a
	// timer stands between this screen and the next; the drive has already run
	// everything the server itself serves, so this is real waiting.
	wizardPending = "pending"
	// wizardDone: the sitting has ended, normally or by its boundary.
	wizardDone = "done"
)

// instanceGetter is the one read the walk below needs, which both the loop-owned store
// and an off-loop snapshot provide.
type instanceGetter interface {
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// enclosingWizardInstance returns the element instance of the wizard subprocess that
// encloses the element instance ei, and 0 when its element is in no wizard. The compiled
// graph says which subprocess that is; the walk up the instance's FlowScopeKey chain
// finds the instance of it this element belongs to — the iteration of a multi-instance
// wizard, for one. A chain that ends before reaching it answers 0.
func enclosingWizardInstance(r instanceGetter, cp *compiler.CompiledProcess, ei *model.ElementInstanceValue) (uint64, error) {
	w := cp.EnclosingWizard(ei.ElementId)
	if w < 0 {
		return 0, nil
	}
	key, root := ei.FlowScopeKey, ei.ProcessInstanceKey
	// The chain is never longer than the graph is deep, so a walk past that many
	// steps is a chain that does not end; it answers "none".
	for range cp.NodeCount() {
		if key == 0 || key == root {
			return 0, nil
		}
		sc, ok, err := r.GetElementInstance(key)
		if err != nil || !ok {
			return 0, err
		}
		if sc.ElementId == w {
			return key, nil
		}
		key = sc.FlowScopeKey
	}
	return 0, nil
}

// wizardOfElement returns the sitting an element instance belongs to and its process
// instance, or zeros when it belongs to none. On the loop.
func (s *Server) wizardOfElement(elKey uint64) (scope, piKey uint64, err error) {
	ei, ok, err := s.store.GetElementInstance(elKey)
	if err != nil || !ok {
		return 0, 0, err
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok || d.cp == nil {
		return 0, 0, nil
	}
	piKey = ei.ProcessInstanceKey
	scope, err = enclosingWizardInstance(s.store, d.cp, ei)
	if err != nil || scope == 0 {
		return 0, 0, err
	}
	return scope, piKey, nil
}

// openStepsOf calls fn for every open user task of the instance, with the job and the
// sitting it belongs to (0 for none). On the loop.
func (s *Server) openStepsOf(piKey uint64, fn func(elKey, jobKey uint64, jv *model.JobValue, scope uint64)) error {
	return s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
		jobKey, ok, err := s.store.JobOfElement(elKey)
		if err != nil || !ok {
			return err
		}
		jv, ok, err := s.store.GetJob(jobKey)
		if err != nil || !ok {
			return err
		}
		// Open user tasks only: retries 0 is a task parked behind an incident.
		if jv.JobType != compiler.UserTaskJobTypeIndex || jv.Retries <= 0 {
			return nil
		}
		scope, _, err := s.wizardOfElement(elKey)
		if err != nil {
			return err
		}
		fn(elKey, jobKey, jv, scope)
		return nil
	})
}

// continueWizard answers where the sitting at scope stands for the caller of r, and
// claims the next screen for them when it is open to them and nobody holds it.
//
// The next screen is the open user task of the sitting that was activated first, so
// parallel branches are presented one after the other.
func (s *Server) continueWizard(r *http.Request, piKey, scope uint64) (*wizardResp, error) {
	out := &wizardResp{ScopeKey: scope, ProcessInstanceKey: piKey}
	var (
		live  bool
		found bool
		tr    taskResp
		err   error
	)
	s.do(func() {
		_, live, err = s.store.GetElementInstance(scope)
		if err != nil || !live {
			return
		}
		var best uint64
		err = s.openStepsOf(piKey, func(elKey, jobKey uint64, jv *model.JobValue, in uint64) {
			if in != scope || (found && elKey >= best) {
				return
			}
			best, found = elKey, true
			tr = s.enrichTask(jobKey, jv)
		})
	})
	switch {
	case err != nil:
		return nil, err
	case !live:
		out.State = wizardDone
		return out, nil
	case !found:
		out.State = wizardPending
		return out, nil
	}
	// Single-user mode has nobody to claim for, and everything is theirs.
	if !s.authEnabled {
		out.State, out.Task = wizardStep, &tr
		return out, nil
	}
	// A public sitting is the visitor's who started it. Nobody signed in is carried
	// into it, and nothing in it is claimed: a claim would put the visitor's screen
	// in one person's inbox (ADR-0449).
	if tr.Wizard == compiler.WizardPublic.String() {
		out.State = wizardHandedOver
		return out, nil
	}
	// The screen is the caller's when it names nobody, or names them: as its assignee,
	// or through a candidate group they are in (ADR-0317's rule for who may act).
	pr := httpapi.PrincipalFrom(r.Context())
	open := tr.Assignee == "" && tr.CandidateGroups == ""
	if pr == nil || !open && !s.holdsTask(pr, tr.Assignee, tr.CandidateGroups) {
		out.State = wizardHandedOver
		return out, nil
	}
	if tr.Assignee == "" {
		// Claimed, so the screen leaves every other inbox for the moment between two
		// screens (ADR-0421) — and only if it is still nobody's when the claim is
		// queued: a claim is last-writer-wins, and somebody else's must not be undone.
		claimed := false
		s.do(func() {
			jv, ok, gErr := s.store.GetJob(tr.Key)
			if gErr != nil {
				err = gErr
				return
			}
			if ok && jv.Assignee == "" {
				s.proc.AssignJob(tr.Key, pr.Username)
				claimed = true
			}
		})
		if err != nil {
			return nil, err
		}
		if !claimed {
			out.State = wizardHandedOver
			return out, nil
		}
		if err := s.drive(); err != nil {
			return nil, err
		}
		tr.Assignee = pr.Username
	}
	out.State, out.Task = wizardStep, &tr
	return out, nil
}

// wizardAfterStart answers where the first sitting a new instance entered stands, and
// nil when the start entered none. The sitting of the earliest open screen in a wizard
// comes first; failing one, a live wizard with no open screen is pending.
func (s *Server) wizardAfterStart(r *http.Request, piKey uint64) (*wizardResp, error) {
	var (
		scope, stepEl, live uint64
		err                 error
	)
	s.do(func() {
		err = s.openStepsOf(piKey, func(elKey, _ uint64, _ *model.JobValue, in uint64) {
			if in != 0 && (stepEl == 0 || elKey < stepEl) {
				stepEl, scope = elKey, in
			}
		})
		if err != nil || scope != 0 {
			return
		}
		err = s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
			ei, ok, gErr := s.store.GetElementInstance(elKey)
			if gErr != nil || !ok {
				return gErr
			}
			if d, ok := s.deployments[ei.ProcessDefKey]; ok && d.cp != nil &&
				d.cp.Wizard(ei.ElementId) != compiler.WizardNone && (live == 0 || elKey < live) {
				live = elKey
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if scope == 0 {
		scope = live
	}
	if scope == 0 {
		return nil, nil
	}
	return s.continueWizard(r, piKey, scope)
}

// logWizardFailure reports a continuation that could not be answered. The command it
// followed stands; the person finds the next screen in their inbox instead.
func logWizardFailure(r *http.Request, piKey uint64, err error) {
	logging.Warn(logging.WizardContinuationFailed,
		"a wizard step was taken, and where the sitting stands could not be answered",
		slog.Uint64("instance", piKey), slog.String("path", r.URL.Path), slog.String("error", err.Error()))
}
