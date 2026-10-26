package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A forced delete removes a deployed version whatever holds it
// (ADR-0468): it cancels the version's
// running instances and overrides ADR-0427's guard. Without force nothing changes.

// TestForcedDeleteCancelsTheRunningInstances: a version with instances parked at a
// service task is refused by a plain delete, and a forced one terminates them and
// removes the version.
func TestForcedDeleteCancelsTheRunningInstances(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	for i := range 3 {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("start %d: %d %s", i, code, body)
		}
	}

	code, body := doReq(t, ts, http.MethodDelete, "/api/v1/processes/1", "", "")
	if code != http.StatusConflict || !strings.Contains(string(body), "3 running instance(s)") || !strings.Contains(string(body), "force") {
		t.Fatalf("plain delete: %d %s, want 409 naming 3 running instances and the forced delete", code, body)
	}

	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/processes/1?force=true", "", ""); code != http.StatusNoContent {
		t.Fatalf("forced delete: %d %s, want 204", code, body)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", ""); code != http.StatusOK || strings.Contains(string(body), `"key":1`) {
		t.Fatalf("definition 1 still listed: %s", body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	var insts []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(listRows(t, body), &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	for _, in := range insts {
		if in.State == "active" {
			t.Fatalf("an instance is still active after the forced delete: %s", body)
		}
	}
}

// TestForcedDeleteRejectsAMalformedFlag: force is a boolean, and a value that is not
// one is a client error rather than a silent plain delete.
func TestForcedDeleteRejectsAMalformedFlag(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/processes/1?force=perhaps", "", ""); code != http.StatusBadRequest {
		t.Fatalf("force=perhaps: %d %s, want 400", code, body)
	}
}

// TestForcedDeleteOverridesTheCatalogueBinding is the reported case: a process the
// current catalogue release binds, never started, is refused by a plain delete with
// the server's reason, and removed by a forced one.
func TestForcedDeleteOverridesTheCatalogueBinding(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", plainProcess("zentrale"))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatal(err)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"zentrale","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Zentrale"},`+
			`"approval":{"kind":"none"},"provisionProcess":"zentrale","deprovisionProcess":"zentrale"}`); code != http.StatusOK {
		t.Fatalf("product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["zentrale"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", ""); code != http.StatusCreated {
		t.Fatalf("release: %d (%s)", code, b)
	}

	path := fmt.Sprintf("/api/v1/processes/%d", dep.Key)
	code, body = cReq(t, admin, ts, "DELETE", path, "")
	if code != http.StatusConflict || !strings.Contains(string(body), "binds zentrale") {
		t.Fatalf("plain delete: %d (%s), want 409 naming the binding", code, body)
	}
	if code, b := cReq(t, admin, ts, "DELETE", path+"?force=true", ""); code != http.StatusNoContent {
		t.Fatalf("forced delete: %d (%s), want 204", code, b)
	}
}

// TestForcedDeleteNeedsTheOperatorRoleToCancel: cancelling an instance is the
// operator's right (ADR-0209), so a modeler who holds nothing else may force a
// version with no instances away, but not one whose instances it would cancel.
func TestForcedDeleteNeedsTheOperatorRoleToCancel(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	createUserWithRoles(t, admin, ts.URL, "mod", `["modeler"]`)
	mod := signInAs(t, ts.URL, "mod", "a-password-that-is-long")

	deploy := func(id string) uint64 {
		t.Helper()
		code, body := cReqTyped(t, mod, ts, "POST", "/api/v1/deployments", "application/xml",
			strings.Replace(sampleBPMN, `id="order"`, `id="`+id+`"`, 1))
		if code != http.StatusOK {
			t.Fatalf("deploy %s: %d %s", id, code, body)
		}
		var dep struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal(body, &dep); err != nil {
			t.Fatal(err)
		}
		return dep.Key
	}
	busy, idle := deploy("busy"), deploy("idle")
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", busy), "{}"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, b)
	}

	code, body := cReq(t, mod, ts, "DELETE", fmt.Sprintf("/api/v1/processes/%d?force=true", busy), "")
	if code != http.StatusForbidden || !strings.Contains(string(body), "operator") {
		t.Fatalf("modeler forcing a busy version: %d (%s), want 403 naming the operator role", code, body)
	}
	if code, b := cReq(t, mod, ts, "DELETE", fmt.Sprintf("/api/v1/processes/%d?force=true", idle), ""); code != http.StatusNoContent {
		t.Fatalf("modeler forcing an idle version: %d (%s), want 204", code, b)
	}
	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/processes/%d?force=true", busy), ""); code != http.StatusNoContent {
		t.Fatalf("admin forcing the busy version: %d (%s), want 204", code, b)
	}
}
