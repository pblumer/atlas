package api

import (
	"testing"

	"github.com/pblumer/atlas/authz"
	"github.com/pblumer/atlas/model"
)

// The answers that stand for an action are each approver's latest answer to a request
// for that action's digest. These tests hold the fold to that, and to failing closed
// when there are more than a decision may read.

func approvalFact(kind model.ApprovalKind, approval uint64, principal string, outcome model.ApprovalOutcome, digest string) model.ActionApprovalValue {
	return model.ActionApprovalValue{Kind: kind, ApprovalKey: approval, Principal: principal, Outcome: outcome,
		ActionDigest: digest, At: int64(approval)}
}

// One approver granting and withdrawing again and again must not crowd another
// approver's rejection out of what the decision reads.
func TestAnApproverCannotCrowdOutAnotherApproversRejection(t *testing.T) {
	srv := newServerWithOptions(t)
	facts := []model.ActionApprovalValue{approvalFact(model.ApprovalRequested, 1, "usr_alice", 0, "d1")}
	facts = append(facts, approvalFact(model.ApprovalDecided, 1, "usr_carol", model.ApprovalOutcomeRejected, ""))
	for i := 0; i < 2*authz.MaxApprovals; i++ {
		facts = append(facts,
			approvalFact(model.ApprovalDecided, 1, "usr_bob", model.ApprovalOutcomeGranted, ""),
			approvalFact(model.ApprovalRevoked, 1, "usr_bob", 0, ""))
	}
	facts = append(facts, approvalFact(model.ApprovalDecided, 1, "usr_bob", model.ApprovalOutcomeGranted, ""))
	got := srv.approvalFacts(facts, "d1")
	if len(got) != 2 {
		t.Fatalf("%d answers stand, want Bob's and Carol's: %+v", len(got), got)
	}
	seen := map[string]string{}
	for _, a := range got {
		seen[a.ApproverID] = a.Outcome
	}
	if seen["usr_carol"] != authz.ApprovalRejected || seen["usr_bob"] != authz.ApprovalGranted {
		t.Fatalf("answers: %v", seen)
	}
}

// An answer to a request for another action is not an answer to this one, and does
// not count toward what this decision reads.
func TestOnlyAnswersForThisActionAreRead(t *testing.T) {
	srv := newServerWithOptions(t)
	facts := []model.ActionApprovalValue{
		approvalFact(model.ApprovalRequested, 1, "usr_alice", 0, "other"),
		approvalFact(model.ApprovalRequested, 2, "usr_alice", 0, "this"),
		approvalFact(model.ApprovalDecided, 1, "usr_bob", model.ApprovalOutcomeGranted, ""),
		approvalFact(model.ApprovalDecided, 2, "usr_carol", model.ApprovalOutcomeGranted, ""),
	}
	got := srv.approvalFacts(facts, "this")
	if len(got) != 1 || got[0].ApproverID != "usr_carol" || got[0].Digest != "this" {
		t.Fatalf("read %+v", got)
	}
}

// More standing answers than a decision may read are all passed on, and the decision
// point refuses them (INPUT_TOO_LARGE) — none is silently dropped, because the one
// dropped could be the rejection.
func TestMoreAnswersThanADecisionReadsAreRefusedNotTruncated(t *testing.T) {
	srv := newServerWithOptions(t)
	facts := []model.ActionApprovalValue{approvalFact(model.ApprovalRequested, 1, "usr_alice", 0, "d1")}
	for i := 0; i <= authz.MaxApprovals; i++ {
		facts = append(facts, approvalFact(model.ApprovalDecided, 1, "usr_"+string(rune('a'+i%26))+string(rune('a'+i/26)), model.ApprovalOutcomeGranted, ""))
	}
	got := srv.approvalFacts(facts, "d1")
	if len(got) != authz.MaxApprovals+1 {
		t.Fatalf("%d answers passed on, want all %d", len(got), authz.MaxApprovals+1)
	}
	set, err := authz.CompileSet(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := set.Evaluate(t.Context(), &authz.Request{
		Subject: authz.Subject{ID: "usr_alice", Known: true},
		Actor:   authz.Actor{IdentityMode: authz.ModeService, Worker: "sp", WorkerType: "sharepoint"},
		Action:  authz.Action{Operation: "delete-file", Digest: "d1"},
		Context: authz.Context{Approvals: got},
	})
	if d.Permitted() || d.ReasonCode != "INPUT_TOO_LARGE" {
		t.Fatalf("decided %v %s", d.Effect, d.ReasonCode)
	}
}
