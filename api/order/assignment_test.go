package order

import (
	"strings"
	"testing"
)

// A deadline on an approval reminds, then escalates to a deputy. It never
// decides. Silence is not a refusal, and treating it as one would write into a
// record kept forever a decision nobody made — the same thing Abandon exists to
// prevent on the incident path.

// deputies builds a lookup of who stands in for whom.
func deputies(m map[string]string) func(string) string {
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
	got, err := Escalate(a, deputies(map[string]string{"usr_boss": "usr_deputy"}), 2000)
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
	got, err := Escalate(a, deputies(nil), 2000)
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
	both := deputies(map[string]string{"usr_a": "usr_b", "usr_b": "usr_a"})

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
	self := deputies(map[string]string{"usr_a": "usr_a"})
	if _, err := Escalate(Assign("laptop", "usr_a", 1000), self, 2000); err == nil {
		t.Fatal("escalating to the current approver must fail")
	}
}

// TestEscalationChainsThroughSeveralDeputies, each recorded, so the record shows
// the route an approval took rather than only where it ended.
func TestEscalationChainsThroughSeveralDeputies(t *testing.T) {
	d := deputies(map[string]string{"usr_a": "usr_b", "usr_b": "usr_c", "usr_c": "usr_d"})

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
	if _, err := Escalate(a, deputies(map[string]string{"usr_boss": "usr_d"}), 0); err == nil {
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
		deputies(map[string]string{"usr_a": "usr_b"}), 2000)
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
	if _, err := Escalate(broken, deputies(map[string]string{"": "usr_b"}), 2000); err == nil {
		t.Fatal("escalating an assignment with no approver must fail")
	}
}

// TestEscalationDoesNotReturnToSomebodyMidChain: the loop need not be between
// two people. A deputises for B, B for C, and C back to B — following it would
// return the approval to somebody who already declined to act on it.
func TestEscalationDoesNotReturnToSomebodyMidChain(t *testing.T) {
	d := deputies(map[string]string{"usr_a": "usr_b", "usr_b": "usr_c", "usr_c": "usr_b"})

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
