package model

import (
	"reflect"
	"testing"
)

func TestAnAuthorizationDecisionRoundTrips(t *testing.T) {
	want := &AuthorizationValue{
		ProcessInstanceKey: 1, ElementInstanceKey: 2, JobKey: 3, LeaseEpoch: 4, DecisionKey: 5,
		DecidedAt: 1_700_000_000_000_000_000, Effect: AuthzDeny, Enforced: true, PolicyVersion: 3,
		ReasonCode: "APPROVAL_REQUIRED", PolicyID: "sp-documents", Worker: "sp-prod",
		WorkerInstance: "sharepoint", WorkerType: "sharepoint", Operation: "delete-file",
		Resource: "sharepoint:site=s", ActionDigest: "abc", InitiatedBy: "usr_a", ExecutedFor: "usr_a",
		ExecutedAs: "microsoft:t:o", ConnectionID: "con_1", IdentityMode: "delegated-per-job",
		Detail: `{"trace":[]}`,
	}
	got := &AuthorizationValue{}
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
	if want.ValueType() != VTAuthorization || VTAuthorization.String() != "Authorization" {
		t.Errorf("value type = %v", want.ValueType())
	}
	if err := got.decode(make([]byte, 10)); err != ErrShortBuffer {
		t.Errorf("a short buffer decoded: %v", err)
	}
	if err := got.decode(want.encode(nil)[:60]); err == nil {
		t.Error("a truncated string list decoded")
	}
}

func TestAnApprovalFactRoundTrips(t *testing.T) {
	want := &ActionApprovalValue{
		ProcessInstanceKey: 1, ApprovalKey: 2, JobKey: 3, DecisionKey: 4, At: 5, ExpiresAt: 6,
		Kind: ApprovalDecided, Outcome: ApprovalOutcomeGranted, Approvals: 2, PolicyVersion: 7,
		ActionDigest: "d", Worker: "w", Operation: "o", Resource: "r", RequestedBy: "usr_r",
		ApproverGroup: "grp_m", PolicyID: "p", ObligationID: "ob", Principal: "usr_m", Reason: "ok",
	}
	got := &ActionApprovalValue{}
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
	if want.ValueType() != VTActionApproval || VTActionApproval.String() != "ActionApproval" {
		t.Errorf("value type = %v", want.ValueType())
	}
	if err := got.decode(make([]byte, 10)); err != ErrShortBuffer {
		t.Errorf("a short buffer decoded: %v", err)
	}
	if err := got.decode(want.encode(nil)[:55]); err == nil {
		t.Error("a truncated string list decoded")
	}
	for k, name := range map[ApprovalKind]string{ApprovalRequested: "requested", ApprovalDecided: "decided", ApprovalRevoked: "revoked", 0: "ApprovalKind(?)"} {
		if k.String() != name {
			t.Errorf("%d.String() = %s", k, k.String())
		}
	}
	for o, name := range map[ApprovalOutcome]string{ApprovalOutcomeGranted: "granted", ApprovalOutcomeRejected: "rejected", 0: ""} {
		if o.String() != name {
			t.Errorf("%d.String() = %q", o, o.String())
		}
	}
}

// The engine's effect numbers are the PDP's: a record decoded years later must read
// back as the effect it was written as. authz cannot be imported here (model is the
// bottom of the dependency graph), so the numbers are pinned instead.
func TestAuthorizationEffectNumbersArePinned(t *testing.T) {
	for e, n := range map[AuthzEffect]uint8{AuthzIndeterminate: 0, AuthzPermit: 1, AuthzDeny: 2, AuthzNotApplicable: 3} {
		if uint8(e) != n {
			t.Errorf("%v = %d, want %d", e, e, n)
		}
	}
	for e, name := range map[AuthzEffect]string{AuthzIndeterminate: "Indeterminate", AuthzPermit: "Permit", AuthzDeny: "Deny", AuthzNotApplicable: "NotApplicable"} {
		if e.String() != name {
			t.Errorf("%d.String() = %s", e, e.String())
		}
	}
}

func TestTheInstanceRecordsWhoStartedIt(t *testing.T) {
	want := &ProcessInstanceValue{ProcessDefKey: 1, CreatedAt: 2, StartedBy: "usr_alice"}
	got := &ProcessInstanceValue{}
	if err := got.decode(want.encode(nil)); err != nil {
		t.Fatal(err)
	}
	if got.StartedBy != "usr_alice" {
		t.Fatalf("StartedBy = %q", got.StartedBy)
	}
	// A record written before the field ends after the pass hash, and reads back as
	// started by nobody known.
	old := &ProcessInstanceValue{ProcessDefKey: 1, CreatedAt: 2}
	enc := old.encode(nil)
	enc = enc[:len(enc)-4] // the empty StartedBy's length prefix
	got = &ProcessInstanceValue{StartedBy: "stale"}
	if err := got.decode(enc); err != nil || got.StartedBy != "" {
		t.Fatalf("legacy record: %q %v", got.StartedBy, err)
	}
}
