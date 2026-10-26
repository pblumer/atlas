package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// Authorization evidence and action approvals as the API shows them
// (ADR-0475 §9, §12).

// authorizationView is one recorded decision.
type authorizationView struct {
	DecisionKey        uint64          `json:"decisionKey"`
	JobKey             uint64          `json:"jobKey"`
	ElementInstanceKey uint64          `json:"elementInstanceKey"`
	LeaseEpoch         uint64          `json:"leaseEpoch"`
	DecidedAt          int64           `json:"decidedAt"`
	Effect             string          `json:"effect"`
	Enforced           bool            `json:"enforced"`
	ReasonCode         string          `json:"reasonCode"`
	PolicyID           string          `json:"policyId,omitempty"`
	PolicyVersion      uint32          `json:"policyVersion,omitempty"`
	Worker             string          `json:"worker"`
	WorkerInstance     string          `json:"workerInstance"`
	WorkerType         string          `json:"workerType"`
	Operation          string          `json:"operation"`
	Resource           string          `json:"resource"`
	ActionDigest       string          `json:"actionDigest"`
	InitiatedBy        string          `json:"initiatedBy,omitempty"`
	ExecutedFor        string          `json:"executedFor,omitempty"`
	ExecutedAs         string          `json:"executedAs,omitempty"`
	ConnectionID       string          `json:"connectionId,omitempty"`
	IdentityMode       string          `json:"identityMode"`
	Detail             json.RawMessage `json:"detail,omitempty"`
}

func viewAuthorization(v *model.AuthorizationValue) authorizationView {
	out := authorizationView{
		DecisionKey: v.DecisionKey, JobKey: v.JobKey, ElementInstanceKey: v.ElementInstanceKey, LeaseEpoch: v.LeaseEpoch,
		DecidedAt: v.DecidedAt, Effect: v.Effect.String(), Enforced: v.Enforced, ReasonCode: v.ReasonCode,
		PolicyID: v.PolicyID, PolicyVersion: v.PolicyVersion, Worker: v.Worker, WorkerInstance: v.WorkerInstance,
		WorkerType: v.WorkerType, Operation: v.Operation, Resource: v.Resource, ActionDigest: v.ActionDigest,
		InitiatedBy: v.InitiatedBy, ExecutedFor: v.ExecutedFor, ExecutedAs: v.ExecutedAs,
		ConnectionID: v.ConnectionID, IdentityMode: v.IdentityMode,
	}
	if json.Valid([]byte(v.Detail)) {
		out.Detail = json.RawMessage(v.Detail)
	}
	return out
}

// handleInstanceAuthorizations lists an instance's recorded decisions: who asked, as
// whom it ran, which policy and version decided, and what it said.
func (s *Server) handleInstanceAuthorizations(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	out := []authorizationView{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.AuthorizationHistory(key, func(_ uint64, v *model.AuthorizationValue) error {
			out = append(out, viewAuthorization(v))
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// approvalView is one approval request with every answer it has had.
type approvalView struct {
	ProcessInstanceKey uint64       `json:"processInstanceKey"`
	ApprovalKey        uint64       `json:"approvalKey"`
	JobKey             uint64       `json:"jobKey"`
	DecisionKey        uint64       `json:"decisionKey"`
	RequestedAt        int64        `json:"requestedAt"`
	ExpiresAt          int64        `json:"expiresAt"`
	RequestedBy        string       `json:"requestedBy"`
	ApproverGroup      string       `json:"approverGroup"`
	Approvals          uint8        `json:"approvals"`
	PolicyID           string       `json:"policyId"`
	PolicyVersion      uint32       `json:"policyVersion"`
	ObligationID       string       `json:"obligationId"`
	Worker             string       `json:"worker"`
	Operation          string       `json:"operation"`
	Resource           string       `json:"resource"`
	ActionDigest       string       `json:"actionDigest"`
	Answers            []answerView `json:"answers"`
	Open               bool         `json:"open"`
	request            model.ActionApprovalValue
}

type answerView struct {
	Kind      string `json:"kind"`
	Principal string `json:"principal"`
	// Name is the principal as a person reads it; only the caller's own worklist
	// resolves it.
	Name    string `json:"name,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	At      int64  `json:"at"`
	Reason  string `json:"reason,omitempty"`
}

// approvalsOf folds an instance's approval facts into requests with their answers.
func approvalsOf(facts []model.ActionApprovalValue, now int64) []approvalView {
	byKey := map[uint64]*approvalView{}
	var order []uint64
	for _, f := range facts {
		if f.Kind == model.ApprovalRequested {
			byKey[f.ApprovalKey] = &approvalView{
				ProcessInstanceKey: f.ProcessInstanceKey, ApprovalKey: f.ApprovalKey, JobKey: f.JobKey, DecisionKey: f.DecisionKey, RequestedAt: f.At,
				ExpiresAt: f.ExpiresAt, RequestedBy: f.RequestedBy, ApproverGroup: f.ApproverGroup,
				Approvals: f.Approvals, PolicyID: f.PolicyID, PolicyVersion: f.PolicyVersion, ObligationID: f.ObligationID,
				Worker: f.Worker, Operation: f.Operation, Resource: f.Resource, ActionDigest: f.ActionDigest,
				Answers: []answerView{}, Open: f.ExpiresAt > now,
			}
			order = append(order, f.ApprovalKey)
		}
	}
	for _, f := range facts {
		a, ok := byKey[f.ApprovalKey]
		if !ok || f.Kind == model.ApprovalRequested {
			continue
		}
		a.Answers = append(a.Answers, answerView{Kind: f.Kind.String(), Principal: f.Principal,
			Outcome: f.Outcome.String(), At: f.At, Reason: f.Reason})
	}
	out := make([]approvalView, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// handleInstanceActionApprovals lists the approvals an instance's actions asked for.
// An operator sees them all; anybody else sees the ones they could answer or that
// were asked on their behalf.
func (s *Server) handleInstanceActionApprovals(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	var facts []model.ActionApprovalValue
	s.do(func() {
		_ = s.store.ApprovalFacts(key, func(v *model.ActionApprovalValue) error {
			facts = append(facts, *v)
			return nil
		})
	})
	all := approvalsOf(facts, s.now())
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || mayReach(p, RoleOperator) {
		httpapi.JSON(w, http.StatusOK, all)
		return
	}
	groups, _ := s.groups.idsForUser(p.UserID)
	out := []approvalView{}
	for _, a := range all {
		if a.RequestedBy == p.UserID || containsString(groups, a.ApproverGroup) {
			out = append(out, a)
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// myApprovalView is an open approval as the person reading their worklist sees it:
// what they may do about it, what they already said, and where it stands.
type myApprovalView struct {
	approvalView
	ProcessID         string `json:"processId,omitempty"`
	RequestedByName   string `json:"requestedByName"`
	ApproverGroupName string `json:"approverGroupName"`
	// Role is "approver" when the caller is in the approver group and may answer, and
	// "requester" when the action would run for the caller, who may never answer it.
	Role string `json:"role"`
	// MyAnswer is the caller's standing answer; a withdrawn one is no answer.
	MyAnswer *answerView `json:"myAnswer,omitempty"`
	// Granted counts the people whose grant stands, Rejected says whether a rejection
	// does. They describe the answers given, not the decision: the policy decides when
	// the action is next authorized, on the approvers' group membership at that time.
	Granted  int  `json:"granted"`
	Rejected bool `json:"rejected"`
}

// maxMyApprovals bounds one worklist. The soonest-expiring requests come first, so
// a person who has more open than this sees the most urgent, and the page says it
// is truncated. A variable so a test can reach the bound.
var maxMyApprovals = 200

// errStopScan ends the scan of open requests once a worklist is full.
var errStopScan = errors.New("worklist full")

// standingAnswers folds an approval's answers into each principal's standing one: a
// withdrawal cancels the answer before it.
func standingAnswers(answers []answerView) map[string]answerView {
	out := map[string]answerView{}
	for _, a := range answers {
		if a.Kind == model.ApprovalDecided.String() {
			out[a.Principal] = a
		} else {
			delete(out, a.Principal)
		}
	}
	return out
}

// handleMyActionApprovals lists the open approvals the caller may answer, and the
// ones asked on the caller's behalf, across every instance. Open requests are read
// through their expiry index, so the work is bounded by what is open now, not by
// every approval ever recorded. Group membership is read now, as the decide route
// reads it. With sign-in off there is no person, and so nothing anybody may answer:
// the page is empty rather than refused.
func (s *Server) handleMyActionApprovals(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.JSON(w, http.StatusOK, httpapi.PageOf([]myApprovalView{}, false))
		return
	}
	if !strings.HasPrefix(p.UserID, "usr_") {
		httpapi.Error(w, http.StatusForbidden, "approvals are answered by people; this principal has no worklist")
		return
	}
	groups, err := s.groups.idsForUser(p.UserID)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []myApprovalView{}
	truncated := false
	var scanErr error
	s.do(func() {
		now := s.now()
		seen := map[uint64][]approvalView{}
		scanErr = s.store.OpenApprovalRequests(now, func(piKey, approvalKey uint64) error {
			all, read := seen[piKey]
			if !read {
				var facts []model.ActionApprovalValue
				if err := s.store.ApprovalFacts(piKey, func(v *model.ActionApprovalValue) error {
					facts = append(facts, *v)
					return nil
				}); err != nil {
					return err
				}
				all = approvalsOf(facts, now)
				seen[piKey] = all
			}
			for _, a := range all {
				if a.ApprovalKey != approvalKey || !a.Open {
					continue
				}
				role := ""
				switch {
				case a.RequestedBy == p.UserID:
					role = "requester"
				case containsString(groups, a.ApproverGroup):
					role = "approver"
				default:
					continue
				}
				if len(out) == maxMyApprovals {
					truncated = true
					return errStopScan
				}
				out = append(out, s.myApproval(a, role, p.UserID))
			}
			return nil
		})
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		httpapi.Error(w, http.StatusInternalServerError, scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, httpapi.PageOf(out, truncated))
}

// myApproval is an open approval as the caller reads it: names resolved, their own answer picked out, the
// standing answers counted. Run on the loop.
func (s *Server) myApproval(a approvalView, role, caller string) myApprovalView {
	v := myApprovalView{approvalView: a, Role: role,
		RequestedByName: s.principalName(a.RequestedBy), ApproverGroupName: s.groupNames(a.ApproverGroup)}
	if pi, ok, err := s.store.ProcessInstance(a.ProcessInstanceKey); err == nil && ok {
		if d, ok := s.deployments[pi.ProcessDefKey]; ok {
			v.ProcessID = d.ProcessID
		}
	}
	v.Answers = make([]answerView, len(a.Answers))
	for i, ans := range a.Answers {
		ans.Name = s.principalName(ans.Principal)
		v.Answers[i] = ans
	}
	for principal, ans := range standingAnswers(v.Answers) {
		switch ans.Outcome {
		case model.ApprovalOutcomeGranted.String():
			v.Granted++
		case model.ApprovalOutcomeRejected.String():
			v.Rejected = true
		}
		if principal == caller {
			mine := ans
			v.MyAnswer = &mine
		}
	}
	return v
}

// handleDecideActionApproval records a person's answer to an approval, or the
// withdrawal of their earlier answer. Who may answer is checked here, on stable ids:
// a person — never a machine credential — who is in the approver group now and is
// not the one the action would run for. The policy checks it all again, and more,
// when the action is next authorized.
func (s *Server) handleDecideActionApproval(w http.ResponseWriter, r *http.Request) {
	piKey, err1 := strconv.ParseUint(r.PathValue("key"), 10, 64)
	apKey, err2 := strconv.ParseUint(r.PathValue("approval"), 10, 64)
	if err1 != nil || err2 != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance or approval key")
		return
	}
	var body struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !strings.HasPrefix(p.UserID, "usr_") || !p.Session {
		httpapi.Error(w, http.StatusForbidden, "an approval is a person's answer, given in their own session")
		return
	}
	v := model.ActionApprovalValue{ProcessInstanceKey: piKey, ApprovalKey: apKey, Principal: p.UserID,
		At: s.now(), Reason: strings.TrimSpace(body.Reason)}
	switch body.Outcome {
	case "granted":
		v.Kind, v.Outcome = model.ApprovalDecided, model.ApprovalOutcomeGranted
	case "rejected":
		v.Kind, v.Outcome = model.ApprovalDecided, model.ApprovalOutcomeRejected
	case "withdrawn":
		v.Kind = model.ApprovalRevoked
	default:
		httpapi.Error(w, http.StatusBadRequest, `outcome must be "granted", "rejected" or "withdrawn"`)
		return
	}
	// The request decides who may answer it, so it is read first.
	var request *model.ActionApprovalValue
	s.do(func() {
		_ = s.store.ApprovalFacts(piKey, func(f *model.ActionApprovalValue) error {
			if f.ApprovalKey == apKey && f.Kind == model.ApprovalRequested {
				cp := *f
				request = &cp
			}
			return nil
		})
	})
	if request == nil {
		httpapi.Error(w, http.StatusNotFound, "no such approval in that instance")
		return
	}
	if v.Kind == model.ApprovalDecided {
		if request.RequestedBy == p.UserID {
			auditRefusal(r, logging.ApprovalAnswered, "refused a self-approval", slog.Uint64("approval", apKey))
			httpapi.Error(w, http.StatusForbidden, "nobody approves an action that would run for themselves")
			return
		}
		groups, gerr := s.groups.idsForUser(p.UserID)
		if gerr != nil || !containsString(groups, request.ApproverGroup) {
			auditRefusal(r, logging.ApprovalAnswered, "refused an answer from outside the approver group", slog.Uint64("approval", apKey))
			httpapi.Error(w, http.StatusForbidden, "only a member of the approver group may answer this approval")
			return
		}
	}
	var (
		res    engine.ApprovalResult
		runErr error
	)
	s.do(func() {
		s.proc.RecordApprovalAnswer(v, &res)
		runErr = s.proc.RunUntilIdle()
	})
	switch {
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, runErr.Error())
	case res.Answer == engine.ApprovalUnknown:
		httpapi.Error(w, http.StatusNotFound, "no such approval in that instance")
	case res.Answer == engine.ApprovalClosed:
		httpapi.Error(w, http.StatusConflict, "the approval expired before this answer")
	case res.Answer == engine.ApprovalAlreadyAnswered:
		httpapi.Error(w, http.StatusConflict, "you have already answered; withdraw that answer first")
	case res.Answer == engine.ApprovalNothingToWithdraw:
		httpapi.Error(w, http.StatusConflict, "you have no answer to withdraw")
	case res.Answer != engine.ApprovalRecordedAnswer:
		httpapi.Error(w, http.StatusBadRequest, "the answer was not recorded")
	default:
		audit(r, logging.ApprovalAnswered, "answered an action approval",
			slog.Uint64("approval", apKey), slog.String("outcome", body.Outcome))
		httpapi.JSON(w, http.StatusOK, map[string]any{"approvalKey": apKey, "outcome": body.Outcome})
	}
}
