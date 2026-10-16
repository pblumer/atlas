package job

import (
	"errors"
	"strings"
)

// BPMNError is how a handler ends a job in a BPMN error instead of a result
// (ADR-0089): return one, wrapped or not, and the runner throws Code rather than
// failing the job. An error boundary or error event subprocess matching Code catches
// it and the instance takes the error path, with no retry spent. With none in reach
// the job waits on an incident naming Code and Message until an operator resolves it.
//
// It is for an answer the process has a path for — an account that already exists, a
// recipient that does not — not for a fault: anything else a handler returns fails the
// job and is retried as before. The out-of-process worker's type is this one under
// another name (worker.BPMNError), so a handler shared by both paths says it once.
type BPMNError struct {
	Code    string
	Message string
}

func (e *BPMNError) Error() string {
	if e.Message == "" {
		return "BPMN error " + e.Code
	}
	return "BPMN error " + e.Code + ": " + e.Message
}

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
