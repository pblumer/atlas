package order

import (
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// TestStillStartsFollowsTheStatusTable: the table ADR-0427 traced, row by row.
func TestStillStartsFollowsTheStatusTable(t *testing.T) {
	cases := []struct {
		status       LineStatus
		prov, deprov bool
	}{
		{StatusPending, true, true},
		{StatusBlocked, true, true},
		{StatusRunning, false, true},
		{StatusFailed, false, true},
		{StatusDone, false, true},
		{StatusReturning, false, false},
		{StatusReturnFailed, false, true},
		{StatusSkipped, false, false},
		{StatusRejected, false, false},
		{StatusAbandoned, false, false},
		{StatusCancelled, false, false},
		{StatusReturned, false, false},
	}
	for _, c := range cases {
		l := Line{ItemID: "vpn", Status: c.status, ProvisionProcess: "p", DeprovisionProcess: "d"}
		if got := l.StillStarts("p"); got != c.prov {
			t.Errorf("%s: StillStarts(provision) = %v, want %v", c.status, got, c.prov)
		}
		if got := l.StillStarts("d"); got != c.deprov {
			t.Errorf("%s: StillStarts(deprovision) = %v, want %v", c.status, got, c.deprov)
		}
	}
}

func lifecycleVPN() catalog.Item {
	return catalog.Item{ID: "vpn", LifecycleProcess: "vpn-lc",
		Operations: map[string]string{catalog.OpProvision: "vpn.p", catalog.OpDeprovision: "vpn.d"}}
}

// TestRebindMovesWhatCanStillStartAndRecordsIt: done and pending lines move and say
// where from; a running line and a finished one stay.
func TestRebindMovesWhatCanStillStartAndRecordsIt(t *testing.T) {
	o := Order{ID: "o1", Lines: []Line{
		{ItemID: "vpn", Status: StatusDone, ProvisionProcess: "p", DeprovisionProcess: "d"},
		{ItemID: "vpn", VariantID: "b", Status: StatusRunning, ProvisionProcess: "p", DeprovisionProcess: "d"},
		{ItemID: "vpn", VariantID: "c", Status: StatusReturned, ProvisionProcess: "p", DeprovisionProcess: "d"},
		{ItemID: "laptop", Status: StatusDone, ProvisionProcess: "lp", DeprovisionProcess: "ld"},
	}}
	next, moved := Rebind(o, lifecycleVPN(), "pm", "the old IdM is gone", 42)
	if moved != 1 {
		t.Fatalf("moved = %d, want 1 (only the done vpn line)", moved)
	}
	l := next.Lines[0]
	if b := l.BindingFor(catalog.OpDeprovision); b.Process != "vpn-lc" || b.Message != "vpn.d" {
		t.Errorf("moved line deprovisions through %+v", b)
	}
	if len(l.Rebindings) != 1 || l.Rebindings[0].FromDeprovision != "d" || l.Rebindings[0].By != "pm" || l.Rebindings[0].At != 42 {
		t.Errorf("record = %+v", l.Rebindings)
	}
	if next.Lines[1].LifecycleProcess != "" || next.Lines[2].LifecycleProcess != "" || next.Lines[3].LifecycleProcess != "" {
		t.Errorf("a line that should have stayed moved: %+v", next.Lines)
	}
	if o.Lines[0].LifecycleProcess != "" {
		t.Error("Rebind wrote through to the order it was given")
	}
	if got := LinesStarting([]Order{next}, "d"); len(got) != 1 || got[0].Lines != 1 {
		t.Errorf("lines still starting d = %+v, want the running line only", got)
	}
}
