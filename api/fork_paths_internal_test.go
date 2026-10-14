package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// forkPathsV1 parks one instance in several places at once at a single element: a
// parallel review over two reviewers (the body and one token per reviewer) with a
// deadline armed on it. It also declares two data objects.
const forkPathsV1 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="forkpaths" isExecutable="true">
    <dataObject id="DO_order" name="order"/>
    <dataObject id="DO_invoice" name="invoice"/>
    <startEvent id="start"/>
    <userTask id="review">
      <multiInstanceLoopCharacteristics isSequential="false">
        <extensionElements><zeebe:loopCharacteristics inputCollection="=reviewers" inputElement="reviewer"/></extensionElements>
      </multiInstanceLoopCharacteristics>
    </userTask>
    <boundaryEvent id="late" attachedToRef="review">
      <timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition>
    </boundaryEvent>
    <endEvent id="end"/>
    <endEvent id="lateEnd"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
    <sequenceFlow id="f3" sourceRef="late" targetRef="lateEnd"/>
  </process>
</definitions>`

// forkPathsV2 keeps the review, as a single task, and drops the invoice.
const forkPathsV2 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="forkpaths" isExecutable="true">
    <dataObject id="DO_order" name="order"/>
    <startEvent id="start"/>
    <userTask id="review"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </process>
</definitions>`

// forkPathsDeploy deploys one version and returns its definition key.
func forkPathsDeploy(t *testing.T, srv *Server, xml string) uint64 {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// forkPathsSetup deploys v1, starts one instance of it parked at the review, then
// deploys v2. It returns the instance and the v2 key.
func forkPathsSetup(t *testing.T, srv *Server) (piKey, v2 uint64) {
	t.Helper()
	v1 := forkPathsDeploy(t, srv, forkPathsV1)
	code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", v1),
		`{"variables":{"reviewers":["ann","bob"],"note":"urgent"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, body)
	}
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &inst); err != nil || inst.Key == 0 {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	return inst.Key, forkPathsDeploy(t, srv, forkPathsV2)
}

// forkPathsFork posts a fork straight to the handler as an operator.
func forkPathsFork(srv *Server, piKey uint64, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/migrate/fork", piKey), strings.NewReader(body))
	req.SetPathValue("key", fmt.Sprint(piKey))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "usr_ops", Username: "ops", Roles: []string{RoleOperator}}))
	rec := httptest.NewRecorder()
	srv.handleForkInstance(rec, req)
	return rec
}

// TestForkPathsRefusesARequestItCannotRead: a fork ends the instance it continues,
// so a body that does not parse — no target, no reason — ends nothing.
func TestForkPathsRefusesARequestItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	piKey, _ := forkPathsSetup(t, srv)
	if rec := forkPathsFork(srv, piKey, `{"targetProcessDefKey":`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "invalid JSON body") {
		t.Fatalf("malformed fork: %d (%s), want 400", rec.Code, rec.Body)
	}
	var (
		pi  *model.ProcessInstanceValue
		ok  bool
		err error
	)
	srv.do(func() { pi, ok, err = srv.store.ProcessInstance(piKey) })
	if err != nil || !ok || pi.State != model.PIActive || pi.SuccessorInstanceKey != 0 {
		t.Errorf("instance after a refused fork = %+v (ok=%v err=%v), want it still running and unforked", pi, ok, err)
	}
}

// TestForkPathsSaysOnceWhereTheWorkIsAndWhatCrosses: an instance parked at one
// element through several tokens — a parallel review's body and its two reviewers —
// is parked in one place, and a deadline armed on that element is not a place the
// work is at all. The plan counts what crosses with the successor — the variables,
// and only the data objects the target version still declares — and the open tasks
// that end with the predecessor. The fork records the operator who made it.
func TestForkPathsSaysOnceWhereTheWorkIsAndWhatCrosses(t *testing.T) {
	srv := newServerForErrors(t)
	piKey, v2 := forkPathsSetup(t, srv)

	rec := forkPathsFork(srv, piKey, fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"review moved to v2"}`, v2))
	if rec.Code != http.StatusOK {
		t.Fatalf("fork: %d (%s)", rec.Code, rec.Body)
	}
	var plan forkPlanResp
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if len(plan.Parked) != 1 || plan.Parked[0].ElementID != "review" || plan.Parked[0].ResumeAt != "review" {
		t.Errorf("parked = %+v, want the review once, resuming at the review", plan.Parked)
	}
	if plan.Variables != 2 {
		t.Errorf("variables = %d, want reviewers and note", plan.Variables)
	}
	if plan.Jobs != 2 {
		t.Errorf("jobs = %d, want the two reviewers' tasks, which end with the predecessor", plan.Jobs)
	}
	if plan.DataObjects != 1 {
		t.Errorf("data objects = %d, want only the order — v2 no longer declares the invoice", plan.DataObjects)
	}
	if plan.SuccessorInstanceKey == 0 {
		t.Fatalf("plan = %+v, want the successor named", plan)
	}

	var actions []model.OperatorActionValue
	var err error
	srv.do(func() {
		err = srv.store.OperatorActionHistory(piKey, func(_ int64, _ uint64, v *model.OperatorActionValue) error {
			actions = append(actions, *v)
			return nil
		})
	})
	if err != nil || len(actions) != 1 || actions[0].Kind != model.OperatorActionForkedTo || actions[0].Actor != "ops" {
		t.Errorf("predecessor's record = %+v (%v), want a fork made by ops", actions, err)
	}
}
