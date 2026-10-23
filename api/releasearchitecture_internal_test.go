package api

import (
	"testing"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/compiler"
)

// TestTheSnapshotSaysWhatItCouldNotRead covers the members a publish does not produce
// in an ordinary round but a snapshot still has to answer for: a decision member, a
// process member whose compiled form is not there, a local decision, and a call into
// another process of the same application that this release did not ship (ADR-0471 §1).
func TestTheSnapshotSaysWhatItCouldNotRead(t *testing.T) {
	srv := newServerWithOptions(t)

	b := compiler.NewBuilder(1, "rechnung", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask("rabatt", nil, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	b.SetElementBpmnId(rule, "Rule_Rabatt")
	call := b.AddCallActivity("nachbar", compiler.BindingLatest, false, false)
	b.SetElementBpmnId(call, "Call_Nachbar")
	end := b.AddEndEvent()
	b.Connect(start, rule)
	b.Connect(rule, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var snap *archsketch.Snapshot
	srv.do(func() {
		// The process this release calls, deployed earlier by the same application.
		srv.deployments[901] = &deployment{Key: 901, ProcessID: "nachbar", Version: 1, ProjectID: "app-1"}
		srv.versions["nachbar"] = 1
		srv.order = append(srv.order, 901)
		srv.deployments[902] = &deployment{Key: 902, ProcessID: "rechnung", Name: "Rechnung", Version: 1, ProjectID: "app-1", cp: cp}

		snap = srv.architectureSnapshot("app-1", []releaseMember{
			{Kind: "process", Ref: "rechnung", ArtifactVer: 1, Key: 902},
			{Kind: "process", Ref: "verloren", ArtifactVer: 2, Key: 999},
			{Kind: "decision", Ref: "rabatt", ArtifactVer: 1, Key: 903},
		}, 1_792_000_000)
	})

	got := *snap
	if len(got.Processes) != 2 {
		t.Fatalf("processes = %+v, want the two process members and not the decision", got.Processes)
	}
	if p := got.Processes[1]; p.ProcessID != "verloren" || !p.Unread {
		t.Errorf("missing member = %+v, want it recorded as unread", p)
	}
	if p := got.Processes[0]; p.Name != "Rechnung" || p.Unread {
		t.Errorf("read member = %+v", p)
	}
	if len(got.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v, want only the local decision — a call within the application is not a way out", got.Dependencies)
	}
	d := got.Dependencies[0]
	if d.Kind != "decision" || d.Name != "rabatt" || len(d.UsedBy) != 1 || d.UsedBy[0].ElementID != "Rule_Rabatt" {
		t.Errorf("decision = %+v, want rabatt used at Rule_Rabatt", d)
	}
	if got.UnreadProcesses() != 1 || got.LocalDecisions() != 1 {
		t.Errorf("unread=%d decisions=%d, want 1 and 1", got.UnreadProcesses(), got.LocalDecisions())
	}
}
