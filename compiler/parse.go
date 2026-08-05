package compiler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
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
	return compileProcess(key, version, defs.Processes[0], buildMessageResolver(defs), buildSignalResolver(defs))
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
	poolName := participantNames(defs)

	var out []Deployable
	for _, proc := range defs.Processes {
		if len(proc.StartEvents) == 0 {
			continue // black-box pool: nothing to run
		}
		cp, err := compileProcess(baseKey+uint64(len(out)), version, proc, resolve, resolveSig)
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
			return compileProcess(key, version, proc, buildMessageResolver(defs), buildSignalResolver(defs))
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

// compileProcess linearizes one <process> into an immutable CompiledProcess,
// resolving message and signal references through resolveMessage/resolveSignal (shared
// across a collaboration's processes).
func compileProcess(key uint64, version int32, proc xmlProcess, resolveMessage func(ownerId, messageRef string) (string, *expr.Compiled, error), resolveSignal func(ownerId, signalRef string) (string, error)) (*CompiledProcess, error) {
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

	// Register every flow node — the process root and, recursively, each embedded
	// subprocess scope — then require a root-scope start event before wiring flows.
	// Data objects and I/O mappings below stay process-scoped (ADR-0074).
	if err := registerScope(b, ids, register, resolveMessage, resolveSignal, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	if err := connectScope(b, ids, &proc.xmlFlowContent); err != nil {
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

	// Wire multi-instance loop characteristics for every activity in the scope tree,
	// recursively, mirroring wireScopeIO (ADR-0077). Each FEEL source compiles once at
	// deploy (I5); a loop with neither an input collection nor a cardinality is refused
	// (it has no way to know how many iterations to run). The compiler records the
	// detail; the engine runs the iterations.
	compileMI := func(ownerId, what, source string) (*expr.Compiled, error) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "="))
		if text == "" {
			return nil, nil
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: multi-instance %s on %q: %w", what, ownerId, err)
		}
		return e, nil
	}
	wireMI := func(ownerId string, mi *xmlMultiInstance) error {
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
	var wireScopeMI func(c *xmlFlowContent) error
	wireScopeMI = func(c *xmlFlowContent) error {
		for _, st := range c.ServiceTasks {
			if err := wireMI(st.Id, st.MultiInstance); err != nil {
				return err
			}
		}
		for _, st := range c.ScriptTasks {
			if err := wireMI(st.Id, st.MultiInstance); err != nil {
				return err
			}
		}
		for _, ut := range c.UserTasks {
			if err := wireMI(ut.Id, ut.MultiInstance); err != nil {
				return err
			}
		}
		for _, ca := range c.CallActivities {
			if err := wireMI(ca.Id, ca.MultiInstance); err != nil {
				return err
			}
		}
		for i := range c.SubProcesses {
			sub := &c.SubProcesses[i]
			if err := wireMI(sub.Id, sub.MultiInstance); err != nil {
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

// A top-level signal declaration (ADR-0088). A signal is broadcast by name — it carries
// no correlation key and no code — so it needs only an id and a name.
type xmlSignal struct {
	Id   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type xmlSignalEventDefinition struct {
	SignalRef string `xml:"signalRef,attr"`
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

	Tasks             []xmlNode             `xml:"task"`
	ManualTasks       []xmlNode             `xml:"manualTask"`
	ParallelGateways  []xmlNode             `xml:"parallelGateway"`
	InclusiveGateways []xmlInclusiveGateway `xml:"inclusiveGateway"`

	UserTasks []xmlUserTask `xml:"userTask"`

	SubProcesses   []xmlSubProcess   `xml:"subProcess"`
	CallActivities []xmlCallActivity `xml:"callActivity"`

	// Captured only to give a clear "unsupported element" error (see Parse); none
	// of these are executable yet.
	SendTasks    []xmlNode `xml:"sendTask"`
	ReceiveTasks []xmlNode `xml:"receiveTask"`
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
	xmlFlowContent
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

// An intermediate catch event; the timer and message variants are executable.
// Each definition is a pointer so an absent one is detected as nil.
type xmlIntermediateCatchEvent struct {
	Id      string                     `xml:"id,attr"`
	Timer   *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
}

// An intermediate throw event; the message and signal variants are executable.
type xmlIntermediateThrowEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
}

// An end event. A plain (none) end event just ends the instance; one bearing a
// messageEventDefinition is a message end event, which publishes the message
// then ends (ADR-0052); a signalEventDefinition is a signal end event, which
// broadcasts the signal then ends (ADR-0088). Each is a pointer so an absent one is nil.
type xmlEndEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Terminate is present when the end event carries a <terminateEventDefinition>.
	// Atlas can't execute a terminate end yet, so it is rejected at compile time rather
	// than silently dropped to a plain end (which would abandon the terminate semantics).
	Terminate *xmlTerminateEventDefinition `xml:"terminateEventDefinition"`
}

// xmlTerminateEventDefinition is the empty <terminateEventDefinition> element; only its
// presence matters (a non-nil pointer once parsed).
type xmlTerminateEventDefinition struct{}

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
	Id            string                     `xml:"id,attr"`
	Name          string                     `xml:"name,attr"`
	Assignment    xmlAssignmentDefinition    `xml:"extensionElements>assignmentDefinition"`
	Form          xmlFormDefinition          `xml:"extensionElements>formDefinition"`
	Priority      xmlPriorityDefinition      `xml:"extensionElements>priorityDefinition"`
	Schedule      xmlTaskSchedule            `xml:"extensionElements>taskSchedule"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
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
	Id             string            `xml:"id,attr"`
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
	Mail          *xmlMailConnector          `xml:"extensionElements>mailConnector"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

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
	JobScript     *xmlAtlasScript            `xml:"extensionElements>jobScript"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
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
