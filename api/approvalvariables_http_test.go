package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestAnApprovalAnswersItsFormAndWritesNothingElse: the shipped approval models
// decide with an operator's token. After the task they call Atlas's own API at
// atlasApiBase, for the position orderId and positionId name, and report
// approvalRef as the person who approved. All of those are variables of the
// instance, and completing a task writes whatever variables the request carries
// into the scope its results land in — the instance, for a task with no output
// mapping. So the person holding an approval could send, beside their answer, a
// different order, a different approver, or a different address for the call that
// carries the token.
//
// The approval tasks now map out exactly what their form asks for. A task with
// output mappings keeps what it is sent in its own scope (ADR-0068), so the
// decision and its reason reach the instance and nothing else does. One case per
// model, because each has its own task.
func TestAnApprovalAnswersItsFormAndWritesNothingElse(t *testing.T) {
	for _, tc := range []struct {
		processID string
		start     string
		// who answers: the named approver, or the administrator for the group model
		approver string
	}{
		{"atlas-genehmigung-fix", `"approvalRef":"alice"`, "alice"},
		{"atlas-genehmigung-rolle", `"approvalRef":"alice"`, "root"},
		{"atlas-genehmigung-vorgesetzter", `"approvalRef":"alice","vorgesetzter":"alice"`, "alice"},
	} {
		t.Run(tc.processID, func(t *testing.T) {
			ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
			admin := newClient(t)
			if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
				t.Fatal("admin login failed")
			}
			approver := twoUsers(t, ts, admin, "alice")[0]
			if tc.approver == "root" {
				// The group model routes to whoever holds a group; an administrator may
				// work any task, which is enough to send the same answer.
				approver = admin
			}

			start := fmt.Sprintf(`{"processId":%q,"variables":{
				"orderId":"ord_x","itemId":"vpn","positionId":"vpn","provisionProcess":"prov",
				"recipient":"usr_kunde","orderer":"root","atlasApiBase":"http://127.0.0.1:1",%s}}`,
				tc.processID, tc.start)
			if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
				t.Fatalf("start %s: %d (%s)", tc.processID, code, b)
			}
			code, body := cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
			if code != http.StatusOK {
				t.Fatalf("list tasks: %d (%s)", code, body)
			}
			var tasks []struct {
				Key                uint64 `json:"key"`
				ProcessInstanceKey uint64 `json:"processInstanceKey"`
			}
			if err := json.Unmarshal(listRows(t, body), &tasks); err != nil || len(tasks) != 1 {
				t.Fatalf("tasks: %v (%s)", err, body)
			}

			// The answer, and beside it everything the calls after the task are built from.
			answer := `{"variables":{"genehmigt":false,"begruendung":"nein",
				"atlasApiBase":"http://elsewhere.example","orderId":"ord_other","itemId":"other",
				"positionId":"other","approvalRef":"mallory","vorgesetzter":"mallory"}}`
			if code, b := cReq(t, approver, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), answer); code != http.StatusOK {
				t.Fatalf("the approver was refused their own task: %d (%s)", code, b)
			}

			code, body = cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", tasks[0].ProcessInstanceKey), "")
			if code != http.StatusOK {
				t.Fatalf("read the instance's variables: %d (%s)", code, body)
			}
			var vars map[string]any
			if err := json.Unmarshal(body, &vars); err != nil {
				t.Fatalf("decode variables: %v (%s)", err, body)
			}
			for name, want := range map[string]any{
				"atlasApiBase": "http://127.0.0.1:1", "orderId": "ord_x", "itemId": "vpn",
				"positionId": "vpn", "approvalRef": "alice",
			} {
				if vars[name] != want {
					t.Errorf("%s = %v after the approval was answered, want %v: the answer rewrote what the calls after it are built from", name, vars[name], want)
				}
			}
			// What the form does ask for still arrives.
			if vars["genehmigt"] != false || vars["begruendung"] != "nein" {
				t.Errorf("the decision did not reach the instance: genehmigt = %v, begruendung = %v", vars["genehmigt"], vars["begruendung"])
			}
		})
	}
}
