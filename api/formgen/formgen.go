// Package formgen generates a form from a description and from the process it belongs
// to (ADR-0260).
//
// It is design-time authoring and nothing else. There is no instance, no token, no job
// and no event: an author describes the form they want — in prose, or by naming the
// process it starts — a model writes a form-js schema, and that schema arrives in the
// editor as an unsaved proposal the author reads before saving. Nothing here writes to a
// store. ADR-0032 said this about diagrams and it is the same sentence about forms: what
// a model produces is a draft that passes the same gate a hand-written one does.
//
// **It asks the Worker an operator already configured.** An agent Worker (ADR-0255) is a
// Console record holding an endpoint, a wire format, a model name and a vault reference
// to an API key. Generation reaches exactly that record, through exactly the adapters
// the runtime agent uses (connector/agent) — so there is no second place to configure a
// model, no second credential to rotate, and no key in the browser. Which model answers
// a generation is the same question, with the same answer, as which model answers an ai
// task.
//
// **This area owns no state**, which is why it holds no run loop where every other
// service under ADR-0147 holds one. It stores nothing and reads nothing directly: its
// three collaborators are the server's, and each observes the single writer itself (I3).
// What this service does hold is the one thing that must never go near that loop — an
// outbound call to a model endpoint, seconds to minutes long, on the goroutine of the
// request that asked for it.
package formgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/agent"
)

// defaultTimeout bounds one generation. Long enough for a considered answer from a
// reasoning model, short enough that an author who is watching a spinner learns
// something before they give up on it.
const defaultTimeout = 90 * time.Second

// Worker is one agent Worker as this area needs to speak about it: the name a request
// names it by, and what it is configured to ask. No endpoint and nowhere to put a
// credential — the same property connector/agent's Task has, and for the same reason
// (ADR-0041/0069).
type Worker struct {
	Name string `json:"name"`
	// Model is the language model the Worker is configured for, empty when it is
	// whatever its protocol defaults to. It is shown to the author because it is the
	// one thing about an AI Worker that changes what comes back.
	Model string `json:"model,omitempty"`
	// Provider is the wire format ("messages", "chat-completions"), carried so the
	// picker can tell two Workers apart when their names do not.
	Provider string `json:"provider,omitempty"`
}

// Source is a process's BPMN as design-time state holds it: a draft under the author's
// hands, or the version currently deployed. Which of the two it came from is carried
// because it is worth saying — a form generated against a draft was generated against a
// process that is not running yet.
type Source struct {
	ProcessID string
	Name      string
	XML       string
	// Origin is "draft" or "deployment".
	Origin string
}

// Request is what an author asks for. Everything but the description is optional, and a
// request with only a process id is a complete one: "the form that starts this" says
// enough.
type Request struct {
	// Description is the author's own brief, in their own language.
	Description string `json:"description"`
	// Worker names the agent Worker to ask. Empty picks the only one in reach, and is
	// refused when there are several — choosing a model for somebody is choosing what
	// their form costs.
	Worker string `json:"worker,omitempty"`
	// Model overrides the language model that Worker is configured for, exactly as a
	// task may (ADR-0256): one Worker, one credential, a cheap model for a short form
	// and a strong one for a hard one.
	Model string `json:"model,omitempty"`
	// ProcessID names the process the form belongs to. Its draft is read if there is
	// one, otherwise its deployed version.
	ProcessID string `json:"processId,omitempty"`
	// ElementID names the step the form is for. Empty is the start-form case: the form
	// starts the process rather than completing a step in it.
	ElementID string `json:"elementId,omitempty"`
	// FormID is the id the editor is holding this form under, stamped into the result
	// so a generation cannot rename a form a user task binds (ADR-0222).
	FormID string `json:"formId,omitempty"`
	// Schema is the form as it stands, present when this is a refinement. The model is
	// shown it and asked to return the whole document.
	Schema json.RawMessage `json:"schema,omitempty"`
}

// Response is the proposal. It is not saved anywhere: the author reads it in the editor
// and saves it themselves, or does not.
type Response struct {
	Schema map[string]any `json:"schema"`
	// Worker and Model say who answered. An author comparing two attempts needs to
	// know which model wrote which, and it is the first thing to check when a
	// generation comes back poor.
	Worker string `json:"worker"`
	Model  string `json:"model,omitempty"`
	// ProcessID, ElementID and ProcessSource echo the context that was actually read,
	// so a result that ignored the process the author thought they had named says so.
	ProcessID     string `json:"processId,omitempty"`
	ElementID     string `json:"elementId,omitempty"`
	ProcessSource string `json:"processSource,omitempty"`
}

// Capability is what the editor asks before it offers the affordance at all. A button
// that produces "no AI Worker is configured" is a button that should not have been
// there.
type Capability struct {
	Available bool     `json:"available"`
	Workers   []Worker `json:"workers"`
}

// Service generates forms. Build it with [New].
type Service struct {
	// workers lists the agent Workers this request's principal may use. Scope is the
	// server's to apply, not this package's: a Worker is a shared artifact with an
	// owner and members like any other (ADR-0205).
	workers func(r *http.Request) ([]Worker, error)
	// dial resolves one named agent Worker into something that can be asked — the
	// adapter for its wire format, its endpoint, and its credential read from the
	// vault. The credential never comes back out of here, which is why this returns a
	// [agent.Model] and not a configuration.
	dial func(r *http.Request, worker string) (agent.Model, error)
	// source reads a process's BPMN, applying the same scope the Modeler applies to
	// opening it. A process the principal may not see reads as absent.
	source func(r *http.Request, processID string) (Source, bool, error)
	// timeout bounds one call to a model.
	timeout time.Duration
}

// New builds the generation service. All three collaborators are the server's, and each
// reaches shared state through the run loop itself — this service holds none of it (see
// the package comment).
func New(workers func(*http.Request) ([]Worker, error),
	dial func(*http.Request, string) (agent.Model, error),
	source func(*http.Request, string) (Source, bool, error)) *Service {
	return &Service{workers: workers, dial: dial, source: source, timeout: defaultTimeout}
}

// errNoWorker is the "not configured" state, told apart from every other failure because
// its remedy is a different screen: an operator adds an AI Worker in the Console.
var errNoWorker = errors.New("no AI Worker is configured; add one under Workers to generate forms")

// Generate runs one generation and returns the proposal. status is the HTTP status the
// error should reach the author as; it is 0 when there is no error.
//
// The model call happens here, on the caller's goroutine, under the request's own
// context — so an author who navigates away takes their generation with them, and a
// model endpoint that hangs costs one request rather than the engine.
func (s *Service) Generate(r *http.Request, req Request) (Response, int, error) {
	if strings.TrimSpace(req.Description) == "" && strings.TrimSpace(req.ProcessID) == "" {
		return Response{}, http.StatusBadRequest,
			errors.New("say what the form is for, or name the process it belongs to")
	}
	worker, status, err := s.pick(r, req.Worker)
	if err != nil {
		return Response{}, status, err
	}
	var (
		process Process
		src     Source
	)
	if id := strings.TrimSpace(req.ProcessID); id != "" {
		found, ok, err := s.source(r, id)
		if err != nil {
			return Response{}, http.StatusInternalServerError, fmt.Errorf("read process: %w", err)
		}
		if !ok {
			return Response{}, http.StatusNotFound, fmt.Errorf("no draft or deployed process with the id %q", id)
		}
		src = found
		process = ReadProcess([]byte(found.XML))
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
	decision, err := model.Decide(ctx, agent.Request{
		System: systemPrompt(),
		Goal:   goalPrompt(req, process, currentSchema(req.Schema)),
		Round:  1,
	})
	if err != nil {
		// The adapter's message carries the endpoint's status, which is what decides
		// what an operator does about it: 401 is the credential, 429 is capacity.
		return Response{}, http.StatusBadGateway, fmt.Errorf("the AI Worker %q could not answer: %w", worker.Name, err)
	}
	answer := answerText(decision)
	if answer == "" {
		return Response{}, http.StatusBadGateway,
			fmt.Errorf("the AI Worker %q answered with nothing", worker.Name)
	}
	schema, err := SchemaFrom(answer, strings.TrimSpace(req.FormID))
	if err != nil {
		// The model answered, and its answer was not a form. That is not a server
		// failure and not the author's mistake either — it is the one outcome this
		// feature has that neither party can prevent, so it is reported as what it is.
		return Response{}, http.StatusUnprocessableEntity, err
	}
	return Response{
		Schema: schema, Worker: worker.Name, Model: asked,
		ProcessID: src.ProcessID, ElementID: strings.TrimSpace(req.ElementID), ProcessSource: src.Origin,
	}, 0, nil
}

// pick settles which Worker answers. Naming none picks the only one in reach; with
// several it is refused, because choosing a model on somebody's behalf chooses what
// their generation costs and how good it is.
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

// answerText is the model's words. An adapter offered no tools answers in text and the
// shared ending puts it in one output variable (connector/agent), so this reads that one
// — and reads nothing at all out of a decision that called tools it was never given.
func answerText(d agent.Decision) string {
	for _, out := range d.Outputs {
		if text := strings.TrimSpace(out.Text); text != "" {
			return text
		}
	}
	return ""
}

// currentSchema renders the form being refined for the prompt, and drops it when it is
// absent or unreadable — a refinement that cannot show what it is refining is simply a
// generation.
func currentSchema(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return ""
	}
	out, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return ""
	}
	return string(out)
}
