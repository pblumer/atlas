package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A position ordered in a variant is started like any other.
//
// A position's key is the item id, or the item id and the variant joined by "#"
// (order.Line.Key) — and the fulfilment and approval models put that key into the
// URL they call as it stood. In a URL "#" opens the fragment: POST
// .../lines/lizenz#standard/start went out as POST .../lines/lizenz, the route that
// sets a line's status, without a status. The order refused it ("a line cannot be
// set to here"), the orchestration raised an incident, and a retry failed the same
// way. Every product with variants stopped there, whatever its process, while every
// product without one went through.

const variantProvisionBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs_variant_prov" targetNamespace="http://atlas/test">
  <process id="variant_prov" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <userTask id="t" name="Bereitstellen"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

const variantDeprovisionBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs_variant_deprov" targetNamespace="http://atlas/test">
  <process id="variant_deprov" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

// TestAPositionWithAVariantIsStarted: the orchestration starts it itself.
func TestAPositionWithAVariantIsStarted(t *testing.T) {
	ts, admin, ord := anOrderInAVariant(t, `{"kind":"none"}`)
	awaitProvisioning(t, admin, ts, ord)
}

// TestAnApprovedPositionWithAVariantIsStarted: the approval starts it, through the
// same route, once somebody has said yes.
func TestAnApprovedPositionWithAVariantIsStarted(t *testing.T) {
	ts, admin, ord := anOrderInAVariant(t, `{"kind":"fixed","ref":"root"}`)

	var task uint64
	deadline := time.Now().Add(20 * time.Second)
	for task == 0 {
		code, body := cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
		if code == http.StatusOK {
			var tasks []struct {
				Key       uint64 `json:"key"`
				ProcessID string `json:"processId"`
			}
			if err := json.Unmarshal(listRows(t, body), &tasks); err != nil {
				t.Fatalf("decode tasks: %v (%s)", err, body)
			}
			for _, k := range tasks {
				if k.ProcessID == "atlas-genehmigung-fix" {
					task = k.Key
				}
			}
		}
		if task == 0 && time.Now().After(deadline) {
			_, raw := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("no approval task for lizenz#standard.\nincidents: %s", raw)
		}
		if task == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", task),
		`{"variables":{"genehmigt":true,"begruendung":"ok"}}`); code != http.StatusOK {
		t.Fatalf("approve: %d (%s)", code, b)
	}
	awaitProvisioning(t, admin, ts, ord)
}

// anOrderInAVariant places an order for one product in the variant "standard",
// approved as the given rule says, on a server that calls itself.
func anOrderInAVariant(t *testing.T, approval string) (*httptest.Server, *http.Client, string) {
	t.Helper()
	ts, admin := aServerThatCanCallItself(t)

	for _, xml := range []string{variantProvisionBPMN, variantDeprovisionBPMN} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", xml); code != http.StatusOK {
			t.Fatalf("deploy: %d (%s)", code, b)
		}
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Lizenzen"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"lizenz","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Lizenz"},`+
			`"variants":[{"id":"standard","texts":{"de":"Standard"}},{"id":"erweitert","texts":{"de":"Erweitert"}}],`+
			`"approval":`+approval+`,"provisionProcess":"variant_prov","deprovisionProcess":"variant_deprov"}`,
	); code != http.StatusOK {
		t.Fatalf("save the product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["lizenz"]}`); code != http.StatusOK {
		t.Fatalf("offer it: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["lizenz"],"variants":{"lizenz":["standard"]}}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return ts, admin, idOf(t, body)
}

// awaitProvisioning waits until the process the product is bound to runs: the
// position lizenz#standard was started. Polled, because every step is a real round
// trip to this server on the engine's own loop.
func awaitProvisioning(t *testing.T, c *http.Client, ts *httptest.Server, ord string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !provisioningStarted(t, c, ts) {
		if time.Now().After(deadline) {
			_, raw := cReq(t, c, ts, "GET", "/api/v1/incidents", "")
			_, line := cReq(t, c, ts, "GET", "/api/v1/orders/"+ord, "")
			t.Fatalf("the position lizenz#standard was never started.\nincidents: %s\norder: %s", raw, line)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// provisioningStarted reports whether an instance of variant_prov exists.
func provisioningStarted(t *testing.T, c *http.Client, ts *httptest.Server) bool {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		return false
	}
	var page struct {
		Items []struct {
			ProcessID string `json:"processId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	for _, i := range page.Items {
		if i.ProcessID == "variant_prov" {
			return true
		}
	}
	return false
}
