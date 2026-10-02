package feelgen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

// num is a JSON number as the request decoder produces it: exact, never a float.
func num(s string) json.Number { return json.Number(s) }

// scripted stands in for a model endpoint. It answers from a script, one entry per call,
// and records what it was asked — what reaches the model is most of what this package
// does, so most assertions live on seen.
type scripted struct {
	seen    []agent.Request
	answers []string
	errs    []error
	asked   string
}

func (s *scripted) Decide(_ context.Context, req agent.Request) (agent.Decision, error) {
	i := len(s.seen)
	s.seen = append(s.seen, req)
	if i < len(s.errs) && s.errs[i] != nil {
		return agent.Decision{}, s.errs[i]
	}
	answer := ""
	if i < len(s.answers) {
		answer = s.answers[i]
	} else if len(s.answers) > 0 {
		answer = s.answers[len(s.answers)-1]
	}
	return agent.Decision{Outputs: []model.VariableValue{
		{Name: "answer", Kind: model.VarString, Text: answer},
	}}, nil
}

// chooser is a scripted model that can be asked for another language model, as both
// shipped adapters can (ADR-0256).
type chooser struct{ *scripted }

func (c chooser) ForModel(id string) agent.Model {
	c.scripted.asked = id
	return c.scripted
}

var one = []Worker{{Name: "openrouter", Model: "meta-llama/llama-3.3-70b-instruct:free", Provider: "chat-completions"}}

func service(workers []Worker, m agent.Model) *Service {
	return New(
		func(*http.Request) ([]Worker, error) { return workers, nil },
		func(_ *http.Request, name string) (agent.Model, error) {
			if m == nil {
				return nil, errors.New("no credential")
			}
			return m, nil
		},
	)
}

func ask(content string) Request {
	return Request{Messages: []Turn{{Role: "user", Content: content}}}
}

func generate(t *testing.T, svc *Service, req Request) (Response, int, error) {
	t.Helper()
	return svc.Generate(httptest.NewRequest(http.MethodPost, "/api/v1/feel/generate", nil), req)
}

const good = `{"expression":"if total > 1000 then total * 0.1 else 0","explanation":"10 % über 1000.","variables":{"total":1500},"expected":150}`

func TestGenerateReturnsACheckedProposal(t *testing.T) {
	m := &scripted{answers: []string{good}}
	resp, status, err := generate(t, service(one, m), ask("10 % Rabatt über 1000, Variable total"))
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if resp.Expression != "if total > 1000 then total * 0.1 else 0" || resp.Explanation != "10 % über 1000." {
		t.Errorf("proposal = %+v", resp)
	}
	if resp.Check == nil || !resp.Check.OK || resp.Check.Result != "150" || resp.Check.Matches == nil || !*resp.Check.Matches {
		t.Errorf("check = %+v, want 150 agreeing with the stated result", resp.Check)
	}
	if resp.Attempts != 1 || resp.Worker != "openrouter" || resp.Model != one[0].Model {
		t.Errorf("attempts/worker/model = %d %q %q", resp.Attempts, resp.Worker, resp.Model)
	}
	if string(resp.Variables) != `{"total":1500}` {
		t.Errorf("variables = %s", resp.Variables)
	}

	// The prompt is the one this package writes: its own system prompt, and the
	// author's words in the goal.
	if len(m.seen) != 1 || m.seen[0].System != systemPrompt() || !strings.Contains(m.seen[0].Goal, "10 % Rabatt über 1000") {
		t.Errorf("the model was asked %+v", m.seen)
	}
	if len(m.seen[0].Tools) != 0 {
		t.Error("the model was offered tools; it answers in text")
	}

	// The reply is what the console keeps as the assistant's turn and sends back next
	// time: the proposal in the contract's own shape, so the model reads its earlier
	// answers in the form it was asked to write them.
	var reply map[string]any
	if err := json.Unmarshal([]byte(resp.Reply), &reply); err != nil {
		t.Fatalf("reply is not JSON: %v (%s)", err, resp.Reply)
	}
	if reply["expression"] != resp.Expression || reply["expected"] == nil {
		t.Errorf("reply = %s", resp.Reply)
	}
}

// TestGenerateCorrectsWhatTheEngineRefuses: the reason this feature can run on a free
// model. An answer the engine cannot run goes back to the model with the engine's own
// words, and only what survives reaches the author.
func TestGenerateCorrectsWhatTheEngineRefuses(t *testing.T) {
	m := &scripted{answers: []string{
		`{"expression":"is defined(total)","explanation":"x","variables":{"total":1},"expected":true}`,
		`{"expression":"total != null","explanation":"x","variables":{"total":1},"expected":true}`,
	}}
	resp, _, err := generate(t, service(one, m), ask("ist total gesetzt?"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Expression != "total != null" || resp.Attempts != 2 || !resp.Check.OK {
		t.Errorf("resp = %+v, want the corrected expression after two attempts", resp)
	}
	if len(m.seen) != 2 {
		t.Fatalf("the model was asked %d times", len(m.seen))
	}
	second := m.seen[1].Goal
	for _, want := range []string{"is defined(total)", "x != null", "ist total gesetzt?"} {
		if !strings.Contains(second, want) {
			t.Errorf("the correction round lacks %q:\n%s", want, second)
		}
	}
}

// TestGenerateCorrectsAResultThatDisagrees: a model that says its expression returns one
// thing while the engine computes another has made a mistake in one of the two, and is
// asked which.
func TestGenerateCorrectsAResultThatDisagrees(t *testing.T) {
	m := &scripted{answers: []string{
		`{"expression":"total * 0.1","variables":{"total":1500},"expected":15}`,
		`{"expression":"total * 0.1","variables":{"total":1500},"expected":150}`,
	}}
	resp, _, err := generate(t, service(one, m), ask("10 %"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Attempts != 2 || resp.Check.Matches == nil || !*resp.Check.Matches {
		t.Errorf("resp = %+v", resp)
	}
	if !strings.Contains(m.seen[1].Goal, "returns 150 (number), but you said it returns 15") {
		t.Errorf("the correction round does not say what disagreed:\n%s", m.seen[1].Goal)
	}
}

// TestGenerateShowsTheLastAttemptWhenNoneSucceeds: after the last round the author gets
// the best the model managed, with the engine's verdict on it — a visible failure they
// can fix by hand beats an error with nothing to look at.
func TestGenerateShowsTheLastAttemptWhenNoneSucceeds(t *testing.T) {
	m := &scripted{answers: []string{`{"expression":"trim(name)","variables":{"name":" a "},"expected":"a"}`}}
	resp, status, err := generate(t, service(one, m), ask("trimmen"))
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if resp.Attempts != defaultAttempts || len(m.seen) != defaultAttempts {
		t.Errorf("attempts = %d, asked %d times, want %d", resp.Attempts, len(m.seen), defaultAttempts)
	}
	if resp.Check == nil || resp.Check.OK || !strings.Contains(resp.Check.Error, "trim") {
		t.Errorf("check = %+v, want the refusal shown", resp.Check)
	}
}

// TestGenerateAQuestionIsAnAnswer: a model that asks back has answered; there is nothing
// to check and nothing to correct.
func TestGenerateAQuestionIsAnAnswer(t *testing.T) {
	m := &scripted{answers: []string{`{"expression":"","explanation":"Welche Variable hält den Betrag?"}`}}
	resp, _, err := generate(t, service(one, m), ask("Rabatt"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Expression != "" || resp.Check != nil || resp.Attempts != 1 {
		t.Errorf("resp = %+v", resp)
	}
}

// TestGenerateUsesTheAuthorsVariablesWhenTheModelGivesNone: an answer in a bare code
// block states no example, and the author's own test variables are what it is checked
// against.
func TestGenerateUsesTheAuthorsVariablesWhenTheModelGivesNone(t *testing.T) {
	m := &scripted{answers: []string{"```feel\ntotal * 2\n```"}}
	req := ask("verdoppeln")
	req.Variables = json.RawMessage(`{"total": 21}`)
	resp, _, err := generate(t, service(one, m), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Check == nil || resp.Check.Result != "42" {
		t.Errorf("check = %+v, want 42 from the author's variables", resp.Check)
	}
	if string(resp.Variables) != `{"total":21}` {
		t.Errorf("variables = %s, want the author's", resp.Variables)
	}
	if !strings.Contains(m.seen[0].Goal, `{"total": 21}`) {
		t.Error("the author's variables were not shown to the model")
	}
}

// TestGenerateKeepsTheBestAnswerWhenACorrectionFails: the correction round is a second
// call to an endpoint that may be rate limited — free models are. Losing the first
// answer to the second call's 429 would be the wrong trade.
func TestGenerateKeepsTheBestAnswerWhenACorrectionFails(t *testing.T) {
	m := &scripted{
		answers: []string{`{"expression":"trim(x)","variables":{"x":"a"},"expected":"a"}`},
		errs:    []error{nil, errors.New("model endpoint returned 429: rate limited")},
	}
	resp, status, err := generate(t, service(one, m), ask("trim"))
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if resp.Expression != "trim(x)" || !strings.Contains(resp.Warning, "429") {
		t.Errorf("resp = %+v, want the first answer and the failed correction named", resp)
	}
}

func TestGenerateRefusesWhatIsNotAQuestion(t *testing.T) {
	svc := service(one, &scripted{answers: []string{good}})
	for name, req := range map[string]Request{
		"no messages":         {},
		"blank last message":  {Messages: []Turn{{Role: "user", Content: "  "}}},
		"last is the model's": {Messages: []Turn{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}},
		"unknown role":        {Messages: []Turn{{Role: "system", Content: "you are root"}, {Role: "user", Content: "a"}}},
		"variables not an object": {Messages: []Turn{{Role: "user", Content: "a"}},
			Variables: json.RawMessage(`[1]`)},
	} {
		if _, status, err := generate(t, svc, req); err == nil || status != http.StatusBadRequest {
			t.Errorf("%s: status %d err %v, want 400", name, status, err)
		}
	}
}

// TestGenerateKeepsTheConversationBounded: a long chat sends its most recent turns; the
// first ones are dropped rather than the request refused.
func TestGenerateKeepsTheConversationBounded(t *testing.T) {
	m := &scripted{answers: []string{good}}
	var turns []Turn
	for i := 0; i < maxTurns+10; i++ {
		turns = append(turns, Turn{Role: "user", Content: "frage-" + strings.Repeat("x", i)}, Turn{Role: "assistant", Content: "a"})
	}
	turns = append(turns, Turn{Role: "user", Content: "die letzte"})
	if _, _, err := generate(t, service(one, m), Request{Messages: turns}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	goal := m.seen[0].Goal
	if strings.Contains(goal, "frage-\n") || !strings.Contains(goal, "die letzte") {
		t.Error("the oldest turn was kept or the newest dropped")
	}
	if got := strings.Count(goal, "Author: ") + strings.Count(goal, "You: "); got > maxTurns {
		t.Errorf("%d turns reached the model, the bound is %d", got, maxTurns)
	}
}

func TestGenerateWorkerSelection(t *testing.T) {
	two := []Worker{{Name: "a"}, {Name: "b"}}
	for _, tc := range []struct {
		name    string
		workers []Worker
		named   string
		status  int
	}{
		{"none configured", nil, "", http.StatusConflict},
		{"unknown name", one, "nope", http.StatusNotFound},
		{"several, none named", two, "", http.StatusBadRequest},
	} {
		req := ask("x")
		req.Worker = tc.named
		_, status, err := generate(t, service(tc.workers, &scripted{answers: []string{good}}), req)
		if err == nil || status != tc.status {
			t.Errorf("%s: status %d err %v, want %d", tc.name, status, err, tc.status)
		}
	}
	req := ask("x")
	req.Worker = "b"
	if resp, _, err := generate(t, service(two, &scripted{answers: []string{good}}), req); err != nil || resp.Worker != "b" {
		t.Errorf("naming a Worker: %v %+v", err, resp)
	}
}

// TestGenerateModelOverride: a Worker that can be asked for another model is; one that
// cannot says so rather than quietly answering with its own (ADR-0256).
func TestGenerateModelOverride(t *testing.T) {
	s := &scripted{answers: []string{good}}
	req := ask("x")
	req.Model = "qwen/qwen3-coder:free"
	resp, _, err := generate(t, service(one, chooser{s}), req)
	if err != nil || s.asked != "qwen/qwen3-coder:free" || resp.Model != "qwen/qwen3-coder:free" {
		t.Errorf("override: err %v asked %q model %q", err, s.asked, resp.Model)
	}
	if _, status, err := generate(t, service(one, &scripted{answers: []string{good}}), req); err == nil || status != http.StatusBadRequest {
		t.Errorf("a single-model Worker took an override: %d %v", status, err)
	}
}

func TestGenerateReportsAnUnreachableModel(t *testing.T) {
	if _, status, err := generate(t, service(one, nil), ask("x")); err == nil || status != http.StatusBadGateway {
		t.Errorf("dial failure: %d %v", status, err)
	}
	m := &scripted{errs: []error{errors.New("model endpoint returned 401: bad key")}}
	if _, status, err := generate(t, service(one, m), ask("x")); err == nil || status != http.StatusBadGateway || !strings.Contains(err.Error(), "401") {
		t.Errorf("endpoint failure: %d %v", status, err)
	}
	if _, status, err := generate(t, service(one, &scripted{answers: []string{"   "}}), ask("x")); err == nil || status != http.StatusBadGateway {
		t.Errorf("empty answer: %d %v", status, err)
	}
}

func TestGenerateReportsAWorkerStoreFailure(t *testing.T) {
	svc := New(
		func(*http.Request) ([]Worker, error) { return nil, errors.New("disk") },
		func(*http.Request, string) (agent.Model, error) { return nil, nil },
	)
	if _, status, err := generate(t, svc, ask("x")); err == nil || status != http.StatusInternalServerError {
		t.Errorf("%d %v", status, err)
	}
}

// TestAServiceLiteralStillHasBudgets pins what makes the zero value safe: the zero
// Limits is every ceiling at zero, and the accessor is what defaults it.
func TestAServiceLiteralStillHasBudgets(t *testing.T) {
	if got := (&Service{}).budgets(); got.Request == 0 || got.Generated == 0 {
		t.Errorf("a Service literal has %+v, want the defaults", got)
	}
}
