package engine

import "github.com/pblumer/atlas/model"

// Authorization evidence and approvals
// (ADR-0475 §5, §9, §12).
//
// The decision is made off the run loop, by the enforcement point; the engine's part
// is to make it a fact, fenced on the lease it was made for, and to make its
// consequence — a refused job's incident, a request for approval — durable in the
// same batch, so nothing an enforcement point releases afterwards can rest on a
// decision a crash could lose (I2).

// ApprovalRequiredErrorCode is the BPMN error an enforced refusal throws when what is
// missing is an approval. A model that plans for approval catches it and routes to a
// user task; one that does not parks on an incident, which is the right answer for a
// process that never said what should happen.
const ApprovalRequiredErrorCode = "ATLAS_APPROVAL_REQUIRED"

// AuthorizationAnswer is what recording a decision did.
type AuthorizationAnswer uint8

const (
	// AuthorizationNotProcessed is the zero value: the command never ran.
	AuthorizationNotProcessed AuthorizationAnswer = iota
	// AuthorizationRecorded: the decision is a fact, and its consequence with it.
	AuthorizationRecorded
	// AuthorizationStale: the job is no longer held under the lease the decision was
	// made for — completed, re-leased, failed. Nothing was written, and nothing may be
	// released on the strength of it.
	AuthorizationStale
)

// AuthorizationResult is written through Command.Authorized.
type AuthorizationResult struct {
	Answer      AuthorizationAnswer
	DecisionKey uint64
	// ApprovalKey is the approval the refusal asked for: a new request, or the open
	// one for the same action that it joined.
	ApprovalKey uint64
}

// RecordAuthorization enqueues a decision about the job v names, under the lease v
// names. For an enforced refusal, approval non-nil asks for that approval and throws
// ApprovalRequiredErrorCode; otherwise the job fails with its retries spent, under an
// incident carrying message. A permit, a shadow decision and a stale lease change
// nothing but the record. The answer is written to *result once RunUntilIdle returned.
func (p *Processor) RecordAuthorization(v model.AuthorizationValue, approval *model.ActionApprovalValue, message string, result *AuthorizationResult) {
	p.queue = append(p.queue, Command{
		Key:          v.JobKey,
		ValueType:    model.VTAuthorization,
		Intent:       model.IntentAuthorizationDeciding,
		Value:        inflightValue{authorization: &v, approval: approval},
		ErrorMessage: message,
		Authorized:   result,
	})
}

func handleAuthorizationDeciding(c *ProcessingContext) {
	res := c.cmd.Authorized
	if res == nil {
		res = &AuthorizationResult{}
	}
	in := c.cmd.Value.authorization
	if in == nil {
		return
	}
	v := *in
	// The lease is checked here, on the single writer, and not only where the request
	// arrived: between the two a lease can expire and be handed to another Worker
	// Instance, and a decision for a lease that is gone must not become a fact anybody
	// can act on.
	job := c.GetJob(v.JobKey)
	if job == nil || job.ProcessInstanceKey != v.ProcessInstanceKey || job.LeaseExpiresAt == 0 ||
		job.LeaseEpoch != v.LeaseEpoch || job.Assignee != v.WorkerInstance {
		res.Answer = AuthorizationStale
		return
	}
	v.DecisionKey = c.NewKey()
	c.appendEvent(v.DecisionKey, model.VTAuthorization, model.IntentAuthorizationDecided, inflightValue{authorization: &v})
	res.Answer, res.DecisionKey = AuthorizationRecorded, v.DecisionKey
	if !v.Enforced || v.Effect == model.AuthzPermit {
		return
	}
	if want := c.cmd.Value.approval; want != nil {
		res.ApprovalKey = requestApproval(c, &v, want)
		throwJobError(c, v.JobKey, job, ApprovalRequiredErrorCode, c.cmd.ErrorMessage)
		return
	}
	// A refusal is not a failure to retry: the next attempt would be refused for the
	// same reason. So the job parks with its retries spent, and resolving the incident
	// is what offers it again — to be decided afresh.
	job.Retries = 0
	job.LeaseExpiresAt = 0
	recordJobFailure(c, v.JobKey, job, 0, c.cmd.ErrorMessage, model.IncidentAuthorizationDenied)
}

// requestApproval opens an approval of the decision's action, or joins the open one: a
// request for the same digest that has not expired. Joining is what keeps a retried
// action from collecting a pile of identical requests for one approver to answer.
func requestApproval(c *ProcessingContext, d *model.AuthorizationValue, want *model.ActionApprovalValue) uint64 {
	now := c.Now()
	var open uint64
	if err := c.tx.ApprovalFactsInBatch(d.ProcessInstanceKey, func(f *model.ActionApprovalValue) error {
		if f.Kind == model.ApprovalRequested && f.ActionDigest == d.ActionDigest && f.ExpiresAt > now {
			open = f.ApprovalKey
		}
		return nil
	}); err != nil {
		c.p.fail(err)
		return 0
	}
	if open != 0 {
		return open
	}
	a := *want
	a.Kind = model.ApprovalRequested
	a.ProcessInstanceKey, a.JobKey, a.DecisionKey = d.ProcessInstanceKey, d.JobKey, d.DecisionKey
	a.ActionDigest, a.Worker, a.Operation, a.Resource = d.ActionDigest, d.Worker, d.Operation, d.Resource
	a.PolicyID, a.PolicyVersion = d.PolicyID, d.PolicyVersion
	a.At = now
	a.ApprovalKey = c.NewKey()
	c.appendEvent(a.ApprovalKey, model.VTActionApproval, model.IntentApprovalRecorded, inflightValue{approval: &a})
	return a.ApprovalKey
}

// ApprovalAnswer is what an approver's answer, or a withdrawal, did.
type ApprovalAnswer uint8

const (
	ApprovalNotProcessed ApprovalAnswer = iota
	// ApprovalRecordedAnswer: the answer or withdrawal is a fact.
	ApprovalRecordedAnswer
	// ApprovalUnknown: the instance holds no request of that key.
	ApprovalUnknown
	// ApprovalClosed: the request expired before the answer arrived.
	ApprovalClosed
	// ApprovalAlreadyAnswered: this principal has an answer standing; withdraw it first.
	ApprovalAlreadyAnswered
	// ApprovalNothingToWithdraw: this principal has no standing answer.
	ApprovalNothingToWithdraw
	// ApprovalInvalid: the fact is neither an answer nor a withdrawal, or names nobody.
	ApprovalInvalid
)

// ApprovalResult is written through Command.Approved. Request is the request the fact
// was checked against, when there was one.
type ApprovalResult struct {
	Answer  ApprovalAnswer
	Request model.ActionApprovalValue
}

// RecordApprovalAnswer enqueues an approver's answer (Kind ApprovalDecided, with an
// Outcome) or the withdrawal of one (Kind ApprovalRevoked) for the request
// v.ApprovalKey names in instance v.ProcessInstanceKey. Who may answer — the approver
// group, separation of duties — is the caller's check, against the user store the
// engine does not read; this checks what only the log knows: that the request exists
// and is open, and what this principal has already said. v.At is the caller's clock.
func (p *Processor) RecordApprovalAnswer(v model.ActionApprovalValue, result *ApprovalResult) {
	p.queue = append(p.queue, Command{
		Key:       v.ApprovalKey,
		ValueType: model.VTActionApproval,
		Intent:    model.IntentApprovalDeciding,
		Value:     inflightValue{approval: &v},
		Approved:  result,
	})
}

func handleApprovalDeciding(c *ProcessingContext) {
	res := c.cmd.Approved
	if res == nil {
		res = &ApprovalResult{}
	}
	in := c.cmd.Value.approval
	if in == nil {
		return
	}
	v := *in
	if v.Principal == "" || (v.Kind == model.ApprovalDecided && v.Outcome != model.ApprovalOutcomeGranted &&
		v.Outcome != model.ApprovalOutcomeRejected) || (v.Kind != model.ApprovalDecided && v.Kind != model.ApprovalRevoked) {
		res.Answer = ApprovalInvalid
		return
	}
	var (
		request  model.ActionApprovalValue
		found    bool
		standing bool // this principal has an answer that has not been withdrawn
	)
	if err := c.tx.ApprovalFactsInBatch(v.ProcessInstanceKey, func(f *model.ActionApprovalValue) error {
		if f.ApprovalKey != v.ApprovalKey {
			return nil
		}
		switch f.Kind {
		case model.ApprovalRequested:
			request, found = *f, true
		case model.ApprovalDecided:
			if f.Principal == v.Principal {
				standing = true
			}
		case model.ApprovalRevoked:
			if f.Principal == v.Principal {
				standing = false
			}
		}
		return nil
	}); err != nil {
		c.p.fail(err)
		return
	}
	res.Request = request
	switch {
	case !found:
		res.Answer = ApprovalUnknown
		return
	case v.Kind == model.ApprovalDecided && v.At >= request.ExpiresAt:
		res.Answer = ApprovalClosed
		return
	case v.Kind == model.ApprovalDecided && standing:
		res.Answer = ApprovalAlreadyAnswered
		return
	case v.Kind == model.ApprovalRevoked && !standing:
		res.Answer = ApprovalNothingToWithdraw
		return
	}
	// The fact names the action it is about, copied from the request, so a reader of
	// one answer never has to join it back to know what was agreed to.
	v.ActionDigest, v.Worker, v.Operation, v.Resource = request.ActionDigest, request.Worker, request.Operation, request.Resource
	v.RequestedBy, v.ApproverGroup, v.Approvals = request.RequestedBy, request.ApproverGroup, request.Approvals
	v.PolicyID, v.PolicyVersion, v.ObligationID = request.PolicyID, request.PolicyVersion, request.ObligationID
	v.JobKey, v.DecisionKey, v.ExpiresAt = request.JobKey, request.DecisionKey, request.ExpiresAt
	c.appendEvent(c.NewKey(), model.VTActionApproval, model.IntentApprovalRecorded, inflightValue{approval: &v})
	res.Answer = ApprovalRecordedAnswer
}
