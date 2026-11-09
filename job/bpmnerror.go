package job

import (
	"errors"
	"strings"

	"github.com/pblumer/atlas/workersdk"
)

// BPMNError is how a handler ends a job in a BPMN error instead of a result
// (ADR-0089): return one, wrapped or not, and the runner throws Code rather than
// failing the job. An error boundary or error event subprocess matching Code catches
// it and the instance takes the error path, with no retry spent. With none in reach
// the job waits on an incident naming Code and Message until an operator resolves it.
//
// It is for an answer the process has a path for — an account that already exists, a
// recipient that does not — not for a fault: anything else a handler returns fails the
// job and is retried as before.
//
// It is the Worker protocol's type (workersdk.BPMNError) under this package's name, so
// a handler shared by the in-process runner and an out-of-process worker says it once.
// The type lives in workersdk because that package may import nothing of the engine;
// this one may import it, since workersdk is a leaf.
type BPMNError = workersdk.BPMNError

// thrownError returns the BPMN error err carries, its code trimmed, if it carries one
// with a code. One without a code names nothing a catch could match and the engine
// would refuse it, so it is the handler's own fault and fails the job like any other.
func thrownError(err error) (BPMNError, bool) {
	var be *BPMNError
	if !errors.As(err, &be) {
		return BPMNError{}, false
	}
	code := strings.TrimSpace(be.Code)
	if code == "" {
		return BPMNError{}, false
	}
	return BPMNError{Code: code, Message: be.Message}, true
}
