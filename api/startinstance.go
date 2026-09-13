package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// Starting an instance by the name of its process.
//
// Every other way in names a *definition key*: one deployed version, pinned. That
// is right for an operator restarting a particular version and wrong for anything
// modelled. A process that starts another process knows the other one's id — it is
// what a modeller writes and what a catalogue stores — and it does not know, and
// must not pin, which version is current. Atlas had no route for that at all, and
// it went unnoticed because the only callers were models: the portal's fulfilment
// orchestrator posts to `/api/v1/instances` for every provisioning run and every
// approval, and every one of those calls would have been a 404 on a customer's
// order. `TestEverySystemProcessCallsARouteThatExists` is what found it.
//
// Latest version, deliberately. A message start event correlates to the latest
// version too, and an orchestrator that pinned the version it first saw would keep
// starting a superseded model for as long as an order stayed open.

// startInstanceReq names the process to start and the variables to seed it with.
type startInstanceReq struct {
	ProcessID string         `json:"processId"`
	Variables map[string]any `json:"variables"`
}

// handleCreateInstanceByProcessID starts the newest deployed version of a named
// process. Same authority as starting one by key: this is the same act, addressed
// the way a model can address it.
func (s *Server) handleCreateInstanceByProcessID(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req startInstanceReq
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ProcessID) == "" {
		httpapi.Error(w, http.StatusBadRequest, "processId is required")
		return
	}
	startVars, err := startVarsFromMap(req.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	var key uint64
	var found bool
	// latestDeploymentOf is the registry walk mimimport already needed; the newest
	// version of an id is one question, answered in one place.
	s.do(func() {
		if d := s.latestDeploymentOf(req.ProcessID); d != nil {
			key, found = d.Key, true
		}
	})
	if !found {
		// The same answer a wrong key gets. A model naming a process that is not
		// deployed is the fault this reports, and saying which of the two it is
		// would not help the model.
		httpapi.Error(w, http.StatusNotFound, "no deployed process with id "+req.ProcessID)
		return
	}
	s.startInstance(w, key, startVars)
}

// startInstance is the half both entry points share: create, drive, read back.
// Split out when the by-id route arrived, so the two cannot answer differently
// about a process that is deployed but not executable.
func (s *Server) startInstance(w http.ResponseWriter, key uint64, startVars []model.VariableValue) {
	var (
		found       bool
		notExec     bool
		runErr      error
		statErr     error
		stats       statsResp
		driveNeeded bool
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		// A non-executable process is descriptive-only; refuse to start it (the UI
		// also hides it, but this guards the API and public start paths directly).
		if d.cp != nil && !d.cp.IsExecutable() {
			notExec = true
			return
		}
		s.proc.CreateInstance(key, startVars...)
		driveNeeded = true
	})
	// The handlers run off the run loop (ADR-0157 step 6), so the drive and the
	// read-back that follows it are two separate visits to the loop — and the
	// read-back's is now only long enough to take a view, not to do the counting
	// (ADR-0266).
	if driveNeeded {
		if runErr = s.drive(); runErr == nil {
			stats, statErr = s.statsOffLoop()
		}
	}
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case notExec:
		httpapi.Error(w, http.StatusConflict, "process is not executable and cannot be started")
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "run instance: "+runErr.Error())
	case statErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, createInstanceResp{DefinitionKey: key, Stats: stats})
	}
}
