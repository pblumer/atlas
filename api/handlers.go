package api

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// maxXMLBytes caps a deployment body. BPMN models are small; this is a sanity
// bound, not a tuning knob.
const maxXMLBytes = 4 << 20 // 4 MiB

// maxFeelBytes caps a FEEL validation body. Expressions are tiny; this is a
// sanity bound.
const maxFeelBytes = 64 << 10 // 64 KiB

// deployedProcess is one process registered by a deployment. A collaboration
// deploys several (one per executable pool); a plain model deploys one.
type deployedProcess struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	Version   int32  `json:"version"`
}

// deployResp echoes the first deployed process flat (single-process clients read
// key/processId/version) and lists every process the model deployed, so a
// collaboration surfaces all its pools.
type deployResp struct {
	Key         uint64            `json:"key"`
	ProcessID   string            `json:"processId"`
	Version     int32             `json:"version"`
	Deployments []deployedProcess `json:"deployments"`
}

type processResp struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
	// CollaborationKey groups a collaboration's pools: when non-zero, this process
	// is a pool of a collaboration and the value is the stable key the Operations
	// replay view is opened with (#/operations/c/{collaborationKey}). Zero for a
	// standalone process (ADR-0038).
	CollaborationKey uint64 `json:"collaborationKey,omitempty"`
	// StartFormID names the form the UI shows before starting an instance, empty
	// when the process has no start form (ADR-0028). It lets the Tasks app offer a
	// "start via form" flow whose submitted data becomes the start variables.
	StartFormID string `json:"startFormId,omitempty"`
	// Executable is the process's bpmn:isExecutable flag. A non-executable process
	// still lists (so it can be inspected) but is omitted from the start surfaces and
	// cannot be started. Always emitted so the UI can filter on it.
	Executable bool `json:"executable"`
	// VersionTag is the process's atlas:versionTag revision label, empty when unset.
	VersionTag string `json:"versionTag,omitempty"`
}

// collaborationParticipants reports how many <participant> pools a model's
// <collaboration> declares. Two or more marks the XML as a collaboration whose
// pools deploy as sibling definitions sharing this XML (ADR-0023).
func collaborationParticipants(body []byte) int {
	var d struct {
		Participants []struct{} `xml:"collaboration>participant"`
	}
	// A deployed body is well-formed XML; on the impossible parse error the zero
	// value (no participants) is the right answer anyway.
	_ = xml.Unmarshal(body, &d)
	return len(d.Participants)
}

// poolSiblings returns the deployments that are pools of the same collaboration
// as d — every deployment sharing d's identical BPMN body — keeping the highest
// version of each pool (so a redeploy of the same collaboration shows its current
// pools), in registration order. For a standalone process it returns just d.
// Must be called on the run-loop goroutine.
func (s *Server) poolSiblings(d *deployment) []*deployment {
	if collaborationParticipants(d.xml) < 2 {
		return []*deployment{d}
	}
	latest := map[string]*deployment{}
	for _, key := range s.order {
		sib := s.deployments[key]
		if !bytes.Equal(sib.xml, d.xml) {
			continue
		}
		if cur, ok := latest[sib.ProcessID]; !ok || sib.Version > cur.Version {
			latest[sib.ProcessID] = sib
		}
	}
	var out []*deployment
	for _, key := range s.order {
		sib := s.deployments[key]
		if latest[sib.ProcessID] == sib {
			out = append(out, sib)
		}
	}
	return out
}

// collaborationKeyOf returns the stable group key for the collaboration d belongs
// to, or 0 when d is a standalone process. poolSiblings lists pools in
// registration order and keys are assigned monotonically, so the first pool
// carries the smallest key — a stable group id. Must be called on the run-loop
// goroutine.
func (s *Server) collaborationKeyOf(d *deployment) uint64 {
	pools := s.poolSiblings(d)
	if len(pools) < 2 {
		return 0
	}
	return pools[0].Key
}

// processIdentity extracts the first process element's id and name from BPMN XML.
// encoding/xml matches on local name, so it works whether or not the element
// carries a namespace prefix (<process> or <bpmn:process>).
func processIdentity(body []byte) (id, name string) {
	var d struct {
		Processes []struct {
			ID   string `xml:"id,attr"`
			Name string `xml:"name,attr"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(body, &d); err != nil || len(d.Processes) == 0 {
		return "", ""
	}
	return d.Processes[0].ID, d.Processes[0].Name
}

type infoResp struct {
	Product string `json:"product"`
	Version string `json:"version"`
	// Docs reports whether the OpenAPI spec and the API explorer are served
	// (the --docs gate, ADR-0043), so the web UI can show or hide its
	// "API Explorer" entry without probing /api/docs.
	Docs bool `json:"docs"`
	// Revision/BuildTime/Modified/Go are the binary's embedded VCS build metadata,
	// so the web UI can show exactly which commit the running server was built from.
	Revision  string `json:"revision,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	Modified  bool   `json:"modified"`
	Go        string `json:"go,omitempty"`
}

type runtimeElement struct {
	ElementID string `json:"elementId"`
	Type      string `json:"type"`
	Tokens    int    `json:"tokens"` // tokens sitting here now (live — drawn green)
	Visits    int    `json:"visits"` // tokens that have ever passed through (history — drawn gray)
}

type runtimeResp struct {
	Instances int              `json:"instances"`
	Tokens    int              `json:"tokens"`
	Elements  []runtimeElement `json:"elements"`
}

// collabPool is one pool (participant) of a collaboration, as a deployed
// definition the collaboration runtime aggregates.
type collabPool struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	Version   int32  `json:"version"`
}

// collabFlow is one delivered message flow on the replay timeline: which message
// crossed to which receiving element, when, and between which instances. The
// receiving element is the message-flow edge's target on the shared diagram.
type collabFlow struct {
	At                int64  `json:"at"` // unix nanoseconds
	MessageName       string `json:"messageName"`
	CorrelationKey    string `json:"correlationKey"`
	ReceiverElementID string `json:"receiverElementId"`
	SenderInstance    uint64 `json:"senderInstance,omitempty"`
	ReceiverInstance  uint64 `json:"receiverInstance,omitempty"`
}

// collabRuntimeResp is the whole collaboration's runtime for the replay view:
// its pools, the merged live/visited element overlay across every pool, and the
// time-ordered message flows that crossed between them (ADR-0038).
type collabRuntimeResp struct {
	Pools        []collabPool     `json:"pools"`
	Instances    int              `json:"instances"`
	Tokens       int              `json:"tokens"`
	Elements     []runtimeElement `json:"elements"`
	MessageFlows []collabFlow     `json:"messageFlows"`
}

// timelineStep is one element activation on a single instance's replay timeline:
// which BPMN element a token entered, its type, when, and the variable values as
// they stood when the token entered it (ADR-0046, ADR-0048). Steps are ordered
// oldest-first, so the Operations view can step through them and show the
// variables at each point.
type timelineStep struct {
	At                 int64          `json:"at"`              // unix nanoseconds (element activated)
	EndAt              int64          `json:"endAt,omitempty"` // unix nanoseconds (element completed), 0 if still active
	ElementID          string         `json:"elementId"`
	Type               string         `json:"type"`
	Variables          []variableView `json:"variables"`
	Position           uint64         `json:"position"`
	TokenID            uint64         `json:"tokenId,omitempty"`
	ElementInstanceKey uint64         `json:"elementInstanceKey,omitempty"`
	SourceElementID    string         `json:"sourceElementId,omitempty"`
	Action             string         `json:"action,omitempty"`
	Relation           string         `json:"relation,omitempty"`
}

type timelineToken struct {
	TokenID            uint64 `json:"tokenId"`
	ElementID          string `json:"elementId"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	State              string `json:"state"`
}

type timelineFrame struct {
	Position uint64          `json:"position"`
	At       int64           `json:"at"`
	Tokens   []timelineToken `json:"tokens"`
}

// instanceTimelineResp is one process instance's step-by-step replay: its
// definition, lifecycle state, and the ordered elements a token walked through
// (ADR-0046). It powers the single-process replay transport, the analogue of the
// collaboration message-flow timeline.
type instanceTimelineResp struct {
	InstanceKey   uint64          `json:"instanceKey"`
	ProcessDefKey uint64          `json:"processDefKey"`
	ProcessID     string          `json:"processId"`
	Version       int32           `json:"version"`
	VersionTag    string          `json:"versionTag,omitempty"`
	State         string          `json:"state"`
	Steps         []timelineStep  `json:"steps"`
	Frames        []timelineFrame `json:"frames"`
}

type instanceResp struct {
	Key              uint64         `json:"key"`
	ProcessDefKey    uint64         `json:"processDefKey"`
	ProcessID        string         `json:"processId"`
	Version          int32          `json:"version"`
	VersionTag       string         `json:"versionTag,omitempty"`
	ElementInstances int            `json:"elementInstances"`
	State            string         `json:"state"`
	CreatedAt        int64          `json:"createdAt,omitempty"`
	CompletedAt      int64          `json:"completedAt,omitempty"`
	CorrelationKey   string         `json:"correlationKey,omitempty"`
	Variables        []variableView `json:"variables"`
}

type statsResp struct {
	ActiveProcessInstances int `json:"activeProcessInstances"`
	ActiveElementInstances int `json:"activeElementInstances"`
}

type createInstanceResp struct {
	DefinitionKey uint64    `json:"definitionKey"`
	Stats         statsResp `json:"stats"`
}

type cancelInstanceResp struct {
	InstanceKey uint64    `json:"instanceKey"`
	State       string    `json:"state"`
	Stats       statsResp `json:"stats"`
}

type cancelInstancesResp struct {
	DefinitionKey uint64    `json:"definitionKey"`
	Canceled      int       `json:"canceled"`
	Remaining     bool      `json:"remaining"` // the per-call cap was hit; call again to continue
	Stats         statsResp `json:"stats"`
}

type failJobReq struct {
	Retries int32  `json:"retries"`
	Message string `json:"message"`
}

type resolveIncidentReq struct {
	Retries int32 `json:"retries"`
}

type incidentView struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	JobKey             uint64 `json:"jobKey"`
	ElementId          int32  `json:"elementId"`
	RaisedAt           int64  `json:"raisedAt"`
	Message            string `json:"message"`
}

// handleInfo reports product/version metadata for the UI shell.
type logsResp struct {
	Lines []string `json:"lines"`
}

// handleLogs returns the recent process-log tail so an operator can diagnose from
// the web UI without shell access. Logs can carry operational detail, so when auth
// is enforced it is admin-only; with auth off (open single-user mode) it is open
// like the rest of the API. Reports an empty list when no buffer was wired.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	lines := []string{}
	if s.logs != nil {
		lines = s.logs.Lines()
	}
	writeJSON(w, http.StatusOK, logsResp{Lines: lines})
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	b := buildInfo()
	writeJSON(w, http.StatusOK, infoResp{
		Product:   "Atlas",
		Version:   Version,
		Docs:      s.docsEnabled,
		Revision:  b.Revision,
		BuildTime: b.Time,
		Modified:  b.Modified,
		Go:        b.Go,
	})
}

type validateFeelReq struct {
	Expression string `json:"expression"`
}

type validateFeelResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleValidateFeel compiles a FEEL expression with the same engine deployment
// uses, so the Modeler can flag syntax/type errors as they're typed instead of
// only at deploy time. Unknown identifiers are allowed (they're process
// variables, discovered via CompileAuto) — only genuine parse/type errors fail.
//
// It is a pure compile: no state is read or written, so it runs off the
// single-writer loop (no s.do) and never touches the processor hot path — a
// read-only edit-time check, consistent with "compile, don't interpret"
// (ADR-0008).
func (s *Server) handleValidateFeel(w http.ResponseWriter, r *http.Request) {
	var req validateFeelReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxFeelBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// A blank expression is a no-op success: an empty field simply carries no
	// condition/script, which the editor treats as unset rather than an error.
	if strings.TrimSpace(req.Expression) == "" {
		writeJSON(w, http.StatusOK, validateFeelResp{OK: true})
		return
	}
	if _, err := expr.CompileAuto(req.Expression); err != nil {
		writeJSON(w, http.StatusOK, validateFeelResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, validateFeelResp{OK: true})
}

type evalFeelReq struct {
	Expression string         `json:"expression"`
	Variables  map[string]any `json:"variables"`
}

type evalFeelResp struct {
	OK     bool   `json:"ok"`
	Result string `json:"result"`
	Kind   string `json:"kind"`
	Error  string `json:"error,omitempty"`
}

// handleEvaluateFeel compiles and evaluates a FEEL expression against sample
// variables, so the Modeler's "Test expression" can show what an expression
// produces before deploying. A FEEL type error (number + string, division by
// zero, …) evaluates to null rather than erroring — reported faithfully as a
// null result. Like validation, it's a pure compile+eval over a caller-supplied
// scope: no engine state is read or written, so it runs off the single-writer
// loop and never touches the processor hot path (ADR-0008).
func (s *Server) handleEvaluateFeel(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxFeelBytes))
	dec.UseNumber() // keep numbers exact (json.Number) for FEEL's decimals
	var req evalFeelReq
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: "empty expression"})
		return
	}
	compiled, err := expr.CompileAuto(req.Expression)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	bindings, err := feelBindings(req.Variables)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	v, err := compiled.Eval(bindings)
	if err != nil {
		writeJSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	kind, b, text := expr.Classify(v)
	result := text
	switch kind {
	case expr.KindBool:
		result = strconv.FormatBool(b)
	case expr.KindNull:
		result = "null"
	}
	writeJSON(w, http.StatusOK, evalFeelResp{OK: true, Result: result, Kind: feelKindName(kind)})
}

// feelBindings converts the JSON sample variables into FEEL values. Numbers keep
// their exact text (json.Number) so decimals aren't mangled by float rounding.
// Objects and arrays bind as FEEL contexts and lists — the same contract as start
// variables (ADR-0037).
func feelBindings(in map[string]any) (map[string]expr.Value, error) {
	out := make(map[string]expr.Value, len(in))
	for name, raw := range in {
		switch x := raw.(type) {
		case nil:
			out[name] = expr.Null
		case bool:
			out[name] = expr.Bool(x)
		case string:
			out[name] = expr.String(x)
		case json.Number:
			out[name] = expr.FromStored(expr.KindNumber, false, x.String())
		case map[string]any, []any:
			out[name] = expr.FromJSON(x)
		default:
			return nil, fmt.Errorf("variable %q: unsupported value type %T", name, raw)
		}
	}
	return out, nil
}

// feelKindName maps a classified value kind to the label the UI shows.
func feelKindName(k expr.ValueKind) string {
	switch k {
	case expr.KindBool:
		return "boolean"
	case expr.KindNumber:
		return "number"
	case expr.KindString:
		return "string"
	case expr.KindJSON:
		return "json"
	default:
		return "null"
	}
}

// handleDeploy parses a BPMN XML body, compiles and deploys every executable
// process it contains — one for a plain model, several for a collaboration (one
// per pool) — and returns the assigned key/id/version for each. Each pool's
// process becomes its own runnable definition; the message flows between pools
// are the diagram's counterpart of the message events that link them at runtime
// (ADR-0023).
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}

	// Resolve the DMN model this diagram's business rule tasks need and bundle it,
	// so a decision authored in Atlas deploys together with its process instead of
	// model-less (which would create DMN jobs that can never evaluate and then fail
	// every future deploy). The reference records are read on the loop; resolving and
	// compiling the models — I/O + CPU — runs off it, like the project deploy.
	var (
		refs    []dmnRef
		loadErr error
	)
	s.do(func() { refs, loadErr = s.dmnrefs.loadAll() })
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	dmnXMLs, refuse, dmnErr := s.dmnForDeployBody(r.Context(), body, refs)
	if dmnErr != nil {
		writeError(w, http.StatusInternalServerError, "resolve dmn model: "+dmnErr.Error())
		return
	}
	if refuse != "" {
		writeError(w, http.StatusConflict, refuse)
		return
	}

	var (
		resp       deployResp
		compErr    error
		persistErr error
	)
	s.do(func() {
		var deployed []deployedProcess
		deployed, compErr, persistErr = s.deployModel(body, dmnXMLs, time.Now().Unix())
		if compErr != nil || persistErr != nil {
			return
		}
		resp = deployResp{
			Key:         deployed[0].Key,
			ProcessID:   deployed[0].ProcessID,
			Version:     deployed[0].Version,
			Deployments: deployed,
		}
	})
	switch {
	case compErr != nil:
		// A compile failure is a client error: the submitted model is invalid.
		writeError(w, http.StatusBadRequest, compErr.Error())
	case persistErr != nil:
		writeError(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// deployModel compiles and registers every executable process in one BPMN model
// — one for a plain model, several for a collaboration (one per pool) — persisting
// each before registering it (durable before visible, I2 / ADR-0019). It returns
// the deployed processes, or a compile error (a client error: the model is
// invalid) or a persist error (a server error), the two failure modes the deploy
// handlers distinguish. It MUST be called on the run-loop goroutine (inside do),
// since it mutates the deployment registry and the processor.
//
// A mid-model persist failure leaves earlier processes deployed (no rollback
// yet) — an honest limitation until deployment is a first-class WAL event.
//
// dmnXML is the resolved DMN model this model's business rule tasks evaluate
// against (nil when there are none). It is snapshotted into each process's
// deployment record and registered in the DMN registry under the process key, so
// the tasks run now and re-register on restart (ADR-0014/ADR-0034). The caller is
// responsible for having validated it; a compile failure here is a server error.
func (s *Server) deployModel(body []byte, dmnXMLs [][]byte, deployedAt int64) (deployed []deployedProcess, compErr, persistErr error) {
	deployables, err := compiler.ParseAll(s.nextKey, 1, bytes.NewReader(body))
	if err != nil {
		return nil, err, nil
	}
	dmnStrings := make([]string, len(dmnXMLs))
	for i, x := range dmnXMLs {
		dmnStrings[i] = string(x)
	}
	deployed = make([]deployedProcess, 0, len(deployables))
	for i := range deployables {
		cp := deployables[i].Process
		pid := cp.Intern(cp.BpmnProcessId)
		version := s.versions[pid] + 1
		cp.Version = version
		key := cp.Key // ParseAll assigned s.nextKey+i in document order
		// A pool's name labels its process; fall back to the process's own name.
		name := deployables[i].PoolName
		if name == "" {
			name = deployables[i].ProcessName
		}

		if err := s.deploys.save(persistedDeployment{
			Key:        key,
			ProcessID:  pid,
			Name:       name,
			Version:    version,
			DeployedAt: deployedAt,
			XML:        string(body),
			DMNXMLs:    dmnStrings,
		}); err != nil {
			return deployed, nil, err
		}

		s.versions[pid] = version
		s.proc.Deploy(cp)
		// Arm this fresh version's timer start events and supersede any the prior
		// version left running, so the process starts on its schedule (ADR-0051).
		// Only fresh deploys arm; loadDeployments (recovery) restores armed timers
		// from the log instead. Skip it for a first-version process with no timer
		// start events — the common case — so no timer scan runs; a re-version may
		// need to supersede a prior version's schedule even if it has none itself.
		if version > 1 || len(cp.TimerStartEvents()) > 0 {
			s.proc.ArmStartTimers(cp.Key)
		}
		// Register the process's decisions so its business rule tasks can evaluate.
		// A process may bundle several models (its tasks span models); register each.
		for _, dmnXML := range dmnXMLs {
			if err := s.dmnRegistry.Deploy(key, dmnXML); err != nil {
				return deployed, nil, fmt.Errorf("register dmn model for %s: %w", pid, err)
			}
		}
		s.deployments[key] = &deployment{
			Key:        key,
			ProcessID:  pid,
			Name:       name,
			Version:    version,
			DeployedAt: deployedAt,
			xml:        body,
			cp:         cp,
		}
		s.order = append(s.order, key)
		if key >= s.nextKey {
			s.nextKey = key + 1
		}
		deployed = append(deployed, deployedProcess{Key: key, ProcessID: pid, Name: name, Version: version})
	}
	// Run the arm commands queued above (ADR-0051) so a timer start event's durable
	// timer is created and fsynced as part of the deploy, before it is acknowledged.
	// A no-op for a model with no timer start events.
	if err := s.jobRunner.Drive(); err != nil {
		return deployed, nil, err
	}
	return deployed, nil, nil
}

// handleListProcesses lists deployed definitions in registration order.
func (s *Server) handleListProcesses(w http.ResponseWriter, _ *http.Request) {
	list := []processResp{}
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			list = append(list, processResp{
				Key:              d.Key,
				ProcessID:        d.ProcessID,
				Name:             d.Name,
				Version:          d.Version,
				DeployedAt:       d.DeployedAt,
				CollaborationKey: s.collaborationKeyOf(d),
				StartFormID:      d.cp.StartFormId(),
				Executable:       d.cp.IsExecutable(),
				VersionTag:       d.cp.VersionTag(),
			})
		}
	})
	writeJSON(w, http.StatusOK, list)
}

// handleProcessXML returns a deployed definition's BPMN XML for the browser to
// render. If the model carries no diagram layout, a generated left-to-right
// layout is injected so it still renders (ensureDiagramLayout).
func (s *Server) handleProcessXML(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var raw []byte
	s.do(func() {
		if d, ok := s.deployments[key]; ok {
			raw = d.xml
		}
	})
	if raw == nil {
		writeError(w, http.StatusNotFound, "no deployment with that key")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(ensureDiagramLayout(raw))
}

// handleDeleteProcess removes a deployed definition (one version). It refuses if
// the definition still has running instances, since a live instance resolves its
// definition by key on every batch.
func (s *Server) handleDeleteProcess(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found      bool
		running    int
		scanErr    error
		persistErr error
	)
	s.do(func() {
		if _, ok := s.deployments[key]; !ok {
			return
		}
		found = true
		scanErr = s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey == key {
				running++
			}
			return nil
		})
		if scanErr != nil || running > 0 {
			return
		}
		// Durable before visible (I2, ADR-0019): remove the on-disk record first,
		// so a deletion that is acknowledged never reappears on restart.
		if err := s.deploys.delete(key); err != nil {
			persistErr = err
			return
		}
		s.proc.Undeploy(key)
		delete(s.deployments, key)
		for i, k := range s.order {
			if k == key {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "check instances: "+scanErr.Error())
	case running > 0:
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot delete: %d running instance(s); cancel them first", running))
	case persistErr != nil:
		writeError(w, http.StatusInternalServerError, "remove deployment: "+persistErr.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleProcessRuntime returns, for one definition, how many instances are live
// and how many tokens (element instances) currently sit on each BPMN element —
// the data the browser overlays onto the diagram. An optional ?instance=<key>
// filter narrows the result to a single process instance, so the live view can
// isolate one instance on the diagram instead of aggregating all of them.
func (s *Server) handleProcessRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	// instanceFilter == 0 means "all instances"; instance keys are never 0.
	var instanceFilter uint64
	if q := r.URL.Query().Get("instance"); q != "" {
		instanceFilter, err = strconv.ParseUint(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid instance key")
			return
		}
	}
	var (
		found   bool
		scanErr error
		resp    = runtimeResp{Elements: []runtimeElement{}}
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true

		byElement := map[string]*runtimeElement{}
		var order []string
		// get returns the accumulator for an element index, creating it (and
		// recording its position) on first sight. Both the live-token scan and the
		// visit-history scan funnel through it, so an element carries its live and
		// historical counts on one entry.
		get := func(elementId int32) *runtimeElement {
			bid := d.cp.ElementBpmnId(elementId)
			if bid == "" {
				return nil
			}
			e := byElement[bid]
			if e == nil {
				e = &runtimeElement{ElementID: bid, Type: d.cp.Node(elementId).Type.String()}
				byElement[bid] = e
				order = append(order, bid)
			}
			return e
		}

		if instanceFilter == 0 {
			// Aggregate over the whole definition: read the maintained per-element
			// and per-definition counters (ADR-0080). This is O(elements), never a
			// scan over every instance, so the view stays responsive — and the run
			// loop unblocked — at hundreds of thousands of instances. The reads nest
			// so a failure short-circuits to the single error check below.
			if scanErr = s.store.ElementLiveTokens(key, func(elementId int32, count int64) error {
				if count == 0 {
					return nil
				}
				if e := get(elementId); e != nil {
					e.Tokens += int(count)
					resp.Tokens += int(count)
				}
				return nil
			}); scanErr == nil {
				if scanErr = s.store.ElementVisitTotals(key, func(elementId int32, count int64) error {
					if e := get(elementId); e != nil {
						e.Visits += int(count)
					}
					return nil
				}); scanErr == nil {
					resp.Instances, scanErr = s.store.DefInstanceCount(key)
				}
			}
		} else {
			// Isolating one instance on the diagram (a deliberate single-instance
			// action, not the default view): the overlay walks instances filtered to
			// this one. Making this path sublinear too is a follow-up to ADR-0080 (the
			// aggregate default view above is what mattered for scale).
			if scanErr = s.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
				if v.ProcessDefKey != key || v.ProcessInstanceKey != instanceFilter {
					return nil
				}
				if e := get(v.ElementId); e != nil {
					e.Tokens++
					resp.Tokens++
				}
				return nil
			}); scanErr == nil {
				if scanErr = s.store.ElementVisitHistory(key, instanceFilter, func(elementId int32, count int64) error {
					if e := get(elementId); e != nil {
						e.Visits += int(count)
					}
					return nil
				}); scanErr == nil {
					scanErr = s.store.ActiveProcessInstances(func(piKey uint64, v *model.ProcessInstanceValue) error {
						if v.ProcessDefKey == key && piKey == instanceFilter {
							resp.Instances++
						}
						return nil
					})
				}
			}
		}
		if scanErr != nil {
			return
		}
		for _, bid := range order {
			resp.Elements = append(resp.Elements, *byElement[bid])
		}
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "read runtime: "+scanErr.Error())
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleCollaborationRuntime returns the runtime of a whole collaboration for the
// replay view: its pools, the live-token/visited overlay merged across every pool
// onto the shared diagram, and the message flows that crossed between them in the
// order they occurred (the replay timeline, ADR-0038). The path key may be any
// pool of the collaboration; the sibling pools are discovered from the shared XML.
func (s *Server) handleCollaborationRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found   bool
		scanErr error
		resp    = collabRuntimeResp{Pools: []collabPool{}, Elements: []runtimeElement{}, MessageFlows: []collabFlow{}}
	)
	// Flows are collected across pools then sorted into one timeline; ts leads,
	// with the log position as a stable tiebreaker for same-nanosecond flows.
	type tsFlow struct {
		ts  int64
		pos uint64
		f   collabFlow
	}
	var flows []tsFlow
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		pools := s.poolSiblings(d)

		// scan runs one store scan, keeping the first error so later scans on a
		// broken store become no-ops instead of masking it.
		scan := func(fn func() error) {
			if scanErr == nil {
				scanErr = fn()
			}
		}
		byElement := map[string]*runtimeElement{}
		var order []string
		for _, pd := range pools {
			pd := pd
			resp.Pools = append(resp.Pools, collabPool{Key: pd.Key, ProcessID: pd.ProcessID, Name: pd.Name, Version: pd.Version})
			// Resolve this pool's element indices against its own compiled process;
			// the shared diagram's ids are globally unique, so pools merge cleanly.
			get := func(elementId int32) *runtimeElement {
				bid := pd.cp.ElementBpmnId(elementId)
				if bid == "" {
					return nil
				}
				e := byElement[bid]
				if e == nil {
					e = &runtimeElement{ElementID: bid, Type: pd.cp.Node(elementId).Type.String()}
					byElement[bid] = e
					order = append(order, bid)
				}
				return e
			}
			scan(func() error {
				return s.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
					if v.ProcessDefKey != pd.Key {
						return nil
					}
					if e := get(v.ElementId); e != nil {
						e.Tokens++
						resp.Tokens++
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.ElementVisitHistory(pd.Key, 0, func(elementId int32, count int64) error {
					if e := get(elementId); e != nil {
						e.Visits += int(count)
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
					if v.ProcessDefKey == pd.Key {
						resp.Instances++
					}
					return nil
				})
			})
			scan(func() error {
				return s.store.MessageFlowHistory(pd.Key, func(ts int64, pos uint64, v *model.MessageFlowValue) error {
					flows = append(flows, tsFlow{ts: ts, pos: pos, f: collabFlow{
						At:                ts,
						MessageName:       v.MessageName,
						CorrelationKey:    v.CorrelationKey,
						ReceiverElementID: pd.cp.ElementBpmnId(v.ReceiverElementId),
						SenderInstance:    v.SenderProcessInstanceKey,
						ReceiverInstance:  v.ReceiverProcessInstanceKey,
					}})
					return nil
				})
			})
		}
		if scanErr != nil {
			return
		}
		for _, bid := range order {
			resp.Elements = append(resp.Elements, *byElement[bid])
		}
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "read collaboration runtime: "+scanErr.Error())
	default:
		sort.Slice(flows, func(i, j int) bool {
			if flows[i].ts != flows[j].ts {
				return flows[i].ts < flows[j].ts
			}
			return flows[i].pos < flows[j].pos
		})
		for _, tf := range flows {
			resp.MessageFlows = append(resp.MessageFlows, tf.f)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleInstanceTimeline returns one process instance's step-by-step replay: the
// ordered elements a token activated over the instance's life, each with its
// diagram id, type, and event timestamp (ADR-0046). The instance is resolved
// whether it is still running or already finished, and its element indices are
// mapped to diagram ids via its definition's compiled process. This is the
// single-process analogue of the collaboration message-flow timeline: the browser
// steps the token through the elements in order.
func (s *Server) handleInstanceTimeline(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	var (
		foundInstance bool
		foundDef      bool
		scanErr       error
		resp          = instanceTimelineResp{InstanceKey: key, Steps: []timelineStep{}, Frames: []timelineFrame{}}
	)
	s.do(func() {
		pi, ok, err := s.store.ProcessInstance(key)
		if err != nil {
			scanErr = err
			return
		}
		if !ok {
			return
		}
		foundInstance = true
		resp.ProcessDefKey = pi.ProcessDefKey
		resp.State = pi.State.String()

		d, ok := s.deployments[pi.ProcessDefKey]
		if !ok {
			// The instance outlived its deployment (definition deleted); its element
			// indices can no longer be mapped to a diagram.
			return
		}
		foundDef = true
		resp.ProcessID = d.ProcessID
		resp.Version = d.Version
		if d.cp != nil {
			resp.VersionTag = d.cp.VersionTag()
		}

		// Collect the element steps and the variable changes, each with its log
		// position, then fold the variables into the steps below.
		type stepRow struct {
			at        int64
			pos       uint64
			elementID int32
		}
		type varChange struct {
			pos    uint64
			endPos uint64 // last position the change's scope is live (^0 for the root scope)
			view   variableView
		}
		var (
			stepRows []stepRow
			changes  []varChange
		)
		type replayRow struct {
			at  int64
			pos uint64
			v   state.ElementReplayValue
		}
		var replayRows []replayRow
		scanErr = s.store.ElementStepHistory(key, func(ts int64, pos uint64, elementId int32) error {
			stepRows = append(stepRows, stepRow{at: ts, pos: pos, elementID: elementId})
			return nil
		})
		if scanErr == nil {
			scanErr = s.store.ElementReplayHistory(key, func(ts int64, pos uint64, v state.ElementReplayValue) error {
				replayRows = append(replayRows, replayRow{ts, pos, v})
				return nil
			})
		}
		// Variable snapshots are scope-aware: the process-instance (root) scope plus
		// each embedded subprocess scope of this instance, so a subprocess's local
		// variables (its input mappings) are visible in the replay, labeled by their
		// subprocess. A local-scope drop is not snapshotted (ADR-0068), so a local is
		// bounded to its subprocess's lifetime — shown only for steps at or before the
		// subprocess completes (ADR-0074). endPos ^0 = never expires (the root scope).
		const noEnd = ^uint64(0)
		type scopeSpan struct {
			scopeKey uint64
			label    string
			endPos   uint64
		}
		subScopes := map[uint64]*scopeSpan{}
		var subScopeList []*scopeSpan
		for _, rr := range replayRows {
			if d.cp.Node(rr.v.ElementID).Type != compiler.TypeSubProcess {
				continue
			}
			if rr.v.Action == 1 { // the subprocess scope opened
				sp := &scopeSpan{scopeKey: rr.v.ElementInstanceKey, label: d.cp.ElementBpmnId(rr.v.ElementID), endPos: noEnd}
				subScopes[rr.v.ElementInstanceKey] = sp
				subScopeList = append(subScopeList, sp)
			} else if sp := subScopes[rr.v.ElementInstanceKey]; sp != nil { // it completed/terminated
				sp.endPos = rr.pos
			}
		}
		if scanErr == nil {
			scanErr = s.store.VariableSnapshotHistory(key, func(_ int64, pos uint64, v *model.VariableValue) error {
				changes = append(changes, varChange{pos: pos, endPos: noEnd, view: toVariableView(v)})
				return nil
			})
		}
		for _, sp := range subScopeList {
			if scanErr != nil {
				break
			}
			scanErr = s.store.VariableSnapshotHistory(sp.scopeKey, func(_ int64, pos uint64, v *model.VariableValue) error {
				view := toVariableView(v)
				view.Scope = sp.label
				changes = append(changes, varChange{pos: pos, endPos: sp.endPos, view: view})
				return nil
			})
		}
		if scanErr != nil {
			return
		}
		// Both histories are keyed by log position; sort by it so the fold walks
		// them in true execution order regardless of clock monotonicity.
		sort.Slice(stepRows, func(i, j int) bool { return stepRows[i].pos < stepRows[j].pos })
		sort.Slice(changes, func(i, j int) bool { return changes[i].pos < changes[j].pos })
		sort.Slice(replayRows, func(i, j int) bool { return replayRows[i].pos < replayRows[j].pos })

		// Fold the causal lifecycle facts into stable multi-token frames. The
		// processor is sequential, so a token's completion on one element and the
		// activation it causes on the next land at different log positions. Emitting
		// a frame per row would show the token vanish between the two — a flicker,
		// and at a parallel join the merge would appear to lose an arrival. Instead a
		// non-leaf completion is *deferred*: the token stays visible on the completed
		// element until the activation it causes appears, at which point it moves.
		// The link is the graph, not a guessed token id: an activation arriving via
		// flow F is the successor of whatever completed on F's source node, so a join
		// (whose continuation leaves on one flow whose source is the gateway)
		// consumes every waiting arrival in a single transition. Only a leaf
		// completion (an end event, no outgoing flow) removes its token at once,
		// yielding the one legitimate empty frame that marks the instance done.
		active := map[uint64]timelineToken{}
		pending := map[uint64]state.ElementReplayValue{} // completions awaiting their successor
		activations := map[uint64]state.ElementReplayValue{}
		endAt := map[uint64]int64{} // element instance key → completion timestamp (Action==2)
		emitFrame := func(pos uint64, at int64) {
			tokens := make([]timelineToken, 0, len(active))
			for _, token := range active {
				tokens = append(tokens, token)
			}
			sort.Slice(tokens, func(i, j int) bool {
				if tokens[i].TokenID != tokens[j].TokenID {
					return tokens[i].TokenID < tokens[j].TokenID
				}
				return tokens[i].ElementInstanceKey < tokens[j].ElementInstanceKey
			})
			resp.Frames = append(resp.Frames, timelineFrame{Position: pos, At: at, Tokens: tokens})
		}
		for _, rr := range replayRows {
			v := rr.v
			if v.Action == 1 {
				activations[rr.pos] = v
				// This activation is the successor of any deferred completion on its
				// incoming flow's source node: those tokens move into it now.
				if v.SourceFlowID >= 0 {
					src := d.cp.Flow(v.SourceFlowID).Source
					for eik, pc := range pending {
						if pc.ElementID == src {
							delete(pending, eik)
							delete(active, eik)
						}
					}
				}
				node := d.cp.Node(v.ElementID)
				stateName := "active"
				if node.Type == compiler.TypeParallelGateway && node.IncomingCount > 1 {
					stateName = "waiting"
				}
				active[v.ElementInstanceKey] = timelineToken{TokenID: v.TokenID, ElementID: d.cp.ElementBpmnId(v.ElementID), ElementInstanceKey: v.ElementInstanceKey, State: stateName}
				emitFrame(rr.pos, rr.at)
			} else if d.cp.Node(v.ElementID).OutgoingCount == 0 {
				// A leaf has no successor to move into: remove it at once.
				endAt[v.ElementInstanceKey] = rr.at
				delete(pending, v.ElementInstanceKey)
				delete(active, v.ElementInstanceKey)
				emitFrame(rr.pos, rr.at)
			} else {
				// Defer: keep the token visible until its successor activates.
				endAt[v.ElementInstanceKey] = rr.at
				pending[v.ElementInstanceKey] = v
			}
		}

		// Walk the steps in order, advancing through every variable change at or
		// before each step's position, so a step carries the variables as they stood
		// when the token entered that element. A change overwrites the previous value
		// for the same (scope, name) — a subprocess-local shadows nothing here, it is a
		// distinct entry keyed by its scope. A local is only surfaced while its
		// subprocess is live (endPos), since its drop is not snapshotted (ADR-0074).
		type varKey struct{ scope, name string }
		type varEntry struct {
			view   variableView
			endPos uint64
		}
		running := map[varKey]varEntry{}
		ci := 0
		for _, sr := range stepRows {
			for ci < len(changes) && changes[ci].pos <= sr.pos {
				c := changes[ci]
				running[varKey{c.view.Scope, c.view.Name}] = varEntry{view: c.view, endPos: c.endPos}
				ci++
			}
			vars := make([]variableView, 0, len(running))
			for _, e := range running {
				if sr.pos <= e.endPos { // the variable's scope is still live at this step
					vars = append(vars, e.view)
				}
			}
			sort.Slice(vars, func(i, j int) bool {
				if vars[i].Scope != vars[j].Scope {
					return vars[i].Scope < vars[j].Scope // process (root) scope first
				}
				return vars[i].Name < vars[j].Name
			})
			// Every recorded step is a real activated node, so its diagram id is
			// always present (unlike the shared runtime overlay's get, which guards
			// synthetic elements).
			step := timelineStep{
				At:        sr.at,
				Position:  sr.pos,
				ElementID: d.cp.ElementBpmnId(sr.elementID),
				Type:      d.cp.Node(sr.elementID).Type.String(),
				Variables: vars,
				Action:    "activate",
			}
			if rv, ok := activations[sr.pos]; ok {
				step.TokenID, step.ElementInstanceKey = rv.TokenID, rv.ElementInstanceKey
				// The completion of this same element instance (Action==2) gives the
				// element's end time, so the history can show start → end per element
				// like Operate. Absent (still active / parked), endAt stays zero.
				step.EndAt = endAt[rv.ElementInstanceKey]
				// The activation's incoming flow identifies the element the token came
				// from (the flow's source node). The frontend animates the token dot
				// along that real edge — for a fork branch the predecessor is the
				// gateway, not the previous row in the linear step list.
				if rv.SourceFlowID >= 0 {
					step.SourceElementID = d.cp.ElementBpmnId(d.cp.Flow(rv.SourceFlowID).Source)
				}
				if rv.ParentTokenID != 0 {
					step.Relation = "fork"
				}
				if n := d.cp.Node(rv.ElementID); n.Type == compiler.TypeParallelGateway && n.IncomingCount > 1 {
					step.Relation = "join-arrival"
				}
			}
			resp.Steps = append(resp.Steps, step)
		}
	})
	switch {
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "read timeline: "+scanErr.Error())
	case !foundInstance:
		writeError(w, http.StatusNotFound, "no instance with that key")
	case !foundDef:
		writeError(w, http.StatusNotFound, "instance's definition is no longer deployed")
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleCreateInstance starts one instance of a deployed definition, optionally
// seeded with variables from the request body ({"variables": {"amount": 100}}),
// runs the processor until idle, and returns the resulting live counts.
func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	startVars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		found   bool
		notExec bool
		runErr  error
		statErr error
		stats   statsResp
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
		if err := s.jobRunner.Drive(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case notExec:
		writeError(w, http.StatusConflict, "process is not executable and cannot be started")
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "run instance: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, createInstanceResp{DefinitionKey: key, Stats: stats})
	}
}

// parseStartVariables reads {"variables": {name: value}} from a request body
// into VariableValues. Scalars (number, string, boolean, null) are stored
// directly; numbers keep their exact textual form for FEEL's decimal semantics.
// Objects and arrays are stored as canonical JSON under VarJSON, so they bind
// back into FEEL as contexts and lists for property access (ADR-0037).
func parseStartVariables(body []byte) ([]model.VariableValue, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var payload struct {
		Variables map[string]any `json:"variables"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	return startVarsFromMap(payload.Variables)
}

// startVarsFromMap converts a name→value map (values in the encoding/json shape
// produced with UseNumber: nil, bool, json.Number, string, map, slice) into
// VariableValues. Scalars are stored directly, keeping a number's exact textual
// form for FEEL's decimal semantics; objects and arrays are stored as canonical
// JSON under VarJSON, so they bind back into FEEL as contexts and lists
// (ADR-0037). Shared by the JSON start body and the CSV upload path (ADR-0084).
func startVarsFromMap(vars map[string]any) ([]model.VariableValue, error) {
	if len(vars) == 0 {
		return nil, nil
	}
	out := make([]model.VariableValue, 0, len(vars))
	for name, raw := range vars {
		vv := model.VariableValue{Name: name}
		switch x := raw.(type) {
		case nil:
			vv.Kind = model.VarNull
		case bool:
			vv.Kind, vv.Bool = model.VarBool, x
		case json.Number:
			vv.Kind, vv.Text = model.VarNumber, x.String()
		case string:
			vv.Kind, vv.Text = model.VarString, x
		case map[string]any, []any:
			text, ok := expr.ToJSON(expr.FromJSON(x))
			if !ok {
				return nil, fmt.Errorf("variable %q: value is not encodable as JSON", name)
			}
			vv.Kind, vv.Text = model.VarJSON, text
		default:
			return nil, fmt.Errorf("variable %q: unsupported value type %T", name, raw)
		}
		out = append(out, vv)
	}
	return out, nil
}

// variableView renders a variable for the operator UI.
type variableView struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Kind  string `json:"kind"`
	// Scope labels a subprocess-local variable with the id of the embedded
	// subprocess it lives in; empty for a process (root) scope variable (ADR-0074).
	Scope string `json:"scope,omitempty"`
}

func toVariableView(v *model.VariableValue) variableView {
	out := variableView{Name: v.Name}
	switch v.Kind {
	case model.VarBool:
		out.Kind = "boolean"
		if v.Bool {
			out.Value = "true"
		} else {
			out.Value = "false"
		}
	case model.VarNumber:
		out.Kind, out.Value = "number", v.Text
	case model.VarString:
		out.Kind, out.Value = "string", v.Text
	case model.VarJSON:
		out.Kind, out.Value = "json", v.Text
	default:
		out.Kind, out.Value = "null", "null"
	}
	return out
}

// nativeVar converts a stored variable into its native JSON value, so a form
// (or any client) receives real types: a number as a number, an object/array as
// itself, not stringified. The number and JSON canonical strings are emitted
// verbatim via json.Number / json.RawMessage.
func nativeVar(v *model.VariableValue) any {
	switch v.Kind {
	case model.VarBool:
		return v.Bool
	case model.VarNumber:
		return json.Number(v.Text)
	case model.VarString:
		return v.Text
	case model.VarJSON:
		return json.RawMessage(v.Text)
	default:
		return nil // VarNull
	}
}

// handleInstanceVariables returns a process instance's variables as a typed JSON
// object ({"Name": "Patrick", ...}) — the shape the Tasks app feeds a bound form
// so a field whose key matches a variable is prefilled (ADR-0028). An instance
// with no variables (or an unknown key) yields an empty object, not a 404: the
// endpoint is a convenience read, not an existence check.
func (s *Server) handleInstanceVariables(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := map[string]any{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.VariablesOfScope(key, func(v *model.VariableValue) error {
			out[v.Name] = nativeVar(v)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "read variables: "+scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// dataObjectView renders a data object for the operator UI: its name, its BPMN
// data state (the [received]/[approved] label), and its typed value. The value/kind
// mirror a variable's; state is what a variable has not — the per-datum lifecycle
// Atlas puts front and center (ADR-0053).
type dataObjectView struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Value any    `json:"value"`
	Kind  string `json:"kind"`
}

func toDataObjectView(v *model.DataObjectValue) dataObjectView {
	out := dataObjectView{Name: v.Name, State: v.State}
	switch v.Kind {
	case model.VarBool:
		out.Kind, out.Value = "boolean", v.Bool
	case model.VarNumber:
		out.Kind, out.Value = "number", json.Number(v.Text)
	case model.VarString:
		out.Kind, out.Value = "string", v.Text
	case model.VarJSON:
		out.Kind, out.Value = "json", json.RawMessage(v.Text)
	default:
		out.Kind, out.Value = "null", nil
	}
	return out
}

// handleInstanceDataObjects returns a process instance's data objects as a JSON
// array, each carrying its name, data state, and typed value — so an operator sees
// the data the process carries and what lifecycle state it is in (ADR-0053). The
// objects come back in name order (the store scans the family by name). An instance
// with no data objects (or an unknown key) yields an empty array, not a 404: like
// the variables endpoint, it is a convenience read, not an existence check.
func (s *Server) handleInstanceDataObjects(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := []dataObjectView{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.DataObjectsOfScope(key, func(v *model.DataObjectValue) error {
			out = append(out, toDataObjectView(v))
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "read data objects: "+scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// decisionEvaluationView renders one DMN decision evaluation for the operator UI
// (ADR-0066): when it ran, which business rule task on the diagram made it, which
// decision it evaluated, and — the point of the record — the input context it saw,
// the outputs it produced, and the temis trace explaining which rules fired. The
// three payloads are already canonical JSON in the store, so they pass through as
// raw JSON rather than being re-encoded. Trace is omitted when the decision produced
// none (a literal-expression decision, or a remote decision returning no trace).
type decisionEvaluationView struct {
	At         int64           `json:"at"`
	ElementID  string          `json:"elementId"`
	DecisionID string          `json:"decisionId"`
	Inputs     json.RawMessage `json:"inputs"`
	Outputs    json.RawMessage `json:"outputs"`
	Trace      json.RawMessage `json:"trace,omitempty"`
}

// rawJSONOr returns s as raw JSON, or fallback when s is empty — so a view field
// declared as JSON never carries an invalid empty document.
func rawJSONOr(s, fallback string) json.RawMessage {
	if s == "" {
		return json.RawMessage(fallback)
	}
	return json.RawMessage(s)
}

// handleInstanceDecisions returns the DMN decision evaluations a process instance
// made, in evaluation order, each with its inputs, outputs, and trace (ADR-0066).
// It is the "look up after the fact how a decision was made" surface: because the
// evaluations are durable history, it works while the instance runs and after it
// has finished. An instance that evaluated no decisions (or an unknown key) yields
// an empty array, not a 404 — like the variables and data-objects endpoints, it is
// a convenience read, not an existence check. The element id is mapped to its BPMN
// diagram id via the instance's compiled process; if the definition has since been
// deleted, the id is left empty (the diagram can no longer be resolved).
func (s *Server) handleInstanceDecisions(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := []decisionEvaluationView{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.DecisionEvaluationHistory(key, func(ts int64, _ uint64, v *model.DecisionEvaluationValue) error {
			view := decisionEvaluationView{
				At:         ts,
				DecisionID: v.DecisionId,
				Inputs:     rawJSONOr(v.InputsJSON, "{}"),
				Outputs:    rawJSONOr(v.OutputsJSON, "{}"),
			}
			if v.TraceJSON != "" {
				view.Trace = json.RawMessage(v.TraceJSON)
			}
			if d, ok := s.deployments[v.ProcessDefKey]; ok {
				view.ElementID = d.cp.ElementBpmnId(v.ElementId)
			}
			out = append(out, view)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "read decisions: "+scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListInstances lists process instances — live ones (with their current
// token count) followed by finished ones from the history index, most recently
// completed first (ADR-0017). It is the operator "instances" view.
// errListTruncated stops a bounded list scan once the page cap is reached. It is a
// sentinel to break the scan early, not a failure.
var errListTruncated = errors.New("list page full")

// unlessTruncated maps a bounded-scan result to a real error: the page-full sentinel
// (a deliberate early stop) becomes nil, any other error passes through. It lets the
// capped list handlers report a genuine scan failure without treating truncation as one.
func unlessTruncated(err error) error {
	if errors.Is(err, errListTruncated) {
		return nil
	}
	return err
}

const (
	// maxInstanceListDefault and maxInstanceListMax bound how many active and how many
	// completed instances GET /api/v1/instances returns (each capped independently),
	// so the endpoint can never try to enrich and serialize hundreds of thousands of
	// rows — the shape that made the operations page unreachable during the reported
	// flood. Raise per request with ?limit= (up to the max); narrow to one definition
	// with ?process=. The overview reads per-definition counts from
	// /api/v1/instances/summary instead, so the cap does not skew its tallies.
	maxInstanceListDefault = 1000
	maxInstanceListMax     = 10000
)

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	limit := maxInstanceListDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > maxInstanceListMax {
		limit = maxInstanceListMax
	}
	var (
		filterDef uint64
		hasFilter bool
	)
	if q := strings.TrimSpace(r.URL.Query().Get("process")); q != "" {
		n, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid process key")
			return
		}
		filterDef, hasFilter = n, true
	}
	active := []instanceResp{}
	done := []instanceResp{}
	truncated := false
	var scanErr error
	s.do(func() {
		// Attach the definition's id/version and the scope's variables to a row.
		enrich := func(r *instanceResp, key uint64) error {
			if d, ok := s.deployments[r.ProcessDefKey]; ok {
				r.ProcessID = d.ProcessID
				r.Version = d.Version
				if d.cp != nil {
					r.VersionTag = d.cp.VersionTag()
				}
			}
			return s.store.VariablesOfScope(key, func(vv *model.VariableValue) error {
				r.Variables = append(r.Variables, toVariableView(vv))
				return nil
			})
		}

		err := s.store.ActiveProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			if hasFilter && v.ProcessDefKey != filterDef {
				return nil
			}
			if len(active) >= limit {
				truncated = true
				return errListTruncated // page full: stop enriching further rows
			}
			elements := 0
			if err := s.store.ElementInstancesOfProcess(key, func(uint64) error {
				elements++
				return nil
			}); err != nil {
				return err
			}
			r := instanceResp{
				Key:              key,
				ProcessDefKey:    v.ProcessDefKey,
				ElementInstances: elements,
				State:            "active",
				CreatedAt:        v.CreatedAt,
				CorrelationKey:   v.CorrelationKey,
				Variables:        []variableView{},
			}
			if err := enrich(&r, key); err != nil {
				return err
			}
			active = append(active, r)
			return nil
		})
		if err != nil && !errors.Is(err, errListTruncated) {
			scanErr = err
			return
		}

		err = s.store.CompletedProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			if hasFilter && v.ProcessDefKey != filterDef {
				return nil
			}
			if len(done) >= limit {
				truncated = true
				return errListTruncated
			}
			r := instanceResp{
				Key:            key,
				ProcessDefKey:  v.ProcessDefKey,
				State:          v.State.String(),
				CreatedAt:      v.CreatedAt,
				CompletedAt:    v.CompletedAt,
				CorrelationKey: v.CorrelationKey,
				Variables:      []variableView{},
			}
			if err := enrich(&r, key); err != nil {
				return err
			}
			done = append(done, r)
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list instances: "+scanErr.Error())
		return
	}
	// Finished instances: most recently completed first.
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	if truncated {
		// Signal that the page was capped so a client can page with ?process=/?limit=
		// rather than assume it received every instance.
		w.Header().Set("X-Instances-Truncated", "true")
	}
	writeJSON(w, http.StatusOK, append(active, done...))
}

type instanceSummaryRow struct {
	ProcessDefKey     uint64 `json:"processDefKey"`
	ProcessID         string `json:"processId"`
	Version           int32  `json:"version"`
	Active            int    `json:"active"`
	Completed         int    `json:"completed"`
	LatestCompletedAt int64  `json:"latestCompletedAt"`
}

// handleInstancesSummary returns per-definition instance counts (active and finished,
// plus the last-activity time) read from the maintained O(1) counters — one point read
// per deployed definition, no instance scan (ADR-0083, extending ADR-0080). This is
// what keeps the operations overview responsive even when a definition has hundreds of
// thousands of instances: the earlier scan-based version blocked the single-writer loop
// on every load (the reported flood), and draining active instances into the history
// only moved that cost rather than removing it.
func (s *Server) handleInstancesSummary(w http.ResponseWriter, _ *http.Request) {
	var out []instanceSummaryRow
	s.do(func() {
		out = make([]instanceSummaryRow, 0, len(s.order))
		for _, key := range s.order {
			d := s.deployments[key]
			// Each is one O(1) point read of a maintained counter (ADR-0083/0080); the
			// only failure mode is a catastrophic store error, and this is a display
			// aggregate — a counter that cannot be read is shown as 0 rather than failing
			// the whole overview.
			active, _ := s.store.DefInstanceCount(key)
			completed, _ := s.store.DefCompletedCount(key)
			lastAct, _ := s.store.DefLastActivity(key)
			out = append(out, instanceSummaryRow{
				ProcessDefKey:     key,
				ProcessID:         d.ProcessID,
				Version:           d.Version,
				Active:            active,
				Completed:         completed,
				LatestCompletedAt: lastAct,
			})
		}
	})
	writeJSON(w, http.StatusOK, out)
}

// maxInstanceSearchResults caps a variable search so a single query can't return
// an unbounded response on a large deployment. The search is a full scan (v1, no
// index); the cap keeps the answer size and scan cost bounded — active instances
// first, then finished ones most-recently-completed first, so the newest matches
// survive truncation.
const maxInstanceSearchResults = 200

// varQuery is a parsed instance-variable search. Two shapes: a structured
// name=value match (variable name exact, value substring) when the query
// contains "="; otherwise a free-text substring over variable names and values.
// All comparisons are case-insensitive.
type varQuery struct {
	structured bool
	name       string // lower-cased variable name; structured only
	needle     string // lower-cased substring (value in structured, term in free-text)
}

// parseVarQuery interprets a raw ?q= value. ok is false for a blank query (the
// caller returns an empty result set rather than scanning everything). A query
// like "=value" with no name degrades to free text — an empty exact name can
// never match, so treating it structurally would be a silent dead end.
func parseVarQuery(q string) (varQuery, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return varQuery{}, false
	}
	if i := strings.IndexByte(q, '='); i >= 0 {
		if name := strings.TrimSpace(q[:i]); name != "" {
			return varQuery{
				structured: true,
				name:       strings.ToLower(name),
				needle:     strings.ToLower(strings.TrimSpace(q[i+1:])),
			}, true
		}
	}
	return varQuery{needle: strings.ToLower(q)}, true
}

// match reports whether a variable satisfies the query.
func (p varQuery) match(v variableView) bool {
	if p.structured {
		return strings.ToLower(v.Name) == p.name && strings.Contains(strings.ToLower(v.Value), p.needle)
	}
	return strings.Contains(strings.ToLower(v.Name), p.needle) || strings.Contains(strings.ToLower(v.Value), p.needle)
}

// handleSearchInstances finds process instances by the content of their
// variables — the operator "which instance had customerType=Business?" surface.
// It reuses the same live+history scan as handleListInstances, but keeps only
// instances with a variable matching ?q= and, on each row, only the matching
// variables (so the UI can highlight them). A blank query returns an empty list,
// not every instance. This is a full scan with no value index (v1): finished
// instances are searchable only while their scope's variables are retained, same
// as the instances list. Results are capped at maxInstanceSearchResults.
func (s *Server) handleSearchInstances(w http.ResponseWriter, r *http.Request) {
	pred, ok := parseVarQuery(r.URL.Query().Get("q"))
	if !ok {
		writeJSON(w, http.StatusOK, []instanceResp{})
		return
	}
	active := []instanceResp{}
	done := []instanceResp{}
	var scanErr error
	s.do(func() {
		// matchingVars returns the scope's variables that satisfy the query, or
		// nil if none do (so the caller can drop the instance).
		matchingVars := func(key uint64) ([]variableView, error) {
			var hits []variableView
			err := s.store.VariablesOfScope(key, func(vv *model.VariableValue) error {
				if view := toVariableView(vv); pred.match(view) {
					hits = append(hits, view)
				}
				return nil
			})
			return hits, err
		}
		enrichDef := func(r *instanceResp) {
			if d, ok := s.deployments[r.ProcessDefKey]; ok {
				r.ProcessID = d.ProcessID
				r.Version = d.Version
				if d.cp != nil {
					r.VersionTag = d.cp.VersionTag()
				}
			}
		}

		scanErr = s.store.ActiveProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			hits, err := matchingVars(key)
			if err != nil || len(hits) == 0 {
				return err
			}
			r := instanceResp{
				Key:            key,
				ProcessDefKey:  v.ProcessDefKey,
				State:          "active",
				CreatedAt:      v.CreatedAt,
				CorrelationKey: v.CorrelationKey,
				Variables:      hits,
			}
			enrichDef(&r)
			active = append(active, r)
			return nil
		})
		if scanErr != nil {
			return
		}

		scanErr = s.store.CompletedProcessInstances(func(key uint64, v *model.ProcessInstanceValue) error {
			hits, err := matchingVars(key)
			if err != nil || len(hits) == 0 {
				return err
			}
			r := instanceResp{
				Key:            key,
				ProcessDefKey:  v.ProcessDefKey,
				State:          v.State.String(),
				CreatedAt:      v.CreatedAt,
				CompletedAt:    v.CompletedAt,
				CorrelationKey: v.CorrelationKey,
				Variables:      hits,
			}
			enrichDef(&r)
			done = append(done, r)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "search instances: "+scanErr.Error())
		return
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	out := append(active, done...)
	if len(out) > maxInstanceSearchResults {
		out = out[:maxInstanceSearchResults]
	}
	writeJSON(w, http.StatusOK, out)
}

type publishMessageResp struct {
	Name           string    `json:"name"`
	CorrelationKey string    `json:"correlationKey"`
	Stats          statsResp `json:"stats"`
}

// handlePublishMessage publishes a message by name and correlation key, with
// optional payload variables, then runs the processor to idle so any waiting
// instance advances. It correlates against open subscriptions through the engine;
// a message that matches nothing is accepted as a no-op (no buffering yet,
// ADR-0020). Body: {"name","correlationKey","variables":{…}}.
func (s *Server) handlePublishMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name           string `json:"name"`
		CorrelationKey string `json:"correlationKey"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if payload.Name == "" {
		writeError(w, http.StatusBadRequest, "message name is required")
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		s.proc.PublishMessage(payload.Name, payload.CorrelationKey, vars...)
		if err := s.jobRunner.Drive(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "publish message: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, publishMessageResp{Name: payload.Name, CorrelationKey: payload.CorrelationKey, Stats: stats})
	}
}

// handleCancelInstance terminates a running process instance: it terminates
// every active element instance and records the instance as terminated in
// history, so it disappears from the running list and the live overlay and shows
// as "terminated" in the finished list. Useful for a stuck instance (e.g. one
// parked on a wait that will never complete). 404 if no active instance has the
// key. Returns the resulting live counts.
func (s *Server) handleCancelInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	var (
		found   bool
		scanErr error
		runErr  error
		statErr error
		stats   statsResp
	)
	s.do(func() {
		scanErr = s.store.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			if k == key {
				found = true
			}
			return nil
		})
		if scanErr != nil || !found {
			return
		}
		s.proc.CancelInstance(key)
		if err := s.jobRunner.Drive(); err != nil {
			runErr = err
			return
		}
		stats, statErr = s.readStats()
	})
	switch {
	case scanErr != nil:
		writeError(w, http.StatusInternalServerError, "find instance: "+scanErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no active instance with that key")
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "cancel instance: "+runErr.Error())
	case statErr != nil:
		writeError(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		writeJSON(w, http.StatusOK, cancelInstanceResp{InstanceKey: key, State: "terminated", Stats: stats})
	}
}

// errCancelBatchFull stops the active-instance scan once a bulk cancel has collected
// its per-call cap of instance keys. It is a sentinel to break the scan early, not a
// failure — the handler treats it as success.
var errCancelBatchFull = errors.New("cancel batch full")

const (
	// bulkCancelBatchDefault and bulkCancelBatchMax bound how many of a definition's
	// instances one POST .../cancel-instances call terminates. The cap keeps a single
	// call from holding the single-writer run loop while it terminates a runaway
	// backlog of hundreds of thousands of instances; the caller repeats while the
	// response reports remaining=true. This is the drain path for the reported
	// /employees flood, where per-instance cancellation is infeasible.
	bulkCancelBatchDefault = 5000
	bulkCancelBatchMax     = 50000
)

// handleCancelInstancesOfProcess terminates a bounded batch of a definition's running
// instances in one call: it scans the active process instances, cancels up to the
// per-call cap that belong to the definition, and reports how many it cancelled and
// whether the cap was hit (remaining=true → call again). All work happens in one run-
// loop turn so the terminations are atomic with the scan; the cap bounds the turn.
func (s *Server) handleCancelInstancesOfProcess(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	limit := bulkCancelBatchDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > bulkCancelBatchMax {
		limit = bulkCancelBatchMax
	}
	var (
		found     bool
		remaining bool
		keys      []uint64
		opErr     error // any scan / drive / stats failure inside the run loop
		stats     statsResp
	)
	s.do(func() {
		if _, ok := s.deployments[key]; !ok {
			return
		}
		found = true
		err := s.store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey != key {
				return nil
			}
			keys = append(keys, k)
			if len(keys) >= limit {
				return errCancelBatchFull // stop early: this batch is full
			}
			return nil
		})
		if err != nil && !errors.Is(err, errCancelBatchFull) {
			opErr = err
			return
		}
		remaining = errors.Is(err, errCancelBatchFull) // hit the cap → more may remain
		for _, k := range keys {
			s.proc.CancelInstance(k)
		}
		if opErr = s.jobRunner.Drive(); opErr != nil {
			return
		}
		stats, opErr = s.readStats()
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "cancel instances: "+opErr.Error())
	default:
		writeJSON(w, http.StatusOK, cancelInstancesResp{DefinitionKey: key, Canceled: len(keys), Remaining: remaining, Stats: stats})
	}
}

// terminateInstancesReq selects which running instances to terminate. Exactly one
// mode is used per call: Keys picks an explicit, hand-selected set (the operator
// ticked rows); ProcessDefKey (optionally narrowed by Query) terminates every active
// instance of one definition that matches — the "select all N matching" scope. Limit
// bounds a filter-mode call the way the bulk-drain endpoint does (repeat while the
// response reports remaining=true); it is ignored in keys mode, where the request is
// already the bound.
type terminateInstancesReq struct {
	Keys          []uint64 `json:"keys"`
	ProcessDefKey uint64   `json:"processDefKey"`
	Query         string   `json:"q"`
	Limit         int      `json:"limit"`
}

// terminateInstancesResp reports what a terminate call did: how many instances it
// terminated, how many requested keys had no active instance (keys mode only), and —
// for a filter-mode call that hit its per-call cap — whether more may remain.
type terminateInstancesResp struct {
	Terminated int       `json:"terminated"`
	NotFound   int       `json:"notFound"`
	Remaining  bool      `json:"remaining"`
	Stats      statsResp `json:"stats"`
}

// handleTerminateInstances terminates a selected set of running instances in one
// call. Two mutually exclusive modes: an explicit set of instance keys, or every
// active instance of a definition matching an optional variable query. It is the
// operator's bulk-terminate surface over the instances list; single-instance cancel
// (DELETE /instances/{key}) and the per-definition drain (cancel-instances) remain
// for their narrower uses. All terminations for a call happen in one run-loop turn so
// they are atomic with the scan/lookups; filter mode's Limit bounds that turn.
func (s *Server) handleTerminateInstances(w http.ResponseWriter, r *http.Request) {
	var req terminateInstancesReq
	if !decodeJSONBody(w, r, &req) {
		return
	}
	switch {
	case len(req.Keys) > 0 && req.ProcessDefKey != 0:
		writeError(w, http.StatusBadRequest, "specify either keys or processDefKey, not both")
		return
	case len(req.Keys) > 0:
		s.terminateByKeys(w, req.Keys)
	case req.ProcessDefKey != 0:
		s.terminateByFilter(w, req)
	default:
		writeError(w, http.StatusBadRequest, "want keys or processDefKey")
	}
}

// terminateByKeys terminates an explicit set of instance keys. Each key is checked
// with an O(1) point lookup (not a scan over the whole active set), so a hand-picked
// terminate stays cheap even under a flood; a key with no active instance — already
// finished, or never valid — is counted as notFound rather than failing the call.
// Duplicate keys in the request are collapsed. The request body limit already bounds
// the batch (a few thousand keys at most), which keeps one call from holding the run
// loop; the unbounded "drain everything" path is filter mode, which batches with
// remaining.
func (s *Server) terminateByKeys(w http.ResponseWriter, keys []uint64) {
	uniq := make(map[uint64]struct{}, len(keys))
	for _, k := range keys {
		uniq[k] = struct{}{}
	}
	var (
		terminated int
		opErr      error
		stats      statsResp
	)
	s.do(func() {
		active := make([]uint64, 0, len(uniq))
		for k := range uniq {
			v, ok, err := s.store.ProcessInstance(k)
			if err != nil {
				opErr = err
				return
			}
			// Only a record in the active keyspace (State PIActive) is terminable; a
			// key found only in history is already finished → notFound.
			if ok && v.State == model.PIActive {
				active = append(active, k)
			}
		}
		for _, k := range active {
			s.proc.CancelInstance(k)
		}
		terminated = len(active)
		if opErr = s.jobRunner.Drive(); opErr != nil {
			return
		}
		stats, opErr = s.readStats()
	})
	if opErr != nil {
		writeError(w, http.StatusInternalServerError, "terminate instances: "+opErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, terminateInstancesResp{
		Terminated: terminated,
		NotFound:   len(uniq) - terminated,
		Stats:      stats,
	})
}

// terminateByFilter terminates every active instance of one definition that matches
// the request's optional variable query, up to a per-call cap. A blank query matches
// all of the definition's active instances. Like the bulk-drain endpoint it reports
// remaining=true when the cap was hit, so the caller repeats until it clears.
func (s *Server) terminateByFilter(w http.ResponseWriter, req terminateInstancesReq) {
	limit := bulkCancelBatchDefault
	if req.Limit != 0 {
		if req.Limit < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = req.Limit
	}
	if limit > bulkCancelBatchMax {
		limit = bulkCancelBatchMax
	}
	// A blank query matches every active instance of the definition; otherwise only
	// those with a variable satisfying it (same matcher as the instances search).
	pred, hasQuery := parseVarQuery(req.Query)
	var (
		found     bool
		remaining bool
		keys      []uint64
		opErr     error
		stats     statsResp
	)
	s.do(func() {
		if _, ok := s.deployments[req.ProcessDefKey]; !ok {
			return
		}
		found = true
		err := s.store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey != req.ProcessDefKey {
				return nil
			}
			if hasQuery {
				matched := false
				if verr := s.store.VariablesOfScope(k, func(vv *model.VariableValue) error {
					if pred.match(toVariableView(vv)) {
						matched = true
					}
					return nil
				}); verr != nil {
					return verr
				}
				if !matched {
					return nil
				}
			}
			keys = append(keys, k)
			if len(keys) >= limit {
				return errCancelBatchFull // batch full: more may match, stop here
			}
			return nil
		})
		if err != nil && !errors.Is(err, errCancelBatchFull) {
			opErr = err
			return
		}
		remaining = errors.Is(err, errCancelBatchFull)
		for _, k := range keys {
			s.proc.CancelInstance(k)
		}
		if opErr = s.jobRunner.Drive(); opErr != nil {
			return
		}
		stats, opErr = s.readStats()
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "no deployment with that key")
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "terminate instances: "+opErr.Error())
	default:
		writeJSON(w, http.StatusOK, terminateInstancesResp{
			Terminated: len(keys),
			Remaining:  remaining,
			Stats:      stats,
		})
	}
}

// --- user tasks (ADR-0028) ---

// jobResp is one activatable job an instance is parked on: its key (what
// /jobs/{key}/complete takes), the instance and definition it belongs to, the BPMN
// element it sits on, and its interned job type. It is the read side of the
// operator complete/fail affordance.
type jobResp struct {
	Key                uint64 `json:"key"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	ProcessDefKey      uint64 `json:"processDefKey,omitempty"`
	ElementID          string `json:"elementId,omitempty"`
	JobType            string `json:"jobType,omitempty"`
	Retries            int32  `json:"retries"`
}

// handleListInstanceJobs lists the activatable jobs one instance is parked on,
// regardless of type — the read side of POST /jobs/{key}/complete. It mirrors
// handleListTasks but is scoped to one instance and not limited to user tasks, so
// a client that only speaks HTTP can discover the job keys of parked service tasks
// and finish them by hand. The scan runs on the run-loop goroutine (state's sole
// owner) via do.
func (s *Server) handleListInstanceJobs(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	jobs := []jobResp{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.AllActivatableJobs(func(jobKey uint64) error {
			jv, ok, err := s.store.GetJob(jobKey)
			if err != nil || !ok || jv.ProcessInstanceKey != key {
				return err // err is nil for the skip cases (missing job / other instance)
			}
			jr := jobResp{Key: jobKey, ProcessInstanceKey: jv.ProcessInstanceKey, Retries: jv.Retries}
			if ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey); err == nil && ok {
				jr.ProcessDefKey = ei.ProcessDefKey
				if d, dok := s.deployments[ei.ProcessDefKey]; dok {
					cp := d.cp
					jr.ElementID = cp.ElementBpmnId(ei.ElementId)
					jr.JobType = cp.Intern(jv.JobType)
				}
			}
			jobs = append(jobs, jr)
			return nil
		})
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list jobs: "+scanErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

type taskResp struct {
	Key                uint64 `json:"key"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	ProcessDefKey      uint64 `json:"processDefKey,omitempty"`
	ProcessID          string `json:"processId,omitempty"`
	ElementID          string `json:"elementId,omitempty"`
	Name               string `json:"name,omitempty"`
	Assignee           string `json:"assignee,omitempty"`
	CandidateGroups    string `json:"candidateGroups,omitempty"`
	FormID             string `json:"formId,omitempty"`
	// Priority is the task's importance from the model (default 50); the inbox
	// sorts by it. DueDate is the absolute due instant in Unix milliseconds, or 0
	// when the task has no due date (ADR-0051).
	Priority int32 `json:"priority"`
	DueDate  int64 `json:"dueDate,omitempty"`
}

// handleListTasks lists open user tasks — activatable jobs of the reserved
// user-task type. Each entry carries the task's key, the instance it belongs to,
// and the element's assignment metadata from the compiled process.
// maxTaskListDefault and maxTaskListMax bound how many user tasks GET /api/v1/tasks
// returns per call, so the inbox loads even when a definition has parked hundreds of
// thousands of instances on a user task (the reported flood): the scan stops at the
// cap instead of enriching and shipping every job. Raise per request with ?limit= (up
// to the max); a capped page is flagged with X-Tasks-Truncated.
const (
	maxTaskListDefault = 500
	maxTaskListMax     = 5000
)

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	limit := maxTaskListDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > maxTaskListMax {
		limit = maxTaskListMax
	}
	tasks := []taskResp{}
	truncated := false
	var scanErr error
	s.do(func() {
		err := s.store.ActivatableJobs(compiler.UserTaskJobTypeIndex, func(jobKey uint64) error {
			if len(tasks) >= limit {
				truncated = true
				return errListTruncated // page full: stop before enriching more jobs
			}
			jv, ok, err := s.store.GetJob(jobKey)
			if err != nil || !ok {
				return err
			}
			tasks = append(tasks, s.enrichTask(jobKey, jv))
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list tasks: "+scanErr.Error())
		return
	}
	if truncated {
		// Signal a capped page so a client can narrow (by process/assignee) rather than
		// assume it received every task.
		w.Header().Set("X-Tasks-Truncated", "true")
	}
	writeJSON(w, http.StatusOK, tasks)
}

// enrichTask turns a user-task job into the response row the inbox and the
// single-task lookup both return: the job's key and instance, plus the element's
// name, assignment metadata, form, priority and due date from the compiled process.
// It is the one place that shape is built, so the list and the by-key fetch can never
// drift. Callers run it inside s.do (it reads the store and the deployments map).
func (s *Server) enrichTask(jobKey uint64, jv *model.JobValue) taskResp {
	tr := taskResp{
		Key:                jobKey,
		ProcessInstanceKey: jv.ProcessInstanceKey,
	}
	if ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey); err == nil && ok {
		tr.ProcessDefKey = ei.ProcessDefKey
		if d, dok := s.deployments[ei.ProcessDefKey]; dok {
			tr.ProcessID = d.ProcessID
			cp := d.cp
			tr.ElementID = cp.ElementBpmnId(ei.ElementId)
			if n := cp.Node(ei.ElementId); n.Type == compiler.TypeUserTask {
				detail := cp.UserTask(n.Detail)
				tr.Name = cp.Intern(detail.Name)
				// The assignee is the job's runtime value (claim/unclaim rewrite it,
				// ADR-0042); candidate groups stay the compile-time attribute.
				tr.Assignee = jv.Assignee
				tr.CandidateGroups = cp.Intern(detail.CandidateGroups)
				tr.FormID = cp.Intern(detail.FormId)
				tr.Priority = detail.Priority
				// The due date is frozen on the job as an absolute instant
				// (nanoseconds); expose it as Unix ms for the browser.
				if jv.Deadline != 0 {
					tr.DueDate = jv.Deadline / int64(time.Millisecond)
				}
			}
		}
	}
	return tr
}

// handleGetTask returns one open user task by its job key, enriched the same way as a
// list row. It is what keeps a deep link (…/tasks/t/{key}, e.g. from the Operations
// live view) working when the task falls outside the capped task-list page during a
// flood: the client fetches the one task directly instead of scanning a bounded list
// for it. 404 if no open job has that key (never existed, or already completed).
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task key")
		return
	}
	var (
		task  taskResp
		found bool
		opErr error
	)
	s.do(func() {
		jv, ok, err := s.store.GetJob(key)
		if err != nil {
			opErr = err
			return
		}
		// A completed or unknown job leaves found=false → 404. Only genuine user-task
		// jobs are addressable here; anything else (a service-task job) is treated as
		// absent so this stays the inbox's read side, not a generic job peek.
		if !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
			return
		}
		found = true
		task = s.enrichTask(key, jv)
	})
	switch {
	case opErr != nil:
		writeError(w, http.StatusInternalServerError, "get task: "+opErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no open task with that key")
	default:
		writeJSON(w, http.StatusOK, task)
	}
}

// handleCompleteTask completes a user task by its job key: it feeds the job
// completion back to the processor (the same path a service-task worker uses)
// and drives any jobs that unblocked (e.g. a business rule task the completion
// flowed into) to idle. 404 if the job doesn't exist or is already completed.
// handleFailJob applies a worker's failure report for a job (ADR-0061): the body
// carries the remaining retries and a message. With retries > 0 the job is retried;
// with none an incident is raised on its element. 404 if the job doesn't exist.
func (s *Server) handleFailJob(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job key")
		return
	}
	var req failJobReq
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var (
		found  bool
		runErr error
	)
	s.do(func() {
		if _, ok, err := s.store.GetJob(key); err != nil || !ok {
			return
		}
		found = true
		s.proc.FailJob(key, req.Retries, req.Message)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "fail job: "+runErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no job with that key")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jobKey": key})
	}
}

// handleCompleteJob is the operator/manual counterpart to handleFailJob: it
// completes an activatable job by key, optionally writing the outputs a worker
// would have produced as {"variables": {...}} into the instance scope, then
// drives any jobs that unblocked. It runs the same processor path a service-task
// worker's CompleteJob takes, so it works for any job type. 404 if the job
// doesn't exist or is already completed.
//
// This is deliberately NOT the gRPC job-worker protocol (ADR-0007): there is no
// lease, fencing token, or at-least-once streaming — it is a synchronous
// operator affordance, the completion mirror of POST /jobs/{key}/fail from the
// incident model (ADR-0061), for finishing a parked service-task job by hand
// (e.g. when the external work was carried out out-of-band) until a real worker
// transport lands. Discovering the job key is out of scope here — that comes
// from runtime inspection.
func (s *Server) handleCompleteJob(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job key")
		return
	}
	// Variables are parsed (and rejected on bad JSON) before the job is looked
	// up, so a malformed body is a 400 regardless of whether the key exists —
	// matching handleCompleteTask. An empty body completes with no variables.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		found  bool
		runErr error
	)
	s.do(func() {
		if _, ok, err := s.store.GetJob(key); err != nil || !ok {
			return
		}
		found = true
		s.proc.CompleteJob(key, vars...)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "complete job: "+runErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no job with that key")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jobKey": key})
	}
}

// handleResolveIncident resolves the incident on an element instance and resumes
// its job (ADR-0061): the body's retries (default 1) is how many attempts the
// re-activated job gets. 404 if there is no incident on that element.
func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid element instance key")
		return
	}
	var req resolveIncidentReq
	if !decodeJSONBody(w, r, &req) {
		return
	}
	retries := req.Retries
	if retries < 1 {
		retries = 1
	}
	var (
		found  bool
		jobKey uint64
		runErr error
	)
	s.do(func() {
		inc, err := s.store.GetIncident(key)
		if err != nil || inc == nil {
			return
		}
		found = true
		jobKey = inc.JobKey
		s.proc.ResolveIncident(key, retries)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "resolve incident: "+runErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no incident on that element instance")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"elementInstanceKey": key, "jobKey": jobKey, "retries": retries})
	}
}

// handleListIncidents lists the unresolved incidents — the operator "what's stuck"
// view (ADR-0061).
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	limit := maxTaskListMax // incidents share the task list's ceiling; the default page is generous
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		if limit = n; limit > maxTaskListMax {
			limit = maxTaskListMax
		}
	}
	list := []incidentView{}
	truncated := false
	var scanErr error
	s.do(func() {
		err := s.store.Incidents(func(elKey uint64, v *model.IncidentValue) error {
			if len(list) >= limit {
				truncated = true
				return errListTruncated // page full: bound the response even under a flood of failures
			}
			list = append(list, incidentView{
				ElementInstanceKey: elKey,
				ProcessInstanceKey: v.ProcessInstanceKey,
				JobKey:             v.JobKey,
				ElementId:          v.ElementId,
				RaisedAt:           v.RaisedAt,
				Message:            v.Message,
			})
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		writeError(w, http.StatusInternalServerError, "list incidents: "+scanErr.Error())
		return
	}
	if truncated {
		w.Header().Set("X-Incidents-Truncated", "true")
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": list})
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task key")
		return
	}
	// A completion may carry the submitted form's data as {"variables": {...}},
	// written into the instance scope on completion exactly like a service-task
	// worker's outputs (ADR-0028; the engine path landed with ADR-0039). An empty
	// body completes with no variables.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		found  bool
		runErr error
	)
	s.do(func() {
		// A job that can't be read — absent, already completed, or unreadable — is
		// simply not an open task (reported 404 below); GetJob reports ok=false in
		// every such case, so there's no separate error path to plumb here.
		if _, ok, err := s.store.GetJob(key); err != nil || !ok {
			return
		}
		found = true
		s.proc.CompleteJob(key, vars...)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "complete task: "+runErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no open task with that key")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"taskKey": key})
	}
}

// handleClaimTask assigns an open user task to a person (claim), last-writer-wins
// (ADR-0042). Once the server has identity (ADR-0044/0045) claim is authoritative:
//
//   - With auth enabled, an empty/omitted body claims the task for the signed-in
//     user, and a named {"assignee": "..."} must be a real, enabled account
//     (400 otherwise) — you cannot assign work to a username that doesn't exist.
//   - With auth disabled (open single-user mode) there is no session identity, so
//     the caller must still name the assignee, unvalidated, as before.
//
// 404 if the job doesn't exist or is already completed.
func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	// The body is optional now: an empty body (io.EOF) means "claim for me"
	// (auth on); a malformed non-empty body is still a 400.
	var body struct {
		Assignee string `json:"assignee"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	assignee := strings.TrimSpace(body.Assignee)

	if s.authEnabled {
		p := principalFrom(r.Context())
		if p == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if assignee == "" {
			assignee = p.Username
		} else {
			// A named assignee must resolve to a real, enabled user; normalize to
			// the account's stored username (canonical case).
			var (
				canonical string
				known     bool
				vErr      error
			)
			s.do(func() {
				u, ok, e := s.users.byUsername(assignee)
				vErr = e
				known = ok && !u.Disabled
				if ok {
					canonical = u.Username
				}
			})
			if vErr != nil {
				writeError(w, http.StatusInternalServerError, "claim: "+vErr.Error())
				return
			}
			if !known {
				writeError(w, http.StatusBadRequest, "unknown or disabled user")
				return
			}
			assignee = canonical
		}
	} else if assignee == "" {
		writeError(w, http.StatusBadRequest, "assignee is required")
		return
	}
	s.assignTask(w, r, assignee)
}

// handleUnclaimTask releases a user task (assignee cleared), making it available
// again. 404 if the job doesn't exist or is already completed (ADR-0042).
func (s *Server) handleUnclaimTask(w http.ResponseWriter, r *http.Request) {
	s.assignTask(w, r, "")
}

// assignTask drives an assignee change for a user task's job through the
// processor (the same run-loop path completion uses) and reports the outcome. An
// empty assignee unclaims. Shared by the claim and unclaim handlers.
func (s *Server) assignTask(w http.ResponseWriter, r *http.Request, assignee string) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task key")
		return
	}
	var (
		found  bool
		runErr error
	)
	s.do(func() {
		if _, ok, err := s.store.GetJob(key); err != nil || !ok {
			return
		}
		found = true
		s.proc.AssignJob(key, assignee)
		runErr = s.jobRunner.Drive()
	})
	switch {
	case runErr != nil:
		writeError(w, http.StatusInternalServerError, "assign task: "+runErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no open task with that key")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"taskKey": key, "assignee": assignee})
	}
}

// handleStats returns the live instance counts.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	var (
		stats statsResp
		err   error
	)
	s.do(func() { stats, err = s.readStats() })
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read stats: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

type draftResp struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	SavedAt   int64  `json:"savedAt"`
}

// handleSaveDraft persists a diagram as a draft: the raw BPMN XML is stored as-is,
// keyed by its process id, WITHOUT compiling it — so an incomplete or not-yet
// executable model can still be saved and reopened. Re-saving the same process id
// overwrites the previous draft rather than creating a version. An optional
// ?projectId= query files the draft into that project (ADR-0034); it must name an
// existing project, else the save is rejected.
func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	pid, name := processIdentity(body)
	if pid == "" {
		writeError(w, http.StatusBadRequest, "cannot save draft: no <process id> in the diagram")
		return
	}
	projectID := r.URL.Query().Get("projectId")
	hasProjectParam := r.URL.Query().Has("projectId")
	rec := draft{ProcessID: pid, Name: name, ProjectID: projectID, SavedAt: time.Now().Unix(), XML: string(body)}
	var (
		saveErr, projErr error
		unknownProject   bool
	)
	s.do(func() {
		if !hasProjectParam {
			existing, ok, e := s.drafts.get(pid)
			if e == nil && ok {
				rec.ProjectID = existing.ProjectID
			}
		} else if projectID != "" {
			_, ok, e := s.projects.get(projectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
		}
		saveErr = s.drafts.save(rec)
	})
	switch {
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, draftResp{ProcessID: pid, Name: name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt})
	}
}

// handleListDrafts lists saved drafts, most recently saved first. An optional
// ?projectId= query narrows the list to one project's artifacts (ADR-0034).
func (s *Server) handleListDrafts(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("projectId")
	list := []draftResp{}
	var loadErr error
	s.do(func() {
		var recs []draft
		recs, loadErr = s.drafts.loadAll()
		for _, d := range recs {
			if filter != "" && d.ProjectID != filter {
				continue
			}
			list = append(list, draftResp{ProcessID: d.ProcessID, Name: d.Name, ProjectID: d.ProjectID, SavedAt: d.SavedAt})
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list drafts: "+loadErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMoveDraft reassigns a draft to a different project (or to Ungrouped when
// projectId is empty), without touching its XML. Body: {"projectId": "..."}. A
// non-empty projectId must name an existing project (ADR-0034).
func (s *Server) handleMoveDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		found, unknownProject    bool
		getErr, projErr, saveErr error
		view                     draftResp
	)
	s.do(func() {
		rec, ok, e := s.drafts.get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.ProjectID != "" {
			_, pok, pe := s.projects.get(payload.ProjectID)
			if pe != nil {
				projErr = pe
				return
			}
			if !pok {
				unknownProject = true
				return
			}
		}
		rec.ProjectID = payload.ProjectID
		if saveErr = s.drafts.save(rec); saveErr != nil {
			return
		}
		view = draftResp{ProcessID: rec.ProcessID, Name: rec.Name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt}
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read draft: "+getErr.Error())
	case !found:
		writeError(w, http.StatusNotFound, "no draft with that process id")
	case projErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		writeError(w, http.StatusBadRequest, "unknown project id")
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "move draft: "+saveErr.Error())
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

// handleDraftXML returns a draft's raw BPMN XML so the editor can reopen it.
func (s *Server) handleDraftXML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec     draft
		ok      bool
		readErr error
	)
	s.do(func() { rec, ok, readErr = s.drafts.get(id) })
	switch {
	case readErr != nil:
		writeError(w, http.StatusInternalServerError, "read draft: "+readErr.Error())
	case !ok:
		writeError(w, http.StatusNotFound, "no draft with that process id")
	default:
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(rec.XML))
	}
}

// handleDeleteDraft removes a saved draft. Deleting an absent draft succeeds, so
// the operation is idempotent.
func (s *Server) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.drafts.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete draft: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
