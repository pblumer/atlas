package order

import (
	"strings"
	"testing"
)

// Changing a position after it was ordered
// (ADR-draft-amending-an-order-line).
//
// Two acts, kept apart. What is **held** is never changed in place — that would be
// a different claim about the past, and the access record exists to answer what
// somebody had and when. What was **recorded about it** may be corrected, and how
// depends on where the line stands.

func lineAt(status LineStatus) Line {
	return Line{
		ItemID: "laptop", Status: status,
		ConfigForm: "form_laptop",
		Config:     map[string]string{"kostenstelle": "4711"},
	}
}

// TestCorrectingARequestLeavesNoAmendment.
//
// Nothing was attempted, so the order is still only a request. An amendment here
// would tell a reader that something was delivered under the old answers, which is
// the one thing that did not happen.
func TestCorrectingARequestLeavesNoAmendment(t *testing.T) {
	for _, status := range []LineStatus{StatusPending, StatusBlocked} {
		got, err := AmendAnswers(lineAt(status), map[string]string{"kostenstelle": "0815"},
			"usr_1", 1700, "wrong cost centre")
		if err != nil {
			t.Fatalf("correcting a %s line: %v", status, err)
		}
		if got.Config["kostenstelle"] != "0815" {
			t.Errorf("%s: the correction did not take: %v", status, got.Config)
		}
		if len(got.Amendments) != 0 {
			t.Errorf("%s: a request that was never acted on recorded an amendment: %+v",
				status, got.Amendments)
		}
	}
}

// TestCorrectingWhatIsHeldIsRecordedAsACorrection.
//
// The laptop is at the wrong site and correcting the record does not move it. An
// overwrite would leave the order saying something that was never true of the
// delivery, and a reader could not tell the corrected record from an accurate one.
func TestCorrectingWhatIsHeldIsRecordedAsACorrection(t *testing.T) {
	for _, status := range []LineStatus{StatusDone, StatusReturning, StatusReturnFailed} {
		got, err := AmendAnswers(lineAt(status), map[string]string{"kostenstelle": "0815"},
			"usr_1", 1700, "wrong cost centre")
		if err != nil {
			t.Fatalf("correcting a %s line: %v", status, err)
		}
		if got.Config["kostenstelle"] != "0815" {
			t.Errorf("%s: the correction did not take: %v", status, got.Config)
		}
		if len(got.Amendments) != 1 {
			t.Fatalf("%s: the correction was not recorded: %+v", status, got.Amendments)
		}
		a := got.Amendments[0]
		if a.Was["kostenstelle"] != "4711" {
			t.Errorf("%s: the amendment lost what it replaced: %+v", status, a)
		}
		if a.By != "usr_1" || a.At != 1700 || a.Reason != "wrong cost centre" {
			t.Errorf("%s: the amendment does not say who, when or why: %+v", status, a)
		}
	}
}

// TestASecondCorrectionKeepsTheFirst: the amendments are a history and not a slot.
// Two corrections say the details were wrong twice, which is a different fact from
// their having been wrong once.
func TestASecondCorrectionKeepsTheFirst(t *testing.T) {
	once, err := AmendAnswers(lineAt(StatusDone), map[string]string{"kostenstelle": "0815"},
		"usr_1", 1700, "first")
	if err != nil {
		t.Fatalf("first correction: %v", err)
	}
	twice, err := AmendAnswers(once, map[string]string{"kostenstelle": "9999"}, "usr_2", 1800, "second")
	if err != nil {
		t.Fatalf("second correction: %v", err)
	}
	if len(twice.Amendments) != 2 {
		t.Fatalf("the second correction replaced the first: %+v", twice.Amendments)
	}
	if twice.Amendments[0].Was["kostenstelle"] != "4711" ||
		twice.Amendments[1].Was["kostenstelle"] != "0815" {
		t.Errorf("the history does not read oldest first: %+v", twice.Amendments)
	}
}

// TestARunningLineIsNotCorrectedUnderneathItsProcess.
//
// A provisioning process has the line now, which is a conversation with a system
// this server does not control. Changing the answers underneath it would leave the
// record saying one thing and the target system having been told another, with
// nothing anywhere saying which the delivery followed.
func TestARunningLineIsNotCorrectedUnderneathItsProcess(t *testing.T) {
	_, err := AmendAnswers(lineAt(StatusRunning), map[string]string{"kostenstelle": "0815"},
		"usr_1", 1700, "")
	if err == nil {
		t.Fatal("a running line was corrected while its process was acting on it")
	}
	// And says what to do instead, because "no" alone produces a support call.
	if !strings.Contains(err.Error(), "wait for it to finish") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// TestAClosedLineHasNothingToCorrect: rejected, cancelled and abandoned are
// records of requests that produced nothing. Editing the cost centre of a laptop
// nobody ever received serves no reader.
func TestAClosedLineHasNothingToCorrect(t *testing.T) {
	for _, status := range []LineStatus{StatusRejected, StatusCancelled, StatusAbandoned} {
		if _, err := AmendAnswers(lineAt(status), map[string]string{"k": "v"},
			"usr_1", 1700, ""); err == nil {
			t.Errorf("a %s line was corrected", status)
		}
	}
}

// TestALineThatAsksNothingHasNothingToCorrect.
func TestALineThatAsksNothingHasNothingToCorrect(t *testing.T) {
	plain := Line{ItemID: "account", Status: StatusPending}
	if _, err := AmendAnswers(plain, map[string]string{"k": "v"}, "usr_1", 1700, ""); err == nil {
		t.Error("a product that asks for no details accepted some")
	}
}

// TestACorrectionNamesWhoAndWhen: both required, for the reason every settled
// transition here requires them.
func TestACorrectionNamesWhoAndWhen(t *testing.T) {
	if _, err := AmendAnswers(lineAt(StatusDone), nil, "", 1700, ""); err == nil {
		t.Error("a correction was recorded with nobody to attribute it to")
	}
	if _, err := AmendAnswers(lineAt(StatusDone), nil, "usr_1", 0, ""); err == nil {
		t.Error("a correction was recorded with no moment")
	}
}

// --- Removing one position ---------------------------------------------------

func orderOf(lines ...Line) Order {
	return Order{ID: "ord_1", Lines: lines, Requires: map[string][]string{}}
}

// TestOnePositionCanBeWithdrawnWithoutTheRest.
//
// The whole-order form exists because somebody cancelling an order is not making a
// series of per-line decisions. Removing one position is exactly one decision, and
// offering only the whole-order form made somebody who no longer wanted the second
// screen take back the laptop with it.
func TestOnePositionCanBeWithdrawnWithoutTheRest(t *testing.T) {
	o := orderOf(
		Line{ItemID: "laptop", Status: StatusPending},
		Line{ItemID: "screen", Status: StatusPending},
	)
	got, err := CancelLine(o, "screen", "usr_1", 1700, "not needed")
	if err != nil {
		t.Fatalf("withdrawing one position: %v", err)
	}
	for _, l := range got.Lines {
		switch l.ItemID {
		case "screen":
			if l.Status != StatusCancelled {
				t.Errorf("the screen is %s, want cancelled", l.Status)
			}
			if l.DecidedBy != "usr_1" || l.DecidedAt != 1700 {
				t.Errorf("the withdrawal does not say who or when: %+v", l)
			}
		case "laptop":
			if l.Status != StatusPending {
				t.Errorf("withdrawing the screen also took back the laptop: %s", l.Status)
			}
		}
	}
}

// TestWithdrawingOnePositionUnblocksWhatWaitedOnIt.
//
// A withdrawn line is a root cause like a refused one, and anything waiting on it
// is waiting for nothing. Skipping that pass leaves a line blocked forever behind
// something that will never arrive.
func TestWithdrawingOnePositionUnblocksWhatWaitedOnIt(t *testing.T) {
	o := Order{
		ID: "ord_1",
		Lines: []Line{
			{ItemID: "account", Status: StatusPending},
			{ItemID: "laptop", Status: StatusPending},
		},
		Requires: map[string][]string{"laptop": {"account"}},
	}
	got, err := CancelLine(o, "account", "usr_1", 1700, "")
	if err != nil {
		t.Fatalf("withdrawing the account: %v", err)
	}
	for _, l := range got.Lines {
		if l.ItemID != "laptop" {
			continue
		}
		if l.Status != StatusBlocked {
			t.Fatalf("the laptop is %s; it needs the account that was withdrawn, so it "+
				"is waiting for nothing and the record has to say so", l.Status)
		}
		if len(l.BlockedBy) != 1 || l.BlockedBy[0] != "account" {
			t.Errorf("the laptop does not name what stopped it: %+v", l.BlockedBy)
		}
	}
}

// TestAnIntegralPositionIsNotWithdrawnOnItsOwn.
//
// The basket refuses to deselect a part its whole always carries — a workplace is
// not a workplace without its account — and a rule enforced when ordering and not
// afterwards is not a rule.
func TestAnIntegralPositionIsNotWithdrawnOnItsOwn(t *testing.T) {
	o := orderOf(
		Line{ItemID: "workplace", Status: StatusPending, Includes: []string{"account"}},
		Line{ItemID: "account", Status: StatusPending, Integral: true},
	)
	_, err := CancelLine(o, "account", "usr_1", 1700, "")
	if err == nil {
		t.Fatal("an integral part was taken back on its own, which the basket does not " +
			"let anybody do when ordering")
	}
	// It names what carries it, because the answer somebody needs is "take back the
	// workplace instead", not "no".
	if !strings.Contains(err.Error(), "workplace") {
		t.Errorf("the refusal does not say what carries it: %v", err)
	}
}

// TestWithdrawingSomethingNotInTheOrderSaysSo.
func TestWithdrawingSomethingNotInTheOrderSaysSo(t *testing.T) {
	o := orderOf(Line{ItemID: "laptop", Status: StatusPending})
	if _, err := CancelLine(o, "screen", "usr_1", 1700, ""); err == nil {
		t.Error("a line the order does not carry was withdrawn")
	}
}

// TestARunningPositionIsNotWithdrawn: the same rule the whole-order form follows,
// asked of one line. Stopping a provisioning halfway is not withdrawal but a
// half-provisioned account nobody owns.
func TestARunningPositionIsNotWithdrawn(t *testing.T) {
	o := orderOf(Line{ItemID: "laptop", Status: StatusRunning})
	if _, err := CancelLine(o, "laptop", "usr_1", 1700, ""); err == nil {
		t.Error("a running line was withdrawn")
	}
}

// TestARefusalNamesEveryWholeThatCarriesIt.
//
// Two wholes in one order can both carry the same part — a workplace and a
// laptop-bundle that each include the account — and the person reading the refusal
// has to withdraw whichever they meant. Naming one of them would send them to take
// back the wrong thing.
func TestARefusalNamesEveryWholeThatCarriesIt(t *testing.T) {
	o := orderOf(
		Line{ItemID: "workplace", Status: StatusPending, Includes: []string{"account"}},
		Line{ItemID: "homeoffice", Status: StatusPending, Includes: []string{"account"}},
		Line{ItemID: "telefonie", Status: StatusPending, Includes: []string{"account"}},
		Line{ItemID: "account", Status: StatusPending, Integral: true},
	)
	_, err := CancelLine(o, "account", "usr_1", 1700, "")
	if err == nil {
		t.Fatal("an integral part was taken back on its own")
	}
	for _, whole := range []string{"homeoffice", "telefonie", "workplace"} {
		if !strings.Contains(err.Error(), whole) {
			t.Errorf("the refusal does not name %q, so somebody would withdraw the "+
				"wrong whole: %v", whole, err)
		}
	}
}

// TestAnIntegralLineWithNothingCarryingItIsStillRefused.
//
// The defensive half. A line is marked integral because something in the order
// carried it at placement, so a carrier is normally there — but the refusal must
// not turn into an acceptance if that ever stops holding. Refusing with a plainer
// sentence is the safe direction: the alternative is taking back a part the
// catalogue says its whole cannot exist without.
func TestAnIntegralLineWithNothingCarryingItIsStillRefused(t *testing.T) {
	o := orderOf(Line{ItemID: "account", Status: StatusPending, Integral: true})
	_, err := CancelLine(o, "account", "usr_1", 1700, "")
	if err == nil {
		t.Fatal("an integral line with no carrier on record was withdrawn")
	}
	if !strings.Contains(err.Error(), "not ordered on its own") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}
