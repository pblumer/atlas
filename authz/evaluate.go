package authz

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"
)

// Decision is the PDP's answer, with everything needed to explain it.
type Decision struct {
	Effect     Effect
	ReasonCode string
	// PolicyID and PolicyVersion name the deciding policy: the first Deny under
	// deny-overrides, else the first Permit. Empty for a guard's or a default answer.
	PolicyID      string
	PolicyVersion int
	// Obligations are those of every contributing Permit, satisfied or not.
	Obligations []ObligationResult
	// Trace is every policy that was asked and what it said, in evaluation order.
	Trace []Step
}

// Permitted is the only question an enforcement point asks.
func (d Decision) Permitted() bool { return d.Effect == Permit }

// ObligationResult is one obligation and whether the request satisfies it. An unmet
// approval carries what an approval request needs to be opened for it.
//
// Its JSON names are part of the recorded evidence (an authorization's detail), so
// they are spelled out rather than left to the field names.
type ObligationResult struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	PolicyID      string `json:"policyId"`
	PolicyVersion int    `json:"policyVersion"`
	RuleID        string `json:"ruleId,omitempty"`
	Satisfied     bool   `json:"satisfied"`
	Reason        string `json:"reason,omitempty"` // why it is not satisfied; empty when it is
	// ApprovalsUsed are the ids of the approvals that satisfied it, sorted.
	ApprovalsUsed []string `json:"approvalsUsed,omitempty"`
	// What the approval asks for, so the enforcement point can open a request.
	ApproverGroup    string `json:"approverGroup,omitempty"`
	Approvals        int    `json:"approvals,omitempty"`
	ValidForSeconds  int64  `json:"validForSeconds,omitempty"`
	ExcludeRequester bool   `json:"excludeRequester,omitempty"`
}

// Step is one policy's answer.
type Step struct {
	PolicyID      string
	PolicyVersion int
	RuleID        string // the rule that answered; empty when none did
	Effect        Effect
	Reason        string
	Note          string // why an answer was Indeterminate
}

// Reason codes the PDP itself produces. Rules add their own.
const (
	ReasonPermit              = "PERMIT"
	ReasonPolicyDeny          = "POLICY_DENY"
	ReasonPolicyIndeterminate = "POLICY_INDETERMINATE"
	ReasonNoApplicablePolicy  = "NO_APPLICABLE_POLICY"
	ReasonSetUnavailable      = "POLICY_SET_UNAVAILABLE"
	ReasonApprovalRequired    = "APPROVAL_REQUIRED"
	ReasonApprovalRejected    = "APPROVAL_REJECTED"
	ReasonApprovalExpired     = "APPROVAL_EXPIRED"
	ReasonReauthRequired      = "REAUTHENTICATION_REQUIRED"
)

// Evaluate decides one request. A nil set is a set that could not be loaded, and is
// Indeterminate: "there are no policies" and "the policies are unreadable" must never
// be confused, because the first denies by default while the second would deny for a
// reason nobody can fix by writing a policy.
func (s *Set) Evaluate(ctx context.Context, r *Request) Decision {
	if s == nil {
		return Decision{Effect: Indeterminate, ReasonCode: ReasonSetUnavailable}
	}
	if r == nil {
		return Decision{Effect: Indeterminate, ReasonCode: "MISSING_ATTRIBUTE"}
	}
	if d, stop := guard(r); stop {
		return d
	}
	ev := evaluation{ctx: ctx, r: r}
	deny, permit, indeterminate := -1, -1, -1
	var (
		obligations []ObligationResult
		trace       []Step
	)
	for _, p := range s.policies {
		if !p.applies(r) {
			continue
		}
		step, by := p.evaluate(&ev)
		trace = append(trace, step)
		at := len(trace) - 1
		switch step.Effect {
		case Deny:
			if deny < 0 {
				deny = at
			}
		case Indeterminate:
			if indeterminate < 0 {
				indeterminate = at
			}
		case Permit:
			if permit < 0 {
				permit = at
			}
			for _, ob := range by.obligations {
				obligations = append(obligations, ob.check(r, p, by.id))
			}
		}
	}
	switch {
	case deny >= 0:
		s := trace[deny]
		return Decision{Effect: Deny, ReasonCode: s.Reason, PolicyID: s.PolicyID,
			PolicyVersion: s.PolicyVersion, Obligations: obligations, Trace: trace}
	case indeterminate >= 0:
		s := trace[indeterminate]
		return Decision{Effect: Indeterminate, ReasonCode: ReasonPolicyIndeterminate,
			PolicyID: s.PolicyID, PolicyVersion: s.PolicyVersion, Obligations: obligations, Trace: trace}
	case permit >= 0:
		s := trace[permit]
		d := Decision{Effect: Permit, ReasonCode: s.Reason, PolicyID: s.PolicyID,
			PolicyVersion: s.PolicyVersion, Obligations: obligations, Trace: trace}
		// A Permit holds only with every obligation of every contributing Permit met;
		// the first unmet one decides the refusal and names the policy that asked.
		for _, ob := range obligations {
			if !ob.Satisfied {
				d.Effect, d.ReasonCode = Deny, ob.Reason
				d.PolicyID, d.PolicyVersion = ob.PolicyID, ob.PolicyVersion
				break
			}
		}
		return d
	default:
		return Decision{Effect: NotApplicable, ReasonCode: ReasonNoApplicablePolicy, Trace: trace}
	}
}

// guard runs the built-in checks no policy can override. It answers (and stops) on the
// first one that fails.
func guard(r *Request) (Decision, bool) {
	stop := func(e Effect, reason string) (Decision, bool) {
		return Decision{Effect: e, ReasonCode: reason}, true
	}
	if len(r.Subject.Roles) > MaxListLen || len(r.Subject.Groups) > MaxListLen ||
		len(r.Context.Approvals) > MaxApprovals ||
		(r.Actor.Connection != nil && len(r.Actor.Connection.AllowedWorkers) > MaxListLen) {
		return stop(Indeterminate, "INPUT_TOO_LARGE")
	}
	for _, a := range r.Context.Approvals {
		if len(a.ApproverGroups) > MaxListLen {
			return stop(Indeterminate, "INPUT_TOO_LARGE")
		}
	}
	if !knownMode(r.Actor.IdentityMode) {
		return stop(Indeterminate, "UNKNOWN_IDENTITY_MODE")
	}
	if r.Action.Operation == "" || r.Action.Digest == "" || r.Actor.Worker == "" ||
		r.Actor.WorkerType == "" || r.Context.Time == 0 {
		return stop(Indeterminate, "MISSING_ATTRIBUTE")
	}
	delegated := IsDelegated(r.Actor.IdentityMode)
	if delegated && r.Subject.ID == "" {
		return stop(Deny, "NO_EXECUTING_SUBJECT")
	}
	// A subject named under any mode must be a live account: an initiator who has
	// left does not get their request run as a service either.
	if r.Subject.ID != "" {
		if !r.Subject.Known {
			return stop(Deny, "SUBJECT_UNKNOWN")
		}
		if r.Subject.Disabled {
			return stop(Deny, "SUBJECT_DISABLED")
		}
	}
	if !delegated {
		return Decision{}, false
	}
	c := r.Actor.Connection
	switch {
	case c == nil:
		return stop(Deny, "NO_CONNECTION")
	case c.Status == ConnectionRevoked:
		return stop(Deny, "CONNECTION_REVOKED")
	case c.Status == ConnectionReauthRequired:
		return stop(Deny, "CONNECTION_REAUTH_REQUIRED")
	case c.Status != ConnectionActive:
		return stop(Deny, "CONNECTION_INACTIVE")
	case c.OwnerID != r.Subject.ID:
		return stop(Deny, "CONNECTION_NOT_OWNED")
	case !contains(c.AllowedWorkers, r.Actor.Worker):
		return stop(Deny, "CONNECTION_NOT_ALLOWED_FOR_WORKER")
	}
	return Decision{}, false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (p *Policy) applies(r *Request) bool {
	return p.target.workerTypes.has(r.Actor.WorkerType) && p.target.workers.has(r.Actor.Worker) &&
		p.target.operations.has(r.Action.Operation) && p.target.modes.has(r.Actor.IdentityMode)
}

// evaluate combines the policy's rules and reports its answer and the rule that gave
// it (nil when none did).
func (p *Policy) evaluate(ev *evaluation) (Step, *rule) {
	step := Step{PolicyID: p.id, PolicyVersion: p.version, Effect: NotApplicable}
	var permitStep, indetStep *Step
	var permitBy *rule
	for i := range p.rules {
		r := &p.rules[i]
		eff, reason, note := r.evaluate(ev)
		one := Step{PolicyID: p.id, PolicyVersion: p.version, RuleID: r.id, Effect: eff, Reason: reason, Note: note}
		if p.combining == FirstApplicable {
			if eff != NotApplicable {
				return one, r
			}
			continue
		}
		// deny-overrides
		switch eff {
		case Deny:
			return one, r
		case Indeterminate:
			if indetStep == nil {
				indetStep = &one
			}
		case Permit:
			if permitStep == nil {
				permitStep, permitBy = &one, r
			}
		}
	}
	switch {
	case indetStep != nil:
		return *indetStep, nil
	case permitStep != nil:
		return *permitStep, permitBy
	}
	return step, nil
}

// evaluate answers one rule: its matchers, then its condition or decision.
func (r *rule) evaluate(ev *evaluation) (Effect, string, string) {
	req := ev.r
	m := &r.match
	if !m.roles.hasAny(req.Subject.Roles) || !m.groups.hasAny(req.Subject.Groups) ||
		!m.subjects.has(req.Subject.ID) || !m.operations.has(req.Action.Operation) ||
		!m.workers.has(req.Actor.Worker) || !m.modes.has(req.Actor.IdentityMode) ||
		!m.processes.has(req.Context.ProcessID) || !hasPrefix(m.prefixes, req.Resource.ID) {
		return NotApplicable, "", ""
	}
	if r.condition != nil {
		v, err := r.condition.Eval(ev.feelInputs())
		if err != nil {
			return Indeterminate, ReasonPolicyIndeterminate, "condition: " + err.Error()
		}
		kind, b, _ := expr.Classify(v)
		if kind != expr.KindBool {
			return Indeterminate, ReasonPolicyIndeterminate, "condition evaluated to " + kind.Label() + ", not a boolean"
		}
		if !b {
			return NotApplicable, "", ""
		}
	}
	if r.decision != nil {
		return r.decide(ev)
	}
	reason := r.reason
	if reason == "" {
		if r.effect == Permit {
			reason = ReasonPermit
		} else {
			reason = ReasonPolicyDeny
		}
	}
	return r.effect, reason, ""
}

// decide asks the rule's DMN decision. Anything but an effect it may give — an error,
// a null, an unknown word — is Indeterminate.
func (r *rule) decide(ev *evaluation) (Effect, string, string) {
	res, err := r.decision.Evaluate(ev.ctx, ev.dmnInputs())
	if err != nil {
		return Indeterminate, ReasonPolicyIndeterminate, "decision: " + err.Error()
	}
	var effect, reason any
	switch out := res.(type) {
	case map[string]any:
		effect, reason = out["effect"], out["reasonCode"]
	case string:
		effect = out
	}
	word, _ := effect.(string)
	e, ok := ParseEffect(word)
	if !ok {
		return Indeterminate, ReasonPolicyIndeterminate, "decision answered no effect (want \"Permit\", \"Deny\" or \"NotApplicable\")"
	}
	code, _ := reason.(string)
	if code != "" && !reasonPattern.MatchString(code) {
		return Indeterminate, ReasonPolicyIndeterminate, "decision answered reasonCode " + strconv.Quote(code) + ", which is not UPPER_SNAKE_CASE"
	}
	if code == "" {
		switch e {
		case Permit:
			code = ReasonPermit
		case Deny:
			code = ReasonPolicyDeny
		}
	}
	return e, code, ""
}

func hasPrefix(prefixes []string, id string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// check decides one obligation against the request.
func (ob *obligation) check(r *Request, p *Policy, ruleID string) ObligationResult {
	res := ObligationResult{ID: ob.id, Type: ob.kind, PolicyID: p.id, PolicyVersion: p.version, RuleID: ruleID}
	switch ob.kind {
	case ObligationApproval:
		res.ApproverGroup, res.Approvals = ob.approverGroup, ob.approvals
		res.ValidForSeconds, res.ExcludeRequester = ob.validFor/1e9, ob.excludeRequester
		res.Satisfied, res.Reason, res.ApprovalsUsed = ob.checkApproval(r)
	case ObligationFreshAuthentication:
		c := r.Actor.Connection
		res.Satisfied = c != nil && c.LastInteractiveAuth > 0 && c.LastInteractiveAuth <= r.Context.Time &&
			r.Context.Time-c.LastInteractiveAuth <= ob.maxAge
		if !res.Satisfied {
			res.Reason = ReasonReauthRequired
		}
	}
	return res
}

// checkApproval counts the distinct, eligible, unexpired approvals of this exact
// action. An approver is eligible when they are in the group now, are not the
// executing subject, are not the initiator when the obligation excludes them, and are
// a person: approvals are decided through a person's session, so a service principal
// that appears here is refused rather than counted.
func (ob *obligation) checkApproval(r *Request) (bool, string, []string) {
	now := r.Context.Time
	granted := map[string]string{} // approver → approval id
	expired, rejected := false, false
	for _, a := range r.Context.Approvals {
		if a.Revoked || a.Digest != r.Action.Digest || a.ApproverID == "" ||
			strings.HasPrefix(a.ApproverID, "system:") || a.ApproverID == r.Subject.ID ||
			(ob.excludeRequester && a.ApproverID == r.Context.InitiatedBy) ||
			!contains(a.ApproverGroups, ob.approverGroup) || a.DecidedAt > now {
			continue
		}
		switch a.Outcome {
		case ApprovalRejected:
			rejected = true
		case ApprovalGranted:
			if now-a.DecidedAt >= ob.validFor {
				expired = true
				continue
			}
			if _, dup := granted[a.ApproverID]; !dup {
				granted[a.ApproverID] = a.ID
			}
		}
	}
	if rejected {
		return false, ReasonApprovalRejected, nil
	}
	if len(granted) >= ob.approvals {
		used := make([]string, 0, len(granted))
		for _, id := range granted {
			used = append(used, id)
		}
		sort.Strings(used)
		return true, "", used
	}
	if expired {
		return false, ReasonApprovalExpired, nil
	}
	return false, ReasonApprovalRequired, nil
}

// evaluation carries one request's inputs, built at most once and only when a rule
// needs them.
type evaluation struct {
	ctx  context.Context
	r    *Request
	feel map[string]expr.Value
	flat map[string]any
}

func strs(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func (ev *evaluation) feelInputs() map[string]expr.Value {
	if ev.feel != nil {
		return ev.feel
	}
	r := ev.r
	connID := ""
	if r.Actor.Connection != nil {
		connID = r.Actor.Connection.ID
	}
	ev.feel = map[string]expr.Value{
		"subject": expr.FromJSON(map[string]any{
			"id": r.Subject.ID, "known": r.Subject.Known, "disabled": r.Subject.Disabled,
			"roles": strs(r.Subject.Roles), "groups": strs(r.Subject.Groups),
		}),
		"actor": expr.FromJSON(map[string]any{
			"identityMode": r.Actor.IdentityMode, "worker": r.Actor.Worker, "workerType": r.Actor.WorkerType,
			"workerInstance": r.Actor.WorkerInstance, "executedAs": r.Actor.ExecutedAs, "connectionId": connID,
		}),
		"action":   expr.FromJSON(map[string]any{"operation": r.Action.Operation, "digest": r.Action.Digest}),
		"resource": expr.FromJSON(map[string]any{"type": r.Resource.Type, "id": r.Resource.ID}),
		"context": expr.ContextOf(map[string]expr.Value{
			"time":               expr.DateTime(time.Unix(0, r.Context.Time).UTC()),
			"processId":          expr.String(r.Context.ProcessID),
			"processInstanceKey": expr.String(strconv.FormatUint(r.Context.ProcessInstanceKey, 10)),
			"jobKey":             expr.String(strconv.FormatUint(r.Context.JobKey, 10)),
			"initiatedBy":        expr.String(r.Context.InitiatedBy),
		}),
	}
	return ev.feel
}

// dmnInputs are flat, because a decision table's inputs are names, and a business
// analyst writing one reads "subjectRoles" more easily than a path.
func (ev *evaluation) dmnInputs() map[string]any {
	if ev.flat != nil {
		return ev.flat
	}
	r := ev.r
	ev.flat = map[string]any{
		"operation": r.Action.Operation, "workerType": r.Actor.WorkerType, "worker": r.Actor.Worker,
		"identityMode": r.Actor.IdentityMode, "subjectId": r.Subject.ID,
		"subjectRoles": strs(r.Subject.Roles), "subjectGroups": strs(r.Subject.Groups),
		"resourceType": r.Resource.Type, "resourceId": r.Resource.ID,
		"processId": r.Context.ProcessID, "initiatedBy": r.Context.InitiatedBy,
	}
	return ev.flat
}
