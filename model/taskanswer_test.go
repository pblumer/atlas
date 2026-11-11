package model

import (
	"reflect"
	"testing"
)

func TestATaskAnswerRoundTrips(t *testing.T) {
	want := &TaskAnswerValue{
		ProcessInstanceKey: 1, ElementInstanceKey: 2, JobKey: 3, ProcessDefKey: 4, ElementId: 7,
		Completer: "usr_ada", FormRevision: "sha256:abc", Assignee: "ada", CandidateGroups: "hr,it",
	}
	got := &TaskAnswerValue{}
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
	if want.ValueType() != VTTaskAnswer || VTTaskAnswer.String() != "TaskAnswer" || IntentTaskAnswered.String() != "TaskAnswered" {
		t.Errorf("value type = %v", want.ValueType())
	}
	if err := got.decode(make([]byte, 20)); err != ErrShortBuffer {
		t.Errorf("a short buffer decoded: %v", err)
	}
	if err := got.decode(want.encode(nil)[:45]); err == nil {
		t.Error("a truncated string decoded")
	}
	// The registry hands back the type, so the log can decode it.
	if v, err := DecodeValue(VTTaskAnswer, want.encode(nil)); err != nil || !reflect.DeepEqual(v, want) {
		t.Errorf("DecodeValue = %+v, %v", v, err)
	}
}
