package sharepoint

import (
	"errors"
	"slices"

	"github.com/pblumer/atlas/job"
)

// The codes of the refusals a process may have a path for (ADR-0141, amended
// 2026-11-04). Each is a BPMN error when the task's process catches it — an error
// boundary on the task, or an error event subprocess around it, naming the code — and an
// ordinary failure otherwise, which keeps the retries a transient fault is owed
// (ADR-0461's rule). The handbook's error recipe names them; [Codes] is the list it is
// held to.
//
// Both belong to set-site-quota, the one operation that leaves Graph for SharePoint's
// tenant administration, and both say the same thing to a process: this tenant will not
// let the Worker set the quota, so a person sets it. Neither changes on a retry, which
// is why they are worth a path rather than three attempts and an incident.
const (
	// CodeAdminNotPermitted: the Worker may not use SharePoint's tenant administration —
	// its credential is a client secret, which that API refuses for an app, or the app
	// holds no SharePoint permission that reaches it (Sites.FullControl.All).
	CodeAdminNotPermitted = "SHAREPOINT_ADMIN_NOT_PERMITTED"
	// CodeQuotaRefused: SharePoint's tenant administration answered and refused the
	// quota — typically because the tenant manages site storage automatically, where a
	// site has no quota of its own to set, or because the quota exceeds what the tenant
	// has left.
	CodeQuotaRefused = "SHAREPOINT_QUOTA_REFUSED"
)

// Codes lists every code a refusal carries, in the order the handbook explains them.
var Codes = []string{CodeAdminNotPermitted, CodeQuotaRefused}

// Refusal is an operation's refusal under one of [Codes]. Its message reads as every
// other failure of the package does; [Run] turns it into a BPMN error when the process
// catches its code.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// refused builds a [Refusal] whose message is formatted like the package's other errors.
func refused(code, op, msg string) *Refusal {
	return &Refusal{Code: code, Message: "sharepoint: " + op + ": " + msg}
}

// thrown is err as the job ends with it: a refusal whose code the task's process catches
// becomes the BPMN error the process has a path for, and the instance takes that path at
// once; anything else stays a failure and keeps its retries (ADR-0461).
func thrown(j Job, err error) error {
	var r *Refusal
	if errors.As(err, &r) && slices.Contains(j.ErrorCodes, r.Code) {
		return &job.BPMNError{Code: r.Code, Message: r.Message}
	}
	return err
}
