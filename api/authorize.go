package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/api/policy"
	"github.com/pblumer/atlas/authz"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// The policy enforcement point
// (ADR-0475 §5): a Worker Instance holding a lease
// on a governed job asks, before it acts, whether it may — and is answered with a
// decision that is already durable, the parameters it covers, and, for a delegated
// Worker, the short-lived credential to act with.
//
// Leasing a job and being authorized to act on it are two separate permissions. The
// first needs the worker scope and the job type; the second needs a decision for this
// job, this lease and these parameters.

// authorizeTimeout bounds a decision: a policy that cannot decide in this long is
// Indeterminate, which is a refusal.
const authorizeTimeout = 2 * time.Second

// maxEvidenceDetail bounds the JSON detail a decision event carries.
const maxEvidenceDetail = 4 << 10

// authzSnapshot is what the run loop knows about a leased job, read in one turn.
type authzSnapshot struct {
	jobKey, piKey, eiKey uint64
	epoch                uint64
	holder               string
	worker               string
	operation            string
	resource             string
	params               map[string]any
	fields               map[string]any
	processID            string
	startedBy            string
	approvals            []model.ActionApprovalValue
}

// authorizeAnswer is the response body.
type authorizeAnswer struct {
	Enforcement   string                   `json:"enforcement"`
	Effect        string                   `json:"effect,omitempty"`
	Permitted     bool                     `json:"permitted"`
	Enforced      bool                     `json:"enforced"`
	ReasonCode    string                   `json:"reasonCode,omitempty"`
	DecisionKey   uint64                   `json:"decisionKey,omitempty"`
	PolicyID      string                   `json:"policyId,omitempty"`
	PolicyVersion int                      `json:"policyVersion,omitempty"`
	ApprovalKey   uint64                   `json:"approvalKey,omitempty"`
	Obligations   []authz.ObligationResult `json:"obligations,omitempty"`
	// Job is the parameters the decision covers. A Worker acts on these, not on what
	// it was handed at lease time: a variable changed in between cannot widen what was
	// authorized.
	Job        map[string]any `json:"job,omitempty"`
	Credential *credentialOut `json:"credential,omitempty"`
}

type credentialOut struct {
	Type        string `json:"type"`
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"`
}

// handleAuthorizeJob is POST /api/v1/jobs/{key}/authorize.
func (s *Server) handleAuthorizeJob(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid job key")
		return
	}
	var body struct {
		Worker     string `json:"worker"`
		LeaseToken uint64 `json:"leaseToken"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	if err != nil || strings.TrimSpace(body.Worker) == "" || body.LeaseToken == 0 {
		httpapi.Error(w, http.StatusBadRequest, "worker and leaseToken are required: authorization is for the lease a worker holds")
		return
	}

	// Phase 1, on the loop: the lease, and everything about the job a decision needs,
	// read in one turn so it all describes the same moment.
	var (
		snap      authzSnapshot
		notFound  bool
		notHolder bool
		governed  bool
		g         governedWorker
	)
	s.do(func() {
		jv, ok, gerr := s.store.GetJob(key)
		if gerr != nil || !ok {
			notFound = true
			return
		}
		if !holdsLease(jv, strings.TrimSpace(body.Worker), body.LeaseToken) {
			notHolder = true
			return
		}
		snap, governed = s.snapshotJob(key, jv)
		if governed {
			g, governed = s.governedWorkerNamed(snap.worker)
		}
	})
	switch {
	case notFound:
		httpapi.Error(w, http.StatusNotFound, "no such job")
		return
	case notHolder:
		httpapi.Error(w, http.StatusConflict, "the job is not held under that lease")
		return
	case !governed:
		// A legacy Worker: nothing is decided and nothing recorded, exactly as before.
		httpapi.JSON(w, http.StatusOK, authorizeAnswer{Enforcement: "off", Permitted: true})
		return
	}
	delegated := authz.IsDelegated(g.identity.Mode)
	// A delegated credential leaves Atlas only for a Worker Instance Atlas itself
	// supervises (§11); an external one is refused before anything is decided, so no
	// Permit is recorded that nothing could act on.
	if delegated && !s.isBrokerWorker(r) {
		auditRefusal(r, logging.AuthorizationRefused, "refused to broker a delegated credential to a Worker Instance Atlas does not supervise",
			slog.Uint64("job", key), slog.String("worker", snap.worker))
		httpapi.Error(w, http.StatusForbidden, "a delegated Worker's credential is released only to the Worker Instance this server supervises for it")
		return
	}
	// The endpoint is where the token will go. The routes that set it refuse anything
	// but Microsoft Graph for a delegated Worker; this is the same rule where it counts.
	if delegated && !delegatedEndpointAllowed(g.endpoint) {
		auditRefusal(r, logging.AuthorizationRefused, "refused to broker a delegated credential to an endpoint that is not Microsoft Graph",
			slog.Uint64("job", key), slog.String("worker", snap.worker))
		httpapi.Error(w, http.StatusConflict, "the Worker's endpoint is not Microsoft Graph; a person's token is never sent anywhere else")
		return
	}

	// Phase 2, off the loop: trusted attributes, then the decision.
	ctx, cancel := context.WithTimeout(r.Context(), authorizeTimeout)
	defer cancel()
	now := s.now()
	req, conn, pre := s.authorizationRequest(snap, g, now)
	var d authz.Decision
	if pre != nil {
		d = *pre
	} else {
		d = s.policySet().Evaluate(ctx, req)
	}
	enforced := g.identity.Enforcement == enforcementEnforce
	evidence := evidenceOf(snap, g, req, d, enforced, now)
	var want *model.ActionApprovalValue
	message := ""
	if enforced && !d.Permitted() {
		message = fmt.Sprintf("authorization denied: %s", d.ReasonCode)
		if d.PolicyID != "" {
			message += fmt.Sprintf(" by policy %s v%d", d.PolicyID, d.PolicyVersion)
		}
		want = approvalWanted(d, req, now)
	}

	// Phase 3, on the loop: the decision becomes a fact for this lease, and its
	// consequence with it, before anything is released (I2).
	var (
		res    engine.AuthorizationResult
		runErr error
	)
	s.do(func() {
		s.proc.RecordAuthorization(evidence, want, message, &res)
		runErr = s.proc.RunUntilIdle()
	})
	if runErr == nil && enforced && !d.Permitted() {
		// The refusal failed the job or threw: whatever that unblocked runs now, off
		// the loop, as a worker's own failure report would.
		runErr = s.drive()
	}
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "record the decision: "+runErr.Error())
		return
	case res.Answer != engine.AuthorizationRecorded:
		httpapi.Error(w, http.StatusConflict, "the lease ended while the decision was made; nothing was authorized")
		return
	}
	answer := authorizeAnswer{
		Enforcement: g.identity.Enforcement, Effect: d.Effect.String(), Permitted: d.Permitted(), Enforced: enforced,
		ReasonCode: d.ReasonCode, DecisionKey: res.DecisionKey, PolicyID: d.PolicyID, PolicyVersion: d.PolicyVersion,
		ApprovalKey: res.ApprovalKey, Obligations: d.Obligations,
	}
	if !d.Permitted() {
		if enforced {
			auditRefusal(r, logging.AuthorizationRefused, "refused an external action",
				slog.Uint64("job", key), slog.String("worker", snap.worker), slog.String("reason", d.ReasonCode),
				slog.Uint64("decision", res.DecisionKey))
		}
		// Shadow: the Worker goes ahead, and the record says what would have been said.
		if !enforced {
			answer.Job = snap.fields
		}
		httpapi.JSON(w, http.StatusOK, answer)
		return
	}
	answer.Job = snap.fields

	// Phase 4: a permitted delegated action gets its credential — after the decision is
	// durable, never before.
	if delegated && conn != nil {
		tok, exp, err := s.identity.AccessToken(ctx, conn.ID)
		if err != nil {
			logging.Warn(logging.ConnectionRefreshFailed, "could not obtain an access token for a permitted action",
				slog.String("connection", conn.ID), slog.Uint64("decision", res.DecisionKey), slog.String("reason", err.Error()))
			httpapi.Error(w, http.StatusBadGateway, "the action is permitted (decision "+strconv.FormatUint(res.DecisionKey, 10)+
				") but no credential could be obtained for its connection: "+err.Error())
			return
		}
		answer.Credential = &credentialOut{Type: "Bearer", AccessToken: tok, ExpiresAt: exp.UnixNano()}
		// A token is in this body: nothing between here and the Worker may keep it.
		w.Header().Set("Cache-Control", "no-store")
	}
	httpapi.JSON(w, http.StatusOK, answer)
}

// brokerKeyEnv and brokerKeyHeader carry the broker key from the supervisor to the
// Worker Instance and back.
const (
	brokerKeyEnv    = "ATLAS_BROKER_KEY"
	brokerKeyHeader = "X-Atlas-Broker-Key"
)

// isSupervisedWorker reports whether the request carries the internal credential this
// server hands only to the Worker Instances it starts.
func isSupervisedWorker(r *http.Request) bool {
	p := httpapi.PrincipalFrom(r.Context())
	return p != nil && p.UserID == servicePrincipalName
}

// isBrokerWorker reports whether the request comes from the supervised Worker Instance
// this server handed the broker key: the internal credential, which every supervised
// child holds, and the key, which only the child serving a delegated Worker does.
func (s *Server) isBrokerWorker(r *http.Request) bool {
	got := r.Header.Get(brokerKeyHeader)
	return isSupervisedWorker(r) && s.brokerKey != "" && got != "" &&
		subtle.ConstantTimeCompare([]byte(got), []byte(s.brokerKey)) == 1
}

func (s *Server) policySet() *authz.Set {
	if s.policies == nil {
		return nil
	}
	return s.policies.Set()
}

// snapshotJob reads what a decision needs about a leased job, and reports whether
// the job is of a kind this build can govern. Runs on the loop.
func (s *Server) snapshotJob(key uint64, jv *model.JobValue) (authzSnapshot, bool) {
	snap := authzSnapshot{jobKey: key, piKey: jv.ProcessInstanceKey, eiKey: jv.ElementInstanceKey,
		epoch: jv.LeaseEpoch, holder: jv.Assignee}
	if jv.JobType != compiler.SharePointJobTypeIndex && jv.JobType != compiler.SharePointOpsJobTypeIndex {
		return snap, false
	}
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return snap, false
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok {
		return snap, false
	}
	payload := s.resolveConnectorTask(key, jv, ei, d.cp)
	if payload == nil || payload.Kind != "sharepoint" {
		return snap, false
	}
	snap.processID = d.ProcessID
	snap.fields = payload.Fields
	snap.worker, _ = payload.Fields["connector"].(string)
	snap.operation, _ = payload.Fields["operation"].(string)
	if snap.operation == "" {
		snap.operation = "create-item"
	}
	snap.resource, snap.params = sharePointAction(payload.Fields)
	if pi, ok, err := s.store.ProcessInstance(jv.ProcessInstanceKey); err == nil && ok {
		snap.startedBy = pi.StartedBy
	}
	_ = s.store.ApprovalFacts(jv.ProcessInstanceKey, func(v *model.ActionApprovalValue) error {
		snap.approvals = append(snap.approvals, *v)
		return nil
	})
	return snap, true
}

// resourceEscaper percent-encodes the canonical resource's separators inside a value.
var resourceEscaper = strings.NewReplacer("%", "%25", ";", "%3B", "=", "%3D")

// sharePointAction is a SharePoint job's canonical resource and material parameters:
// everything that decides what the call does, and nothing that only shapes how it is
// reported. An upload's content enters as its hash, so a digest stays small and the
// content stays out of the evidence.
func sharePointAction(fields map[string]any) (string, map[string]any) {
	// Each value is escaped, so a value carrying ';' or '=' cannot forge a segment a
	// policy's resource prefix would then match.
	str := func(k string) string { v, _ := fields[k].(string); return resourceEscaper.Replace(v) }
	res := "sharepoint:site=" + str("site")
	if l := str("list"); l != "" {
		res += ";list=" + l
	}
	if id := str("itemId"); id != "" {
		res += ";item=" + id
	}
	if p := str("path"); p != "" {
		res += ";path=" + p
	}
	params := map[string]any{}
	for k, v := range fields {
		switch k {
		case "connector", "requestId", "resultVariable":
			continue
		case "content":
			if c, _ := v.(string); c != "" {
				sum := sha256.Sum256([]byte(c))
				params["contentSha256"] = hex.EncodeToString(sum[:])
			}
			continue
		}
		params[k] = v
	}
	return res, params
}

// authorizationRequest assembles the request from server-side facts: the Worker's
// identity policy, the user and group stores, the connection store and the approval
// facts in the log. A failure to establish a fact is a decision of its own, returned
// as pre, so the refusal is recorded like any other.
func (s *Server) authorizationRequest(snap authzSnapshot, g governedWorker, now int64) (*authz.Request, *identity.Connection, *authz.Decision) {
	req := &authz.Request{
		Actor: authz.Actor{IdentityMode: g.identity.Mode, Worker: snap.worker, WorkerType: g.kind,
			WorkerInstance: snap.holder, ExecutedAs: "service:" + snap.worker},
		Action:   authz.Action{Operation: snap.operation},
		Resource: authz.Resource{Type: g.kind, ID: snap.resource},
		Context: authz.Context{Time: now, ProcessID: snap.processID, ProcessInstanceKey: snap.piKey,
			JobKey: snap.jobKey, InitiatedBy: snap.startedBy},
	}
	digest, err := authz.Digest(g.kind, snap.worker, snap.operation, snap.resource, snap.params)
	if err != nil {
		return req, nil, &authz.Decision{Effect: authz.Indeterminate, ReasonCode: "MISSING_ATTRIBUTE"}
	}
	req.Action.Digest = digest

	var conn *identity.Connection
	subject := snap.startedBy
	switch g.identity.Mode {
	case authz.ModeDelegatedFixed:
		c, ok, err := s.identity.Get(g.identity.ConnectionID)
		if err != nil {
			return req, nil, &authz.Decision{Effect: authz.Indeterminate, ReasonCode: "PIP_UNAVAILABLE"}
		}
		if ok {
			conn = &c
			subject = c.OwnerID
		} else {
			subject = ""
		}
	case authz.ModeDelegatedPerJob:
		if subject != "" {
			c, ok, err := s.identity.ConnectionFor(subject, identity.ProviderMicrosoft, snap.worker)
			switch {
			case errors.Is(err, identity.ErrAmbiguous):
				return req, nil, &authz.Decision{Effect: authz.Deny, ReasonCode: "CONNECTION_AMBIGUOUS"}
			case err != nil:
				return req, nil, &authz.Decision{Effect: authz.Indeterminate, ReasonCode: "PIP_UNAVAILABLE"}
			case ok:
				conn = &c
			}
		}
	}
	if conn != nil {
		req.Actor.ExecutedAs = conn.IdentityKey
		req.Actor.Connection = &authz.Connection{ID: conn.ID, OwnerID: conn.OwnerID, Status: conn.Status,
			AllowedWorkers: conn.AllowedWorkers, LastInteractiveAuth: conn.LastInteractiveAuthAt}
	}
	if subject != "" {
		subj, err := s.subjectOf(subject)
		if err != nil {
			return req, conn, &authz.Decision{Effect: authz.Indeterminate, ReasonCode: "PIP_UNAVAILABLE"}
		}
		req.Subject = subj
	}
	req.Context.Approvals = s.approvalFacts(snap.approvals, digest)
	return req, conn, nil
}

// subjectOf reads a principal's current state: whether the account exists and is
// enabled, its roles and its groups. Current, not the session's snapshot — a decision
// about an action now is made on who the person is now.
func (s *Server) subjectOf(id string) (authz.Subject, error) {
	if s.users == nil {
		return authz.Subject{ID: id}, nil
	}
	u, ok, err := s.users.Get(id)
	if err != nil {
		return authz.Subject{}, err
	}
	if !ok {
		return authz.Subject{ID: id}, nil
	}
	var groups []string
	if s.groups != nil {
		if groups, err = s.groups.idsForUser(id); err != nil {
			return authz.Subject{}, err
		}
	}
	return authz.Subject{ID: id, Known: true, Disabled: u.Disabled, Roles: u.Roles, Groups: groups}, nil
}

// approvalFacts folds the log's approval facts into the answers that stand for the
// action whose digest is digest: each approver's latest answer to a request for that
// action, withdrawn ones gone, with the approver's groups as they are now. An answer
// to a request for another action is not read at all.
//
// Nothing is dropped to fit: more standing answers than a decision may read are passed
// on whole, and the decision point refuses them (authz.MaxApprovals), because the one
// left out could be the rejection.
func (s *Server) approvalFacts(facts []model.ActionApprovalValue, digest string) []authz.Approval {
	type answerKey struct {
		approval  uint64
		principal string
	}
	// Requests first: an answer is read only when it answers a request for this action.
	forThis := map[uint64]bool{}
	for _, f := range facts {
		if f.Kind == model.ApprovalRequested && f.ActionDigest == digest {
			forThis[f.ApprovalKey] = true
		}
	}
	standing := map[answerKey]model.ActionApprovalValue{}
	listed := map[answerKey]bool{}
	var order []answerKey
	for _, f := range facts {
		k := answerKey{f.ApprovalKey, f.Principal}
		switch {
		case !forThis[f.ApprovalKey]:
		case f.Kind == model.ApprovalDecided:
			// An approver's place in the order is their first answer; answering again
			// after a withdrawal takes the same place, not another one.
			if !listed[k] {
				listed[k] = true
				order = append(order, k)
			}
			standing[k] = f
		case f.Kind == model.ApprovalRevoked:
			delete(standing, k)
		}
	}
	groupsOf := map[string][]string{}
	var out []authz.Approval
	for _, k := range order {
		f, ok := standing[k]
		if !ok {
			continue
		}
		groups, seen := groupsOf[f.Principal]
		if !seen && s.groups != nil {
			groups, _ = s.groups.idsForUser(f.Principal)
			groupsOf[f.Principal] = groups
		}
		out = append(out, authz.Approval{
			ID:     strconv.FormatUint(k.approval, 10) + ":" + k.principal,
			Digest: digest, ApproverID: f.Principal, ApproverGroups: groups,
			Outcome: f.Outcome.String(), DecidedAt: f.At,
		})
	}
	return out
}

// approvalWanted is the approval an enforced refusal asks for, when an unmet approval
// obligation is what refused it.
func approvalWanted(d authz.Decision, req *authz.Request, now int64) *model.ActionApprovalValue {
	if d.ReasonCode != authz.ReasonApprovalRequired && d.ReasonCode != authz.ReasonApprovalExpired {
		return nil
	}
	for _, ob := range d.Obligations {
		if ob.Type != authz.ObligationApproval || ob.Satisfied {
			continue
		}
		return &model.ActionApprovalValue{
			RequestedBy: req.Subject.ID, ApproverGroup: ob.ApproverGroup, Approvals: uint8(ob.Approvals),
			ObligationID: ob.ID, ExpiresAt: now + ob.ValidForSeconds*int64(time.Second),
		}
	}
	return nil
}

// evidenceOf is the decision as the log records it.
func evidenceOf(snap authzSnapshot, g governedWorker, req *authz.Request, d authz.Decision, enforced bool, now int64) model.AuthorizationValue {
	connID := ""
	if req.Actor.Connection != nil {
		connID = req.Actor.Connection.ID
	}
	detail := map[string]any{"trace": policy.TraceView(d.Trace)}
	if len(d.Obligations) > 0 {
		detail["obligations"] = d.Obligations
	}
	b, _ := json.Marshal(detail)
	if len(b) > maxEvidenceDetail {
		// The trace is the bulky part, and its first entries are the ones that decided.
		b, _ = json.Marshal(map[string]any{"truncated": true, "obligations": d.Obligations})
	}
	return model.AuthorizationValue{
		ProcessInstanceKey: snap.piKey, ElementInstanceKey: snap.eiKey, JobKey: snap.jobKey, LeaseEpoch: snap.epoch,
		DecidedAt: now, Effect: model.AuthzEffect(d.Effect), Enforced: enforced, PolicyVersion: uint32(d.PolicyVersion),
		ReasonCode: d.ReasonCode, PolicyID: d.PolicyID, Worker: snap.worker, WorkerInstance: snap.holder,
		WorkerType: g.kind, Operation: snap.operation, Resource: snap.resource, ActionDigest: req.Action.Digest,
		InitiatedBy: snap.startedBy, ExecutedFor: req.Subject.ID, ExecutedAs: req.Actor.ExecutedAs,
		ConnectionID: connID, IdentityMode: g.identity.Mode, Detail: string(b),
	}
}

// refusesUnauthorizedCompletion reports whether a worker's completion of jv must be
// refused: the job belongs to an enforced Worker and no Permit was recorded for the
// lease it is held under. Runs on the loop. It is what makes a cooperative Worker's
// enforcement real for the process: a Worker that acted without asking cannot move
// the instance on.
func (s *Server) refusesUnauthorizedCompletion(key uint64, jv *model.JobValue) bool {
	if jv.JobType != compiler.SharePointJobTypeIndex && jv.JobType != compiler.SharePointOpsJobTypeIndex {
		return false
	}
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return false
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok {
		return false
	}
	node := d.cp.Node(ei.ElementId)
	detail := d.cp.ConnectorTask(node.Detail)
	if detail == nil {
		return false
	}
	g, ok := s.governedWorkerNamed(d.cp.Intern(detail.Connector))
	if !ok || g.identity.Enforcement != enforcementEnforce {
		return false
	}
	permitted := false
	_ = s.store.AuthorizationsOfJob(jv.ProcessInstanceKey, key, func(_ uint64, v *model.AuthorizationValue) error {
		if v.LeaseEpoch == jv.LeaseEpoch && v.Enforced && v.Effect == model.AuthzPermit {
			permitted = true
		}
		return nil
	})
	return !permitted
}
