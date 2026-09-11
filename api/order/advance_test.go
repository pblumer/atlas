package order

import (
	"strings"
	"testing"
)

// Driving an order forward.
//
// Waves say what may run together and are what a person reads. What may start
// *now* is a finer question, and the waves cannot answer it once one of them has
// failed in part: a wave boundary knows only that the previous round is over. So
// the orchestrator asks per line — everything I need has been provisioned — which
// is the same edge set the blocking rule already uses, read the other way round.

func ord(requires map[string][]string, ls ...Line) Order {
	return Order{ID: "ord_1", ReleaseID: "rel_1", Orderer: "usr_1", Recipient: "usr_1",
		Lines: ls, Requires: requires}
}

func line(id string, s LineStatus) Line { return Line{ItemID: id, Status: s} }

// TestWhatMayStartNow: pending lines whose preconditions are all met.
func TestWhatMayStartNow(t *testing.T) {
	o := ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusPending), line("vpn", StatusPending))

	if got := strings.Join(Next(o), ","); got != "laptop" {
		t.Fatalf("Next = %s, want laptop alone", got)
	}

	o.Lines[0].Status = StatusDone
	if got := strings.Join(Next(o), ","); got != "vpn" {
		t.Fatalf("Next after the laptop = %s, want vpn", got)
	}
}

// TestSkippedCountsAsProvisioned: the recipient already holds it, so what waited
// on it may run.
func TestSkippedCountsAsProvisioned(t *testing.T) {
	o := ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusSkipped), line("vpn", StatusPending))
	if got := strings.Join(Next(o), ","); got != "vpn" {
		t.Fatalf("Next = %s, want vpn", got)
	}
}

// TestNothingStartsTwice: a line already running or finished is not offered
// again, which is what lets the orchestrator ask after every single result.
func TestNothingStartsTwice(t *testing.T) {
	o := ord(nil,
		line("a", StatusRunning), line("b", StatusDone),
		line("c", StatusFailed), line("d", StatusRejected))
	if got := Next(o); len(got) != 0 {
		t.Fatalf("Next = %v, want nothing", got)
	}
}

// TestABlockedLineIsNotOffered: its precondition will not arrive.
func TestABlockedLineIsNotOffered(t *testing.T) {
	o := Propagate(ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusRejected), line("vpn", StatusPending)).Lines,
		map[string][]string{"vpn": {"laptop"}})

	got := Next(Order{Lines: o, Requires: map[string][]string{"vpn": {"laptop"}}})
	if len(got) != 0 {
		t.Fatalf("Next = %v, want nothing — the vpn is blocked", got)
	}
}

// TestIndependentLinesAreOfferedTogether, sorted, so an orchestrator starting
// them gets the same set in the same order every time.
func TestIndependentLinesAreOfferedTogether(t *testing.T) {
	o := ord(nil, line("c", StatusPending), line("a", StatusPending), line("b", StatusPending))
	if got := strings.Join(Next(o), ","); got != "a,b,c" {
		t.Fatalf("Next = %s, want a,b,c", got)
	}
}

// TestALineWaitsForAllOfItsPreconditions, not the first one to arrive.
func TestALineWaitsForAllOfItsPreconditions(t *testing.T) {
	o := ord(map[string][]string{"z": {"x", "y"}},
		line("x", StatusDone), line("y", StatusRunning), line("z", StatusPending))
	if got := Next(o); len(got) != 0 {
		t.Fatalf("Next = %v, want nothing — y is still running", got)
	}
}

// Applying a result is the other half: record it, propagate what it stopped, and
// recompute the order's own standing. All three, because doing one without the
// others leaves the order describing something that is no longer true.

func TestApplyRecordsAResultAndPropagatesIt(t *testing.T) {
	o := ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusRunning), line("vpn", StatusPending))

	got, err := Apply(o, "laptop", StatusDone, 1800)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Lines[0].Status != StatusDone {
		t.Errorf("laptop = %s, want done", got.Lines[0].Status)
	}
	if got.UpdatedAt != 1800 {
		t.Errorf("UpdatedAt = %d, want 1800", got.UpdatedAt)
	}
	if s := Derive(got.Lines); s != OrderRunning {
		t.Errorf("order = %s, want running — the vpn is now startable", s)
	}
}

func TestApplyBlocksWhatDependedOnAFailure(t *testing.T) {
	o := ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusRunning), line("vpn", StatusPending))

	got, err := Apply(o, "laptop", StatusFailed, 1800)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s := statusOf(got.Lines, "vpn"); s != StatusBlocked {
		t.Fatalf("vpn = %s, want blocked", s)
	}
	if by := blockedBy(got.Lines, "vpn"); by != "laptop" {
		t.Fatalf("vpn blocked by %q, want laptop", by)
	}
}

// TestApplyLeavesTheOrderAloneOnRefusal: a caller that retried against a
// half-changed order would compound the mistake.
func TestApplyLeavesTheOrderAloneOnRefusal(t *testing.T) {
	o := ord(nil, line("a", StatusRunning))

	for _, tt := range []struct {
		name   string
		item   string
		status LineStatus
	}{
		{"unknown line", "ghost", StatusDone},
		{"a status nothing may be set to directly", "a", StatusBlocked},
		{"one that needs its own transition", "a", StatusRejected},
		{"another", "a", StatusAbandoned},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Apply(o, tt.item, tt.status, 1800)
			if err == nil {
				t.Fatalf("Apply(%s, %s) succeeded, want a refusal", tt.item, tt.status)
			}
			if got.Lines[0].Status != StatusRunning || o.Lines[0].Status != StatusRunning {
				t.Fatalf("the order changed on a refused call: %+v", got.Lines)
			}
		})
	}
}

// TestApplyNeedsAMoment, like every other recorded transition.
func TestApplyNeedsAMoment(t *testing.T) {
	o := ord(nil, line("a", StatusRunning))
	if _, err := Apply(o, "a", StatusDone, 0); err == nil {
		t.Fatal("Apply with no moment must succeed only with one")
	}
}

// TestAnOrderRunsToCompletion is the whole mechanism in one test: start what may
// start, apply what comes back, repeat, and stop when nothing is left.
func TestAnOrderRunsToCompletion(t *testing.T) {
	o := ord(map[string][]string{"laptop": {"account"}, "vpn": {"laptop"}},
		line("account", StatusPending), line("laptop", StatusPending),
		line("vpn", StatusPending), line("mailbox", StatusPending))

	var started []string
	at := int64(1800)
	for rounds := 0; ; rounds++ {
		if rounds > 10 {
			t.Fatal("the order did not settle in ten rounds")
		}
		next := Next(o)
		if len(next) == 0 {
			break
		}
		for _, id := range next {
			started = append(started, id)
			var err error
			if o, err = Apply(o, id, StatusDone, at); err != nil {
				t.Fatalf("Apply(%s): %v", id, err)
			}
			at++
		}
	}

	if s := Derive(o.Lines); s != OrderCompleted {
		t.Fatalf("order = %s, want completed", s)
	}
	// The independent mailbox went in the first round; the chain took three more.
	if got := strings.Join(started, ","); got != "account,mailbox,laptop,vpn" {
		t.Fatalf("started %s, want the chain in order with the mailbox early", got)
	}
}

// TestAFailureStopsOnlyItsChain, driven the same way: everything that can still
// be provisioned is.
func TestAFailureStopsOnlyItsChain(t *testing.T) {
	o := ord(map[string][]string{"vpn": {"laptop"}},
		line("laptop", StatusPending), line("vpn", StatusPending),
		line("mailbox", StatusPending))

	at := int64(1800)
	for {
		next := Next(o)
		if len(next) == 0 {
			break
		}
		for _, id := range next {
			status := StatusDone
			if id == "laptop" {
				status = StatusFailed
			}
			var err error
			if o, err = Apply(o, id, status, at); err != nil {
				t.Fatalf("Apply(%s): %v", id, err)
			}
			at++
		}
	}

	if s := statusOf(o.Lines, "mailbox"); s != StatusDone {
		t.Errorf("mailbox = %s, want done — it needed nothing from the laptop", s)
	}
	if s := statusOf(o.Lines, "vpn"); s != StatusBlocked {
		t.Errorf("vpn = %s, want blocked", s)
	}
	// Still running: the laptop's incident can be repaired.
	if s := Derive(o.Lines); s != OrderRunning {
		t.Errorf("order = %s, want running", s)
	}
}

// An order is self-contained: it carries the schedule, the preconditions, and
// the process each line is provisioned by. All three come from the release at
// the moment of placing, for the same reason — fulfilment reads one record, and
// what was ordered cannot change because somebody edited a product afterwards.

// TestReadyLinesCarryTheirProcess: an orchestrator has to know what to start,
// and looking it up in the catalogue would reintroduce the edit it was frozen
// against.
func TestReadyLinesCarryTheirProcess(t *testing.T) {
	o := Order{ID: "ord_1", Lines: []Line{
		{ItemID: "laptop", Status: StatusPending, ProvisionProcess: "prov-laptop"},
		{ItemID: "vpn", Status: StatusPending, ProvisionProcess: "prov-vpn",
			VariantID: "fast"},
	}, Requires: map[string][]string{"vpn": {"laptop"}}}

	got := Ready(o)
	if len(got) != 1 {
		t.Fatalf("Ready = %v, want the laptop alone", got)
	}
	if got[0].ItemID != "laptop" || got[0].ProvisionProcess != "prov-laptop" {
		t.Fatalf("ready line = %+v, want the laptop with its process", got[0])
	}

	o.Lines[0].Status = StatusDone
	got = Ready(o)
	if len(got) != 1 || got[0].VariantID != "fast" {
		t.Fatalf("ready line = %+v, want the vpn with its variant", got)
	}
}

// TestReadyAndNextAgree: one is the other with the detail an orchestrator needs,
// and a reader has to be able to trust that.
func TestReadyAndNextAgree(t *testing.T) {
	o := Order{Lines: []Line{
		{ItemID: "b", Status: StatusPending}, {ItemID: "a", Status: StatusPending},
		{ItemID: "c", Status: StatusDone},
	}}
	ids := Next(o)
	ready := Ready(o)
	if len(ids) != len(ready) {
		t.Fatalf("Next has %d, Ready has %d", len(ids), len(ready))
	}
	for i := range ids {
		if ids[i] != ready[i].ItemID {
			t.Fatalf("position %d: Next says %s, Ready says %s", i, ids[i], ready[i].ItemID)
		}
	}
}
