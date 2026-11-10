package entrachannel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/evidence"
)

// withOperation runs the suite with op in place of entra.Run and returns each case's
// outcome by name.
func withOperation(t *testing.T, op func(context.Context, entra.Job, *entra.Registry) (map[string]any, error)) map[string]evidence.Result {
	t.Helper()
	prev := runOperation
	runOperation = op
	t.Cleanup(func() { runOperation = prev })
	results, err := Suite().Run(context.Background(), loadContract(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := map[string]evidence.Result{}
	for _, r := range results {
		out[r.Case] = r
	}
	return out
}

func wantOutcome(t *testing.T, got map[string]evidence.Result, name, outcome string) {
	t.Helper()
	if r := got[name]; r.Outcome != outcome {
		t.Errorf("%q = %s (%s), want %s", name, r.Outcome, r.Detail, outcome)
	}
}

// An operation that reports success without asking Graph anything fails every case
// that a success or a missing request would hide.
func TestTheSuiteFailsAnOperationThatClaimsSuccessWithoutActing(t *testing.T) {
	got := withOperation(t, func(_ context.Context, j entra.Job, _ *entra.Registry) (map[string]any, error) {
		return map[string]any{j.ResultVariable: map[string]any{"id": "19:made-up@thread.tacv2"}}, nil
	})
	for _, name := range []string{caseCreates, caseUnknownSpaceCreatesNothing, caseUnknownSpaceCode, caseRepeatCreatesNoSecond, caseRepeatCode, caseRefusedSpace} {
		wantOutcome(t, got, name, evidence.OutcomeFailed)
	}
}

// What a create request must carry, and what its answer must be, each fail on their own.
func TestTheSuiteChecksTheRequestAndTheAnswer(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*entra.Job)
		answer func(map[string]any) map[string]any
	}{
		{name: "a private channel", mutate: func(j *entra.Job) { j.Attributes["membershipType"] = "private" }},
		{name: "another team", mutate: func(j *entra.Job) {
			if j.GroupID == knownSpace {
				j.GroupID = otherSpace
			}
		}},
		{name: "an id that is not the channel's", answer: func(map[string]any) map[string]any { return map[string]any{"id": "19:other@thread.tacv2"} }},
		{name: "an answer the schema refuses", answer: func(map[string]any) map[string]any { return map[string]any{"id": ""} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withOperation(t, func(ctx context.Context, j entra.Job, reg *entra.Registry) (map[string]any, error) {
				if c.mutate != nil {
					j.Attributes = map[string]any{"displayName": j.Attributes["displayName"], "description": j.Attributes["description"], "membershipType": j.Attributes["membershipType"]}
					c.mutate(&j)
				}
				res, err := entra.Run(ctx, j, reg)
				if err == nil && c.answer != nil {
					res = map[string]any{j.ResultVariable: c.answer(res)}
				}
				return res, err
			})
			wantOutcome(t, got, caseCreates, evidence.OutcomeFailed)
		})
	}
}

// An operation that fails even for a known team and a free name fails the first case,
// and the suite stops when its own setup — the first of two creates — cannot succeed.
func TestTheSuiteFailsAnOperationThatAlwaysFails(t *testing.T) {
	prev := runOperation
	runOperation = func(context.Context, entra.Job, *entra.Registry) (map[string]any, error) {
		return nil, &job.BPMNError{Code: "X", Message: "no"}
	}
	t.Cleanup(func() { runOperation = prev })
	if _, err := Suite().Run(context.Background(), loadContract(t)); err == nil {
		t.Fatal("the suite ran its repeat cases although their first create failed")
	}
}

// A code the operation throws counts once it is mapped to the contract's code.
func TestAMappedCodeMeetsTheContractCode(t *testing.T) {
	codeOf["ENTRA_TEAM_NOT_FOUND"] = "SPACE_NOT_FOUND"
	codeOf["ENTRA_CHANNEL_EXISTS"] = "CHANNEL_ALREADY_EXISTS"
	t.Cleanup(func() {
		delete(codeOf, "ENTRA_TEAM_NOT_FOUND")
		delete(codeOf, "ENTRA_CHANNEL_EXISTS")
	})
	got := withOperation(t, func(ctx context.Context, j entra.Job, reg *entra.Registry) (map[string]any, error) {
		res, err := entra.Run(ctx, j, reg)
		var ge *entra.GraphError
		if asGraphError(err, &ge) {
			switch ge.Status {
			case 404:
				return nil, &job.BPMNError{Code: "ENTRA_TEAM_NOT_FOUND"}
			case 409:
				return nil, &job.BPMNError{Code: "ENTRA_CHANNEL_EXISTS"}
			}
		}
		return res, err
	})
	wantOutcome(t, got, caseUnknownSpaceCode, evidence.OutcomePassed)
	wantOutcome(t, got, caseRepeatCode, evidence.OutcomePassed)
}

func asGraphError(err error, target **entra.GraphError) bool {
	if err == nil {
		return false
	}
	ge, ok := err.(*entra.GraphError)
	if ok {
		*target = ge
	}
	return ok
}

// An operation that reports the refusal but creates a channel anyway — here, by
// retrying under another name in the known team — fails every "creates nothing" case.
func TestTheSuiteFailsAnOperationThatActsDespiteARefusal(t *testing.T) {
	got := withOperation(t, func(ctx context.Context, j entra.Job, reg *entra.Registry) (map[string]any, error) {
		res, err := entra.Run(ctx, j, reg)
		if err != nil {
			retry := j
			retry.GroupID = knownSpace
			retry.Attributes = map[string]any{"displayName": j.Attributes["displayName"].(string) + " 2", "membershipType": "standard"}
			_, _ = entra.Run(ctx, retry, reg)
		}
		return res, err
	})
	for _, name := range []string{caseUnknownSpaceCreatesNothing, caseRepeatCreatesNoSecond, caseRefusedSpace} {
		wantOutcome(t, got, name, evidence.OutcomeFailed)
	}
}

// What the suite refuses itself: a contract whose schemas do not compile, an input its
// contract refuses, and a request the fake was not built to answer.
func TestTheSuiteRefusesWhatItCannotCheck(t *testing.T) {
	c := loadContract(t)
	c.Spec.InputSchema = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"a":{"type":"nope"}}}`)
	if _, err := Suite().Run(context.Background(), c); err == nil {
		t.Error("the suite ran against a contract whose schema does not compile")
	}

	compiled, err := compileFor(t)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{ctx: context.Background(), contract: compiled}
	g := newFakeGraph()
	defer g.close()
	if _, err := s.operate(g, map[string]any{"spaceId": knownSpace}); err == nil {
		t.Error("an input without displayName reached the operation")
	}

	for _, req := range []struct{ method, path, body string }{
		{"GET", "/teams/" + knownSpace + "/channels", ""},
		{"POST", "/users", "{}"},
		{"POST", "/teams/" + knownSpace + "/channels", "not json"},
	} {
		resp, err := g.srv.Client().Do(mustRequest(t, req.method, g.srv.URL+req.path, req.body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s answered %d, want a refusal", req.method, req.path, resp.StatusCode)
		}
	}
	if g.created() != 0 {
		t.Error("a request the fake refuses created a channel")
	}
}

func compileFor(t *testing.T) (*workertype.CompiledTechnicalContract, error) {
	t.Helper()
	return workertype.CompileTechnicalContract(loadContract(t))
}

func mustRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}
