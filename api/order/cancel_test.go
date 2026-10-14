package order

import (
	"strings"
	"testing"
)

// What may be taken back, what may not, and what a withdrawal has to say about
// itself.

func TestWhatCanStillBeWithdrawn(t *testing.T) {
	for status, want := range map[LineStatus]bool{
		StatusPending: true,
		StatusBlocked: true,
		// With a provisioning process now: a conversation with a system this
		// server does not control, and stopping it halfway is not withdrawal but a
		// half-provisioned account nobody owns.
		StatusRunning: false,
		// Finished, each in its own way. A failed line in particular has a
		// decision of its own waiting — somebody gives up on it — and calling that
		// a change of mind files a repair nobody finished as one.
		StatusDone:      false,
		StatusSkipped:   false,
		StatusFailed:    false,
		StatusRejected:  false,
		StatusAbandoned: false,
		StatusCancelled: false,
	} {
		if got := status.Cancellable(); got != want {
			t.Errorf("%s.Cancellable() = %v, want %v", status, got, want)
		}
	}
}

func TestAWithdrawalNamesWhoMadeIt(t *testing.T) {
	line := Line{ItemID: "vpn", Status: StatusPending}

	if _, err := Cancel(line, "", 7, ""); err == nil {
		t.Error("a withdrawal with no author was accepted; the record would say somebody " +
			"decided without saying who")
	}
	if _, err := Cancel(line, "usr_1", 0, ""); err == nil {
		t.Error("a withdrawal with no moment was accepted")
	}
	if _, err := Cancel(Line{ItemID: "vpn", Status: StatusRunning}, "usr_1", 7, ""); err == nil {
		t.Error("a running line was withdrawn")
	}

	// A reason is optional: the person a cancellation is explained to is the
	// person who made it.
	got, err := Cancel(line, "usr_1", 7, "")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.Status != StatusCancelled || got.DecidedBy != "usr_1" || got.DecidedAt != 7 {
		t.Errorf("= %+v, want it withdrawn with its author", got)
	}
	if err := got.Valid(); err != nil {
		t.Errorf("a withdrawn line does not survive the persistence boundary: %v", err)
	}
}

// TestAWithdrawnLineIsNoLongerWaitingForAnything: a blocked line carries what
// stopped it, and leaving those on it would have the portal tell somebody their
// cancelled line is waiting for a laptop.
func TestAWithdrawnLineIsNoLongerWaitingForAnything(t *testing.T) {
	blocked := Line{ItemID: "vpn", Status: StatusBlocked,
		BlockedBy: []string{"laptop"}, TerminallyBlocked: true}

	got, err := Cancel(blocked, "usr_1", 7, "")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(got.BlockedBy) != 0 || got.TerminallyBlocked {
		t.Errorf("= %+v, want nothing left saying it waits", got)
	}
}

func TestWithdrawingAnOrderNamesBothHalves(t *testing.T) {
	o := Order{ID: "ord_1", Requires: map[string][]string{"laptop": {"account"}}, Lines: []Line{
		{ItemID: "account", Status: StatusPending},
		{ItemID: "laptop", Status: StatusPending},
		{ItemID: "screen", Status: StatusDone},
		{ItemID: "dock", Status: StatusRunning},
	}}

	got, cancelled, kept, err := CancelOrder(o, "usr_1", 7, "falsch bestellt")
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if strings.Join(cancelled, ",") != "account,laptop" {
		t.Errorf("cancelled = %v, want the two that had not happened", cancelled)
	}
	if strings.Join(kept, ",") != "screen,dock" {
		t.Errorf("kept = %v, want the delivered one and the one under way", kept)
	}
	if got.UpdatedAt != 7 {
		t.Errorf("updatedAt = %d, want the moment of the withdrawal", got.UpdatedAt)
	}
	for _, l := range got.Lines {
		switch l.ItemID {
		case "screen":
			if l.Status != StatusDone {
				t.Errorf("a provisioned line was rewritten to %q; the recipient holds it", l.Status)
			}
		case "dock":
			if l.Status != StatusRunning {
				t.Errorf("a running line was rewritten to %q", l.Status)
			}
		default:
			if l.Status != StatusCancelled {
				t.Errorf("%s = %q, want cancelled", l.ItemID, l.Status)
			}
		}
	}

	// Nothing left to take back is refused rather than answered with an empty
	// list: the caller asked for something and did not get it.
	settled := Order{ID: "ord_2", Lines: []Line{{ItemID: "a", Status: StatusDone}}}
	if _, _, kept, err := CancelOrder(settled, "usr_1", 7, ""); err == nil {
		t.Errorf("a settled order was withdrawn (kept = %v)", kept)
	}

	// And the author is still required one level up.
	if _, _, _, err := CancelOrder(o, "", 7, ""); err == nil {
		t.Error("an order was withdrawn by nobody")
	}
}
