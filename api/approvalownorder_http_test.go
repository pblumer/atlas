package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestNobodyApprovesAnOrderTheyPlacedThemselves: a `fixed` approval names a person at
// the product and a `role` approval a group, and neither asked who had placed the
// order. So the person the product names, or a member of that group, ordered the
// product and approved it — the one case an approval exists to rule out. Action
// approvals have always refused it; order approvals now do too, on both ways an
// approval is decided: the approvals page and the task itself.
//
// The orderer here is an administrator as well, who may otherwise work any task: the
// rule is about whose order it is, not about what the account may do elsewhere.
func TestNobodyApprovesAnOrderTheyPlacedThemselves(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]

	// A product root approves, in a catalogue root maintains — and orders from.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"Katalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	for _, p := range []struct{ id, approver string }{{"vpn", "root"}, {"laptop", "alice"}} {
		product := fmt.Sprintf(`{"id":%q,"homeCatalog":%q,"state":"active","texts":{"de":%q},`+
			`"approval":{"kind":"fixed","ref":%q},"provisionProcess":"prov","deprovisionProcess":"deprov"}`, p.id, cat, p.id, p.approver)
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save product %s: %d (%s)", p.id, code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["vpn","laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer products: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["vpn","laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d (%s)", code, body)
	}
	orderID := idOf(t, body)

	// The two approvals, started as the fulfilment starts them.
	taskOf := map[string]uint64{}
	for item, approver := range map[string]string{"vpn": "root", "laptop": "alice"} {
		start := fmt.Sprintf(`{"processId":"atlas-genehmigung-fix","variables":{"orderId":%q,"itemId":%q,"positionId":%q,
			"approvalRef":%q,"provisionProcess":"prov","recipient":"usr_x","orderer":"root","atlasApiBase":"http://127.0.0.1:1"}}`,
			orderID, item, item, approver)
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
			t.Fatalf("start the approval of %s: %d (%s)", item, code, b)
		}
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	var tasks []struct {
		Key      uint64 `json:"key"`
		Assignee string `json:"assignee"`
	}
	if code != http.StatusOK || json.Unmarshal(listRows(t, body), &tasks) != nil || len(tasks) != 2 {
		t.Fatalf("tasks: %d (%s)", code, body)
	}
	for _, tk := range tasks {
		taskOf[tk.Assignee] = tk.Key
	}

	// root placed the order and is the approver the product names: neither door opens.
	code, body = cReq(t, admin, ts, "POST", "/api/v1/approvals/decide",
		fmt.Sprintf(`{"approved":true,"reason":"","taskKeys":[%d]}`, taskOf["root"]))
	if code == http.StatusOK && !strings.Contains(string(body), "placed") {
		t.Errorf("the approvals page let the orderer decide their own order: %s", body)
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", taskOf["root"]),
		`{"variables":{"genehmigt":true}}`); code != http.StatusForbidden {
		t.Errorf("completing the approval of one's own order = %d (%s), want 403", code, b)
	}
	// Somebody else's decision on the same order is theirs to make.
	if code, b := cReq(t, alice, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", taskOf["alice"]),
		`{"variables":{"genehmigt":false,"begruendung":"nein"}}`); code != http.StatusOK {
		t.Errorf("the approver of another line was refused: %d (%s)", code, b)
	}
}
