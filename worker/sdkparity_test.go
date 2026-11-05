package worker_test

import (
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/workersdk"
)

// TestTheSDKRetriesAsLongAsTheServerDoes holds the protocol client's restated retry
// policy to the in-process runner's (ADR-0111, amendment 2026-11-04). workersdk may not
// import job, so the policy exists twice; a Worker Type running out of process must
// still wait exactly as long as one running inside the server before its job raises an
// incident.
func TestTheSDKRetriesAsLongAsTheServerDoes(t *testing.T) {
	for retriesLeft := int32(-1); retriesLeft <= 10; retriesLeft++ {
		if got, want := workersdk.DefaultRetryBackoff(retriesLeft), job.DefaultRetryBackoff(retriesLeft); got != want {
			t.Errorf("retriesLeft %d: workersdk waits %d ns, the in-process runner %d ns", retriesLeft, got, want)
		}
	}
}

// TestABPMNErrorIsOneTypeOnBothPaths keeps a handler shared by the in-process runner
// and an out-of-process worker throwing one error: job.BPMNError and
// workersdk.BPMNError must be the same type, not two that look alike.
func TestABPMNErrorIsOneTypeOnBothPaths(t *testing.T) {
	var fromJob any = &job.BPMNError{Code: "X"}
	if _, ok := fromJob.(*workersdk.BPMNError); !ok {
		t.Fatal("job.BPMNError is not workersdk.BPMNError; a shared handler would throw on one path and fail on the other")
	}
}
