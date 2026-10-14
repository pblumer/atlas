// Package feelgen writes a FEEL expression from a conversation with the author
// (ADR-0445).
//
// It is design-time authoring, the way form generation is (ADR-0260), and it stands on
// the same three legs. There is no instance, no token, no job and no event: an author
// says what an expression should compute, a model writes one, and it arrives in the
// console's FEEL assistant as a proposal the author tests, copies or applies to a field
// themselves. Nothing here writes to a store.
//
// **It asks the Worker an operator already configured.** An agent Worker (ADR-0255) is a
// Console record holding an endpoint, a wire format, a model name and a vault reference
// to an API key; generation reaches exactly that record, through the adapters the
// runtime agent uses (connector/agent). There is no second place to configure a model,
// no second credential, and no key in the browser.
//
// **What the model writes is checked by the engine that will run it.** That is the one
// thing this package adds to form generation's pattern, and the reason it can run on a
// small free model. Every expression is compiled, its calls checked as a deploy checks
// them (ADR-0388), and evaluated against the example the model gave, with the result
// held against the one the model claimed. An answer that fails goes back to the model
// with the engine's own words, a bounded number of times; only then does the author see
// it — with the engine's verdict beside it, whichever way it went.
//
// **The conversation is the console's, not this package's.** The adapters send one
// message per call, and nothing here remembers a request after answering it: the
// console sends the whole conversation each time, and the reply it should keep as the
// model's turn comes back with every answer.
package feelgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/limits"
)

const (
	// defaultTimeout bounds one request, correction rounds included. A free model
	// behind a shared gateway can take most of a minute for one answer; three of those
	// is the most an author watching a spinner should wait before they learn something.
	defaultTimeout = 150 * time.Second
	// defaultAttempts is one answer and up to two corrections. A model that has been
	// shown the engine's error twice and still gets it wrong is not going to get it
	// right on the fourth try, and every round is a call somebody's rate limit counts.
	defaultAttempts = 3
	// maxTurns bounds the conversation that reaches the model. The oldest turns are
	// the first to go: an expression is refined, and what was said about its first
	// version matters least.
	maxTurns = 30
)

// The two roles a turn may have. There is no system role: the system prompt is this
// package's, and a request cannot put words in its place.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// Worker is one agent Worker as this area needs to speak about it: the name a request
// names it by, and what it is configured to ask. No endpoint and nowhere to put a
// credential (ADR-0041/0069).
type Worker struct {
	Name     string `json:"name"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Turn is one message of the conversation, the author's ("user") or the model's
// ("assistant"). The model's turns are the Reply of earlier responses, kept by the
// console as they came.
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is one message of the author's, with everything said before it.
type Request struct {
	// Messages is the conversation, oldest first. The last one is the author's and is
	// the one to answer.
	Messages []Turn `json:"messages"`
	// Expression is what the assistant's editor holds now, which the author may have
	// changed by hand since the last answer.
	Expression string `json:"expression,omitempty"`
	// Variables are the editor's test variables, a JSON object. They are shown to the
	// model and are what an answer without an example of its own is checked against.
	Variables json.RawMessage `json:"variables,omitempty"`
	// Target says where the expression will be used — the field the assistant was
	// opened from, in the console's words — because a gateway condition must be a
	// boolean and a decision table's input cell is not an expression at all.
	Target string `json:"target,omitempty"`
	// Worker names the agent Worker to ask; empty picks the only one in reach.
	Worker string `json:"worker,omitempty"`
	// Model overrides the language model that Worker is configured for (ADR-0256).
	Model string `json:"model,omitempty"`
}

// Response is the proposal, with the engine's verdict on it. Nothing is stored.
type Response struct {
	Expression  string `json:"expression"`
	Explanation string `json:"explanation"`
	// Variables is the example the expression was checked against — the model's
	// own, or the author's when it gave none — for the console to load beside it.
	Variables json.RawMessage `json:"variables,omitempty"`
	// Check is the engine's verdict, absent when the answer was a question back.
	Check *Check `json:"check,omitempty"`
	// Reply is the model's turn as the console should keep it and send it back.
	Reply string `json:"reply"`
	// Attempts is how many times the model was asked: one, plus each correction.
	Attempts int `json:"attempts"`
	// Warning says why a correction round could not run, when one could not — the
	// proposal is then the best answer the model had given.
	Warning string `json:"warning,omitempty"`
	Worker  string `json:"worker"`
	Model   string `json:"model,omitempty"`
	// Prompt is the PromptVersion that asked: an author comparing two answers across
	// an upgrade can tell whether the prompt changed between them.
	Prompt string `json:"prompt"`
}

// Capability is what the console asks before it offers to write anything.
type Capability struct {
	Available bool     `json:"available"`
	Workers   []Worker `json:"workers"`
}

// Service writes FEEL expressions. Build it with [New].
type Service struct {
	// workers lists the agent Workers a request may name; dial resolves one into
	// something that can be asked, its credential read from the vault. Both are the
	// server's, which applies the single writer and the scopes (ADR-0260).
	workers func(r *http.Request) ([]Worker, error)
	dial    func(r *http.Request, worker string) (agent.Model, error)

	timeout  time.Duration
	attempts int
	// now is the clock an Outcome's duration is read from; tests fix it.
	now func() time.Time

	// Observe, when set, is told what every request that reached a model came to
	// (Outcome). The server logs it and counts it; nil measures nothing.
	Observe func(Outcome)

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own (ADR-0291).
	Limits limits.Limits
}

// New builds the service over the server's two collaborators.
func New(workers func(*http.Request) ([]Worker, error), dial func(*http.Request, string) (agent.Model, error)) *Service {
	return &Service{
		workers: workers, dial: dial, timeout: defaultTimeout, attempts: defaultAttempts,
		now: time.Now, Limits: limits.Default(),
	}
}

// errNoWorker is the "not configured" state, told apart because its remedy is a
// different screen: an operator adds an AI Worker in the Console.
var errNoWorker = errors.New("no AI Worker is configured; add one under Workers to use the FEEL assistant")

// Generate answers the author's last message.
//
// The model call happens here, on the caller's goroutine, under the request's own
// context — an author who closes the assistant takes their request with them, and a
// model endpoint that hangs costs one request rather than the engine (ADR-0260). status
// is the HTTP status an error should reach the author as.
func (s *Service) Generate(r *http.Request, req Request) (Response, int, error) {
	turns, err := conversation(req.Messages)
	if err != nil {
		return Response{}, http.StatusBadRequest, err
	}
	authorVars, err := testVariables(req.Variables)
	if err != nil {
		return Response{}, http.StatusBadRequest, err
	}
	worker, status, err := s.pick(r, req.Worker)
	if err != nil {
		return Response{}, status, err
	}
	model, err := s.dial(r, worker.Name)
	if err != nil {
		return Response{}, http.StatusBadGateway, fmt.Errorf("reach the AI Worker %q: %w", worker.Name, err)
	}
	asked := worker.Model
	if chosen := strings.TrimSpace(req.Model); chosen != "" && chosen != worker.Model {
		chooser, ok := model.(agent.ModelChooser)
		if !ok {
			return Response{}, http.StatusBadRequest, fmt.Errorf(
				"the AI Worker %q serves one model and cannot be asked for %q", worker.Name, chosen)
		}
		model, asked = chooser.ForModel(chosen), chosen
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	goal := goalPrompt(turns, req.Expression, strings.TrimSpace(string(req.Variables)), req.Target)
	prompt := goal
	var (
		best     Proposal
		check    Check
		answered bool
		warning  string
		attempts int
	)
	// From here on a model is asked, so whatever happens is a data point about the
	// prompt and the model, and is reported whichever way the request ends.
	outcome := Outcome{Worker: worker.Name, Model: asked, Prompt: PromptVersion}
	started := s.now()
	defer func() {
		if s.Observe == nil {
			return
		}
		outcome.Duration = s.now().Sub(started)
		s.Observe(outcome)
	}()
	for attempts < s.attempts {
		attempts++
		decision, err := model.Decide(ctx, agent.Request{System: systemPrompt(), Goal: prompt, Round: 1})
		if err != nil {
			outcome.Attempts = append(outcome.Attempts, unusable(err))
			if !answered {
				// The adapter's message carries the endpoint's status, which decides
				// what an operator does about it: 401 is the credential, 429 capacity.
				outcome.Result = ResultUnanswered
				return Response{}, http.StatusBadGateway, fmt.Errorf("the AI Worker %q could not answer: %w", worker.Name, err)
			}
			// A correction round failed. The answer before it is still an answer, and
			// on a rate-limited free model the second call is the likely one to fail.
			warning = fmt.Sprintf("the correction round could not run: %v", err)
			outcome.Result = ResultCutShort
			break
		}
		answer := answerText(decision)
		p, perr := ParseAnswer(answer, s.budgets().Request)
		if perr != nil {
			outcome.Attempts = append(outcome.Attempts, unusable(perr))
			if !answered {
				outcome.Result = ResultUnanswered
				return Response{}, http.StatusBadGateway, fmt.Errorf("the AI Worker %q answered with nothing usable: %w", worker.Name, perr)
			}
			warning = fmt.Sprintf("the correction round answered with nothing usable: %v", perr)
			outcome.Result = ResultCutShort
			break
		}
		vars := p.Variables
		if vars == nil {
			vars = authorVars
		}
		var c Check
		if p.Expression != "" {
			c = Evaluate(p.Expression, vars, p.Expected, p.HasExpected)
		}
		best, check, answered = p, c, true
		if p.Variables == nil {
			best.Variables = authorVars
		}
		outcome.Attempts = append(outcome.Attempts, attemptOf(p, c))
		if settled(p, c) {
			outcome.Result = ResultSettled
			if p.Expression == "" {
				outcome.Result = ResultQuestion
			}
			break
		}
		outcome.Result = ResultUnsettled
		prompt = repairPrompt(goal, answer, problem(p, c))
	}
	return s.response(best, check, attempts, warning, worker.Name, asked), 0, nil
}

// response assembles what the console receives.
func (s *Service) response(p Proposal, c Check, attempts int, warning, worker, model string) Response {
	resp := Response{
		Expression: p.Expression, Explanation: p.Explanation,
		Attempts: attempts, Warning: warning, Worker: worker, Model: model, Prompt: PromptVersion,
	}
	if p.Expression != "" {
		resp.Check = &c
	}
	if p.Variables != nil {
		if b, err := json.Marshal(p.Variables); err == nil {
			resp.Variables = b
		}
	}
	// The reply is the proposal in the contract's own shape, so the model reads its
	// earlier answers in the form it was asked to write them — whatever form it
	// actually wrote them in.
	reply := map[string]any{"expression": p.Expression, "explanation": p.Explanation}
	if p.Variables != nil {
		reply["variables"] = p.Variables
	}
	if p.HasExpected {
		reply["expected"] = p.Expected
	}
	b, _ := json.Marshal(reply)
	resp.Reply = string(b)
	return resp
}

// conversation validates the turns and keeps the most recent maxTurns of them. The last
// turn must be the author's: it is the message being answered.
func conversation(in []Turn) ([]Turn, error) {
	if len(in) == 0 {
		return nil, errors.New("say what the expression should compute")
	}
	out := make([]Turn, 0, len(in))
	for _, t := range in {
		if t.Role != roleUser && t.Role != roleAssistant {
			return nil, fmt.Errorf("a turn's role is %q or %q, not %q", roleUser, roleAssistant, t.Role)
		}
		out = append(out, t)
	}
	last := out[len(out)-1]
	if last.Role != roleUser || strings.TrimSpace(last.Content) == "" {
		return nil, errors.New("say what the expression should compute")
	}
	if len(out) > maxTurns {
		out = out[len(out)-maxTurns:]
	}
	return out, nil
}

// testVariables reads the author's test variables, which must be a JSON object: they
// bind by name.
func testVariables(raw json.RawMessage) (map[string]any, error) {
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return nil, nil
	}
	vars, ok := decodeExact(raw).(map[string]any)
	if !ok {
		return nil, errors.New("the test variables must be a JSON object, one entry per variable")
	}
	return vars, nil
}

// pick settles which Worker answers. Naming none picks the only one in reach; with
// several it is refused, because choosing a model on somebody's behalf chooses what
// their request costs and how good the answer is (ADR-0260).
func (s *Service) pick(r *http.Request, named string) (Worker, int, error) {
	available, err := s.workers(r)
	if err != nil {
		return Worker{}, http.StatusInternalServerError, fmt.Errorf("read the configured Workers: %w", err)
	}
	if len(available) == 0 {
		return Worker{}, http.StatusConflict, errNoWorker
	}
	if named = strings.TrimSpace(named); named != "" {
		for _, w := range available {
			if w.Name == named {
				return w, 0, nil
			}
		}
		return Worker{}, http.StatusNotFound, fmt.Errorf("no AI Worker named %q", named)
	}
	if len(available) == 1 {
		return available[0], 0, nil
	}
	names := make([]string, 0, len(available))
	for _, w := range available {
		names = append(names, w.Name)
	}
	return Worker{}, http.StatusBadRequest, fmt.Errorf("name which AI Worker to ask: %s", strings.Join(names, ", "))
}

// answerText is the model's words: an adapter offered no tools answers in text, in one
// output variable (connector/agent).
func answerText(d agent.Decision) string {
	for _, out := range d.Outputs {
		if text := strings.TrimSpace(out.Text); text != "" {
			return text
		}
	}
	return ""
}

// budgets is how this service reads a ceiling, defaulting a struct literal's zero
// Limits — every ceiling at zero — to [limits.Default].
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}
