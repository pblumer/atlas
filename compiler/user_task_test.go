package compiler

import (
	"strings"
	"testing"
)

const userTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="approval" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:userTask id="review" name="Review tweet">
      <bpmn:extensionElements>
        <zeebe:assignmentDefinition assignee="editor" candidateGroups="reviewers"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseUserTask(t *testing.T) {
	cp, err := Parse(10, 1, strings.NewReader(userTaskBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	starts := cp.StartEvents()
	if len(starts) != 1 {
		t.Fatalf("start events = %d, want 1", len(starts))
	}

	out := cp.Outgoing(starts[0])
	if len(out) != 1 {
		t.Fatalf("start outgoing = %d, want 1", len(out))
	}
	task := cp.Flow(out[0]).Target
	node := cp.Node(task)
	if node.Type != TypeUserTask {
		t.Fatalf("expected user task after start, got %v", node.Type)
	}
	detail := cp.UserTask(node.Detail)
	if cp.Intern(detail.JobType) != UserTaskJobType {
		t.Errorf("job type = %q, want %q", cp.Intern(detail.JobType), UserTaskJobType)
	}
	if cp.Intern(detail.Assignee) != "editor" {
		t.Errorf("assignee = %q, want \"editor\"", cp.Intern(detail.Assignee))
	}
	if cp.Intern(detail.CandidateGroups) != "reviewers" {
		t.Errorf("candidateGroups = %q, want \"reviewers\"", cp.Intern(detail.CandidateGroups))
	}
	if detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}

	out = cp.Outgoing(task)
	if len(out) != 1 || cp.Node(cp.Flow(out[0]).Target).Type != TypeEndEvent {
		t.Errorf("expected end event after user task")
	}
}

func TestParseUserTaskNoAssignment(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <userTask id="t"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := cp.Outgoing(cp.StartEvents()[0])
	task := cp.Flow(out[0]).Target
	detail := cp.UserTask(cp.Node(task).Detail)
	if detail.Assignee != -1 {
		t.Errorf("assignee = %d, want -1 (unset)", detail.Assignee)
	}
	if detail.CandidateGroups != -1 {
		t.Errorf("candidateGroups = %d, want -1 (unset)", detail.CandidateGroups)
	}
}
