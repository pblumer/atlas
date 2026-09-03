package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// eventSubStart returns the event subprocess's triggering start event — the first
// start event carrying a message, timer, signal, or error event definition — or nil if it
// has none (ADR-0082, ADR-0088, ADR-0089).
func eventSubStart(sub *xmlSubProcess) *xmlStartEvent {
	for i := range sub.StartEvents {
		if s := &sub.StartEvents[i]; s.Message != nil || s.Timer != nil || s.Signal != nil || s.Error != nil || s.Escalation != nil || s.Conditional != nil {
			return s
		}
	}
	return nil
}

// compileCondition compiles a conditional event's <condition> body to a boolean FEEL
// expression (ADR-0137), mirroring how a gateway condition is compiled (connectScope). It
// strips a leading Zeebe FEEL "=" and trims; an empty condition is a deploy error (a
// conditional event with no predicate can never fire). id names the event for the error.
func compileCondition(id, raw string) (*expr.Compiled, error) {
	cond := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "="))
	if cond == "" {
		return nil, fmt.Errorf("compiler: conditional event %q has no condition", id)
	}
	ce, err := expr.CompileAuto(cond)
	if err != nil {
		return nil, fmt.Errorf("compiler: conditional event %q condition: %w", id, err)
	}
	return ce, nil
}

// registrar records each flow node's BPMN id against the compiled node it became,
// and rejects an id that is empty or already taken.
//
// It keeps the first rejection rather than returning it, so registering a node is
// one statement at each of the ~50 element kinds the scope walk handles instead of
// three. The walk carries on afterwards on purpose: every *valid* id is still
// recorded, so a later step that resolves an element through the table — a boundary
// event finding its host — behaves exactly as it would have, and cannot raise a
// second, misleading error caused by the first. Parse reports the kept error, in
// preference to anything the rest of the walk raised.
type registrar struct {
	b   *Builder
	ids map[string]int32
	// docs is the model-wide element-id → <bpmn:documentation> index, so a node's prose
	// is recorded with its id in one place rather than at each element kind (ADR-0025).
	docs map[string]string
	err  error
}

// node registers nodeID under its BPMN id.
func (r *registrar) node(id string, nodeID int32) {
	if id == "" {
		r.reject(fmt.Errorf("compiler: element with empty id"))
		return
	}
	if _, dup := r.ids[id]; dup {
		r.reject(fmt.Errorf("compiler: duplicate element id %q", id))
		return
	}
	r.ids[id] = nodeID
	r.b.SetElementBpmnId(nodeID, id)                // retain for the live diagram overlay
	r.b.SetElementDocumentation(nodeID, r.docs[id]) // the author's prose about this element (ADR-0025)
}

// taskNode registers a task's node and records the repair form it named (ADR-0169) — the
// form an operator is offered when a token parks on it with an incident. It is a
// separate entry point rather than a parameter on node() because only a task that can
// park has anywhere to put one: a gateway or a flow never raises an incident a person
// repairs by editing variables.
func (r *registrar) taskNode(id string, nodeID int32, formID string) {
	r.node(id, nodeID)
	r.b.SetRepairForm(nodeID, formID)
}

// reject keeps the first rejection; later ones are almost always its fallout.
func (r *registrar) reject(err error) {
	if r.err == nil {
		r.err = err
	}
}

// registerScope registers every flow node of one scope — the process root or an
// embedded subprocess — into the flat node array and the shared id map, then
// recurses into nested subprocesses under a pushed scope so their children carry
// the subprocess as their FlowScope (ADR-0074). It is the per-scope half of
// compileProcess's node registration; data objects and I/O mappings stay
// process-scoped and are wired by the root pass in compileProcess.
//
// resolveMessage is threaded through so a message start/catch/throw inside a scope
// resolves against the collaboration's messages exactly as at the root. reg owns
// the string id → node id mapping and holds any rejection it made; the error
// returned here is the walk's own, and Parse prefers reg's over it.
func registerScope(
	b *Builder,
	reg *registrar,
	resolveMessage func(ownerId, messageRef string) (string, *expr.Compiled, error),
	resolveSignal func(ownerId, signalRef string) (string, error),
	resolveError func(ownerId, errorRef string) (string, error),
	resolveEscalation func(ownerId, escalationRef string) (string, error),
	c *xmlFlowContent,
) error {
	for _, s := range c.StartEvents {
		if s.Message != nil {
			name, keyExpr, err := resolveMessage(s.Id, s.Message.MessageRef)
			if err != nil {
				return err
			}
			reg.node(s.Id, b.AddMessageStartEvent(name, keyExpr, s.SingletonStart == "true"))
			continue
		}
		if s.Signal != nil {
			name, err := resolveSignal(s.Id, s.Signal.SignalRef)
			if err != nil {
				return err
			}
			reg.node(s.Id, b.AddSignalStartEvent(name))
			continue
		}
		if s.Timer != nil {
			schedule, err := parseTimerSchedule(s.Timer)
			if err != nil {
				return fmt.Errorf("compiler: start event %q timer: %w", s.Id, err)
			}
			if schedule.IsFeel() && len(schedule.Expr.Inputs()) > 0 {
				return fmt.Errorf("compiler: start event %q: a timer start event's FEEL schedule must be constant (reference no variables) — a start event has no instance to evaluate against (ADR-0056)", s.Id)
			}
			reg.node(s.Id, b.AddTimerStartEvent(schedule))
			continue
		}
		reg.node(s.Id, b.AddStartEvent())
		// A none start event may carry a start form; the first one that does wins as
		// the process's start form (ADR-0028). Only a root-scope start is a process
		// entry, so a form on a subprocess start is ignored.
		if b.CurrentScope() == -1 && s.Form.FormId != "" {
			b.SetStartFormId(s.Form.FormId)
		}
	}
	// A send task is a service task under a different BPMN label (ADR-0112): it creates a
	// job and waits, and a worker extension on it takes the worker path exactly as on
	// a service task. registerJobWorkerTask compiles both — only the plain-worker node type
	// (AddServiceTask vs AddSendTask, passed as plain) and the diagnostic label differ.
	registerJobWorkerTask := func(st xmlServiceTask, label string, plain func(jobType string, retries int32) int32) error {
		retries, err := serviceTaskRetries(st, label)
		if err != nil {
			return err
		}
		// A service task bearing an <atlas:mockupConnector> extension is simulated by
		// the engine itself (ADR-0120): it creates no job and delegates to no external
		// worker, so it is checked before the worker table and the
		// plain-worker fallthrough. It has no reserved job type — the engine's
		// mockupTaskBehavior arms a timer and completes it.
		if st.Mockup != nil {
			id, err := compileMockupTask(b, st)
			if err != nil {
				return err
			}
			reg.taskNode(st.Id, id, st.Form.FormId)
			return nil
		}
		// A service task (or send task) bearing a worker extension delegates to a
		// server-registered worker via the job path rather than to an external
		// service-task worker. The ordered connectorCompilers table owns the set of
		// flavors (clio, rest, mail, sharepoint, remedy); the first present extension
		// wins, exactly as when these were inlined arms.
		for _, cc := range connectorCompilers {
			if !cc.present(st) {
				continue
			}
			// The worker's own retries attribute is where an author configures the
			// budget (the Modeler writes it there, ADR-0135); a <zeebe:taskDefinition
			// retries> on the same task stays honoured as the fallback.
			retries, err := parseRetries(label, st.Id, firstNonBlank(cc.retries(st), st.TaskDefinition.Retries))
			if err != nil {
				return err
			}
			id, err := cc.compile(b, st, retries)
			if err != nil {
				return err
			}
			reg.taskNode(st.Id, id, st.Form.FormId)
			return nil
		}
		// A service task bearing an <atlas:csvConnector> extension is a CSV-to-JSON
		// task: the in-process CSV worker parses the named source variable's
		// text against the model-authored layout into a rows collection via the job
		// path (ADR-0139), rather than reading a columnConfig variable (ADR-0087). The
		// whole layout lives in the model; only the file arrives at runtime.
		if cn := st.Csv; cn != nil {
			retries, err := parseRetries(label, st.Id, firstNonBlank(cn.Retries, st.TaskDefinition.Retries))
			if err != nil {
				return err
			}
			format, operation, err := csvFormatAndOperation(st.Id, cn.Format, cn.Operation)
			if err != nil {
				return err
			}
			cols, widths, err := splitCSVColumns(st.Id, format, cn.Columns)
			if err != nil {
				return err
			}
			hasHeader := csvHasHeader(cn.HasHeader)
			// A delimited file read without a header row maps columns by position, so it
			// must name them; with a header they may be derived from it (ADR-0139).
			// Fixed-width always carries a layout (checked above) and an attribute-value
			// file names its own fields, so neither needs this.
			if format == csvimportFormatCSV && operation == csvimportOperationRead && !hasHeader && len(cols) == 0 {
				return fmt.Errorf("compiler: csv task %q without a header row must list its columns", st.Id)
			}
			id := b.AddCsvConnectorTask(CsvConfig{
				Source:    strings.TrimSpace(cn.Source),
				Result:    strings.TrimSpace(cn.ResultVariable),
				Delimiter: cn.Delimiter,
				HasHeader: hasHeader,
				Columns:   cols,
				Format:    format,
				Operation: operation,
				Widths:    widths,
				Retries:   retries,
			})
			reg.taskNode(st.Id, id, st.Form.FormId)
			return nil
		}
		if st.TaskDefinition.Type == "" {
			// A send task reaching here has no message kind either (its messageRef/operationRef
			// were resolved earlier), so name every kind it could take rather than only the task
			// definition (ADR-0112) — this is the "Message selected but no message chosen yet" state.
			if label == "send task" {
				return fmt.Errorf("compiler: send task %q has no kind: choose a message (messageRef/operationRef), a task definition, or a worker", st.Id)
			}
			return fmt.Errorf("compiler: %s %q has no task definition type", label, st.Id)
		}
		reg.taskNode(st.Id, plain(st.TaskDefinition.Type, retries), st.Form.FormId)
		return nil
	}
	for _, st := range c.ServiceTasks {
		if err := registerJobWorkerTask(st, "service task", b.AddServiceTask); err != nil {
			return err
		}
	}
	// Message-kind send tasks compile to a throw (ADR-0112) — instantaneous, so not an
	// activity. Record them so a boundary drawn on one gets a targeted error below rather
	// than the generic "attaches to a non-activity".
	messageSendIDs := map[string]bool{}
	for _, st := range c.SendTasks {
		// The message kind: a <sendTask messageRef> is a correlating throw in task form
		// (ADR-0112). It reuses the intermediate message throw's compile path — resolve the
		// message, then register a TypeMessageThrowEvent, which correlates and flows straight
		// on. A throw is instantaneous, so unlike the job and worker kinds it is not an activity
		// (no boundary/I/O/MI — those loops skip it, keyed on the same MessageRef).
		if strings.TrimSpace(st.MessageRef) != "" {
			messageSendIDs[st.Id] = true
			name, keyExpr, err := resolveMessage(st.Id, st.MessageRef)
			if err != nil {
				return err
			}
			reg.node(st.Id, b.AddMessageThrowEvent(name, keyExpr))
			continue
		}
		if err := registerJobWorkerTask(st, "send task", b.AddSendTask); err != nil {
			return err
		}
	}
	for _, st := range c.ScriptTasks {
		// A script task bearing an <atlas:jobScript> extension is a polyglot script
		// task: it runs in a general-purpose language via the job path (ADR-0047),
		// not inline as FEEL. The language is validated and mapped to its reserved
		// job type at deploy time (invariant I5), so the runtime worker never has to.
		if js := st.JobScript; js != nil {
			jobType, ok := scriptJobTypes[strings.ToLower(strings.TrimSpace(js.Language))]
			if !ok {
				return fmt.Errorf("compiler: script task %q has unsupported script language %q", st.Id, js.Language)
			}
			source := strings.TrimSpace(js.Source)
			if source == "" {
				return fmt.Errorf("compiler: script task %q has no script source", st.Id)
			}
			if js.ResultVariable == "" {
				return fmt.Errorf("compiler: script task %q has no result variable", st.Id)
			}
			// A script job fails like any other job, so it carries its own retry budget
			// (ADR-0135) authored on the <atlas:jobScript> extension.
			retries, err := parseRetries("script task", st.Id, js.Retries)
			if err != nil {
				return err
			}
			node := b.AddScriptJobTask(jobType, strings.ToLower(strings.TrimSpace(js.Language)), source, js.ResultVariable, retries)
			reg.node(st.Id, node)
			continue
		}
		text := strings.TrimSpace(st.Script.Expression)
		text = strings.TrimPrefix(text, "=") // Zeebe marks expressions with a leading '='
		text = strings.TrimSpace(text)
		if text == "" {
			return fmt.Errorf("compiler: script task %q has no expression", st.Id)
		}
		if st.Script.ResultVariable == "" {
			return fmt.Errorf("compiler: script task %q has no result variable", st.Id)
		}
		// FEEL is compiled once, at deploy time (ADR-0008/0015). CompileAuto
		// discovers the process variables the expression reads; a syntax or type
		// error fails here — i.e. fails deploy.
		e, err := expr.CompileAuto(text)
		if err != nil {
			return fmt.Errorf("compiler: script task %q: %w", st.Id, err)
		}
		reg.node(st.Id, b.AddScriptTask(e, st.Script.ResultVariable))
	}
	for _, brt := range c.BusinessRuleTasks {
		if brt.CalledDecision.DecisionId == "" {
			return fmt.Errorf("compiler: business rule task %q has no calledDecision decisionId", brt.Id)
		}
		retries, err := parseRetries("business rule task", brt.Id, brt.CalledDecision.Retries)
		if err != nil {
			return err
		}
		inputs, err := decisionInputs(brt.Inputs)
		if err != nil {
			return fmt.Errorf("compiler: business rule task %q: %w", brt.Id, err)
		}
		mappings, err := decisionInputMappings(brt.Id, brt.InputMappings)
		if err != nil {
			return err
		}
		// A business rule task marked with <atlas:temisConnector> is a central
		// decision: it delegates to the named server-registered temis worker
		// instead of the embedded library (ADR-0050). Authoring is otherwise
		// identical.
		var node int32
		if tc := brt.TemisConnector; tc != nil {
			if tc.Connector == "" {
				return fmt.Errorf("compiler: business rule task %q has a temisConnector with no worker name", brt.Id)
			}
			node, err = b.AddTemisDecisionTask(tc.Connector, brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries)
		} else {
			node, err = b.AddBusinessRuleTaskMapped(brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries, decisionBinding(brt.CalledDecision.BindingType))
		}
		if err != nil {
			return err
		}
		reg.taskNode(brt.Id, node, brt.Form.FormId)
	}
	for _, ut := range c.UserTasks {
		retries := int32(defaultRetries)
		priority := int32(defaultUserTaskPriority)
		if s := strings.TrimSpace(ut.Priority.Priority); s != "" {
			p, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return fmt.Errorf("compiler: user task %q priority %q: %w", ut.Id, s, err)
			}
			priority = int32(p)
		}
		var dueDateNanos int64
		if s := strings.TrimSpace(ut.Schedule.DueDate); s != "" {
			s = strings.TrimSpace(strings.TrimPrefix(s, "=")) // tolerate a FEEL '=' prefix
			nanos, err := parseISO8601Duration(s)
			if err != nil {
				return fmt.Errorf("compiler: user task %q dueDate: %w", ut.Id, err)
			}
			dueDateNanos = nanos
		}
		reg.node(ut.Id, b.AddUserTask(ut.Name, ut.Assignment.Assignee, ut.Assignment.CandidateGroups, ut.Form.FormId, priority, dueDateNanos, retries))
	}
	for _, ca := range c.CallActivities {
		ce := ca.CalledElement
		pid := strings.TrimSpace(ce.ProcessId)
		if pid == "" {
			return fmt.Errorf("compiler: call activity %q has no calledElement processId", ca.Id)
		}
		// Each propagation flag defaults to true (Zeebe): an absent or non-"false"
		// value propagates all variables in that direction.
		propParent := ce.PropagateAllParentVariables != "false"
		propChild := ce.PropagateAllChildVariables != "false"
		reg.node(ca.Id, b.AddCallActivity(pid, decisionBinding(ce.BindingType), propParent, propChild))
	}
	for _, g := range c.ExclusiveGateways {
		reg.node(g.Id, b.AddExclusiveGateway())
	}
	for _, g := range c.ParallelGateways {
		reg.node(g.Id, b.AddParallelGateway())
	}
	for _, g := range c.InclusiveGateways {
		reg.node(g.Id, b.AddInclusiveGateway())
	}
	for _, g := range c.EventBasedGateways {
		reg.node(g.Id, b.AddEventBasedGateway())
	}
	for _, ev := range c.IntermediateCatchEvents {
		switch {
		case ev.Timer != nil:
			schedule, err := parseTimerSchedule(ev.Timer)
			if err != nil {
				return fmt.Errorf("compiler: intermediate catch event %q timer: %w", ev.Id, err)
			}
			if schedule.Repeats() {
				return fmt.Errorf("compiler: intermediate catch event %q: timeCycle is not supported (a catch fires once); use timeDuration or timeDate", ev.Id)
			}
			reg.node(ev.Id, b.AddTimerCatchSchedule(schedule))
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddMessageCatchEvent(name, keyExpr))
		case ev.Signal != nil:
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddSignalCatchEvent(name))
		case ev.Link != nil:
			// A link catch is the landing point of a link throw of the same name (ADR-0133).
			// It compiles to a bare pass-through node; connectScope wires the synthetic edge
			// from each matching throw and validates the name.
			reg.node(ev.Id, b.AddLinkCatchEvent())
		case ev.Conditional != nil:
			// A conditional catch waits until its boolean FEEL condition becomes true, then
			// flows on (ADR-0137). It arms inert and is driven to Completing by a re-check on
			// variable change.
			cond, err := compileCondition(ev.Id, ev.Conditional.Condition)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddConditionalCatchEvent(cond))
		default:
			return fmt.Errorf("compiler: intermediate catch event %q: only timer, message, signal, link, and conditional events are supported yet", ev.Id)
		}
	}
	for _, ev := range c.IntermediateThrowEvents {
		if ev.Signal != nil {
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddSignalThrowEvent(name))
			continue
		}
		if ev.Compensation != nil {
			// A compensation throw triggers compensation of completed activities in its
			// scope (or of the one named by activityRef, resolved in a post-pass since the
			// target may be registered later) — ADR-0103.
			reg.node(ev.Id, b.AddCompensationThrowEvent())
			continue
		}
		if ev.Escalation != nil {
			// An escalation throw raises an escalation, propagating up to the nearest
			// matching handler, then continues on its outgoing flow (ADR-0125).
			code, err := resolveEscalation(ev.Id, ev.Escalation.EscalationRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddEscalationThrowEvent(code))
			continue
		}
		if ev.Link != nil {
			// A link throw is a goto to the link catch of the same name (ADR-0133). It compiles
			// to a bare pass-through node; connectScope adds the synthetic edge to its catch.
			reg.node(ev.Id, b.AddLinkThrowEvent())
			continue
		}
		if ev.Message == nil {
			return fmt.Errorf("compiler: intermediate throw event %q: only message, signal, compensation, escalation, and link events are supported yet", ev.Id)
		}
		name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
		if err != nil {
			return err
		}
		reg.node(ev.Id, b.AddMessageThrowEvent(name, keyExpr))
	}
	// An undefined <task> and a <manualTask> have no execution semantics, so Atlas
	// runs them as pass-throughs (the token flows straight on). This lets a model
	// be drafted and its routing — e.g. a gateway's branches — tested before its
	// tasks are given real implementations.
	for _, t := range c.Tasks {
		reg.node(t.Id, b.AddTask())
	}
	for _, t := range c.ManualTasks {
		reg.node(t.Id, b.AddTask())
	}
	// A receive task waits for its referenced message to correlate, then continues — the
	// message-catch semantics as an activity (ADR-0102). An empty or unknown messageRef is a
	// deploy error, exactly like a message catch event.
	for _, rt := range c.ReceiveTasks {
		name, keyExpr, err := resolveMessage(rt.Id, rt.MessageRef)
		if err != nil {
			return err
		}
		reg.node(rt.Id, b.AddReceiveTask(name, keyExpr))
	}
	for _, e := range c.EndEvents {
		// A terminate end event ends its enclosing flow scope at once (ADR-0116): it
		// terminates every other live token in the scope, then completes the scope — at the
		// root the instance ends, inside a subprocess that subprocess ends and the parent
		// continues. It carries no detail.
		if e.Terminate != nil {
			reg.node(e.Id, b.AddTerminateEndEvent())
			continue
		}
		// A message end event publishes its message then ends; a plain end event
		// just ends (ADR-0052).
		if e.Message != nil {
			name, keyExpr, err := resolveMessage(e.Id, e.Message.MessageRef)
			if err != nil {
				return err
			}
			reg.node(e.Id, b.AddMessageEndEvent(name, keyExpr))
			continue
		}
		if e.Signal != nil {
			name, err := resolveSignal(e.Id, e.Signal.SignalRef)
			if err != nil {
				return err
			}
			reg.node(e.Id, b.AddSignalEndEvent(name))
			continue
		}
		// An error end event throws its error code, ending its scope abnormally and
		// propagating up to the nearest matching handler (ADR-0089).
		if e.Error != nil {
			code, err := resolveError(e.Id, e.Error.ErrorRef)
			if err != nil {
				return err
			}
			reg.node(e.Id, b.AddErrorEndEvent(code))
			continue
		}
		// An escalation end event raises its escalation code, propagating up to the nearest
		// matching handler, then ends its path (ADR-0125). Unlike an error end an uncaught
		// escalation is benign. This arm also stops the former silent degrade — before
		// escalation was compiled, an escalationEventDefinition on an end event fell through
		// to a plain none end, quietly dropping the escalation.
		if e.Escalation != nil {
			code, err := resolveEscalation(e.Id, e.Escalation.EscalationRef)
			if err != nil {
				return err
			}
			reg.node(e.Id, b.AddEscalationEndEvent(code))
			continue
		}
		// A compensation end event triggers compensation, then ends its scope (ADR-0103);
		// its optional activityRef is resolved in the post-pass, like a compensation throw.
		if e.Compensation != nil {
			reg.node(e.Id, b.AddCompensationEndEvent())
			continue
		}
		// A cancel end event cancels its enclosing transaction (ADR-0108); validation
		// (checkTransactions) enforces that the enclosing scope really is a transaction.
		if e.Cancel != nil {
			reg.node(e.Id, b.AddCancelEndEvent())
			continue
		}
		reg.node(e.Id, b.AddEndEvent())
	}
	// Ad-hoc subprocesses: a container scope like an embedded subprocess, but its contained
	// activities are not sequenced from a start event — the runtime activates its entry
	// activities (the contained nodes with no incoming flow) on entry (ADR-0138). Register the
	// container first so its children can be scoped to it, compile its optional FEEL completion
	// condition and its ordering / cancel-remaining flags, then recurse into its flow content.
	for i := range c.AdHocSubProcesses {
		ah := &c.AdHocSubProcesses[i]
		// Sequential ordering runs one contained activity at a time. The engine implements
		// the parallel form (ADR-0138); rather than silently running a Sequential ad-hoc as
		// parallel — which would run every activity at once, not what the model says — it is
		// refused at deploy until the sequential driver lands.
		if ah.Ordering == "Sequential" {
			return fmt.Errorf("compiler: ad-hoc subprocess %q uses ordering=\"Sequential\", which Atlas can't execute yet "+
				"(only the default parallel ordering, where every entry activity is activated at once, is supported)", ah.Id)
		}
		d := AdHocDetail{
			// BPMN defaults: cancel the remaining activities when the completion condition
			// holds, and run the entry activities in parallel.
			CancelRemaining: ah.CancelRemainingInstances != "false",
		}
		if cond := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ah.CompletionCondition), "=")); cond != "" {
			ce, err := expr.CompileAuto(cond)
			if err != nil {
				return fmt.Errorf("compiler: ad-hoc subprocess %q completion condition: %w", ah.Id, err)
			}
			d.CompletionCondition = ce
		}
		adhocID := b.AddAdHocSubProcess(d)
		reg.node(ah.Id, adhocID)
		b.PushScope(adhocID)
		if err := registerScope(b, reg, resolveMessage, resolveSignal, resolveError, resolveEscalation, &ah.xmlFlowContent); err != nil {
			return err
		}
		b.PopScope()
	}
	// Nested subprocesses: register the container node, then push its scope so its
	// own flow nodes (registered by recursion) carry it as their FlowScope. The
	// container is created before its children so it has an ElementId to scope them
	// to (ADR-0074).
	for i := range c.SubProcesses {
		sub := &c.SubProcesses[i]
		subID := b.AddSubProcess()
		reg.node(sub.Id, subID)
		// A <transaction> is a subprocess with cancellation added; mark the node so the
		// runtime and validation treat it as one (ADR-0108). It is never triggeredByEvent.
		if sub.IsTransaction {
			b.SetTransaction(subID)
		}
		b.PushScope(subID)
		if err := registerScope(b, reg, resolveMessage, resolveSignal, resolveError, resolveEscalation, &sub.xmlFlowContent); err != nil {
			return err
		}
		b.PopScope()
		// An event subprocess (triggeredByEvent) is not entered by a flow; capture its
		// start event's trigger (message/timer) and interrupting flag so the runtime can
		// arm it while the parent scope runs (ADR-0082). Its inner nodes were registered
		// above (the start also stays a normal message/timer start that flows on when the
		// handler runs); this only records the arming spec on the container.
		if sub.TriggeredByEvent == "true" {
			st := eventSubStart(sub)
			if st == nil {
				return fmt.Errorf("compiler: event subprocess %q must have a start event with a message, timer, signal, error, escalation, or conditional event definition", sub.Id)
			}
			d := EventSubProcessDetail{StartNode: reg.ids[st.Id], Interrupting: st.IsInterrupting != "false"}
			switch {
			case st.Message != nil:
				name, keyExpr, err := resolveMessage(st.Id, st.Message.MessageRef)
				if err != nil {
					return err
				}
				d.Kind, d.MessageName, d.CorrelationKey = BoundaryMessage, name, keyExpr
			case st.Signal != nil:
				name, err := resolveSignal(st.Id, st.Signal.SignalRef)
				if err != nil {
					return err
				}
				d.Kind, d.SignalName = BoundarySignal, name
			case st.Error != nil:
				// An error event subprocess catches an error propagating in its scope by code
				// (ADR-0089). Like an error boundary it is always interrupting — an error tears
				// its scope down — so the isInterrupting attribute is overridden.
				code, err := resolveError(st.Id, st.Error.ErrorRef)
				if err != nil {
					return err
				}
				d.Kind, d.ErrorCode, d.Interrupting = BoundaryError, code, true
			case st.Escalation != nil:
				// An escalation event subprocess catches an escalation propagating in its scope
				// by code (ADR-0125). Unlike an error event subprocess it honors isInterrupting —
				// a non-interrupting escalation handler runs alongside the still-running scope —
				// so d.Interrupting (already set from st.IsInterrupting) is kept.
				code, err := resolveEscalation(st.Id, st.Escalation.EscalationRef)
				if err != nil {
					return err
				}
				d.Kind, d.EscalationCode = BoundaryEscalation, code
			case st.Timer != nil:
				schedule, err := parseTimerSchedule(st.Timer)
				if err != nil {
					return fmt.Errorf("compiler: event subprocess %q timer: %w", sub.Id, err)
				}
				d.Kind, d.Schedule = BoundaryTimer, schedule
			case st.Conditional != nil:
				// A conditional event subprocess arms inert and fires when its FEEL condition
				// over the parent scope's variables becomes true (ADR-0137) — the event-sub
				// analog of a conditional boundary. Like escalation it honors isInterrupting;
				// unlike error it is not always interrupting. It is re-checked on every variable
				// change in its scope, not driven by a throw.
				cond, err := compileCondition(st.Id, st.Conditional.Condition)
				if err != nil {
					return err
				}
				d.Kind, d.Condition = BoundaryConditional, cond
			}
			b.SetEventSubProcess(subID, d)
		}
	}
	// Boundary events are registered last in their scope: each attaches to a host
	// activity by id, which must already be registered so attachedToRef resolves
	// (ADR-0040). A subprocess is an activity too, so a boundary may attach to one.
	// An absent or "true" cancelActivity is interrupting (BPMN default); "false" is
	// non-interrupting.
	for _, ev := range c.BoundaryEvents {
		host, ok := reg.ids[ev.AttachedToRef]
		if !ok {
			return fmt.Errorf("compiler: boundary event %q attaches to unknown activity %q", ev.Id, ev.AttachedToRef)
		}
		if messageSendIDs[ev.AttachedToRef] {
			return fmt.Errorf("compiler: boundary event %q attaches to send task %q, but a message-kind send task is an instantaneous throw (it publishes the message and continues) and cannot host a boundary event; switch the send task to a job or Worker Type, which waits so a boundary can fire, or model a wait-and-time-out with a receive task and a boundary timer", ev.Id, ev.AttachedToRef)
		}
		interrupting := ev.CancelActivity != "false"
		switch {
		case ev.Timer != nil:
			schedule, err := parseTimerSchedule(ev.Timer)
			if err != nil {
				return fmt.Errorf("compiler: boundary event %q timer: %w", ev.Id, err)
			}
			if schedule.Repeats() && interrupting {
				return fmt.Errorf("compiler: boundary event %q: an interrupting boundary timer does not support timeCycle (it fires once); use timeDuration or timeDate, or make the boundary non-interrupting", ev.Id)
			}
			reg.node(ev.Id, b.AddBoundaryTimerSchedule(host, interrupting, schedule))
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddBoundaryMessageEvent(host, interrupting, name, keyExpr))
		case ev.Signal != nil:
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddBoundarySignalEvent(host, interrupting, name))
		case ev.Error != nil:
			// An error boundary catches an error propagating up to the host by code and is
			// always interrupting — cancelActivity is ignored (ADR-0089).
			code, err := resolveError(ev.Id, ev.Error.ErrorRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddBoundaryErrorEvent(host, code))
		case ev.Escalation != nil:
			// An escalation boundary catches an escalation propagating up to the host by code
			// (ADR-0125). Unlike an error boundary it honors cancelActivity — an interrupting
			// escalation boundary tears the host down, a non-interrupting one runs the handler
			// alongside the still-running host.
			code, err := resolveEscalation(ev.Id, ev.Escalation.EscalationRef)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddBoundaryEscalationEvent(host, code, interrupting))
		case ev.Compensation != nil:
			// A compensation boundary is inert: it never arms, it only marks its host
			// compensable and links to a handler (resolved from a BPMN <association> in the
			// post-pass). cancelActivity is ignored — a compensation boundary never
			// interrupts (ADR-0103).
			reg.node(ev.Id, b.AddBoundaryCompensationEvent(host))
		case ev.Cancel != nil:
			// A cancel boundary catches its host transaction's cancellation and is always
			// interrupting — cancelActivity is ignored (ADR-0108). validation (checkTransactions)
			// enforces that the host really is a transaction.
			reg.node(ev.Id, b.AddBoundaryCancelEvent(host))
		case ev.Conditional != nil:
			// A conditional boundary fires while the host runs when its boolean FEEL condition
			// becomes true (ADR-0137). It honors cancelActivity — interrupting tears the host
			// down, non-interrupting runs the handler alongside. It opens no subscription and is
			// re-evaluated on variable change.
			cond, err := compileCondition(ev.Id, ev.Conditional.Condition)
			if err != nil {
				return err
			}
			reg.node(ev.Id, b.AddBoundaryConditionalEvent(host, cond, interrupting))
		default:
			return fmt.Errorf("compiler: boundary event %q: only timer, message, signal, error, escalation, conditional, compensation, and cancel boundary events are supported yet", ev.Id)
		}
	}
	// Report an unsupported element with a clear message rather than letting it
	// surface later as a confusing "unknown targetRef" when a flow points at it.
	// (Ad-hoc subprocesses used to be listed here; they execute now — ADR-0138.)
	for _, u := range []struct {
		label string
		nodes []xmlNode
	}{
		{"complexGateway", c.ComplexGateways},
	} {
		if len(u.nodes) > 0 {
			return fmt.Errorf("compiler: element %q is a <%s>, which Atlas can't execute yet "+
				"(supported: start/end events, tasks (undefined/manual pass-through, service, script, "+
				"business rule, user), embedded and ad-hoc subprocesses, exclusive/parallel/inclusive gateways, and timer/message intermediate events)", u.nodes[0].Id, u.label)
		}
	}
	return nil
}

// resolveCompensation wires compensation links after every node is registered (ADR-0103).
// A compensation boundary event is joined to its handler activity by a BPMN <association>
// (one endpoint the boundary, the other the handler); a compensation throw/end may name a
// single activity to compensate via activityRef. Both endpoints resolve through the flat,
// process-wide id map, so this walks the whole scope tree once. A compensation boundary
// with no association, or a reference to an unknown activity, is a deploy error.
func resolveCompensation(b *Builder, ids map[string]int32, root *xmlFlowContent) error {
	// Gather every scope's flow content — the root and, recursively, each subprocess.
	var scopes []*xmlFlowContent
	var walk func(fc *xmlFlowContent)
	walk = func(fc *xmlFlowContent) {
		scopes = append(scopes, fc)
		for i := range fc.SubProcesses {
			walk(&fc.SubProcesses[i].xmlFlowContent)
		}
		for i := range fc.AdHocSubProcesses {
			walk(&fc.AdHocSubProcesses[i].xmlFlowContent)
		}
	}
	walk(root)

	// Which boundary-event ids are compensation boundaries.
	compBoundary := make(map[string]bool)
	for _, fc := range scopes {
		for _, ev := range fc.BoundaryEvents {
			if ev.Compensation != nil {
				compBoundary[ev.Id] = true
			}
		}
	}

	// Link each compensation boundary to its handler via an <association>: one endpoint is
	// the boundary, the other the handler activity.
	resolved := make(map[string]bool)
	for _, fc := range scopes {
		for _, a := range fc.Associations {
			var boundaryID, handlerID string
			switch {
			case compBoundary[a.SourceRef]:
				boundaryID, handlerID = a.SourceRef, a.TargetRef
			case compBoundary[a.TargetRef]:
				boundaryID, handlerID = a.TargetRef, a.SourceRef
			default:
				continue // not a compensation association
			}
			bNode, ok := ids[boundaryID]
			if !ok {
				continue
			}
			hNode, ok := ids[handlerID]
			if !ok {
				return fmt.Errorf("compiler: compensation association %q links boundary %q to unknown activity %q", a.Id, boundaryID, handlerID)
			}
			b.SetCompensationHandler(bNode, hNode)
			resolved[boundaryID] = true
		}
	}
	// A compensation boundary with no association has no handler to run — fail the deploy
	// rather than silently deploy a boundary that can never compensate anything.
	for id := range compBoundary {
		if !resolved[id] {
			return fmt.Errorf("compiler: compensation boundary event %q has no <association> linking it to a compensation handler", id)
		}
	}

	// Narrow each compensation throw/end that names a specific activity to compensate.
	resolveRef := func(evID, activityRef string) error {
		if activityRef == "" {
			return nil // compensate the whole scope
		}
		node, ok := ids[evID]
		if !ok {
			return nil
		}
		target, ok := ids[activityRef]
		if !ok {
			return fmt.Errorf("compiler: compensation event %q references unknown activity %q", evID, activityRef)
		}
		b.SetCompensationActivityRef(node, target)
		return nil
	}
	for _, fc := range scopes {
		for _, ev := range fc.IntermediateThrowEvents {
			if ev.Compensation != nil {
				if err := resolveRef(ev.Id, ev.Compensation.ActivityRef); err != nil {
					return err
				}
			}
		}
		for _, ev := range fc.EndEvents {
			if ev.Compensation != nil {
				if err := resolveRef(ev.Id, ev.Compensation.ActivityRef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// connectScope wires the sequence flows of one scope — compiling any FEEL
// condition and marking each exclusive/inclusive gateway's default flow — then
// recurses into nested subprocesses. A flow's endpoints resolve through the flat,
// process-wide id map; a flow that connects two different scopes compiles here but
// is rejected by checkScopes (ADR-0074). Defaults are per scope: a gateway's
// default is one of its own outgoing flows, so a scope-local flow index suffices.
func connectScope(b *Builder, ids map[string]int32, c *xmlFlowContent) error {
	flowIdx := make(map[string]int32, len(c.Flows))
	for _, f := range c.Flows {
		src, ok := ids[f.SourceRef]
		if !ok {
			return fmt.Errorf("compiler: flow %q references unknown sourceRef %q", f.Id, f.SourceRef)
		}
		tgt, ok := ids[f.TargetRef]
		if !ok {
			return fmt.Errorf("compiler: flow %q references unknown targetRef %q", f.Id, f.TargetRef)
		}
		fid := b.Connect(src, tgt)
		flowIdx[f.Id] = fid
		if cond := strings.TrimSpace(f.Condition); cond != "" {
			cond = strings.TrimSpace(strings.TrimPrefix(cond, "=")) // FEEL condition, '=' prefix per Zeebe
			ce, err := expr.CompileAuto(cond)
			if err != nil {
				return fmt.Errorf("compiler: flow %q condition: %w", f.Id, err)
			}
			b.SetFlowCondition(fid, ce)
		}
	}
	// Mark each exclusive/inclusive gateway's default flow (taken when no condition
	// holds).
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
	for _, g := range c.ExclusiveGateways {
		if err := markDefault("exclusive", g.Id, g.Default); err != nil {
			return err
		}
	}
	for _, g := range c.InclusiveGateways {
		if err := markDefault("inclusive", g.Id, g.Default); err != nil {
			return err
		}
	}
	// Resolve link events within this scope (ADR-0133): a link throw is a goto to the link
	// catch of the same name, which compiles to a synthetic sequence flow throw→catch. Index
	// this scope's link catches by name (exactly one per name — a second is an ambiguous
	// destination), then connect each link throw to its catch. Matching is scope-local, so the
	// synthetic edge stays scope-consistent (checkScopes) and a throw and catch in different
	// scopes do not pair.
	linkCatch := make(map[string]int32)
	for _, ev := range c.IntermediateCatchEvents {
		if ev.Link == nil {
			continue
		}
		name := strings.TrimSpace(ev.Link.Name)
		if name == "" {
			return fmt.Errorf("compiler: link catch event %q has no name", ev.Id)
		}
		if _, dup := linkCatch[name]; dup {
			return fmt.Errorf("compiler: duplicate link catch name %q — a link name has at most one catch (its destination) per scope", name)
		}
		linkCatch[name] = ids[ev.Id]
	}
	for _, ev := range c.IntermediateThrowEvents {
		if ev.Link == nil {
			continue
		}
		name := strings.TrimSpace(ev.Link.Name)
		if name == "" {
			return fmt.Errorf("compiler: link throw event %q has no name", ev.Id)
		}
		catch, ok := linkCatch[name]
		if !ok {
			return fmt.Errorf("compiler: link throw event %q references link %q, which has no matching link catch in this scope", ev.Id, name)
		}
		b.Connect(ids[ev.Id], catch)
	}
	for i := range c.SubProcesses {
		if err := connectScope(b, ids, &c.SubProcesses[i].xmlFlowContent); err != nil {
			return err
		}
	}
	// An ad-hoc subprocess's contained sequence flows are wired like any other scope's
	// (ADR-0138) — contained activities may still be connected to each other, and a
	// boundary event on one routes out normally.
	for i := range c.AdHocSubProcesses {
		if err := connectScope(b, ids, &c.AdHocSubProcesses[i].xmlFlowContent); err != nil {
			return err
		}
	}
	return nil
}

// clioOperation normalizes a clio worker task's operation attribute, defaulting
// an empty value to "write" so the original write-only <atlas:clioConnector>
// element (which carried no operation) keeps compiling unchanged.
func clioOperation(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		return "write"
	}
	return op
}

// clioLimit parses a clio read task's limit attribute: empty is 0 (the worker's
// default), otherwise a non-negative integer. taskID names the task for the error.
func clioLimit(taskID, raw string) (int32, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("compiler: clio read task %q has invalid limit %q", taskID, raw)
	}
	return int32(n), nil
}
