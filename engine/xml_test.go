package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/chrampfer/compiler"
	"github.com/pblumer/chrampfer/engine"
)

const orderBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="payment" retries="3"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="charge"/>
    <bpmn:sequenceFlow id="f2" sourceRef="charge" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

// TestExecuteParsedBPMN deploys a process compiled straight from BPMN XML and
// runs it to completion, proving the XML front end feeds the engine.
func TestExecuteParsedBPMN(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, err := compiler.Parse(7, 1, strings.NewReader(orderBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Job type of the single service task (start's successor).
	taskID := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	jobType := cp.ServiceTask(cp.Node(taskID).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}

	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
