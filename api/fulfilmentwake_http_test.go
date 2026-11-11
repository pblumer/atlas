package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A wake is not lost because the orchestration was not parked yet.
//
// The orchestration reads the order, completes that job, starts the round, and only
// then opens its subscription at "Warten". A position that reports inside that
// window — a provisioning process that finishes at once reports before the start
// that launched it has come back — published atlas.order.advanced to nobody, and an
// unmatched publish without a time to live is dropped (ADR-0020). The orchestration
// then waited for a message that had already been sent.
//
// The window is closed by keeping the wake in the message buffer (ADR-0370). The
// case is built here directly: the wake is published while nothing waits, and a
// catch on the same name and order id opened afterwards must still receive it.

// advancedCatchBPMN stands in for the orchestration's "Warten": it waits for the
// order's wake and ends once it has it.
const advancedCatchBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             id="defs_wake_catch" targetNamespace="http://atlas/test">
  <message id="m" name="atlas.order.advanced">
    <extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements>
  </message>
  <process id="wake_catch" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <intermediateCatchEvent id="w"><messageEventDefinition messageRef="m"/></intermediateCatchEvent>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

// TestAWakePublishedBeforeTheOrchestrationWaitsIsKept: for every act that wakes the
// orchestration — an outcome reported, a refusal, a withdrawal.
func TestAWakePublishedBeforeTheOrchestrationWaitsIsKept(t *testing.T) {
	acts := map[string]struct{ path, body string }{
		"an outcome reported": {"/lines/account", `{"status":"done"}`},
		"a refusal":           {"/lines/account/decision", `{"by":"root","reason":"nicht vorgesehen"}`},
		"a withdrawal":        {"/lines/laptop/cancel", ""},
	}
	for name, act := range acts {
		t.Run(name, func(t *testing.T) {
			ts, _ := newAuthServer(t, "root", "rootpassword")
			admin := newClient(t)
			if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
				t.Fatal("admin login failed")
			}
			if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", advancedCatchBPMN); code != http.StatusOK {
				t.Fatalf("deploy the catch: %d (%s)", code, b)
			}
			ord := aWorkplaceOrder(t, ts, admin)

			// Nothing waits for this order's wake yet.
			if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+act.path, act.body); code != http.StatusOK {
				t.Fatalf("%s: %d (%s)", name, code, b)
			}

			// The orchestration arrives at its catch afterwards.
			code, body := cReq(t, admin, ts, "POST", "/api/v1/instances",
				fmt.Sprintf(`{"processId":"wake_catch","variables":{"orderId":%q}}`, ord))
			if code != http.StatusOK {
				t.Fatalf("start the catch: %d (%s)", code, body)
			}
			var inst struct {
				Key uint64 `json:"instanceKey"`
			}
			if err := json.Unmarshal(body, &inst); err != nil || inst.Key == 0 {
				t.Fatalf("decode the instance: %v (%s)", err, body)
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				state := lookupInstanceState(t, admin, ts, inst.Key)
				if strings.EqualFold(state, "completed") {
					return
				}
				if time.Now().After(deadline) {
					_, buffered := cReq(t, admin, ts, "GET", "/api/v1/messages/buffered", "")
					t.Fatalf("the catch opened after %s is still %q: the wake was published to "+
						"nobody and dropped, so an orchestration parked now waits for a message "+
						"that was already sent.\nbuffered: %s", name, state, buffered)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
