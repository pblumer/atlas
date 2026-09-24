package api

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// TestTheShopOffersNoUnaddressedTaskToAnOrderer.
//
// A task the model addressed to nobody is anybody's to pick up in Tasks, and the
// task routes keep it that way. The shop does not draw its form for somebody who
// does not operate: the reader of an order's row is first of all the person who
// ordered, and a manual step of their own order with a button beside it invites
// them to report it done themselves.
func TestTheShopOffersNoUnaddressedTaskToAnOrderer(t *testing.T) {
	s := &Server{authEnabled: true}
	orderer := &httpapi.Principal{UserID: "usr_1", Username: "alice", Roles: []string{RoleUser}}
	operator := &httpapi.Principal{UserID: "usr_2", Username: "olga", Roles: []string{RoleOperator}}
	unaddressed := taskResp{Name: "Laptop ausgeben"}

	if s.mayWorkShopTask(orderer, unaddressed) {
		t.Error("the shop offers the orderer an unaddressed task of their own order")
	}
	if !s.mayWorkShopTask(operator, unaddressed) {
		t.Error("the shop withholds an unaddressed task from an operator, who may work every task")
	}
	// And a task addressed to the reader is theirs, here as in Tasks.
	if !s.mayWorkShopTask(orderer, taskResp{Assignee: "alice"}) {
		t.Error("the shop withholds a task assigned to the reader")
	}
	if s.mayWorkShopTask(orderer, taskResp{Assignee: "bob"}) {
		t.Error("the shop offers the reader somebody else's task")
	}
}

// TestAHolderIsNotShownTheOrderersAnswers: an order in front of somebody because
// they hold one of its tasks carries what was ordered and where it stands, and not
// what the orderer answered on the products' forms, nor the order's bookkeeping.
func TestAHolderIsNotShownTheOrderersAnswers(t *testing.T) {
	o := order.Order{ID: "ord_1", Lines: []order.Line{{
		ItemID: "vpn", Status: order.StatusPending,
		ConfigForm: "kst", Config: map[string]string{"kostenstelle": "4711"},
		Amendments: []order.AmendedAnswers{{}},
		Instances:  []order.LineInstance{{Key: 7}},
	}}}
	got := forHolder(o)
	l := got.Lines[0]
	if l.Config != nil || l.Amendments != nil || l.Instances != nil {
		t.Errorf("a holder's view carries %v / %v / %v", l.Config, l.Amendments, l.Instances)
	}
	if o.Lines[0].Config == nil {
		t.Error("forHolder changed the order it was handed rather than a copy")
	}
	if l.ItemID != "vpn" || l.Status != order.StatusPending {
		t.Errorf("a holder's view lost what was ordered or where it stands: %+v", l)
	}
}
