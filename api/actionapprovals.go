package api

import (
	"encoding/json"
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
// (ADR-draft-identity-aware-worker-execution §9, §12).

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
	ApprovalKey   uint64       `json:"approvalKey"`
	JobKey        uint64       `json:"jobKey"`
	DecisionKey   uint64       `json:"decisionKey"`
	RequestedAt   int64        `json:"requestedAt"`
	ExpiresAt     int64        `json:"expiresAt"`
	RequestedBy   string       `json:"requestedBy"`
	ApproverGroup string       `json:"approverGroup"`
	Approvals     uint8        `json:"approvals"`
	PolicyID      string       `json:"policyId"`
	PolicyVersion uint32       `json:"policyVersion"`
	ObligationID  string       `json:"obligationId"`
	Worker        string       `json:"worker"`
	Operation     string       `json:"operation"`
	Resource      string       `json:"resource"`
	ActionDigest  string       `json:"actionDigest"`
	Answers       []answerView `json:"answers"`
	Open          bool         `json:"open"`
	request       model.ActionApprovalValue
}

type answerView struct {
	Kind      string `json:"kind"`
	Principal string `json:"principal"`
	Outcome   string `json:"outcome,omitempty"`
	At        int64  `json:"at"`
	Reason    string `json:"reason,omitempty"`
}

// approvalsOf folds an instance's approval facts into requests with their answers.
func approvalsOf(facts []model.ActionApprovalValue, now int64) []approvalView {
	byKey := map[uint64]*approvalView{}
	var order []uint64
	for _, f := range facts {
		if f.Kind == model.ApprovalRequested {
			byKey[f.ApprovalKey] = &approvalView{
				ApprovalKey: f.ApprovalKey, JobKey: f.JobKey, DecisionKey: f.DecisionKey, RequestedAt: f.At,
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
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !strings.HasPrefix(p.UserID, "usr_") {
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
