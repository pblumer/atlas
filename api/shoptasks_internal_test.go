package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
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

// TestWhomATaskWaitsForIsNamed: a person by display name, groups by name whether
// the model wrote their id or their name, nobody for an unaddressed task — and an
// approval by the rule its line is approved under.
func TestWhomATaskWaitsForIsNamed(t *testing.T) {
	users, err := newUserStore(t.TempDir())
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	if err := users.Save(User{ID: "usr_bob", Username: "bob", DisplayName: "Bob Muster"}); err != nil {
		t.Fatalf("save bob: %v", err)
	}
	if err := users.Save(User{ID: "usr_eve", Username: "eve"}); err != nil {
		t.Fatalf("save eve: %v", err)
	}
	groups, err := newGroupStore(t.TempDir())
	if err != nil {
		t.Fatalf("newGroupStore: %v", err)
	}
	if err := groups.Save(group{ID: "grp_it", Name: "IT-Support"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	s := &Server{users: users, groups: groups}
	plain := order.Line{ItemID: "laptop"}
	approved := order.Line{ItemID: "vpn", Approval: order.Approval{Kind: "role", Ref: "grp_it"}}

	cases := []struct {
		name     string
		tr       taskResp
		line     order.Line
		approval bool
		want     shopTaskHolder
	}{
		{"person", taskResp{Assignee: "bob"}, plain, false, shopTaskHolder{Kind: "person", Name: "Bob Muster"}},
		{"person without a display name", taskResp{Assignee: "eve"}, plain, false, shopTaskHolder{Kind: "person", Name: "eve"}},
		{"unknown person", taskResp{Assignee: "zoe"}, plain, false, shopTaskHolder{Kind: "person", Name: "zoe"}},
		{"groups by id and by name", taskResp{CandidateGroups: "grp_it, Admins,"}, plain, false,
			shopTaskHolder{Kind: "group", Name: "IT-Support, Admins"}},
		{"unaddressed", taskResp{}, plain, false, shopTaskHolder{Kind: "open"}},
		{"an approval by its rule", taskResp{CandidateGroups: "grp_it"}, approved, true,
			shopTaskHolder{Kind: "role", Name: "IT-Support"}},
	}
	for _, c := range cases {
		if got := s.holderOf(c.tr, c.line, c.approval); got != c.want {
			t.Errorf("%s: holder = %+v, want %+v", c.name, got, c.want)
		}
	}

	if got := s.principalName("usr_bob"); got != "Bob Muster" {
		t.Errorf("principalName(usr_bob) = %q, want the display name", got)
	}
	if got := s.principalName("usr_eve"); got != "eve" {
		t.Errorf("principalName(usr_eve) = %q, want the username where there is no display name", got)
	}
	if got := s.principalName("usr_gone"); got != "usr_gone" {
		t.Errorf("principalName(usr_gone) = %q, want the id where no account answers", got)
	}
	// Without a directory at all the id still says who.
	if got := (&Server{}).principalName("usr_bob"); got != "usr_bob" {
		t.Errorf("principalName with no directory = %q, want the id", got)
	}
	if got := (&Server{}).groupNames("grp_it"); got != "grp_it" {
		t.Errorf("groupNames with no directory = %q, want the id", got)
	}
}

// TestTheShopTaskGateOutsideIdentity: with authentication off there is one user and
// every task is theirs, as on the task routes; a request with no principal where
// authentication is on is nobody's and may answer nothing.
func TestTheShopTaskGateOutsideIdentity(t *testing.T) {
	if !(&Server{}).mayWorkShopTask(nil, taskResp{Assignee: "bob"}) {
		t.Error("single-user mode withholds a task from its one user")
	}
	if (&Server{authEnabled: true}).mayWorkShopTask(nil, taskResp{}) {
		t.Error("a request with no principal is offered a task")
	}
}

// TestNobodyAsksForNobodysTasks: a request carrying no principal is answered with
// the empty shape, not an error and not somebody else's tasks.
func TestNobodyAsksForNobodysTasks(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).handleShopTasks(rec, httptest.NewRequest(http.MethodGet, "/api/v1/shop/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got shopTasksResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Tasks == nil || got.Orders == nil || len(got.Tasks)+len(got.Orders) != 0 {
		t.Errorf("answer = %+v, want two empty lists", got)
	}
}

// TestOnlyAStartThatNamesAPositionIsRecorded: a server without an order service
// records nothing and asks nothing of one, and a variable that is not a string does
// not name an order.
func TestOnlyAStartThatNamesAPositionIsRecorded(t *testing.T) {
	s := &Server{} // no order service: a call reaching it would panic
	s.notePositionInstance([]model.VariableValue{
		{Name: "orderId", Kind: model.VarString, Text: "ord_1"},
		{Name: "positionId", Kind: model.VarString, Text: "vpn"},
	}, 7, "p")
	if v, ok := nativeString(&model.VariableValue{Name: "orderId", Kind: model.VarNumber, Text: "1"}); ok || v != "" {
		t.Errorf("a number was read as the order id: %q", v)
	}
}
