package api

import (
	"io"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// Deploying one decision (ADR-0322).
//
// The counterpart of POST /api/v1/deployments, which deploys a single BPMN
// diagram. Until now a decision reached the runtime only through the application's
// Publish (ADR-0128), which ships every artifact the application holds: an author
// who had just written a decision and wanted to see it evaluate had to publish
// other people's drafts to get there.
//
// It is deliberately not a second way to make a decision runnable. The compile and
// the validation happen off the run loop, and the write is `deployDecisions` — the
// very function the publish calls — so the key, the per-decision version, the
// durable record and the registry registration all happen in one turn, record
// first (I2, I3, ADR-0319). A decision deployed here is indistinguishable from one
// a publish deployed: same record, same listing, same recovery.

// deployDecisionResp is what a single-decision deploy reports: the key the
// deployment got, and one row per decision it provides, with the version each is
// now at. Same rows GET /api/v1/decision-deployments returns, so a caller reads one
// shape.
type deployDecisionResp struct {
	Key       uint64                 `json:"key"`
	Decisions []deployedDecisionResp `json:"decisions"`
}

// handleDeployDecision deploys one DMN model as a decision deployment. The body is
// the raw DMN XML — what the editor has on screen, the way the BPMN deploy takes
// what the diagram has on screen.
//
//	?projectId=  the application it is filed under; needs editor there (ADR-0071)
//	?artifactId= the DMN reference it was authored as, for the record's provenance
//	?modelRef=   the model handle behind that reference, when it has one
//
// Only projectId is authorized, because only it decides anything: it files the
// deployment under an application, which is a write there. artifactId and modelRef
// are recorded, not resolved — the record carries its own XML and never reads them
// back — so they say where this came from and grant nothing. modelRef still goes
// through the handle sanitizer, so what is written into a resource name cannot be
// a path.
//
// A decision that has never been written to the model can still be deployed: the
// record carries its own XML, so it needs no model behind it. It is then named
// after its own decision id rather than a handle that does not exist — and nothing
// can *call* it until it is in the model, because a business rule task's picker
// lists what references resolve. The editor says so; the server does not refuse
// something coherent.
func (s *Server) handleDeployDecision(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().ModelUpload))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected DMN XML")
		return
	}
	// Off the loop: compiling is CPU, and a model that does not compile must be
	// refused before anything is claimed or written.
	res := s.dmnValidator.ValidateXML(r.Context(), body)
	if !res.Valid {
		httpapi.Error(w, http.StatusBadRequest, "not a valid DMN model: "+res.Message)
		return
	}
	if len(res.Decisions) == 0 {
		httpapi.Error(w, http.StatusBadRequest,
			"this model declares no decision, so there is nothing to deploy")
		return
	}

	// Filing a deployment into an application is a write on it (ADR-0071), the same
	// check the BPMN deploy makes. It runs before the write, so a refused deploy
	// leaves behind neither a record nor a registry entry, and outside the deploy's
	// own do(): authorization reads the project store through a do() of its own, and
	// dispatching onto the loop from the loop would deadlock.
	projectID := r.URL.Query().Get("projectId")
	if projectID != "" {
		if code, msg := s.authorizeTargetProject(r, projectID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}

	model := decisionDeployment{
		artifactID: r.URL.Query().Get("artifactId"),
		modelRef:   sanitizeHandle(r.URL.Query().Get("modelRef")),
		modelName:  res.ModelName,
		decisions:  res.Decisions,
		xml:        body,
	}
	var (
		recs   []persistedDecision
		depErr error
	)
	s.do(func() {
		recs, depErr = s.deployDecisions([]decisionDeployment{model}, projectID, principalID(r), time.Now().Unix())
	})
	if depErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "deploy decision: "+depErr.Error())
		return
	}
	if len(recs) == 0 {
		httpapi.Error(w, http.StatusInternalServerError, "deploy decision: nothing was written")
		return
	}
	httpapi.JSON(w, http.StatusOK, deployDecisionResp{
		Key: recs[0].Key,
		// Every decision of a record just written is by construction the newest
		// version of its id, so these rows all read Current — which is exactly what
		// a `latest` reference would now pin to (ADR-0319).
		Decisions: decisionResponses(recs),
	})
}
