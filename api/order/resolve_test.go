package order

import (
	"strings"
	"testing"
)

// Propagation is where the three failure statuses earn their separation. The rule
// they serve: a line that fails stops only the lines that depend on it, and
// everything else provisions (order 20 of the portal decisions).

// requires builds the precondition map a release carries.
func requires(pairs map[string][]string) map[string][]string { return pairs }

func lines(spec ...any) []Line {
	var out []Line
	for i := 0; i < len(spec); i += 2 {
		out = append(out, Line{ItemID: spec[i].(string), Status: spec[i+1].(LineStatus)})
	}
	return out
}

func statusOf(got []Line, id string) LineStatus {
	for _, l := range got {
		if l.ItemID == id {
			return l.Status
		}
	}
	return ""
}

func blockedBy(got []Line, id string) string {
	for _, l := range got {
		if l.ItemID == id {
			return strings.Join(l.BlockedBy, ",")
		}
	}
	return ""
}

// TestFailureBlocksOnlyItsDependents is the rule in one test: laptop fails, vpn
// depends on it and stops, mailbox depends on account and runs.
func TestFailureBlocksOnlyItsDependents(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}, "mailbox": {"account"}})
	got := Propagate(lines(
		"laptop", StatusFailed,
		"account", StatusDone,
		"vpn", StatusPending,
		"mailbox", StatusPending,
	), req)

	if s := statusOf(got, "vpn"); s != StatusBlocked {
		t.Errorf("vpn = %s, want blocked", s)
	}
	if s := statusOf(got, "mailbox"); s != StatusPending {
		t.Errorf("mailbox = %s, want pending — account succeeded", s)
	}
}

// TestRejectionBlocksLikeAFailureButKeepsItsName: the propagation is the same,
// the cause is not, and a decision must never be filed as a malfunction.
func TestRejectionBlocksLikeAFailureButKeepsItsName(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusRejected, "vpn", StatusPending), req)

	if s := statusOf(got, "laptop"); s != StatusRejected {
		t.Errorf("laptop = %s, want rejected — propagation must not rewrite it as failed", s)
	}
	if s := statusOf(got, "vpn"); s != StatusBlocked {
		t.Errorf("vpn = %s, want blocked", s)
	}
}

// TestBlockedNamesItsRootCause: not the intermediate blocked line, which carries
// the same cause and would make a reader walk the chain.
func TestBlockedNamesItsRootCause(t *testing.T) {
	req := requires(map[string][]string{"dock": {"laptop"}, "vpn": {"dock"}})
	got := Propagate(lines(
		"laptop", StatusRejected,
		"dock", StatusPending,
		"vpn", StatusPending,
	), req)

	if got := blockedBy(got, "vpn"); got != "laptop" {
		t.Fatalf("vpn blocked by %q, want laptop — dock is itself blocked", got)
	}
}

// TestSeveralCausesAreAllNamed, sorted, so a person sees everything that has to
// be resolved rather than the first thing found.
func TestSeveralCausesAreAllNamed(t *testing.T) {
	req := requires(map[string][]string{"z": {"x", "y"}})
	got := Propagate(lines(
		"x", StatusFailed,
		"y", StatusRejected,
		"z", StatusPending,
	), req)

	if got := blockedBy(got, "z"); got != "x,y" {
		t.Fatalf("z blocked by %q, want x,y", got)
	}
}

// TestSkippedSatisfiesItsDependents: a laptop the recipient already holds is a
// met precondition. Blocking the VPN because the laptop was unnecessary would be
// the inventory making the order worse.
func TestSkippedSatisfiesItsDependents(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusSkipped, "vpn", StatusPending), req)

	if s := statusOf(got, "vpn"); s != StatusPending {
		t.Fatalf("vpn = %s, want pending — a skipped precondition is met", s)
	}
}

// TestRunningPreconditionDoesNotBlock: not yet satisfied is not the same as never
// going to be. A line whose precondition is still running simply waits.
func TestRunningPreconditionDoesNotBlock(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusRunning, "vpn", StatusPending), req)

	if s := statusOf(got, "vpn"); s != StatusPending {
		t.Fatalf("vpn = %s, want pending", s)
	}
}

// TestSettledLinesAreNotRewritten: a line already provisioned before something
// upstream failed stays done. Provisioning happened; the record must say so.
func TestSettledLinesAreNotRewritten(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusFailed, "vpn", StatusDone), req)

	if s := statusOf(got, "vpn"); s != StatusDone {
		t.Fatalf("vpn = %s, want done — it was already provisioned", s)
	}
}

// TestPropagationIsIdempotent: running it twice changes nothing, which is what
// lets the fulfilment process call it after every line settles.
func TestPropagationIsIdempotent(t *testing.T) {
	req := requires(map[string][]string{"b": {"a"}, "c": {"b"}})
	once := Propagate(lines("a", StatusFailed, "b", StatusPending, "c", StatusPending), req)
	twice := Propagate(once, req)

	for i := range once {
		if once[i].Status != twice[i].Status ||
			strings.Join(once[i].BlockedBy, ",") != strings.Join(twice[i].BlockedBy, ",") {
			t.Fatalf("line %s changed on a second pass: %v then %v",
				once[i].ItemID, once[i], twice[i])
		}
	}
}

// TestPropagateLeavesTheInputAlone: the caller's slice must not change under it,
// or a retry would resolve against already-rewritten lines.
func TestPropagateLeavesTheInputAlone(t *testing.T) {
	in := lines("a", StatusFailed, "b", StatusPending)
	Propagate(in, requires(map[string][]string{"b": {"a"}}))

	if in[1].Status != StatusPending {
		t.Fatalf("input line b was mutated to %s", in[1].Status)
	}
}

// TestOrderStatusIsDerived pins the four outcomes. Partial is an ordinary result:
// everything that could be provisioned was.
func TestOrderStatusIsDerived(t *testing.T) {
	tests := []struct {
		name string
		in   []Line
		want Status
	}{
		{"still working", lines("a", StatusDone, "b", StatusRunning), OrderRunning},
		{"nothing reached yet", lines("a", StatusPending), OrderRunning},
		{"all provisioned", lines("a", StatusDone, "b", StatusDone), OrderCompleted},
		{"skipped counts as provisioned", lines("a", StatusSkipped, "b", StatusDone), OrderCompleted},
		{"an open incident keeps it running", lines("a", StatusDone, "b", StatusFailed), OrderRunning},
		{"some through, some refused", lines("a", StatusDone, "b", StatusRejected), OrderPartial},
		{"nothing through", lines("a", StatusRejected), OrderUnfulfilled},
		{"an empty order is complete", nil, OrderCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Derive(tt.in); got != tt.want {
				t.Fatalf("Derive = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestStatusPredicates pins which statuses satisfy a dependent and which have
// stopped moving, since the propagation reads both.
func TestStatusPredicates(t *testing.T) {
	satisfied := map[LineStatus]bool{
		StatusPending: false, StatusRunning: false, StatusDone: true,
		StatusSkipped: true, StatusFailed: false, StatusRejected: false,
		StatusBlocked: false,
	}
	settled := map[LineStatus]bool{
		StatusPending: false, StatusRunning: false, StatusDone: true,
		StatusSkipped: true, StatusFailed: true, StatusRejected: true,
		// Blocked is derived from the others and recomputed on every pass, so it
		// is never a line's own settled state — repairing its cause releases it.
		StatusBlocked: false,
	}
	for s, want := range satisfied {
		if got := s.Satisfied(); got != want {
			t.Errorf("%s.Satisfied() = %v, want %v", s, got, want)
		}
	}
	for s, want := range settled {
		if got := s.Settled(); got != want {
			t.Errorf("%s.Settled() = %v, want %v", s, got, want)
		}
	}
}

// TestOrderWithNoPreconditionsIsUntouched: most catalogues have no precedence at
// all, and that path must not depend on the propagation machinery working.
func TestOrderWithNoPreconditionsIsUntouched(t *testing.T) {
	in := lines("a", StatusFailed, "b", StatusPending)
	got := Propagate(in, nil)

	if s := statusOf(got, "b"); s != StatusPending {
		t.Fatalf("b = %s, want pending — nothing requires anything", s)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d lines, want %d", len(got), len(in))
	}
}

// TestPreconditionOutsideTheOrderDoesNotBlock.
//
// A line can require an item this order does not carry — because the recipient
// already holds it, or because it was never in the basket. The catalogue cannot
// know which, and this is deliberately not where that is decided: whether the
// recipient actually has it is a question only the target system can answer, so
// the item's own provisioning process asks it. Blocking here would refuse every
// order that builds on something already in place, which is most of them.
func TestPreconditionOutsideTheOrderDoesNotBlock(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("vpn", StatusPending), req)

	if s := statusOf(got, "vpn"); s != StatusPending {
		t.Fatalf("vpn = %s, want pending — laptop is not part of this order", s)
	}
}

// An order stays open while a blockage can still be repaired, and settles when it
// cannot. The difference is the cause: a failure is an incident somebody can fix,
// after which the blocked line runs after all; a rejection is a decision that will
// not change, so the line will never run and there is nothing to wait for.

// TestRepairingACauseReleasesTheBlockedLine is why blocked is derived rather than
// stored. The operator repairs the laptop; the VPN must become orderable again
// without anybody rewriting its status by hand.
func TestRepairingACauseReleasesTheBlockedLine(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})

	failed := Propagate(lines("laptop", StatusFailed, "vpn", StatusPending), req)
	if s := statusOf(failed, "vpn"); s != StatusBlocked {
		t.Fatalf("vpn = %s, want blocked", s)
	}

	// The incident is repaired and the laptop provisions.
	repaired := make([]Line, len(failed))
	copy(repaired, failed)
	for i := range repaired {
		if repaired[i].ItemID == "laptop" {
			repaired[i].Status = StatusDone
		}
	}
	got := Propagate(repaired, req)

	if s := statusOf(got, "vpn"); s != StatusPending {
		t.Fatalf("vpn = %s, want pending again", s)
	}
	if by := blockedBy(got, "vpn"); by != "" {
		t.Fatalf("vpn still blocked by %q, want nothing", by)
	}
}

// TestOrderStaysOpenWhileABlockageIsRepairable: the whole point of the decision.
// Somebody can still fix the laptop, so the VPN is not lost and the order is not
// finished.
func TestOrderStaysOpenWhileABlockageIsRepairable(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusFailed, "vpn", StatusPending), req)

	if s := Derive(got); s != OrderRunning {
		t.Fatalf("Derive = %s, want running — the laptop incident is repairable", s)
	}
}

// TestOrderSettlesWhenTheBlockageIsADecision: a rejection will not change, so
// waiting for it is waiting for nothing.
func TestOrderSettlesWhenTheBlockageIsADecision(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusRejected, "vpn", StatusPending), req)

	if s := Derive(got); s != OrderUnfulfilled {
		t.Fatalf("Derive = %s, want unfulfilled — nothing was provisioned and nothing will be", s)
	}
}

// TestARejectionAmongTheCausesSettlesTheLine: a line blocked by both a failure
// and a rejection is finished whatever happens to the failure. Repairing the
// incident cannot release it, because the rejection still stands, and an order
// left open on that basis would never close.
func TestARejectionAmongTheCausesSettlesTheLine(t *testing.T) {
	req := requires(map[string][]string{"z": {"x", "y"}})
	got := Propagate(lines(
		"x", StatusFailed,
		"y", StatusRejected,
		"z", StatusPending,
	), req)

	if by := blockedBy(got, "z"); by != "x,y" {
		t.Fatalf("z blocked by %q, want x,y", by)
	}
	// z is finished whatever happens to x: repairing an incident cannot undo y.
	for _, l := range got {
		if l.ItemID == "z" && !l.Terminal() {
			t.Fatal("z must be terminally blocked — the rejection will not lift")
		}
	}
	// The order itself is still running, because x's incident is still open and
	// x can still provision. Only z is finished.
	if s := Derive(got); s != OrderRunning {
		t.Fatalf("Derive = %s, want running — x is repairable", s)
	}
}

// TestPartialSettlesOnceNothingIsRepairable: some lines through, the rest
// rejected, nothing left to wait for.
func TestPartialSettlesOnceNothingIsRepairable(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines(
		"mailbox", StatusDone,
		"laptop", StatusRejected,
		"vpn", StatusPending,
	), req)

	if s := Derive(got); s != OrderPartial {
		t.Fatalf("Derive = %s, want partial", s)
	}
}

// TestRepairableBlockageKeepsAnOtherwiseFinishedOrderOpen: every other line is
// done, and the order is still not finished because one repairable blockage
// stands. Settling here would tell the orderer their VPN is never coming while
// somebody is actively fixing the reason it has not.
func TestRepairableBlockageKeepsAnOtherwiseFinishedOrderOpen(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines(
		"mailbox", StatusDone,
		"laptop", StatusFailed,
		"vpn", StatusPending,
	), req)

	if s := Derive(got); s != OrderRunning {
		t.Fatalf("Derive = %s, want running", s)
	}
}

// A deadline sits on the incident, not on the order: the incident is the thing
// that is actually stuck, and the order follows it. What the order model owes is
// the ability to say that a failure will not be repaired after all — otherwise
// "the order stays open while a blockage is repairable" means "the order never
// closes", because nothing could ever stop being repairable.

// TestAbandonedFailureSettlesItsDependents: once the incident behind a failure is
// given up on, the lines waiting for it are finished, exactly as a rejection
// finishes them. The cause differs; the consequence does not.
func TestAbandonedFailureSettlesItsDependents(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})
	got := Propagate(lines("laptop", StatusAbandoned, "vpn", StatusPending), req)

	if s := statusOf(got, "vpn"); s != StatusBlocked {
		t.Fatalf("vpn = %s, want blocked", s)
	}
	if by := blockedBy(got, "vpn"); by != "laptop" {
		t.Fatalf("vpn blocked by %q, want laptop", by)
	}
	if s := Derive(got); s != OrderUnfulfilled {
		t.Fatalf("Derive = %s, want unfulfilled — the incident was given up on", s)
	}
}

// TestAbandonedIsNotAFailureWaitingToBeRepaired is the whole distinction: the
// same two orders differ only in whether somebody is still going to fix the
// laptop, and they settle differently.
func TestAbandonedIsNotAFailureWaitingToBeRepaired(t *testing.T) {
	req := requires(map[string][]string{"vpn": {"laptop"}})

	open := Propagate(lines("laptop", StatusFailed, "vpn", StatusPending), req)
	if s := Derive(open); s != OrderRunning {
		t.Errorf("with a live incident Derive = %s, want running", s)
	}

	closed := Propagate(lines("laptop", StatusAbandoned, "vpn", StatusPending), req)
	if s := Derive(closed); s != OrderUnfulfilled {
		t.Errorf("with an abandoned incident Derive = %s, want unfulfilled", s)
	}
}

// TestAbandonedAmongTheCausesSettlesTheLine: same rule as a rejection. One cause
// that will not lift is enough, whatever the others do.
func TestAbandonedAmongTheCausesSettlesTheLine(t *testing.T) {
	req := requires(map[string][]string{"z": {"x", "y"}})
	got := Propagate(lines(
		"x", StatusFailed,
		"y", StatusAbandoned,
		"z", StatusPending,
	), req)

	if by := blockedBy(got, "z"); by != "x,y" {
		t.Fatalf("z blocked by %q, want x,y", by)
	}
	// Same as a rejection: z is finished whatever happens to x.
	for _, l := range got {
		if l.ItemID == "z" && !l.Terminal() {
			t.Fatal("z must be terminally blocked — an abandoned incident does not lift")
		}
	}
	if s := Derive(got); s != OrderRunning {
		t.Fatalf("Derive = %s, want running — x is still repairable", s)
	}
}

// TestAbandonedLineIsNotProvisioned: giving up on an incident does not make the
// line count as delivered. An order of one abandoned line is unfulfilled, not
// partial.
func TestAbandonedLineIsNotProvisioned(t *testing.T) {
	got := Propagate(lines("a", StatusDone, "b", StatusAbandoned), nil)
	if s := Derive(got); s != OrderPartial {
		t.Fatalf("Derive = %s, want partial", s)
	}
	if s := Derive(lines("a", StatusAbandoned)); s != OrderUnfulfilled {
		t.Fatalf("Derive = %s, want unfulfilled", s)
	}
}

// TestAbandonedPredicates: settled like a failure, satisfying nobody.
func TestAbandonedPredicates(t *testing.T) {
	if !StatusAbandoned.Settled() {
		t.Error("abandoned must be settled — nothing further will happen to it")
	}
	if StatusAbandoned.Satisfied() {
		t.Error("abandoned must not satisfy a dependent — it was never provisioned")
	}
}

// TestAnOpenIncidentKeepsTheOrderRunning: the same rule that keeps a blocked line
// open applies to the failure itself, and more directly. A line whose incident is
// being repaired can still provision, so the order it belongs to has not
// finished — reporting it as settled would tell the orderer the outcome is final
// while somebody is working on it.
func TestAnOpenIncidentKeepsTheOrderRunning(t *testing.T) {
	if s := Derive(lines("a", StatusFailed)); s != OrderRunning {
		t.Fatalf("Derive = %s, want running — the incident is open", s)
	}
	if s := Derive(lines("a", StatusAbandoned)); s != OrderUnfulfilled {
		t.Fatalf("Derive = %s, want unfulfilled — the incident was given up on", s)
	}
}

// TestFailedHasAnOutcomeButIsNotTerminal pins the distinction the two predicates
// carry: propagation must leave a failure alone, and Derive must keep waiting on
// it.
func TestFailedHasAnOutcomeButIsNotTerminal(t *testing.T) {
	if !StatusFailed.Settled() {
		t.Error("failed must be settled — propagation must not overwrite it")
	}
	if (Line{ItemID: "a", Status: StatusFailed}).Terminal() {
		t.Error("failed must not be terminal — the incident can still be repaired")
	}
}

// TestAWithdrawnOrderSaysSoRatherThanSayingItFailed.
//
// "Not fulfilled" is what an order says when it tried and did not manage. Telling
// somebody that about their own cancellation invites them to ask why it failed,
// which is the support call the cancellation was supposed to replace.
func TestAWithdrawnOrderSaysSoRatherThanSayingItFailed(t *testing.T) {
	withdrawn := func(ids ...string) []Line {
		out := make([]Line, 0, len(ids))
		for _, id := range ids {
			out = append(out, Line{ItemID: id, Status: StatusCancelled, DecidedBy: "usr_1", DecidedAt: 7})
		}
		return out
	}

	if got := Derive(withdrawn("a", "b")); got != OrderCancelled {
		t.Errorf("everything withdrawn = %q, want cancelled", got)
	}
	// Half delivered and then withdrawn is partly fulfilled, and nothing more
	// honest than that: something was provisioned and the recipient holds it.
	half := append(withdrawn("b"), Line{ItemID: "a", Status: StatusDone})
	if got := Derive(half); got != OrderPartial {
		t.Errorf("half delivered then withdrawn = %q, want partial", got)
	}
	// And a refusal is still unfulfilled: somebody considered it and said no.
	refused := []Line{{ItemID: "a", Status: StatusRejected, DecidedBy: "usr_b", DecidedAt: 7, Reason: "nein"}}
	if got := Derive(refused); got != OrderUnfulfilled {
		t.Errorf("refused = %q, want unfulfilled", got)
	}
}

// TestAWithdrawnPreconditionStopsWhatWaitedOnIt: a cancelled line is not coming,
// so a line requiring it is waiting for nothing — the same shape as a refusal, and
// it does not lift either.
func TestAWithdrawnPreconditionStopsWhatWaitedOnIt(t *testing.T) {
	lines := []Line{
		{ItemID: "account", Status: StatusCancelled, DecidedBy: "usr_1", DecidedAt: 7},
		{ItemID: "laptop", Status: StatusPending},
	}
	got := Propagate(lines, map[string][]string{"laptop": {"account"}})

	var laptop Line
	for _, l := range got {
		if l.ItemID == "laptop" {
			laptop = l
		}
	}
	if laptop.Status != StatusBlocked {
		t.Fatalf("laptop = %q, want blocked behind the withdrawn account", laptop.Status)
	}
	if len(laptop.BlockedBy) != 1 || laptop.BlockedBy[0] != "account" {
		t.Errorf("blockedBy = %v, want the withdrawn line named", laptop.BlockedBy)
	}
	if !laptop.TerminallyBlocked {
		t.Error("the block is not terminal; a withdrawal does not lift the way a repaired incident does")
	}
}
