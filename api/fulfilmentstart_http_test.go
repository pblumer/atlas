package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

	// And the consequence, which is the half a variable check alone would miss: the
	// first thing the orchestration does is ask which positions may start, and the
	// address it asks at is built from that variable. Null in, null path, and an
	// order nothing ever works on.
	code, tl := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/timeline", orchestration), "")
	if code != http.StatusOK {
		t.Fatalf("read the timeline: %d (%s)", code, tl)
	}
	var timeline struct {
		Steps []struct {
			ElementID string `json:"elementId"`
			Inputs    []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"inputs"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(tl, &timeline); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, tl)
	}
	asked := ""
	for _, s := range timeline.Steps {
		if s.ElementID != "Next" {
			continue
		}
		for _, in := range s.Inputs {
			if in.Name == "path" {
				asked = in.Value
			}
		}
	}
	if !strings.Contains(asked, ord) {
		t.Errorf("the orchestration asks for %q, which names no order — so nothing it "+
			"is told to start is ever started", asked)
	}
}
