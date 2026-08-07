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
		if s := &sub.StartEvents[i]; s.Message != nil || s.Timer != nil || s.Signal != nil || s.Error != nil {
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
	for _, st := range c.ServiceTasks {
		retries := int32(defaultRetries)
		if r := st.TaskDefinition.Retries; r != "" {
			n, err := strconv.Atoi(r)
			if err != nil {
				return fmt.Errorf("compiler: service task %q has invalid retries %q: %w", st.Id, r, err)
			}
			retries = int32(n)
		}
		// A service task bearing an <atlas:clioConnector> extension is a connector
		// task: it delegates to a server-registered clio connector via the job path
		// (ADR-0036), not to an external service-task worker. operation selects the
		// clio call (write/query/read); write is the default for back-compatibility
		// with the original write-only element.
		if cn := st.Clio; cn != nil {
			if cn.Connector == "" {
				return fmt.Errorf("compiler: clio connector task %q needs a connector", st.Id)
			}
			var id int32
			switch op := clioOperation(cn.Operation); op {
			case "write":
				if cn.Subject == "" || cn.EventType == "" {
					return fmt.Errorf("compiler: clio write task %q needs subject and eventType", st.Id)
				}
				id = b.AddClioWriteTask(cn.Connector, cn.Subject, cn.EventType, retries)
			case "query":
				if cn.Query == "" && cn.Subject == "" {
					return fmt.Errorf("compiler: clio query task %q needs a query or a subject", st.Id)
				}
				if strings.TrimSpace(cn.ResultVariable) == "" {
					return fmt.Errorf("compiler: clio query task %q needs a resultVariable", st.Id)
				}
				id = b.AddClioQueryTask(cn.Connector, cn.Subject, cn.ReduceSpec, cn.Query, strings.TrimSpace(cn.ResultVariable), retries)
			case "read":
				if cn.Subject == "" {
					return fmt.Errorf("compiler: clio read task %q needs a subject", st.Id)
				}
				if strings.TrimSpace(cn.ResultVariable) == "" {
					return fmt.Errorf("compiler: clio read task %q needs a resultVariable", st.Id)
				}
				limit, err := clioLimit(st.Id, cn.Limit)
				if err != nil {
					return err
				}
				id = b.AddClioReadTask(cn.Connector, cn.Subject, strings.TrimSpace(cn.ResultVariable), limit, retries)
			default:
				return fmt.Errorf("compiler: clio connector task %q has unknown operation %q (want write, query, or read)", st.Id, op)
			}
			if err := register(st.Id, id); err != nil {
				return err
			}
			continue
		}
		// A service task bearing an <atlas:restConnector> extension is an HTTP-REST
		// connector task: it calls the model-authored URL via the job path
		// (ADR-0067), not an external service-task worker. The URL lives in the model
		// (unlike clio's registry-only endpoint); credentials never do.
		if cn := st.Rest; cn != nil {
			if strings.TrimSpace(cn.Url) == "" {
				return fmt.Errorf("compiler: rest connector task %q needs a url", st.Id)
			}
			method, err := normalizeHTTPMethod(cn.Method)
			if err != nil {
				return fmt.Errorf("compiler: rest connector task %q: %w", st.Id, err)
			}
			url, err := restValue(st.Id, "url", cn.Url)
			if err != nil {
				return err
			}
			headers, err := httpKVList(st.Id, "header", cn.Headers)
			if err != nil {
				return err
			}
			query, err := httpKVList(st.Id, "query parameter", cn.QueryParams)
			if err != nil {
				return err
			}
			auth, err := restAuth(st.Id, cn)
			if err != nil {
				return err
			}
			id := b.AddRestConnectorTask(RestConfig{
				Method:    method,
				Url:       url,
				ResultVar: strings.TrimSpace(cn.ResultVariable),
				Headers:   headers,
				Query:     query,
				Auth:      auth,
				Retries:   retries,
			})
			if err := register(st.Id, id); err != nil {
				return err
			}
			continue
		}
		// A service task bearing an <atlas:mailConnector> extension is an outbound
		// mail connector task: it sends a model-authored message through a
		// server-registered mail provider via the job path (ADR-0079). The provider
		// (host, credentials) is resolved server-side by connector name, like clio;
		// only the message (recipients, subject, body) lives in the model.
		if cn := st.Mail; cn != nil {
			if strings.TrimSpace(cn.Connector) == "" {
				return fmt.Errorf("compiler: mail connector task %q needs a connector", st.Id)
			}
			if strings.TrimSpace(cn.To) == "" {
				return fmt.Errorf("compiler: mail connector task %q needs a to recipient", st.Id)
			}
			to, err := restValue(st.Id, "to", cn.To)
			if err != nil {
				return err
			}
			cc, err := restValue(st.Id, "cc", cn.Cc)
			if err != nil {
				return err
			}
			bcc, err := restValue(st.Id, "bcc", cn.Bcc)
			if err != nil {
				return err
			}
			from, err := restValue(st.Id, "from", cn.From)
			if err != nil {
				return err
			}
			subject, err := restValue(st.Id, "subject", cn.Subject)
			if err != nil {
				return err
			}
			body, err := restValue(st.Id, "body", cn.Body)
			if err != nil {
				return err
			}
			id := b.AddMailConnectorTask(MailConfig{
				Connector: strings.TrimSpace(cn.Connector),
				To:        to,
				Cc:        cc,
				Bcc:       bcc,
				From:      from,
				Subject:   subject,
				Body:      body,
				Retries:   retries,
			})
			if err := register(st.Id, id); err != nil {
				return err
			}
			continue
		}
		if st.TaskDefinition.Type == "" {
			return fmt.Errorf("compiler: service task %q has no task definition type", st.Id)
		}
		if err := register(st.Id, b.AddServiceTask(st.TaskDefinition.Type, retries)); err != nil {
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
		default:
			return fmt.Errorf("compiler: intermediate catch event %q: only timer, message, and signal events are supported yet", ev.Id)
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
		if ev.Message == nil {
			return fmt.Errorf("compiler: intermediate throw event %q: only message and signal events are supported yet", ev.Id)
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
	for _, e := range c.EndEvents {
		// A terminate end event would end the whole instance at once — Atlas can't
		// execute that yet, so reject it with a clear message instead of silently
		// dropping the <terminateEventDefinition> and deploying a plain end that
		// simply completes one token (the modeled abort would never happen).
		if e.Terminate != nil {
			return fmt.Errorf("compiler: end event %q has a <terminateEventDefinition>, "+
				"which Atlas can't execute yet — a terminate end would abort the whole instance; "+
				"remove it or use a plain end event", e.Id)
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
		b.PushScope(subID)
		if err := registerScope(b, ids, register, resolveMessage, resolveSignal, resolveError, &sub.xmlFlowContent); err != nil {
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
				return fmt.Errorf("compiler: event subprocess %q must have a start event with a message, timer, signal, or error event definition", sub.Id)
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
		default:
			return fmt.Errorf("compiler: boundary event %q: only timer, message, signal, and error boundary events are supported yet", ev.Id)
		}
	}
	// Report an unsupported element with a clear message rather than letting it
	// surface later as a confusing "unknown targetRef" when a flow points at it.
	for _, u := range []struct {
		label string
		nodes []xmlNode
	}{
		{"sendTask", c.SendTasks}, {"receiveTask", c.ReceiveTasks},
	} {
		if len(u.nodes) > 0 {
			return fmt.Errorf("compiler: element %q is a <%s>, which Atlas can't execute yet "+
				"(supported: start/end events, tasks (undefined/manual pass-through, service, script, "+
				"business rule, user), embedded subprocesses, exclusive/parallel/inclusive gateways, and timer/message intermediate events)", u.nodes[0].Id, u.label)
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
