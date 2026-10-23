// Package authz is Atlas's policy decision point: it decides whether one external
// Worker action may run, and why
// (ADR-0475).
//
// It is pure. [Set.Evaluate] reads its arguments and nothing else — no store, no
// network, no clock (the instant is in the request) — so a decision is a function of
// its request and can be explained, tested and simulated as one. Assembling the
// request from trusted server-side facts is the caller's job (the policy information
// point), and acting on the answer is the enforcement point's; neither lives here.
//
// It fails closed. The zero [Decision] is Indeterminate, an evaluation error is
// Indeterminate, no applicable policy is NotApplicable, and only [Decision.Permitted]
// lets an action through.
package authz

// Effect is what a decision, a policy or a rule says.
type Effect uint8

const (
	// Indeterminate is the zero value: something could not be decided — a missing
	// attribute, a condition that did not evaluate to a boolean, a policy set that
	// is not there. Never a permit.
	Indeterminate Effect = iota
	// Permit lets the action run, once every obligation it carries is satisfied.
	Permit
	// Deny refuses it.
	Deny
	// NotApplicable means nothing spoke to this request. The enforcement point
	// treats it as a refusal: Atlas authorizes by default deny.
	NotApplicable
)

func (e Effect) String() string {
	switch e {
	case Permit:
		return "Permit"
	case Deny:
		return "Deny"
	case NotApplicable:
		return "NotApplicable"
	default:
		return "Indeterminate"
	}
}

// ParseEffect reads an effect as a policy or a DMN decision writes it. Indeterminate
// is not something a rule may answer on purpose, so it does not parse.
func ParseEffect(s string) (Effect, bool) {
	switch s {
	case "Permit":
		return Permit, true
	case "Deny":
		return Deny, true
	case "NotApplicable":
		return NotApplicable, true
	}
	return Indeterminate, false
}

// Identity modes a configured Worker may declare.
const (
	// ModeService executes as the Worker's own service identity. The Worker
	// Instance holds that credential, so enforcement is cooperative.
	ModeService = "service"
	// ModeDelegatedFixed executes as one connection's external identity, for the
	// connection's owner.
	ModeDelegatedFixed = "delegated-fixed"
	// ModeDelegatedPerJob executes as the executing subject's own connection,
	// chosen for each job.
	ModeDelegatedPerJob = "delegated-per-job"
)

// IsDelegated reports whether mode executes as a person through a connection, which
// is what makes a subject and a connection mandatory.
func IsDelegated(mode string) bool {
	return mode == ModeDelegatedFixed || mode == ModeDelegatedPerJob
}

func knownMode(mode string) bool {
	return mode == ModeService || IsDelegated(mode)
}

// Connection states.
const (
	ConnectionActive         = "active"
	ConnectionReauthRequired = "reauth_required"
	ConnectionRevoked        = "revoked"
)

// Approval outcomes.
const (
	ApprovalGranted  = "granted"
	ApprovalRejected = "rejected"
)

// MaxListLen bounds every list a request carries, and MaxApprovals the approvals:
// evaluation time is bounded by its input, because neither FEEL nor temis offers a
// step budget to bound it otherwise.
const (
	MaxListLen   = 256
	MaxApprovals = 64
)

// Request is everything a decision may depend on. Every field is filled by the
// server from its own records; nothing in it comes from a BPMN model's claims, a
// process variable or a Worker Instance's report.
type Request struct {
	Subject  Subject
	Actor    Actor
	Action   Action
	Resource Resource
	Context  Context
}

// Subject is the Atlas principal the action is executed for.
type Subject struct {
	// ID is the stable principal id ("usr_…"); empty when there is none.
	ID string
	// Known says ID was found in the user store; Disabled that the account is
	// deactivated. Both are read at decision time — current state decides.
	Known    bool
	Disabled bool
	// Roles and Groups are the principal's current roles and group ids.
	Roles  []string
	Groups []string
}

// Actor is what executes the action: the configured Worker, its identity mode, and
// the identity it acts as.
type Actor struct {
	IdentityMode string
	Worker       string // the configured Worker the compiled task names
	WorkerType   string
	// WorkerInstance is the lease holder's self-asserted label. It is recorded and
	// may be matched by nothing that decides — it is not a credential.
	WorkerInstance string
	// ExecutedAs is the external identity key, or "service:<worker>".
	ExecutedAs string
	// Connection is the connection a delegated mode uses; nil when there is none.
	Connection *Connection
}

// Connection is the public half of a connection: never its credential.
type Connection struct {
	ID             string   `json:"id"`
	OwnerID        string   `json:"ownerId"`
	Status         string   `json:"status"`
	AllowedWorkers []string `json:"allowedWorkers"`
	// LastInteractiveAuth is when its owner last signed in at the provider, Unix
	// nanoseconds; 0 when unknown.
	LastInteractiveAuth int64 `json:"lastInteractiveAuth"`
}

// Action is the operation and the digest of everything it will do.
type Action struct {
	Operation string
	// Digest is [Digest] over the Worker Type, the Worker, the operation, the
	// resource and every material parameter. Approvals are of a digest.
	Digest string
}

// Resource is what the action touches, in its canonical spelling
// ("sharepoint:site=…;list=…;path=…").
type Resource struct {
	Type string
	ID   string
}

// Context is when and in which process the action happens.
type Context struct {
	// Time is the evaluation instant, Unix nanoseconds, read by the caller from its
	// clock and frozen into the decision's event.
	Time               int64
	ProcessID          string // the BPMN process id
	ProcessInstanceKey uint64
	JobKey             uint64
	// InitiatedBy is the principal who started the process instance, as the engine
	// recorded it at creation; empty when nobody authenticated did.
	InitiatedBy string
	// Approvals are the approval facts recorded for this instance, with each
	// approver's current group membership.
	Approvals []Approval
}

// Approval is one approver's decision about one action digest.
type Approval struct {
	ID             string   `json:"id"`
	Digest         string   `json:"digest"`
	ApproverID     string   `json:"approverId"`
	ApproverGroups []string `json:"approverGroups"` // the approver's groups now, not when they decided
	Outcome        string   `json:"outcome"`        // ApprovalGranted or ApprovalRejected
	DecidedAt      int64    `json:"decidedAt"`      // Unix nanoseconds
	Revoked        bool     `json:"revoked"`
}
