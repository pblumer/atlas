package order

import (
	"strings"
	"testing"
)

// Giving back what an order granted: what may go, in what order, and what the
// record then says.

// aHeldOrder is a workplace whose laptop needs the account, both provisioned.
func aHeldOrder() Order {
	return Order{
		ID:       "ord_1",
		Requires: map[string][]string{"laptop": {"account"}},
		Lines: []Line{
			{ItemID: "account", Status: StatusDone, DeprovisionProcess: "konto-entziehen"},
			{ItemID: "laptop", Status: StatusDone, DeprovisionProcess: "laptop-einziehen"},
		},
	}
}

// TestNothingIsGivenBackFromUnderSomethingThatNeedsIt is the precedence graph read
// backwards, and the reason this is not simply "run the deprovisioning".
//
// Provisioning ordered the account before the laptop that needs it. Giving back
// runs the other way: an account revoked under a laptop that still uses it leaves
// the laptop working until somebody notices, or not working for a reason nobody
// connects to this.
func TestNothingIsGivenBackFromUnderSomethingThatNeedsIt(t *testing.T) {
	o := aHeldOrder()

	err := Returnable(o, "account")
	if err == nil {
		t.Fatal("the account was given back under a laptop that still needs it")
	}
	if !strings.Contains(err.Error(), "laptop") {
		t.Errorf("the refusal does not name what is in the way: %v", err)
	}

	// The laptop goes first.
	if err := Returnable(o, "laptop"); err != nil {
		t.Fatalf("the laptop, which nothing needs, was refused: %v", err)
	}
	o, err = Returning(o, "laptop", 100)
	if err != nil {
		t.Fatalf("Returning: %v", err)
	}

	// And the account waits until the laptop is *confirmed* gone, not merely until
	// its revocation was asked for. A requested return has not happened: the access
	// is there until it confirms, and the first cut of this guard let the account
	// be revoked out from under a laptop that was still working.
	if err := Returnable(o, "account"); err == nil {
		t.Error("the account went while the laptop's own return was only under way")
	}
	// A revocation that ran and failed is the plainest case of all: the laptop is
	// still there, and now somebody knows it.
	failed, err := Apply(o, "laptop", StatusReturnFailed, 150)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := Returnable(failed, "account"); err == nil {
		t.Error("the account went while the laptop's revocation had failed")
	}

	back, err := Apply(o, "laptop", StatusReturned, 200)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := Returnable(back, "account"); err != nil {
		t.Errorf("the account is still refused once the laptop is confirmed gone: %v", err)
	}
}

// TestARevocationIsNotAskedForTwice: two revocations racing against one target
// system is how a half-deleted account happens.
func TestARevocationIsNotAskedForTwice(t *testing.T) {
	o, err := Returning(aHeldOrder(), "laptop", 100)
	if err != nil {
		t.Fatalf("Returning: %v", err)
	}
	err = Returnable(o, "laptop")
	if err == nil {
		t.Fatal("a return already under way was asked for again")
	}
	if !strings.Contains(err.Error(), "already going back") {
		t.Errorf("the refusal reads as if nobody holds it: %v", err)
	}
}

// TestAFailedRevocationCanBeTriedAgain — the ordinary repair: fix the target
// system, ask once more.
func TestAFailedRevocationCanBeTriedAgain(t *testing.T) {
	o, err := Returning(aHeldOrder(), "laptop", 100)
	if err != nil {
		t.Fatalf("Returning: %v", err)
	}
	o, err = Apply(o, "laptop", StatusReturnFailed, 150)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := Returnable(o, "laptop"); err != nil {
		t.Fatalf("a failed revocation could not be retried: %v", err)
	}
	if _, err := Returning(o, "laptop", 200); err != nil {
		t.Errorf("Returning after a failure: %v", err)
	}
}

// TestAFailedRevocationIsNotAFailedProvisioning. Read as Failed, a reader would
// conclude nobody has it — and the precedence guard would let the account
// underneath be revoked out from under something very much still there.
func TestAFailedRevocationIsNotAFailedProvisioning(t *testing.T) {
	if StatusReturnFailed == StatusFailed {
		t.Fatal("the two are the same value")
	}
	if !StatusReturnFailed.Held() {
		t.Error("a failed revocation reads as not held; the thing is still there")
	}
	if StatusFailed.Held() {
		t.Error("a failed provisioning reads as held")
	}
	if StatusReturnFailed.Settled() {
		t.Error("a failed revocation is settled, so the order would close over it " +
			"while somebody still holds what they gave back")
	}
	// Only the return that was asked for reports it.
	if _, err := Apply(aHeldOrder(), "laptop", StatusReturnFailed, 100); err == nil {
		t.Error("a held line was recorded as a failed revocation with nothing running")
	}
}

func TestOnlyWhatIsHeldCanBeGivenBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		line Line
	}{
		{"never provisioned", Line{ItemID: "a", Status: StatusPending, DeprovisionProcess: "p"}},
		// Skipped means the recipient already had it from somewhere else. It was
		// never this order's to revoke.
		{"already had it", Line{ItemID: "a", Status: StatusSkipped, DeprovisionProcess: "p"}},
		{"already given back", Line{ItemID: "a", Status: StatusReturned, DeprovisionProcess: "p"}},
		{"on its way back", Line{ItemID: "a", Status: StatusReturning, DeprovisionProcess: "p"}},
		{"withdrawn", Line{ItemID: "a", Status: StatusCancelled, DeprovisionProcess: "p"}},
		// A return with nothing to run is a status change pretending to be an act.
		{"nothing to run", Line{ItemID: "a", Status: StatusDone}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Order{ID: "ord_1", Lines: []Line{tc.line}}
			if err := Returnable(o, "a"); err == nil {
				t.Fatalf("a %s line was given back", tc.line.Status)
			}
		})
	}

	if err := Returnable(Order{ID: "ord_1"}, "nothing"); err == nil {
		t.Error("a line the order does not carry was given back")
	}
	if _, err := Returning(aHeldOrder(), "laptop", 0); err == nil {
		t.Error("a return with no moment was accepted")
	}
}

// TestAReturnIsReportedOnlyByTheReturnThatWasAskedFor: without this, a
// provisioning worker reporting "returned" would take a line somebody holds and
// record it as given back, with nothing having run.
func TestAReturnIsReportedOnlyByTheReturnThatWasAskedFor(t *testing.T) {
	o := aHeldOrder()
	if _, err := Apply(o, "laptop", StatusReturned, 100); err == nil {
		t.Fatal("a held line was recorded as given back without a return being asked for")
	}

	moving, err := Returning(o, "laptop", 100)
	if err != nil {
		t.Fatalf("Returning: %v", err)
	}
	back, err := Apply(moving, "laptop", StatusReturned, 200)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, l := range back.Lines {
		if l.ItemID == "laptop" && l.Status != StatusReturned {
			t.Errorf("laptop = %q, want returned", l.Status)
		}
	}
}

// TestAReturnedLineIsNotACancelledOne. A record that says "cancelled" where
// somebody held a laptop for three weeks is a record that lost three weeks, and an
// audit that cannot tell the two apart cannot answer who had access when.
func TestAReturnedLineIsNotACancelledOne(t *testing.T) {
	if StatusReturned == StatusCancelled {
		t.Fatal("the two are the same value")
	}
	if !StatusReturned.Settled() {
		t.Error("a returned line is not settled, so the order would never close")
	}
	if StatusReturning.Settled() {
		t.Error("a line on its way back is settled; an order would report itself finished " +
			"while an account is half-deleted")
	}
	if StatusReturned.Held() {
		t.Error("a line already given back still reads as held")
	}
	// A revocation that has only been *asked for* has not happened. Until it
	// confirms, the access is there.
	if !StatusReturning.Held() {
		t.Error("a return under way reads as gone before anything confirmed it")
	}
	if !StatusDone.Held() || StatusSkipped.Held() {
		t.Error("Held does not mean what this order granted")
	}
}

// TestReturnUsesTheProcessThatWasInForce: a product whose deprovisioning was
// changed afterwards must not revoke an older grant by the newer rules.
func TestReturnUsesTheProcessThatWasInForce(t *testing.T) {
	if got := ReturnProcessOf(aHeldOrder(), "account"); got != "konto-entziehen" {
		t.Errorf("= %q, want the process the order froze", got)
	}
	if got := ReturnProcessOf(aHeldOrder(), "nothing"); got != "" {
		t.Errorf("= %q for a line that is not there", got)
	}
}
