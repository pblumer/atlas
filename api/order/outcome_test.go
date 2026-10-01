package order

import (
	"errors"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
)

// TestAnActsEndingWithoutARightIsReportedOnItsOwn: a provision that failed and a
// return that failed change no right, so their outcomes are reported on their own,
// as failed, under the attempt the line recorded; the refusal of a line is the
// rejected of its provision; and a reporter that fails makes the report a 500 the
// caller can retry.
func TestAnActsEndingWithoutARightIsReportedOnItsOwn(t *testing.T) {
	s, _, inv, _ := inventoryFixture(t,
		Line{ItemID: "laptop", Status: StatusRunning, ProvisionProcess: "prov", DeprovisionProcess: "deprov",
			Instances: []LineInstance{{Key: 7, Operation: catalog.OpProvision, CommandID: "order:ord_1:laptop:provision:1"}}},
		Line{ItemID: "vpn", Status: StatusReturning, ProvisionProcess: "prov", DeprovisionProcess: "deprov",
			Instances: []LineInstance{{Key: 8, Operation: catalog.OpDeprovision}, {Key: 9, Operation: catalog.OpDeprovision}}},
	)
	var reported []Outcome
	s.ReportOutcomesTo(func(o Outcome) error { reported = append(reported, o); return nil })

	if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"failed"}`, "id", "ord_1", "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("report failed: %d (%s)", rec.Code, rec.Body)
	}
	if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"returnFailed"}`, "id", "ord_1", "item", "vpn"); rec.Code != http.StatusOK {
		t.Fatalf("report returnFailed: %d (%s)", rec.Code, rec.Body)
	}
	if len(inv.granted)+len(inv.revoked) != 0 {
		t.Fatalf("a failure moved the inventory: %v %v", inv.granted, inv.revoked)
	}
	if len(reported) != 2 {
		t.Fatalf("reported %d outcomes, want 2: %+v", len(reported), reported)
	}
	if p := reported[0]; p.CommandID != "order:ord_1:laptop:provision:1" || p.Effect != "provision" ||
		p.Outcome != "failed" || p.InstanceKey != 7 || p.EventType != "laptop.provision.failed" || p.Principal != "usr_ada" {
		t.Errorf("the failed provision = %+v", p)
	}
	if r := reported[1]; r.CommandID != "order:ord_1:vpn:deprovision:2" || r.Effect != "deprovision" ||
		r.Outcome != "failed" || r.InstanceKey != 9 {
		t.Errorf("the failed return = %+v, want the second attempt and its instance", r)
	}

	// A reporter that cannot write is the caller's to retry, as the inventory is.
	s2, _, _, _ := inventoryFixture(t, Line{ItemID: "laptop", Status: StatusRunning, ProvisionProcess: "prov"})
	s2.ReportOutcomesTo(func(Outcome) error { return errors.New("log full") })
	if rec := do(t, s2.HandleReport, reporter(), "POST", `{"status":"failed"}`, "id", "ord_1", "item", "laptop"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a reporter that fails: %d (%s), want 500", rec.Code, rec.Body)
	}
}

// TestARefusalIsTheRejectedOfTheProvision: an approver's refusal is reported as the
// provision's rejected outcome, for the first attempt, before anything is woken.
func TestARefusalIsTheRejectedOfTheProvision(t *testing.T) {
	s := newService(t)
	var reported []Outcome
	s.ReportOutcomesTo(func(o Outcome) error { reported = append(reported, o); return nil })
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}
	if rec := do(t, s.HandleDecide, op, "POST", `{"by":"usr_boss","reason":"kein Budget"}`,
		"id", placed.ID, "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("reject: %d (%s)", rec.Code, rec.Body)
	}
	if len(reported) != 1 || reported[0].Outcome != "rejected" || reported[0].Effect != "provision" ||
		reported[0].CommandID != AttemptID(placed.ID, "laptop", "provision", 1) {
		t.Fatalf("reported = %+v, want the provision rejected", reported)
	}

	// Without a reporter nothing is written and nothing fails.
	quiet := newService(t)
	placed = decode[Order](t, do(t, quiet.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))
	if rec := do(t, quiet.HandleDecide, op, "POST", `{"by":"usr_boss","reason":"kein Budget"}`,
		"id", placed.ID, "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("reject without a reporter: %d (%s)", rec.Code, rec.Body)
	}
}

// TestAnOutcomeIsPublishedUnderTheTypeTheActionDeclares: a declared event type
// wins; otherwise the message and the outcome; and an action with no message is
// named after its item.
func TestAnOutcomeIsPublishedUnderTheTypeTheActionDeclares(t *testing.T) {
	declared := catalog.Action{Key: "extend", Message: "mailbox.storage.extend",
		Outcomes: map[string]string{"completed": "mailbox.storage.extended"}}
	for _, c := range []struct {
		a       catalog.Action
		outcome string
		want    string
	}{
		{declared, "completed", "mailbox.storage.extended"},
		{declared, "failed", "mailbox.storage.extend.failed"},
		{catalog.Action{Key: "provision"}, "rejected", "laptop.provision.rejected"},
	} {
		if got := EventTypeOf("laptop", c.a, c.outcome); got != c.want {
			t.Errorf("EventTypeOf(%s, %s) = %q, want %q", c.a.Key, c.outcome, got, c.want)
		}
	}
	if (Outcome{}).Set() || !(Outcome{CommandID: "c"}).Set() {
		t.Error("Set does not say whether an outcome names a command")
	}
}
