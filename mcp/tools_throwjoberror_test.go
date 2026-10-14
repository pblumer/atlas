package mcp_test

import (
	"strings"
	"testing"
)

// An operator throws a BPMN error from a parked job through the tool (ADR-0089). With
// nothing in the model to catch it, the job is kept under an incident naming the code
// and the message, which is how the call's effect is read back here.
func TestThrowJobErrorViaTool(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)

	text, isErr := toolText(t, result(t, run(t, atlas, callTool(4, "atlas_throw_job_error", map[string]any{
		"key": jobKey, "errorCode": "WITHDRAWN", "errorMessage": "by phone", "reason": "the customer called",
	}))[0]))
	if isErr {
		t.Fatalf("throw_job_error returned an error: %s", text)
	}
	incs := incidentJobKeys(t, atlas.URL)
	if len(incs) != 1 || incs[0].JobKey != jobKey || incs[0].Message != "unhandled error 'WITHDRAWN': by phone" {
		t.Fatalf("incidents = %+v, want one on job %d naming the code and message", incs, jobKey)
	}
}

// Throwing by hand is an intervention: without a reason the tool refuses before
// anything is sent, and without a code there is nothing to throw.
func TestThrowJobErrorNeedsACodeAndAReason(t *testing.T) {
	atlas := newAtlas(t)
	jobKey := firstTaskKey(t, atlas)
	for _, args := range []map[string]any{
		{"key": jobKey, "errorCode": "WITHDRAWN"},
		{"key": jobKey, "reason": "r"},
	} {
		text, isErr := toolText(t, result(t, run(t, atlas, callTool(5, "atlas_throw_job_error", args))[0]))
		if !isErr {
			t.Errorf("throw_job_error(%v) succeeded, want a refusal", args)
		}
		if !strings.Contains(text, "reason") && !strings.Contains(text, "errorCode") {
			t.Errorf("refusal %q does not say what is missing", text)
		}
	}
	if n := len(incidentJobKeys(t, atlas.URL)); n != 0 {
		t.Errorf("a refused throw left %d incidents", n)
	}
}
