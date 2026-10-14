package order

import "testing"

// Giving up on an incident is a decision, and a decision has an author. The
// deadline on an incident escalates it — it makes somebody look — and never
// abandons anything itself: a system that closed orders because nobody was in the
// incident queue over the holidays would tell an orderer their line is never
// coming, and file "the system decided" in a record kept forever.
//
// So Abandon is the only way to reach the status, and it cannot be called without
// naming who. Valid is the same rule at the persistence boundary, where a line
// arrives as JSON rather than through this function.

func TestAbandonRecordsWhoAndWhen(t *testing.T) {
	got, err := Abandon(Line{ItemID: "laptop", Status: StatusFailed}, "usr_7", 1700)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if got.Status != StatusAbandoned {
		t.Errorf("status = %s, want abandoned", got.Status)
	}
	if got.AbandonedBy != "usr_7" || got.AbandonedAt != 1700 {
		t.Errorf("by/at = %q/%d, want usr_7/1700", got.AbandonedBy, got.AbandonedAt)
	}
}

// TestAbandonNeedsAPerson is the rule that keeps the deadline from doing this:
// there is no caller to name, so there is no call to make.
func TestAbandonNeedsAPerson(t *testing.T) {
	in := Line{ItemID: "laptop", Status: StatusFailed}
	got, err := Abandon(in, "", 1700)
	if err == nil {
		t.Fatal("Abandon with no principal must fail")
	}
	if got.Status != StatusFailed {
		t.Fatalf("line changed to %s on a refused call", got.Status)
	}
}

// TestAbandonNeedsAMoment: a decision kept forever with no date on it is a
// decision nobody can place against the incident it was about.
func TestAbandonNeedsAMoment(t *testing.T) {
	in := Line{ItemID: "laptop", Status: StatusFailed}
	got, err := Abandon(in, "usr_7", 0)
	if err == nil {
		t.Fatal("Abandon with no moment must fail")
	}
	if got.Status != StatusFailed {
		t.Fatalf("line changed to %s on a refused call", got.Status)
	}
}

// TestOnlyAFailureCanBeAbandoned: there is nothing to give up on in a line that
// never failed, and rewriting a provisioned one would deny work that happened.
func TestOnlyAFailureCanBeAbandoned(t *testing.T) {
	for _, s := range []LineStatus{
		StatusPending, StatusRunning, StatusDone, StatusSkipped,
		StatusRejected, StatusBlocked, StatusAbandoned,
	} {
		if _, err := Abandon(Line{ItemID: "a", Status: s}, "usr_7", 1700); err == nil {
			t.Errorf("Abandon accepted a %s line", s)
		}
	}
}

// TestValidHoldsTheRuleAtThePersistenceBoundary: a line deserialised from JSON
// never went through Abandon, so the invariant is checked again where it enters.
func TestValidHoldsTheRuleAtThePersistenceBoundary(t *testing.T) {
	tests := []struct {
		name string
		line Line
		ok   bool
	}{
		{"abandoned with an author", Line{ItemID: "a", Status: StatusAbandoned,
			AbandonedBy: "usr_7", AbandonedAt: 1700}, true},
		{"abandoned with nobody", Line{ItemID: "a", Status: StatusAbandoned}, false},
		{"abandoned with an author and no time",
			Line{ItemID: "a", Status: StatusAbandoned, AbandonedBy: "usr_7"}, false},
		{"an author on a line nobody abandoned",
			Line{ItemID: "a", Status: StatusFailed, AbandonedBy: "usr_7"}, false},
		{"an ordinary failure", Line{ItemID: "a", Status: StatusFailed}, true},
		{"a line with no item", Line{Status: StatusPending}, false},
		{"rejected with an author, a moment and words",
			Line{ItemID: "a", Status: StatusRejected, DecidedBy: "usr_9",
				DecidedAt: 1700, Reason: "no budget"}, true},
		{"rejected with nobody",
			Line{ItemID: "a", Status: StatusRejected, DecidedAt: 1700, Reason: "x"}, false},
		{"rejected with no moment",
			Line{ItemID: "a", Status: StatusRejected, DecidedBy: "usr_9", Reason: "x"}, false},
		{"rejected with no reason — the orderer is told it",
			Line{ItemID: "a", Status: StatusRejected, DecidedBy: "usr_9", DecidedAt: 1700}, false},
		{"a decision on a line nobody decided",
			Line{ItemID: "a", Status: StatusDone, DecidedBy: "usr_9"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.line.Valid()
			if tt.ok && err != nil {
				t.Fatalf("Valid() = %v, want nil", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("Valid() = nil, want an error")
			}
		})
	}
}

// TestAbandonedLineStaysAbandonedThroughPropagation: propagation recomputes
// blocked and must not touch a line somebody settled by hand.
func TestAbandonedLineStaysAbandonedThroughPropagation(t *testing.T) {
	line, err := Abandon(Line{ItemID: "laptop", Status: StatusFailed}, "usr_7", 1700)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	got := Propagate([]Line{line, {ItemID: "vpn", Status: StatusPending}},
		map[string][]string{"vpn": {"laptop"}})

	if got[0].Status != StatusAbandoned || got[0].AbandonedBy != "usr_7" {
		t.Fatalf("laptop = %s by %q, want abandoned by usr_7", got[0].Status, got[0].AbandonedBy)
	}
	if got[1].Status != StatusBlocked {
		t.Fatalf("vpn = %s, want blocked", got[1].Status)
	}
}
