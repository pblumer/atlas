package clio

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The refusals on the offload seam. Two of the three matter more here than in any
// other kind, because clio's three operations are one Worker Type: read, query and
// write are told apart by a value in the payload rather than by which handler was
// registered. Anything that lets that value be wrong or unrecognised is a read that
// could append to somebody's event store.

// A task with no compiled detail is a payload arm asking for a kind this node is not.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := Resolve(nil, nil, nil, nil, 1, 2)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}

// A job type that is not one of clio's three is an error, not a default. This is the
// refusal the code's own comment is about: defaulting would pick an operation for a
// task that asked for none, and the only operation with a side effect is the write.
func TestResolveRefusesAJobTypeThatIsNotAClioOperation(t *testing.T) {
	_, err := Resolve(nil, nil, &compiler.ConnectorTaskDetail{JobType: -1}, nil, 1, 2)
	if err == nil {
		t.Fatal("a non-clio job type resolved to a clio operation")
	}
	if !strings.Contains(err.Error(), "not a clio operation") {
		t.Errorf("error = %v, want it to say the job type is not a clio operation", err)
	}
}

// And the same refusal on the far side: a payload whose operation word this switch
// does not know fails the job rather than falling through to one that does. Both
// halves reach it, which is why it is in Run.
func TestRunRefusesAnOperationItDoesNotKnow(t *testing.T) {
	_, err := Run(context.Background(), Job{Operation: "append-quietly"}, nil)
	if err == nil {
		t.Fatal("an unknown operation was accepted")
	}
	if !strings.Contains(err.Error(), "append-quietly") {
		t.Errorf("error = %v, want it to name the operation it was given", err)
	}
}
