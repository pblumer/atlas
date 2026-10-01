package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// laptopActions is the laptop product of aLifecycleOrder with its operations said as
// actions (ADR-0429): the same two messages, plus a service an operator runs.
func laptopActions(catalogID, serviceMessage string) string {
	return `{"id":"laptop","homeCatalog":"` + catalogID + `","state":"active","texts":{"de":"Laptop"},` +
		`"approval":{"kind":"none"},"lifecycleProcess":"laptop-lifecycle","actions":[` +
		`{"key":"provision","message":"laptop.provision","effect":"provision"},` +
		`{"key":"deprovision","message":"laptop.deprovision","effect":"deprovision","triggers":["customer"]}` +
		serviceMessage + `]}`
}

// TestAProductThatSaysItsActionsIsProvisioned: a product whose actions replace the
// operation map publishes, is ordered, and its position is started at the provision
// action's message — the same path an operation map takes.
func TestAProductThatSaysItsActionsIsProvisioned(t *testing.T) {
	ts, admin, _, cat := aLifecycleOrder(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", laptopActions(cat, "")); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	if !strings.Contains(string(body), `"actions":[`) {
		t.Fatalf("the line froze no actions: %s", body)
	}
	ord := idOf(t, body)

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{"operation":"provision"}`)
	var got startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.InstanceKey == 0 || got.Started != "provision" {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if vars := variablesOf(t, admin, ts, got.InstanceKey); !strings.Contains(vars, "ranProvision") || strings.Contains(vars, "ranDeprovision") {
		t.Fatalf("the instance ran %s, want the provision branch only", vars)
	}
}

// TestAWatchMayNotPublishAProductsMessage: a Worker's event under a name a product's
// action owns would drive the lifecycle around the order (ADR-0425 §8, ADR-0429 §1),
// so the watch is refused — on create and on the rename that would reach the same
// name — and the refusal names the product and the action.
func TestAWatchMayNotPublishAProductsMessage(t *testing.T) {
	ts, admin, _, _ := aLifecycleOrder(t)
	worker := createConnector(t, admin, ts.URL, "hr-clio")
	subs := "/api/v1/connectors/" + worker + "/inbound-subscriptions"

	code, body := cReq(t, admin, ts, "POST", subs, `{"watchedSubject":"hr/leavers","messageName":"laptop.deprovision","enabled":true}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "belongs to a catalogue product") ||
		!strings.Contains(string(body), "laptop") || !strings.Contains(string(body), "deprovision") {
		t.Fatalf("create: %d (%s), want 409 naming the product and the action", code, body)
	}

	code, body = cReq(t, admin, ts, "POST", subs, `{"watchedSubject":"hr/leavers","messageName":"hr.leaver","enabled":true}`)
	if code != http.StatusOK {
		t.Fatalf("create an unowned name: %d (%s)", code, body)
	}
	id := idOf(t, body)
	code, body = cReq(t, admin, ts, "PATCH", "/api/v1/inbound-subscriptions/"+id, `{"messageName":"laptop.provision"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "belongs to a catalogue product") {
		t.Fatalf("rename onto a product's name: %d (%s), want 409", code, body)
	}
}

// TestPublishingRefusesAnActionAWatchPublishes is the same pair from the other end: a
// watch published the name first, and the product that would take it is refused.
func TestPublishingRefusesAnActionAWatchPublishes(t *testing.T) {
	ts, admin, _, cat := aLifecycleOrder(t)
	worker := createConnector(t, admin, ts.URL, "repair-clio")
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/connectors/"+worker+"/inbound-subscriptions",
		`{"watchedSubject":"repairs","messageName":"laptop.repair","enabled":true}`); code != http.StatusOK {
		t.Fatalf("create the watch: %d (%s)", code, b)
	}
	service := `,{"key":"repair","message":"laptop.repair","effect":"service","triggers":["operator"],"labels":{"de":"Reparieren"}}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", laptopActions(cat, service)); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "laptop.repair, which an inbound watch publishes") {
		t.Fatalf("publish: %d (%s), want 422 naming the watched message", code, body)
	}
}

// TestAProductsMessageIsNotPublishedByName: a per-operation product's message is a start
// event the order enters — through its start act, a return or an action — and published
// by name it started the lifecycle process outside the order: a provisioning no line knew
// of, or a return the inventory never heard about (ADR-0425 §8, ADR-0429 §1). The publish
// route now refuses it, naming the product and the action, and starts nothing.
func TestAProductsMessageIsNotPublishedByName(t *testing.T) {
	ts, admin, _, _ := aLifecycleOrder(t)
	for _, name := range []string{"laptop.provision", "laptop.deprovision"} {
		code, body := cReq(t, admin, ts, "POST", "/api/v1/messages", `{"name":"`+name+`","correlationKey":"x"}`)
		if code != http.StatusConflict || !strings.Contains(string(body), "product laptop") {
			t.Fatalf("publish %s: %d (%s), want 409 naming the product", name, code, body)
		}
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/instances?processId=laptop-lifecycle", "")
	if code != http.StatusOK || strings.Contains(string(body), `"processId":"laptop-lifecycle"`) {
		t.Fatalf("instances: %d (%s), want none of the lifecycle process", code, body)
	}
	// A name no product owns is published as before.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/messages", `{"name":"nobody.listens","correlationKey":"x"}`); code != http.StatusOK {
		t.Fatalf("publish an unowned name: %d (%s)", code, b)
	}
}
