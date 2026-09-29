package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func plainProcess(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
}

// TestConvertingAProductKeepsItsOldProcessesAliveUntilMoved walks ADR-0427 end to
// end: a line placed under two processes keeps them after the product is converted;
// deleting the old deprovisioning process is refused while that line can still
// start it; the fulfilment report counts it; moving the line to the lifecycle
// process lets the delete through.
func TestConvertingAProductKeepsItsOldProcessesAliveUntilMoved(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	keys := map[string]string{}
	for id, xml := range map[string]string{
		"old-prov": plainProcess("old-prov"), "old-deprov": plainProcess("old-deprov"),
		"laptop-lifecycle": laptopLifecycleBPMN,
	} {
		code, body := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", xml)
		if code != http.StatusOK {
			t.Fatalf("deploy %s: %d (%s)", id, code, body)
		}
		var d struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		keys[id] = fmt.Sprint(d.Key)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	save := func(binding string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
			`{"id":"laptop","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Laptop"},`+
				`"approval":{"kind":"none"},`+binding+`}`); code != http.StatusOK {
			t.Fatalf("save: %d (%s)", code, b)
		}
	}
	publish := func() string {
		t.Helper()
		code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
		if code != http.StatusCreated {
			t.Fatalf("publish: %d (%s)", code, body)
		}
		return idOf(t, body)
	}
	save(`"provisionProcess":"old-prov","deprovisionProcess":"old-deprov"`)
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	rel := publish()
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["laptop"]}`); code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, b)
	}

	// Converted, and published: new orders take the lifecycle process, the old line
	// keeps what it froze.
	save(`"lifecycleProcess":"laptop-lifecycle","operations":{"provision":"laptop.provision","deprovision":"laptop.deprovision"}`)
	publish()

	code, body = cReq(t, admin, ts, "DELETE", "/api/v1/processes/"+keys["old-deprov"], "")
	if code != http.StatusConflict || !strings.Contains(string(body), "laptop: 1") {
		t.Fatalf("delete while a line needs it: %d (%s), want 409 counting laptop: 1", code, body)
	}

	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalog-products/fulfilment-report", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"process":"old-deprov","lines":1`) {
		t.Fatalf("report: %d (%s), want a remainder of 1 on old-deprov", code, body)
	}

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products/laptop/rebind", `{}`); code != http.StatusBadRequest {
		t.Fatalf("rebind without a reason: %d (%s), want 400", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalog-products/laptop/rebind", `{"reason":"the old IdM was replaced"}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"lines":1`) {
		t.Fatalf("rebind: %d (%s), want one line moved", code, body)
	}

	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/processes/"+keys["old-deprov"], ""); code != http.StatusNoContent {
		t.Fatalf("delete after the move: %d (%s), want 204", code, b)
	}
}
