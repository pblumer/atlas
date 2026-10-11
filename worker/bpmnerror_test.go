package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// errorWorkerBPMN is jobWorkerBPMN with an error boundary on the task catching
// NO_RECIPIENT and leading to its own end (ADR-0089).
const errorWorkerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:error id="err" errorCode="NO_RECIPIENT"/>
  <bpmn:process id="orders" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="send-email" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="err"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="handled"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="handled"/>
  </bpmn:process>
</bpmn:definitions>`

// runOnceWith runs one pass of a worker whose only handler returns err.
func runOnceWith(t *testing.T, server string, err error) {
	t.Helper()
	w := worker.New(worker.Options{
		Server: server,
		ID:     "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, err
			}),
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// A handler that ends its work in a BPMN error returns one, and the worker throws it
// instead of failing the job: the boundary on the task catches the code and the
// instance takes the error path, with no retry spent and no incident. Wrapping the
// error on its way out does not hide it.
func TestAHandlersBPMNErrorIsThrownNotFailed(t *testing.T) {
	ts := liveAtlasWith(t, errorWorkerBPMN, `{}`)
	runOnceWith(t, ts.URL, fmt.Errorf("sending: %w", &worker.BPMNError{Code: "NO_RECIPIENT", Message: "mailbox unknown"}))
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the boundary should have caught the error", running)
	}
	if jobs := instanceJobs(t, ts); len(jobs) != 0 {
		t.Errorf("%d jobs still on offer, want none", len(jobs))
	}
}

// With nothing in the model to catch the code, the error lands on an incident that
// names the code and the handler's message, and the job waits there for a resolve
// rather than being retried.
func TestAnUncaughtBPMNErrorWaitsOnAnIncident(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	runOnceWith(t, ts.URL, &worker.BPMNError{Code: "NO_RECIPIENT", Message: "mailbox unknown"})
	if jobs := instanceJobs(t, ts); len(jobs) != 0 {
		t.Errorf("%d jobs on offer, want none: an uncaught error is not retried by itself", len(jobs))
	}
	var page struct {
		Items []struct {
			Message string `json:"message"`
		} `json:"items"`
	}
	if err := json.Unmarshal(get(t, ts, "/api/v1/incidents"), &page); err != nil {
		t.Fatalf("decode incidents: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Message != "unhandled error 'NO_RECIPIENT': mailbox unknown" {
		t.Errorf("incidents = %+v, want one naming the code and the message", page.Items)
	}
}

// A BPMNError says what it is, and an empty one is not mistaken for a code.
func TestABPMNErrorReadsAsItsCodeAndMessage(t *testing.T) {
	if got := (&worker.BPMNError{Code: "X", Message: "why"}).Error(); got != "BPMN error X: why" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&worker.BPMNError{Code: "X"}).Error(); got != "BPMN error X" {
		t.Errorf("Error() without a message = %q", got)
	}
	var be *worker.BPMNError
	if !errors.As(fmt.Errorf("wrapped: %w", &worker.BPMNError{Code: "X"}), &be) || be.Code != "X" {
		t.Error("a wrapped BPMNError is not found by errors.As")
	}
}

// A BPMNError with no code names nothing a catch could match, so it fails the job like
// any other error rather than being refused and handed back after every lease.
func TestABPMNErrorWithoutACodeFailsTheJob(t *testing.T) {
	ts := liveAtlasWith(t, errorWorkerBPMN, `{}`)
	runOnceWith(t, ts.URL, &worker.BPMNError{Message: "forgot the code"})
	jobs := instanceJobs(t, ts)
	if len(jobs) != 1 || jobs[0].Retries != 2 {
		t.Fatalf("jobs = %+v, want the job back on offer with one retry spent", jobs)
	}
}
