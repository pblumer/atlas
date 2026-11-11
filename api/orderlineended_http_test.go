package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An order line whose process an operator ended
// (ADR-draft-a-cancelled-line-instance-settles-its-line).
//
// Observed live: provisioning instances cancelled from the instance list left their
// lines "running" for nine days. The line moves only when its process reports, the
// operator had ended the one thing that would, and the fulfilment orchestration
// waited at "Warten" on a message nobody was left to publish — while the repair
// route counted it healthy.

// advancedWatchBPMN starts an instance every time atlas.order.advanced is
// published, so a test can count the wakes the orchestration would have received.
const advancedWatchBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m_adv" name="atlas.order.advanced"/>
  <process id="advanced-watch" isExecutable="true">
    <startEvent id="S"><messageEventDefinition messageRef="m_adv"/></startEvent>
    <endEvent id="E"/>
    <sequenceFlow id="f" sourceRef="S" targetRef="E"/>
  </process>
</definitions>`

// aStartedLifecycleLine places a lifecycle order, starts its line through the order's
// start act, and deploys the wake watcher. It returns the provisioning instance.
func aStartedLifecycleLine(t *testing.T) (ts *httptest.Server, admin *http.Client, ord string, inst uint64) {
	t.Helper()
	ts, admin, ord, _ = aLifecycleOrder(t)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", advancedWatchBPMN); code != http.StatusOK {
		t.Fatalf("deploy the watcher: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{"operation":"provision"}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil || started.InstanceKey == 0 {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "running" {
		t.Fatalf("line after the start = %q, want running", got)
	}
	return ts, admin, ord, started.InstanceKey
}

// instanceStatesOf lists every instance of a process, active and finished, as key → state.
func instanceStatesOf(t *testing.T, c *http.Client, ts *httptest.Server, processID string) map[uint64]string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key           uint64 `json:"key"`
			ProcessDefKey uint64 `json:"processDefKey"`
			ProcessID     string `json:"processId"`
			State         string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	out := map[uint64]string{}
	for _, i := range page.Items {
		if i.ProcessID == processID {
			out[i.Key] = i.State
		}
	}
	return out
}

// definitionOf is the definition key an instance belongs to.
func definitionOf(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key           uint64 `json:"key"`
			ProcessDefKey uint64 `json:"processDefKey"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	for _, i := range page.Items {
		if i.Key == inst {
			return i.ProcessDefKey
		}
	}
	t.Fatalf("no instance %d in %s", inst, body)
	return 0
}

// lineOutcomes is the raw outcome list of one position.
func lineOutcomes(t *testing.T, c *http.Client, ts *httptest.Server, ord, item string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+ord+"/lines/"+item+"/outcomes", "")
	if code != http.StatusOK {
		t.Fatalf("outcomes: %d (%s)", code, body)
	}
	return string(body)
}

// TestCancellingALinesProvisioningSettlesTheLine, through every operator cancel
// path: the line fails as a failed report would fail it, the failure says why, and
// the orchestration is woken.
func TestCancellingALinesProvisioningSettlesTheLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64)
	}{
		{"DELETE /instances/{key}", func(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64) {
			if code, b := cReq(t, c, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", inst), ""); code != http.StatusOK {
				t.Fatalf("cancel: %d (%s)", code, b)
			}
		}},
		{"terminate by keys", func(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64) {
			if code, b := cReq(t, c, ts, "POST", "/api/v1/instances/terminate", fmt.Sprintf(`{"keys":[%d]}`, inst)); code != http.StatusOK {
				t.Fatalf("terminate: %d (%s)", code, b)
			}
		}},
		{"terminate by filter", func(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64) {
			def := definitionOf(t, c, ts, inst)
			if code, b := cReq(t, c, ts, "POST", "/api/v1/instances/terminate", fmt.Sprintf(`{"processDefKey":%d}`, def)); code != http.StatusOK {
				t.Fatalf("terminate: %d (%s)", code, b)
			}
		}},
		{"drain the definition", func(t *testing.T, c *http.Client, ts *httptest.Server, inst uint64) {
			def := definitionOf(t, c, ts, inst)
			if code, b := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/cancel-instances", def), ""); code != http.StatusOK {
				t.Fatalf("drain: %d (%s)", code, b)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, admin, ord, inst := aStartedLifecycleLine(t)
			wakes := len(instanceStatesOf(t, admin, ts, "advanced-watch"))

			tc.cancel(t, admin, ts, inst)

			if got := lineStatus(t, ts, admin, ord, "laptop"); got != "failed" {
				t.Fatalf("line after its provisioning was cancelled = %q, want failed — "+
					"nothing else is left to report on it", got)
			}
			if got := len(instanceStatesOf(t, admin, ts, "advanced-watch")); got != wakes+1 {
				t.Errorf("atlas.order.advanced published %d times, want once — the orchestration "+
					"waits on it", got-wakes)
			}
			out := lineOutcomes(t, admin, ts, ord, "laptop")
			if !strings.Contains(out, `"failed"`) || !strings.Contains(out, fmt.Sprintf(`%d`, inst)) ||
				!strings.Contains(out, "terminated") {
				t.Errorf("outcomes = %s, want a failed provision naming the terminated instance", out)
			}
		})
	}
}

// TestALineThatAlreadyReportedIsUntouchedByItsInstanceBeingCancelled: a
// provisioning that reported done and parks afterwards is cancelled where it parked;
// the delivery stands and nothing is woken.
func TestALineThatAlreadyReportedIsUntouchedByItsInstanceBeingCancelled(t *testing.T) {
	ts, admin, ord, inst := aStartedLifecycleLine(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	wakes := len(instanceStatesOf(t, admin, ts, "advanced-watch"))

	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", inst), ""); code != http.StatusOK {
		t.Fatalf("cancel: %d (%s)", code, b)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "done" {
		t.Errorf("line = %q, want done: it reported before its instance was cancelled", got)
	}
	if got := len(instanceStatesOf(t, admin, ts, "advanced-watch")); got != wakes {
		t.Errorf("woken %d more times, want none", got-wakes)
	}
}

// TestCancellingALinesReturnLeavesItReturnFailed: the recipient still holds what a
// cancelled revocation was taking away, so the line says the return failed, and
// asking for it again is the repair.
func TestCancellingALinesReturnLeavesItReturnFailed(t *testing.T) {
	ts, admin, ord, _ := aStartedLifecycleLine(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	before := instanceStatesOf(t, admin, ts, "laptop-lifecycle")
	if code, b := returnLine(t, ts, admin, ord, "laptop"); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "returning" {
		t.Fatalf("line after the return was asked = %q", got)
	}
	var deprov uint64
	for k, state := range instanceStatesOf(t, admin, ts, "laptop-lifecycle") {
		if _, old := before[k]; !old && state == "active" {
			deprov = k
		}
	}
	if deprov == 0 {
		t.Fatal("the return started no instance")
	}

	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", deprov), ""); code != http.StatusOK {
		t.Fatalf("cancel: %d (%s)", code, b)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "returnFailed" {
		t.Errorf("line = %q, want returnFailed — the recipient still holds it", got)
	}
	if code, b := returnLine(t, ts, admin, ord, "laptop"); code != http.StatusOK {
		t.Errorf("asking for the return again = %d (%s), want it accepted as the repair", code, b)
	}
}

// anApprovalStartedByTheOrder places an order whose one line needs approving,
// deploys the approval model and the wake watcher, and starts the line through the
// order's start act — which starts, and records on the line, its approval. It
// returns the order and the approval instance.
func anApprovalStartedByTheOrder(t *testing.T) (ts *httptest.Server, admin *http.Client, ord string, inst uint64) {
	t.Helper()
	ts, _ = newAuthServer(t, "root", "rootpassword")
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, ord = aCatalogueWithAnOrder(t, ts, admin)
	for _, model := range []string{ownApprovalBPMN, advancedWatchBPMN} {
		if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", model); code != http.StatusOK {
			t.Fatalf("deploy: %d (%s)", code, b)
		}
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/vpn/start", `{}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil || started.Started != "approval" {
		t.Fatalf("start: %d (%s), want the approval started", code, body)
	}
	return ts, admin, ord, started.InstanceKey
}

// TestCancellingALinesApprovalFailsTheLine: an operator stopping the approval is
// neither an approver's refusal nor the orderer's withdrawal, so the line is not
// filed as either. It fails, which keeps the order open for somebody to look at,
// and the orchestration is told.
func TestCancellingALinesApprovalFailsTheLine(t *testing.T) {
	ts, admin, ord, inst := anApprovalStartedByTheOrder(t)
	wakes := len(instanceStatesOf(t, admin, ts, "advanced-watch"))

	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", inst), ""); code != http.StatusOK {
		t.Fatalf("cancel: %d (%s)", code, b)
	}
	if got := lineStatus(t, ts, admin, ord, "vpn"); got != "failed" {
		t.Errorf("line = %q, want failed — not rejected: nobody refused it", got)
	}
	if got := len(instanceStatesOf(t, admin, ts, "advanced-watch")); got != wakes+1 {
		t.Errorf("woken %d times, want once", got-wakes)
	}
}

// TestWithdrawingAnOrderStillSettlesItCancelled: the shop's own withdrawal stops the
// approval it took back, and the line says cancelled — the withdrawal's outcome —
// and not failed, which is what an operator's cancel of the same instance says.
func TestWithdrawingAnOrderStillSettlesItCancelled(t *testing.T) {
	ts, admin, ord, inst := anApprovalStartedByTheOrder(t)

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/cancel", `{}`); code != http.StatusOK {
		t.Fatalf("withdraw: %d (%s)", code, b)
	}
	if got := instanceStatesOf(t, admin, ts, "kunden-genehmigung")[inst]; got == "active" {
		t.Fatalf("the withdrawal left the approval %s", got)
	}
	if got := lineStatus(t, ts, admin, ord, "vpn"); got != "cancelled" {
		t.Errorf("line = %q, want cancelled", got)
	}
	if out := lineOutcomes(t, admin, ts, ord, "vpn"); strings.Contains(out, `"failed"`) {
		t.Errorf("outcomes = %s, want no failure recorded for a withdrawal", out)
	}
}

// TestTheRepairSettlesALineWaitingOnAnInstanceThatEnded: the state a crash between
// the cancel and the settlement leaves — and the state a process leaves that ends
// without reporting — is listed by the dry run, not counted healthy, and settled by
// the repair.
func TestTheRepairSettlesALineWaitingOnAnInstanceThatEnded(t *testing.T) {
	ts, admin, ord, inst := aStartedLifecycleLine(t)
	// The provision branch parks on a message correlated on the order id; publishing
	// it ends the instance with nothing reported, which is the orphan a lost
	// settlement leaves too.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/messages",
		fmt.Sprintf(`{"name":"never","correlationKey":%q}`, ord)); code != http.StatusOK {
		t.Fatalf("end the provisioning: %d (%s)", code, b)
	}
	if got := instanceStatesOf(t, admin, ts, "laptop-lifecycle")[inst]; got == "active" {
		t.Fatalf("the provisioning instance is still %s", got)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "running" {
		t.Fatalf("line = %q, want it stranded at running", got)
	}

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/fulfilment/repair?dryRun=true", "")
	if code != http.StatusOK {
		t.Fatalf("dry run: %d (%s)", code, body)
	}
	var dry struct {
		OrphanedLines []struct {
			OrderID       string `json:"orderId"`
			Line          string `json:"line"`
			Instance      uint64 `json:"instance"`
			InstanceState string `json:"instanceState"`
			Settles       string `json:"settles"`
		} `json:"orphanedLines"`
	}
	if err := json.Unmarshal(body, &dry); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(dry.OrphanedLines) != 1 || dry.OrphanedLines[0].OrderID != ord || dry.OrphanedLines[0].Line != "laptop" ||
		dry.OrphanedLines[0].Instance != inst || dry.OrphanedLines[0].Settles != "failed" ||
		dry.OrphanedLines[0].InstanceState != "completed" {
		t.Fatalf("dry run = %s, want the stranded line", body)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "running" {
		t.Fatalf("a dry run settled the line: %q", got)
	}

	if code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/fulfilment/repair", ""); code != http.StatusOK {
		t.Fatalf("repair: %d (%s)", code, body)
	}
	if got := lineStatus(t, ts, admin, ord, "laptop"); got != "failed" {
		t.Errorf("line after the repair = %q, want failed", got)
	}
	again := repair(t, admin, ts, "?dryRun=true")
	if len(again.Cancelled) != 0 {
		t.Errorf("second dry run = %+v", again)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/fulfilment/repair?dryRun=true", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"orphanedLines":[]`) {
		t.Errorf("second dry run = %d (%s), want nothing left orphaned", code, body)
	}
}
