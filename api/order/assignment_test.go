package order

import (
	"errors"
	"strings"
	"testing"
)

// A deadline on an approval reminds, then escalates to a deputy. It never
// decides. Silence is not a refusal, and treating it as one would write into a
// record kept forever a decision nobody made — the same thing Abandon exists to
// prevent on the incident path.

// superiors builds a directory lookup: who an approver reports to.
func superiors(m map[string]string) func(string) string {
	return func(of string) string { return m[of] }
}

func chain(a Assignment) string {
	var out []string
	for _, e := range a.Escalations {
		out = append(out, e.From+">"+e.To)
	}
	return strings.Join(out, " ")
}

func TestEscalationMovesTheApprovalToTheDeputy(t *testing.T) {
	a := Assign("laptop", "usr_boss", 1000)
	got, err := Escalate(a, superiors(map[string]string{"usr_boss": "usr_deputy"}), 2000)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}

	if got.Approver != "usr_deputy" {
		t.Errorf("approver = %s, want usr_deputy", got.Approver)
	}
	if chain(got) != "usr_boss>usr_deputy" {
		t.Errorf("chain = %q, want usr_boss>usr_deputy", chain(got))
	}
	if got.Escalations[0].At != 2000 {
		t.Errorf("escalated at %d, want 2000", got.Escalations[0].At)
	}
	// Who it was originally for survives every escalation: an approval that
	// travelled must still say where it started.
	if got.Original != "usr_boss" {
		t.Errorf("original = %s, want usr_boss", got.Original)
	}
}

// TestEscalationWithoutADeputyIsRefused: there is nowhere to send it, and
// silently leaving it where it is would hide that the deadline achieved nothing.
func TestEscalationWithoutADeputyIsRefused(t *testing.T) {
	a := Assign("laptop", "usr_boss", 1000)
	got, err := Escalate(a, superiors(nil), 2000)
	if err == nil {
		t.Fatal("Escalate with no deputy must fail")
	}
	if got.Approver != "usr_boss" {
		t.Fatalf("approver changed to %s on a refused call", got.Approver)
	}
}

// TestEscalationDoesNotCircleBack: A deputises for B and B for A is an ordinary
// arrangement between two colleagues, and following it would hand the approval
// back and forth until the order is forgotten.
func TestEscalationDoesNotCircleBack(t *testing.T) {
	both := superiors(map[string]string{"usr_a": "usr_b", "usr_b": "usr_a"})

	first, err := Escalate(Assign("laptop", "usr_a", 1000), both, 2000)
	if err != nil {
		t.Fatalf("first escalation: %v", err)
	}
	if _, err := Escalate(first, both, 3000); err == nil {
		t.Fatal("escalating back to somebody who already held it must fail")
	}
}

// TestSelfDeputyIsRefused: somebody recorded as their own deputy would absorb the
// deadline forever and nothing would ever move.
func TestSelfDeputyIsRefused(t *testing.T) {
	self := superiors(map[string]string{"usr_a": "usr_a"})
	if _, err := Escalate(Assign("laptop", "usr_a", 1000), self, 2000); err == nil {
		t.Fatal("escalating to the current approver must fail")
	}
}

// TestEscalationChainsThroughSeveralDeputies, each recorded, so the record shows
// the route an approval took rather than only where it ended.
func TestEscalationChainsThroughSeveralDeputies(t *testing.T) {
	d := superiors(map[string]string{"usr_a": "usr_b", "usr_b": "usr_c", "usr_c": "usr_d"})

	a := Assign("laptop", "usr_a", 1000)
	for i, at := range []int64{2000, 3000, 4000} {
		var err error
		if a, err = Escalate(a, d, at); err != nil {
			t.Fatalf("escalation %d: %v", i+1, err)
		}
	}
	if a.Approver != "usr_d" {
		t.Errorf("approver = %s, want usr_d", a.Approver)
	}
	if chain(a) != "usr_a>usr_b usr_b>usr_c usr_c>usr_d" {
		t.Errorf("chain = %q", chain(a))
	}
}

// TestEscalationNeedsAMoment: an escalation kept forever with no date cannot be
// placed against the deadline it came from.
func TestEscalationNeedsAMoment(t *testing.T) {
	a := Assign("laptop", "usr_boss", 1000)
	if _, err := Escalate(a, superiors(map[string]string{"usr_boss": "usr_d"}), 0); err == nil {
		t.Fatal("Escalate with no moment must fail")
	}
}

// TestAssignNeedsAnApprover: an approval nobody holds is a task in nobody's
// inbox, which is the state this whole mechanism exists to make impossible.
func TestAssignNeedsAnApprover(t *testing.T) {
	if err := Assign("laptop", "", 1000).Valid(); err == nil {
		t.Fatal("an assignment with no approver must not be valid")
	}
	if err := Assign("laptop", "usr_a", 1000).Valid(); err != nil {
		t.Fatalf("Valid = %v, want nil", err)
	}
}

// TestSilenceIsNeverARejection is the rule stated as a test, because it is the
// one somebody will be tempted to relax.
//
// There is no transition from an elapsed deadline to a refusal: Reject takes the
// principal who decided, so a clock has nothing to pass and therefore no call to
// make. The only thing a deadline can do to an assignment is move it.
func TestSilenceIsNeverARejection(t *testing.T) {
	line := Line{ItemID: "laptop", Status: StatusRunning}

	// What a timer would have to do, and cannot.
	if _, err := Reject(line, "", 9999, "no response"); err == nil {
		t.Fatal("a rejection with no deciding principal must be refused")
	}

	// What it does instead.
	a, err := Escalate(Assign("laptop", "usr_a", 1000),
		superiors(map[string]string{"usr_a": "usr_b"}), 2000)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if a.Approver != "usr_b" {
		t.Fatalf("approver = %s, want the deputy", a.Approver)
	}
	if line.Status != StatusRunning {
		t.Fatal("the line itself must be untouched by an escalation")
	}
}

// TestAssignmentValidRejectsAnUnnamedLine: an assignment that names no line
// cannot be matched back to the order it belongs to.
func TestAssignmentValidRejectsAnUnnamedLine(t *testing.T) {
	if err := (Assignment{Approver: "usr_a"}).Valid(); err == nil {
		t.Fatal("an assignment with no item must not be valid")
	}
}

// TestEscalateRefusesAnInvalidAssignment: a deadline firing on a broken record
// must not manufacture a valid-looking escalation out of it.
func TestEscalateRefusesAnInvalidAssignment(t *testing.T) {
	broken := Assignment{ItemID: "laptop"} // nobody holds it
	if _, err := Escalate(broken, superiors(map[string]string{"": "usr_b"}), 2000); err == nil {
		t.Fatal("escalating an assignment with no approver must fail")
	}
}

// TestEscalationDoesNotReturnToSomebodyMidChain: the loop need not be between
// two people. A deputises for B, B for C, and C back to B — following it would
// return the approval to somebody who already declined to act on it.
func TestEscalationDoesNotReturnToSomebodyMidChain(t *testing.T) {
	d := superiors(map[string]string{"usr_a": "usr_b", "usr_b": "usr_c", "usr_c": "usr_b"})

	a, err := Escalate(Assign("laptop", "usr_a", 1000), d, 2000)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if a, err = Escalate(a, d, 3000); err != nil {
		t.Fatalf("second: %v", err)
	}
	if _, err := Escalate(a, d, 4000); err == nil {
		t.Fatal("escalating back to usr_b, who already held it, must fail")
	}
}

// The chain escalates upwards — a deputy is the approver's superior, read from
// the directory, which is the same lookup the "superior" approval rule already
// uses. Upwards ends somewhere: nobody is above the top, and a directory can
// loop. When it ends, the approval must become visible rather than quietly sit
// where nobody is looking, and somebody must be able to start it moving again.

// TestExhaustedChainIsDistinguishable: an operator's tooling has to tell "this
// cannot escalate any further" from "this record is broken", because only the
// first is a thing to hand to a person.
func TestExhaustedChainIsDistinguishable(t *testing.T) {
	top := Assign("laptop", "usr_ceo", 1000) // nobody above
	_, err := Escalate(top, superiors(nil), 2000)
	if !errors.Is(err, ErrNoFurtherEscalation) {
		t.Fatalf("err = %v, want ErrNoFurtherEscalation", err)
	}

	// A directory loop is the same operational case: there is nowhere new to go.
	loop := superiors(map[string]string{"usr_a": "usr_b", "usr_b": "usr_a"})
	one, err := Escalate(Assign("laptop", "usr_a", 1000), loop, 2000)
	if err != nil {
		t.Fatalf("first escalation: %v", err)
	}
	if _, err := Escalate(one, loop, 3000); !errors.Is(err, ErrNoFurtherEscalation) {
		t.Fatalf("err = %v, want ErrNoFurtherEscalation", err)
	}

	// A broken record is not.
	if _, err := Escalate(Assignment{ItemID: "laptop"}, superiors(nil), 2000); errors.Is(err, ErrNoFurtherEscalation) {
		t.Fatal("an invalid assignment must not report as an exhausted chain")
	}
}

// TestStallMakesAStuckApprovalVisible: without this the approval sits in the last
// holder's inbox and the order waits on it with nothing anywhere saying so.
func TestStallMakesAStuckApprovalVisible(t *testing.T) {
	a := Assign("laptop", "usr_ceo", 1000)
	if a.Stalled() {
		t.Fatal("a fresh assignment must not be stalled")
	}

	got, err := Stall(a, 2000)
	if err != nil {
		t.Fatalf("Stall: %v", err)
	}
	if !got.Stalled() || got.StalledAt != 2000 {
		t.Fatalf("stalled = %v at %d, want true at 2000", got.Stalled(), got.StalledAt)
	}
	// It is still with whoever last held it: stalling reports a fact, it does not
	// take the task away from them.
	if got.Approver != "usr_ceo" {
		t.Errorf("approver = %s, want usr_ceo", got.Approver)
	}
}

func TestStallNeedsAMomentAndAValidAssignment(t *testing.T) {
	if _, err := Stall(Assign("laptop", "usr_a", 1000), 0); err == nil {
		t.Error("Stall with no moment must fail")
	}
	if _, err := Stall(Assignment{ItemID: "laptop"}, 2000); err == nil {
		t.Error("Stall of an assignment with no approver must fail")
	}
}

// TestReassignClosesTheCircle: visibility with no way to act on it is just a
// quieter kind of stuck. A person puts the approval somewhere, and it moves again.
func TestReassignClosesTheCircle(t *testing.T) {
	stalled, err := Stall(Assign("laptop", "usr_ceo", 1000), 2000)
	if err != nil {
		t.Fatalf("Stall: %v", err)
	}

	got, err := Reassign(stalled, "usr_new", "usr_admin", 3000)
	if err != nil {
		t.Fatalf("Reassign: %v", err)
	}
	if got.Approver != "usr_new" {
		t.Errorf("approver = %s, want usr_new", got.Approver)
	}
	if got.Stalled() {
		t.Error("a reassigned approval must no longer be stalled")
	}
	if chain(got) != "usr_ceo>usr_new" {
		t.Errorf("chain = %q, want the hop recorded", chain(got))
	}
	if got.Escalations[0].By != "usr_admin" {
		t.Errorf("hop by %q, want usr_admin — a person did this, and the record says so",
			got.Escalations[0].By)
	}
}

// TestEscalationRecordsNoAuthor is the other half of the same field: a hop the
// deadline made has nobody to name, and that absence is what distinguishes an
// automatic move from somebody's decision to intervene.
func TestEscalationRecordsNoAuthor(t *testing.T) {
	got, err := Escalate(Assign("laptop", "usr_a", 1000),
		superiors(map[string]string{"usr_a": "usr_b"}), 2000)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if got.Escalations[0].By != "" {
		t.Fatalf("hop by %q, want nobody — a clock moved it", got.Escalations[0].By)
	}
}

// TestNobodyReassignsAnApprovalToThemselves.
//
// The escalation path exists so a stuck approval reaches somebody who will act on
// it. Taking it for yourself and approving it is the one move that turns the
// mechanism into its own bypass — no role is needed, only access to an approval
// that has stalled, which is by definition one nobody is watching. The chain
// records it, but a chain records it *afterwards*, and nobody reads escalation
// histories routinely.
//
// Refusing to and from the same principal is a property of the model rather than
// a permission: whoever genuinely needs the approval can still be given it, by
// somebody else, which is the whole difference.
func TestNobodyReassignsAnApprovalToThemselves(t *testing.T) {
	stalled, err := Stall(Assign("laptop", "usr_ceo", 1000), 2000)
	if err != nil {
		t.Fatalf("Stall: %v", err)
	}

	got, err := Reassign(stalled, "usr_greedy", "usr_greedy", 3000)
	if err == nil {
		t.Fatal("reassigning an approval to oneself must fail")
	}
	if got.Approver != "usr_ceo" || !got.Stalled() {
		t.Fatalf("assignment changed on a refused call: %+v", got)
	}

	// The same person may still route it to somebody else, and somebody else may
	// still route it to them.
	if _, err := Reassign(stalled, "usr_other", "usr_greedy", 3000); err != nil {
		t.Errorf("reassigning to a third party: %v, want it allowed", err)
	}
	if _, err := Reassign(stalled, "usr_greedy", "usr_admin", 3000); err != nil {
		t.Errorf("somebody else giving it to them: %v, want it allowed", err)
	}
}

// TestReassignMayGoToSomebodyWhoAlreadyHeldIt. The loop guard exists to stop a
// clock cycling an approval between two colleagues; a person choosing to send it
// back to the original approver knows something the guard does not.
func TestReassignMayGoToSomebodyWhoAlreadyHeldIt(t *testing.T) {
	a, err := Escalate(Assign("laptop", "usr_a", 1000),
		superiors(map[string]string{"usr_a": "usr_b"}), 2000)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if _, err := Reassign(a, "usr_a", "usr_admin", 3000); err != nil {
		t.Fatalf("Reassign back to usr_a: %v, want it allowed", err)
	}
}

func TestReassignNeedsATargetAnAuthorAndAMoment(t *testing.T) {
	a := Assign("laptop", "usr_a", 1000)
	if _, err := Reassign(Assignment{ItemID: "laptop"}, "usr_b", "usr_admin", 3000); err == nil {
		t.Error("reassigning an assignment with no approver must fail")
	}
	if _, err := Reassign(a, "", "usr_admin", 3000); err == nil {
		t.Error("Reassign with no target must fail")
	}
	if _, err := Reassign(a, "usr_b", "", 3000); err == nil {
		t.Error("Reassign with no author must fail")
	}
	if _, err := Reassign(a, "usr_b", "usr_admin", 0); err == nil {
		t.Error("Reassign with no moment must fail")
	}
	if _, err := Reassign(a, "usr_a", "usr_admin", 3000); err == nil {
		t.Error("Reassign to the current approver must fail — nothing would move")
	}
}

// TestEscalationResumesAfterAReassignment: the new holder's own superior is where
// the deadline goes next, so intervening puts the approval back on the normal path
// rather than into a special case.
func TestEscalationResumesAfterAReassignment(t *testing.T) {
	sup := superiors(map[string]string{"usr_new": "usr_newboss"})
	a, err := Reassign(Assign("laptop", "usr_ceo", 1000), "usr_new", "usr_admin", 3000)
	if err != nil {
		t.Fatalf("Reassign: %v", err)
	}
	got, err := Escalate(a, sup, 4000)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if got.Approver != "usr_newboss" {
		t.Fatalf("approver = %s, want usr_newboss", got.Approver)
	}
}

// The order is where an assignment lives, and reading one back is how anything
// later knows an approval ever moved.

func TestAnOrderCarriesAtMostOneAssignmentPerLine(t *testing.T) {
	o := Order{ID: "ord_1"}
	if _, ok := o.AssignmentFor("vpn"); ok {
		t.Error("a fresh order already carries an assignment")
	}

	first := Assign("vpn", "alice", 100)
	o = o.WithAssignment(first)
	o = o.WithAssignment(Assign("laptop", "bruno", 100))

	got, ok := o.AssignmentFor("vpn")
	if !ok || got.Approver != "alice" {
		t.Fatalf("= %+v, %v", got, ok)
	}
	if len(o.Assignments) != 2 {
		t.Fatalf("assignments = %d, want one per line", len(o.Assignments))
	}

	// Replacing rather than appending: an approval that moved twice is one
	// approval with two hops, not two approvals.
	moved, err := Escalate(first, func(string) string { return "carla" }, 200)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	o = o.WithAssignment(moved)
	if len(o.Assignments) != 2 {
		t.Fatalf("assignments = %d after a hop, want still one per line", len(o.Assignments))
	}
	if got, _ := o.AssignmentFor("vpn"); got.Approver != "carla" {
		t.Errorf("vpn is with %q, want carla", got.Approver)
	}

	// A copy, so a caller holding the previous order keeps its own history.
	before := Order{Assignments: []Assignment{first}}
	after := before.WithAssignment(moved)
	if before.Assignments[0].Approver != "alice" {
		t.Error("the earlier order was rewritten underneath its holder")
	}
	if after.Assignments[0].Approver != "carla" {
		t.Error("the new order does not carry the hop")
	}
}
