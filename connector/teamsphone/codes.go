package teamsphone

import (
	"errors"
	"slices"

	"github.com/pblumer/atlas/job"
)

// The codes of the refusals a process may have a path for (ADR-0463, amendment
// 2026-10-20). Each is a BPMN error when the task's process catches it — an error
// boundary on the task, or an error event subprocess around it, naming the code — and
// an ordinary failure otherwise, which keeps the retries a transient fault is owed
// (ADR-0461's rule). The handbook's operations reference lists them; [Codes] is the list
// it is held to.
const (
	// CodeUPNTaken: ensure-resource-account was given the address of an enabled user
	// account, which is somebody's, not a resource account.
	CodeUPNTaken = "TEAMSPHONE_UPN_TAKEN"
	// CodeAccountTypeMismatch: the resource account was made for the other kind of voice
	// app — an auto attendant's account for a queue, or the reverse.
	CodeAccountTypeMismatch = "TEAMSPHONE_ACCOUNT_TYPE_MISMATCH"
	// CodeAccountInUse: the resource account is associated with the other kind of voice
	// app, which the operation will not take over or remove.
	CodeAccountInUse = "TEAMSPHONE_ACCOUNT_IN_USE"
	// CodeAccountNotVisible: Teams still does not show the resource account after the
	// wait; Teams synchronizes a new account after a delay.
	CodeAccountNotVisible = "TEAMSPHONE_ACCOUNT_NOT_VISIBLE"
	// CodeWouldEmptyList: an empty list would take every agent or every authorized user
	// away from a queue that has some, which an ensure never does.
	CodeWouldEmptyList = "TEAMSPHONE_WOULD_EMPTY_LIST"
	// CodeTooManyAgents: more agents than a call queue takes directly.
	CodeTooManyAgents = "TEAMSPHONE_TOO_MANY_AGENTS"
	// CodeNumberInUse: the phone number belongs to another user or resource account,
	// which an ensure will not take it from.
	CodeNumberInUse = "TEAMSPHONE_NUMBER_IN_USE"
	// CodeNumberNotFound: a Calling Plan or Operator Connect number the tenant does not
	// have; it has to be acquired or ported first.
	CodeNumberNotFound = "TEAMSPHONE_NUMBER_NOT_FOUND"
)

// Codes lists every code a refusal carries, in the order the handbook explains them.
var Codes = []string{
	CodeUPNTaken, CodeAccountTypeMismatch, CodeAccountInUse,
	CodeAccountNotVisible, CodeWouldEmptyList, CodeTooManyAgents,
	CodeNumberInUse, CodeNumberNotFound,
}

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
	return &Refusal{Code: code, Message: "teamsphone: " + op + ": " + msg}
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
