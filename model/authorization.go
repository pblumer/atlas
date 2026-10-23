package model

import (
	"encoding/binary"
)

// AuthzEffect is a policy decision's effect as the log records it. The numbers are
// authz.Effect's and are pinned by a test, because a decision decoded years later must
// read back as what it was.
type AuthzEffect uint8

const (
	AuthzIndeterminate AuthzEffect = 0
	AuthzPermit        AuthzEffect = 1
	AuthzDeny          AuthzEffect = 2
	AuthzNotApplicable AuthzEffect = 3
)

func (e AuthzEffect) String() string {
	switch e {
	case AuthzPermit:
		return "Permit"
	case AuthzDeny:
		return "Deny"
	case AuthzNotApplicable:
		return "NotApplicable"
	default:
		return "Indeterminate"
	}
}

// AuthorizationValue is one policy decision about one external Worker action, the
// evidence an enforcement point writes before it releases anything the action needs
// (ADR-0475 §12).
//
// Everything in it was decided on the command path and is frozen here: applyToState
// stores it and nothing else, so replay rebuilds the same history without evaluating
// a policy, and a decision already in the log is never recomputed (I4/I6).
//
// It names people by stable principal id and never by name or address, and it never
// holds a token, a secret or a credential reference. Detail is a bounded JSON object —
// obligations, approvals used, the trace — written by the enforcement point.
type AuthorizationValue struct {
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	JobKey             uint64
	// LeaseEpoch is the lease the decision was made under. A decision is a decision
	// for that lease only: the next lease needs its own.
	LeaseEpoch uint64
	// DecisionKey is the event's own key, minted on the command path.
	DecisionKey uint64
	// DecidedAt is the instant the request was evaluated at, read by the enforcement
	// point from its clock.
	DecidedAt int64
	Effect    AuthzEffect
	// Enforced is false for a shadow decision: recorded, labeled, never blocking.
	Enforced      bool
	PolicyVersion uint32

	ReasonCode     string
	PolicyID       string
	Worker         string // the configured Worker
	WorkerInstance string // the lease holder's self-asserted label
	WorkerType     string
	Operation      string
	Resource       string
	ActionDigest   string
	InitiatedBy    string // the instance's StartedBy
	ExecutedFor    string // the Atlas principal the action ran for
	ExecutedAs     string // the external identity key, or service:<worker>
	ConnectionID   string
	IdentityMode   string
	Detail         string
}

func (*AuthorizationValue) ValueType() ValueType { return VTAuthorization }

const authorizationFixedSize = 8*6 + 1 + 1 + 4

func (v *AuthorizationValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.JobKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.LeaseEpoch)
	dst = binary.LittleEndian.AppendUint64(dst, v.DecisionKey)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.DecidedAt))
	dst = append(dst, byte(v.Effect))
	enforced := byte(0)
	if v.Enforced {
		enforced = 1
	}
	dst = append(dst, enforced)
	dst = binary.LittleEndian.AppendUint32(dst, v.PolicyVersion)
	// Written one by one rather than from a slice of them: the encoder runs inside the
	// processor's batch, which allocates nothing per record (I1).
	dst = appendString(dst, v.ReasonCode)
	dst = appendString(dst, v.PolicyID)
	dst = appendString(dst, v.Worker)
	dst = appendString(dst, v.WorkerInstance)
	dst = appendString(dst, v.WorkerType)
	dst = appendString(dst, v.Operation)
	dst = appendString(dst, v.Resource)
	dst = appendString(dst, v.ActionDigest)
	dst = appendString(dst, v.InitiatedBy)
	dst = appendString(dst, v.ExecutedFor)
	dst = appendString(dst, v.ExecutedAs)
	dst = appendString(dst, v.ConnectionID)
	dst = appendString(dst, v.IdentityMode)
	return appendString(dst, v.Detail)
}

func (v *AuthorizationValue) decode(src []byte) error {
	if len(src) < authorizationFixedSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.JobKey = binary.LittleEndian.Uint64(src[16:])
	v.LeaseEpoch = binary.LittleEndian.Uint64(src[24:])
	v.DecisionKey = binary.LittleEndian.Uint64(src[32:])
	v.DecidedAt = int64(binary.LittleEndian.Uint64(src[40:]))
	v.Effect = AuthzEffect(src[48])
	v.Enforced = src[49] == 1
	v.PolicyVersion = binary.LittleEndian.Uint32(src[50:])
	r := stringReader{rest: src[authorizationFixedSize:]}
	v.ReasonCode = r.next()
	v.PolicyID = r.next()
	v.Worker = r.next()
	v.WorkerInstance = r.next()
	v.WorkerType = r.next()
	v.Operation = r.next()
	v.Resource = r.next()
	v.ActionDigest = r.next()
	v.InitiatedBy = r.next()
	v.ExecutedFor = r.next()
	v.ExecutedAs = r.next()
	v.ConnectionID = r.next()
	v.IdentityMode = r.next()
	v.Detail = r.next()
	return r.err
}

// stringReader reads length-prefixed strings in sequence and keeps the first error, so
// a decoder can read a run of them without checking each.
type stringReader struct {
	rest []byte
	err  error
}

func (r *stringReader) next() string {
	if r.err != nil {
		return ""
	}
	s, tail, err := readString(r.rest)
	if err != nil {
		r.err = err
		return ""
	}
	r.rest = tail
	return s
}

// ApprovalKind says which fact about an approval an ActionApprovalValue is.
type ApprovalKind uint8

const (
	// ApprovalRequested opens an approval: a decision found an approval obligation
	// unmet and asked for one, of one action digest.
	ApprovalRequested ApprovalKind = 1
	// ApprovalDecided is one approver's answer.
	ApprovalDecided ApprovalKind = 2
	// ApprovalRevoked withdraws an approver's earlier answer.
	ApprovalRevoked ApprovalKind = 3
)

func (k ApprovalKind) String() string {
	switch k {
	case ApprovalRequested:
		return "requested"
	case ApprovalDecided:
		return "decided"
	case ApprovalRevoked:
		return "revoked"
	default:
		return "ApprovalKind(?)"
	}
}

// ApprovalOutcome is an approver's answer.
type ApprovalOutcome uint8

const (
	ApprovalOutcomeGranted  ApprovalOutcome = 1
	ApprovalOutcomeRejected ApprovalOutcome = 2
)

func (o ApprovalOutcome) String() string {
	switch o {
	case ApprovalOutcomeGranted:
		return "granted"
	case ApprovalOutcomeRejected:
		return "rejected"
	default:
		return ""
	}
}

// ActionApprovalValue is one fact about an approval of one action
// (ADR-0475 §9): that it was requested, that an
// approver answered, or that an answer was withdrawn. Every fact is its own event,
// keyed under its instance and approval, so the approval's history is the sequence of
// them and nothing is ever overwritten.
//
// An approval is of ActionDigest and nothing else. Who may give it — ApproverGroup,
// Approvals, the separation-of-duties rules — is checked by the server when the
// answer is given and again by the policy when the action is authorized; the record
// says what happened, not whether it still counts.
type ActionApprovalValue struct {
	ProcessInstanceKey uint64
	ApprovalKey        uint64 // the request's key; every later fact names it
	JobKey             uint64 // the job whose decision asked
	DecisionKey        uint64 // the decision that asked
	At                 int64  // when this fact happened, frozen by the caller
	ExpiresAt          int64  // until when an answer may be given (requested)
	Kind               ApprovalKind
	Outcome            ApprovalOutcome // decided
	Approvals          uint8           // how many distinct approvers the obligation needs
	PolicyVersion      uint32

	ActionDigest  string
	Worker        string
	Operation     string
	Resource      string
	RequestedBy   string // the principal the action would run for
	ApproverGroup string
	PolicyID      string
	ObligationID  string
	// Principal is who answered or withdrew; empty on a request.
	Principal string
	Reason    string
}

func (*ActionApprovalValue) ValueType() ValueType { return VTActionApproval }

const actionApprovalFixedSize = 8*6 + 3 + 4

func (v *ActionApprovalValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ApprovalKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.JobKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.DecisionKey)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.At))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(v.ExpiresAt))
	dst = append(dst, byte(v.Kind), byte(v.Outcome), v.Approvals)
	dst = binary.LittleEndian.AppendUint32(dst, v.PolicyVersion)
	dst = appendString(dst, v.ActionDigest)
	dst = appendString(dst, v.Worker)
	dst = appendString(dst, v.Operation)
	dst = appendString(dst, v.Resource)
	dst = appendString(dst, v.RequestedBy)
	dst = appendString(dst, v.ApproverGroup)
	dst = appendString(dst, v.PolicyID)
	dst = appendString(dst, v.ObligationID)
	dst = appendString(dst, v.Principal)
	return appendString(dst, v.Reason)
}

func (v *ActionApprovalValue) decode(src []byte) error {
	if len(src) < actionApprovalFixedSize {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src[0:])
	v.ApprovalKey = binary.LittleEndian.Uint64(src[8:])
	v.JobKey = binary.LittleEndian.Uint64(src[16:])
	v.DecisionKey = binary.LittleEndian.Uint64(src[24:])
	v.At = int64(binary.LittleEndian.Uint64(src[32:]))
	v.ExpiresAt = int64(binary.LittleEndian.Uint64(src[40:]))
	v.Kind = ApprovalKind(src[48])
	v.Outcome = ApprovalOutcome(src[49])
	v.Approvals = src[50]
	v.PolicyVersion = binary.LittleEndian.Uint32(src[51:])
	r := stringReader{rest: src[actionApprovalFixedSize:]}
	v.ActionDigest = r.next()
	v.Worker = r.next()
	v.Operation = r.next()
	v.Resource = r.next()
	v.RequestedBy = r.next()
	v.ApproverGroup = r.next()
	v.PolicyID = r.next()
	v.ObligationID = r.next()
	v.Principal = r.next()
	v.Reason = r.next()
	return r.err
}
