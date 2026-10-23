package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The engine half of an enforcement point
// (ADR-0475): a decision becomes a fact only for the
// lease it was made under, its consequence commits in the same batch, and replay
// rebuilds the evidence without deciding anything.

const leaseNanos = int64(60e9)

type authzRig struct {
	t       *testing.T
	dir     string
	h       *harness
	p       *engine.Processor
	cp      *compiler.CompiledProcess
	jobType int32
	piKey   uint64
	job     uint64
	epoch   uint64
	// closed is set once replayed has closed the harness, so the cleanup does not
	// close it twice.
	closed bool
}

// newAuthzRig starts one instance of cp as alice and leases its service-task job to
// the Worker Instance "sharepoint".
func newAuthzRig(t *testing.T, cp *compiler.CompiledProcess, jobType int32) *authzRig {
	t.Helper()
	dir := t.TempDir()
	r := &authzRig{t: t, dir: dir, h: openHarness(t, dir), cp: cp, jobType: jobType}
	// Closed before the temporary directory is removed: Windows refuses to delete a
	// file that is still open, so a store left open fails the test at cleanup.
	t.Cleanup(func() {
		if !r.closed {
			r.h.close(t)
		}
	})
	r.p = engine.New(1, r.h.log, r.h.store, &manualClock{})
	r.p.Deploy(cp)
	if err := r.p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	var created uint64
	r.p.CreateInstanceStartedBy(cp.Key, &created, "usr_alice")
	r.run()
	r.piKey = created
	jobs := activatableJobs(t, r.h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}
	r.job = jobs[0]
	r.p.ActivateJob(r.job, "sharepoint", leaseNanos)
	r.run()
	jv, ok, err := r.h.store.GetJob(r.job)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v %v", ok, err)
	}
	r.epoch = jv.LeaseEpoch
	return r
}

func (r *authzRig) run() {
	r.t.Helper()
	if err := r.p.RunUntilIdle(); err != nil {
		r.t.Fatalf("RunUntilIdle: %v", err)
	}
}

func (r *authzRig) decision(effect model.AuthzEffect, enforced bool) model.AuthorizationValue {
	return model.AuthorizationValue{
		ProcessInstanceKey: r.piKey, JobKey: r.job, LeaseEpoch: r.epoch, DecidedAt: 1000,
		Effect: effect, Enforced: enforced, PolicyID: "sp-documents", PolicyVersion: 3,
		ReasonCode: "PERMIT", Worker: "sp-prod", WorkerInstance: "sharepoint", WorkerType: "sharepoint",
		Operation: "delete-file", Resource: "sharepoint:site=s", ActionDigest: "d1",
		InitiatedBy: "usr_alice", ExecutedFor: "usr_alice", ExecutedAs: "microsoft:t:o",
		ConnectionID: "con_1", IdentityMode: "delegated-per-job",
	}
}

func authorizations(t *testing.T, s *state.Store, piKey uint64) []model.AuthorizationValue {
	t.Helper()
	var out []model.AuthorizationValue
	if err := s.AuthorizationHistory(piKey, func(_ uint64, v *model.AuthorizationValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("AuthorizationHistory: %v", err)
	}
	return out
}

func approvalFacts(t *testing.T, s *state.Store, piKey uint64) []model.ActionApprovalValue {
	t.Helper()
	var out []model.ActionApprovalValue
	if err := s.ApprovalFacts(piKey, func(v *model.ActionApprovalValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("ApprovalFacts: %v", err)
	}
	return out
}

// replayed reopens the log into a fresh, empty store, as recovery after a crash would.
func (r *authzRig) replayed() *state.Store {
	r.t.Helper()
	r.h.close(r.t)
	r.closed = true
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(r.dir, "wal")})
	if err != nil {
		r.t.Fatalf("reopen wal: %v", err)
	}
	store2, err := state.Open(filepath.Join(r.t.TempDir(), "state"))
	if err != nil {
		r.t.Fatalf("fresh state.Open: %v", err)
	}
	r.t.Cleanup(func() {
		_ = store2.Close()
		_ = log2.Close()
	})
	rp := engine.New(1, log2, store2, &manualClock{})
	rp.Deploy(r.cp)
	if err := rp.Recover(); err != nil {
		r.t.Fatalf("recovery Recover: %v", err)
	}
	return store2
}

func TestAPermitIsRecordedAndReplayedWithoutDeciding(t *testing.T) {
	cp, jobType := linearProcess(t)
	r := newAuthzRig(t, cp, jobType)
	var res engine.AuthorizationResult
	r.p.RecordAuthorization(r.decision(model.AuthzPermit, true), nil, "", &res)
	r.run()
	if res.Answer != engine.AuthorizationRecorded || res.DecisionKey == 0 {
		t.Fatalf("result = %+v", res)
	}
	live := authorizations(t, r.h.store, r.piKey)
	if len(live) != 1 || live[0].DecisionKey != res.DecisionKey || live[0].Effect != model.AuthzPermit {
		t.Fatalf("live evidence = %+v", live)
	}
	// A permit changes nothing but the record: the job is still the Worker's.
	jv, _, _ := r.h.store.GetJob(r.job)
	if jv.LeaseExpiresAt == 0 || jv.Retries != 3 {
		t.Fatalf("a permit touched the job: %+v", jv)
	}
	pi, _, err := r.h.store.ProcessInstance(r.piKey)
	if err != nil || pi.StartedBy != "usr_alice" {
		t.Fatalf("StartedBy = %q (%v)", pi.StartedBy, err)
	}
	store2 := r.replayed()
	if got := authorizations(t, store2, r.piKey); !reflect.DeepEqual(got, live) {
		t.Fatalf("replayed evidence differs:\n got %+v\nwant %+v", got, live)
	}
	pi2, _, err := store2.ProcessInstance(r.piKey)
	if err != nil || pi2.StartedBy != "usr_alice" {
		t.Fatalf("replayed StartedBy = %q (%v)", pi2.StartedBy, err)
	}
}

// A decision made for one lease must never become a fact once the job has moved on:
// the next Worker Instance holding it has had no decision at all.
func TestADecisionForALeaseThatIsGoneIsNotRecorded(t *testing.T) {
	cp, jobType := linearProcess(t)
	r := newAuthzRig(t, cp, jobType)
	for name, mutate := range map[string]func(*model.AuthorizationValue){
		"another epoch":    func(v *model.AuthorizationValue) { v.LeaseEpoch++ },
		"another holder":   func(v *model.AuthorizationValue) { v.WorkerInstance = "intruder" },
		"another instance": func(v *model.AuthorizationValue) { v.ProcessInstanceKey++ },
		"no such job":      func(v *model.AuthorizationValue) { v.JobKey = 424242 },
	} {
		v := r.decision(model.AuthzPermit, true)
		mutate(&v)
		var res engine.AuthorizationResult
		r.p.RecordAuthorization(v, nil, "", &res)
		r.run()
		if res.Answer != engine.AuthorizationStale {
			t.Errorf("%s: answer = %v", name, res.Answer)
		}
	}
	if got := authorizations(t, r.h.store, r.piKey); len(got) != 0 {
		t.Fatalf("stale decisions were recorded: %+v", got)
	}
}

func TestAnEnforcedRefusalParksTheJobOnAnIncidentInTheSameBatch(t *testing.T) {
	cp, jobType := linearProcess(t)
	r := newAuthzRig(t, cp, jobType)
	v := r.decision(model.AuthzDeny, true)
	v.ReasonCode = "CONNECTION_REVOKED"
	var res engine.AuthorizationResult
	r.p.RecordAuthorization(v, nil, "authorization denied: CONNECTION_REVOKED", &res)
	r.run()
	if res.Answer != engine.AuthorizationRecorded {
		t.Fatalf("answer = %v", res.Answer)
	}
	jv, ok, _ := r.h.store.GetJob(r.job)
	if !ok || jv.Retries != 0 || jv.LeaseExpiresAt != 0 {
		t.Fatalf("job after refusal = %+v", jv)
	}
	if n := len(activatableJobs(t, r.h.store, jobType)); n != 0 {
		t.Fatalf("a refused job went back to the workers (%d activatable)", n)
	}
	inc, err := r.h.store.GetIncident(jv.ElementInstanceKey)
	if err != nil || inc == nil || inc.Reason != model.IncidentAuthorizationDenied || inc.Message != "authorization denied: CONNECTION_REVOKED" {
		t.Fatalf("incident = %+v (%v)", inc, err)
	}
	store2 := r.replayed()
	inc2, err := store2.GetIncident(jv.ElementInstanceKey)
	if err != nil || inc2 == nil || inc2.Reason != model.IncidentAuthorizationDenied {
		t.Fatalf("replayed incident = %+v (%v)", inc2, err)
	}
	if got := authorizations(t, store2, r.piKey); len(got) != 1 || got[0].Effect != model.AuthzDeny {
		t.Fatalf("replayed evidence = %+v", got)
	}
}

// Shadow mode records what policy would have said and never blocks.
func TestAShadowRefusalBlocksNothing(t *testing.T) {
	cp, jobType := linearProcess(t)
	r := newAuthzRig(t, cp, jobType)
	var res engine.AuthorizationResult
	r.p.RecordAuthorization(r.decision(model.AuthzDeny, false), nil, "x", &res)
	r.run()
	jv, _, _ := r.h.store.GetJob(r.job)
	if jv.LeaseExpiresAt == 0 || jv.Retries != 3 {
		t.Fatalf("a shadow decision touched the job: %+v", jv)
	}
	if got := authorizations(t, r.h.store, r.piKey); len(got) != 1 || got[0].Enforced {
		t.Fatalf("evidence = %+v", got)
	}
}

// approvalProcess is Start → ServiceTask → End, with an error boundary on the task
// catching ATLAS_APPROVAL_REQUIRED and routing to its own end.
func approvalProcess(t *testing.T) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "approval", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	boundary := b.AddBoundaryErrorEvent(task, engine.ApprovalRequiredErrorCode)
	asked := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.Connect(boundary, asked)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(task).Detail).JobType, asked
}

func TestARefusalForWantOfApprovalAsksForItAndThrowsTheError(t *testing.T) {
	cp, jobType, asked := approvalProcess(t)
	r := newAuthzRig(t, cp, jobType)
	v := r.decision(model.AuthzDeny, true)
	v.ReasonCode = "APPROVAL_REQUIRED"
	want := &model.ActionApprovalValue{
		RequestedBy: "usr_alice", ApproverGroup: "grp_managers", Approvals: 1, ObligationID: "manager",
		ExpiresAt: 1 << 62,
	}
	var res engine.AuthorizationResult
	r.p.RecordAuthorization(v, want, "approval required", &res)
	r.run()
	if res.ApprovalKey == 0 {
		t.Fatalf("no approval was asked for: %+v", res)
	}
	// The boundary caught the error: the instance left the task for the approval path.
	if pi, ei := counts(t, r.h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the throw: process=%d element=%d", pi, ei)
	}
	if v := elementVisits(t, r.h.store, cp.Key); v[asked] != 1 {
		t.Fatalf("the approval path was not taken: %v", v)
	}
	facts := approvalFacts(t, r.h.store, r.piKey)
	if len(facts) != 1 {
		t.Fatalf("facts = %+v", facts)
	}
	f := facts[0]
	if f.Kind != model.ApprovalRequested || f.ApprovalKey != res.ApprovalKey || f.ActionDigest != "d1" ||
		f.DecisionKey != res.DecisionKey || f.JobKey != r.job || f.Worker != "sp-prod" ||
		f.ApproverGroup != "grp_managers" || f.PolicyID != "sp-documents" || f.PolicyVersion != 3 {
		t.Fatalf("request = %+v", f)
	}
	store2 := r.replayed()
	if got := approvalFacts(t, store2, r.piKey); !reflect.DeepEqual(got, facts) {
		t.Fatalf("replayed facts differ:\n got %+v\nwant %+v", got, facts)
	}
}

// An approval request that nothing catches parks the job on an incident naming the
// code, and a second refusal of the same action joins the open request.
func TestARepeatedRefusalJoinsTheOpenRequest(t *testing.T) {
	cp, jobType := linearProcess(t)
	r := newAuthzRig(t, cp, jobType)
	want := &model.ActionApprovalValue{ApproverGroup: "grp_managers", Approvals: 1, ExpiresAt: 1 << 62}
	var first engine.AuthorizationResult
	v := r.decision(model.AuthzDeny, true)
	r.p.RecordAuthorization(v, want, "approval required", &first)
	r.run()
	jv, _, _ := r.h.store.GetJob(r.job)
	inc, _ := r.h.store.GetIncident(jv.ElementInstanceKey)
	if inc == nil || inc.Reason != model.IncidentUnhandledError {
		t.Fatalf("incident = %+v", inc)
	}
	// Resolving re-offers the job; a new lease is decided again and refused again.
	r.p.ResolveIncident(jv.ElementInstanceKey, 3)
	r.run()
	r.p.ActivateJob(r.job, "sharepoint", leaseNanos)
	r.run()
	jv, _, _ = r.h.store.GetJob(r.job)
	v.LeaseEpoch = jv.LeaseEpoch
	var second engine.AuthorizationResult
	r.p.RecordAuthorization(v, want, "approval required", &second)
	r.run()
	if second.ApprovalKey != first.ApprovalKey {
		t.Fatalf("a second request was opened: %d vs %d", second.ApprovalKey, first.ApprovalKey)
	}
	if n := len(approvalFacts(t, r.h.store, r.piKey)); n != 1 {
		t.Fatalf("facts = %d, want 1", n)
	}
	if n := len(authorizations(t, r.h.store, r.piKey)); n != 2 {
		t.Fatalf("decisions = %d, want 2 — every lease is decided on its own", n)
	}
}

func TestAnApproverAnswersOnceAndMayWithdraw(t *testing.T) {
	cp, jobType, _ := approvalProcess(t)
	r := newAuthzRig(t, cp, jobType)
	var res engine.AuthorizationResult
	r.p.RecordAuthorization(r.decision(model.AuthzDeny, true),
		&model.ActionApprovalValue{ApproverGroup: "grp_managers", Approvals: 1, ExpiresAt: 5000, RequestedBy: "usr_alice"},
		"approval required", &res)
	r.run()
	answer := func(kind model.ApprovalKind, outcome model.ApprovalOutcome, who string, at int64, key uint64) engine.ApprovalResult {
		var ar engine.ApprovalResult
		r.p.RecordApprovalAnswer(model.ActionApprovalValue{ProcessInstanceKey: r.piKey, ApprovalKey: key,
			Kind: kind, Outcome: outcome, Principal: who, At: at, Reason: "ok"}, &ar)
		r.run()
		return ar
	}
	key := res.ApprovalKey
	for _, tc := range []struct {
		name    string
		kind    model.ApprovalKind
		outcome model.ApprovalOutcome
		who     string
		at      int64
		key     uint64
		want    engine.ApprovalAnswer
	}{
		{"unknown request", model.ApprovalDecided, model.ApprovalOutcomeGranted, "usr_mgr", 2000, 99, engine.ApprovalUnknown},
		{"nobody", model.ApprovalDecided, model.ApprovalOutcomeGranted, "", 2000, key, engine.ApprovalInvalid},
		{"no outcome", model.ApprovalDecided, 0, "usr_mgr", 2000, key, engine.ApprovalInvalid},
		{"a request is not an answer", model.ApprovalRequested, 0, "usr_mgr", 2000, key, engine.ApprovalInvalid},
		{"nothing to withdraw", model.ApprovalRevoked, 0, "usr_mgr", 2000, key, engine.ApprovalNothingToWithdraw},
		{"granted", model.ApprovalDecided, model.ApprovalOutcomeGranted, "usr_mgr", 2000, key, engine.ApprovalRecordedAnswer},
		{"twice", model.ApprovalDecided, model.ApprovalOutcomeRejected, "usr_mgr", 2100, key, engine.ApprovalAlreadyAnswered},
		{"withdrawn", model.ApprovalRevoked, 0, "usr_mgr", 2200, key, engine.ApprovalRecordedAnswer},
		{"answered again", model.ApprovalDecided, model.ApprovalOutcomeRejected, "usr_mgr", 2300, key, engine.ApprovalRecordedAnswer},
		{"too late", model.ApprovalDecided, model.ApprovalOutcomeGranted, "usr_other", 5000, key, engine.ApprovalClosed},
	} {
		if got := answer(tc.kind, tc.outcome, tc.who, tc.at, tc.key); got.Answer != tc.want {
			t.Errorf("%s: answer = %v, want %v", tc.name, got.Answer, tc.want)
		}
	}
	facts := approvalFacts(t, r.h.store, r.piKey)
	if len(facts) != 4 {
		t.Fatalf("facts = %d, want request + grant + withdrawal + rejection", len(facts))
	}
	grant := facts[1]
	if grant.Kind != model.ApprovalDecided || grant.Outcome != model.ApprovalOutcomeGranted ||
		grant.ActionDigest != "d1" || grant.ApproverGroup != "grp_managers" || grant.RequestedBy != "usr_alice" {
		t.Fatalf("an answer does not say what it agreed to: %+v", grant)
	}
	store2 := r.replayed()
	if got := approvalFacts(t, store2, r.piKey); !reflect.DeepEqual(got, facts) {
		t.Fatalf("replayed facts differ")
	}
}

// A caller that does not want the answer may pass no result holder: the decision and
// the approval are recorded all the same.
func TestADecisionAndAnAnswerNeedNoResultHolder(t *testing.T) {
	cp, jobType, _ := approvalProcess(t)
	r := newAuthzRig(t, cp, jobType)
	r.p.RecordAuthorization(r.decision(model.AuthzDeny, true),
		&model.ActionApprovalValue{ApproverGroup: "grp_managers", Approvals: 1, ExpiresAt: 5000, RequestedBy: "usr_alice"},
		"approval required", nil)
	r.run()
	facts := approvalFacts(t, r.h.store, r.piKey)
	if len(authorizations(t, r.h.store, r.piKey)) != 1 || len(facts) != 1 {
		t.Fatalf("recorded %d decisions and %d approval facts", len(authorizations(t, r.h.store, r.piKey)), len(facts))
	}
	r.p.RecordApprovalAnswer(model.ActionApprovalValue{ProcessInstanceKey: r.piKey, ApprovalKey: facts[0].ApprovalKey,
		Kind: model.ApprovalDecided, Outcome: model.ApprovalOutcomeGranted, Principal: "usr_mgr", At: 2000}, nil)
	r.run()
	if got := approvalFacts(t, r.h.store, r.piKey); len(got) != 2 || got[1].Principal != "usr_mgr" {
		t.Fatalf("the answer was not recorded: %+v", got)
	}
}
