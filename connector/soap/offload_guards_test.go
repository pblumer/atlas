package soap

import (
	"strings"
	"testing"
)

// A task with no compiled detail is a payload arm asking for a kind this node is not.
// An error rather than a zero Job, because a zero Job would be refused later as "no
// endpoint" — sending whoever reads it to look at the model instead of at the code
// that resolved it. (The empty-endpoint refusal itself lives in Run, so it reads the
// same from either half; worker/soapconnector_test.go covers that one.)
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := Resolve(nil, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}
