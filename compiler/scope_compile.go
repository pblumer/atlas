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
		if s := &sub.StartEvents[i]; s.Message != nil || s.Timer != nil || s.Signal != nil || s.Error != nil || s.Escalation != nil {
			return s
		}
	}
	return nil
}

// registerScope registers every flow node of one scope — the process root or an
// embedded subprocess — into the flat node array and the shared id map, then
// recurses into nested subprocesses under a pushed scope so their children carry
// the subprocess as their FlowScope (ADR-0074). It is the per-scope half of
// compileProcess's node registration; data objects and I/O mappings stay
// process-scoped and are wired by the root pass in compileProcess.
//
// resolveMessage is threaded through so a message start/catch/throw inside a scope
// resolves against the collaboration's messages exactly as at the root. register
// records the string id → node id mapping (and rejects empty/duplicate ids).
func registerScope(
	b *Builder,
	ids map[string]int32,
	register func(id string, nodeID int32) error,
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
			if err := register(s.Id, b.AddMessageStartEvent(name, keyExpr, s.SingletonStart == "true")); err != nil {
				return err
			}
			continue
		}
		if s.Signal != nil {
			name, err := resolveSignal(s.Id, s.Signal.SignalRef)
			if err != nil {
				return err
			}
			if err := register(s.Id, b.AddSignalStartEvent(name)); err != nil {
				return err
			}
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
			if err := register(s.Id, b.AddTimerStartEvent(schedule)); err != nil {
				return err
			}
			continue
		}
		if err := register(s.Id, b.AddStartEvent()); err != nil {
			return err
		}
		// A none start event may carry a start form; the first one that does wins as
		// the process's start form (ADR-0028). Only a root-scope start is a process
		// entry, so a form on a subprocess start is ignored.
		if b.CurrentScope() == -1 && s.Form.FormId != "" {
			b.SetStartFormId(s.Form.FormId)
		}
	}
	// A send task is a service task under a different BPMN label (ADR-0112): it creates a
	// job and waits, and a connector extension on it takes the connector path exactly as on
	// a service task. registerJobWorkerTask compiles both — only the plain-worker node type
	// (AddServiceTask vs AddSendTask, passed as plain) and the diagnostic label differ.
	registerJobWorkerTask := func(st xmlServiceTask, label string, plain func(jobType string, retries int32) int32) error {
		retries, err := serviceTaskRetries(st, label)
		if err != nil {
			return err
		}
		// A service task bearing an <atlas:mockupConnector> extension is simulated by
		// the engine itself (ADR-0120): it creates no job and delegates to no external
		// worker or connector, so it is checked before the connector table and the
		// plain-worker fallthrough. It has no reserved job type — the engine's
		// mockupTaskBehavior arms a timer and completes it.
		if st.Mockup != nil {
			id, err := compileMockupTask(b, st)
			if err != nil {
				return err
			}
			return register(st.Id, id)
		}
		// A service task (or send task) bearing a connector extension delegates to a
		// server-registered connector via the job path rather than to an external
		// service-task worker. The ordered connectorCompilers table owns the set of
		// flavors (clio, rest, mail, sharepoint, remedy); the first present extension
		// wins, exactly as when these were inlined arms.
		for _, cc := range connectorCompilers {
			if !cc.present(st) {
				continue
			}
			id, err := cc.compile(b, st, retries)
			if err != nil {
				return err
			}
			return register(st.Id, id)
		}
		// A service task bearing an <atlas:csvConnector> extension is a CSV-to-JSON
		// connector task: the in-process CSV worker parses the named source variable's
		// text against the model-authored layout into a rows collection via the job
		// path (ADR-0090), rather than reading a columnConfig variable (ADR-0087). The
		// whole layout lives in the model; only the file arrives at runtime.
		if cn := st.Csv; cn != nil {
			if r := strings.TrimSpace(cn.Retries); r != "" {
				n, err := strconv.Atoi(r)
				if err != nil {
					return fmt.Errorf("compiler: csv connector task %q has invalid retries %q: %w", st.Id, r, err)
				}
				retries = int32(n)
			}
			cols := splitCSVColumns(cn.Columns)
			hasHeader := csvHasHeader(cn.HasHeader)
			// A headerless file maps columns by position, so it must name them; a header
			// file may omit them to derive the columns from the header row (ADR-0090).
			if !hasHeader && len(cols) == 0 {
				return fmt.Errorf("compiler: csv connector task %q without a header row must list its columns", st.Id)
			}
			id := b.AddCsvConnectorTask(CsvConfig{
				Source:    strings.TrimSpace(cn.Source),
				Result:    strings.TrimSpace(cn.ResultVariable),
				Delimiter: cn.Delimiter,
				HasHeader: hasHeader,
				Columns:   cols,
				Retries:   retries,
			})
			return register(st.Id, id)
		}
		if st.TaskDefinition.Type == "" {
			// A send task reaching here has no message kind either (its messageRef/operationRef
			// were resolved earlier), so name every kind it could take rather than only the task
			// definition (ADR-0112) — this is the "Message selected but no message chosen yet" state.
			if label == "send task" {
				return fmt.Errorf("compiler: send task %q has no kind: choose a message (messageRef/operationRef), a task definition, or a connector", st.Id)
			}
			return fmt.Errorf("compiler: %s %q has no task definition type", label, st.Id)
		}
		return register(st.Id, plain(st.TaskDefinition.Type, retries))
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
		// on. A throw is instantaneous, so unlike the job/connector kinds it is not an activity
		// (no boundary/I/O/MI — those loops skip it, keyed on the same MessageRef).
		if strings.TrimSpace(st.MessageRef) != "" {
			messageSendIDs[st.Id] = true
			name, keyExpr, err := resolveMessage(st.Id, st.MessageRef)
			if err != nil {
				return err
			}
			if err := register(st.Id, b.AddMessageThrowEvent(name, keyExpr)); err != nil {
				return err
			}
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
			node := b.AddScriptJobTask(jobType, strings.ToLower(strings.TrimSpace(js.Language)), source, js.ResultVariable, defaultRetries)
			if err := register(st.Id, node); err != nil {
				return err
			}
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
		if err := register(st.Id, b.AddScriptTask(e, st.Script.ResultVariable)); err != nil {
			return err
		}
	}
	for _, brt := range c.BusinessRuleTasks {
		if brt.CalledDecision.DecisionId == "" {
			return fmt.Errorf("compiler: business rule task %q has no calledDecision decisionId", brt.Id)
		}
		retries := int32(defaultRetries)
		if r := brt.CalledDecision.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return fmt.Errorf("compiler: business rule task %q has invalid retries %q: %w", brt.Id, r, err)
			}
			retries = int32(n)
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
		// decision: it delegates to the named server-registered temis connector
		// instead of the embedded library (ADR-0050). Authoring is otherwise
		// identical.
		var node int32
		if tc := brt.TemisConnector; tc != nil {
			if tc.Connector == "" {
				return fmt.Errorf("compiler: business rule task %q has a temisConnector with no connector name", brt.Id)
			}
			node, err = b.AddTemisDecisionTask(tc.Connector, brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries)
		} else {
			node, err = b.AddBusinessRuleTaskMapped(brt.CalledDecision.DecisionId, brt.CalledDecision.ResultVariable, inputs, mappings, retries, decisionBinding(brt.CalledDecision.BindingType))
		}
		if err != nil {
			return err
		}
		if err := register(brt.Id, node); err != nil {
			return err
		}
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
		if err := register(ut.Id, b.AddUserTask(ut.Name, ut.Assignment.Assignee, ut.Assignment.CandidateGroups, ut.Form.FormId, priority, dueDateNanos, retries)); err != nil {
			return err
		}
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
		if err := register(ca.Id, b.AddCallActivity(pid, decisionBinding(ce.BindingType), propParent, propChild)); err != nil {
			return err
		}
	}
	for _, g := range c.ExclusiveGateways {
		if err := register(g.Id, b.AddExclusiveGateway()); err != nil {
			return err
		}
	}
	for _, g := range c.ParallelGateways {
		if err := register(g.Id, b.AddParallelGateway()); err != nil {
			return err
		}
	}
	for _, g := range c.InclusiveGateways {
		if err := register(g.Id, b.AddInclusiveGateway()); err != nil {
			return err
		}
	}
	for _, g := range c.EventBasedGateways {
		if err := register(g.Id, b.AddEventBasedGateway()); err != nil {
			return err
		}
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
			if err := register(ev.Id, b.AddTimerCatchSchedule(schedule)); err != nil {
				return err
			}
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddMessageCatchEvent(name, keyExpr)); err != nil {
				return err
			}
		case ev.Signal != nil:
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddSignalCatchEvent(name)); err != nil {
				return err
			}
		case ev.Link != nil:
			// A link catch is the landing point of a link throw of the same name (ADR-0131).
			// It compiles to a bare pass-through node; connectScope wires the synthetic edge
			// from each matching throw and validates the name.
			if err := register(ev.Id, b.AddLinkCatchEvent()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("compiler: intermediate catch event %q: only timer, message, signal, and link events are supported yet", ev.Id)
		}
	}
	for _, ev := range c.IntermediateThrowEvents {
		if ev.Signal != nil {
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddSignalThrowEvent(name)); err != nil {
				return err
			}
			continue
		}
		if ev.Compensation != nil {
			// A compensation throw triggers compensation of completed activities in its
			// scope (or of the one named by activityRef, resolved in a post-pass since the
			// target may be registered later) — ADR-0103.
			if err := register(ev.Id, b.AddCompensationThrowEvent()); err != nil {
				return err
			}
			continue
		}
		if ev.Escalation != nil {
			// An escalation throw raises an escalation, propagating up to the nearest
			// matching handler, then continues on its outgoing flow (ADR-0125).
			code, err := resolveEscalation(ev.Id, ev.Escalation.EscalationRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddEscalationThrowEvent(code)); err != nil {
				return err
			}
			continue
		}
		if ev.Link != nil {
			// A link throw is a goto to the link catch of the same name (ADR-0131). It compiles
			// to a bare pass-through node; connectScope adds the synthetic edge to its catch.
			if err := register(ev.Id, b.AddLinkThrowEvent()); err != nil {
				return err
			}
			continue
		}
		if ev.Message == nil {
			return fmt.Errorf("compiler: intermediate throw event %q: only message, signal, compensation, escalation, and link events are supported yet", ev.Id)
		}
		name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
		if err != nil {
			return err
		}
		if err := register(ev.Id, b.AddMessageThrowEvent(name, keyExpr)); err != nil {
			return err
		}
	}
	// An undefined <task> and a <manualTask> have no execution semantics, so Atlas
	// runs them as pass-throughs (the token flows straight on). This lets a model
	// be drafted and its routing — e.g. a gateway's branches — tested before its
	// tasks are given real implementations.
	for _, t := range c.Tasks {
		if err := register(t.Id, b.AddTask()); err != nil {
			return err
		}
	}
	for _, t := range c.ManualTasks {
		if err := register(t.Id, b.AddTask()); err != nil {
			return err
		}
	}
	// A receive task waits for its referenced message to correlate, then continues — the
	// message-catch semantics as an activity (ADR-0102). An empty or unknown messageRef is a
	// deploy error, exactly like a message catch event.
	for _, rt := range c.ReceiveTasks {
		name, keyExpr, err := resolveMessage(rt.Id, rt.MessageRef)
		if err != nil {
			return err
		}
		if err := register(rt.Id, b.AddReceiveTask(name, keyExpr)); err != nil {
			return err
		}
	}
	for _, e := range c.EndEvents {
		// A terminate end event ends its enclosing flow scope at once (ADR-0116): it
		// terminates every other live token in the scope, then completes the scope — at the
		// root the instance ends, inside a subprocess that subprocess ends and the parent
		// continues. It carries no detail.
		if e.Terminate != nil {
			if err := register(e.Id, b.AddTerminateEndEvent()); err != nil {
				return err
			}
			continue
		}
		// A message end event publishes its message then ends; a plain end event
		// just ends (ADR-0052).
		if e.Message != nil {
			name, keyExpr, err := resolveMessage(e.Id, e.Message.MessageRef)
			if err != nil {
				return err
			}
			if err := register(e.Id, b.AddMessageEndEvent(name, keyExpr)); err != nil {
				return err
			}
			continue
		}
		if e.Signal != nil {
			name, err := resolveSignal(e.Id, e.Signal.SignalRef)
			if err != nil {
				return err
			}
			if err := register(e.Id, b.AddSignalEndEvent(name)); err != nil {
				return err
			}
			continue
		}
		// An error end event throws its error code, ending its scope abnormally and
		// propagating up to the nearest matching handler (ADR-0089).
		if e.Error != nil {
			code, err := resolveError(e.Id, e.Error.ErrorRef)
			if err != nil {
				return err
			}
			if err := register(e.Id, b.AddErrorEndEvent(code)); err != nil {
				return err
			}
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
			if err := register(e.Id, b.AddEscalationEndEvent(code)); err != nil {
				return err
			}
			continue
		}
		// A compensation end event triggers compensation, then ends its scope (ADR-0103);
		// its optional activityRef is resolved in the post-pass, like a compensation throw.
		if e.Compensation != nil {
			if err := register(e.Id, b.AddCompensationEndEvent()); err != nil {
				return err
			}
			continue
		}
		// A cancel end event cancels its enclosing transaction (ADR-0108); validation
		// (checkTransactions) enforces that the enclosing scope really is a transaction.
		if e.Cancel != nil {
			if err := register(e.Id, b.AddCancelEndEvent()); err != nil {
				return err
			}
			continue
		}
		if err := register(e.Id, b.AddEndEvent()); err != nil {
			return err
		}
	}
	// Nested subprocesses: register the container node, then push its scope so its
	// own flow nodes (registered by recursion) carry it as their FlowScope. The
	// container is created before its children so it has an ElementId to scope them
	// to (ADR-0074).
	for i := range c.SubProcesses {
		sub := &c.SubProcesses[i]
		subID := b.AddSubProcess()
		if err := register(sub.Id, subID); err != nil {
			return err
		}
		// A <transaction> is a subprocess with cancellation added; mark the node so the
		// runtime and validation treat it as one (ADR-0108). It is never triggeredByEvent.
		if sub.IsTransaction {
			b.SetTransaction(subID)
		}
		b.PushScope(subID)
		if err := registerScope(b, ids, register, resolveMessage, resolveSignal, resolveError, resolveEscalation, &sub.xmlFlowContent); err != nil {
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
				return fmt.Errorf("compiler: event subprocess %q must have a start event with a message, timer, signal, error, or escalation event definition", sub.Id)
			}
			d := EventSubProcessDetail{StartNode: ids[st.Id], Interrupting: st.IsInterrupting != "false"}
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
		host, ok := ids[ev.AttachedToRef]
		if !ok {
			return fmt.Errorf("compiler: boundary event %q attaches to unknown activity %q", ev.Id, ev.AttachedToRef)
		}
		if messageSendIDs[ev.AttachedToRef] {
			return fmt.Errorf("compiler: boundary event %q attaches to send task %q, but a message-kind send task is an instantaneous throw (it publishes the message and continues) and cannot host a boundary event; switch the send task to a job or connector kind, which waits so a boundary can fire, or model a wait-and-time-out with a receive task and a boundary timer", ev.Id, ev.AttachedToRef)
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
			if err := register(ev.Id, b.AddBoundaryTimerSchedule(host, interrupting, schedule)); err != nil {
				return err
			}
		case ev.Message != nil:
			name, keyExpr, err := resolveMessage(ev.Id, ev.Message.MessageRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddBoundaryMessageEvent(host, interrupting, name, keyExpr)); err != nil {
				return err
			}
		case ev.Signal != nil:
			name, err := resolveSignal(ev.Id, ev.Signal.SignalRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddBoundarySignalEvent(host, interrupting, name)); err != nil {
				return err
			}
		case ev.Error != nil:
			// An error boundary catches an error propagating up to the host by code and is
			// always interrupting — cancelActivity is ignored (ADR-0089).
			code, err := resolveError(ev.Id, ev.Error.ErrorRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddBoundaryErrorEvent(host, code)); err != nil {
				return err
			}
		case ev.Escalation != nil:
			// An escalation boundary catches an escalation propagating up to the host by code
			// (ADR-0125). Unlike an error boundary it honors cancelActivity — an interrupting
			// escalation boundary tears the host down, a non-interrupting one runs the handler
			// alongside the still-running host.
			code, err := resolveEscalation(ev.Id, ev.Escalation.EscalationRef)
			if err != nil {
				return err
			}
			if err := register(ev.Id, b.AddBoundaryEscalationEvent(host, code, interrupting)); err != nil {
				return err
			}
		case ev.Compensation != nil:
			// A compensation boundary is inert: it never arms, it only marks its host
			// compensable and links to a handler (resolved from a BPMN <association> in the
			// post-pass). cancelActivity is ignored — a compensation boundary never
			// interrupts (ADR-0103).
			if err := register(ev.Id, b.AddBoundaryCompensationEvent(host)); err != nil {
				return err
			}
		case ev.Cancel != nil:
			// A cancel boundary catches its host transaction's cancellation and is always
			// interrupting — cancelActivity is ignored (ADR-0108). validation (checkTransactions)
			// enforces that the host really is a transaction.
			if err := register(ev.Id, b.AddBoundaryCancelEvent(host)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("compiler: boundary event %q: only timer, message, signal, error, escalation, compensation, and cancel boundary events are supported yet", ev.Id)
		}
	}
	// Report an unsupported element with a clear message rather than letting it
	// surface later as a confusing "unknown targetRef" when a flow points at it.
	for _, u := range []struct {
		label string
		nodes []xmlNode
	}{
		{"adHocSubProcess", c.AdHocSubProcesses},
	} {
		if len(u.nodes) > 0 {
			return fmt.Errorf("compiler: element %q is a <%s>, which Atlas can't execute yet "+
				"(supported: start/end events, tasks (undefined/manual pass-through, service, script, "+
				"business rule, user), embedded subprocesses, exclusive/parallel/inclusive gateways, and timer/message intermediate events)", u.nodes[0].Id, u.label)
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
	// Resolve link events within this scope (ADR-0131): a link throw is a goto to the link
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
	return nil
}

// clioOperation normalizes a clio connector task's operation attribute, defaulting
// an empty value to "write" so the original write-only <atlas:clioConnector>
// element (which carried no operation) keeps compiling unchanged.
func clioOperation(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		return "write"
	}
	return op
}

// clioLimit parses a clio read task's limit attribute: empty is 0 (the connector's
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
