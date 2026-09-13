package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pblumer/atlas/compiler"

	"github.com/pblumer/atlas/api/httpapi"
)

// projectDeployResp reports the outcome of deploying a whole project: the BPMN
// definitions that were registered and the DMN references that were resolved and
// validated as part of the same action. Deployed is false when the bundle was
// refused (Reason says why) and nothing was registered.
type projectDeployResp struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Deployed    bool              `json:"deployed"`
	Reason      string            `json:"reason,omitempty"`
	Definitions []deployedProcess `json:"definitions"`
	// Decisions are the DMN models this publish deployed as runtime artifacts in
	// their own right — durable, versioned, and evaluable without a process to carry
	// them (ADR-draft-durable-versioned-decision-deployments). An application whose
	// only artifacts are decisions reports them here with an empty Definitions.
	Decisions  []deployedDecisionResp `json:"decisions"`
	References []dmnRefValidationResp `json:"references"`
	// Warnings are what a single-model deploy has reported since ADR-0158: things
	// that registered fine but will not hold as written — a worker reference naming
	// something not configured, a data object typed against a vocabulary that does
	// not carry it, an Atlas extension element bound to somebody else's namespace.
	// They are not a refusal here either: an application is routinely published
	// before the workers it names are provisioned, and to a server where they are
	// provisioned later. Same shape as deployResp.Warnings, so a client reads one
	// field whichever route it deployed through (ADR-0287).
	Warnings []string `json:"warnings,omitempty"`
}

// handleDeployProject deploys a project as a bundle (ADR-0034): it first resolves
// and validates every DMN reference (the deploy-time gate), then deploys those
// decisions as durable runtime artifacts, then deploys every BPMN draft as a
// runnable definition. It is "validate all, then deploy all" — a draft that does
// not compile or a reference that does not validate refuses the whole bundle
// before anything is registered, so a broken artifact never leaves a
// half-deployed project.
//
// Decisions go first on purpose: a process published alongside a decision must pin
// its latest-bound reference to that decision deployment rather than to the copy
// bundled with itself (ADR-draft-durable-versioned-decision-deployments).
//
// Honest limitation: the final BPMN deploy loop is not atomic against a mid-loop
// persist failure (same as a multi-pool deploy). The decisions are inside that
// boundary rather than outside it — every decision record is written before any is
// registered — so a failed publish leaves nothing evaluable that a restart would
// not also produce.
func (s *Server) handleDeployProject(w http.ResponseWriter, r *http.Request) {
	out := s.deployApplicationBundle(r, r.PathValue("id"))
	out.write(w)
}

// bundleOutcome is the result of one bundle-deploy attempt, kept separate from the
// writing of it so both the deploy route and the ADR-0128 publish route can drive
// the same logic and then render it their own way (publish additionally mints a
// release). Exactly one of errMsg / resp is meaningful: a non-empty errMsg is an
// {error} payload at status, otherwise resp is written as JSON at status.
type bundleOutcome struct {
	resp   projectDeployResp
	status int
	errMsg string
	// deployed is true only when the whole bundle registered, which is the gate for
	// minting a release (all-or-nothing, ADR-0034/0127).
	deployed bool
	// proj is the resolved application, valid once past the lookup/authz phase.
	proj project
}

// write renders the outcome onto the response.
func (o bundleOutcome) write(w http.ResponseWriter) {
	if o.errMsg != "" {
		httpapi.Error(w, o.status, o.errMsg)
		return
	}
	httpapi.JSON(w, o.status, o.resp)
}

// deployApplicationBundle runs the "validate all, then deploy all" bundle deploy
// for one application and reports the outcome without writing a response. See
// handleDeployProject for the semantics and the honest limitations.
func (s *Server) deployApplicationBundle(r *http.Request, id string) bundleOutcome {

	// Phase 1 (on-loop): load the project and its artifacts.
	var (
		proj            project
		ok              bool
		getErr, loadErr error
		drafts          []draft
		refs            []dmnRef
	)
	s.do(func() {
		if proj, ok, getErr = s.projects.Get(id); getErr != nil || !ok {
			return
		}
		var allDrafts []draft
		if allDrafts, loadErr = s.drafts.LoadAll(); loadErr != nil {
			return
		}
		for _, d := range allDrafts {
			if d.ProjectID == id {
				drafts = append(drafts, d)
			}
		}
		var allRefs []dmnRef
		if allRefs, loadErr = s.dmnrefs.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range allRefs {
			if rec.ProjectID == id {
				refs = append(refs, rec)
			}
		}
	})
	switch {
	case getErr != nil:
		return bundleOutcome{status: http.StatusInternalServerError, errMsg: "read project: " + getErr.Error()}
	case !ok:
		return bundleOutcome{status: http.StatusNotFound, errMsg: "no project with that id"}
	case loadErr != nil:
		return bundleOutcome{status: http.StatusInternalServerError, errMsg: "list artifacts: " + loadErr.Error()}
	}
	// Deploying a project's artifacts is a write on the project, so it needs the
	// editor role (ADR-0071). This gates the design-time action; it does not
	// isolate the resulting running instances, which stay out of scope.
	if code, msg := s.checkProjectRole(r, proj, ScopeRoleEditor); code != 0 {
		return bundleOutcome{status: code, errMsg: msg, proj: proj}
	}
	// A protected system project is deployed only by the startup bootstrap
	// (ADR-0122); refuse deploying it through the API, for every caller.
	if code, msg := protectedGuard(proj); code != 0 {
		return bundleOutcome{status: code, errMsg: msg, proj: proj}
	}

	// Phase 2 (off-loop): DMN preflight. Resolve + validate every reference; a
	// single failure refuses the bundle without deploying anything. For each valid
	// reference, keep its model XML and the decisions it provides, so a draft's
	// business rule tasks can be matched to a model below.
	refReports := make([]dmnRefValidationResp, 0, len(refs))
	var models []resolvedModel
	invalidRefs := 0
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
		if err != nil {
			return bundleOutcome{status: http.StatusInternalServerError, errMsg: "resolve dmn model: " + err.Error(), proj: proj}
		}
		if !res.Valid {
			invalidRefs++
		} else {
			xml, err := s.dmnResolver.Resolve(r.Context(), rec.ModelRef)
			if err != nil {
				return bundleOutcome{status: http.StatusInternalServerError, errMsg: "resolve dmn model: " + err.Error(), proj: proj}
			}
			models = append(models, resolvedModel{
				artifactID: rec.ID,
				modelRef:   rec.ModelRef,
				modelName:  res.ModelName,
				decisions:  res.Decisions,
				xml:        xml,
			})
		}
		refReports = append(refReports, dmnRefValidationResp{
			ID: rec.ID, Name: rec.Name, ModelRef: rec.ModelRef,
			Resolved: res.Resolved, Valid: res.Valid,
			ModelName: res.ModelName, Decisions: res.Decisions, Message: res.Message,
		})
	}
	if invalidRefs > 0 {
		return bundleOutcome{status: http.StatusConflict, proj: proj, resp: projectDeployResp{
			ID: proj.ID, Name: proj.Name, Deployed: false,
			Reason:      fmt.Sprintf("%d DMN reference(s) unresolved or invalid", invalidRefs),
			Definitions: []deployedProcess{}, Decisions: []deployedDecisionResp{}, References: refReports,
		}}
	}

	// Phase 3-prep (off-loop): compile every draft and match it to the DMN model
	// that provides its business rule tasks' decisions. A draft that does not
	// compile, or references a decision no project model provides, refuses the
	// whole bundle before anything is registered.
	dmnForDraft := make([][][]byte, len(drafts))
	for i, d := range drafts {
		deployables, err := compiler.ParseAll(1, 1, bytes.NewReader([]byte(d.XML)))
		if err != nil {
			return bundleOutcome{status: http.StatusConflict, proj: proj, resp: projectDeployResp{
				ID: proj.ID, Name: proj.Name, Deployed: false,
				Reason:      fmt.Sprintf("draft %q does not compile: %s", d.ProcessID, err.Error()),
				Definitions: []deployedProcess{}, Decisions: []deployedDecisionResp{}, References: refReports,
			}}
		}
		needed := draftDecisions(deployables)
		if len(needed) == 0 {
			continue
		}
		xmls, ok := coverModels(models, needed)
		if !ok {
			return bundleOutcome{status: http.StatusConflict, proj: proj, resp: projectDeployResp{
				ID: proj.ID, Name: proj.Name, Deployed: false,
				Reason:      fmt.Sprintf("draft %q references decision(s) %v not provided by any DMN reference in this project", d.ProcessID, needed),
				Definitions: []deployedProcess{}, Decisions: []deployedDecisionResp{}, References: refReports,
			}}
		}
		dmnForDraft[i] = xmls
	}

	// Phase 3 (on-loop): deploy the application's decisions, then each draft with
	// its matched DMN model.
	var (
		persistErr error
		claimed    string
		deployed   []deployedProcess
		decisions  []persistedDecision
		warnings   []string
	)
	s.do(func() {
		// Every draft's claim first, then any deploy: a bundle is "validate all, then
		// deploy all", and a claim refusal in the third draft must not leave the first
		// two registered (ADR-0205) — nor, now, its decisions deployed.
		for _, d := range drafts {
			var e error
			if claimed, e = s.claimBlockingModel(r, []byte(d.XML)); e != nil {
				persistErr = e
				return
			} else if claimed != "" {
				return
			}
		}
		deployedAt := time.Now().Unix()
		// Decisions before drafts, deliberately: a process published alongside a
		// decision must pin its latest-bound reference to *that* decision deployment,
		// not to the copy bundled with itself
		// (ADR-draft-durable-versioned-decision-deployments).
		var decErr error
		if decisions, decErr = s.deployDecisions(decisionDeployments(models), id, principalID(r), deployedAt); decErr != nil {
			persistErr = decErr
			return
		}
		for i, d := range drafts {
			dps, _, pErr := s.deployModel([]byte(d.XML), dmnForDraft[i], deployedAt, d.ProjectID, principalID(r))
			if pErr != nil {
				persistErr = pErr
				return
			}
			deployed = append(deployed, dps...)
		}
		// The same preflight a single-model deploy runs, on the same loop and in the
		// same closure — every draft of this bundle is registered by now, and every
		// one of them files under this application, so one vocabulary answers for all
		// (ADR-0158/0230).
		warnings = s.deployWarningsOnLoop(deployed, id)
	})
	if persistErr != nil {
		return bundleOutcome{status: http.StatusInternalServerError, errMsg: "persist deployment: " + persistErr.Error(), proj: proj}
	}
	if claimed != "" {
		return bundleOutcome{status: http.StatusConflict, proj: proj, resp: projectDeployResp{
			ID: proj.ID, Name: proj.Name, Deployed: false,
			Reason: "a draft can be delivered the message name " + claimed +
				", which an inbound worker you cannot reach publishes under. Rename the message, " +
				"or ask whoever owns that worker to share it.",
			Definitions: []deployedProcess{}, Decisions: []deployedDecisionResp{}, References: refReports,
		}}
	}
	if deployed == nil {
		deployed = []deployedProcess{}
	}
	// Off the loop on purpose: this reads the drafts' bytes and nothing the engine
	// owns (I3). One pass per draft, because a namespace prefix is bound at each
	// document's root and each draft is its own document.
	for _, d := range drafts {
		warnings = append(warnings, foreignAtlasNamespaceWarnings([]byte(d.XML))...)
		warnings = append(warnings, searchableDeclarationWarnings([]byte(d.XML))...)
	}
	return bundleOutcome{status: http.StatusOK, deployed: true, proj: proj, resp: projectDeployResp{
		ID: proj.ID, Name: proj.Name, Deployed: true,
		Definitions: deployed, Decisions: decisionResponses(decisions),
		References: refReports, Warnings: dedupeWarnings(warnings),
	}}
}

// resolvedModel is one project DMN reference resolved for the bundle: its model
// XML, the decision names it provides, and — for a reference that belongs to an
// application being published — where it came from, which is what the decision
// deployment records (ADR-draft-durable-versioned-decision-deployments). The
// single-deploy path (dmnForDeployBody) fills only the first two: it bundles a
// model with a process rather than deploying it as a decision.
type resolvedModel struct {
	artifactID string
	modelRef   string
	modelName  string
	decisions  []string
	xml        []byte
}

// decisionDeployments turns the application's resolved DMN references into the
// decision deployments to publish, one per distinct model. Two references to the
// same handle are one model and get one deployment; a reference with no handle
// (nothing to name a resource by) is skipped.
func decisionDeployments(models []resolvedModel) []decisionDeployment {
	var out []decisionDeployment
	seen := map[string]bool{}
	for _, m := range models {
		if m.modelRef == "" || seen[m.modelRef] {
			continue
		}
		seen[m.modelRef] = true
		out = append(out, decisionDeployment{
			artifactID: m.artifactID,
			modelRef:   m.modelRef,
			modelName:  m.modelName,
			decisions:  m.decisions,
			xml:        m.xml,
		})
	}
	return out
}

// draftDecisions is the distinct set of DMN decision ids referenced by every
// process in one compiled draft.
func draftDecisions(deployables []compiler.Deployable) []string {
	seen := map[string]bool{}
	var out []string
	for i := range deployables {
		for _, id := range deployables[i].Process.BusinessRuleDecisions() {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// dmnForDeployBody resolves the DMN model a single (non-project) deploy's business
// rule tasks need, so a local decision is bundled with the process instead of
// deployed model-less. A model-less business-rule deploy is the trap that started
// this: its tasks create DMN jobs that can never evaluate, and because every deploy
// drives all pending jobs, one such job then fails every future deploy. It mirrors
// the project deploy's matching but over all DMN references passed in.
//
// Returns (xmls, "", nil) when stored models together provide every needed local
// decision (one model when they share it, several when the tasks span models);
// (nil, "", nil) when the body needs none (no business rule tasks, or only central
// worker decisions, which carry no local model); (nil, reason, nil) when a needed
// decision is in no model, so the caller refuses (409) instead of deploying a
// process that can never run; or (nil, "", err) on an infrastructure failure. It
// resolves and compiles models (I/O + CPU), so it runs OFF the run loop; the
// reference records are read on the loop and passed in.
func (s *Server) dmnForDeployBody(ctx context.Context, body []byte, refs []dmnRef) ([][]byte, string, error) {
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(body))
	if err != nil {
		return nil, "", nil // a compile error is surfaced (as a 400) by the deploy itself
	}
	needed := draftDecisions(deployables)
	if len(needed) == 0 {
		return nil, "", nil
	}
	var models []resolvedModel
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(ctx, rec.ModelRef)
		if err != nil {
			return nil, "", err
		}
		if !res.Valid {
			continue
		}
		xml, err := s.dmnResolver.Resolve(ctx, rec.ModelRef)
		if err != nil {
			return nil, "", err
		}
		models = append(models, resolvedModel{decisions: res.Decisions, xml: xml})
	}
	if xmls, ok := coverModels(models, needed); ok {
		return xmls, "", nil
	}
	return nil, fmt.Sprintf("this diagram's business rule task(s) reference decision(s) %v that no DMN model provides — create the decision (or add its reference) in Atlas, then deploy", needed), nil
}

// coverModels returns the XML of the models that together provide every needed
// decision — one model when they all live together, several when a process's
// business rule tasks reference decisions across different models (the registry
// holds a list per process, so a deployment can bundle more than one). Each needed
// decision is assigned to the first model that provides it, and the distinct chosen
// models are returned in model order for determinism. ok is false if any needed
// decision is in no model at all, in which case the deploy is refused rather than
// registering a business rule task that can never evaluate.
func coverModels(models []resolvedModel, needed []string) ([][]byte, bool) {
	provider := map[string]int{} // decision id → index of the model that provides it
	for i, m := range models {
		for _, d := range m.decisions {
			if _, ok := provider[d]; !ok {
				provider[d] = i
			}
		}
	}
	used := map[int]bool{}
	for _, n := range needed {
		i, ok := provider[n]
		if !ok {
			return nil, false
		}
		used[i] = true
	}
	var out [][]byte
	for i := range models {
		if used[i] {
			out = append(out, models[i].xml)
		}
	}
	return out, true
}
