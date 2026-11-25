package conformance

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Inclusive-join oracles for registry row FLOW-05. OMG BPMN 2.0.2 Table 13.3 states the
// join's activation as a condition on the tokens in the diagram: it fires once no token
// can reach an incoming flow that has none. Until ADR-0551 part 1 atlas checked that rule
// only when a token arrived at the join, so a join whose remaining branch ended elsewhere
// waited for an arrival that never came and the instance never completed.

// con2JoinModel builds start → inclusive split → a (script, reaches the join) and b (a
// service task) → exclusive "route" whose default goes to end event "elsewhere"; the
// conditional flow from route to the join is false. bExtra is spliced in after b's
// declaration, for a boundary on b; extra adds elements and flows.
func con2JoinModel(decls, bExtra, joinOut, extra string) []byte {
	S, F, W := con2Script, con2Flow, con2Wait
	if joinOut == "" {
		joinOut = F("f5", "join", "after")
	}
	return con2Defs(decls, `<startEvent id="s"/><inclusiveGateway id="split"/>`+S("a")+W("b")+bExtra+
		`<exclusiveGateway id="route" default="toEnd"/><endEvent id="elsewhere"/>`+
		`<inclusiveGateway id="join"/>`+S("after")+`<endEvent id="end"/>`+
		F("f0", "s", "split")+F("f1", "split", "a")+F("f2", "split", "b")+
		F("f3", "a", "join")+F("f4", "b", "route")+F("toEnd", "route", "elsewhere")+
		`<sequenceFlow id="toJoin" sourceRef="route" targetRef="join"><conditionExpression>= false</conditionExpression></sequenceFlow>`+
		joinOut+F("f6", "after", "end")+extra)
}

// TestCON2InclusiveJoinFiresWhenItsUpstreamTokenEndsElsewhere: branch a parks at the
// join while b is at its task; b then ends at "elsewhere". No token can reach the join
// any more, so it fires and the instance completes.
func TestCON2InclusiveJoinFiresWhenItsUpstreamTokenEndsElsewhere(t *testing.T) {
	got := con2Run(t, con2JoinModel("", "", "", ""), Complete("b"))
	if got.State != model.PICompleted {
		t.Fatalf("state %v, want completed; path=%v", got.State, got.Path)
	}
	con2Expect(t, "FLOW-05 §13.4", got, map[string]int{
		"join": 1, "elsewhere": 1, "after": 1, "end": 1, "toJoin": 0,
	})
}

// TestCON2InclusiveJoinFiresWhenItsUpstreamTokenIsInterrupted: b never completes; an
// interrupting boundary timer terminates it and routes to an end event. The terminated
// token can no longer reach the join, so the join fires.
func TestCON2InclusiveJoinFiresWhenItsUpstreamTokenIsInterrupted(t *testing.T) {
	boundary := `<boundaryEvent id="timeout" attachedToRef="b"><timerEventDefinition>` +
		`<timeDuration>PT30S</timeDuration></timerEventDefinition></boundaryEvent><endEvent id="timedOut"/>`
	xml := con2JoinModel("", boundary, "", con2Flow("f7", "timeout", "timedOut"))
	got := con2Run(t, xml, Wait(31*time.Second))
	if got.State != model.PICompleted {
		t.Fatalf("state %v, want completed; path=%v", got.State, got.Path)
	}
	con2Expect(t, "FLOW-05 §13.4", got, map[string]int{
		"timeout": 1, "timedOut": 1, "join": 1, "after": 1, "end": 1, "route": 0,
	})
}

// TestCON2InclusiveJoinRecheckRaisesNoSecondIncident: a join that cannot route parks on
// an incident (ADR-0273). A later token leaving the instance re-checks the join, and the
// re-check must leave the incident as it is rather than raise another one.
func TestCON2InclusiveJoinRecheckRaisesNoSecondIncident(t *testing.T) {
	// a parks at the join; b can still reach it through route, so the join waits. When b
	// ends elsewhere the re-check fires the join, whose only outgoing flow is false: an
	// incident. c then leaves too, which re-checks the join again.
	noRoute := `<sequenceFlow id="f5" sourceRef="join" targetRef="after"><conditionExpression>= false</conditionExpression></sequenceFlow>`
	extra := con2Wait("c") + `<endEvent id="endC"/>` + con2Flow("f8", "split", "c") + con2Flow("f9", "c", "endC")
	xml := con2JoinModel("", "", noRoute, extra)
	steps := []Step{Complete("b"), Complete("c")}
	got := con2Run(t, xml, steps...)
	if got.State != model.PIActive {
		t.Fatalf("state %v, want active (parked on the routing incident)", got.State)
	}
	// The incident is raised when b leaves. c leaving afterwards re-checks the join; a
	// re-check that raised again would overwrite the incident with a later RaisedAt, so the
	// timestamp after both steps must be the one after the first.
	raised := func(steps []Step) int64 {
		var at int64
		con2Inspect(t, xml, steps, func(store *state.Store, cp *compiler.CompiledProcess) {
			var on []string
			if err := store.Incidents(func(_ uint64, incident *model.IncidentValue) error {
				on = append(on, cp.ElementBpmnId(incident.ElementId))
				at = incident.RaisedAt
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(on) != 1 || on[0] != "join" {
				t.Fatalf("incidents on %v, want exactly one on join", on)
			}
		})
		return at
	}
	if first, after := raised(steps[:1]), raised(steps); first != after {
		t.Fatalf("the re-check raised the incident again: RaisedAt %d after b, %d after c", first, after)
	}
}

// TestCON3InclusiveJoinRecoversWithABranchParked restarts at the interesting moment: a
// parked at the join, b waiting at its task. The recovered store must hold the same
// durable state, and completing b on the recovered engine must let the join fire, which
// shows the re-check is driven by live facts after a restart, not by memory of the run.
func TestCON3InclusiveJoinRecoversWithABranchParked(t *testing.T) {
	xml := con2JoinModel("", "", "", "")
	defs, err := compiler.ParseAll(defKey, 1, bytes.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	root, err := pickRoot(defs, "")
	if err != nil {
		t.Fatal(err)
	}
	liveDir := filepath.Join(t.TempDir(), "live")
	log, store, err := openStores(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	clock := &driverClock{}
	p := engine.New(1, log, store, clock)
	deployAll(p, defs, root)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	d := &driver{p: p, store: store, cp: root, clock: clock, deployables: defs}
	if err := d.begin(Start{}); err != nil {
		t.Fatal(err)
	}
	waiting, err := con3CaptureWait(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting.jobs) != 1 {
		t.Fatalf("checkpoint: want b's job pending, got %+v", waiting)
	}
	closeStores(log, store)

	replayLog, err := wal.Open(wal.Options{Dir: filepath.Join(liveDir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := state.Open(filepath.Join(t.TempDir(), "replayed"))
	if err != nil {
		_ = replayLog.Close()
		t.Fatal(err)
	}
	defer closeStores(replayLog, recovered)
	restartClock := &driverClock{t: clock.t}
	restarted := engine.New(1, replayLog, recovered, restartClock)
	deployAll(restarted, defs, root)
	if err := restarted.Recover(); err != nil {
		t.Fatal(err)
	}
	after, err := con3CaptureWait(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, waiting) {
		t.Fatalf("replay diverged\nlive=%+v\nreplay=%+v", waiting, after)
	}
	continued := &driver{p: restarted, store: recovered, cp: root, clock: restartClock, deployables: defs}
	if err := continued.apply(Complete("b")); err != nil {
		t.Fatal(err)
	}
	got, err := capture(recovered, root)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.PICompleted {
		t.Fatalf("after restart: state %v, want completed; path=%v", got.State, got.Path)
	}
	con2Expect(t, "FLOW-05 restart", got, map[string]int{"join": 1, "elsewhere": 1, "after": 1, "end": 1})
}
