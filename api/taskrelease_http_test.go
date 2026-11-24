package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestReleasingATaskAddressedToOnePersonDoesNotOpenItToEverybody: a task nobody is
// named for is open work, which is what ADR-0317 decided for a task the model
// addressed to nobody. Releasing a task empties its assignee, and for a task the
// model addressed to one person and to no group that left exactly such a task: the
// approval a product routes to its approver became everybody's to answer the moment
// that approver released it, the person who had ordered it included.
//
// Releasing gives a task back to the group it came from. Where the model names a
// person and no group there is nobody to give it back to, so the release is refused
// and says what to do instead — hand it to somebody.
func TestReleasingATaskAddressedToOnePersonDoesNotOpenItToEverybody(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	users := twoUsers(t, ts, admin, "alice", "bob")
	alice, bob := users[0], users[1]

	start := `{"processId":"atlas-genehmigung-fix","variables":{
		"orderId":"ord_x","itemId":"vpn","positionId":"vpn","approvalRef":"alice",
		"provisionProcess":"prov","recipient":"usr_kunde","orderer":"bob","atlasApiBase":"http://127.0.0.1:1"}}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if code != http.StatusOK || json.Unmarshal(listRows(t, body), &tasks) != nil || len(tasks) != 1 {
		t.Fatalf("tasks: %d (%s)", code, body)
	}
	task := fmt.Sprintf("/api/v1/tasks/%d", tasks[0].Key)

	// Before anything is released the task is alice's alone.
	if code, b := cReq(t, bob, ts, "POST", task+"/complete", `{"variables":{"genehmigt":true}}`); code == http.StatusOK {
		t.Fatalf("somebody the task does not name completed it: %s", b)
	}

	if code, b := cReq(t, alice, ts, "POST", task+"/unclaim", ""); code != http.StatusConflict {
		t.Errorf("releasing a task addressed to one person and no group = %d (%s), want 409", code, b)
	}
	// Whatever the release answered, the task must not have become everybody's.
	if code, b := cReq(t, bob, ts, "POST", task+"/complete", `{"variables":{"genehmigt":true}}`); code == http.StatusOK {
		t.Errorf("after the approver released it, the orderer answered their own approval: %s", b)
	}
	// And it is still hers: she can answer it, or hand it to somebody.
	if code, b := cReq(t, alice, ts, "POST", task+"/complete", `{"variables":{"genehmigt":false,"begruendung":"nein"}}`); code != http.StatusOK {
		t.Errorf("the approver can no longer answer her own task: %d (%s)", code, b)
	}
}
