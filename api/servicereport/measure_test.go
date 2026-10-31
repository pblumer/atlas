package servicereport

import (
	"math"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
)

var zurich = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}()

// at is a wall-clock moment in Zurich, in unix nanoseconds.
func at(y int, m time.Month, d, h int) int64 {
	return time.Date(y, m, d, h, 0, 0, 0, zurich).UnixNano()
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestWorkingDaysSkipsTheWeekend(t *testing.T) {
	cases := []struct {
		name     string
		from, to int64
		want     float64
	}{
		{"same day", at(2026, 10, 26, 9), at(2026, 10, 26, 21), 0.5},                 // Monday
		{"Friday noon to Monday noon", at(2026, 10, 30, 12), at(2026, 11, 2, 12), 1}, // over a weekend
		{"a whole week", at(2026, 10, 26, 0), at(2026, 11, 2, 0), 5},
		{"Saturday to Sunday", at(2026, 10, 31, 8), at(2026, 11, 1, 20), 0},
		{"backwards", at(2026, 10, 27, 0), at(2026, 10, 26, 0), 0},
	}
	for _, c := range cases {
		got := WorkingDays(time.Unix(0, c.from), time.Unix(0, c.to), zurich)
		if !near(got, c.want) {
			t.Errorf("%s: %v working days, want %v", c.name, got, c.want)
		}
	}
}

func line(item string, status order.LineStatus, days int) order.Line {
	l := order.Line{ItemID: item, Status: status, Approval: order.Approval{Kind: "none"}}
	if days > 0 {
		l.ServiceLevel = &catalog.ServiceLevel{Name: "Standard", DeliveryDays: days}
	}
	return l
}

func TestEachPositionGetsItsVerdict(t *testing.T) {
	monday := at(2026, 10, 26, 9)
	now := time.Unix(0, at(2026, 11, 4, 9)) // the following Wednesday: 7 working days later

	approved := line("phone", order.StatusDone, 2)
	approved.Approval.Kind = "superior"
	approved.ApprovedAt = at(2026, 10, 28, 9) // Wednesday; the clock starts here

	waiting := line("tablet", order.StatusPending, 2)
	waiting.Approval.Kind = "superior"

	orders := []order.Order{{
		ID: "o1", ReleaseID: "rel_1", Recipient: "ada", Orderer: "bob", CreatedAt: monday,
		Lines: []order.Line{
			line("laptop", order.StatusDone, 3),     // provisioned Thursday: 3 days → met
			line("screen", order.StatusDone, 1),     // provisioned Thursday: 3 days → missed
			approved,                                // provisioned Friday: 2 days after approval → met
			line("headset", order.StatusRunning, 3), // still open after 7 days → overdue
			line("mouse", order.StatusRunning, 10),  // still open, within 10 → open
			waiting,                                 // approval owed → awaiting approval
			line("dock", order.StatusFailed, 3),     // failed
			line("bag", order.StatusDone, 0),        // no promise
			line("pen", order.StatusDone, 3),        // done, but no hold to say when
			line("cable", order.StatusRejected, 3),  // not measured
			line("desk", order.StatusSkipped, 3),    // not measured
		},
	}}
	holds := []Hold{
		{Principal: "ada", ItemID: "laptop", OrderID: "o1", Since: at(2026, 10, 29, 9)},
		{Principal: "ada", ItemID: "screen", OrderID: "o1", Since: at(2026, 10, 29, 9)},
		{Principal: "ada", ItemID: "phone", OrderID: "o1", Since: at(2026, 10, 30, 9)},
		{Principal: "ada", ItemID: "bag", OrderID: "o1", Since: at(2026, 10, 27, 9)},
		// Another order's hold of the same product does not date this line.
		{Principal: "ada", ItemID: "pen", OrderID: "o0", Since: at(2026, 10, 27, 9)},
	}
	got := map[string]Position{}
	for _, p := range Measure(orders, holds, now, zurich) {
		got[p.ItemID] = p
	}
	want := map[string]Verdict{
		"laptop": Met, "screen": Missed, "phone": Met, "headset": Overdue, "mouse": Open,
		"tablet": AwaitingApproval, "dock": Failed, "bag": NoPromise, "pen": Unmeasured,
	}
	for item, v := range want {
		if got[item].Verdict != v {
			t.Errorf("%s: verdict %q, want %q (%+v)", item, got[item].Verdict, v, got[item])
		}
	}
	for _, gone := range []string{"cable", "desk"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s was measured; a line that delivered nothing has no lead time", gone)
		}
	}
	if !near(got["laptop"].LeadDays, 3) {
		t.Errorf("laptop lead %v, want 3", got["laptop"].LeadDays)
	}
	if !near(got["phone"].LeadDays, 2) || !near(got["phone"].ApprovalWaitDays, 2) {
		t.Errorf("phone lead %v / approval wait %v, want 2 / 2", got["phone"].LeadDays, got["phone"].ApprovalWaitDays)
	}
	if !near(got["headset"].LeadDays, 7) {
		t.Errorf("an open position's lead is its elapsed time: %v, want 7", got["headset"].LeadDays)
	}
	if p := got["laptop"]; p.Recipient != "ada" || p.Orderer != "bob" || p.OrderID != "o1" ||
		p.ReleaseID != "rel_1" || p.DeliveryDays != 3 || p.ServiceLevel != "Standard" || p.ProvisionedAt == 0 {
		t.Errorf("laptop position lost its facts: %+v", p)
	}
}

func TestAReturnedPositionIsDatedFromItsHistory(t *testing.T) {
	orders := []order.Order{{ID: "o1", Recipient: "ada", CreatedAt: at(2026, 10, 26, 9),
		Lines: []order.Line{line("laptop", order.StatusReturned, 3)}}}
	holds := []Hold{{Principal: "ada", ItemID: "laptop", OrderID: "o1", Since: at(2026, 10, 27, 9)}}
	ps := Measure(orders, holds, time.Unix(0, at(2026, 11, 20, 9)), zurich)
	if len(ps) != 1 || ps[0].Verdict != Met || !near(ps[0].LeadDays, 1) {
		t.Fatalf("returned position measured as %+v, want met after 1 day", ps)
	}
}
