package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Clearing up after an orchestration that could not know its order.
//
// The wake that starts the fulfilment process passed the order id as the message's
// correlation key alone, and a message *start* event evaluates that key from the
// payload — so the variable the model reads was never written. Such an instance
// builds every request from nothing, is never woken by a settled line, and never
// ends. Deploying the fix does not repair one: it is already running, and the
// variable it needed was never there to write.
//
// So there is a route that ends them and starts an orchestration again for every
// open order left without one. Both halves together, because either alone leaves a
// state nobody wants: cancelling alone leaves the order with nothing working it,
// and starting alone puts a second orchestration beside a healthy one — and two
// orchestrations on one order both ask what may start and both start it.

type repairResp struct {
	DryRun    bool `json:"dryRun"`
	Cancelled []struct {
		Instance uint64 `json:"instance"`
		OrderID  string `json:"orderId"`
		Reason   string `json:"reason"`
	} `json:"cancelled"`
	Restarted []string `json:"restarted"`
	Healthy   int      `json:"healthy"`
}

func repair(t *testing.T, c *http.Client, ts *httptest.Server, query string) repairResp {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/orders/fulfilment/repair"+query, "")
	if code != http.StatusOK {
		t.Fatalf("repair%s = %d (%s), want 200", query, code, body)
	}
	var out repairResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode repair: %v (%s)", err, body)
	}
	return out
}

// orchestrationsOf returns the active fulfilment instances and the order each one
// says it is working on.
func orchestrationsOf(t *testing.T, c *http.Client, ts *httptest.Server) map[uint64]string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
			State     string `json:"state"`
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	out := map[uint64]string{}
	for _, i := range page.Items {
		if i.ProcessID != "atlas-auftrag-erfuellung" || i.State != "active" {
			continue
		}
		out[i.Key] = ""
		for _, v := range i.Variables {
			if v.Name == "orderId" {
				out[i.Key] = v.Value
			}
		}
	}
	return out
}

// startABlindOrchestration reproduces the state the defect left behind: the
// fulfilment model started by the message it subscribes to, with the payload that
// message used to carry — orderer and recipient, and no order id.
func startABlindOrchestration(t *testing.T, admin *http.Client, ts *httptest.Server, orderID string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":"atlas.order.placed","correlationKey":%q,"variables":{
		"orderer":"root","recipient":"usr_empfaenger","portalBaseUrl":""}}`, orderID)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/messages", body); code != http.StatusOK {
		t.Fatalf("publish the message the defect published: %d (%s)", code, b)
	}
}

// TestAnOrchestrationThatNamesNoOrderIsEnded.
func TestAnOrchestrationThatNamesNoOrderIsEnded(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")
	startABlindOrchestration(t, admin, ts, ord)

	before := orchestrationsOf(t, admin, ts)
	blind := uint64(0)
	for key, names := range before {
		if names == "" {
			blind = key
		}
	}
	if blind == 0 {
		t.Fatalf("the fixture started no blind orchestration; it reads %v", before)
	}

	got := repair(t, admin, ts, "")
	if len(got.Cancelled) != 1 || got.Cancelled[0].Instance != blind {
		t.Fatalf("the repair ended %+v, want exactly the orchestration that names no "+
			"order (%d)", got.Cancelled, blind)
	}
	// And the reason is this instance's own. Two situations end an orchestration —
	// one that names no order, and one that names an order this server no longer
	// holds — and they read as the same act with the same outcome. The sentence is
	// the only place they differ, and it is what tells an operator whether they are
	// looking at the defect or at an instance that outlived its work.
	if !strings.Contains(got.Cancelled[0].Reason, "no order id") {
		t.Errorf("the repair ends this instance saying %q, which does not say that it "+
			"names no order at all", got.Cancelled[0].Reason)
	}
	// The healthy one is left alone, and the order is not started again: it already
	// has an orchestration, and a second would start every position twice.
	if got.Healthy != 1 {
		t.Errorf("healthy = %d, want 1 — the order's own orchestration is working", got.Healthy)
	}
	if len(got.Restarted) != 0 {
		t.Errorf("the repair started %v again, each of which already has an "+
			"orchestration working it", got.Restarted)
	}
	after := orchestrationsOf(t, admin, ts)
	if _, still := after[blind]; still {
		t.Error("the orchestration that names no order is still running")
	}
	if len(after) != 1 {
		t.Errorf("%d orchestrations are left, want the one that knows its order", len(after))
	}
}

// TestAnOpenOrderWithNothingWorkingItIsStartedAgain.
//
// The other half, and the one that matters to somebody whose order is stuck:
// ending the blind instance leaves the order exactly where it was.
func TestAnOpenOrderWithNothingWorkingItIsStartedAgain(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")

	// Take the order's own orchestration away, which is the state an installation is
	// in after the blind ones have been cleared by hand.
	for key := range orchestrationsOf(t, admin, ts) {
		if code, b := cReq(t, admin, ts, "DELETE",
			fmt.Sprintf("/api/v1/instances/%d", key), ""); code != http.StatusOK && code != http.StatusNoContent {
			t.Fatalf("cancel the orchestration: %d (%s)", code, b)
		}
	}
	if left := orchestrationsOf(t, admin, ts); len(left) != 0 {
		t.Fatalf("the fixture left %d orchestrations running", len(left))
	}

	got := repair(t, admin, ts, "")
	if len(got.Restarted) != 1 || got.Restarted[0] != ord {
		t.Fatalf("the repair started %v again, want the open order %s", got.Restarted, ord)
	}
	// And the new one knows its order, which is the whole point: an orchestration
	// started again without it would be the instance this route just ended.
	after := orchestrationsOf(t, admin, ts)
	if len(after) != 1 {
		t.Fatalf("%d orchestrations are running, want 1 (%v)", len(after), after)
	}
	for key, names := range after {
		if names != ord {
			t.Errorf("the orchestration started for %s says it works on %q (instance %d)",
				ord, names, key)
		}
	}
}

// TestRunningTheRepairTwiceChangesNothing.
//
// The property that makes it safe to run at all. An operator who runs it, waits,
// and runs it again must not thereby order everything a second time.
func TestRunningTheRepairTwiceChangesNothing(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")
	startABlindOrchestration(t, admin, ts, ord)

	first := repair(t, admin, ts, "")
	if len(first.Cancelled) != 1 {
		t.Fatalf("the first pass ended %d orchestrations, want 1", len(first.Cancelled))
	}
	second := repair(t, admin, ts, "")
	if len(second.Cancelled) != 0 || len(second.Restarted) != 0 {
		t.Errorf("the second pass ended %+v and started %v — an installation with "+
			"nothing broken must be answered with two empty lists",
			second.Cancelled, second.Restarted)
	}
	if n := len(orchestrationsOf(t, admin, ts)); n != 1 {
		t.Errorf("%d orchestrations are running after two passes, want 1", n)
	}
}

// TestADryRunChangesNothing.
//
// The list is what an operator decides on, so producing it must not be the
// decision.
func TestADryRunChangesNothing(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")
	startABlindOrchestration(t, admin, ts, ord)
	before := orchestrationsOf(t, admin, ts)

	got := repair(t, admin, ts, "?dryRun=true")
	if !got.DryRun {
		t.Error("a dry run does not say it was one, so its lists read as acts")
	}
	if len(got.Cancelled) != 1 {
		t.Errorf("the dry run names %d orchestrations to end, want 1 — a proposal "+
			"nobody can act on is not a proposal", len(got.Cancelled))
	}
	after := orchestrationsOf(t, admin, ts)
	if len(after) != len(before) {
		t.Errorf("the dry run left %d orchestrations running, %d were before it",
			len(after), len(before))
	}
}
