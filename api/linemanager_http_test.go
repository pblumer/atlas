package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The line manager as an administrator sets it and as an escalation reads it
// (ADR-0470).

func TestAnAdministratorSetsSomebodysLineManager(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	bruno := createUserWithRoles(t, admin, ts.URL, "bruno", `["user"]`)

	manager := func() string {
		t.Helper()
		code, body := cReq(t, admin, ts, "GET", "/api/v1/users/"+alice, "")
		if code != http.StatusOK {
			t.Fatalf("get alice: %d %s", code, body)
		}
		var u struct {
			ManagerID string `json:"managerId"`
		}
		if err := json.Unmarshal(body, &u); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return u.ManagerID
	}

	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+alice, `{"managerId":"`+bruno+`"}`); code != http.StatusOK {
		t.Fatalf("set the manager: %d %s", code, body)
	}
	if got := manager(); got != bruno {
		t.Fatalf("managerId = %q, want bruno's id", got)
	}
	// Changing something else leaves it alone.
	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+alice, `{"displayName":"Alice"}`); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, body)
	}
	if got := manager(); got != bruno {
		t.Errorf("managerId = %q after an unrelated change, want it kept", got)
	}
	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+alice, `{"managerId":""}`); code != http.StatusOK {
		t.Fatalf("clear the manager: %d %s", code, body)
	}
	if got := manager(); got != "" {
		t.Errorf("managerId = %q, want it cleared", got)
	}
}

func TestALineManagerMustBeSomebodyElseWithAnAccount(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Einkauf"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &grp)

	for _, c := range []struct{ name, managerID, want string }{
		{"nobody", "usr_0000000000000000000000", "no account"},
		{"a group", grp.ID, "no account"},
		{"herself", alice, "own line manager"},
	} {
		code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+alice, `{"managerId":"`+c.managerID+`"}`)
		if code != http.StatusBadRequest || !strings.Contains(string(body), c.want) {
			t.Errorf("%s: = %d %s, want a 400 saying %q", c.name, code, body, c.want)
		}
	}
}

func TestAnEscalationCanAskForTheApproversLineManager(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice", "bruno")
	ids := map[string]string{}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("list users: %d %s", code, body)
	}
	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("decode users: %v (%s)", err, body)
	}
	for _, u := range users {
		ids[u.Username] = u.ID
	}
	// alice reports to bruno, and bruno to alice: a loop the guard has to catch even
	// though the approval started with alice's username and the line is kept in ids.
	for who, boss := range map[string]string{"alice": "bruno", "bruno": "alice"} {
		if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+ids[who], `{"managerId":"`+ids[boss]+`"}`); code != http.StatusOK {
			t.Fatalf("set %s's manager: %d %s", who, code, body)
		}
	}
	orderID := anApprovalWaitingOn(t, ts, admin)

	code, got := move(t, ts, admin, orderID, "vpn", "escalate", `{"lineManager":true}`)
	if code != http.StatusOK {
		t.Fatalf("escalate = %d", code)
	}
	if got.Approver != ids["bruno"] || got.Stalled {
		t.Fatalf("= %+v, want it with bruno (%s)", got, ids["bruno"])
	}
	if who := assigneeOfTheOnlyTask(t, ts, admin); who != ids["bruno"] {
		t.Errorf("the task is with %q; the hop moved nothing", who)
	}

	// Bruno's manager is alice, who already had it.
	code, got = move(t, ts, admin, orderID, "vpn", "escalate", `{"lineManager":true}`)
	if code != http.StatusOK {
		t.Fatalf("second escalate = %d", code)
	}
	if !got.Stalled {
		t.Errorf("= %+v, want a stall rather than a hop back to alice", got)
	}
}

func TestAnEscalationAsksForASuperiorOrForTheLineManagerNotBoth(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice", "bruno")
	orderID := anApprovalWaitingOn(t, ts, admin)
	code, body := cReqMove(t, ts, admin, orderID, "vpn", "escalate", `{"superior":"bruno","lineManager":true}`)
	if code != http.StatusBadRequest {
		t.Errorf("both = %d %s, want a 400", code, body)
	}
}

func TestAnEscalationWithNoLineManagerStalls(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice")
	orderID := anApprovalWaitingOn(t, ts, admin)
	code, got := move(t, ts, admin, orderID, "vpn", "escalate", `{"lineManager":true}`)
	if code != http.StatusOK || !got.Stalled || got.Approver != "alice" {
		t.Errorf("= %d %+v, want it stalled and still with alice", code, got)
	}
}
