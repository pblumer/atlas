package workersdk

import "errors"

// RejectedError marks a failure the target answered: it was reached and refused the
// request as wrong — HTTP 400, 404, 401, 422 and the like ([RejectsRequest]). Return
// one, wrapped or not, and the job fails exactly as any other failure does: one attempt
// spent, an incident once none are left. What changes is what the server's circuit
// breaker hears (ADR-0340, amended 2026-11-11): a rejection is not evidence that the
// target is down, so it never counts toward judging it so — it is evidence that the
// target answers.
//
// It is for a request the far side could read and said no to. A timeout, a refused
// connection, a 5xx, a 408 or a 429 is the target's own trouble and is a plain error.
//
// The engine's in-process runner uses this same type under its own name
// (job.RejectedError is an alias of it), so a handler shared by both paths — a REST
// task's call — marks the rejection once, as with [BPMNError].
type RejectedError struct{ Err error }

func (e *RejectedError) Error() string { return e.Err.Error() }

// Unwrap returns the failure the mark was put on.
func (e *RejectedError) Unwrap() error { return e.Err }

// Rejected marks err as a rejection by the target; nil stays nil.
func Rejected(err error) error {
	if err == nil {
		return nil
	}
	return &RejectedError{Err: err}
}

// IsRejected reports whether err, or anything it wraps, was marked with [Rejected].
func IsRejected(err error) bool {
	var re *RejectedError
	return errors.As(err, &re)
}

// RejectsRequest reports whether an HTTP status says the request was wrong rather than
// that the target is in trouble: any 4xx except 408 (the target timed out waiting for
// it) and 429 (the target is shedding load), which are about the target's state and are
// failures like a 503.
func RejectsRequest(status int) bool {
	return status >= 400 && status < 500 &&
		status != 408 && status != 429
}
