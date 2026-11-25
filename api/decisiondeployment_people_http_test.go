package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// brandsDMN is a decision whose XML id and published name differ, as every decision
// dmn-js creates does: the deployment is listed under the name.
const brandsDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_brands" name="Brands" namespace="http://atlas/test">
  <decision id="Decision_0brands" name="Brands">
    <variable id="var_brands" name="Brands" />
    <literalExpression id="le_brands"><text>["ferrari", "audi"]</text></literalExpression>
  </decision>
</definitions>`

// TestDecisionDeploymentsNameWhoDeployed: the decision editor's version panel says who
// deployed each version, the way the Modeler names the person behind a process
// version — so the listing carries the name a colleague knows, beside the id.
func TestDecisionDeploymentsNameWhoDeployed(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","displayName":"Alice Adams","password":"password1","roles":["modeler","operator","user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice = %d %s", code, body)
	}
	aliceID := idOf(t, body)
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login")
	}
	_, pbody := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Cars"}`)
	pid := decodeProject(t, pbody).ID

	if code, body := cReqTyped(t, alice, ts, "POST", "/api/v1/decision-deployments?projectId="+pid, "application/xml", brandsDMN); code != http.StatusOK {
		t.Fatalf("alice deploy = %d %s", code, body)
	}

	code, body = cReq(t, admin, ts, "GET", "/api/v1/decision-deployments?decisionId=Brands", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	var rows []struct {
		DecisionID     string `json:"decisionId"`
		Version        int32  `json:"version"`
		DeployedBy     string `json:"deployedBy"`
		DeployedByName string `json:"deployedByName"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("listed %d rows for Brands, want 1: %s", len(rows), body)
	}
	if r := rows[0]; r.DecisionID != "Brands" || r.Version != 1 || r.DeployedBy != aliceID || r.DeployedByName != "Alice Adams" {
		t.Fatalf("row = %+v, want Brands v1 deployed by Alice Adams (%s)", r, aliceID)
	}

	// Asked by the XML id, the listing has nothing: a decision is published and
	// versioned under its name (dmn/names.go), which is what the editor must ask by.
	_, body = cReq(t, admin, ts, "GET", "/api/v1/decision-deployments?decisionId=Decision_0brands", "")
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("listing by XML id = %s, want []", body)
	}
}
