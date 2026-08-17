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

// defaultUserTaskPriority mirrors Camunda's default task priority (ADR-0091): a
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
	return compileProcess(key, version, defs.Processes[0], buildMessageResolver(defs), buildSignalResolver(defs), buildErrorResolver(defs), buildEscalationResolver(defs), buildOperationResolver(defs))
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
	resolveSig := buildSignalResolver(defs)
	resolveErr := buildErrorResolver(defs)
	resolveEsc := buildEscalationResolver(defs)
	resolveOp := buildOperationResolver(defs)
	poolName := participantNames(defs)

	var out []Deployable
	for _, proc := range defs.Processes {
		if len(proc.StartEvents) == 0 {
			continue // black-box pool: nothing to run
		}
		cp, err := compileProcess(baseKey+uint64(len(out)), version, proc, resolve, resolveSig, resolveErr, resolveEsc, resolveOp)
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
			return compileProcess(key, version, proc, buildMessageResolver(defs), buildSignalResolver(defs), buildErrorResolver(defs), buildEscalationResolver(defs), buildOperationResolver(defs))
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

// buildOperationResolver indexes a model's <interface> operations by id and returns a
// resolver from a send task's operationRef to the id of the operation's <inMessageRef>
// message (ADR-0112) — the message that send publishes. An unknown operation, or one with no
// inMessageRef, is a deploy error. The operation's outMessageRef (a response) is ignored: an
// operationRef send is a fire-and-forget throw, exactly like a messageRef send.
func buildOperationResolver(defs xmlDefinitions) func(ownerId, operationRef string) (string, error) {
	ops := make(map[string]string, len(defs.Interfaces))
	for _, iface := range defs.Interfaces {
		for _, op := range iface.Operations {
			if op.Id != "" {
				ops[op.Id] = strings.TrimSpace(op.InMessageRef)
			}
		}
	}
	return func(ownerId, operationRef string) (string, error) {
		msg, ok := ops[operationRef]
		if !ok {
			return "", fmt.Errorf("compiler: send task %q references unknown operation %q", ownerId, operationRef)
		}
		if msg == "" {
			return "", fmt.Errorf("compiler: operation %q referenced by send task %q has no inMessageRef", operationRef, ownerId)
		}
		return msg, nil
	}
}

// buildSignalResolver indexes a model's top-level <bpmn:signal> declarations by id and
// returns a closure resolving a signalRef to the signal's name (ADR-0088). A signal is
// broadcast by name, so — unlike a message — there is no correlation key to compile.
func buildSignalResolver(defs xmlDefinitions) func(ownerId, signalRef string) (string, error) {
	signals := make(map[string]xmlSignal, len(defs.Signals))
	for _, s := range defs.Signals {
		if s.Id != "" {
			signals[s.Id] = s
		}
	}
	return func(ownerId, signalRef string) (string, error) {
		s, ok := signals[signalRef]
		if !ok {
			return "", fmt.Errorf("compiler: signal event %q references unknown signal %q", ownerId, signalRef)
		}
		if s.Name == "" {
			return "", fmt.Errorf("compiler: signal %q referenced by %q has no name", signalRef, ownerId)
		}
		return s.Name, nil
	}
}

// buildErrorResolver indexes a model's top-level <bpmn:error> declarations by id and
// returns a closure resolving an errorRef to the error's code (ADR-0089). Errors match by
// code, not id or name, so the resolver returns the code. A code-less error — or an
// errorEventDefinition with no errorRef at all — resolves to "": a catch-all on a
// boundary/handler, and an uncoded throw on an error end event. Unlike a message or
// signal, an empty code is therefore legal; only a non-empty errorRef that names no
// declared error is a deploy error.
func buildErrorResolver(defs xmlDefinitions) func(ownerId, errorRef string) (string, error) {
	errs := make(map[string]xmlError, len(defs.Errors))
	for _, e := range defs.Errors {
		if e.Id != "" {
			errs[e.Id] = e
		}
	}
	return func(ownerId, errorRef string) (string, error) {
		if errorRef == "" {
			return "", nil // a code-less catch-all, or an uncoded error throw
		}
		e, ok := errs[errorRef]
		if !ok {
			return "", fmt.Errorf("compiler: error event %q references unknown error %q", ownerId, errorRef)
		}
		return e.ErrorCode, nil
	}
}

// buildEscalationResolver indexes a model's top-level <bpmn:escalation> declarations by id
// and returns a closure resolving an escalationRef to the escalation's code (ADR-0125).
// Escalations match by code, mirroring errors (buildErrorResolver): a code-less escalation —
// or an escalationEventDefinition with no escalationRef at all — resolves to "": a catch-all
// on a boundary/handler, and an uncoded throw on an escalation throw/end event. An empty
// code is legal; only a non-empty escalationRef that names no declared escalation is a
// deploy error.
func buildEscalationResolver(defs xmlDefinitions) func(ownerId, escalationRef string) (string, error) {
	escs := make(map[string]xmlEscalation, len(defs.Escalations))
	for _, e := range defs.Escalations {
		if e.Id != "" {
			escs[e.Id] = e
		}
	}
	return func(ownerId, escalationRef string) (string, error) {
		if escalationRef == "" {
			return "", nil // a code-less catch-all, or an uncoded escalation throw
		}
		e, ok := escs[escalationRef]
		if !ok {
			return "", fmt.Errorf("compiler: escalation event %q references unknown escalation %q", ownerId, escalationRef)
		}
		return e.EscalationCode, nil
	}
}

// compileProcess linearizes one <process> into an immutable CompiledProcess,
// resolving message, signal, and error references through resolveMessage/resolveSignal/
// resolveError (shared across a collaboration's processes).
func compileProcess(key uint64, version int32, proc xmlProcess, resolveMessage func(ownerId, messageRef string) (string, *expr.Compiled, error), resolveSignal func(ownerId, signalRef string) (string, error), resolveError func(ownerId, errorRef string) (string, error), resolveEscalation func(ownerId, escalationRef string) (string, error), resolveOperation func(ownerId, operationRef string) (string, error)) (*CompiledProcess, error) {
	b := NewBuilder(key, proc.Id, version)
	// isExecutable defaults to true when the attribute is absent (BPMN convention;
	// Atlas has always run every deployed process), so an existing model without it
	// keeps working. Only an explicit "false" marks a process non-executable.
	b.SetExecutable(proc.IsExecutable != "false")
	if proc.VersionTag != "" {
		b.SetVersionTag(proc.VersionTag)
	}
	// A per-definition instance TTL (ADR-0085): parse the ISO-8601 duration up front so
	// a malformed value fails the deploy rather than silently disabling the bound.
	if ttl := strings.TrimSpace(proc.InstanceTtl); ttl != "" {
		nanos, err := parseISO8601Duration(ttl)
		if err != nil {
			return nil, fmt.Errorf("compiler: process %q: invalid instanceTtl %q: %w", proc.Id, ttl, err)
		}
		if nanos <= 0 {
			return nil, fmt.Errorf("compiler: process %q: instanceTtl %q must be a positive duration", proc.Id, ttl)
		}
		b.SetInstanceTtl(nanos)
	}
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

	// Fold <transaction> subprocesses into SubProcesses (marked IsTransaction) before any
	// scope walk, so a transaction is registered, wired, and validated as the subprocess it
	// structurally is (ADR-0108).
	foldTransactions(&proc.xmlFlowContent)
	// Resolve each send task's operationRef to the message the operation sends, before
	// registerScope, so an operationRef send is compiled as the message kind (ADR-0112).
	if err := resolveSendTaskOperations(&proc.xmlFlowContent, resolveOperation); err != nil {
		return nil, err
	}

	// Register every flow node — the process root and, recursively, each embedded
	// subprocess scope — then require a root-scope start event before wiring flows.
	// Data objects and I/O mappings below stay process-scoped (ADR-0074).
	if err := registerScope(b, ids, register, resolveMessage, resolveSignal, resolveError, resolveEscalation, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	if err := connectScope(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Resolve compensation links now that every node is registered (ADR-0103): join each
	// compensation boundary to its handler via a BPMN <association>, and narrow each
	// compensation throw/end that names a single activity. Both endpoints resolve through
	// the flat, process-wide id map, so this is a post-pass over the whole scope tree.
	if err := resolveCompensation(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Record each flow node's organizational lane (ADR-0121). Lanes are metadata with no
	// execution effect, resolved through the same process-wide id map as compensation.
	if err := resolveLanes(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
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
	for _, rt := range proc.ReceiveTasks {
		if err := wireDataOut(rt.Id, rt.DataOut); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.SendTasks {
		if strings.TrimSpace(st.MessageRef) != "" {
			continue // a message-kind send task is a throw, not an activity (ADR-0112)
		}
		if err := wireDataOut(st.Id, st.DataOut); err != nil {
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
	for _, rt := range proc.ReceiveTasks {
		if err := wireDataIn(rt.Id, rt.DataIn); err != nil {
			return nil, err
		}
	}
	for _, st := range proc.SendTasks {
		if strings.TrimSpace(st.MessageRef) != "" {
			continue // a message-kind send task is a throw, not an activity (ADR-0112)
		}
		if err := wireDataIn(st.Id, st.DataIn); err != nil {
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
	// Wire I/O mappings for every scope, recursively: a subprocess's own ioMapping
	// (input mappings write its scope on entry, output mappings promote to the
	// parent on completion — the engine applies both generically) and the mappings
	// on the activities inside it (ADR-0074 Phase 4).
	var wireScopeIO func(c *xmlFlowContent) error
	wireScopeIO = func(c *xmlFlowContent) error {
		for _, st := range c.ServiceTasks {
			if err := wireIO(st.Id, st.IOMapping); err != nil {
				return err
			}
		}
		for _, st := range c.ScriptTasks {
			if err := wireIO(st.Id, st.IOMapping); err != nil {
				return err
			}
		}
		for _, ut := range c.UserTasks {
			if err := wireIO(ut.Id, ut.IOMapping); err != nil {
				return err
			}
		}
		for _, ca := range c.CallActivities {
			if err := wireIO(ca.Id, ca.IOMapping); err != nil {
				return err
			}
		}
		for _, rt := range c.ReceiveTasks {
			if err := wireIO(rt.Id, rt.IOMapping); err != nil {
				return err
			}
		}
		for _, st := range c.SendTasks {
			if strings.TrimSpace(st.MessageRef) != "" {
				continue // a message-kind send task is a throw, not an activity (ADR-0112)
			}
			if err := wireIO(st.Id, st.IOMapping); err != nil {
				return err
			}
		}
		for i := range c.SubProcesses {
			sub := &c.SubProcesses[i]
			if err := wireIO(sub.Id, sub.IOMapping); err != nil {
				return err
			}
			if err := wireScopeIO(&sub.xmlFlowContent); err != nil {
				return err
			}
		}
		return nil
	}
	if err := wireScopeIO(&proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Wire the loop characteristics of every activity in the scope tree, recursively,
	// mirroring wireScopeIO — both BPMN markers: multi-instance (ADR-0077) and standard
	// loop (ADR-0133). Each FEEL source compiles once at deploy (I5), and a loop with no
	// way to decide how many iterations to run is refused (a multi-instance with neither
	// an input collection nor a cardinality; a standard loop with neither a condition nor
	// a maximum). The compiler records the detail; the engine runs the iterations.
	compileMI := func(ownerId, what, source string) (*expr.Compiled, error) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "="))
		if text == "" {
			return nil, nil
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: loop %s on %q: %w", what, ownerId, err)
		}
		return e, nil
	}
	// A standard loop is the other BPMN loop marker (ADR-0133): it repeats the activity
	// while its condition holds rather than once per collection element. It compiles into
	// the same loop table (the engine runs it on the multi-instance body/iteration
	// machinery), so an activity carries at most one of the two markers, and a loop with
	// neither a condition nor a maximum is refused — it could never end.
	wireStandardLoop := func(ownerId string, sl *xmlStandardLoop) error {
		if sl == nil {
			return nil
		}
		cond, err := compileMI(ownerId, "loopCondition", sl.LoopCondition)
		if err != nil {
			return err
		}
		var max int32
		if text := strings.TrimSpace(sl.LoopMaximum); text != "" {
			n, err := strconv.Atoi(text)
			if err != nil || n <= 0 {
				return fmt.Errorf("compiler: loop activity %q has an invalid loopMaximum %q (want a positive whole number)", ownerId, sl.LoopMaximum)
			}
			max = int32(n)
		}
		if cond == nil && max == 0 {
			return fmt.Errorf("compiler: loop activity %q needs a loopCondition or a loopMaximum", ownerId)
		}
		b.SetStandardLoop(ids[ownerId], strings.TrimSpace(sl.TestBefore) == "true", max, cond)
		return nil
	}
	wireMultiInstance := func(ownerId string, mi *xmlMultiInstance) error {
		if mi == nil {
			return nil
		}
		coll, err := compileMI(ownerId, "inputCollection", mi.Loop.InputCollection)
		if err != nil {
			return err
		}
		card, err := compileMI(ownerId, "loopCardinality", mi.LoopCardinality)
		if err != nil {
			return err
		}
		if coll == nil && card == nil {
			return fmt.Errorf("compiler: multi-instance activity %q needs an inputCollection or a loopCardinality", ownerId)
		}
		if coll != nil && card != nil {
			return fmt.Errorf("compiler: multi-instance activity %q has both an inputCollection and a loopCardinality (use one)", ownerId)
		}
		out, err := compileMI(ownerId, "outputElement", mi.Loop.OutputElement)
		if err != nil {
			return err
		}
		cond, err := compileMI(ownerId, "completionCondition", mi.CompletionCondition)
		if err != nil {
			return err
		}
		b.SetMultiInstance(ids[ownerId], mi.IsSequential == "true",
			strings.TrimSpace(mi.Loop.InputElement), strings.TrimSpace(mi.Loop.OutputCollection),
			coll, card, out, cond)
		return nil
	}
	// An activity carries at most one loop marker: BPMN draws them as different icons
	// (∥/≡ for a multi-instance, ↻ for a standard loop) and they mean different things,
	// so a model with both is refused rather than silently running one of them.
	wireLoop := func(ownerId string, mi *xmlMultiInstance, sl *xmlStandardLoop) error {
		if mi != nil && sl != nil {
			return fmt.Errorf("compiler: activity %q has both a multiInstanceLoopCharacteristics and a standardLoopCharacteristics (use one)", ownerId)
		}
		if err := wireMultiInstance(ownerId, mi); err != nil {
			return err
		}
		return wireStandardLoop(ownerId, sl)
	}
	var wireScopeMI func(c *xmlFlowContent) error
	wireScopeMI = func(c *xmlFlowContent) error {
		for _, st := range c.ServiceTasks {
			if err := wireLoop(st.Id, st.MultiInstance, st.StandardLoop); err != nil {
				return err
			}
		}
		for _, st := range c.ScriptTasks {
			if err := wireLoop(st.Id, st.MultiInstance, st.StandardLoop); err != nil {
				return err
			}
		}
		for _, ut := range c.UserTasks {
			if err := wireLoop(ut.Id, ut.MultiInstance, ut.StandardLoop); err != nil {
				return err
			}
		}
		for _, ca := range c.CallActivities {
			if err := wireLoop(ca.Id, ca.MultiInstance, ca.StandardLoop); err != nil {
				return err
			}
		}
		for _, rt := range c.ReceiveTasks {
			if err := wireLoop(rt.Id, rt.MultiInstance, rt.StandardLoop); err != nil {
				return err
			}
		}
		for _, brt := range c.BusinessRuleTasks {
			if err := wireLoop(brt.Id, brt.MultiInstance, brt.StandardLoop); err != nil {
				return err
			}
		}
		// An undefined task and a manual task have no implementation, but they are
		// activities: a loop marker on one repeats the (pass-through) step, which is what
		// the diagram says it does.
		for _, t := range c.Tasks {
			if err := wireLoop(t.Id, t.MultiInstance, t.StandardLoop); err != nil {
				return err
			}
		}
		for _, t := range c.ManualTasks {
			if err := wireLoop(t.Id, t.MultiInstance, t.StandardLoop); err != nil {
				return err
			}
		}
		for _, st := range c.SendTasks {
			if strings.TrimSpace(st.MessageRef) != "" {
				continue // a message-kind send task is a throw, not an activity (ADR-0112)
			}
			if err := wireLoop(st.Id, st.MultiInstance, st.StandardLoop); err != nil {
				return err
			}
		}
		for i := range c.SubProcesses {
			sub := &c.SubProcesses[i]
			if err := wireLoop(sub.Id, sub.MultiInstance, sub.StandardLoop); err != nil {
				return err
			}
			if err := wireScopeMI(&sub.xmlFlowContent); err != nil {
				return err
			}
		}
		return nil
	}
	if err := wireScopeMI(&proc.xmlFlowContent); err != nil {
		return nil, err
	}

	cp, err := b.Build()
	if err != nil {
		return nil, err
	}
	// Stage 5 (compiler.md): graph-wide validation. An error-severity Problem
	// refuses the deploy, preserving the compile-gate contract (invariant I5) — the
	// same fatal-at-deploy behavior structural parse errors already have, now for
	// graph-level faults (an unroutable gateway, a boundary event on a non-activity).
	// Warnings (e.g. unreachable dead code) are non-fatal and surface through the
	// future /validate dry-run (ADR-0026), not here.
	if problems := Validate(cp); HasErrors(problems) {
		return nil, &ValidationError{Problems: problems}
	}
	return cp, nil
}

// BPMN XML is matched by element/attribute local name, so namespace prefixes
// (bpmn:, zeebe:) are handled transparently by encoding/xml.

type xmlDefinitions struct {
	Processes     []xmlProcess      `xml:"process"`
	Messages      []xmlMessage      `xml:"message"`
	Signals       []xmlSignal       `xml:"signal"`
	Errors        []xmlError        `xml:"error"`
	Escalations   []xmlEscalation   `xml:"escalation"`
	Interfaces    []xmlInterface    `xml:"interface"`
	Collaboration *xmlCollaboration `xml:"collaboration"`
}

// A BPMN <interface> groups <operation>s (the WSDL-style service-interface model). Atlas
// reads them only to resolve a send task's operationRef to the operation's inMessageRef
// (ADR-0112) — the message that send publishes.
type xmlInterface struct {
	Operations []xmlOperation `xml:"operation"`
}

// A BPMN <operation> inside an <interface>. Its <inMessageRef> names the message an
// operationRef send task publishes; its outMessageRef (a response) is not supported.
type xmlOperation struct {
	Id           string `xml:"id,attr"`
	InMessageRef string `xml:"inMessageRef"`
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

// A top-level signal declaration (ADR-0088). A signal is broadcast by name — it carries
// no correlation key and no code — so it needs only an id and a name.
type xmlSignal struct {
	Id   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type xmlSignalEventDefinition struct {
	SignalRef string `xml:"signalRef,attr"`
}

// A top-level error declaration (ADR-0089). An error is caught by its code — the
// nearest enclosing handler whose error code matches, or a code-less catch-all — so the
// errorCode, not the id or name, is what an error boundary/handler compares against. The
// id is what an errorRef points at; the name is human-facing only.
type xmlError struct {
	Id        string `xml:"id,attr"`
	ErrorCode string `xml:"errorCode,attr"`
	Name      string `xml:"name,attr"`
}

type xmlErrorEventDefinition struct {
	ErrorRef string `xml:"errorRef,attr"`
}

// A top-level escalation declaration (ADR-0125). Like an error, an escalation is caught by
// its code — the nearest enclosing escalation handler whose escalationCode matches, or a
// code-less catch-all — so the escalationCode, not the id or name, is what an escalation
// boundary/handler compares against. The id is what an escalationRef points at; the name is
// human-facing only.
type xmlEscalation struct {
	Id             string `xml:"id,attr"`
	EscalationCode string `xml:"escalationCode,attr"`
	Name           string `xml:"name,attr"`
}

type xmlEscalationEventDefinition struct {
	EscalationRef string `xml:"escalationRef,attr"`
}

type xmlProcess struct {
	Id           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	IsExecutable string `xml:"isExecutable,attr"`
	VersionTag   string `xml:"versionTag,attr"`
	InstanceTtl  string `xml:"instanceTtl,attr"` // ISO-8601 duration; self-cleaning TTL (ADR-0085), empty = off

	xmlFlowContent // the process root's flow nodes and sequence flows

	DataObjects          []xmlDataObject          `xml:"dataObject"`
	DataObjectReferences []xmlDataObjectReference `xml:"dataObjectReference"`
}

// xmlFlowContent is the set of flow nodes and sequence flows of one scope — the
// process root or an embedded subprocess. Both xmlProcess and xmlSubProcess embed
// it, so a subprocess nests the very same element shapes (ADR-0074). encoding/xml
// promotes the embedded fields, so a <subProcess>'s own <startEvent>, <task>,
// <sequenceFlow>, … unmarshal exactly as they do at the process root.
type xmlFlowContent struct {
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

	Tasks              []xmlPlainTask        `xml:"task"`
	ManualTasks        []xmlPlainTask        `xml:"manualTask"`
	ParallelGateways   []xmlNode             `xml:"parallelGateway"`
	InclusiveGateways  []xmlInclusiveGateway `xml:"inclusiveGateway"`
	EventBasedGateways []xmlNode             `xml:"eventBasedGateway"` // deferred choice; only its id matters (ADR-0110)

	UserTasks []xmlUserTask `xml:"userTask"`

	SubProcesses   []xmlSubProcess   `xml:"subProcess"`
	CallActivities []xmlCallActivity `xml:"callActivity"`

	// Transactions are <transaction> subprocesses — structurally an embedded subprocess with
	// one added outcome, cancellation (ADR-0108). They share xmlSubProcess's shape; foldTransactions
	// merges them into SubProcesses (marked IsTransaction) right after parse, so every scope walk
	// that already handles subprocesses handles transactions unchanged.
	Transactions []xmlSubProcess `xml:"transaction"`

	ReceiveTasks []xmlReceiveTask `xml:"receiveTask"`

	// A send task is a service task under a different BPMN label (ADR-0112): it creates a
	// job and waits, carrying the same taskDefinition, connector extensions, and activity
	// sub-elements. It parses into the very same shape, so xmlSendTask is an alias.
	SendTasks []xmlSendTask `xml:"sendTask"`

	// Captured only to give a clear "unsupported element" error (see registerScope); not
	// executable yet.
	AdHocSubProcesses []xmlNode `xml:"adHocSubProcess"`

	// Associations are BPMN <association> artifacts declared in this scope. Atlas reads
	// them only to link a compensation boundary event to its handler (ADR-0103).
	Associations []xmlAssociation `xml:"association"`

	// LaneSets partition this scope's flow nodes into organizational lanes (ADR-0121).
	// Lanes are metadata with no execution effect; resolveLanes records each node's lane.
	LaneSets []xmlLaneSet `xml:"laneSet"`
}

// xmlLaneSet is a <laneSet> — a set of sibling lanes partitioning a process or subprocess scope.
type xmlLaneSet struct {
	Lanes []xmlLane `xml:"lane"`
}

// xmlLane is a <lane> — an organizational partition naming the flow nodes it contains via
// <flowNodeRef> children, optionally subdivided into a nested <childLaneSet> (ADR-0121).
type xmlLane struct {
	Id           string      `xml:"id,attr"`
	Name         string      `xml:"name,attr"`
	FlowNodeRefs []string    `xml:"flowNodeRef"`
	ChildLaneSet *xmlLaneSet `xml:"childLaneSet"`
}

// laneLabel is a lane's name, or its id when unnamed — for error messages.
func laneLabel(l *xmlLane) string {
	if l.Name != "" {
		return l.Name
	}
	return l.Id
}

// resolveLanes records each flow node's organizational lane (ADR-0121). It walks every scope's
// laneSets — following nested childLaneSets and building the lane table with parent pointers — and
// resolves each <flowNodeRef> to a node through the process-wide id map, so a lane's assignment
// works regardless of scope. A flowNodeRef naming no registered node, or a node claimed by two
// lanes, is a deploy error. Lanes are pure metadata, so this runs after registration and changes
// no flow; a process with no lanes leaves every node's Lane at -1.
func resolveLanes(b *Builder, ids map[string]int32, fc *xmlFlowContent) error {
	assigned := map[int32]string{} // node id → the lane that already claimed it (process-wide)

	var walkLaneSet func(ls *xmlLaneSet, parent int32) error
	walkLaneSet = func(ls *xmlLaneSet, parent int32) error {
		for i := range ls.Lanes {
			lane := &ls.Lanes[i]
			idx := b.AddLane(lane.Name, parent)
			for _, ref := range lane.FlowNodeRefs {
				ref = strings.TrimSpace(ref)
				if ref == "" {
					continue
				}
				node, ok := ids[ref]
				if !ok {
					return fmt.Errorf("compiler: lane %q references unknown flow node %q", laneLabel(lane), ref)
				}
				if prev, dup := assigned[node]; dup {
					return fmt.Errorf("compiler: flow node %q is in two lanes (%q and %q); a node belongs to at most one lane", ref, prev, laneLabel(lane))
				}
				assigned[node] = laneLabel(lane)
				b.SetLane(node, idx)
			}
			if lane.ChildLaneSet != nil {
				if err := walkLaneSet(lane.ChildLaneSet, idx); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var walkScope func(c *xmlFlowContent) error
	walkScope = func(c *xmlFlowContent) error {
		for i := range c.LaneSets {
			if err := walkLaneSet(&c.LaneSets[i], -1); err != nil {
				return err
			}
		}
		for i := range c.SubProcesses {
			if err := walkScope(&c.SubProcesses[i].xmlFlowContent); err != nil {
				return err
			}
		}
		return nil
	}
	return walkScope(fc)
}

// xmlReceiveTask is a <receiveTask messageRef="…">: an activity that waits for the
// referenced message to correlate, then continues (ADR-0102). It carries the same activity
// sub-elements as a service task — I/O mappings, multi-instance, and data associations — so
// it is a first-class activity.
type xmlReceiveTask struct {
	Id            string                     `xml:"id,attr"`
	MessageRef    string                     `xml:"messageRef,attr"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlCallActivity is a <callActivity>: it starts a separate process (named by
// <zeebe:calledElement processId=…>) as a child instance and passes variables via
// its <zeebe:ioMapping> and the propagation flags (ADR-0076).
type xmlCallActivity struct {
	Id            string            `xml:"id,attr"`
	Name          string            `xml:"name,attr"`
	CalledElement xmlZeebeCalledEl  `xml:"extensionElements>calledElement"`
	IOMapping     xmlZeebeIOMapping `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop  `xml:"standardLoopCharacteristics"`
}

// xmlZeebeCalledEl is the <zeebe:calledElement> of a call activity: the called
// process id, its version binding, and the two variable-propagation flags (each
// defaults to true when the attribute is absent, matching Zeebe).
type xmlZeebeCalledEl struct {
	ProcessId                   string `xml:"processId,attr"`
	BindingType                 string `xml:"bindingType,attr"`
	PropagateAllParentVariables string `xml:"propagateAllParentVariables,attr"`
	PropagateAllChildVariables  string `xml:"propagateAllChildVariables,attr"`
}

// xmlMultiInstance is a <multiInstanceLoopCharacteristics> on an activity (ADR-0077):
// the sequential/parallel flag, an optional <loopCardinality> and <completionCondition>,
// and its <zeebe:loopCharacteristics>. Zeebe nests the loop characteristics inside the
// marker's own <extensionElements>, so the path is
// multiInstanceLoopCharacteristics > extensionElements > loopCharacteristics.
type xmlMultiInstance struct {
	IsSequential        string            `xml:"isSequential,attr"`
	LoopCardinality     string            `xml:"loopCardinality"`
	CompletionCondition string            `xml:"completionCondition"`
	Loop                xmlZeebeLoopChars `xml:"extensionElements>loopCharacteristics"`
}

// xmlStandardLoop is a <standardLoopCharacteristics> on an activity (ADR-0133): the
// other BPMN loop marker — the circular-arrow icon — which repeats the activity while
// its <loopCondition> holds. TestBefore checks the condition before the first
// iteration (a while loop; absent means a repeat-until that always runs once) and
// LoopMaximum caps the iteration count. Both are plain BPMN, no Zeebe extension.
type xmlStandardLoop struct {
	TestBefore    string `xml:"testBefore,attr"`
	LoopMaximum   string `xml:"loopMaximum,attr"`
	LoopCondition string `xml:"loopCondition"`
}

// xmlZeebeLoopChars is the <zeebe:loopCharacteristics> of a multi-instance activity:
// the FEEL input collection, the per-iteration input element, and the output
// collection/element that assemble each iteration's result into a list (ADR-0077).
type xmlZeebeLoopChars struct {
	InputCollection  string `xml:"inputCollection,attr"`
	InputElement     string `xml:"inputElement,attr"`
	OutputCollection string `xml:"outputCollection,attr"`
	OutputElement    string `xml:"outputElement,attr"`
}

// xmlSubProcess is an embedded <subProcess>: a container whose own flow nodes and
// sequence flows compile into the flat node array, linked back to it only by their
// FlowScope (ADR-0074). It recurses — a subprocess may contain subprocesses.
type xmlSubProcess struct {
	Id        string            `xml:"id,attr"`
	Name      string            `xml:"name,attr"`
	IOMapping xmlZeebeIOMapping `xml:"extensionElements>ioMapping"`
	// TriggeredByEvent marks an event subprocess (ADR-0082): it is not entered by a
	// sequence flow but armed by its start event's event definition while the parent
	// scope runs. "true" makes it an event subprocess; empty/absent is an ordinary one.
	TriggeredByEvent string            `xml:"triggeredByEvent,attr"`
	MultiInstance    *xmlMultiInstance `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop     *xmlStandardLoop  `xml:"standardLoopCharacteristics"`
	// IsTransaction marks a subprocess that was parsed from a <transaction> element (never from
	// XML — set by foldTransactions). The compiler marks its compiled node so the runtime and
	// validation know it may host a cancel boundary and hold a cancel end event (ADR-0108).
	IsTransaction bool `xml:"-"`
	xmlFlowContent
}

// foldTransactions merges each scope's <transaction> subprocesses into its SubProcesses
// slice, marked IsTransaction, recursively down the scope tree. A transaction is
// structurally an embedded subprocess with cancellation added (ADR-0108); folding it into
// SubProcesses means every existing walk (registration, flow wiring, compensation
// resolution, I/O and multi-instance) treats it as a subprocess with no special-casing, and
// only the two genuinely new sites — marking the compiled node, and dispatching a cancel
// end/boundary — need to look at IsTransaction / the cancel event definition.
func foldTransactions(fc *xmlFlowContent) {
	for i := range fc.Transactions {
		fc.Transactions[i].IsTransaction = true
	}
	fc.SubProcesses = append(fc.SubProcesses, fc.Transactions...)
	fc.Transactions = nil
	for i := range fc.SubProcesses {
		foldTransactions(&fc.SubProcesses[i].xmlFlowContent)
	}
}

// resolveSendTaskOperations rewrites each send task's operationRef to the message that
// operation sends (its inMessageRef), so the rest of the compiler treats an operationRef send
// exactly as a messageRef send — the message kind (ADR-0112). It walks every scope, like
// foldTransactions, and runs right after it (so send tasks inside a folded transaction are
// covered) and before registerScope. A send task carrying both a messageRef and an operationRef
// is a conflict (a deploy error).
func resolveSendTaskOperations(fc *xmlFlowContent, resolveOperation func(ownerId, operationRef string) (string, error)) error {
	for i := range fc.SendTasks {
		st := &fc.SendTasks[i]
		op := strings.TrimSpace(st.OperationRef)
		if op == "" {
			continue
		}
		if strings.TrimSpace(st.MessageRef) != "" {
			return fmt.Errorf("compiler: send task %q sets both messageRef and operationRef; use one", st.Id)
		}
		msg, err := resolveOperation(st.Id, op)
		if err != nil {
			return err
		}
		st.MessageRef = msg
	}
	for i := range fc.SubProcesses {
		if err := resolveSendTaskOperations(&fc.SubProcesses[i].xmlFlowContent, resolveOperation); err != nil {
			return err
		}
	}
	return nil
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
	// SingletonStart ("true") marks a message start event as one-per-correlation-key
	// (ADR-0094): while an instance started with a key is live, a further correlating
	// message starts no duplicate. A plain attribute (like versionTag on a process);
	// absent = the default start-per-message behavior (ADR-0035).
	SingletonStart string `xml:"singletonStart,attr"`
	// Timer, when present, makes this a timer start event: the process starts a
	// fresh instance on the schedule (duration/date/cycle/cron) the definition
	// carries, armed at deploy time (ADR-0051). A pointer so an absent one is nil.
	Timer *xmlTimerEventDefinition `xml:"timerEventDefinition"`
	// Signal, when present, makes this a signal start event: a broadcast signal of the
	// referenced name instantiates the process (ADR-0088). A pointer so an absent one is nil.
	Signal *xmlSignalEventDefinition `xml:"signalEventDefinition"`
	// Error, when present on an event-subprocess start event, makes it an error-triggered
	// event subprocess: it catches an error propagating in its scope whose code matches
	// (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present on an event-subprocess start event, makes it an
	// escalation-triggered event subprocess: it catches an escalation propagating in its
	// scope whose code matches (ADR-0125). May be interrupting or non-interrupting per
	// IsInterrupting. A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// IsInterrupting is the event-subprocess start event's cancel flag (ADR-0082):
	// absent or "true" interrupts the parent scope when the trigger fires, "false" runs
	// the handler alongside it. Empty for an ordinary start event.
	IsInterrupting string `xml:"isInterrupting,attr"`
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

// An intermediate catch event; the timer, message, signal, and link variants are executable.
// Each definition is a pointer so an absent one is detected as nil.
type xmlIntermediateCatchEvent struct {
	Id      string                     `xml:"id,attr"`
	Timer   *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Link, when present, makes this a link catch event: the landing point of a link throw
	// with the same name in the same scope — an off-page connector / goto (ADR-0133). A
	// pointer so an absent one is nil.
	Link *xmlLinkEventDefinition `xml:"linkEventDefinition"`
}

// An intermediate throw event; the message, signal, compensation, and escalation variants
// are executable.
type xmlIntermediateThrowEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Compensation, when present, makes this a compensation throw event: it triggers
	// compensation of completed compensable activities in its scope (or of the one named
	// by activityRef), then flows on (ADR-0103). A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Escalation, when present, makes this an escalation throw event: it raises the
	// referenced escalation, propagating up to the nearest matching handler, then continues
	// on its outgoing flow (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Link, when present, makes this a link throw event: a goto to the link catch of the same
	// name in the same scope — an off-page connector (ADR-0133). A pointer so an absent one is nil.
	Link *xmlLinkEventDefinition `xml:"linkEventDefinition"`
}

// xmlLinkEventDefinition is a <linkEventDefinition name="…"> on an intermediate throw or
// catch event (ADR-0133). A link is matched by Name within one flow scope: a throw jumps to
// the catch of the same name. Only the name matters — a link carries no ref, code, or payload.
type xmlLinkEventDefinition struct {
	Name string `xml:"name,attr"`
}

// xmlCompensateEventDefinition is a <compensateEventDefinition> on a throw, end, or
// boundary event. On a throw/end event, ActivityRef optionally names the single activity
// to compensate — empty compensates every completed compensable activity in the scope
// (ADR-0103). On a boundary event it has no attributes (the boundary just marks its host
// compensable and links to a handler via a BPMN <association>). waitForCompletion is
// accepted but not yet honored (compensation is synchronous).
type xmlCompensateEventDefinition struct {
	ActivityRef       string `xml:"activityRef,attr"`
	WaitForCompletion string `xml:"waitForCompletion,attr"`
}

// xmlAssociation is a BPMN <association>: an undirected artifact link. Atlas parses it
// only to join a compensation boundary event to its compensation handler activity — one
// endpoint is the boundary, the other the handler (ADR-0103). Non-compensation
// associations are ignored.
type xmlAssociation struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}

// An end event. A plain (none) end event just ends the instance; one bearing a
// messageEventDefinition is a message end event, which publishes the message
// then ends (ADR-0052); a signalEventDefinition is a signal end event, which
// broadcasts the signal then ends (ADR-0088). Each is a pointer so an absent one is nil.
type xmlEndEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Error, when present, makes this an error end event: it throws the referenced error,
	// ending its enclosing scope abnormally and propagating up to the nearest matching
	// handler (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present, makes this an escalation end event: it raises the referenced
	// escalation, propagating up to the nearest matching handler, then ends its path
	// (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Terminate is present when the end event carries a <terminateEventDefinition>.
	// Atlas can't execute a terminate end yet, so it is rejected at compile time rather
	// than silently dropped to a plain end (which would abandon the terminate semantics).
	Terminate *xmlTerminateEventDefinition `xml:"terminateEventDefinition"`
	// Compensation, when present, makes this a compensation end event: it triggers
	// compensation, then ends its scope (ADR-0103); the trigger-and-stop counterpart of a
	// compensation throw. A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Cancel, when present, makes this a cancel end event: it cancels the enclosing
	// transaction — compensating its completed activities, then routing out the transaction's
	// cancel boundary (ADR-0108). Valid only inside a transaction. A pointer so an absent one is nil.
	Cancel *xmlCancelEventDefinition `xml:"cancelEventDefinition"`
}

// xmlTerminateEventDefinition is the empty <terminateEventDefinition> element; only its
// presence matters (a non-nil pointer once parsed).
type xmlTerminateEventDefinition struct{}

// xmlCancelEventDefinition is the empty <cancelEventDefinition> element; only its presence
// matters. On an end event it makes a cancel end event (cancels the enclosing transaction);
// on a boundary event it makes a cancel boundary (catches a transaction's cancellation),
// which may attach only to a transaction and is always interrupting (ADR-0108).
type xmlCancelEventDefinition struct{}

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
	Signal         *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Error, when present, makes this an error boundary event: it catches an error thrown
	// by the host activity (or propagated up to it) whose code matches, and is always
	// interrupting (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present, makes this an escalation boundary event: it catches an
	// escalation raised by the host activity (or propagated up to it) whose code matches.
	// Unlike an error boundary it honors CancelActivity — it may be interrupting or
	// non-interrupting (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Compensation, when present, makes this a compensation boundary event: it is inert
	// (never armed), marking its host activity compensable and linking — via a BPMN
	// <association> — to the compensation handler (ADR-0103). A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Cancel, when present, makes this a cancel boundary event: it catches its host
	// transaction's cancellation and routes the recovery flow. Valid only on a transaction,
	// and always interrupting (ADR-0108). A pointer so an absent one is nil.
	Cancel *xmlCancelEventDefinition `xml:"cancelEventDefinition"`
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

// xmlPlainTask is an undefined <task> or a <manualTask>: an activity with no
// execution semantics of its own (the engine runs it as a pass-through), but an
// activity all the same — so it carries the loop markers (ADR-0077, ADR-0133) as well
// as data associations. It is a separate shape from xmlNode precisely so those markers
// stay off the gateways that share xmlNode: a looping gateway is not a thing.
type xmlPlainTask struct {
	Id            string                     `xml:"id,attr"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
}

// A user task parks a token for human completion (ADR-0028). It optionally
// carries a zeebe:assignmentDefinition for assignee/candidateGroups.
type xmlUserTask struct {
	Id            string                     `xml:"id,attr"`
	Name          string                     `xml:"name,attr"`
	Assignment    xmlAssignmentDefinition    `xml:"extensionElements>assignmentDefinition"`
	Form          xmlFormDefinition          `xml:"extensionElements>formDefinition"`
	Priority      xmlPriorityDefinition      `xml:"extensionElements>priorityDefinition"`
	Schedule      xmlTaskSchedule            `xml:"extensionElements>taskSchedule"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlPriorityDefinition carries zeebe:priorityDefinition's static task priority
// (ADR-0091). An empty value means the task uses the default priority.
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
	Id string `xml:"id,attr"`
	// MessageRef is read only for a send task (ADR-0112): a <sendTask messageRef> is the
	// message-kind send — a correlating throw in task form. A service task never carries one
	// (it dispatches on its taskDefinition/connector), so the field is inert there.
	MessageRef string `xml:"messageRef,attr"`
	// OperationRef is read only for a send task (ADR-0112): a <sendTask operationRef> names a
	// <bpmn:operation> whose <inMessageRef> is the message to send. It is resolved to that
	// message before compilation (resolveSendTaskOperations), so it is an alternate spelling of
	// the message kind — a fire-and-forget throw. The operation's outMessageRef (a response) is
	// not supported. Inert on a service task.
	OperationRef   string            `xml:"operationRef,attr"`
	TaskDefinition xmlTaskDefinition `xml:"extensionElements>taskDefinition"`
	// Clio, when present, marks this service task a clio connector task (ADR-0036).
	// The pointer is nil when the <atlas:clioConnector> extension is absent.
	Clio *xmlClioConnector `xml:"extensionElements>clioConnector"`
	// Rest, when present, marks this service task an HTTP-REST connector task
	// (ADR-0067). The pointer is nil when the <atlas:restConnector> extension is
	// absent.
	Rest *xmlRestConnector `xml:"extensionElements>restConnector"`
	// Mail, when present, marks this service task an outbound mail connector task
	// (ADR-0079). The pointer is nil when the <atlas:mailConnector> extension is
	// absent.
	Mail *xmlMailConnector `xml:"extensionElements>mailConnector"`
	// User, when present, marks this service task a user-provisioning connector task
	// (ADR-0123). The pointer is nil when the <atlas:userConnector> extension is absent.
	User *xmlUserConnector `xml:"extensionElements>userConnector"`
	// Csv, when present, marks this service task a CSV-to-JSON connector task
	// (ADR-0090). The pointer is nil when the <atlas:csvConnector> extension is absent.
	Csv *xmlCsvConnector `xml:"extensionElements>csvConnector"`
	// SharePoint, when present, marks this service task a SharePoint connector task
	// (ADR-0105). The pointer is nil when the <atlas:sharepointConnector> extension is
	// absent.
	SharePoint *xmlSharePointConnector `xml:"extensionElements>sharepointConnector"`
	// Remedy, when present, marks this service task a BMC Remedy connector task
	// (ADR-0106). The pointer is nil when the <atlas:remedyConnector> extension is
	// absent.
	Remedy *xmlRemedyConnector `xml:"extensionElements>remedyConnector"`
	// WebScrape, when present, marks this service task a web-scraping connector task
	// (ADR-0118). The pointer is nil when the <atlas:webscrapeConnector> extension is
	// absent.
	WebScrape *xmlWebScrapeConnector `xml:"extensionElements>webscrapeConnector"`
	// Mockup, when present, marks this service task an engine-simulated mockup task
	// (ADR-0120). The pointer is nil when the <atlas:mockupConnector> extension is
	// absent.
	Mockup        *xmlMockupConnector        `xml:"extensionElements>mockupConnector"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlSendTask is a <sendTask>: a job-creating activity identical in shape and execution to
// a service task (ADR-0112) — same taskDefinition, connector extensions, I/O mappings,
// multi-instance, and data associations. It is a type alias so both parse and every
// per-activity wiring loop treat the two identically; only the compiled node type differs.
type xmlSendTask = xmlServiceTask

// A clio connector task's parameters, carried on a service task as an
// <atlas:clioConnector connector="..." operation="..." .../> extension element.
// connector names a server-registered connector (its endpoint and credentials live
// in the server config, never in the model). operation is "write" (default),
// "query", or "read", selecting which of the remaining attributes apply:
//   - write: subject and eventType — the clio coordinates the appended event
//     (the instance's variables) lands under.
//   - query: resultVariable receives the result; either query (a run_query string)
//     or subject (with the optional reduceSpec projection, a get_state read).
//   - read: subject's events (up to limit; 0 = the connector's default) are read
//     into resultVariable as a JSON array.
type xmlClioConnector struct {
	Connector      string `xml:"connector,attr"`
	Operation      string `xml:"operation,attr"`
	Subject        string `xml:"subject,attr"`
	EventType      string `xml:"eventType,attr"`
	Query          string `xml:"query,attr"`
	ReduceSpec     string `xml:"reduceSpec,attr"`
	Limit          string `xml:"limit,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
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

// An outbound mail connector task's parameters, carried on a service task as an
// <atlas:mailConnector connector="..." to="..." .../> extension element (ADR-0079).
// connector names a server-registered mail provider (its host and credentials live
// on the server, never in the model). to (required) is a comma-separated recipient
// list; cc, bcc and from are optional; subject and body are the message. Every field
// value is literal or, with a leading '=', a FEEL expression evaluated over the
// instance's variables at send time (the fx toggle, ADR-0067).
type xmlMailConnector struct {
	Connector string `xml:"connector,attr"`
	To        string `xml:"to,attr"`
	Cc        string `xml:"cc,attr"`
	Bcc       string `xml:"bcc,attr"`
	From      string `xml:"from,attr"`
	Subject   string `xml:"subject,attr"`
	Body      string `xml:"body,attr"`
}

// xmlUserConnector is the <atlas:userConnector> extension of a user-provisioning
// connector task (ADR-0123). Operation selects the action; the remaining
// attributes are literal-or-FEEL values, like the mail connector's fields.
type xmlUserConnector struct {
	Operation   string `xml:"operation,attr"`
	Username    string `xml:"username,attr"`
	Email       string `xml:"email,attr"`
	DisplayName string `xml:"displayName,attr"`
	Roles       string `xml:"roles,attr"`
	Password    string `xml:"password,attr"`
}

// A CSV-to-JSON connector task's parameters, carried on a service task as an
// <atlas:csvConnector source="..." delimiter="," .../> extension element (ADR-0090).
// source names the process variable holding the raw CSV text (default "csvText");
// delimiter is the single-character field separator (default ","); hasHeader is
// "true"/"false" (default true) — whether the first row is a header; columns is an
// optional comma-separated list of field names (omit to derive them from the header
// row); resultVariable names the variable the parsed rows are written to (default
// "rows"). The layout lives in the model, so nothing but the file arrives at runtime.
type xmlCsvConnector struct {
	Source         string `xml:"source,attr"`
	Delimiter      string `xml:"delimiter,attr"`
	HasHeader      string `xml:"hasHeader,attr"`
	Columns        string `xml:"columns,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
}

// splitCSVColumns turns a csvConnector's comma-separated columns attribute into a
// trimmed list of field names, dropping empty entries so a trailing comma or an
// unset attribute yields no phantom column. An empty result means "derive the
// columns from the header row" (ADR-0090).
func splitCSVColumns(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if name := strings.TrimSpace(p); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// csvHasHeader interprets a csvConnector's hasHeader attribute, defaulting to true
// (a header row is present) when the attribute is absent or blank — matching the
// CSV parser's own default (ADR-0084/0090).
func csvHasHeader(attr string) bool {
	s := strings.TrimSpace(attr)
	return s == "" || strings.EqualFold(s, "true")
}

// A SharePoint connector task's parameters, carried on a service task as an
// <atlas:sharepointConnector connector="..." site="..." list="..."> extension
// element (ADR-0105). connector names a server-registered SharePoint provider (its
// Graph base and OAuth credential live on the server, never in the model). site
// (required) addresses the SharePoint site ("host,/sites/path" or a site id); list
// (required) is the list name or id the item is created in; resultVariable, if set,
// receives the created item's JSON. Each ItemField child is one column value of the
// created item. Every value is literal or, with a leading '=', a FEEL expression
// evaluated over the instance's variables at call time (the fx toggle, ADR-0067).
type xmlSharePointConnector struct {
	Connector      string      `xml:"connector,attr"`
	Site           string      `xml:"site,attr"`
	List           string      `xml:"list,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	Fields         []xmlHTTPKV `xml:"itemField"`
}

// A BMC Remedy connector task's parameters, carried on a service task as an
// <atlas:remedyConnector connector="..." form="..." resultVariable="..."> extension
// element with <atlas:remedyField name="..." value="..."/> children (ADR-0106).
// connector names a server-registered Remedy instance (its base URL and credentials
// live on the server, never in the model). form is the Remedy form the entry is
// created in (e.g. "HPD:IncidentInterface_Create"); each field is one entry value;
// resultVariable, if set, receives the created entry's id. form and every field value
// is literal or, with a leading '=', a FEEL expression over the instance's variables
// at call time (the fx toggle, ADR-0067).
type xmlRemedyConnector struct {
	Connector      string      `xml:"connector,attr"`
	Form           string      `xml:"form,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	Fields         []xmlHTTPKV `xml:"remedyField"`
}

// A web-scraping connector task's parameters, carried on a service task as an
// <atlas:webscrapeConnector url="..." selector="..." attribute="..."
// resultVariable="..."/> extension element (ADR-0118). url (required) is the page to
// fetch and selector (required) the CSS selector whose matches are extracted; both
// live in the model (unlike a registry endpoint), and credentials never do. attribute,
// when set, names the HTML attribute read from each match (omit to read each match's
// text). resultVariable (required) receives the extracted values as a JSON array. url
// and selector are literal or, with a leading '=', a FEEL expression over the instance's
// variables at call time (the fx toggle, ADR-0067); attribute is a structural literal.
type xmlWebScrapeConnector struct {
	Url            string `xml:"url,attr"`
	Selector       string `xml:"selector,attr"`
	Attribute      string `xml:"attribute,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// A mockup service task's parameters, carried on a service task as an
// <atlas:mockupConnector minDuration="PT1S" maxDuration="PT5S" .../> extension
// element (ADR-0120): the engine simulates the task itself. minDuration/maxDuration
// are ISO-8601 durations bounding the random simulated execution time (a single
// fixed duration is minDuration == maxDuration). resultExpression, when set, is a
// FEEL expression (a leading '=' is optional and stripped) evaluated over the
// instance's variables and written into resultVariable — the input→output script,
// e.g. a simulated REST response. failRate is the failure probability in [0,1].
// When errorCode is set, a simulated failure throws a BPMN error with that code
// (caught by a matching error boundary/event subprocess); otherwise it raises an
// incident with failMessage.
type xmlMockupConnector struct {
	MinDuration      string `xml:"minDuration,attr"`
	MaxDuration      string `xml:"maxDuration,attr"`
	ResultVariable   string `xml:"resultVariable,attr"`
	ResultExpression string `xml:"resultExpression,attr"`
	FailRate         string `xml:"failRate,attr"`
	FailMessage      string `xml:"failMessage,attr"`
	ErrorCode        string `xml:"errorCode,attr"`
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
	JobScript     *xmlAtlasScript            `xml:"extensionElements>jobScript"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
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
	MultiInstance  *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop   *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
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
