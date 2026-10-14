package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The fulfilment orchestration is told which order it is working on.
//
// Its model documents orderId as a start variable, reads it in every path it
// builds ("/api/v1/orders/" + orderId + "/next") and correlates the message that
// wakes it on it. The wake carried it as the message's correlation key and not as a
// variable — and a message start event's correlation key is *evaluated from the
// payload*, so with no orderId in the payload the expression resolved to nothing,
// the instance recorded no key, and the variable the model reads was never there.
//
// Nothing failed. FEEL propagates null, so the first service task was activated
// with path = null: the orchestration asked its REST worker for nothing, was never
// woken by a settled line, and every order sat at "Wartet" with no incident to
// find. A defect that raises an incident is a defect somebody can see.

// TestTheFulfilmentProcessKnowsItsOrder.
func TestTheFulfilmentProcessKnowsItsOrder(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")

	code, body := cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	var orchestration uint64
	carries := ""
	for _, i := range page.Items {
		if i.ProcessID != "atlas-auftrag-erfuellung" {
			continue
		}
		orchestration = i.Key
		for _, v := range i.Variables {
			if v.Name == "orderId" {
				carries = v.Value
			}
		}
	}
	if orchestration == 0 {
		t.Fatalf("placing an order started no fulfilment process (%s)", body)
	}
	if carries != ord {
		t.Errorf("the fulfilment process carries orderId %q, want %q — it reads that "+
			"variable in every request it makes and correlates on it", carries, ord)
	}

	// The consequence — that the orchestration's first request is built from this
	// variable and actually lands — is proved end to end in
	// TestTheOrchestrationReachesAtlasAndMovesOn. It used to be read here, out of
	// the timeline's `path` input, because that step was a plain service task whose
	// address was an input mapping. It is a REST connector task now: the address is
	// resolved into the job rather than written into the scope, and the only honest
	// check of it is whether the call came back.
}
