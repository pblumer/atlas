package sharepoint

import (
	"context"
	"strings"
	"testing"
)

// The two refusals on the offload seam. Neither is a defensive nicety: each is a
// deployment mistake that has a right answer, and the wrong answer in both cases is
// a nil dereference in the process that owns the partition's state.

// A task with no compiled detail is a payload arm asking for a kind the node is not.
// It has to be an error rather than a zero Job, because a zero Job names no instance
// and would be reported as "the instance "" is not configured" — sending whoever
// reads it to look for a Console record instead of at the code that resolved it.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := Resolve(nil, nil, nil, nil, 1, 2)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}

// A job whose registry is nil means this process was handed SharePoint work while
// holding no instances — an engine that is offloading the kind, or a worker that was
// not given any. The message names that, because "instance not configured" would
// point at the Console when the answer is in the deployment.
func TestRunRefusesAJobWithNoRegistry(t *testing.T) {
	_, err := Run(context.Background(), Job{Connector: "intranet"}, nil)
	if err == nil {
		t.Fatal("a job ran against no registry at all")
	}
	if !strings.Contains(err.Error(), "intranet") {
		t.Errorf("error = %v, want it to name the instance the job asked for", err)
	}
}
