package conformance

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// CON2 execution oracles for registry rows that were marked implemented but had no
// runnable evidence. Each model is inline and minimal so the expected outcome is
// stated in this file, next to the assertion, rather than in a golden file that
// -update could rewrite. Every run goes through Run, so live/replay equivalence
// (I4) and the no-orphan-token check after an ended instance hold for each case.
//
// These tests are evidence for the specific variant they name. They are not a
// statement-by-statement OMG BPMN 2.0.2 assessment; clause references are the
// provisional anchors from conformance/registry/registry.json and remain subject
// to the CON0 reviewer gate.

// con2Defs wraps a process body in a definitions element with the namespaces the
// inline fixtures use. Extra root elements (error, escalation, signal, message)
// go in decls.
func con2Defs(decls, process string) []byte {
	return []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"` +
		` xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs" targetNamespace="http://atlas/conformance">` +
		decls + `<process id="p" isExecutable="true">` + process + `</process></definitions>`)
}

// con2Script is an inline FEEL script task, so a branch advances without a worker.
func con2Script(id string) string {
	return `<scriptTask id="` + id + `"><extensionElements><zeebe:script expression="= 1" resultVariable="` +
		id + `"/></extensionElements></scriptTask>`
}

// con2Wait is a service task whose job nobody completes: a branch that is still
// active when something else in its scope ends it.
func con2Wait(id string) string {
	return `<serviceTask id="` + id + `"><extensionElements><zeebe:taskDefinition type="` + id +
		`"/></extensionElements></serviceTask>`
}

func con2Flow(id, from, to string) string {
	return `<sequenceFlow id="` + id + `" sourceRef="` + from + `" targetRef="` + to + `"/>`
}

// con2Visits counts how often each element id appears on the token path.
func con2Visits(path []string) map[string]int {
	visits := make(map[string]int, len(path))
	for _, id := range path {
		visits[id]++
	}
	return visits
}

// con2Expect asserts the exact visit count of each listed element; a zero means
// the element must not have been reached at all.
func con2Expect(t *testing.T, row string, got RunResult, want map[string]int) {
	t.Helper()
	visits := con2Visits(got.Path)
	for id, n := range want {
		if visits[id] != n {
			t.Errorf("%s: %q visited %d time(s), want %d; path=%v", row, id, visits[id], n, got.Path)
		}
	}
}

// con2Run executes the model through the conformance runner (live, replay, orphan
// check) and fails the test on any runner error.
func con2Run(t *testing.T, xml []byte, steps ...Step) RunResult {
	t.Helper()
	got, err := Run(t.TempDir(), xml, "", Start{}, steps)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// con2Inspect re-executes the model live and hands the open store to inspect
// before closing it. It exists for assertions the RunResult projection does not
// carry (incidents, remaining jobs). The run is deterministic, so it observes the
// same facts Run replayed.
func con2Inspect(t *testing.T, xml []byte, steps []Step, inspect func(*state.Store, *compiler.CompiledProcess)) {
	t.Helper()
	defs, err := compiler.ParseAll(defKey, 1, bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	root, err := pickRoot(defs, "")
	if err != nil {
		t.Fatal(err)
	}
	log, store, err := openStores(filepath.Join(t.TempDir(), "inspect"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeStores(log, store)
	clock := &driverClock{}
	p := engine.New(1, log, store, clock)
	deployAll(p, defs, root)
	if err := p.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	d := &driver{p: p, store: store, cp: root, clock: clock, deployables: defs}
	if err := d.begin(Start{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, step := range steps {
		if err := d.apply(step); err != nil {
			t.Fatalf("step %s: %v", step.describe(), err)
		}
	}
	inspect(store, root)
}

// con2ActiveJobs counts the activatable jobs left in the store.
func con2ActiveJobs(t *testing.T, store *state.Store) int {
	t.Helper()
	n := 0
	if err := store.AllActivatableJobs(func(uint64) error { n++; return nil }); err != nil {
		t.Fatalf("scan jobs: %v", err)
	}
	return n
}

// TestCON2ExclusiveGatewayDefaultAndNoRoute pins registry row FLOW-13. With no
// condition true, an exclusive gateway takes its default flow; without a default
// flow, no outgoing flow may be taken at all. OMG BPMN 2.0.2 treats the "no flow
// and no default" case as a runtime exception (exact clause wording to be confirmed
// in the CON0 per-clause review); atlas realises it as an incident on the gateway
// that keeps the token parked (ADR-0273), a documented realisation choice. The oracle checks that nothing
// downstream runs and that the incident names the gateway, not only that the
// instance did not complete.
func TestCON2ExclusiveGatewayDefaultAndNoRoute(t *testing.T) {
	gateway := func(withDefault bool) []byte {
		def := ""
		fallback := ""
		if withDefault {
			def = ` default="f_default"`
			fallback = `<endEvent id="fallback"/>` + con2Flow("f_default", "gw", "fallback")
		}
		return con2Defs("", `<startEvent id="s"/>`+con2Script("x")+
			`<exclusiveGateway id="gw"`+def+`/><endEvent id="chosen"/>`+fallback+
			con2Flow("f1", "s", "x")+con2Flow("f2", "x", "gw")+
			`<sequenceFlow id="f_chosen" sourceRef="gw" targetRef="chosen"><conditionExpression>= x = 2</conditionExpression></sequenceFlow>`)
	}

	t.Run("default-flow", func(t *testing.T) {
		got := con2Run(t, gateway(true))
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed", got.State)
		}
		con2Expect(t, "FLOW-13 §13.4", got, map[string]int{"gw": 1, "fallback": 1, "chosen": 0})
	})

	t.Run("no-route-without-default", func(t *testing.T) {
		xml := gateway(false)
		got := con2Run(t, xml)
		if got.State != model.PIActive {
			t.Fatalf("state %v, want active (token held at the unroutable gateway)", got.State)
		}
		con2Expect(t, "FLOW-13 §13.4", got, map[string]int{"gw": 1, "chosen": 0})
		con2Inspect(t, xml, nil, func(store *state.Store, cp *compiler.CompiledProcess) {
			var on []string
			if err := store.Incidents(func(_ uint64, incident *model.IncidentValue) error {
				on = append(on, cp.ElementBpmnId(incident.ElementId)+": "+incident.Message)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(on) != 1 || !strings.HasPrefix(on[0], "gw: ") || !strings.Contains(on[0], "no default flow") {
				t.Fatalf("incidents = %q, want exactly one on gw naming the missing default flow", on)
			}
		})
	})
}

// TestCON2TerminateEndConsumesEveryTokenOfItsScope pins registry row EVT-38 as an
// execution case (it was only compile-tested). A terminate end ends every other
// active token of its enclosing scope (ADR-0116 also runs no compensation; that is
// not asserted here). At the root
// that ends the instance; inside an embedded subprocess it ends only that
// subprocess, whose parent continues normally. The terminal-state label of the
// root case (completed, not terminated) is atlas's ADR-0116 decision and is
// asserted as such; the normative claim is the consumption of all tokens.
func TestCON2TerminateEndConsumesEveryTokenOfItsScope(t *testing.T) {
	branches := `<parallelGateway id="fork"/>` + con2Wait("wait") + `<endEvent id="end_a"/>` +
		con2Script("b") + `<endEvent id="term"><terminateEventDefinition/></endEvent>` +
		con2Flow("t1", "fork", "wait") + con2Flow("t2", "wait", "end_a") +
		con2Flow("t3", "fork", "b") + con2Flow("t4", "b", "term")

	t.Run("process-root", func(t *testing.T) {
		xml := con2Defs("", `<startEvent id="s"/>`+branches+con2Flow("f1", "s", "fork"))
		got := con2Run(t, xml)
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed (ADR-0116)", got.State)
		}
		con2Expect(t, "EVT-38 §13.5.4", got, map[string]int{"wait": 1, "term": 1, "end_a": 0})
		con2Inspect(t, xml, nil, func(store *state.Store, _ *compiler.CompiledProcess) {
			if n := con2ActiveJobs(t, store); n != 0 {
				t.Fatalf("%d job(s) of the terminated branch still activatable", n)
			}
		})
	})

	t.Run("embedded-subprocess", func(t *testing.T) {
		xml := con2Defs("", `<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/>`+branches+
			con2Flow("i1", "ss", "fork")+`</subProcess>`+con2Script("after")+`<endEvent id="end"/>`+
			con2Flow("f1", "s", "sub")+con2Flow("f2", "sub", "after")+con2Flow("f3", "after", "end"))
		got := con2Run(t, xml)
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed", got.State)
		}
		con2Expect(t, "EVT-38 §13.5.4", got, map[string]int{
			"wait": 1, "term": 1, "end_a": 0, "sub": 1, "after": 1, "end": 1,
		})
	})
}

// TestCON2LinkEventsTransferControlWithinScope pins registry rows EVT-12 and
// EVT-19: a link throw continues at the link catch of the same name in the same
// scope; no element between them runs and the throw has no outgoing flow of its own.
func TestCON2LinkEventsTransferControlWithinScope(t *testing.T) {
	xml := con2Defs("", `<startEvent id="s"/>`+con2Script("before")+
		`<intermediateThrowEvent id="go"><linkEventDefinition name="L"/></intermediateThrowEvent>`+
		`<intermediateCatchEvent id="land"><linkEventDefinition name="L"/></intermediateCatchEvent>`+
		`<intermediateCatchEvent id="other"><linkEventDefinition name="M"/></intermediateCatchEvent>`+
		con2Script("after")+con2Script("unused")+`<endEvent id="end"/><endEvent id="end_unused"/>`+
		con2Flow("f1", "s", "before")+con2Flow("f2", "before", "go")+
		con2Flow("f3", "land", "after")+con2Flow("f4", "after", "end")+
		con2Flow("f5", "other", "unused")+con2Flow("f6", "unused", "end_unused"))
	got := con2Run(t, xml)
	if got.State != model.PICompleted {
		t.Fatalf("state %v, want completed", got.State)
	}
	con2Expect(t, "EVT-12/EVT-19 §13.5", got, map[string]int{
		"before": 1, "go": 1, "land": 1, "after": 1, "end": 1, "other": 0, "unused": 0,
	})
}

// TestCON2ErrorEndInterruptsEnclosingScope pins registry row EVT-35. An error end
// inside a subprocess ends that subprocess abnormally: a concurrently waiting
// sibling inside it is ended, its normal outgoing flow is not taken, and the
// matching boundary error on the subprocess routes the token.
func TestCON2ErrorEndInterruptsEnclosingScope(t *testing.T) {
	xml := con2Defs(`<error id="Err" errorCode="E1"/>`,
		`<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/><parallelGateway id="fork"/>`+
			con2Wait("wait")+`<endEvent id="inner_end"/>`+con2Script("b")+
			`<endEvent id="raise"><errorEventDefinition errorRef="Err"/></endEvent>`+
			con2Flow("i1", "ss", "fork")+con2Flow("i2", "fork", "wait")+con2Flow("i3", "wait", "inner_end")+
			con2Flow("i4", "fork", "b")+con2Flow("i5", "b", "raise")+`</subProcess>`+
			`<boundaryEvent id="on_error" attachedToRef="sub"><errorEventDefinition errorRef="Err"/></boundaryEvent>`+
			`<endEvent id="done"/><endEvent id="handled"/>`+
			con2Flow("f1", "s", "sub")+con2Flow("f2", "sub", "done")+con2Flow("f3", "on_error", "handled"))
	got := con2Run(t, xml)
	if got.State != model.PICompleted {
		t.Fatalf("state %v, want completed", got.State)
	}
	con2Expect(t, "EVT-35 §13.5", got, map[string]int{
		"wait": 1, "raise": 1, "inner_end": 0, "on_error": 1, "handled": 1, "done": 0,
	})
	con2Inspect(t, xml, nil, func(store *state.Store, _ *compiler.CompiledProcess) {
		if n := con2ActiveJobs(t, store); n != 0 {
			t.Fatalf("%d job(s) of the interrupted subprocess still activatable", n)
		}
	})
}

// TestCON2EscalationReachesEnclosingBoundary pins registry rows EVT-17, EVT-26 and
// EVT-36 as execution cases. An escalation end caught by an interrupting boundary
// leaves the subprocess through the boundary only. An intermediate escalation throw
// caught by a non-interrupting boundary spawns the boundary path once and lets the
// subprocess continue and complete normally.
func TestCON2EscalationReachesEnclosingBoundary(t *testing.T) {
	const esc = `<escalation id="Esc" escalationCode="LATE"/>`
	t.Run("end-interrupting", func(t *testing.T) {
		xml := con2Defs(esc, `<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/>`+
			`<endEvent id="raise"><escalationEventDefinition escalationRef="Esc"/></endEvent>`+
			con2Flow("i1", "ss", "raise")+`</subProcess>`+
			`<boundaryEvent id="on_esc" attachedToRef="sub"><escalationEventDefinition escalationRef="Esc"/></boundaryEvent>`+
			`<endEvent id="done"/><endEvent id="escalated"/>`+
			con2Flow("f1", "s", "sub")+con2Flow("f2", "sub", "done")+con2Flow("f3", "on_esc", "escalated"))
		got := con2Run(t, xml)
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed", got.State)
		}
		con2Expect(t, "EVT-26/EVT-36 §13.5", got, map[string]int{"raise": 1, "on_esc": 1, "escalated": 1, "done": 0})
	})

	t.Run("throw-noninterrupting", func(t *testing.T) {
		xml := con2Defs(esc, `<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/>`+
			`<intermediateThrowEvent id="raise"><escalationEventDefinition escalationRef="Esc"/></intermediateThrowEvent>`+
			con2Script("cont")+`<endEvent id="se"/>`+
			con2Flow("i1", "ss", "raise")+con2Flow("i2", "raise", "cont")+con2Flow("i3", "cont", "se")+`</subProcess>`+
			`<boundaryEvent id="on_esc" attachedToRef="sub" cancelActivity="false"><escalationEventDefinition escalationRef="Esc"/></boundaryEvent>`+
			`<endEvent id="done"/><endEvent id="notified"/>`+
			con2Flow("f1", "s", "sub")+con2Flow("f2", "sub", "done")+con2Flow("f3", "on_esc", "notified"))
		got := con2Run(t, xml)
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed", got.State)
		}
		con2Expect(t, "EVT-17/EVT-26 §13.5", got, map[string]int{
			"raise": 1, "on_esc": 1, "notified": 1, "cont": 1, "se": 1, "done": 1,
		})
	})
}

// TestCON2SignalAndMessageThrowingEndsReachCatchers pins registry rows EVT-15,
// EVT-34 and EVT-37. A signal end broadcasts and a message end or intermediate
// message throw publishes before its path ends; a catch armed in a concurrent
// branch of the same instance receives it. A script hop on the throwing branch
// keeps the throw behind the catch's arming, as in the signal-throw-catch fixture.
func TestCON2SignalAndMessageThrowingEndsReachCatchers(t *testing.T) {
	const signal = `<signal id="Sig" name="ready"/>`
	const message = `<message id="Msg" name="ping"><extensionElements>` +
		`<zeebe:subscription correlationKey="= &quot;K&quot;"/></extensionElements></message>`
	concurrent := func(catch, throw string) string {
		return `<startEvent id="s"/><parallelGateway id="fork"/>` +
			`<intermediateCatchEvent id="await">` + catch + `</intermediateCatchEvent><endEvent id="end_a"/>` +
			con2Script("prep") + throw +
			con2Flow("f1", "s", "fork") + con2Flow("f2", "fork", "await") + con2Flow("f3", "await", "end_a") +
			con2Flow("f4", "fork", "prep") + con2Flow("f5", "prep", "raise")
	}
	cases := []struct {
		name, clause, decls, body string
		visits                    map[string]int
	}{
		{
			name: "signal-end", clause: "EVT-37 §13.5", decls: signal,
			body: concurrent(`<signalEventDefinition signalRef="Sig"/>`,
				`<endEvent id="raise"><signalEventDefinition signalRef="Sig"/></endEvent>`),
			visits: map[string]int{"raise": 1, "await": 1, "end_a": 1},
		},
		{
			name: "message-end", clause: "EVT-34 §13.5", decls: message,
			body: concurrent(`<messageEventDefinition messageRef="Msg"/>`,
				`<endEvent id="raise"><messageEventDefinition messageRef="Msg"/></endEvent>`),
			visits: map[string]int{"raise": 1, "await": 1, "end_a": 1},
		},
		{
			name: "message-intermediate-throw", clause: "EVT-15 §13.5", decls: message,
			body: concurrent(`<messageEventDefinition messageRef="Msg"/>`,
				`<intermediateThrowEvent id="raise"><messageEventDefinition messageRef="Msg"/></intermediateThrowEvent>`+
					`<endEvent id="end_b"/>`+con2Flow("f6", "raise", "end_b")),
			visits: map[string]int{"raise": 1, "await": 1, "end_a": 1, "end_b": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := con2Run(t, con2Defs(tc.decls, tc.body))
			if got.State != model.PICompleted {
				t.Fatalf("state %v, want completed: the catch did not receive the thrown trigger; path=%v",
					got.State, got.Path)
			}
			con2Expect(t, tc.clause, got, tc.visits)
		})
	}
}

// TestCON2UncontrolledFlowWithoutGateways pins registry rows FLOW-11 and FLOW-15.
// Several unconditional outgoing flows from an activity behave as a parallel split;
// several incoming flows into an activity without a gateway are uncontrolled, so the
// activity runs once per arriving token and nothing synchronizes them. The instance
// completes only when both resulting tokens have reached an end event.
func TestCON2UncontrolledFlowWithoutGateways(t *testing.T) {
	xml := con2Defs("", `<startEvent id="s"/>`+con2Script("split")+con2Script("a")+con2Script("b")+
		con2Script("merge")+`<endEvent id="end"/>`+
		con2Flow("f1", "s", "split")+con2Flow("f2", "split", "a")+con2Flow("f3", "split", "b")+
		con2Flow("f4", "a", "merge")+con2Flow("f5", "b", "merge")+con2Flow("f6", "merge", "end"))
	got := con2Run(t, xml)
	if got.State != model.PICompleted {
		t.Fatalf("state %v, want completed", got.State)
	}
	con2Expect(t, "FLOW-11/FLOW-15 §13.3.1", got, map[string]int{"split": 1, "a": 1, "b": 1, "merge": 2, "end": 2})
}
