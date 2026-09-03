package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/logging"

	"github.com/pblumer/atlas/api/httpapi"
)

// rowOverride is the per-server override applied to a call activity's target, as
// shown in the inventory (ADR-0105). Exactly one action: redirect (to
// TargetProcessID's latest), pin (to TargetVersion of the called id), or disable.
type rowOverride struct {
	Action          string `json:"action"`
	TargetProcessID string `json:"targetProcessId,omitempty"`
	TargetVersion   int32  `json:"targetVersion,omitempty"`
}

// callActivityRow is one call activity in the per-server management view: the caller
// it lives in, its static configuration (which process it calls, the binding, the
// propagation flags, whether it is a multi-instance loop), any per-server override,
// and how it currently resolves on this server. Resolution is a per-server fact — a
// caller may deploy before its callee, and an operator may redirect/pin/disable the
// target (ADR-0105) — so the same model on two servers can resolve differently; that
// is exactly what this view surfaces (ADR-0076).
type callActivityRow struct {
	CallerKey          uint64 `json:"callerKey"`
	CallerProcessID    string `json:"callerProcessId"`
	CallerName         string `json:"callerName"`
	CallerVersion      int32  `json:"callerVersion"`
	ElementID          string `json:"elementId"`
	CalledProcessID    string `json:"calledProcessId"`
	Binding            string `json:"binding"`
	PropagateAllParent bool   `json:"propagateAllParent"`
	PropagateAllChild  bool   `json:"propagateAllChild"`
	MultiInstance      bool   `json:"multiInstance"`
	// Loop marks a call activity that carries a standard loop marker instead of a
	// multi-instance one: it calls the process repeatedly while a condition holds
	// (ADR-0133).
	Loop bool `json:"loop"`
	// Override, when set, is the operator's per-server target override for this
	// called process id; nil when the default `latest` resolution applies.
	Override *rowOverride `json:"override,omitempty"`
	// Resolved is true when the call would start a child rather than park, taking any
	// override into account. Target* name the definition it would reach (the effective
	// resolution); they are zero when unresolved (unknown target, or a disabled/
	// pin-to-missing override).
	Resolved      bool   `json:"resolved"`
	TargetKey     uint64 `json:"targetKey"`
	TargetName    string `json:"targetName"`
	TargetVersion int32  `json:"targetVersion"`
}

// handleCallActivities lists every call activity across the server's deployed
// processes with its current, override-aware resolution status — the per-server
// "which process calls which, where does it actually go, and can I change it"
// management view (ADR-0076/0105). Read-only: it changes no runtime state and reads
// only the immutable compiled model plus the override sidecar (I4/I5). Callers are
// listed in registration order; a caller's call activities in node order.
func (s *Server) handleCallActivities(w http.ResponseWriter, _ *http.Request) {
	rows := []callActivityRow{}
	var loadErr error
	s.do(func() {
		recs, err := s.callOverrides.LoadAll()
		if err != nil {
			loadErr = err
			return
		}
		ovByPID := make(map[string]callOverride, len(recs))
		for _, rec := range recs {
			ovByPID[rec.CalledProcessID] = rec
		}
		for _, key := range s.order {
			d := s.deployments[key]
			for _, ref := range d.cp.CallActivities() {
				row := callActivityRow{
					CallerKey:          d.Key,
					CallerProcessID:    d.ProcessID,
					CallerName:         d.Name,
					CallerVersion:      d.Version,
					ElementID:          ref.ElementId,
					CalledProcessID:    ref.CalledProcessId,
					Binding:            ref.Binding.String(),
					PropagateAllParent: ref.PropagateAllParent,
					PropagateAllChild:  ref.PropagateAllChild,
					MultiInstance:      ref.MultiInstance,
					Loop:               ref.Loop,
				}
				var ovPtr *callOverride
				if ov, ok := ovByPID[ref.CalledProcessId]; ok {
					ovCopy := ov
					ovPtr = &ovCopy
					row.Override = &rowOverride{
						Action:          ov.Action,
						TargetProcessID: ov.TargetProcessID,
						TargetVersion:   ov.TargetVersion,
					}
				}
				if target := s.resolveEffectiveTarget(ref.CalledProcessId, ovPtr); target != nil {
					row.Resolved = true
					row.TargetKey = target.Key
					row.TargetName = target.Name
					row.TargetVersion = target.Version
				}
				rows = append(rows, row)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "load overrides: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rows)
}

// resolveEffectiveTarget computes the definition a call activity would reach on this
// server, mirroring the engine's resolveCallTarget (ADR-0105): an override redirects
// to another id's latest, pins a version, or disables (nil); otherwise the called
// id's latest. Returns nil when nothing resolves (the call would park). Run-loop
// goroutine only (it reads the deployment registry).
func (s *Server) resolveEffectiveTarget(calledProcessID string, ov *callOverride) *deployment {
	if ov != nil {
		switch ov.Action {
		case overrideDisable:
			return nil
		case overridePin:
			return s.deploymentByProcessIDVersion(calledProcessID, ov.TargetVersion)
		case overrideRedirect:
			return s.latestDeploymentByProcessID(ov.TargetProcessID)
		}
	}
	return s.latestDeploymentByProcessID(calledProcessID)
}

// deploymentByProcessIDVersion returns the deployment of an exact (process id,
// version), or nil if none. Run-loop goroutine only.
func (s *Server) deploymentByProcessIDVersion(pid string, version int32) *deployment {
	for _, key := range s.order {
		if d := s.deployments[key]; d.ProcessID == pid && d.Version == version {
			return d
		}
	}
	return nil
}

// callOverrideDirective translates a stored override record into the engine directive
// (ADR-0105), resolving a pin's version to a definition key against the current
// deployments. Run-loop goroutine only. An unknown action or an unsatisfiable
// redirect/pin is an error the caller surfaces (a bad request at set time, a skipped
// record at load time).
func (s *Server) callOverrideDirective(rec callOverride) (engine.CallTargetOverride, error) {
	switch rec.Action {
	case overrideDisable:
		return engine.CallTargetOverride{Disabled: true}, nil
	case overrideRedirect:
		if strings.TrimSpace(rec.TargetProcessID) == "" {
			return engine.CallTargetOverride{}, fmt.Errorf("a redirect override needs a targetProcessId")
		}
		return engine.CallTargetOverride{RedirectProcessId: rec.TargetProcessID}, nil
	case overridePin:
		d := s.deploymentByProcessIDVersion(rec.CalledProcessID, rec.TargetVersion)
		if d == nil {
			return engine.CallTargetOverride{}, fmt.Errorf("no version %d of %q is deployed to pin to", rec.TargetVersion, rec.CalledProcessID)
		}
		return engine.CallTargetOverride{PinnedDefKey: d.Key}, nil
	default:
		return engine.CallTargetOverride{}, fmt.Errorf("unknown override action %q (want redirect, pin, or disable)", rec.Action)
	}
}

// loadCallOverrides pushes every persisted override into the processor at startup,
// after deployments are loaded so a pin's version resolves to a key (ADR-0105). A
// record that no longer resolves (its pinned version or redirect target was deleted)
// is logged and skipped rather than failing startup — the inventory then shows it
// resolving by default, which is visible and safe. Pre-loop, so direct processor
// access is single-writer-safe.
func (s *Server) loadCallOverrides() error {
	recs, err := s.callOverrides.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		ov, err := s.callOverrideDirective(rec)
		if err != nil {
			logging.Warn(logging.CallOverrideSkipped, "skipping an unusable call-activity target override",
				slog.String("calledProcessId", rec.CalledProcessID), slog.String("error", err.Error()))
			continue
		}
		s.proc.SetCallTargetOverride(rec.CalledProcessID, ov)
	}
	return nil
}

// handleSetCallOverride sets (or replaces) the per-server target override for a called
// process id (ADR-0105). Admin-gated, like worker config. The override is persisted
// durably before it is applied to the processor (I2), both on the run-loop goroutine.
func (s *Server) handleSetCallOverride(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("processId")
	if strings.TrimSpace(pid) == "" {
		httpapi.Error(w, http.StatusBadRequest, "missing called process id")
		return
	}
	var body struct {
		Action          string `json:"action"`
		TargetProcessID string `json:"targetProcessId"`
		TargetVersion   int32  `json:"targetVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid override body")
		return
	}
	rec := callOverride{
		CalledProcessID: pid,
		Action:          strings.TrimSpace(body.Action),
		TargetProcessID: strings.TrimSpace(body.TargetProcessID),
		TargetVersion:   body.TargetVersion,
		UpdatedAt:       time.Now().Unix(),
	}
	var (
		badReq  error
		saveErr error
	)
	s.do(func() {
		ov, err := s.callOverrideDirective(rec)
		if err != nil {
			badReq = err
			return
		}
		// Durable before visible (I2): persist the record, then apply to the engine.
		if err := s.callOverrides.Save(rec); err != nil {
			saveErr = err
			return
		}
		s.proc.SetCallTargetOverride(pid, ov)
	})
	if badReq != nil {
		httpapi.Error(w, http.StatusBadRequest, badReq.Error())
		return
	}
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save override: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleDeleteCallOverride clears a called process id's override, restoring the
// default `latest` resolution (ADR-0105). Admin-gated; idempotent (a missing override
// is a no-op). The durable record is removed before the engine directive is cleared.
func (s *Server) handleDeleteCallOverride(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("processId")
	var delErr error
	s.do(func() {
		if err := s.callOverrides.Delete(pid); err != nil {
			delErr = err
			return
		}
		s.proc.ClearCallTargetOverride(pid)
	})
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete override: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
