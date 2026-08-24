package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestExclusiveGatewayReadsSubprocessScope proves an exclusive gateway inside an
// embedded subprocess branches on a variable produced within that subprocess
// (ADR-0085). A script task writes `localFlag=true` into the subprocess scope; the
// gateway's condition `=localFlag` then routes to the subprocess end (completing
// the instance) rather than falling to its default (a parking service task).
//
// Before gateway conditions resolved over the scope chain, the condition read the
// process root — where `localFlag` does not live — so it saw null, took the
// default, and the token parked. The assertion (instance completes, no parked job)
// is the observable proof the gateway saw the subprocess-local variable.
func TestExclusiveGatewayReadsSubprocessScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "gw-scope", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	istart := b.AddStartEvent()
	setFlag := b.AddScriptTask(mustCompile(t, "true"), "localFlag") // writes into the subprocess scope
	gw := b.AddExclusiveGateway()
	park := b.AddServiceTask("gwscope.park", 3) // no worker registered → a token parks here
	iend := b.AddEndEvent()
	b.Connect(istart, setFlag)
	b.Connect(setFlag, gw)
	toEnd := b.Connect(gw, iend)
	b.SetFlowCondition(toEnd, mustCompile(t, "localFlag"))
	toPark := b.Connect(gw, park)
	b.SetFlowDefault(toPark)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	parkType := cp.ServiceTask(cp.Node(park).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if jobs := activatableJobs(t, h.store, parkType); len(jobs) != 0 {
		t.Fatalf("parked jobs = %d, want 0 (gateway should have read localFlag=true and taken the end branch)", len(jobs))
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("process=%d element=%d, want 0 and 0 (instance completed via the flag branch)", pi, ei)
	}
}
