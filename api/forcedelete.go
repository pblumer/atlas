package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Forced deletion of a deployed version
// (ADR-0468).
//
// A plain delete refuses while a version has running instances, while an order line
// could still start it, and while the current catalogue release binds it
// (ADR-0427). Each refusal is right for somebody tidying up; none is right for
// somebody who has decided the process is to go. force=true is that decision: the
// instances are cancelled and the two guards are overridden. A protected system
// process stays protected, and cancelling instances stays the operator's right.

// forceHint ends every refusal a plain delete gives, so the person who meets one
// learns the way through without having to know the parameter.
const forceHint = " — or delete with force=true, which cancels its running instances and removes it anyway"

// removeRounds bounds how many select-cancel-delete rounds removeDeployments runs.
// A round cancels at most bulkCancelBatchMax instances, so this covers a backlog of
// millions; what it stops is a start event that keeps starting instances faster than
// they are cancelled, which would otherwise hold the request forever.
const removeRounds = 64

// errStillRunning is removeDeployments giving up with instances still running:
// something kept starting them after every round of cancellation.
type errStillRunning struct{ running int }

func (e errStillRunning) Error() string {
	return fmt.Sprintf("cannot delete: %d instance(s) were still running after %d rounds of cancellation — "+
		"something keeps starting them; deactivate the process and try again", e.running, removeRounds)
}

// errMayNotCancel is removeDeployments meeting a running instance on behalf of a
// caller who may not cancel one — an instance that started after the caller was
// told there was none to cancel.
type errMayNotCancel struct{ running int }

func (e errMayNotCancel) Error() string {
	return fmt.Sprintf("cannot delete: %d running instance(s), and cancelling them needs the operator role", e.running)
}

// undeployOnLoop removes one deployed version. Durable before visible (I2,
// ADR-0019): the on-disk record goes first, so a deletion that is acknowledged
// never reappears on restart, and only then the engine's and the server's copies.
//
// Must be called on the run loop, after the caller has established that nothing is
// running under the version.
func (s *Server) undeployOnLoop(key uint64) error {
	if err := s.deploys.delete(key); err != nil {
		return err
	}
	s.proc.Undeploy(key)
	s.forgetLandscape()
	delete(s.deployments, key)
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

// removeDeployments cancels every running instance of the given versions and then
// removes the versions, reporting how many instances it cancelled. A key that is
// no longer deployed is skipped; it is what the caller asked for. mayCancel is
// whether the caller holds the right to cancel an instance (ADR-0209); without it
// the versions are removed only while nothing runs under them.
//
// Each round has the shape POST .../cancel-instances has, for the same reasons:
// selecting the instances walks the whole active family, so it runs off the loop;
// cancelling them is a write bounded by the batch size, so it runs on it. A version
// is removed only in a turn that reads its live count as zero — an instance started
// between the cancellation and that turn (a timer start event) keeps the version,
// and the next round cancels it.
func (s *Server) removeDeployments(keys []uint64, mayCancel bool) (int, error) {
	pending := make(map[uint64]bool, len(keys))
	for _, k := range keys {
		pending[k] = true
	}
	cancelled := 0
	running := 0
	for range removeRounds {
		var batch []uint64
		err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
			err := rv.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
				if !pending[v.ProcessDefKey] {
					return nil
				}
				batch = append(batch, k)
				if len(batch) >= bulkCancelBatchMax {
					return errCancelBatchFull
				}
				return nil
			})
			if err != nil && !errors.Is(err, errCancelBatchFull) {
				return err
			}
			return nil
		})
		if err != nil {
			return cancelled, err
		}
		if len(batch) > 0 && !mayCancel {
			return cancelled, errMayNotCancel{running: len(batch)}
		}
		if len(batch) > 0 {
			s.do(func() {
				for _, k := range batch {
					s.proc.CancelInstance(k)
				}
			})
			if err := s.drive(); err != nil {
				return cancelled, err
			}
			cancelled += len(batch)
			continue
		}
		var opErr error
		running = 0
		s.do(func() {
			for k := range pending {
				if _, ok := s.deployments[k]; !ok {
					delete(pending, k)
					continue
				}
				n, err := s.store.DefInstanceCount(k)
				if err != nil {
					opErr = err
					return
				}
				if n > 0 {
					running += n
					continue
				}
				if err := s.undeployOnLoop(k); err != nil {
					opErr = err
					return
				}
				delete(pending, k)
			}
		})
		if opErr != nil {
			return cancelled, opErr
		}
		if len(pending) == 0 {
			return cancelled, nil
		}
	}
	return cancelled, errStillRunning{running: running}
}

// forceDeleteProcess is DELETE /api/v1/processes/{key}?force=true.
func (s *Server) forceDeleteProcess(w http.ResponseWriter, r *http.Request, key uint64) {
	var (
		found     bool
		protected bool
		processID string
		running   int
		countErr  error
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		processID = d.ProcessID
		// A bootstrap-deployed platform process is platform-managed (ADR-0122):
		// force does not reach it.
		if s.systemPIDs[d.ProcessID] {
			protected = true
			return
		}
		running, countErr = s.store.DefInstanceCount(key)
	})
	mayCancel := s.actsAsOperator(httpapi.PrincipalFrom(r.Context()))
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	case protected:
		httpapi.Error(w, http.StatusForbidden, "protected system process cannot be deleted")
		return
	case countErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "check instances: "+countErr.Error())
		return
	case running > 0 && !mayCancel:
		httpapi.Error(w, http.StatusForbidden, errMayNotCancel{running: running}.Error())
		return
	}
	cancelled, err := s.removeDeployments([]uint64{key}, mayCancel)
	if err != nil {
		removeDeploymentsError(w, err)
		return
	}
	audit(r, logging.DeploymentForceDeleted,
		"a deployed version was deleted with force: its running instances were cancelled",
		slog.Uint64("deploymentKey", key),
		slog.String("processId", processID),
		slog.Int("instancesCancelled", cancelled))
	w.WriteHeader(http.StatusNoContent)
}

// removeDeploymentsError answers a request whose removeDeployments failed.
func removeDeploymentsError(w http.ResponseWriter, err error) {
	var (
		still  errStillRunning
		forbid errMayNotCancel
	)
	switch {
	case errors.As(err, &still):
		httpapi.Error(w, http.StatusConflict, still.Error())
	case errors.As(err, &forbid):
		httpapi.Error(w, http.StatusForbidden, forbid.Error())
	default:
		httpapi.Error(w, http.StatusInternalServerError, "delete deployment: "+err.Error())
	}
}
