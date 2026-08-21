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

	"github.com/pblumer/atlas/api/layout"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"

	"github.com/pblumer/atlas/api/httpapi"
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
	// Warnings are things that deployed fine but will not run as written — today, a
	// connector reference naming something that is not configured, or is configured
	// as another kind, or cannot be built. The deploy succeeds anyway (a model is
	// routinely deployed before its connectors exist), but the author is told now
	// rather than by the first token to park (ADR-0158).
	Warnings []string `json:"warnings,omitempty"`
}

type processResp struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	DeployedAt int64  `json:"deployedAt"`
	// ProjectID names the project this definition's draft belonged to at deploy time,
	// so the Modeler home groups deployed definitions into the same project folders as
	// design-time artifacts (ADR-0034). Empty for a definition deployed outside a
	// project, which the UI shows under "Ungrouped".
	ProjectID string `json:"projectId,omitempty"`
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
	// Active reports whether the definition may auto-start new instances from its
	// timer/message/signal start events. A deactivated definition (false) stays
	// deployed and keeps its running instances, but does not self-start (ADR-0119).
	// Always emitted so the UI can render the active/inactive control unambiguously.
	Active bool `json:"active"`
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
	Tokens    int    `json:"tokens"`    // tokens sitting here now (live — drawn green)
	Visits    int    `json:"visits"`    // tokens that have ever passed through (history — drawn gray)
	Incidents int    `json:"incidents"` // of those live tokens, how many are parked behind an incident (drawn red)
}

// runtimeIncident is one unresolved incident on the overlay: which element instance is
// parked, why, and what to resolve. It carries the *BPMN* element id (the diagram
// speaks that, while model.IncidentValue holds the compiled index) so the browser can
// mark the shape without a second lookup.
type runtimeIncident struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	JobKey             uint64 `json:"jobKey"`
	ElementID          string `json:"elementId"`
	RaisedAt           int64  `json:"raisedAt"`
	Message            string `json:"message"`
	// Connector names the server-registered connector the stuck task refers to, and
	// ConnectorKind the kind it needs; ConnectorID is the configured record's id when
	// one exists under that name and kind. A connector task's incident is usually a
	// connector problem, and the fix is a field on the connector — so the operator gets
	// there from the incident instead of reading the name out of a message (ADR-0160).
	// All three are empty for a task that names no connector.
	Connector     string `json:"connector,omitempty"`
	ConnectorKind string `json:"connectorKind,omitempty"`
	ConnectorID   string `json:"connectorId,omitempty"`
}

type runtimeResp struct {
	Instances int              `json:"instances"`
	Tokens    int              `json:"tokens"`
	Elements  []runtimeElement `json:"elements"`
	// Incidents is why a token is not moving (ADR-0061). Without it a parked token
	// renders exactly like one that is legitimately waiting, which is the reading an
	// operator then gives it — "the task is still open" — while the engine has in fact
	// been holding a failure for hours (ADR-0150).
	Incidents []runtimeIncident `json:"incidents"`
	// IncidentsTruncated marks a capped page: more elements are parked than the
	// overlay lists. The per-element counts are then a floor, not a total.
	IncidentsTruncated bool `json:"incidentsTruncated"`
}

// Bounds on the incident overlay. maxRuntimeIncidents caps what one response carries;
// maxRuntimeIncidentScan caps how far the aggregate view reads to find them, so a
// store full of *other* definitions' incidents cannot turn a 1.5-second poll into an
// unbounded scan on the run loop (the O(elements) discipline of ADR-0080). The
// single-instance view needs neither: it point-looks-up the incident of each token it
// is already walking.
const (
	maxRuntimeIncidents    = 100
	maxRuntimeIncidentScan = 2000
)

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
	At        int64          `json:"at"`              // unix nanoseconds (element activated)
	EndAt     int64          `json:"endAt,omitempty"` // unix nanoseconds (element completed), 0 if still active
	ElementID string         `json:"elementId"`
	Type      string         `json:"type"`
	Variables []variableView `json:"variables"`
	// VariablesAfter is the variable set as it stood once this element finished, where
	// Variables is the set it saw on entry (ADR-0159). A task that writes its result on
	// completion — a job's outputs, an output mapping — shows nothing in Variables, so
	// the replay would report "no variables" for an element that in fact produced some.
	// Absent while the element is still active or parked, since it has not finished.
	VariablesAfter []variableView `json:"variablesAfter,omitempty"`
	// Inputs is what this element itself was handed: the values its zeebe:ioMapping
	// inputs were evaluated to, in its own activity-local scope (ADR-0068). Variables
	// and VariablesAfter carry the process (and subprocess) scopes, so these locals are
	// on the log but in neither. Absent for an element that declares no input mapping.
	Inputs             []variableView `json:"inputs,omitempty"`
	Position           uint64         `json:"position"`
	TokenID            uint64         `json:"tokenId,omitempty"`
	ElementInstanceKey uint64         `json:"elementInstanceKey,omitempty"`
	SourceElementID    string         `json:"sourceElementId,omitempty"`
	Action             string         `json:"action,omitempty"`
	Relation           string         `json:"relation,omitempty"`
	// ChildInstanceKey is the process instance a call activity started as its child
	// (ADR-0076), so the replay view can drill from the caller's call activity into
	// the child's own replay. Zero for a non-call-activity step, or a call activity
	// whose child was never created (it parked). Resolved by the reverse
	// ParentElementInstanceKey link, covering active and completed children.
	ChildInstanceKey uint64 `json:"childInstanceKey,omitempty"`
	// Manual attributes a step an operator forced rather than one the engine drove on
	// its own (ADR-0159) — who completed the parked job by hand, and why. Absent for
	// every ordinary step, so its presence alone marks the intervention.
	Manual *manualActionView `json:"manual,omitempty"`
}

// manualActionView renders one operator intervention on a step for the operator UI
// (ADR-0159). Reason is always present — the routes that mint these records require it
// — while Actor is empty when auth is off or the caller was unidentified, exactly like
// the variable-override attribution it sits beside.
type manualActionView struct {
	Action string `json:"action"`
	Actor  string `json:"actor,omitempty"`
	Reason string `json:"reason"`
	At     int64  `json:"at"`
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
	// UnresolvedIncidents is how many tokens are parked behind an incident right now
	// (ADR-0061). The shell polls it for the Operations nav badge, so "something is
	// stuck" reaches an operator who is not looking at a diagram (ADR-0151).
	UnresolvedIncidents int `json:"unresolvedIncidents"`
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
	// Worker names the worker reporting the failure. With LeaseToken it is the
	// protocol path (ADR-0007): the report is accepted only from the holder of the
	// job's current lease, so a stale worker cannot burn a retry — or raise an
	// incident — on work another worker is now doing. Omitting both leaves the
	// operator path, which is unfenced by design.
	Worker     string `json:"worker"`
	LeaseToken uint64 `json:"leaseToken"`
	// RetryBackoff is the delay in milliseconds a worker asks to wait before its failed job
	// may be retried (ADR-0111). 0 (or absent) retries immediately, the pre-0111 behavior.
	RetryBackoff int64 `json:"retryBackoff"`
}

type resolveIncidentReq struct {
	Retries int32 `json:"retries"`
}

// incidentView is one unresolved incident as the operator surfaces read it. Besides
// the resolve key (ElementInstanceKey) it carries the diagram context the Operations
// live view and the single-instance replay need to draw the incident *on the element
// it stalls* and link it back: the definition it belongs to and the BPMN id of the
// stuck element. Both are resolved on read from the instance and its compiled
// process — the durable IncidentValue keeps holding only the compiled element index
// (I6: nothing here is written back into an event) — ADR-0151.
type incidentView struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	ProcessDefKey      uint64 `json:"processDefKey,omitempty"`
	ProcessID          string `json:"processId,omitempty"`
	JobKey             uint64 `json:"jobKey"`
	// ElementID is the BPMN diagram id of the stuck element (the id every other view
	// calls `elementId`), empty when the instance outlived its deployment.
	// ElementIndex is the compiled-graph index the incident actually stores.
	ElementID    string `json:"elementId,omitempty"`
	ElementIndex int32  `json:"elementIndex"`
	// Type is what parked: "job" (a service-task job whose retries ran out) or
	// "timer" (a job-less timer whose FEEL schedule stopped resolving, ADR-0064/0111).
	Type     string `json:"type"`
	RaisedAt int64  `json:"raisedAt"`
	Message  string `json:"message"`
	// Connector / ConnectorKind / ConnectorID are the server-registered connector the
	// stuck task refers to, so the fix is one click from the incident rather than a
	// name read out of a message (ADR-0160). Empty when the task names no connector,
	// and ConnectorID alone is empty when nothing is configured under that name.
	Connector     string `json:"connector,omitempty"`
	ConnectorKind string `json:"connectorKind,omitempty"`
	ConnectorID   string `json:"connectorId,omitempty"`
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
	httpapi.JSON(w, http.StatusOK, logsResp{Lines: lines})
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	b := buildInfo()
	httpapi.JSON(w, http.StatusOK, infoResp{
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
		httpapi.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// A blank expression is a no-op success: an empty field simply carries no
	// condition/script, which the editor treats as unset rather than an error.
	if strings.TrimSpace(req.Expression) == "" {
		httpapi.JSON(w, http.StatusOK, validateFeelResp{OK: true})
		return
	}
	if _, err := expr.CompileAuto(req.Expression); err != nil {
		httpapi.JSON(w, http.StatusOK, validateFeelResp{OK: false, Error: err.Error()})
		return
	}
	httpapi.JSON(w, http.StatusOK, validateFeelResp{OK: true})
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
		httpapi.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		httpapi.JSON(w, http.StatusOK, evalFeelResp{OK: false, Error: "empty expression"})
		return
	}
	compiled, err := expr.CompileAuto(req.Expression)
	if err != nil {
		httpapi.JSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	bindings, err := feelBindings(req.Variables)
	if err != nil {
		httpapi.JSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
		return
	}
	v, err := compiled.Eval(bindings)
	if err != nil {
		httpapi.JSON(w, http.StatusOK, evalFeelResp{OK: false, Error: err.Error()})
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
	httpapi.JSON(w, http.StatusOK, evalFeelResp{OK: true, Result: result, Kind: feelKindName(kind)})
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
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
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
	s.do(func() { refs, loadErr = s.dmnrefs.LoadAll() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	dmnXMLs, refuse, dmnErr := s.dmnForDeployBody(r.Context(), body, refs)
	if dmnErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+dmnErr.Error())
		return
	}
	if refuse != "" {
		httpapi.Error(w, http.StatusConflict, refuse)
		return
	}

	// Resolve the project this deploy files under, so the Modeler home groups the
	// deployed definition into the same folder as its draft (ADR-0034). An explicit
	// ?projectId= (the editor sends its current project) wins and, when non-empty,
	// must name an existing project; without the param we inherit the matching draft's
	// project so an API/MCP deploy of a project draft still lands in its folder. A
	// deploy with neither is Ungrouped.
	projectID := r.URL.Query().Get("projectId")
	hasProjectParam := r.URL.Query().Has("projectId")
	pid, _ := processIdentity(body)
	var (
		resp           deployResp
		compErr        error
		persistErr     error
		projErr        error
		unknownProject bool
	)
	s.do(func() {
		if !hasProjectParam {
			if d, ok, e := s.drafts.Get(pid); e == nil && ok {
				projectID = d.ProjectID
			}
		} else if projectID != "" {
			_, ok, e := s.projects.Get(projectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
		}
		var deployed []deployedProcess
		deployed, compErr, persistErr = s.deployModel(body, dmnXMLs, time.Now().Unix(), projectID)
		if compErr != nil || persistErr != nil {
			return
		}
		resp = deployResp{
			Key:         deployed[0].Key,
			ProcessID:   deployed[0].ProcessID,
			Version:     deployed[0].Version,
			Deployments: deployed,
		}
		// Still on the run loop, where the connector store and the registries may be
		// read (I3), and with every pool of a collaboration already registered.
		for _, d := range deployed {
			if dep, ok := s.deployments[d.Key]; ok && dep.cp != nil {
				resp.Warnings = append(resp.Warnings, s.connectorWarnings(dep.cp)...)
			}
		}
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case compErr != nil:
		// A compile failure is a client error: the submitted model is invalid.
		httpapi.Error(w, http.StatusBadRequest, compErr.Error())
	case persistErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, resp)
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
//
// projectID files every process the model deploys under a project, so the Modeler
// home can group deployed definitions the way it groups design-time artifacts
// (ADR-0034). Empty for a deploy made outside a project.
func (s *Server) deployModel(body []byte, dmnXMLs [][]byte, deployedAt int64, projectID string) (deployed []deployedProcess, compErr, persistErr error) {
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

		if err := s.deploys.Save(persistedDeployment{
			Key:        key,
			ProcessID:  pid,
			Name:       name,
			Version:    version,
			DeployedAt: deployedAt,
			ProjectID:  projectID,
			XML:        string(body),
			DMNXMLs:    dmnStrings,
		}); err != nil {
			return deployed, nil, err
		}

		s.versions[pid] = version
		// Translate the process's model-authored job types into the engine-wide index
		// space before it can create any job, so the type a job carries means the same
		// thing in every definition (ADR-0007/0157). Resolution is idempotent, so a
		// redeploy of the same model lands on the same indices.
		if err := cp.ResolveJobTypes(s.jobTypes.Intern); err != nil {
			return deployed, nil, err
		}
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
			ProjectID:  projectID,
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
	// A no-op for a model with no timer start events. Applying commands is all this
	// needs — a deploy starts no instance, so it unblocks no in-process handler and
	// nothing has to leave the loop.
	if err := s.proc.RunUntilIdle(); err != nil {
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
				ProjectID:        d.ProjectID,
				CollaborationKey: s.collaborationKeyOf(d),
				StartFormID:      d.cp.StartFormId(),
				Executable:       d.cp.IsExecutable(),
				VersionTag:       d.cp.VersionTag(),
				Active:           !d.inactive,
			})
		}
	})
	httpapi.JSON(w, http.StatusOK, list)
}

// handleProcessXML returns a deployed definition's BPMN XML for the browser to
// render. If the model carries no diagram layout, a generated left-to-right
// layout is injected so it still renders (ensureDiagramLayout).
func (s *Server) handleProcessXML(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var raw []byte
	s.do(func() {
		if d, ok := s.deployments[key]; ok {
			raw = d.xml
		}
	})
	if raw == nil {
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(layout.Ensure(raw))
}

// handleLayout regenerates a posted model's diagram layout: it discards whatever
// diagram interchange the BPMN carries and returns the model with a freshly
// generated left-to-right layout, backing the Modeler's "Auto-layout" button. Like
// validate it is a pure transform — no key minted, no definition registered, no
// state touched — so it runs off the run-loop goroutine. A model whose layout can't
// be regenerated (unparseable, or nodeless) comes back unchanged; only a missing or
// unreadable body is a 4xx, matching the deploy and validate endpoints.
func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(layout.Regenerate(body))
}

// handleDeleteProcess removes a deployed definition (one version). It refuses if
// the definition still has running instances, since a live instance resolves its
// definition by key on every batch.
func (s *Server) handleDeleteProcess(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var (
		found      bool
		protected  bool
		running    int
		scanErr    error
		persistErr error
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		// A bootstrap-deployed platform process is platform-managed (ADR-0122):
		// refuse deletion for every caller.
		if s.systemPIDs[d.ProcessID] {
			protected = true
			return
		}
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
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case protected:
		httpapi.Error(w, http.StatusForbidden, "protected system process cannot be deleted")
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "check instances: "+scanErr.Error())
	case running > 0:
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf("cannot delete: %d running instance(s); cancel them first", running))
	case persistErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "remove deployment: "+persistErr.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSetProcessActive activates or deactivates a deployed definition (ADR-0119).
// A deactivated definition stays deployed and keeps its running instances, but no
// longer auto-starts new ones when its timer, message, or signal start events fire —
// the operator has paused it (e.g. a timer-driven process). Reactivating restores
// automatic starts. The flag persists in the deployment sidecar and is re-applied on
// restart. Body: {"active": bool}.
func (s *Server) handleSetProcessActive(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid body: expected {\"active\": bool}")
		return
	}
	var (
		found      bool
		loadErr    error
		persistErr error
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok {
			return
		}
		found = true
		// No-op fast path: an unchanged flag rewrites nothing (idempotent PUT).
		if d.inactive == !body.Active {
			return
		}
		// Read the full record so the rewrite preserves the XML and DMN models the
		// in-memory deployment does not hold, then flip the flag. Durable before visible
		// (I2, ADR-0019): persist first, then apply to the engine and the display copy.
		rec, ok, err := s.deploys.load(key)
		if err != nil {
			loadErr = err
			return
		}
		if !ok {
			// The registry and the sidecar have diverged (should not happen); treat it as
			// not found rather than silently applying an unpersisted change.
			found = false
			return
		}
		rec.Inactive = !body.Active
		if err := s.deploys.Save(rec); err != nil {
			persistErr = err
			return
		}
		s.proc.SetProcessActive(key, body.Active)
		d.inactive = !body.Active
	})
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read deployment: "+loadErr.Error())
	case persistErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "persist deployment: "+persistErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"key": key, "active": body.Active})
	}
}

// collectDefIncidents adds the unresolved incidents of one definition to the overlay.
// An incident carries its process instance, not its definition (model.IncidentValue),
// so each candidate costs one point lookup to attribute — and attribute it must: a
// compiled element index is only meaningful within its own definition, so an incident
// let through from another one would mark a completely unrelated shape.
//
// The work is bounded twice, by how far it reads and by how much it returns, because
// this runs on the run loop under a 1.5-second poll: a definition whose own tokens are
// healthy must not pay an unbounded scan because some other definition has thousands
// parked behind a broken connector. A bound that bites marks the page truncated, which
// is what tells the browser its per-element counts are a floor rather than a total.
func (s *Server) collectDefIncidents(defKey uint64, add func(uint64, *model.IncidentValue) bool, resp *runtimeResp) error {
	scanned := 0
	return unlessTruncated(s.store.Incidents(func(elKey uint64, v *model.IncidentValue) error {
		if scanned++; scanned > maxRuntimeIncidentScan {
			resp.IncidentsTruncated = true
			return errListTruncated
		}
		pi, ok, err := s.store.ProcessInstance(v.ProcessInstanceKey)
		if err != nil {
			return err
		}
		if !ok || pi.ProcessDefKey != defKey {
			return nil
		}
		if add(elKey, v) {
			resp.IncidentsTruncated = true
			return errListTruncated
		}
		return nil
	}))
}

// handleProcessRuntime returns, for one definition, how many instances are live
// and how many tokens (element instances) currently sit on each BPMN element —
// the data the browser overlays onto the diagram. An optional ?instance=<key>
// filter narrows the result to a single process instance, so the live view can
// isolate one instance on the diagram instead of aggregating all of them.
func (s *Server) handleProcessRuntime(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	// instanceFilter == 0 means "all instances"; instance keys are never 0.
	var instanceFilter uint64
	if q := r.URL.Query().Get("instance"); q != "" {
		instanceFilter, err = strconv.ParseUint(q, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
			return
		}
	}
	var (
		found   bool
		scanErr error
		resp    = runtimeResp{Elements: []runtimeElement{}, Incidents: []runtimeIncident{}}
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

		// One resolver for the whole overlay, so the connector store is read once for
		// the page rather than once per parked token (ADR-0160).
		connectorFor := s.incidentConnectorLookup()
		// addIncident records one parked element instance on the overlay: a count on
		// the element (so the diagram can mark the shape) and the detail behind it (so
		// the panel can say why and offer the resolve). Full is the response cap.
		addIncident := func(elKey uint64, v *model.IncidentValue) (full bool) {
			e := get(v.ElementId)
			if e == nil {
				return false
			}
			e.Incidents++
			inc := runtimeIncident{
				ElementInstanceKey: elKey,
				ProcessInstanceKey: v.ProcessInstanceKey,
				JobKey:             v.JobKey,
				ElementID:          e.ElementID,
				RaisedAt:           v.RaisedAt,
				Message:            v.Message,
			}
			inc.Connector, inc.ConnectorKind, inc.ConnectorID = connectorFor(d.cp, v.ElementId)
			resp.Incidents = append(resp.Incidents, inc)
			return len(resp.Incidents) >= maxRuntimeIncidents
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
					if resp.Instances, scanErr = s.store.DefInstanceCount(key); scanErr == nil {
						scanErr = s.collectDefIncidents(key, addIncident, &resp)
					}
				}
			}
		} else {
			// Isolating one instance on the diagram (a deliberate single-instance
			// action, not the default view): the overlay walks instances filtered to
			// this one. Making this path sublinear too is a follow-up to ADR-0080 (the
			// aggregate default view above is what mattered for scale).
			if scanErr = s.store.ActiveElementInstances(func(elKey uint64, v *model.ElementInstanceValue) error {
				if v.ProcessDefKey != key || v.ProcessInstanceKey != instanceFilter {
					return nil
				}
				if e := get(v.ElementId); e != nil {
					e.Tokens++
					resp.Tokens++
				}
				// One point lookup per token this instance holds: an element instance is
				// where an incident hangs (ADR-0061), so the walk that draws the tokens
				// also finds every reason one of them is not moving.
				if len(resp.Incidents) < maxRuntimeIncidents {
					inc, err := s.store.GetIncident(elKey)
					if err != nil {
						return err
					}
					if inc != nil && addIncident(elKey, inc) {
						resp.IncidentsTruncated = true
					}
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
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read runtime: "+scanErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, resp)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
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
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read collaboration runtime: "+scanErr.Error())
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
		httpapi.JSON(w, http.StatusOK, resp)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
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
		// Reverse call-activity link: a child instance records the call-activity element
		// instance that started it (ParentElementInstanceKey, ADR-0076). Build the map
		// once — only if this definition actually has call activities — so a call
		// activity's step can carry its childInstanceKey and the replay can drill in.
		// Covers active and completed children (the child usually outlives the caller's
		// step but may already be done).
		var childByParent map[uint64]uint64
		if scanErr == nil && len(d.cp.CallActivities()) > 0 {
			childByParent = map[uint64]uint64{}
			link := func(childKey uint64, v *model.ProcessInstanceValue) error {
				if v.ParentElementInstanceKey != 0 {
					childByParent[v.ParentElementInstanceKey] = childKey
				}
				return nil
			}
			if scanErr = s.store.ActiveProcessInstances(link); scanErr == nil {
				scanErr = s.store.CompletedProcessInstances(link)
			}
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
		// External-override attribution (ADR-0098): an operator override emits a
		// variable event immediately followed by its audit event, so the audit's log
		// position is the variable change's position plus one. Map each override's actor
		// back onto the variable change's position, keyed by the global log position so
		// it matches whichever scope's snapshot fold carries that change. Overrides
		// keyed by the process instance cover every scope. An audit with no identity
		// (auth off) is skipped — there is no actor to surface.
		actorByPos := map[uint64]string{}
		manualByElement := map[uint64]manualActionView{}
		if scanErr == nil {
			scanErr = s.store.VariableAuditHistory(key, func(_ int64, pos uint64, v *model.VariableAuditValue) error {
				if v.Actor != "" && pos > 0 {
					actorByPos[pos-1] = v.Actor
				}
				return nil
			})
		}
		// Operator interventions (ADR-0159): who forced a step by hand, and why. Keyed by
		// the element instance the action was applied to, so it attaches to that element's
		// step below — the element id is resolved from the step, never read off the log
		// (invariant I5 keeps ids interned, so the record carries only keys).
		if scanErr == nil {
			scanErr = s.store.OperatorActionHistory(key, func(ts int64, _ uint64, v *model.OperatorActionValue) error {
				if v.ElementInstanceKey != 0 {
					manualByElement[v.ElementInstanceKey] = manualActionView{
						Action: v.Kind.String(), Actor: v.Actor, Reason: v.Reason, At: ts,
					}
				}
				return nil
			})
		}
		if scanErr == nil {
			scanErr = s.store.VariableSnapshotHistory(key, func(_ int64, pos uint64, v *model.VariableValue) error {
				view := toVariableView(v)
				view.Actor = actorByPos[pos]
				changes = append(changes, varChange{pos: pos, endPos: noEnd, view: view})
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
				view.Actor = actorByPos[pos]
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
		//
		// A *termination* is never deferred. An element torn down by an interrupting
		// boundary event, a cancelled instance, or a terminate end event hands its
		// token to no one — its outgoing flow is never taken — so waiting for a
		// successor would leave a ghost token parked on it forever, outliving the
		// instance itself (ADR-0136).
		active := map[uint64]timelineToken{}
		pending := map[uint64]state.ElementReplayValue{} // completions awaiting their successor
		activations := map[uint64]state.ElementReplayValue{}
		endAt := map[uint64]int64{}   // element instance key → completion timestamp (Action==2)
		endPos := map[uint64]uint64{} // element instance key → completion log position (ADR-0159)
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
			switch {
			case v.Action == state.ReplayActivated:
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
			case v.Action == state.ReplayTerminated, d.cp.Node(v.ElementID).OutgoingCount == 0:
				// Nothing will activate from here — a termination hands its token on to
				// no one, and a leaf has no successor to move into — so remove it at once.
				endAt[v.ElementInstanceKey] = rr.at
				endPos[v.ElementInstanceKey] = rr.pos
				delete(pending, v.ElementInstanceKey)
				delete(active, v.ElementInstanceKey)
				emitFrame(rr.pos, rr.at)
			default:
				// Defer: keep the token visible until its successor activates.
				endAt[v.ElementInstanceKey] = rr.at
				endPos[v.ElementInstanceKey] = rr.pos
				pending[v.ElementInstanceKey] = v
			}
		}
		// A finished instance holds no element instance, so it can hold no token: drop
		// anything still deferred and show the empty terminal frame. Live folds resolve
		// every deferral above; this catches history written before terminations were
		// recorded distinctly (ADR-0136), which would otherwise strand a ghost token on
		// the last frame of an already-completed instance.
		if len(active) > 0 && pi.State != model.PIActive && len(replayRows) > 0 {
			last := replayRows[len(replayRows)-1]
			active = map[uint64]timelineToken{}
			emitFrame(last.pos, last.at)
		}

		// What each element was actually handed: an activity's zeebe:ioMapping inputs are
		// evaluated into its *activity-local* scope, keyed by the element instance
		// (ADR-0068). The folds above read the process and subprocess scopes only, so
		// those values sit on the log but appear nowhere in the timeline — the one thing
		// an operator asking "what went into this task" most wants. Kept off Variables /
		// VariablesAfter deliberately: a local belongs to one element instance, and folding
		// it into the shared running set would leak it onto every concurrent step.
		//
		// Only elements that declare input mappings are scanned, so an instance with none
		// pays nothing, and only the declared targets are kept — an activity's local scope
		// also carries values its own behavior parked there (a script result awaiting its
		// output mapping, a multi-instance loop counter), which are not inputs.
		inputsByEIK := map[uint64][]variableView{}
		for _, v := range activations {
			ins := d.cp.IOInputs(v.ElementID)
			if len(ins) == 0 || v.ElementInstanceKey == 0 {
				continue
			}
			targets := make(map[string]struct{}, len(ins))
			for _, m := range ins {
				targets[d.cp.Intern(m.Target)] = struct{}{}
			}
			byName := map[string]variableView{}
			if scanErr = s.store.VariableSnapshotHistory(v.ElementInstanceKey, func(_ int64, pos uint64, vv *model.VariableValue) error {
				if _, ok := targets[vv.Name]; !ok {
					return nil
				}
				view := toVariableView(vv)
				view.Actor = actorByPos[pos] // an operator may have corrected a local too (ADR-0098)
				byName[vv.Name] = view       // later write wins, as in the folds above
				return nil
			}); scanErr != nil {
				return
			}
			list := make([]variableView, 0, len(byName))
			for _, view := range byName {
				list = append(list, view)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			inputsByEIK[v.ElementInstanceKey] = list
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
				step.Inputs = inputsByEIK[rv.ElementInstanceKey]
				// A step a person forced carries its attribution (ADR-0159).
				if m, ok := manualByElement[rv.ElementInstanceKey]; ok {
					mv := m
					step.Manual = &mv
				}
				// A call activity carries the child instance it started, so the replay
				// can drill from the caller into the child (ADR-0076).
				if childByParent != nil && d.cp.Node(sr.elementID).Type == compiler.TypeCallActivity {
					step.ChildInstanceKey = childByParent[rv.ElementInstanceKey]
				}
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

		// A second sweep for the variables as they stood once each element *finished*
		// (ADR-0159). The pass above answers "what did this element see on entry"; a task
		// that writes its result on completion has nothing to show there, which reads as
		// "the element has no variables" even though it produced some. Folding the same
		// ordered change list up to the element's completion position answers "what did it
		// leave behind". Only a finished element has one, so an element still parked keeps
		// it absent. The refs are walked in completion order so the sweep stays linear over
		// the change list rather than re-folding per step.
		type afterRef struct {
			idx int
			pos uint64
		}
		refs := make([]afterRef, 0, len(resp.Steps))
		for i := range resp.Steps {
			if k := resp.Steps[i].ElementInstanceKey; k != 0 {
				if p, ok := endPos[k]; ok {
					refs = append(refs, afterRef{idx: i, pos: p})
				}
			}
		}
		sort.Slice(refs, func(a, b int) bool { return refs[a].pos < refs[b].pos })
		after := map[varKey]varEntry{}
		ai := 0
		for _, r := range refs {
			for ai < len(changes) && changes[ai].pos <= r.pos {
				c := changes[ai]
				after[varKey{c.view.Scope, c.view.Name}] = varEntry{view: c.view, endPos: c.endPos}
				ai++
			}
			vars := make([]variableView, 0, len(after))
			for _, e := range after {
				if r.pos <= e.endPos {
					vars = append(vars, e.view)
				}
			}
			sort.Slice(vars, func(i, j int) bool {
				if vars[i].Scope != vars[j].Scope {
					return vars[i].Scope < vars[j].Scope
				}
				return vars[i].Name < vars[j].Name
			})
			resp.Steps[r.idx].VariablesAfter = vars
		}
	})
	switch {
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read timeline: "+scanErr.Error())
	case !foundInstance:
		httpapi.Error(w, http.StatusNotFound, "no instance with that key")
	case !foundDef:
		httpapi.Error(w, http.StatusNotFound, "instance's definition is no longer deployed")
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}

// handleCreateInstance starts one instance of a deployed definition, optionally
// seeded with variables from the request body ({"variables": {"amount": 100}}),
// runs the processor until idle, and returns the resulting live counts.
func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	startVars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		found   bool
		notExec bool
		runErr  error
		statErr error
		stats   statsResp
	)
	var driveNeeded bool
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
	// read-back that follows it are two separate visits to the loop.
	if driveNeeded {
		if runErr = s.drive(); runErr == nil {
			s.do(func() { stats, statErr = s.readStats() })
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
	// Actor names the operator who last set this value via an external override
	// (ADR-0098), so the timeline attributes a corrected value inline; empty when the
	// value was written by the model itself or the override carried no identity (auth
	// off). It is populated only by the timeline fold, which has the audit trail.
	Actor string `json:"actor,omitempty"`
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

// handleInstanceVariables returns the variables visible at an instance scope as a
// typed JSON object ({"Name": "Patrick", ...}) — the shape the Tasks app feeds a
// bound form so a field whose key matches a variable is prefilled (ADR-0028). The
// key may be a process-instance root or a task's element-instance scope; the read
// resolves up the enclosing-scope chain (ADR-0068), so a user task without its own
// input-mapped locals still prefills from the process variables it inherits — the
// same set the token itself sees. An instance with no variables (or an unknown
// key) yields an empty object, not a 404: the endpoint is a convenience read, not
// an existence check.
func (s *Server) handleInstanceVariables(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := map[string]any{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.VisibleVariablesOfScope(key, func(v *model.VariableValue) error {
			out[v.Name] = nativeVar(v)
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read variables: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleSetInstanceVariables sets or overwrites variables on a running instance
// from outside the model (ADR-0095): an operator correction to a stuck or
// misconfigured instance, the manual counterpart to the writes the model makes for
// itself. Body: {"variables": {…}, "scopeKey": <optional>}. Variables land in the
// instance root scope by default; a scopeKey (a live element instance key belonging
// to the instance) targets a subprocess/multi-instance-body local scope instead.
//
// It is admin-gated when auth is on — a deliberately narrower right than starting an
// instance, because it edits live process state — and open in single-user mode like
// the rest of the runtime surface. 404 if the instance is not running (a finished
// instance's state is history, not something to correct); 400 if the target scope
// does not belong to the instance. The change is recorded in the instance's variable
// timeline as the audit trail, and it does NOT re-evaluate any gateway a token has
// already passed — only the stored values change.
func (s *Server) handleSetInstanceVariables(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// Decode variables and the optional scopeKey in one pass (UseNumber so a
	// number's exact textual form survives for FEEL's decimal semantics), then reuse
	// the shared start-variable conversion.
	var payload struct {
		Variables map[string]any `json:"variables"`
		ScopeKey  uint64         `json:"scopeKey"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	vars, err := startVarsFromMap(payload.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(vars) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "no variables to set (body must carry a non-empty \"variables\" object)")
		return
	}
	scopeKey := payload.ScopeKey
	// Who is making the change, for the audit trail (ADR-0098): the authenticated
	// principal's username, or "" when auth is off (single-user) or the caller is
	// unidentified. Read off the request before the run loop, like every other
	// principal read.
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	var (
		found    bool
		scopeBad bool
		scanErr  error
		runErr   error
	)
	s.do(func() {
		pi, ok, err := s.store.ProcessInstance(key)
		if err != nil {
			scanErr = err
			return
		}
		if !ok || pi.State != model.PIActive {
			return // not found stays 404: only a running instance can be corrected
		}
		found = true
		if scopeKey != 0 && scopeKey != key {
			ei, ok, err := s.store.GetElementInstance(scopeKey)
			if err != nil {
				scanErr = err
				return
			}
			if !ok || ei.ProcessInstanceKey != key {
				scopeBad = true
				return
			}
		}
		s.proc.SetVariables(key, scopeKey, actor, vars...)
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read instance: "+scanErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no running instance with that key")
	case scopeBad:
		httpapi.Error(w, http.StatusBadRequest, "scopeKey is not an element instance of this process instance")
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "set variables: "+runErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"instanceKey": key, "variablesSet": len(vars)})
	}
}

// variableAuditView renders one external variable override for the audit view
// (ADR-0098): when it happened, who made it, on which scope, and the variable name
// and typed new value. It is the "who changed it" surface over the append-only audit
// history — the read side of POST /instances/{key}/variables.
type variableAuditView struct {
	At    int64  `json:"at"`
	Actor string `json:"actor"`
	Scope uint64 `json:"scope"`
	Name  string `json:"name"`
	Value any    `json:"value"`
	Kind  string `json:"kind"`
}

// varKindName maps a stored variable kind to the API's kind label, shared by the
// variable-audit view (and available to any other typed-variable view).
func varKindName(k model.VarKind) string {
	switch k {
	case model.VarBool:
		return "boolean"
	case model.VarNumber:
		return "number"
	case model.VarString:
		return "string"
	case model.VarJSON:
		return "json"
	default:
		return "null"
	}
}

// handleInstanceVariableAudit returns the external variable overrides a process
// instance received, in the order they happened, each with who made it, on which
// scope, and the variable name and typed new value (ADR-0098). It is the audit
// counterpart to the read-only variables view: because the records are append-only
// history, it works while the instance runs and after it has finished. An instance
// with no overrides (or an unknown key) yields an empty array, not a 404 — like the
// variables and decisions endpoints, it is a convenience read, not an existence check.
func (s *Server) handleInstanceVariableAudit(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := []variableAuditView{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.VariableAuditHistory(key, func(ts int64, _ uint64, v *model.VariableAuditValue) error {
			out = append(out, variableAuditView{
				At:    ts,
				Actor: v.Actor,
				Scope: v.ScopeKey,
				Name:  v.Name,
				Value: nativeVar(&model.VariableValue{Kind: v.Kind, Bool: v.Bool, Text: v.Text}),
				Kind:  varKindName(v.Kind),
			})
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read variable audit: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
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
		httpapi.Error(w, http.StatusInternalServerError, "read data objects: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
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
		httpapi.Error(w, http.StatusInternalServerError, "read decisions: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
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
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
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
			httpapi.Error(w, http.StatusBadRequest, "invalid process key")
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
		httpapi.Error(w, http.StatusInternalServerError, "list instances: "+scanErr.Error())
		return
	}
	// Finished instances: most recently completed first.
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	if truncated {
		// Signal that the page was capped so a client can page with ?process=/?limit=
		// rather than assume it received every instance.
		w.Header().Set("X-Instances-Truncated", "true")
	}
	httpapi.JSON(w, http.StatusOK, append(active, done...))
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
	httpapi.JSON(w, http.StatusOK, out)
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
		httpapi.JSON(w, http.StatusOK, []instanceResp{})
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
		httpapi.Error(w, http.StatusInternalServerError, "search instances: "+scanErr.Error())
		return
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	out := append(active, done...)
	if len(out) > maxInstanceSearchResults {
		out = out[:maxInstanceSearchResults]
	}
	httpapi.JSON(w, http.StatusOK, out)
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
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name           string `json:"name"`
		CorrelationKey string `json:"correlationKey"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if payload.Name == "" {
		httpapi.Error(w, http.StatusBadRequest, "message name is required")
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		runErr  error
		statErr error
		stats   statsResp
	)
	var driveNeeded bool
	s.do(func() {
		s.proc.PublishMessage(payload.Name, payload.CorrelationKey, vars...)
		driveNeeded = true
	})
	// The handlers run off the run loop (ADR-0157 step 6), so the drive and the
	// read-back that follows it are two separate visits to the loop.
	if driveNeeded {
		if runErr = s.drive(); runErr == nil {
			s.do(func() { stats, statErr = s.readStats() })
		}
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "publish message: "+runErr.Error())
	case statErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, publishMessageResp{Name: payload.Name, CorrelationKey: payload.CorrelationKey, Stats: stats})
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
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	var (
		found   bool
		scanErr error
		runErr  error
		statErr error
		stats   statsResp
	)
	var driveNeeded bool
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
		driveNeeded = true
	})
	// The handlers run off the run loop (ADR-0157 step 6), so the drive and the
	// read-back that follows it are two separate visits to the loop.
	if driveNeeded {
		if runErr = s.drive(); runErr == nil {
			s.do(func() { stats, statErr = s.readStats() })
		}
	}
	switch {
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "find instance: "+scanErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no active instance with that key")
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "cancel instance: "+runErr.Error())
	case statErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read stats: "+statErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, cancelInstanceResp{InstanceKey: key, State: "terminated", Stats: stats})
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
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	limit := bulkCancelBatchDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
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
	var driveNeeded bool
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
		driveNeeded = true
	})
	if driveNeeded {
		if opErr = s.drive(); opErr == nil {
			s.do(func() { stats, opErr = s.readStats() })
		}
	}
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "cancel instances: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, cancelInstancesResp{DefinitionKey: key, Canceled: len(keys), Remaining: remaining, Stats: stats})
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
		httpapi.Error(w, http.StatusBadRequest, "specify either keys or processDefKey, not both")
		return
	case len(req.Keys) > 0:
		s.terminateByKeys(w, req.Keys)
	case req.ProcessDefKey != 0:
		s.terminateByFilter(w, req)
	default:
		httpapi.Error(w, http.StatusBadRequest, "want keys or processDefKey")
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
	var driveNeeded bool
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
		driveNeeded = true
	})
	if driveNeeded {
		if opErr = s.drive(); opErr == nil {
			s.do(func() { stats, opErr = s.readStats() })
		}
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "terminate instances: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, terminateInstancesResp{
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
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
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
	var driveNeeded bool
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
		driveNeeded = true
	})
	if driveNeeded {
		if opErr = s.drive(); opErr == nil {
			s.do(func() { stats, opErr = s.readStats() })
		}
	}
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "terminate instances: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, terminateInstancesResp{
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
// jobTypeName turns the job-type index on a job record back into the name an
// operator authored. The engine-wide table is authoritative: since job types are
// resolved at deploy (ADR-0007/0157) every job created from then on carries an
// index that means the same thing across definitions.
//
// The fallback is for a job written *before* that table existed, whose index was
// interned per process and is only meaningful against its own string table. Such
// a job can still be read out under the wrong name if its local index happens to
// collide with a registered one — it is why the jobs already in state need a
// migration, which lands with the type-keyed pull that makes it observable.
func (s *Server) jobTypeName(cp *compiler.CompiledProcess, jobType int32) string {
	if name, ok := s.jobTypes.Name(jobType); ok {
		return name
	}
	return cp.Intern(jobType)
}

func (s *Server) handleListInstanceJobs(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
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
					jr.JobType = s.jobTypeName(cp, jv.JobType)
				}
			}
			jobs = append(jobs, jr)
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list jobs: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, jobs)
}

type taskResp struct {
	Key                uint64 `json:"key"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	// ElementInstanceKey scopes the task's own variables — its input-mapped fields
	// live here, so the Tasks app pre-fills a bound form by reading this scope's
	// variables rather than the process root (ADR-0084 Slice 4).
	ElementInstanceKey uint64 `json:"elementInstanceKey,omitempty"`
	ProcessDefKey      uint64 `json:"processDefKey,omitempty"`
	ProcessID          string `json:"processId,omitempty"`
	ElementID          string `json:"elementId,omitempty"`
	Name               string `json:"name,omitempty"`
	// Documentation is the element's <bpmn:documentation> — the work instruction the
	// modeler wrote for whoever picks the task up (ADR-0025). Design-time metadata: the
	// engine never reads it, the Tasks app shows it above the form.
	Documentation   string `json:"documentation,omitempty"`
	Assignee        string `json:"assignee,omitempty"`
	CandidateGroups string `json:"candidateGroups,omitempty"`
	FormID          string `json:"formId,omitempty"`
	// Priority is the task's importance from the model (default 50); the inbox
	// sorts by it. DueDate is the absolute due instant in Unix milliseconds, or 0
	// when the task has no due date (ADR-0091).
	Priority int32 `json:"priority"`
	DueDate  int64 `json:"dueDate,omitempty"`
	// Lane is the organizational lane the task's element is drawn in (ADR-0121), leaf name
	// for a flat model; LanePath is the outermost-to-leaf path for a nested lane. Both empty
	// when the task is in no lane. Metadata only — the Tasks app groups/labels by it.
	Lane     string   `json:"lane,omitempty"`
	LanePath []string `json:"lanePath,omitempty"`
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
	q := r.URL.Query()
	limit := maxTaskListDefault
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > maxTaskListMax {
		limit = maxTaskListMax
	}

	// ?processInstance= scopes the list to one instance's open user tasks, resolved
	// through that instance's own element index rather than the global activatable
	// scan — so a small embedded client (the order-to-cash demo, which asks only for
	// its own instance's task) always finds it, even when a flood has pushed that
	// task past the global page cap.
	if v := strings.TrimSpace(q.Get("processInstance")); v != "" {
		s.listTasksForInstance(w, v, limit)
		return
	}

	// ?before= is the newest-first pagination cursor: the job key handed back as
	// X-Tasks-Next-Cursor on the previous (truncated) page. Absent, the scan starts
	// from the newest task.
	var before uint64
	if v := strings.TrimSpace(q.Get("before")); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid before cursor (want a job key)")
			return
		}
		before = n
	}

	tasks := []taskResp{}
	truncated := false
	var nextCursor uint64
	var scanErr error
	s.do(func() {
		// Newest-first so a capped page shows the most recently created tasks — the
		// ones a just-started instance is parked on — instead of the oldest backlog
		// that a flood would otherwise pin to the front forever.
		err := s.store.ActivatableJobsDesc(compiler.UserTaskJobTypeIndex, before, func(jobKey uint64) error {
			if len(tasks) >= limit {
				truncated = true
				return errListTruncated // page full: stop before enriching more jobs
			}
			jv, ok, err := s.store.GetJob(jobKey)
			if err != nil || !ok {
				return err
			}
			tasks = append(tasks, s.enrichTask(jobKey, jv))
			nextCursor = jobKey // desc scan: the last kept key is the smallest on the page
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list tasks: "+scanErr.Error())
		return
	}
	if truncated {
		// Signal a capped page and hand back the cursor for the next (older) page, so a
		// client can page through or narrow rather than assume it received every task.
		w.Header().Set("X-Tasks-Truncated", "true")
		w.Header().Set("X-Tasks-Next-Cursor", strconv.FormatUint(nextCursor, 10))
	}
	httpapi.JSON(w, http.StatusOK, tasks)
}

// listTasksForInstance writes one process instance's open user tasks, resolved
// through that instance's own element index (bounded by the instance's size) rather
// than the global activatable scan. This keeps a client that only cares about its
// own instance — the order-to-cash demo — working under a flood that has pushed that
// instance's task past the global /tasks page cap.
func (s *Server) listTasksForInstance(w http.ResponseWriter, raw string, limit int) {
	instKey, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid processInstance (want an instance key)")
		return
	}
	tasks := []taskResp{}
	truncated := false
	var scanErr error
	s.do(func() {
		err := s.store.ElementInstancesOfProcess(instKey, func(elKey uint64) error {
			if len(tasks) >= limit {
				truncated = true
				return errListTruncated
			}
			jobKey, ok, err := s.store.JobOfElement(elKey)
			if err != nil || !ok {
				return err // no job on this element (or a read error)
			}
			jv, ok, err := s.store.GetJob(jobKey)
			if err != nil || !ok {
				return err
			}
			// Only open (activatable) user tasks — the same rows the global list
			// returns. Retries == 0 means the job is parked behind an incident.
			if jv.JobType != compiler.UserTaskJobTypeIndex || jv.Retries <= 0 {
				return nil
			}
			tasks = append(tasks, s.enrichTask(jobKey, jv))
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list tasks: "+scanErr.Error())
		return
	}
	if truncated {
		w.Header().Set("X-Tasks-Truncated", "true")
	}
	httpapi.JSON(w, http.StatusOK, tasks)
}

// enrichTask turns a user-task job into the response row the inbox and the
// single-task lookup both return: the job's key and instance, plus the element's
// name, documentation, assignment metadata, form, priority and due date from the
// compiled process.
// It is the one place that shape is built, so the list and the by-key fetch can never
// drift. Callers run it inside s.do (it reads the store and the deployments map).
func (s *Server) enrichTask(jobKey uint64, jv *model.JobValue) taskResp {
	tr := taskResp{
		Key:                jobKey,
		ProcessInstanceKey: jv.ProcessInstanceKey,
		ElementInstanceKey: jv.ElementInstanceKey,
	}
	if ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey); err == nil && ok {
		tr.ProcessDefKey = ei.ProcessDefKey
		if d, dok := s.deployments[ei.ProcessDefKey]; dok {
			tr.ProcessID = d.ProcessID
			cp := d.cp
			tr.ElementID = cp.ElementBpmnId(ei.ElementId)
			// The organizational lane the task is drawn in (ADR-0121) — metadata for
			// grouping in the inbox: the leaf name plus the outermost-to-leaf path.
			if path := cp.LanePath(ei.ElementId); len(path) > 0 {
				tr.Lane = path[len(path)-1]
				tr.LanePath = path
			}
			if n := cp.Node(ei.ElementId); n.Type == compiler.TypeUserTask {
				detail := cp.UserTask(n.Detail)
				tr.Name = cp.Intern(detail.Name)
				// What the task is actually asking the person to do, if the modeler
				// wrote it down (ADR-0025).
				tr.Documentation = cp.ElementDocumentation(ei.ElementId)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid task key")
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
		httpapi.Error(w, http.StatusInternalServerError, "get task: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no open task with that key")
	default:
		httpapi.JSON(w, http.StatusOK, task)
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
		httpapi.Error(w, http.StatusBadRequest, "invalid job key")
		return
	}
	var req failJobReq
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var (
		found     bool
		notHolder bool
		runErr    error
	)
	s.do(func() {
		jv, ok, err := s.store.GetJob(key)
		if err != nil || !ok {
			return
		}
		found = true
		worker := strings.TrimSpace(req.Worker)
		if worker != "" && !holdsLease(jv, worker, req.LeaseToken) {
			notHolder = true
			return
		}
		s.proc.FailJob(key, req.Retries, req.Message, req.RetryBackoff*int64(time.Millisecond))
		if worker != "" {
			if name, ok := s.jobTypes.Name(jv.JobType); ok {
				s.workers.failed(worker, name)
			}
		}
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "fail job: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no job with that key")
	case notHolder:
		httpapi.Error(w, http.StatusConflict,
			"worker "+strconv.Quote(strings.TrimSpace(req.Worker))+" does not hold this job's current lease; it may have elapsed and been taken by another worker")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"jobKey": key})
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
//
// Because a person is forcing a step the engine would not have taken on its own, the
// body must carry a "reason" and the completion is recorded as an operator action —
// who, when, on which element, and why — in append-only audit history that the
// instance timeline and the replay surface (ADR-0159). A blank or missing reason is a
// 400: an unexplained manual completion is exactly what this route must not allow.
func (s *Server) handleCompleteJob(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid job key")
		return
	}
	// Variables are parsed (and rejected on bad JSON) before the job is looked
	// up, so a malformed body is a 400 regardless of whether the key exists —
	// matching handleCompleteTask. An empty body completes with no variables.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// Forcing a step the engine would not have taken on its own is an intervention, so
	// it must say why (ADR-0159). The reason is required — not merely recorded when
	// offered — because the audit trail is the whole point of this route: without it a
	// completed step would carry no explanation of why a person overrode the model.
	var payload struct {
		Reason     string `json:"reason"`
		Worker     string `json:"worker"`
		LeaseToken uint64 `json:"leaseToken"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	reason := strings.TrimSpace(payload.Reason)
	// A worker reporting the outcome of a job it holds a lease on is the ordinary
	// protocol path (ADR-0007), not an intervention: it is doing exactly what the
	// model asked for. Only a completion *by hand* is an override, and only that one
	// needs a reason and an audit entry. The lease is what separates them — a caller
	// naming a worker it is not the holder for is refused below rather than quietly
	// demoted to an operator, so this cannot become a way around the audit trail.
	worker := strings.TrimSpace(payload.Worker)
	if worker == "" && reason == "" {
		httpapi.Error(w, http.StatusBadRequest, "reason is required: completing a job by hand is an operator intervention and is recorded with who did it and why")
		return
	}
	// A name is not a claim the protocol can trust on its own, so a worker completion
	// carries the token its lease was issued with (ADR-0007).
	if worker != "" && payload.LeaseToken == 0 {
		httpapi.Error(w, http.StatusBadRequest, "leaseToken is required when completing as a worker; it is returned by the activation that leased the job")
		return
	}
	// Who is intervening, for the audit trail (ADR-0159/0098): the authenticated
	// principal's username, or "" when auth is off (single-user) or the caller is
	// unidentified. Read off the request before the run loop, like every other
	// principal read.
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}
	var (
		found     bool
		notHolder bool
		runErr    error
	)
	s.do(func() {
		jv, ok, err := s.store.GetJob(key)
		if err != nil || !ok {
			return
		}
		found = true
		if worker != "" {
			if !holdsLease(jv, worker, payload.LeaseToken) {
				notHolder = true
				return
			}
			s.proc.CompleteJob(key, vars...)
			if name, ok := s.jobTypes.Name(jv.JobType); ok {
				s.workers.completed(worker, name)
			}
		} else {
			s.proc.CompleteJobManually(key, actor, reason, vars...)
		}
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "complete job: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no job with that key")
	case notHolder:
		httpapi.Error(w, http.StatusConflict,
			"worker "+strconv.Quote(worker)+" does not hold this job's current lease; it may have elapsed and been taken by another worker. Lease it again, or complete it as an operator with a reason")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"jobKey": key})
	}
}

// handleResolveIncident resolves the incident on an element instance and resumes
// its job (ADR-0061): the body's retries (default 1) is how many attempts the
// re-activated job gets. 404 if there is no incident on that element.
func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid element instance key")
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
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "resolve incident: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no incident on that element instance")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"elementInstanceKey": key, "jobKey": jobKey, "retries": retries})
	}
}

// incidentType names what an incident parked, the distinction the operator views
// label: a job incident holds a service-task job whose retries ran out; a job-less
// incident is a timer whose FEEL schedule stopped resolving (ADR-0064/0111).
func incidentType(v *model.IncidentValue) string {
	if v.JobKey != 0 {
		return "job"
	}
	return "timer"
}

// handleListIncidents lists the unresolved incidents — the operator "what's stuck"
// view (ADR-0061). Optionally scoped to one process instance (?instance=) or one
// deployed definition (?process=): the replay badges a single instance's incidents
// and the live view a whole version's, and neither should have to pull the server's
// entire — page-capped — incident list to find its own (ADR-0151).
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	limit := maxTaskListMax // incidents share the task list's ceiling; the default page is generous
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		if limit = n; limit > maxTaskListMax {
			limit = maxTaskListMax
		}
	}
	var instanceFilter, processFilter uint64
	for _, f := range []struct {
		name string
		dst  *uint64
	}{{"instance", &instanceFilter}, {"process", &processFilter}} {
		q := strings.TrimSpace(r.URL.Query().Get(f.name))
		if q == "" {
			continue
		}
		n, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid "+f.name+" (want a key)")
			return
		}
		*f.dst = n
	}
	list := []incidentView{}
	truncated := false
	var scanErr error
	s.do(func() {
		// One instance is looked up once however many of its elements are stuck, and
		// a flood of incidents is usually a flood on few instances.
		type instanceCtx struct {
			defKey    uint64
			processID string
			cp        *compiler.CompiledProcess
		}
		resolved := map[uint64]instanceCtx{}
		// One resolver for the whole page: the connector store is read once, not once
		// per parked token, and not at all when nothing on the page is on a connector
		// task (ADR-0159).
		connectorFor := s.incidentConnectorLookup()
		lookup := func(piKey uint64) (instanceCtx, error) {
			if ctx, ok := resolved[piKey]; ok {
				return ctx, nil
			}
			var ctx instanceCtx
			pi, ok, err := s.store.ProcessInstance(piKey)
			if err != nil {
				return ctx, err
			}
			if ok {
				ctx.defKey = pi.ProcessDefKey
				if d, ok := s.deployments[pi.ProcessDefKey]; ok {
					ctx.processID, ctx.cp = d.ProcessID, d.cp
				}
			}
			resolved[piKey] = ctx
			return ctx, nil
		}
		err := s.store.Incidents(func(elKey uint64, v *model.IncidentValue) error {
			if instanceFilter != 0 && v.ProcessInstanceKey != instanceFilter {
				return nil
			}
			ctx, err := lookup(v.ProcessInstanceKey)
			if err != nil {
				return err
			}
			if processFilter != 0 && ctx.defKey != processFilter {
				return nil
			}
			if len(list) >= limit {
				truncated = true
				return errListTruncated // page full: bound the response even under a flood of failures
			}
			view := incidentView{
				ElementInstanceKey: elKey,
				ProcessInstanceKey: v.ProcessInstanceKey,
				ProcessDefKey:      ctx.defKey,
				ProcessID:          ctx.processID,
				JobKey:             v.JobKey,
				ElementIndex:       v.ElementId,
				Type:               incidentType(v),
				RaisedAt:           v.RaisedAt,
				Message:            v.Message,
			}
			if ctx.cp != nil {
				view.ElementID = ctx.cp.ElementBpmnId(v.ElementId)
				view.Connector, view.ConnectorKind, view.ConnectorID = connectorFor(ctx.cp, v.ElementId)
			}
			list = append(list, view)
			return nil
		})
		scanErr = unlessTruncated(err)
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list incidents: "+scanErr.Error())
		return
	}
	if truncated {
		w.Header().Set("X-Incidents-Truncated", "true")
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"incidents": list})
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid task key")
		return
	}
	// A completion may carry the submitted form's data as {"variables": {...}},
	// written into the instance scope on completion exactly like a service-task
	// worker's outputs (ADR-0028; the engine path landed with ADR-0039). An empty
	// body completes with no variables.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
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
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "complete task: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no open task with that key")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"taskKey": key})
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
		httpapi.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	assignee := strings.TrimSpace(body.Assignee)

	if s.authEnabled {
		p := httpapi.PrincipalFrom(r.Context())
		if p == nil {
			httpapi.Error(w, http.StatusUnauthorized, "authentication required")
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
				httpapi.Error(w, http.StatusInternalServerError, "claim: "+vErr.Error())
				return
			}
			if !known {
				httpapi.Error(w, http.StatusBadRequest, "unknown or disabled user")
				return
			}
			assignee = canonical
		}
	} else if assignee == "" {
		httpapi.Error(w, http.StatusBadRequest, "assignee is required")
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
		httpapi.Error(w, http.StatusBadRequest, "invalid task key")
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
	})
	// Drive the jobs this command unblocked OUTSIDE the run loop: the handlers are
	// where a connector's outbound call happens, and holding the single writer for
	// its duration is the stall ADR-0157 step 6 removes.
	if runErr == nil {
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "assign task: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no open task with that key")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"taskKey": key, "assignee": assignee})
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
		httpapi.Error(w, http.StatusInternalServerError, "read stats: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, stats)
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
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "empty request body: expected BPMN XML")
		return
	}
	pid, name := processIdentity(body)
	if pid == "" {
		httpapi.Error(w, http.StatusBadRequest, "cannot save draft: no <process id> in the diagram")
		return
	}
	projectID := r.URL.Query().Get("projectId")
	hasProjectParam := r.URL.Query().Has("projectId")
	rec := draft{ProcessID: pid, Name: name, ProjectID: projectID, SavedAt: time.Now().Unix(), XML: string(body)}
	var (
		saveErr, projErr error
		unknownProject   bool
		protectedProject bool
	)
	s.do(func() {
		if !hasProjectParam {
			existing, ok, e := s.drafts.Get(pid)
			if e == nil && ok {
				rec.ProjectID = existing.ProjectID
			}
		} else if projectID != "" {
			proj, ok, e := s.projects.Get(projectID)
			if e != nil {
				projErr = e
				return
			}
			if !ok {
				unknownProject = true
				return
			}
			// A protected system project's content is platform-managed (ADR-0122):
			// refuse authoring a draft into it, for any caller.
			if proj.Protected {
				protectedProject = true
				return
			}
		}
		saveErr = s.drafts.Save(rec)
	})
	switch {
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save draft: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, draftResp{ProcessID: pid, Name: name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt})
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
		recs, loadErr = s.drafts.LoadAll()
		for _, d := range recs {
			if filter != "" && d.ProjectID != filter {
				continue
			}
			list = append(list, draftResp{ProcessID: d.ProcessID, Name: d.Name, ProjectID: d.ProjectID, SavedAt: d.SavedAt})
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list drafts: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleMoveDraft reassigns a draft to a different project (or to Ungrouped when
// projectId is empty), without touching its XML. Body: {"projectId": "..."}. A
// non-empty projectId must name an existing project (ADR-0034).
func (s *Server) handleMoveDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var (
		found, unknownProject    bool
		protectedProject         bool
		getErr, projErr, saveErr error
		view                     draftResp
	)
	s.do(func() {
		rec, ok, e := s.drafts.Get(id)
		if e != nil {
			getErr = e
			return
		}
		if !ok {
			return
		}
		found = true
		if payload.ProjectID != "" {
			proj, pok, pe := s.projects.Get(payload.ProjectID)
			if pe != nil {
				projErr = pe
				return
			}
			if !pok {
				unknownProject = true
				return
			}
			// A protected system project's content is platform-managed (ADR-0122):
			// refuse moving a draft into it, for any caller.
			if proj.Protected {
				protectedProject = true
				return
			}
		}
		rec.ProjectID = payload.ProjectID
		if saveErr = s.drafts.Save(rec); saveErr != nil {
			return
		}
		view = draftResp{ProcessID: rec.ProcessID, Name: rec.Name, ProjectID: rec.ProjectID, SavedAt: rec.SavedAt}
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read draft: "+getErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no draft with that process id")
	case projErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read project: "+projErr.Error())
	case unknownProject:
		httpapi.Error(w, http.StatusBadRequest, "unknown project id")
	case protectedProject:
		httpapi.Error(w, http.StatusForbidden, "protected system project cannot be modified")
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "move draft: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, view)
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
	s.do(func() { rec, ok, readErr = s.drafts.Get(id) })
	switch {
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read draft: "+readErr.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no draft with that process id")
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
	s.do(func() { delErr = s.drafts.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete draft: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// defaultJobLease is how long a worker holds a job when it does not say (ADR-0007).
// Five minutes is long enough for ordinary work and short enough that a crashed
// worker's job is offered again while someone still cares.
const defaultJobLease = 5 * time.Minute

// maxJobLease bounds what a worker may ask for. A lease is the only thing that gets a
// job back from a worker that died, so an unbounded one is a job parked forever by a
// typo.
const maxJobLease = 24 * time.Hour

// handleActivateJob leases one job to an external worker (ADR-0007). While the lease
// holds, the job is off the activatable index — no other worker is offered it — and it
// records who holds it until when. When the lease elapses the job is offered again,
// which is what makes a worker crash recoverable without an operator.
//
// It leases a job the caller *names*. The type-keyed pull an external worker really
// wants ("give me the next send-email job") is not here yet, and deliberately so: job
// type indices are interned per process, so the global activatable index cannot tell
// one definition's "send-email" from another's "charge-card" — see ADR-0007. A worker
// that lists an instance's jobs and leases them by key is unambiguous today.
// maxJobPollWait bounds how long a type-keyed pull may hold its request open
// waiting for work. It is well under the proxy and load-balancer idle timeouts a
// deployment is likely to sit behind, so a poll ends by answering rather than by
// being cut off — a severed request looks like an error to a worker, and an empty
// answer does not.
const maxJobPollWait = 50 * time.Second

// maxJobsPerPull bounds one type-keyed activation. A worker that asks for more is
// given this many: the cap exists so a single request cannot lease an entire
// backlog, which would starve every other worker for a whole lease period.
const maxJobsPerPull = 100

// pulledJob is one leased job as a worker receives it: enough to do the work and
// report the outcome without a second call. Variables are the ones *visible at the
// task* — the element instance's scope chain, so an activity-local input mapping
// shadows the instance value exactly as it does for an in-process worker.
type pulledJob struct {
	JobKey             uint64 `json:"jobKey"`
	Type               string `json:"type"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	ProcessDefKey      uint64 `json:"processDefKey"`
	ElementID          string `json:"elementId"`
	Retries            int32  `json:"retries"`
	LeaseExpiresAt     int64  `json:"leaseExpiresAt"`
	// LeaseToken fences this lease: the worker presents it when it reports the
	// outcome, and a report carrying a token the job has moved past is refused
	// (ADR-0007). It is what makes a second instance of the same worker deployment
	// distinguishable from the first, which the worker id alone cannot do.
	LeaseToken uint64         `json:"leaseToken"`
	Variables  map[string]any `json:"variables"`
	// Connector is a connector task resolved into plain values, present only for a
	// job whose kind Atlas no longer serves itself (ADR-0168). Absent for a plain
	// job-worker task, where there is nothing authored to resolve.
	Connector *connectorPayload `json:"connector,omitempty"`
}

// handleActivateJobsByType is the type-keyed pull ADR-0007 designed and its
// amendment deferred: a worker asks for the next jobs of a *named* type rather
// than having to know a job key. It is what makes an out-of-process worker
// possible at all, and it only became correct once job types were resolved into
// one engine-wide index space (ADR-0157) — before that a name meant a different
// index in every definition.
//
// Two types are refused rather than served. One an in-process worker is already
// draining would be worked twice, because that runner does not lease: it
// dispatches whatever is activatable. And a user task is not worker work — it
// waits for a person and is claimed through the Tasks app.
//
// Body: {"type": "send-email", "worker": "w1", "leaseMs": 30000, "maxJobs": 5}.
// An empty jobs array is the ordinary answer for an idle queue; the caller polls
// again. (Long-poll, so it need not, is the next slice.)
func (s *Server) handleActivateJobsByType(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type    string `json:"type"`
		Worker  string `json:"worker"`
		LeaseMs int64  `json:"leaseMs"`
		MaxJobs int    `json:"maxJobs"`
		// WaitMs long-polls: how long to hold the request open waiting for a job of
		// this type before answering empty. 0 (or absent) answers immediately.
		WaitMs int64 `json:"waitMs"`
		// Connectors names the connector configurations this worker holds — the
		// providers it can actually reach. Only the worker knows this: once a kind is
		// offloaded the engine holds no credential for it and cannot read another
		// process's environment. Reporting it here is what lets the Workers view say
		// which names deployed models reference and *nothing* can serve (ADR-0168).
		Connectors []string `json:"connectors,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	body.Type = strings.TrimSpace(body.Type)
	if body.Type == "" {
		httpapi.Error(w, http.StatusBadRequest, "type is required: a worker leases by job type")
		return
	}
	lease := defaultJobLease
	if body.LeaseMs > 0 {
		lease = time.Duration(body.LeaseMs) * time.Millisecond
	}
	if lease > maxJobLease {
		httpapi.Error(w, http.StatusBadRequest,
			"leaseMs is longer than the "+maxJobLease.String()+" maximum; a lease is what returns a job from a dead worker")
		return
	}
	want := body.MaxJobs
	if want <= 0 {
		want = 1
	}
	if want > maxJobsPerPull {
		want = maxJobsPerPull
	}
	wait := time.Duration(body.WaitMs) * time.Millisecond
	if wait > maxJobPollWait {
		wait = maxJobPollWait
	}

	var (
		unknown  bool
		inProc   bool
		jobs     []pulledJob
		scanErr  error
		runErr   error
		reserved bool
		jobType  int32
	)
	attempt := func() {
		s.do(func() {
			var ok bool
			jobType, ok = s.jobTypes.Index(body.Type)
			if !ok {
				unknown = true
				return
			}
			if jobType == compiler.UserTaskJobTypeIndex {
				reserved = true
				return
			}
			if s.jobRunner.Handles(jobType) {
				inProc = true
				return
			}
			// Collect first, then lease: activating mutates the very index being scanned.
			var keys []uint64
			if scanErr = s.store.ActivatableJobs(jobType, func(k uint64) error {
				if len(keys) < want {
					keys = append(keys, k)
				}
				return nil
			}); scanErr != nil {
				return
			}
			for _, k := range keys {
				s.proc.ActivateJob(k, body.Worker, int64(lease))
			}
			// Applying the activation is all that is needed here: leasing takes a job
			// off the activatable index, so it unblocks no in-process handler and there
			// is nothing to run off the loop.
			if runErr = s.proc.RunUntilIdle(); runErr != nil {
				return
			}
			for _, k := range keys {
				j, ok := s.pulledJob(k, body.Type)
				if !ok {
					continue // completed or re-leased between the scan and here
				}
				jobs = append(jobs, j)
			}
			s.workers.leased(body.Worker, body.Type, len(jobs))
			// Recorded on every poll, not just a productive one: an idle worker still
			// holds its connectors, and it is exactly the idle case an operator is
			// looking at when work is not moving.
			s.workers.holdsConnectors(body.Worker, body.Connectors)
		})
	}

	attempt()
	// Long-poll: park until the engine says a job of this type is durable, rather
	// than answering empty and being asked again.
	//
	// Two things make this correct. The waiter is registered BEFORE the retry, so a
	// job created between the failed attempt and the registration still wakes it —
	// register-then-look, never look-then-register. And the waiting happens out here,
	// outside s.do: a request holding the run loop while it waits would freeze the
	// single writer for the whole poll, which is the stall this entire sequence
	// exists to remove.
	if wait > 0 && len(jobs) == 0 && !unknown && !inProc && !reserved && scanErr == nil && runErr == nil {
		woken, cancel := s.jobWaiters.wait(jobType)
		defer cancel()
		attempt()
		if len(jobs) == 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-woken:
				attempt()
			case <-timer.C:
			case <-r.Context().Done():
			case <-s.quit:
			}
		}
	}
	switch {
	case unknown:
		httpapi.Error(w, http.StatusNotFound,
			"no job type "+strconv.Quote(body.Type)+" is known to this engine; it is registered when a process using it is deployed")
	case reserved:
		httpapi.Error(w, http.StatusConflict,
			"a user task is not worker work: it waits for a person and is claimed through the Tasks API")
	case inProc:
		httpapi.Error(w, http.StatusConflict,
			"job type "+strconv.Quote(body.Type)+" is served by an in-process worker; leasing it externally would run the work twice")
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "activate jobs: "+scanErr.Error())
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "activate jobs: "+runErr.Error())
	default:
		if jobs == nil {
			jobs = []pulledJob{}
		}
		httpapi.JSON(w, http.StatusOK, map[string]any{"jobs": jobs, "worker": body.Worker})
	}
}

// connectorPayload is a connector task resolved into plain values: everything the
// worker needs to do the work, and nothing that only the engine can understand.
type connectorPayload struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields"`
}

// resolveConnectorTask turns a leased connector job into the payload a worker can
// act on, or nil when there is nothing to resolve.
//
// This is the split ADR-0168 draws. Finding a task's detail in the compiled process
// and evaluating it against the instance's variables is engine work by necessity —
// FEEL is compiled at deploy (ADR-0008/0015), and a worker has neither the compiled
// process nor the scope chain. So the engine resolves, and only the *values* travel;
// the worker holds the endpoint and the credential and makes the call.
//
// Resolution happens at lease time rather than at call time, so a variable changed
// in between is not seen. That is the same instant the in-process handler already
// read, moved earlier by the length of one lease.
//
// Runs on the run-loop goroutine, which is where the store and the deployment
// registry are readable.
func (s *Server) resolveConnectorTask(jobKey uint64, jv *model.JobValue, ei *model.ElementInstanceValue, cp *compiler.CompiledProcess) *connectorPayload {
	node := cp.Node(ei.ElementId)
	if node.Type != compiler.TypeConnectorTask {
		return nil // a plain job-worker task: nothing authored to resolve
	}
	switch jv.JobType {
	case compiler.CsvImportJobTypeIndex:
		j, err := csvimport.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), jv.ElementInstanceKey)
		if err != nil {
			// The worker will fail the job with a message of its own; refusing to lease
			// here would park the token with nothing said about why.
			return nil
		}
		return &connectorPayload{Kind: "csv", Fields: map[string]any{
			"source": j.Source, "delimiter": j.Delimiter, "hasHeader": j.HasHeader,
			"columns": j.Columns, "resultVariable": j.Result,
			"format": j.Format, "operation": j.Operation,
		}}
	case compiler.LdifJobTypeIndex:
		// A pure transform: what travels is the file text (or the entries) and the
		// format, and there is no credential to leave behind.
		j, err := ldif.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), jv.ElementInstanceKey)
		if err != nil {
			return nil
		}
		return &connectorPayload{Kind: "ldif", Fields: map[string]any{
			"format": j.Format, "operation": j.Operation,
			"source": j.Source, "resultVariable": j.Result,
		}}
	case compiler.MailJobTypeIndex:
		// The message travels; the SMTP host and password do not. What names the
		// credential is the connector's name, which the worker resolves against its
		// own configuration — the whole of ADR-0168's decision, in one field.
		j, err := mail.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), ei.ProcessInstanceKey, jobKey)
		if err != nil {
			return nil
		}
		return &connectorPayload{Kind: "mail", Fields: map[string]any{
			"connector": j.Connector, "from": j.From, "to": j.To, "cc": j.Cc, "bcc": j.Bcc,
			"subject": j.Subject, "body": j.Body, "html": j.HTML, "messageId": j.MessageID,
		}}
	case compiler.MsSqlJobTypeIndex, compiler.MariaDBJobTypeIndex, compiler.PostgresJobTypeIndex:
		// The statement and its bound parameters travel; the DSN does not exist here
		// to travel. SQL is the first kind with no in-process handler at all, so this
		// is not one of two paths that could drift — it is the only one (ADR-0170).
		j, err := sqldb.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), jv.ElementInstanceKey)
		if err != nil {
			return nil
		}
		return &connectorPayload{Kind: j.Product, Fields: map[string]any{
			"connector": j.Connector, "product": j.Product, "operation": j.Operation,
			"statement": j.Statement, "params": j.Params, "named": j.Named,
			"maxRows": j.MaxRows, "resultVariable": j.ResultVariable,
		}}
	case compiler.AdJobTypeIndex:
		// AD authors its own server (ADR-0166), so unlike mail the endpoint travels.
		// What does not is the bind password: the *reference* travels and whoever runs
		// the job resolves it, so an offloaded AD task never reads the engine's vault
		// (ADR-0168).
		j, err := ad.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), ei.ProcessInstanceKey)
		if err != nil {
			return nil
		}
		return &connectorPayload{Kind: "ad", Fields: map[string]any{
			"url": j.URL, "bindDN": j.BindDN, "bindSecretRef": j.BindSecret,
			"startTLS": j.StartTLS, "operation": j.Operation, "dn": j.DN,
			"memberDN": j.MemberDN, "newDN": j.NewDN, "newPassword": j.NewPassword,
			"attributes": j.Attributes, "baseDN": j.BaseDN, "filter": j.Filter,
			"cookie": j.Cookie, "cookieVariable": j.CookieVariable,
			"maxEntries": j.MaxEntries, "objectSecurity": j.ObjectSecurity,
			"resultVariable": j.ResultVariable,
		}}
	case compiler.EntraJobTypeIndex:
		// The operation and the ids travel; the tenant's app credential does not
		// exist here to travel. Worker-only, like the SQL kinds (ADR-0171).
		j, err := entra.Resolve(s.store, cp, cp.ConnectorTask(node.Detail), jv.ElementInstanceKey)
		if err != nil {
			return nil
		}
		return &connectorPayload{Kind: "entra", Fields: map[string]any{
			"connector": j.Connector, "operation": j.Operation, "userId": j.UserID,
			"groupId": j.GroupID, "attributes": j.Attributes, "resultVariable": j.ResultVariable,
		}}
	}
	return nil
}

// holdsLease reports whether a worker's report may be trusted: the job must be
// leased right now, to that worker, under the very lease the token names.
//
// The token is what the worker id cannot supply. Two instances of one worker
// deployment share a name, so when the first one's lease elapses and the second
// takes the job, a late report from the first still presents the right *name*.
// The epoch moves with every lease, so the stale report names a lease the job has
// left behind and is refused (ADR-0007's fencing item).
func holdsLease(jv *model.JobValue, worker string, token uint64) bool {
	return jv.LeaseExpiresAt != 0 && jv.Assignee == worker && jv.LeaseEpoch == token && token != 0
}

// pulledJob reads one leased job back out of state, with the element it sits on
// and the variables visible there. It reads the lease deadline back rather than
// recomputing it: the engine froze that value from its own clock, and it is what
// the worker has to trust (invariant I6). Runs on the run-loop goroutine.
func (s *Server) pulledJob(jobKey uint64, typeName string) (pulledJob, bool) {
	jv, ok, err := s.store.GetJob(jobKey)
	if err != nil || !ok {
		return pulledJob{}, false
	}
	j := pulledJob{
		JobKey:             jobKey,
		Type:               typeName,
		ProcessInstanceKey: jv.ProcessInstanceKey,
		ElementInstanceKey: jv.ElementInstanceKey,
		Retries:            jv.Retries,
		LeaseExpiresAt:     jv.LeaseExpiresAt,
		LeaseToken:         jv.LeaseEpoch,
		Variables:          map[string]any{},
	}
	if ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey); err == nil && ok {
		j.ProcessDefKey = ei.ProcessDefKey
		if d, dok := s.deployments[ei.ProcessDefKey]; dok {
			j.ElementID = d.cp.ElementBpmnId(ei.ElementId)
			j.Connector = s.resolveConnectorTask(jobKey, jv, ei, d.cp)
		}
	}
	// The task's own scope chain, so an input mapping shadows the instance value.
	if err := s.store.VisibleVariablesOfScope(jv.ElementInstanceKey, func(v *model.VariableValue) error {
		j.Variables[v.Name] = nativeVar(v)
		return nil
	}); err != nil {
		return pulledJob{}, false
	}
	return j, true
}

func (s *Server) handleActivateJob(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid job key")
		return
	}
	var body struct {
		Worker  string `json:"worker"`
		LeaseMs int64  `json:"leaseMs"`
	}
	// An empty body is a valid activation: an anonymous worker taking the default lease.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	lease := defaultJobLease
	if body.LeaseMs > 0 {
		lease = time.Duration(body.LeaseMs) * time.Millisecond
	}
	if lease > maxJobLease {
		httpapi.Error(w, http.StatusBadRequest,
			"leaseMs is longer than the "+maxJobLease.String()+" maximum; a lease is what returns a job from a dead worker")
		return
	}

	var (
		found  bool
		held   bool
		expiry int64
		runErr error
	)
	s.do(func() {
		jv, ok, err := s.store.GetJob(key)
		if err != nil || !ok {
			return
		}
		found = true
		if jv.LeaseExpiresAt != 0 {
			held = true // another worker holds it; its lease has to elapse first
			return
		}
		s.proc.ActivateJob(key, body.Worker, int64(lease))
		// As above: a lease unblocks no in-process handler, so applying the command is
		// the whole of the work and it belongs on the loop.
		runErr = s.proc.RunUntilIdle()
		if runErr != nil {
			return
		}
		// Read back what was committed rather than recomputing the deadline here: the
		// handler froze it from the engine's clock, and that is the value the worker has
		// to trust (invariant I6).
		if jv, ok, err := s.store.GetJob(key); err == nil && ok {
			expiry = jv.LeaseExpiresAt
		}
	})
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "activate job: "+runErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no job with that key")
	case held:
		httpapi.Error(w, http.StatusConflict, "another worker holds this job; its lease has not elapsed yet")
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"jobKey": key, "worker": body.Worker, "leaseExpiresAt": expiry,
		})
	}
}
