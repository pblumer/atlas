package job

import "github.com/pblumer/atlas/workersdk"

// RejectedError marks a failure the target answered: it was reached and refused the
// request as wrong. The job fails as any other does — an attempt spent, an incident once
// none are left — but the [Gate] hears that the target answered rather than that it
// failed, so a run of bad requests is never judged an outage (ADR-0340, amended
// 2026-11-11).
//
// It is the Worker protocol's type (workersdk.RejectedError) under this package's name,
// for the reason [BPMNError] is: a handler shared by the in-process runner and an
// out-of-process worker marks it once.
type RejectedError = workersdk.RejectedError

// Rejected marks err as a rejection by the target; nil stays nil.
func Rejected(err error) error { return workersdk.Rejected(err) }

// IsRejected reports whether err, or anything it wraps, was marked with [Rejected].
func IsRejected(err error) bool { return workersdk.IsRejected(err) }
