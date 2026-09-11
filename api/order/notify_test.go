package order

import (
	"strings"
	"testing"
)

// Three moments reach the person who ordered, and they are the three where
// knowing changes what they do: a line was rejected, a line was given up on, and
// the order finished. Everything else in the chain we built is addressed to
// somebody who can act on it — an incident escalates to an operator, not to the
// orderer, who cannot repair one and would only be alarmed.
//
// Notices reports a *transition*, never a state. Propagate runs after every
// settled line, so a function that reported what is true rather than what changed
// would send the same mail on every pass.

func order(lines ...Line) Order {
	return Order{ID: "ord_1", Orderer: "usr_1", Recipient: "usr_2", Lines: lines}
}

func kinds(ns []Notice) string {
	var out []string
	for _, n := range ns {
		out = append(out, string(n.Kind)+":"+n.ItemID)
	}
	return strings.Join(out, " ")
}

func mustReject(t *testing.T, l Line, reason string) Line {
	t.Helper()
	got, err := Reject(l, "usr_9", 1700, reason)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	return got
}

func mustAbandon(t *testing.T, l Line) Line {
	t.Helper()
	got, err := Abandon(l, "usr_7", 1700)
	if err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	return got
}

// TestRejectionIsNotified, with the reason, because "your request was declined"
// and nothing else is a message that generates a phone call.
func TestRejectionIsNotified(t *testing.T) {
	before := order(Line{ItemID: "laptop", Status: StatusRunning})
	after := order(mustReject(t, before.Lines[0], "no budget this quarter"))

	// The rejection, and the settlement that follows from it being the only line.
	got := Notices(before, after)
	if kinds(got) != "rejected:laptop settled:" {
		t.Fatalf("notices = %q, want rejected:laptop settled:", kinds(got))
	}
	if got[0].Kind != NoticeRejected || got[0].ItemID != "laptop" {
		t.Fatalf("notice = %s/%s, want rejected/laptop", got[0].Kind, got[0].ItemID)
	}
	if got[0].Recipient != "usr_1" {
		t.Errorf("recipient = %s, want the orderer usr_1", got[0].Recipient)
	}
	if got[0].Reason != "no budget this quarter" {
		t.Errorf("reason = %q, want the approver's words", got[0].Reason)
	}
}

// TestAbandonmentIsNotified: the line is never coming, and without this the
// orderer cannot tell that from "still being worked on".
func TestAbandonmentIsNotified(t *testing.T) {
	before := order(Line{ItemID: "laptop", Status: StatusFailed})
	after := order(mustAbandon(t, before.Lines[0]))

	// Two messages, and both are owed: the line is never coming, and with it the
	// order is over. Giving up on the only outstanding line settles the order in
	// the same step.
	got := Notices(before, after)
	if kinds(got) != "abandoned:laptop settled:" {
		t.Fatalf("notices = %q, want abandoned:laptop settled:", kinds(got))
	}
	if got[1].Outcome != OrderUnfulfilled {
		t.Fatalf("outcome = %s, want unfulfilled", got[1].Outcome)
	}
}

// TestFailureIsNotNotified: an incident is addressed to whoever can repair it.
// Telling the orderer that provisioning threw an error gives them a worry and no
// action — and it would arrive again on every retry.
func TestFailureIsNotNotified(t *testing.T) {
	before := order(Line{ItemID: "laptop", Status: StatusRunning})
	after := order(Line{ItemID: "laptop", Status: StatusFailed})

	if got := Notices(before, after); len(got) != 0 {
		t.Fatalf("notices = %q, want none", kinds(got))
	}
}

// TestBlockingIsNotNotified: a blocked line is a consequence, and its cause was
// notified already. A second message about the same event is noise.
func TestBlockingIsNotNotified(t *testing.T) {
	req := map[string][]string{"vpn": {"laptop"}}
	before := order(Line{ItemID: "laptop", Status: StatusFailed},
		Line{ItemID: "vpn", Status: StatusPending})
	after := order(Propagate([]Line{mustAbandon(t, before.Lines[0]),
		{ItemID: "vpn", Status: StatusPending}}, req)...)

	got := Notices(before, after)
	if kinds(got) != "abandoned:laptop settled:" {
		t.Fatalf("notices = %q, want the abandonment and the settlement only", kinds(got))
	}
}

// TestSettlementIsNotifiedOnce is the idempotence that matters most: Propagate
// runs after every settled line, and a notice function reporting state rather
// than change would mail the orderer on each pass.
func TestSettlementIsNotifiedOnce(t *testing.T) {
	before := order(Line{ItemID: "a", Status: StatusRunning})
	after := order(Line{ItemID: "a", Status: StatusDone})

	if kinds(Notices(before, after)) != "settled:" {
		t.Fatalf("notices = %q, want settled", kinds(Notices(before, after)))
	}
	// Nothing changed since: no second message.
	if got := Notices(after, after); len(got) != 0 {
		t.Fatalf("notices = %q on an unchanged order, want none", kinds(got))
	}
}

// TestStillRunningIsNotNotified: a line finishing is not the order finishing.
func TestStillRunningIsNotNotified(t *testing.T) {
	before := order(Line{ItemID: "a", Status: StatusRunning},
		Line{ItemID: "b", Status: StatusRunning})
	after := order(Line{ItemID: "a", Status: StatusDone},
		Line{ItemID: "b", Status: StatusRunning})

	if got := Notices(before, after); len(got) != 0 {
		t.Fatalf("notices = %q, want none — b is still going", kinds(got))
	}
}

// TestSettlementCarriesWhatCameAndWhatDidNot: the closing message is the one
// place the orderer is told the whole outcome.
func TestSettlementCarriesWhatCameAndWhatDidNot(t *testing.T) {
	before := order(Line{ItemID: "laptop", Status: StatusRunning},
		Line{ItemID: "mailbox", Status: StatusDone})
	after := order(mustReject(t, before.Lines[0], "no budget"),
		Line{ItemID: "mailbox", Status: StatusDone})

	var settled *Notice
	for i, n := range Notices(before, after) {
		if n.Kind == NoticeSettled {
			settled = &Notices(before, after)[i]
		}
	}
	if settled == nil {
		t.Fatal("no settlement notice")
	}
	if strings.Join(settled.Provisioned, ",") != "mailbox" {
		t.Errorf("provisioned = %v, want [mailbox]", settled.Provisioned)
	}
	if strings.Join(settled.NotProvisioned, ",") != "laptop" {
		t.Errorf("not provisioned = %v, want [laptop]", settled.NotProvisioned)
	}
	if settled.Outcome != OrderPartial {
		t.Errorf("outcome = %s, want partial", settled.Outcome)
	}
}

// TestSeveralChangesAreAllReported, in a fixed order, so two runs of the same
// transition produce the same messages.
func TestSeveralChangesAreAllReported(t *testing.T) {
	before := order(Line{ItemID: "b", Status: StatusFailed},
		Line{ItemID: "a", Status: StatusRunning})
	after := order(mustAbandon(t, before.Lines[0]),
		mustReject(t, before.Lines[1], "declined"))

	got := kinds(Notices(before, after))
	if got != "rejected:a abandoned:b settled:" {
		t.Fatalf("notices = %q, want rejected:a abandoned:b settled:", got)
	}
}

// TestRejectRecordsWhoAndWhy: a rejection kept forever without an author is a
// decision nobody made, and the same rule Abandon holds applies here.
func TestRejectRecordsWhoAndWhy(t *testing.T) {
	got := mustReject(t, Line{ItemID: "a", Status: StatusRunning}, "no budget")
	if got.Status != StatusRejected || got.DecidedBy != "usr_9" || got.Reason != "no budget" {
		t.Fatalf("line = %+v, want rejected by usr_9 with a reason", got)
	}
}

// TestRejectNeedsAnAuthorAReasonAndAMoment.
func TestRejectNeedsAnAuthorAReasonAndAMoment(t *testing.T) {
	in := Line{ItemID: "a", Status: StatusRunning}
	if _, err := Reject(in, "", 1700, "no budget"); err == nil {
		t.Error("Reject with no author must fail")
	}
	if _, err := Reject(in, "usr_9", 0, "no budget"); err == nil {
		t.Error("Reject with no moment must fail")
	}
	if _, err := Reject(in, "usr_9", 1700, ""); err == nil {
		t.Error("Reject with no reason must fail — the orderer is owed one")
	}
	if _, err := Reject(Line{ItemID: "a", Status: StatusDone}, "usr_9", 1700, "x"); err == nil {
		t.Error("Reject of a provisioned line must fail")
	}
}
