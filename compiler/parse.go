package compiler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// defaultRetries is used when a service task's task definition omits retries.
const defaultRetries = 3

// restMethods is the set of HTTP methods a REST connector task may use. The set
// is validated at deploy time (invariant I5) so the runtime worker never has to.
var restMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true,
}

// normalizeHTTPMethod upper-cases a REST connector's method (defaulting to GET
// when omitted) and rejects anything outside restMethods.
func normalizeHTTPMethod(m string) (string, error) {
	if m == "" {
		return "GET", nil
	}
	up := strings.ToUpper(strings.TrimSpace(m))
	if !restMethods[up] {
		return "", fmt.Errorf("unsupported HTTP method %q", m)
	}
	return up, nil
}

// Parse reads a BPMN 2.0 XML model and compiles the first <process> into an
// immutable CompiledProcess keyed by key at the given version. It is the front
// end to the linearizer (compiler.md stages 1–2 and 6): it parses the XML,
// resolves string element ids to integer indices, and pours the result into the
// shared Builder. Validation beyond reference integrity (reachability, gateway
// coverage) is a later stage.
//
// Service-task job types come from the Zeebe task-definition extension element
// (<zeebe:taskDefinition type="..." retries="..."/>), the de-facto standard for
// executable BPMN.
func Parse(key uint64, version int32, r io.Reader) (*CompiledProcess, error) {
	defs, err := decodeDefinitions(r)
	if err != nil {
		return nil, err
	}
	if len(defs.Processes) == 0 {
		return nil, fmt.Errorf("compiler: no <process> element in definitions")
	}
	return compileProcess(key, version, defs.Processes[0], buildMessageResolver(defs))
}

// Deployable is one executable process compiled from a model, plus the display
// metadata a collaboration provides. PoolName is the participant (pool) name that
// references the process — "" for a standalone <process> outside any
// <collaboration>; ProcessName is the process's own name attribute.
type Deployable struct {
	Process     *CompiledProcess
	PoolName    string
	ProcessName string
}

// ParseAll compiles every executable process in a model — the collaboration case,
// where a <collaboration> has several <participant> pools, each referencing a
// <process>. A process is executable (and thus returned) iff it has a start
// event; a participant whose process is a black box (no start event, or none) is
// skipped rather than erroring, since a message-flow counterpart pool is often
// left unmodeled. The i-th executable process (document order) is keyed baseKey+i,
// so a caller assigning keys sequentially advances its counter by len(result). It
// errors only if the model has no executable process at all.
func ParseAll(baseKey uint64, version int32, r io.Reader) ([]Deployable, error) {
	defs, err := decodeDefinitions(r)
	if err != nil {
		return nil, err
	}
	resolve := buildMessageResolver(defs)
	poolName := participantNames(defs)

	var out []Deployable
	for _, proc := range defs.Processes {
		if len(proc.StartEvents) == 0 {
			continue // black-box pool: nothing to run
		}
		cp, err := compileProcess(baseKey+uint64(len(out)), version, proc, resolve)
		if err != nil {
			return nil, err
		}
		out = append(out, Deployable{Process: cp, PoolName: poolName[proc.Id], ProcessName: proc.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("compiler: no executable <process> (a process needs a start event)")
	}
	return out, nil
}

// ParseNamed compiles the single process with the given BPMN process id. It is
// the reload path: a stored deployment records which process (by id) within its
// (possibly collaboration) XML it represents, so recovery recompiles exactly that
// one under its original key.
func ParseNamed(key uint64, version int32, r io.Reader, processId string) (*CompiledProcess, error) {
	defs, err := decodeDefinitions(r)
	if err != nil {
		return nil, err
	}
	for _, proc := range defs.Processes {
		if proc.Id == processId {
			return compileProcess(key, version, proc, buildMessageResolver(defs))
		}
	}
	return nil, fmt.Errorf("compiler: no <process> with id %q in model", processId)
}

func decodeDefinitions(r io.Reader) (xmlDefinitions, error) {
	var defs xmlDefinitions
	if err := xml.NewDecoder(r).Decode(&defs); err != nil {
		return xmlDefinitions{}, fmt.Errorf("compiler: parse BPMN: %w", err)
	}
	return defs, nil
}

// participantNames maps each referenced process id to its participant (pool) name.
func participantNames(defs xmlDefinitions) map[string]string {
	if defs.Collaboration == nil {
		return nil
	}
	m := make(map[string]string, len(defs.Collaboration.Participants))
	for _, p := range defs.Collaboration.Participants {
		if p.ProcessRef != "" {
			m[p.ProcessRef] = p.Name
		}
	}
	return m
}

// buildMessageResolver indexes a model's top-level <message> declarations and
// returns a resolver from a messageRef to the message's name and its compiled
// correlation-key expression. An empty correlation key compiles to nil, which
// evaluates to "" — matching only publishes with an empty key.
func buildMessageResolver(defs xmlDefinitions) func(ownerId, messageRef string) (string, *expr.Compiled, error) {
	messages := make(map[string]xmlMessage, len(defs.Messages))
	for _, m := range defs.Messages {
		if m.Id != "" {
			messages[m.Id] = m
		}
	}
	return func(ownerId, messageRef string) (string, *expr.Compiled, error) {
		m, ok := messages[messageRef]
		if !ok {
			return "", nil, fmt.Errorf("compiler: message event %q references unknown message %q", ownerId, messageRef)
		}
		if m.Name == "" {
			return "", nil, fmt.Errorf("compiler: message %q referenced by %q has no name", messageRef, ownerId)
		}
		var keyExpr *expr.Compiled
		if text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.Subscription.CorrelationKey), "=")); text != "" {
			ce, err := expr.CompileAuto(text)
			if err != nil {
				return "", nil, fmt.Errorf("compiler: message %q correlationKey: %w", messageRef, err)
			}
			keyExpr = ce
		}
		return m.Name, keyExpr, nil
	}
}

// compileProcess linearizes one <process> into an immutable CompiledProcess,
// resolving message references through resolveMessage (shared across a
// collaboration's processes).
func compileProcess(key uint64, version int32, proc xmlProcess, resolveMessage func(ownerId, messageRef string) (string, *expr.Compiled, error)) (*CompiledProcess, error) {
	b := NewBuilder(key, proc.Id, version)
	ids := make(map[string]int32, len(proc.StartEvents)+len(proc.ServiceTasks)+len(proc.EndEvents))
	register := func(id string, nodeID int32) error {
		if id == "" {
			return fmt.Errorf("compiler: element with empty id")
		}
		if _, dup := ids[id]; dup {
			return fmt.Errorf("compiler: duplicate element id %q", id)
		}
		ids[id] = nodeID
		b.SetElementBpmnId(nodeID, id) // retain for the live diagram overlay
		return nil
	}

	for _, s := range proc.StartEvents {
		if s.Message != nil {
			name, keyExpr, err := resolveMessage(s.Id, s.Message.MessageRef)
			if err != nil {
				return nil, err
			}
			if err := register(s.Id, b.AddMessageStartEvent(name, keyExpr)); err != nil {
				return nil, err
			}
			continue
		}
		if err := register(s.Id, b.AddStartEvent()); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ServiceTasks {
		retries := int32(defaultRetries)
		if r := st.TaskDefinition.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: service task %q has invalid retries %q: %w", st.Id, r, err)
			}
			retries = int32(n)
		}
		// A service task bearing an <atlas:clioConnector> extension is a connector
		// task: it delegates to a server-registered clio connector via the job path
		// (ADR-0036), not to an external service-task worker.
		if c := st.Clio; c != nil {
			if c.Connector == "" || c.Subject == "" || c.EventType == "" {
				return nil, fmt.Errorf("compiler: clio connector task %q needs connector, subject, and eventType", st.Id)
			}
			if err := register(st.Id, b.AddClioWriteTask(c.Connector, c.Subject, c.EventType, retries)); err != nil {
				return nil, err
			}
			continue
		}
		// A service task bearing an <atlas:restConnector> extension is an HTTP-REST
		// connector task: it delegates to a server-registered REST connector via the
		// job path (ADR-0036), not to an external service-task worker.
		if c := st.Rest; c != nil {
			if c.Connector == "" || c.Path == "" {
				return nil, fmt.Errorf("compiler: rest connector task %q needs connector and path", st.Id)
			}
			method, err := normalizeHTTPMethod(c.Method)
			if err != nil {
				return nil, fmt.Errorf("compiler: rest connector task %q: %w", st.Id, err)
			}
			if err := register(st.Id, b.AddRestConnectorTask(c.Connector, method, c.Path, retries)); err != nil {
				return nil, err
			}
			continue
		}
		if st.TaskDefinition.Type == "" {
			return nil, fmt.Errorf("compiler: service task %q has no task definition type", st.Id)
		}
		if err := register(st.Id, b.AddServiceTask(st.TaskDefinition.Type, retries)); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ScriptTasks {
		text := strings.TrimSpace(st.Script.Expression)
		text = strings.TrimPrefix(text, "=") // Zeebe marks expressions with a leading '='
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("compiler: script task %q has no expression", st.Id)
		}
		if st.Script.ResultVariable == "" {
			return nil, fmt.Errorf("compiler: script task %q has no result variable", st.Id)
		}
		// FEEL is compiled once, at deploy time (ADR-0008/0015). CompileAuto
		// discovers the process variables the expression reads; a syntax or type
		// error fails here — i.e. fails deploy.
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: script task %q: %w", st.Id, err)
		}
		if err := register(st.Id, b.AddScriptTask(e, st.Script.ResultVariable)); err != nil {
			return nil, err
		}
	}
	for _, brt := range proc.BusinessRuleTasks {
		if brt.CalledDecision.DecisionId == "" {
			return nil, fmt.Errorf("compiler: business rule task %q has no calledDecision decisionId", brt.Id)
		}
		retries := int32(defaultRetries)
		if r := brt.CalledDecision.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return nil, fmt.Errorf("compiler: business rule task %q has invalid retries %q: %w", brt.Id, r, err)
			}
			retries = int32(n)
		}
		inputs, err := decisionInputs(brt.Inputs)
		if err != nil {
			return nil, fmt.Errorf("compiler: business rule task %q: %w", brt.Id, err)
		}
		mappings, err := decisionInputMappings(brt.Id, brt.InputMappings)
		if err != nil {
			return nil, err
		}
		node, err := b.AddBusinessRuleTaskMapped(brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries)
		if err != nil {
			return nil, err
		}
		if err := register(brt.Id, node); err != nil {
			return nil, err
		}
	}
	for _, ut := range proc.UserTasks {
		retries := int32(defaultRetries)
		if err := register(ut.Id, b.AddUserTask(ut.Name, ut.Assignment.Assignee, ut.Assignment.CandidateGroups, retries)); err != nil {
			return nil, err
		}
	}
	for _, g := range proc.ExclusiveGateways {
		if err := register(g.Id, b.AddExclusiveGateway()); err != nil {
			return nil, err
		}
	}
	for _, g := range proc.ParallelGateways {
		if err := register(g.Id, b.AddParallelGateway()); err != nil {
			return nil, err
		}
	}
	for _, g := range proc.InclusiveGateways {
		if err := register(g.Id, b.AddInclusiveGateway()); err != nil {
			return nil, err
		}
	}
	for _, ev := range proc.IntermediateCatchEvents {
		switch {
		case ev.Timer != nil:
			text := strings.TrimSpace(ev.Timer.TimeDuration)
			text = strings.TrimSpace(strings.TrimPrefix(text, "=")) // tolerate a FEEL '=' prefix
			nanos, err := parseISO8601Duration(text)
			if err != nil {
				return nil, fmt.Errorf("compiler: intermediate catch event %q timer: %w", ev.Id, err)
			}
			if err := register(ev.Id, b.AddTimerCatchEvent(nanos)); err != nil {
				return nil, err
			}
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return nil, err
			}
			if err := register(ev.Id, b.AddMessageCatchEvent(name, keyExpr)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("compiler: intermediate catch event %q: only timer and message events are supported yet", ev.Id)
		}
	}
	for _, ev := range proc.IntermediateThrowEvents {
		if ev.Message == nil {
			return nil, fmt.Errorf("compiler: intermediate throw event %q: only message events are supported yet", ev.Id)
		}
		name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
		if err != nil {
			return nil, err
		}
		if err := register(ev.Id, b.AddMessageThrowEvent(name, keyExpr)); err != nil {
			return nil, err
		}
	}
	// An undefined <task> and a <manualTask> have no execution semantics, so Atlas
	// runs them as pass-throughs (the token flows straight on). This lets a model
	// be drafted and its routing — e.g. a gateway's branches — tested before its
	// tasks are given real implementations.
	for _, t := range proc.Tasks {
		if err := register(t.Id, b.AddTask()); err != nil {
			return nil, err
		}
	}
	for _, t := range proc.ManualTasks {
		if err := register(t.Id, b.AddTask()); err != nil {
			return nil, err
		}
	}
	for _, e := range proc.EndEvents {
		if err := register(e.Id, b.AddEndEvent()); err != nil {
			return nil, err
		}
	}
	// Boundary events are registered last: each attaches to a host activity by id,
	// which must already be registered so attachedToRef resolves (ADR-0040). An
	// absent or "true" cancelActivity is interrupting (BPMN default); "false" is
	// non-interrupting.
	for _, ev := range proc.BoundaryEvents {
		host, ok := ids[ev.AttachedToRef]
		if !ok {
			return nil, fmt.Errorf("compiler: boundary event %q attaches to unknown activity %q", ev.Id, ev.AttachedToRef)
		}
		interrupting := ev.CancelActivity != "false"
		switch {
		case ev.Timer != nil:
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ev.Timer.TimeDuration), "="))
			nanos, err := parseISO8601Duration(text)
			if err != nil {
				return nil, fmt.Errorf("compiler: boundary event %q timer: %w", ev.Id, err)
			}
			if err := register(ev.Id, b.AddBoundaryTimerEvent(host, interrupting, nanos)); err != nil {
				return nil, err
			}
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return nil, err
			}
			if err := register(ev.Id, b.AddBoundaryMessageEvent(host, interrupting, name, keyExpr)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("compiler: boundary event %q: only timer and message boundary events are supported yet", ev.Id)
		}
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	// Report an unsupported element with a clear message rather than letting it
	// surface later as a confusing "unknown targetRef" when a flow points at it.
	for _, u := range []struct {
		label string
		nodes []xmlNode
	}{
		{"sendTask", proc.SendTasks}, {"receiveTask", proc.ReceiveTasks},
	} {
		if len(u.nodes) > 0 {
			return nil, fmt.Errorf("compiler: element %q is a <%s>, which Atlas can't execute yet "+
				"(supported: start/end events, tasks (undefined/manual pass-through, service, script, "+
				"business rule, user), exclusive/parallel/inclusive gateways, and timer/message intermediate events)", u.nodes[0].Id, u.label)
		}
	}

	// Connect flows, compiling any FEEL condition, and remember each BPMN flow id
	// so a gateway's default flow can be marked afterwards.
	flowIdx := make(map[string]int32, len(proc.Flows))
	for _, f := range proc.Flows {
		src, ok := ids[f.SourceRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown sourceRef %q", f.Id, f.SourceRef)
		}
		tgt, ok := ids[f.TargetRef]
		if !ok {
			return nil, fmt.Errorf("compiler: flow %q references unknown targetRef %q", f.Id, f.TargetRef)
		}
		fid := b.Connect(src, tgt)
		flowIdx[f.Id] = fid
		if cond := strings.TrimSpace(f.Condition); cond != "" {
			cond = strings.TrimSpace(strings.TrimPrefix(cond, "=")) // FEEL condition, '=' prefix per Zeebe
			ce, err := expr.CompileAuto(cond)
			if err != nil {
				return nil, fmt.Errorf("compiler: flow %q condition: %w", f.Id, err)
			}
			b.SetFlowCondition(fid, ce)
		}
	}
	// Mark each exclusive/inclusive gateway's default flow (taken when no
	// condition holds).
	markDefault := func(kind, gid, def string) error {
		if def == "" {
			return nil
		}
		fid, ok := flowIdx[def]
		if !ok {
			return fmt.Errorf("compiler: %s gateway %q default references unknown flow %q", kind, gid, def)
		}
		b.SetFlowDefault(fid)
		return nil
	}
	for _, g := range proc.ExclusiveGateways {
		if err := markDefault("exclusive", g.Id, g.Default); err != nil {
			return nil, err
		}
	}
	for _, g := range proc.InclusiveGateways {
		if err := markDefault("inclusive", g.Id, g.Default); err != nil {
			return nil, err
		}
	}

	return b.Build()
}

// BPMN XML is matched by element/attribute local name, so namespace prefixes
// (bpmn:, zeebe:) are handled transparently by encoding/xml.

type xmlDefinitions struct {
	Processes     []xmlProcess      `xml:"process"`
	Messages      []xmlMessage      `xml:"message"`
	Collaboration *xmlCollaboration `xml:"collaboration"`
}

// A collaboration groups participant pools. Each participant references the
// <process> it contains; the participant carries the pool's display name (a
// process in a collaboration is often unnamed, the pool is what's labelled).
type xmlCollaboration struct {
	Participants []xmlParticipant `xml:"participant"`
}

type xmlParticipant struct {
	Id         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	ProcessRef string `xml:"processRef,attr"`
}

// A top-level message declaration. Its Zeebe subscription carries the FEEL
// correlationKey expression shared by every catch/throw event that references it.
type xmlMessage struct {
	Id           string               `xml:"id,attr"`
	Name         string               `xml:"name,attr"`
	Subscription xmlZeebeSubscription `xml:"extensionElements>subscription"`
}

type xmlZeebeSubscription struct {
	CorrelationKey string `xml:"correlationKey,attr"`
}

type xmlMessageEventDefinition struct {
	MessageRef string `xml:"messageRef,attr"`
}

type xmlProcess struct {
	Id                string                `xml:"id,attr"`
	Name              string                `xml:"name,attr"`
	StartEvents       []xmlStartEvent       `xml:"startEvent"`
	EndEvents         []xmlNode             `xml:"endEvent"`
	ServiceTasks      []xmlServiceTask      `xml:"serviceTask"`
	ScriptTasks       []xmlScriptTask       `xml:"scriptTask"`
	BusinessRuleTasks []xmlBusinessRuleTask `xml:"businessRuleTask"`
	ExclusiveGateways []xmlExclusiveGateway `xml:"exclusiveGateway"`

	IntermediateCatchEvents []xmlIntermediateCatchEvent `xml:"intermediateCatchEvent"`
	IntermediateThrowEvents []xmlIntermediateThrowEvent `xml:"intermediateThrowEvent"`
	BoundaryEvents          []xmlBoundaryEvent          `xml:"boundaryEvent"`

	Flows []xmlSequenceFlow `xml:"sequenceFlow"`

	Tasks             []xmlNode             `xml:"task"`
	ManualTasks       []xmlNode             `xml:"manualTask"`
	ParallelGateways  []xmlNode             `xml:"parallelGateway"`
	InclusiveGateways []xmlInclusiveGateway `xml:"inclusiveGateway"`

	UserTasks []xmlUserTask `xml:"userTask"`

	// Captured only to give a clear "unsupported element" error (see Parse); none
	// of these are executable yet.
	SendTasks    []xmlNode `xml:"sendTask"`
	ReceiveTasks []xmlNode `xml:"receiveTask"`
}

// A data-based exclusive gateway; default names the flow taken when no outgoing
// condition matches.
type xmlExclusiveGateway struct {
	Id      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
}

// A start event. A plain (none) start event is a manual entry point; one bearing
// a messageEventDefinition is a message start event, instantiated by a
// correlating message (ADR-0035). The definition is a pointer so an absent one
// is detected as nil.
type xmlStartEvent struct {
	Id      string                     `xml:"id,attr"`
	Name    string                     `xml:"name,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

// A data-based inclusive gateway; default names the flow taken when no outgoing
// condition matches.
type xmlInclusiveGateway struct {
	Id      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
}

// An intermediate catch event; the timer and message variants are executable.
// Each definition is a pointer so an absent one is detected as nil.
type xmlIntermediateCatchEvent struct {
	Id      string                     `xml:"id,attr"`
	Timer   *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

// An intermediate throw event; only the message variant is executable so far.
type xmlIntermediateThrowEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

// A boundary event is attached to a host activity (AttachedToRef) and arms while
// it runs. CancelActivity mirrors BPMN's attribute: absent or "true" is
// interrupting (cancels the host on fire), "false" is non-interrupting. The timer
// and message variants are executable; each definition is a pointer so an absent
// one is detected as nil (ADR-0040).
type xmlBoundaryEvent struct {
	Id             string                     `xml:"id,attr"`
	AttachedToRef  string                     `xml:"attachedToRef,attr"`
	CancelActivity string                     `xml:"cancelActivity,attr"`
	Timer          *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message        *xmlMessageEventDefinition `xml:"messageEventDefinition"`
}

type xmlTimerEventDefinition struct {
	TimeDuration string `xml:"timeDuration"` // ISO-8601 duration, e.g. PT30S
}

type xmlNode struct {
	Id string `xml:"id,attr"`
}

// A user task parks a token for human completion (ADR-0028). It optionally
// carries a zeebe:assignmentDefinition for assignee/candidateGroups.
type xmlUserTask struct {
	Id         string                  `xml:"id,attr"`
	Name       string                  `xml:"name,attr"`
	Assignment xmlAssignmentDefinition `xml:"extensionElements>assignmentDefinition"`
}

type xmlAssignmentDefinition struct {
	Assignee        string `xml:"assignee,attr"`
	CandidateGroups string `xml:"candidateGroups,attr"`
}

type xmlServiceTask struct {
	Id             string            `xml:"id,attr"`
	TaskDefinition xmlTaskDefinition `xml:"extensionElements>taskDefinition"`
	// Clio, when present, marks this service task a clio connector task (ADR-0036).
	// The pointer is nil when the <atlas:clioConnector> extension is absent.
	Clio *xmlClioConnector `xml:"extensionElements>clioConnector"`
	// Rest, when present, marks this service task an HTTP-REST connector task
	// (ADR-0036). The pointer is nil when the <atlas:restConnector> extension is
	// absent.
	Rest *xmlRestConnector `xml:"extensionElements>restConnector"`
}

// A clio connector task's parameters, carried on a service task as an
// <atlas:clioConnector connector="..." subject="..." eventType="..."/> extension
// element. connector names a server-registered connector (its endpoint and
// credentials live in the server config, never in the model); subject and
// eventType are the clio coordinates the appended event lands under.
type xmlClioConnector struct {
	Connector string `xml:"connector,attr"`
	Subject   string `xml:"subject,attr"`
	EventType string `xml:"eventType,attr"`
}

// An HTTP-REST connector task's parameters, carried on a service task as an
// <atlas:restConnector connector="..." method="..." path="..."/> extension
// element. connector names a server-registered connector (its base endpoint and
// credentials live in the server config, never in the model); method is the HTTP
// method and path is appended to the connector's base endpoint to form the
// request URL.
type xmlRestConnector struct {
	Connector string `xml:"connector,attr"`
	Method    string `xml:"method,attr"`
	Path      string `xml:"path,attr"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// Zeebe script tasks carry the FEEL expression and its result variable in a
// <zeebe:script> extension element.
type xmlScriptTask struct {
	Id     string         `xml:"id,attr"`
	Script xmlZeebeScript `xml:"extensionElements>script"`
}

type xmlZeebeScript struct {
	Expression     string `xml:"expression,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
}

// A business rule task references a DMN decision via the Zeebe calledDecision
// extension (<zeebe:calledDecision decisionId="..." resultVariable="..."/>). Its
// input context comes from two layers merged at evaluation time:
//
//   - Variable-driven inputs — the real wiring — are Zeebe io-mapping inputs
//     (<zeebe:ioMapping><zeebe:input source="=order.total" target="Amount"/>), a
//     FEEL source evaluated over the instance's variables bound to a decision
//     input name.
//   - Static inputs are constant Atlas decisionInput elements
//     (<atlas:decisionInput name="Season" value="Winter"/>); each value is parsed
//     as JSON when it parses, else kept as a string, so numbers and booleans reach
//     the decision with their FEEL types. They are a constant base a mapping of the
//     same name overrides.
//
// The decision's result is written back into the resultVariable process variable.
type xmlBusinessRuleTask struct {
	Id             string               `xml:"id,attr"`
	CalledDecision xmlCalledDecision    `xml:"extensionElements>calledDecision"`
	Inputs         []xmlDecisionInput   `xml:"extensionElements>decisionInput"`
	InputMappings  []xmlZeebeIOMapInput `xml:"extensionElements>ioMapping>input"`
}

type xmlCalledDecision struct {
	DecisionId     string `xml:"decisionId,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
}

type xmlDecisionInput struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// xmlZeebeIOMapInput is a Zeebe io-mapping input: a FEEL source expression bound
// to a target name. For a business rule task the target is the DMN decision input
// name the source's value feeds.
type xmlZeebeIOMapInput struct {
	Source string `xml:"source,attr"`
	Target string `xml:"target,attr"`
}

// decisionInputs turns parsed <decisionInput> elements into a name→value map,
// parsing each value as JSON when possible so numbers and booleans keep their
// types (a plain string that is not valid JSON is used verbatim).
func decisionInputs(in []xmlDecisionInput) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	m := make(map[string]any, len(in))
	for _, di := range in {
		if di.Name == "" {
			return nil, fmt.Errorf("decisionInput with empty name")
		}
		if _, dup := m[di.Name]; dup {
			return nil, fmt.Errorf("duplicate decisionInput name %q", di.Name)
		}
		var v any
		if err := json.Unmarshal([]byte(di.Value), &v); err != nil {
			v = di.Value // not JSON: treat as a literal string
		}
		m[di.Name] = v
	}
	return m, nil
}

// decisionInputMappings compiles a business rule task's io-mapping inputs into
// variable-driven decision inputs. Each source is a FEEL expression (compiled
// once at deploy time, invariant I5) evaluated over the instance's variables at
// evaluation time; target names the decision input it feeds. A leading '=' (the
// Zeebe expression marker) is trimmed. An empty target or an uncompilable source
// fails the deploy, exactly like a bad script-task expression.
func decisionInputMappings(taskID string, in []xmlZeebeIOMapInput) ([]DecisionInputMapping, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]DecisionInputMapping, 0, len(in))
	for _, im := range in {
		if im.Target == "" {
			return nil, fmt.Errorf("compiler: business rule task %q has an input mapping with no target", taskID)
		}
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(im.Source), "="))
		if text == "" {
			return nil, fmt.Errorf("compiler: business rule task %q input mapping for %q has no source expression", taskID, im.Target)
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: business rule task %q input mapping for %q: %w", taskID, im.Target, err)
		}
		out = append(out, DecisionInputMapping{Target: im.Target, Source: e})
	}
	return out, nil
}

type xmlSequenceFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
	// Condition is the FEEL guard text from a <conditionExpression> child, if any.
	Condition string `xml:"conditionExpression"`
}
