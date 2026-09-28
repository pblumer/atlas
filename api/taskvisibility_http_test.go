package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// groupTaskBPMN parks on a task offered to the group intmgr.
const groupTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="group-task" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve" name="Approve">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="intmgr"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

// taskKeysSeenBy lists the open tasks one client is shown.
func taskKeysSeenBy(t *testing.T, ts *httptest.Server, c *http.Client, query string) map[uint64]bool {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/tasks"+query, "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	out := map[uint64]bool{}
	for _, tk := range tasks {
		out[tk.Key] = true
	}
	return out
}

// startTask deploys one model and starts it.
func startTask(t *testing.T, ts *httptest.Server, c *http.Client, bpmn string) {
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
	code, body = cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}")
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, body)
	}
}

// The read half of the object axis. A task the model addressed to a group is
// listed to that group's members and to operators and administrators, and to
// nobody else: before this every signed-in account read the whole inbox, so an
// approval offered to the integration managers was shown — with its order — to
// every ordinary user who opened the Tasks app.
func TestAGroupTaskIsShownOnlyToItsGroup(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	ids := map[string]string{}
	for _, u := range []struct{ name, roles string }{
		{"imke", `["user"]`},
		{"mallory", `["user"]`},
		{"otto", `["operator","user"]`},
	} {
		code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+u.name+`","password":"password1","roles":`+u.roles+`}`)
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d (%s)", u.name, code, b)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(b, &created); err != nil {
			t.Fatalf("decode %s: %v", u.name, err)
		}
		ids[u.name] = created.ID
	}
	code, b := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"intmgr"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d (%s)", code, b)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &grp); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+ids["imke"], ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("add imke: %d (%s)", code, b)
	}
	// Signed in after the membership exists, so the session carries the group.
	clients := map[string]*http.Client{}
	for _, u := range []string{"imke", "mallory", "otto"} {
		c := newClient(t)
		if login(t, c, ts, u, "password1") != http.StatusOK {
			t.Fatalf("%s login failed", u)
		}
		clients[u] = c
	}

	startTask(t, ts, admin, groupTaskBPMN)
	startTask(t, ts, admin, openTaskBPMN)
	all := taskKeysSeenBy(t, ts, admin, "")
	if len(all) != 2 {
		t.Fatalf("admin sees %d tasks, want both", len(all))
	}
	var groupKey, openKey, groupInst uint64
	for key := range all {
		code, body := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", key), "")
		if code != http.StatusOK {
			t.Fatalf("admin get %d: %d (%s)", key, code, body)
		}
		var tk struct {
			CandidateGroups    string `json:"candidateGroups"`
			ProcessInstanceKey uint64 `json:"processInstanceKey"`
		}
		if err := json.Unmarshal(body, &tk); err != nil {
			t.Fatalf("decode task: %v", err)
		}
		if tk.CandidateGroups == "intmgr" {
			groupKey, groupInst = key, tk.ProcessInstanceKey
		} else {
			openKey = key
		}
	}
	if groupKey == 0 || openKey == 0 {
		t.Fatalf("could not tell the two tasks apart: %v", all)
	}

	for _, tc := range []struct {
		who       string
		seesGroup bool
	}{
		{"imke", true},     // a member of the group it was offered to
		{"otto", true},     // an operator keeps every task, as every instance
		{"mallory", false}, // neither: the whole point
	} {
		seen := taskKeysSeenBy(t, ts, clients[tc.who], "")
		if seen[groupKey] != tc.seesGroup {
			t.Errorf("%s sees the group task = %v, want %v", tc.who, seen[groupKey], tc.seesGroup)
		}
		// Work addressed to nobody stays everybody's, as it always was.
		if !seen[openKey] {
			t.Errorf("%s does not see the open task", tc.who)
		}
		// The by-key read and the per-instance list are not a way round the list.
		code, _ := cReq(t, clients[tc.who], ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", groupKey), "")
		if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[tc.seesGroup]; code != want {
			t.Errorf("%s get group task = %d, want %d", tc.who, code, want)
		}
		inInstance := taskKeysSeenBy(t, ts, clients[tc.who], fmt.Sprintf("?processInstance=%d", groupInst))
		if inInstance[groupKey] != tc.seesGroup {
			t.Errorf("%s sees the group task by instance = %v, want %v", tc.who, inInstance[groupKey], tc.seesGroup)
		}
	}
}
