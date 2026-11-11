package order

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// A line whose instance ended without reporting
// (ADR-draft-a-cancelled-line-instance-settles-its-line).

func lineWith(status LineStatus, ins ...LineInstance) Line {
	return Line{ItemID: "vpn", Status: status, Instances: ins}
}

// TestWaitsOnNamesTheInstanceTheStatusIsWaitingFor: each status that waits names
// the instance it waits on and what it settles to, and nothing else waits.
func TestWaitsOnNamesTheInstanceTheStatusIsWaitingFor(t *testing.T) {
	approval := LineInstance{Key: 1, Operation: "approval"}
	prov := LineInstance{Key: 2, Operation: catalog.OpProvision}
	retry := LineInstance{Key: 3, Operation: catalog.OpProvision}
	legacy := LineInstance{Key: 4}
	deprov := LineInstance{Key: 5, Operation: catalog.OpDeprovision}
	for _, tc := range []struct {
		name   string
		line   Line
		key    uint64
		settle LineStatus
		ok     bool
	}{
		{"running waits on its provisioning", lineWith(StatusRunning, approval, prov), 2, StatusFailed, true},
		{"running waits on its newest provisioning", lineWith(StatusRunning, prov, retry), 3, StatusFailed, true},
		{"running waits on an instance its starter named no operation for", lineWith(StatusRunning, legacy), 4, StatusFailed, true},
		{"returning waits on its deprovisioning", lineWith(StatusReturning, prov, deprov), 5, StatusReturnFailed, true},
		{"pending waits on its approval", lineWith(StatusPending, approval), 1, StatusFailed, true},
		{"an approved pending line waits on nothing", func() Line { l := lineWith(StatusPending, approval); l.ApprovedBy = "usr_a"; return l }(), 0, "", false},
		{"a pending line nothing was started for waits on nothing", lineWith(StatusPending), 0, "", false},
		{"a running line with no provisioning recorded waits on nothing", lineWith(StatusRunning, approval), 0, "", false},
		{"a returning line with no deprovisioning waits on nothing", lineWith(StatusReturning, prov), 0, "", false},
		{"a done line waits on nothing", lineWith(StatusDone, prov), 0, "", false},
		{"a failed line waits on nothing", lineWith(StatusFailed, prov), 0, "", false},
		{"a cancelled line waits on nothing", lineWith(StatusCancelled, approval), 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst, settle, ok := tc.line.WaitsOn()
			if ok != tc.ok || inst.Key != tc.key || settle != tc.settle {
				t.Errorf("WaitsOn = %d, %q, %v; want %d, %q, %v", inst.Key, settle, ok, tc.key, tc.settle, tc.ok)
			}
		})
	}
}

// TestSettleEndedSettlesOnlyTheLineWaitingOnTheKey, once, and propagates.
func TestSettleEndedSettlesOnlyTheLineWaitingOnTheKey(t *testing.T) {
	o := Order{ID: "ord_1", Lines: []Line{
		lineWith(StatusRunning, LineInstance{Key: 2, Operation: catalog.OpProvision}),
		{ItemID: "token", Status: StatusPending},
		{ItemID: "phone", Status: StatusRunning, Instances: []LineInstance{{Key: 9, Operation: catalog.OpProvision}}},
	}, Requires: map[string][]string{"token": {"vpn"}}}

	got, ended, err := SettleEnded(o, 2, 1800)
	if err != nil {
		t.Fatalf("SettleEnded: %v", err)
	}
	if len(ended) != 1 || ended[0] != (Ended{Line: "vpn", Status: StatusFailed}) {
		t.Fatalf("ended = %+v, want vpn failed", ended)
	}
	if got.Lines[0].Status != StatusFailed || got.Lines[2].Status != StatusRunning {
		t.Errorf("lines = %+v, want vpn failed and phone left running", got.Lines)
	}
	if got.Lines[1].Status != StatusBlocked {
		t.Errorf("token = %q, want blocked behind the failed vpn", got.Lines[1].Status)
	}
	if got.UpdatedAt != 1800 {
		t.Errorf("UpdatedAt = %d, want the moment it was noticed", got.UpdatedAt)
	}
	if o.Lines[0].Status != StatusRunning {
		t.Error("SettleEnded changed the order it was handed")
	}

	// Noticed again — by the repair after a live settlement — it changes nothing.
	again, ended, err := SettleEnded(got, 2, 1900)
	if err != nil || len(ended) != 0 || again.UpdatedAt != 1800 {
		t.Errorf("second settle = %+v, %v (updated %d), want nothing", ended, err, again.UpdatedAt)
	}
	// An instance no line waits on settles nothing.
	if _, ended, _ := SettleEnded(o, 42, 1800); len(ended) != 0 {
		t.Errorf("unrelated key settled %+v", ended)
	}
}

// TestALineThatAlreadyReportedIsNotSettledByItsInstanceEnding: a provisioning that
// reported done and is then cancelled where it parked does not unsay the delivery.
func TestALineThatAlreadyReportedIsNotSettledByItsInstanceEnding(t *testing.T) {
	o := Order{ID: "ord_1", Lines: []Line{lineWith(StatusDone, LineInstance{Key: 2, Operation: catalog.OpProvision})}}
	got, ended, err := SettleEnded(o, 2, 1800)
	if err != nil || len(ended) != 0 || got.Lines[0].Status != StatusDone {
		t.Errorf("= %+v, %+v, %v; want the done line untouched", got.Lines, ended, err)
	}
}

// TestInstanceEndedRecordsTheOutcomeAndWakes: the order is saved, the failed
// outcome says why, and the orchestration is woken — HandleReport's order of
// things for a failed report.
func TestInstanceEndedRecordsTheOutcomeAndWakes(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	var woke []string
	s := New(loop, store, func() int64 { return 1800 },
		func(string) (catalog.Release, bool, error) { return catalog.Release{}, false, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error {
			woke = append(woke, message+" "+orderID)
			return nil
		},
		func() string { return "" }, func() string { return "" },
		ignoreGrant, ignoreRevoke, holdsNothing)
	var outcomes []Outcome
	s.ReportOutcomesTo(func(o Outcome) error { outcomes = append(outcomes, o); return nil })

	if err := store.Save(Order{ID: "ord_1", Orderer: "usr_1", Recipient: "usr_1", Lines: []Line{
		lineWith(StatusReturning, LineInstance{Key: 2, Operation: catalog.OpProvision},
			LineInstance{Key: 5, Operation: catalog.OpDeprovision}),
	}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, ended, err := s.InstanceEnded("ord_1", 5, "terminated")
	if err != nil || !found || len(ended) != 1 || ended[0].Status != StatusReturnFailed {
		t.Fatalf("InstanceEnded = %v, %+v, %v; want the return failed", found, ended, err)
	}
	got, _, _ := store.Get("ord_1")
	if got.Lines[0].Status != StatusReturnFailed {
		t.Errorf("stored status = %q", got.Lines[0].Status)
	}
	if len(outcomes) != 1 || outcomes[0].Effect != catalog.EffectDeprovision || outcomes[0].Outcome != catalog.OutcomeFailed ||
		!strings.Contains(outcomes[0].Result, `"instanceKey":"5"`) || !strings.Contains(outcomes[0].Result, "terminated") {
		t.Errorf("outcomes = %+v, want the return's failure with its reason", outcomes)
	}
	if len(woke) != 1 || woke[0] != AdvancedMessage+" ord_1" {
		t.Errorf("woke = %v, want the order's orchestration once", woke)
	}

	// Again: nothing waits on 5 any more, so nothing is written and nobody woken.
	if _, ended, err := s.InstanceEnded("ord_1", 5, "terminated"); err != nil || len(ended) != 0 || len(woke) != 1 || len(outcomes) != 1 {
		t.Errorf("second = %+v, %v; woke %v, outcomes %d — want nothing", ended, err, woke, len(outcomes))
	}
	// An order this server does not hold is not an error.
	if found, _, err := s.InstanceEnded("ord_gone", 5, "terminated"); found || err != nil {
		t.Errorf("unknown order = %v, %v", found, err)
	}
}

// TestAnOutcomeThatCannotBeWrittenStillWakes: nobody comes back to a line that no
// longer waits on its instance, so a failed outcome write must not keep the
// orchestration asleep. The line is settled, the orchestration woken, and the
// failure returned for the caller to log.
func TestAnOutcomeThatCannotBeWrittenStillWakes(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	woke := 0
	s := New(loop, store, func() int64 { return 1800 },
		func(string) (catalog.Release, bool, error) { return catalog.Release{}, false, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(string, string, map[string]string) error { woke++; return nil },
		func() string { return "" }, func() string { return "" },
		ignoreGrant, ignoreRevoke, holdsNothing)
	s.ReportOutcomesTo(func(Outcome) error { return errTest })
	if err := store.Save(Order{ID: "ord_1", Orderer: "usr_1", Recipient: "usr_1", Lines: []Line{
		lineWith(StatusRunning, LineInstance{Key: 2, Operation: catalog.OpProvision}),
	}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, ended, err := s.InstanceEnded("ord_1", 2, "terminated")
	if err == nil || len(ended) != 1 || woke != 1 {
		t.Errorf("= %+v, %v, woke %d; want the line settled, the error returned and one wake", ended, err, woke)
	}
}
