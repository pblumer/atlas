package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// addressedTaskBPMN parks on a task the model says belongs to alice.
const addressedTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="addressed-task" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="decide" name="Decide">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="alice"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="end"/>
  </process>
</definitions>`

// openTaskBPMN parks on a task the model says nothing about.
const openTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="open-task" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="anybody" name="Anybody"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="anybody"/>
    <sequenceFlow id="f2" sourceRef="anybody" targetRef="end"/>
  </process>
</definitions>`

// taskFrom deploys one model, starts it, and returns the key of the task it parks
// on. Written here rather than reusing oneAuthTask because these cases turn on
// *which* task the model addressed, so each supplies its own.
func taskFrom(t *testing.T, ts *httptest.Server, c *http.Client, bpmn string) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/deployments", bpmn)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, body = cReq(t, c, ts, "POST",
		fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}"); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, body)
	}
	code, body = cReq(t, c, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (%s)", len(tasks), body)
	}
	return tasks[0].Key
}

// The write half of the object axis, held by the case that found it.
//
// Before this, a signed-in account with nothing but the `user` role could
// complete any open user task by key. The portal is what made that unacceptable
// rather than merely untidy: a customer with a portal account could complete the
// approval task that decides their own order.

// taskAction builds the URL of one task command.
func taskAction(key uint64, action string) string {
	return fmt.Sprintf("/api/v1/tasks/%d/%s", key, action)
}

// twoUsers creates the named accounts on an authenticated server and returns a
// logged-in client for each.
func twoUsers(t *testing.T, ts *httptest.Server, admin *http.Client, names ...string) []*http.Client {
	t.Helper()
	out := make([]*http.Client, 0, len(names))
	for _, u := range names {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+u+`","password":"password1"}`); code != http.StatusCreated {
			t.Fatalf("create %s: %d (%s)", u, code, b)
		}
		c := newClient(t)
		if login(t, c, ts, u, "password1") != http.StatusOK {
			t.Fatalf("%s login failed", u)
		}
		out = append(out, c)
	}
	return out
}

func TestAnAddressedTaskIsNotEverybodysToDecide(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]

	key := taskFrom(t, ts, admin, addressedTaskBPMN)

	// The three commands, each from somebody the task was not addressed to.
	for _, tc := range []struct{ action, body string }{
		{"complete", `{"variables":{"genehmigt":true}}`},
		{"claim", `{}`},
		{"unclaim", ``},
	} {
		code, body := cReq(t, mallory, ts, "POST", taskAction(key, tc.action), tc.body)
		if code != http.StatusForbidden {
			t.Errorf("a stranger's %s = %d (%s), want 403", tc.action, code, body)
		}
	}

	// Releasing is gated with the rest, and has to be: a caller who cannot complete
	// a task but can release it takes it away from the person who can, which leaves
	// an approval nobody holds and an order stuck until an operator repairs it.
	if got := assigneeOf(t, ts, admin, key); got != "alice" {
		t.Fatalf("assignee = %q after the attempts, want alice", got)
	}

	// And the person it belongs to still decides it.
	if code, b := cReq(t, alice, ts, "POST", taskAction(key, "complete"),
		`{"variables":{"genehmigt":true}}`); code != http.StatusOK {
		t.Fatalf("alice complete: %d (%s)", code, b)
	}
}

// TestUnaddressedWorkStaysOpen is the other half of the line, and the reason this
// is not simply "only the assignee". A user task that names neither an assignee
// nor a candidate group is unassigned work: BPMN says nothing about who it is
// for, the shared inbox has always let anybody pick it up, and narrowing that
// would change what existing installations do while closing nothing — there is no
// holder to impersonate.
func TestUnaddressedWorkStaysOpen(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anybody := twoUsers(t, ts, admin, "bob")[0]

	key := taskFrom(t, ts, admin, openTaskBPMN)
	if code, b := cReq(t, anybody, ts, "POST", taskAction(key, "complete"), `{}`); code != http.StatusOK {
		t.Fatalf("an unaddressed task refused its taker: %d (%s)", code, b)
	}
}

// TestAnOperatorReachesEveryTask: withholding a task from somebody who can cancel
// the instance underneath it would be a gate in front of an open door.
func TestAnOperatorReachesEveryTask(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_ = twoUsers(t, ts, admin, "alice")

	key := taskFrom(t, ts, admin, addressedTaskBPMN)
	if code, b := cReq(t, admin, ts, "POST", taskAction(key, "complete"), `{}`); code != http.StatusOK {
		t.Fatalf("an administrator was refused somebody else's task: %d (%s)", code, b)
	}
}
