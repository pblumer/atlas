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

// defaultUserTaskPriority mirrors Camunda's default task priority (ADR-0051): a
// user task with no zeebe:priorityDefinition sorts as if priority 50.
const defaultUserTaskPriority = 50

// scriptJobTypes maps a polyglot script task's language (lower-cased) to the
// reserved job type its in-process worker subscribes to (ADR-0047). Adding a
// language is one entry here plus its worker; the compiler, node type, and
// recovery semantics are shared. The mapping is resolved at deploy time
// (invariant I5) so the runtime never inspects the language.
var scriptJobTypes = map[string]string{
	"powershell": PwshJobType,
	"python":     PythonJobType,
	"javascript": JsJobType,
}

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

// httpKVMap turns a REST connector's header or query-parameter child elements into
// a {name:value} map, trimming names and skipping rows with an empty name (an
// incomplete row the author hasn't filled in). A duplicated name is an error, so a
// silent last-wins collision can't hide a modeling mistake. kind names the field
// for the error message ("header" / "query parameter").
func httpKVList(taskID, kind string, kvs []xmlHTTPKV) ([]RestKV, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make([]RestKV, 0, len(kvs))
	seen := make(map[string]bool, len(kvs))
	for _, kv := range kvs {
		name := strings.TrimSpace(kv.Name)
		if name == "" {
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("compiler: rest connector task %q has a duplicate %s %q", taskID, kind, name)
		}
		seen[name] = true
		val, err := restValue(taskID, kind+" "+name, kv.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, RestKV{Name: name, Val: val})
	}
	return out, nil
}

// restValue turns a model field value into a literal or a compiled FEEL expression
// (the Camunda-style fx toggle, ADR-0067): a value with a leading '=' is a FEEL
// expression compiled once at deploy time (invariant I5) and evaluated over the
// instance's variables at call time; otherwise it is a literal used verbatim. what
// names the field for error messages.
func restValue(taskID, what, raw string) (RestExpr, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "=") {
		return RestExpr{Literal: raw}, nil
	}
	text := strings.TrimSpace(trimmed[1:])
	if text == "" {
		return RestExpr{}, fmt.Errorf("compiler: rest connector task %q has an empty FEEL expression for %s", taskID, what)
	}
	e, err := expr.CompileAuto(text)
	if err != nil {
		return RestExpr{}, fmt.Errorf("compiler: rest connector task %q: %s: %w", taskID, what, err)
	}
	return RestExpr{Expr: e}, nil
}

// restAuth reads a REST connector's authentication config from its extension.
// authType selects the scheme; an unknown scheme is rejected, and a scheme that
// needs a secret reference must name one (secrets live server-side, ADR-0041, so
// the model always references rather than carries them).
func restAuth(taskID string, c *xmlRestConnector) (RestAuth, error) {
	t := strings.ToLower(strings.TrimSpace(c.AuthType))
	switch t {
	case "", "none":
		return RestAuth{}, nil
	case "basic", "bearer", "apikey":
		if strings.TrimSpace(c.AuthSecret) == "" {
			return RestAuth{}, fmt.Errorf("compiler: rest connector task %q uses %s auth but names no secret reference", taskID, t)
		}
		if t == "apikey" && strings.TrimSpace(c.AuthApiKeyName) == "" {
			return RestAuth{}, fmt.Errorf("compiler: rest connector task %q uses apiKey auth but names no header", taskID)
		}
		return RestAuth{
			Type:       t,
			Username:   strings.TrimSpace(c.AuthUsername),
			ApiKeyName: strings.TrimSpace(c.AuthApiKeyName),
			SecretRef:  strings.TrimSpace(c.AuthSecret),
		}, nil
	default:
		return RestAuth{}, fmt.Errorf("compiler: rest connector task %q has an unsupported auth type %q", taskID, c.AuthType)
	}
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
	// isExecutable defaults to true when the attribute is absent (BPMN convention;
	// Atlas has always run every deployed process), so an existing model without it
	// keeps working. Only an explicit "false" marks a process non-executable.
	b.SetExecutable(proc.IsExecutable != "false")
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
		if s.Timer != nil {
			schedule, err := parseTimerSchedule(s.Timer)
			if err != nil {
				return nil, fmt.Errorf("compiler: start event %q timer: %w", s.Id, err)
			}
			if schedule.IsFeel() && len(schedule.Expr.Inputs()) > 0 {
				return nil, fmt.Errorf("compiler: start event %q: a timer start event's FEEL schedule must be constant (reference no variables) — a start event has no instance to evaluate against (ADR-0056)", s.Id)
			}
			if err := register(s.Id, b.AddTimerStartEvent(schedule)); err != nil {
				return nil, err
			}
			continue
		}
		if err := register(s.Id, b.AddStartEvent()); err != nil {
			return nil, err
		}
		// A none start event may carry a start form; the first one that does wins
		// as the process's start form (ADR-0028).
		if s.Form.FormId != "" {
			b.SetStartFormId(s.Form.FormId)
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
		// connector task: it calls the model-authored URL via the job path
		// (ADR-0067), not an external service-task worker. The URL lives in the model
		// (unlike clio's registry-only endpoint); credentials never do.
		if c := st.Rest; c != nil {
			if strings.TrimSpace(c.Url) == "" {
				return nil, fmt.Errorf("compiler: rest connector task %q needs a url", st.Id)
			}
			method, err := normalizeHTTPMethod(c.Method)
			if err != nil {
				return nil, fmt.Errorf("compiler: rest connector task %q: %w", st.Id, err)
			}
			url, err := restValue(st.Id, "url", c.Url)
			if err != nil {
				return nil, err
			}
			headers, err := httpKVList(st.Id, "header", c.Headers)
			if err != nil {
				return nil, err
			}
			query, err := httpKVList(st.Id, "query parameter", c.QueryParams)
			if err != nil {
				return nil, err
			}
			auth, err := restAuth(st.Id, c)
			if err != nil {
				return nil, err
			}
			id := b.AddRestConnectorTask(RestConfig{
				Method:    method,
				Url:       url,
				ResultVar: strings.TrimSpace(c.ResultVariable),
				Headers:   headers,
				Query:     query,
				Auth:      auth,
				Retries:   retries,
			})
			if err := register(st.Id, id); err != nil {
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
		// A script task bearing an <atlas:jobScript> extension is a polyglot script
		// task: it runs in a general-purpose language via the job path (ADR-0047),
		// not inline as FEEL. The language is validated and mapped to its reserved
		// job type at deploy time (invariant I5), so the runtime worker never has to.
		if js := st.JobScript; js != nil {
			jobType, ok := scriptJobTypes[strings.ToLower(strings.TrimSpace(js.Language))]
			if !ok {
				return nil, fmt.Errorf("compiler: script task %q has unsupported script language %q", st.Id, js.Language)
			}
			source := strings.TrimSpace(js.Source)
			if source == "" {
				return nil, fmt.Errorf("compiler: script task %q has no script source", st.Id)
			}
			if js.ResultVariable == "" {
				return nil, fmt.Errorf("compiler: script task %q has no result variable", st.Id)
			}
			node := b.AddScriptJobTask(jobType, strings.ToLower(strings.TrimSpace(js.Language)), source, js.ResultVariable, defaultRetries)
			if err := register(st.Id, node); err != nil {
				return nil, err
			}
			continue
		}
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
		// A business rule task marked with <atlas:temisConnector> is a central
		// decision: it delegates to the named server-registered temis connector
		// instead of the embedded library (ADR-0050). Authoring is otherwise
		// identical.
		var node int32
		if tc := brt.TemisConnector; tc != nil {
			if tc.Connector == "" {
				return nil, fmt.Errorf("compiler: business rule task %q has a temisConnector with no connector name", brt.Id)
			}
			node, err = b.AddTemisDecisionTask(tc.Connector, brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries)
		} else {
			node, err = b.AddBusinessRuleTaskMapped(brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries, decisionBinding(brt.CalledDecision.BindingType))
		}
		if err != nil {
			return nil, err
		}
		if err := register(brt.Id, node); err != nil {
			return nil, err
		}
	}
	for _, ut := range proc.UserTasks {
		retries := int32(defaultRetries)
		priority := int32(defaultUserTaskPriority)
		if s := strings.TrimSpace(ut.Priority.Priority); s != "" {
			p, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("compiler: user task %q priority %q: %w", ut.Id, s, err)
			}
			priority = int32(p)
		}
		var dueDateNanos int64
		if s := strings.TrimSpace(ut.Schedule.DueDate); s != "" {
			s = strings.TrimSpace(strings.TrimPrefix(s, "=")) // tolerate a FEEL '=' prefix
			nanos, err := parseISO8601Duration(s)
			if err != nil {
				return nil, fmt.Errorf("compiler: user task %q dueDate: %w", ut.Id, err)
			}
			dueDateNanos = nanos
		}
		if err := register(ut.Id, b.AddUserTask(ut.Name, ut.Assignment.Assignee, ut.Assignment.CandidateGroups, ut.Form.FormId, priority, dueDateNanos, retries)); err != nil {
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
			schedule, err := parseTimerSchedule(ev.Timer)
			if err != nil {
				return nil, fmt.Errorf("compiler: intermediate catch event %q timer: %w", ev.Id, err)
			}
			if schedule.Repeats() {
				return nil, fmt.Errorf("compiler: intermediate catch event %q: timeCycle is not supported (a catch fires once); use timeDuration or timeDate", ev.Id)
			}
			if err := register(ev.Id, b.AddTimerCatchSchedule(schedule)); err != nil {
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
		// A message end event publishes its message then ends; a plain end event
		// just ends (ADR-0052).
		if e.Message != nil {
			name, keyExpr, err := resolveMessage(e.Id, e.Message.MessageRef)
			if err != nil {
				return nil, err
			}
			if err := register(e.Id, b.AddMessageEndEvent(name, keyExpr)); err != nil {
				return nil, err
			}
			continue
		}
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
			schedule, err := parseTimerSchedule(ev.Timer)
			if err != nil {
				return nil, fmt.Errorf("compiler: boundary event %q timer: %w", ev.Id, err)
			}
			if schedule.Repeats() && interrupting {
				return nil, fmt.Errorf("compiler: boundary event %q: an interrupting boundary timer does not support timeCycle (it fires once); use timeDuration or timeDate, or make the boundary non-interrupting", ev.Id)
			}
			if err := register(ev.Id, b.AddBoundaryTimerSchedule(host, interrupting, schedule)); err != nil {
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

	// Data objects are not flow nodes (no token flows through them), so they are
	// added as a separate collection, not registered as flow nodes (ADR-0053). A
	// nameless data object falls back to its BPMN id so it stays addressable.
	//
	// A data object's identity is its name: several <dataObjectReference>s (and even
	// several <dataObject> elements) sharing a name are *views* of one logical object
	// — the BPMN way to place it near several activities without long arrows. So every
	// id resolves through objName for association wiring, but the object is seeded only
	// once per name (the first occurrence wins its item type / initial state), so the
	// engine sees one object, not several.
	objName := make(map[string]string, len(proc.DataObjects)) // BPMN id → data-object name
	seededObj := make(map[string]bool, len(proc.DataObjects))
	for _, d := range proc.DataObjects {
		name := d.Name
		if name == "" {
			name = d.Id
		}
		objName[d.Id] = name
		if seededObj[name] {
			continue // already have a logical object of this name; this is another view
		}
		seededObj[name] = true
		b.AddDataObject(name, d.ItemSubjectRef, d.DataState.Name, d.IsCollection)
	}

	// Wire data-output associations now that every activity node is registered
	// (ADR-0058). A dataObjectReference resolves to its data object plus the target
	// data state; a targetRef may also name a data object directly (no state change).
	refs := make(map[string]xmlDataObjectReference, len(proc.DataObjectReferences))
	for _, ref := range proc.DataObjectReferences {
		refs[ref.Id] = ref
	}
	// resolveDataTarget maps an association's targetRef to the data-object name it
	// writes and the data state it moves the object into.
	resolveDataTarget := func(ownerId, targetRef string) (name, state string, err error) {
		if ref, ok := refs[targetRef]; ok {
			name, ok := objName[ref.DataObjectRef]
			if !ok {
				return "", "", fmt.Errorf("compiler: data output association on %q references data object reference %q whose dataObjectRef %q is unknown", ownerId, targetRef, ref.DataObjectRef)
			}
			return name, ref.DataState.Name, nil
		}
		if name, ok := objName[targetRef]; ok {
			return name, "", nil // targets the object directly; no state change
		}
		return "", "", fmt.Errorf("compiler: data output association on %q has unknown targetRef %q", ownerId, targetRef)
	}
	wireDataOut := func(ownerId string, assocs []xmlDataOutputAssociation) error {
		for _, a := range assocs {
			name, state, err := resolveDataTarget(ownerId, a.TargetRef)
			if err != nil {
				return err
			}
			var valExpr *expr.Compiled
			if from := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Assignment.From), "=")); from != "" {
				ce, err := expr.CompileAuto(from)
				if err != nil {
					return fmt.Errorf("compiler: data output association on %q assignment: %w", ownerId, err)
				}
				valExpr = ce
			}
			b.AddDataOutputAssociation(ids[ownerId], name, valExpr, state, strings.TrimSpace(a.Assignment.To))
		}
		return nil
	}
	for _, st := range proc.ServiceTasks {
		if err := wireDataOut(st.Id, st.DataOut); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ScriptTasks {
		if err := wireDataOut(st.Id, st.DataOut); err != nil {
			return nil, err
		}
	}
	for _, brt := range proc.BusinessRuleTasks {
		if err := wireDataOut(brt.Id, brt.DataOut); err != nil {
			return nil, err
		}
	}
	for _, ut := range proc.UserTasks {
		if err := wireDataOut(ut.Id, ut.DataOut); err != nil {
			return nil, err
		}
	}
	for _, t := range proc.Tasks {
		if err := wireDataOut(t.Id, t.DataOut); err != nil {
			return nil, err
		}
	}
	for _, t := range proc.ManualTasks {
		if err := wireDataOut(t.Id, t.DataOut); err != nil {
			return nil, err
		}
	}

	// Wire data-input associations: a sourceRef names the data object read (resolved
	// like an output target, its state ignored on a read); a targetRef is the process
	// variable the read value is written into (ADR-0059).
	wireDataIn := func(ownerId string, assocs []xmlDataInputAssociation) error {
		for _, a := range assocs {
			name, _, err := resolveDataTarget(ownerId, a.SourceRef)
			if err != nil {
				return fmt.Errorf("compiler: data input association on %q source: %w", ownerId, err)
			}
			// The target variable is the assignment's <to> (a free string the Modeler
			// writes — a drawn association's own <targetRef> is a generated data-input
			// property id, not a variable name). A hand-authored <targetRef> is the
			// fallback when no <to> is given.
			variable := strings.TrimSpace(a.Assignment.To)
			if variable == "" {
				variable = strings.TrimSpace(a.TargetRef)
			}
			if variable == "" {
				return fmt.Errorf("compiler: data input association on %q has no target variable (set the assignment's <to>)", ownerId)
			}
			var valExpr *expr.Compiled
			if from := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Assignment.From), "=")); from != "" {
				ce, err := expr.CompileAuto(from)
				if err != nil {
					return fmt.Errorf("compiler: data input association on %q assignment: %w", ownerId, err)
				}
				valExpr = ce
			}
			b.AddDataInputAssociation(ids[ownerId], name, variable, valExpr)
		}
		return nil
	}
	for _, st := range proc.ServiceTasks {
		if err := wireDataIn(st.Id, st.DataIn); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ScriptTasks {
		if err := wireDataIn(st.Id, st.DataIn); err != nil {
			return nil, err
		}
	}
	for _, brt := range proc.BusinessRuleTasks {
		if err := wireDataIn(brt.Id, brt.DataIn); err != nil {
			return nil, err
		}
	}
	for _, ut := range proc.UserTasks {
		if err := wireDataIn(ut.Id, ut.DataIn); err != nil {
			return nil, err
		}
	}
	for _, t := range proc.Tasks {
		if err := wireDataIn(t.Id, t.DataIn); err != nil {
			return nil, err
		}
	}
	for _, t := range proc.ManualTasks {
		if err := wireDataIn(t.Id, t.DataIn); err != nil {
			return nil, err
		}
	}

	// Wire generic zeebe:ioMapping input/output mappings (ADR-0068). Each source is a
	// FEEL expression compiled once at deploy time (invariant I5); an empty target or
	// an uncompilable source fails the deploy, exactly like a bad script-task
	// expression. The compiler only records them here; the engine applies them.
	compileSource := func(ownerId, dir, target, source string) (*expr.Compiled, error) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "="))
		if text == "" {
			return nil, fmt.Errorf("compiler: task %q ioMapping %s for %q has no source expression", ownerId, dir, target)
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: task %q ioMapping %s for %q: %w", ownerId, dir, target, err)
		}
		return e, nil
	}
	wireIO := func(ownerId string, iom xmlZeebeIOMapping) error {
		for _, in := range iom.Inputs {
			target := strings.TrimSpace(in.Target)
			if target == "" {
				return fmt.Errorf("compiler: task %q has an ioMapping input with no target", ownerId)
			}
			e, err := compileSource(ownerId, "input", target, in.Source)
			if err != nil {
				return err
			}
			b.AddInputMapping(ids[ownerId], target, e)
		}
		for _, out := range iom.Outputs {
			target := strings.TrimSpace(out.Target)
			if target == "" {
				return fmt.Errorf("compiler: task %q has an ioMapping output with no target", ownerId)
			}
			e, err := compileSource(ownerId, "output", target, out.Source)
			if err != nil {
				return err
			}
			b.AddOutputMapping(ids[ownerId], target, e)
		}
		return nil
	}
	for _, st := range proc.ServiceTasks {
		if err := wireIO(st.Id, st.IOMapping); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.ScriptTasks {
		if err := wireIO(st.Id, st.IOMapping); err != nil {
			return nil, err
		}
	}
	for _, ut := range proc.UserTasks {
		if err := wireIO(ut.Id, ut.IOMapping); err != nil {
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
	IsExecutable      string                `xml:"isExecutable,attr"`
	StartEvents       []xmlStartEvent       `xml:"startEvent"`
	EndEvents         []xmlEndEvent         `xml:"endEvent"`
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

	DataObjects          []xmlDataObject          `xml:"dataObject"`
	DataObjectReferences []xmlDataObjectReference `xml:"dataObjectReference"`
}

// A BPMN data object. It is not a flow node — no token flows through it — so it
// carries no sequence flows, only its identity (name, id), an optional collection
// flag, an optional itemDefinition reference (its declared type), and an optional
// <dataState> child naming its initial data state (ADR-0053). dataState is
// spec-legal on any ItemAwareElement.
type xmlDataObject struct {
	Id             string       `xml:"id,attr"`
	Name           string       `xml:"name,attr"`
	IsCollection   bool         `xml:"isCollection,attr"`
	ItemSubjectRef string       `xml:"itemSubjectRef,attr"`
	DataState      xmlDataState `xml:"dataState"`
}

// xmlDataState is a BPMN data state (the [received]/[approved] label on a data
// object or reference), carried by its name.
type xmlDataState struct {
	Name string `xml:"name,attr"`
}

// A BPMN data object reference: a pointer to a <dataObject> (dataObjectRef) that may
// carry its own <dataState> — so the same object can appear on the canvas in several
// states (order [received], order [approved]). A data-output association targets a
// reference to say which object it writes and what state it moves it into (ADR-0058).
type xmlDataObjectReference struct {
	Id            string       `xml:"id,attr"`
	DataObjectRef string       `xml:"dataObjectRef,attr"`
	DataState     xmlDataState `xml:"dataState"`
}

// xmlDataOutputAssociation is a <dataOutputAssociation> on an activity: targetRef
// names the data object (or a <dataObjectReference> to it) the activity writes, and
// the optional <assignment><from> is a FEEL expression, evaluated over the instance's
// variables at completion, that produces the written value (ADR-0058).
type xmlDataOutputAssociation struct {
	TargetRef  string        `xml:"targetRef"`
	Assignment xmlAssignment `xml:"assignment"`
}

// xmlAssignment is a data association's <assignment>: a <from> value expression and a
// <to> target. Atlas reads <from> as the FEEL value expression. On an output
// association <to> is an optional member path within the target data object (e.g.
// "name") — set, the write updates only that member (ADR-0060); empty, it writes the
// whole value. On an input association <to> is unused (its target is targetRef).
type xmlAssignment struct {
	From string `xml:"from"`
	To   string `xml:"to"`
}

// xmlDataInputAssociation is a <dataInputAssociation> on an activity: sourceRef
// names the data object (or a <dataObjectReference> to it) the activity reads,
// targetRef is the process-variable name the read value is written into, and the
// optional <assignment><from> is a FEEL transform evaluated over the instance's
// variables plus the source object bound under its name (ADR-0059).
type xmlDataInputAssociation struct {
	SourceRef  string        `xml:"sourceRef"`
	TargetRef  string        `xml:"targetRef"`
	Assignment xmlAssignment `xml:"assignment"`
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
	// Timer, when present, makes this a timer start event: the process starts a
	// fresh instance on the schedule (duration/date/cycle/cron) the definition
	// carries, armed at deploy time (ADR-0051). A pointer so an absent one is nil.
	Timer *xmlTimerEventDefinition `xml:"timerEventDefinition"`
	// Form binds a start form to a none start event (ADR-0028): the form the UI
	// shows before creating the instance, whose data becomes the start variables.
	// The engine never sees it — it is pre-start UI metadata.
	Form xmlFormDefinition `xml:"extensionElements>formDefinition"`
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

// An end event. A plain (none) end event just ends the instance; one bearing a
// messageEventDefinition is a message end event, which publishes the message
// then ends (ADR-0052). The definition is a pointer so an absent one is nil.
type xmlEndEvent struct {
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
	TimeDate     string `xml:"timeDate"`     // ISO-8601 instant, e.g. 2026-08-01T09:00:00Z (ADR-0051)
	TimeCycle    string `xml:"timeCycle"`    // ISO-8601 repeating interval (R3/PT1H) or cron ("0 * * * *") (ADR-0051)
}

type xmlNode struct {
	Id      string                     `xml:"id,attr"`
	DataOut []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn  []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// A user task parks a token for human completion (ADR-0028). It optionally
// carries a zeebe:assignmentDefinition for assignee/candidateGroups.
type xmlUserTask struct {
	Id         string                     `xml:"id,attr"`
	Name       string                     `xml:"name,attr"`
	Assignment xmlAssignmentDefinition    `xml:"extensionElements>assignmentDefinition"`
	Form       xmlFormDefinition          `xml:"extensionElements>formDefinition"`
	Priority   xmlPriorityDefinition      `xml:"extensionElements>priorityDefinition"`
	Schedule   xmlTaskSchedule            `xml:"extensionElements>taskSchedule"`
	IOMapping  xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	DataOut    []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn     []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlPriorityDefinition carries zeebe:priorityDefinition's static task priority
// (ADR-0051). An empty value means the task uses the default priority.
type xmlPriorityDefinition struct {
	Priority string `xml:"priority,attr"`
}

// xmlTaskSchedule carries zeebe:taskSchedule. Atlas reads dueDate as an ISO-8601
// duration relative to task creation (its timer convention, ADR-0040/0051);
// followUpDate is not yet executable.
type xmlTaskSchedule struct {
	DueDate      string `xml:"dueDate,attr"`
	FollowUpDate string `xml:"followUpDate,attr"`
}

// xmlFormDefinition binds a form to a user task by id (ADR-0028). formId
// references a form stored server-side (the form-js schema the Tasks app
// renders); an empty formId means the task has no form.
type xmlFormDefinition struct {
	FormId string `xml:"formId,attr"`
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
	// (ADR-0067). The pointer is nil when the <atlas:restConnector> extension is
	// absent.
	Rest      *xmlRestConnector          `xml:"extensionElements>restConnector"`
	IOMapping xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	DataOut   []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn    []xmlDataInputAssociation  `xml:"dataInputAssociation"`
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
// <atlas:restConnector> extension element (ADR-0067). method is the HTTP method;
// url is the full request URL, authored in the model; resultVariable, if set, is
// the process variable the JSON response is written back into. Header and
// QueryParam child elements add request headers and query parameters. The auth*
// attributes describe authentication: authType is "basic"/"bearer"/"apiKey";
// authUsername (basic) and authApiKeyName (the apiKey header name) are model data;
// authSecret names a server-side secret (ADR-0041) — never the secret value.
type xmlRestConnector struct {
	Method         string      `xml:"method,attr"`
	Url            string      `xml:"url,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	AuthType       string      `xml:"authType,attr"`
	AuthUsername   string      `xml:"authUsername,attr"`
	AuthApiKeyName string      `xml:"authApiKeyName,attr"`
	AuthSecret     string      `xml:"authSecret,attr"`
	Headers        []xmlHTTPKV `xml:"httpHeader"`
	QueryParams    []xmlHTTPKV `xml:"queryParam"`
}

// xmlHTTPKV is one name/value pair in a REST connector's headers or query
// parameters (an <atlas:httpHeader> or <atlas:queryParam> child element).
type xmlHTTPKV struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// Zeebe script tasks carry the FEEL expression and its result variable in a
// <zeebe:script> extension element. A script task authored in a general-purpose
// language instead carries an <atlas:jobScript> extension (JobScript); the
// distinct local name keeps it from colliding with <zeebe:script> in the XML
// decoder, which matches by local name (ADR-0047).
type xmlScriptTask struct {
	Id     string         `xml:"id,attr"`
	Script xmlZeebeScript `xml:"extensionElements>script"`
	// JobScript, when present, marks this a polyglot job script (PowerShell, …),
	// run via the job path rather than inline as FEEL. The pointer is nil when the
	// <atlas:jobScript> extension is absent.
	JobScript *xmlAtlasScript            `xml:"extensionElements>jobScript"`
	IOMapping xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	DataOut   []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn    []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

type xmlZeebeScript struct {
	Expression     string `xml:"expression,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
}

// An <atlas:jobScript language="..." resultVariable="...">body</atlas:jobScript>
// extension: a script task in a general-purpose language (ADR-0047). language
// selects the interpreter/worker (and thus the reserved job type), resultVariable
// is the process variable the script's result is written back into, and the
// element text is the script source.
type xmlAtlasScript struct {
	Language       string `xml:"language,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Source         string `xml:",chardata"`
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
	// TemisConnector, when present, marks this a central (connector) decision
	// evaluated by a remote temis service rather than the embedded library
	// (ADR-0050). The pointer is nil when the <atlas:temisConnector> extension is
	// absent, i.e. the decision is evaluated locally.
	TemisConnector *xmlTemisConnector         `xml:"extensionElements>temisConnector"`
	DataOut        []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn         []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlTemisConnector is the <atlas:temisConnector connector="..."/> extension that
// routes a business rule task to a server-registered temis connector for central
// evaluation (ADR-0050).
type xmlTemisConnector struct {
	Connector string `xml:"connector,attr"`
}

type xmlCalledDecision struct {
	DecisionId     string `xml:"decisionId,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
	BindingType    string `xml:"bindingType,attr"`
}

// decisionBinding maps a zeebe:calledDecision bindingType to a compiled binding
// (ADR-0063). "deployment" pins to the process's own snapshot; anything else —
// including the empty default and the not-yet-supported "versionTag" — resolves to
// the latest deployed version, Camunda's default.
func decisionBinding(bindingType string) DecisionBinding {
	if bindingType == "deployment" {
		return BindingDeployment
	}
	return BindingLatest
}

type xmlDecisionInput struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// xmlZeebeIOMapInput is a Zeebe io-mapping input: a FEEL source expression bound
// to a target name. For a business rule task the target is the DMN decision input
// name the source's value feeds; for the generic activity ioMapping (ADR-0068) it
// is the activity-local variable the source's value is written to.
type xmlZeebeIOMapInput struct {
	Source string `xml:"source,attr"`
	Target string `xml:"target,attr"`
}

// xmlZeebeIOMapOutput is a Zeebe io-mapping output: a FEEL source expression
// (evaluated over the activity-local scope) promoted into the parent scope under
// target (ADR-0068). It has the same shape as an input; the two are kept distinct
// so the direction reads clearly in the task structs and helpers.
type xmlZeebeIOMapOutput struct {
	Source string `xml:"source,attr"`
	Target string `xml:"target,attr"`
}

// xmlZeebeIOMapping is a generic <zeebe:ioMapping> on an activity: input mappings
// applied on activation and output mappings applied on completion (ADR-0068). It is
// distinct from a business rule task's ioMapping inputs, which have decision-input
// semantics; here both directions are plain variable mappings.
type xmlZeebeIOMapping struct {
	Inputs  []xmlZeebeIOMapInput  `xml:"input"`
	Outputs []xmlZeebeIOMapOutput `xml:"output"`
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
