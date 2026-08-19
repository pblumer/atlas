package engine_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestExampleBonitaetMockup runs the shipped examples/bonitaet-mockup.bpmn model end
// to end: it parses the file, deploys it, starts an instance, fires the mockup task's
// simulated-duration timer, and asserts the instance completes. The mockup writes its
// simulated Umsystem response on activation (so `bonitaet` is present regardless of the
// later success/failure draw), and every path — the two gateway branches and the error
// boundary — leads to an end, so the instance always finishes. This keeps the example a
// deterministic, runnable scenario, not just a static file.
func TestExampleBonitaetMockup(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	xml, err := os.ReadFile("../examples/bonitaet-mockup.bpmn")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	cp, err := compiler.Parse(1, 1, bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}

	// A fixed clock so the timer's due date is controllable; the mockup's random
	// duration stays within [PT1S, PT4S], so advancing well past 4s fires it.
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "betrag", Kind: model.VarNumber, Text: "5000"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Parked on the mockup task's timer, with its error boundary armed (two element
	// instances: the task and the boundary); the simulated response is already written.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after activation: process=%d element=%d, want 1 and 2 (task + armed boundary)", pi, ei)
	}
	scope := model.NewKey(1, 1)
	bonitaet := readVar(t, h.store, scope, "bonitaet")
	if bonitaet == nil || bonitaet.Kind != model.VarJSON || !strings.Contains(bonitaet.Text, "genehmigt") {
		t.Fatalf("simulated response `bonitaet` = %+v, want a JSON object with a genehmigt member", bonitaet)
	}

	// Fire the simulated duration (well past PT4S = 4e9ns): the instance completes down
	// one of its paths — gateway (genehmigt/abgelehnt) on success, error boundary
	// (manuell prüfen) on a simulated Umsystem outage. Either way it finishes.
	clk.t = 1_000 + int64(10e9)
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the simulated duration: process=%d element=%d, want 0 and 0 (finished)", pi, ei)
	}
}
