package formgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

// scripted stands in for a model endpoint. It records what it was asked, which is where
// most of the assertions below actually live: what reaches the model is the whole of
// what this package does.
type scripted struct {
	seen   []agent.Request
	answer string
	err    error
	// asked is the language model ForModel was called with, empty when it never was.
	asked string
}

func (s *scripted) Decide(_ context.Context, req agent.Request) (agent.Decision, error) {
	s.seen = append(s.seen, req)
	if s.err != nil {
		return agent.Decision{}, s.err
	}
	return agent.Decision{Outputs: []model.VariableValue{
		{Name: "answer", Kind: model.VarString, Text: s.answer},
	}}, nil
}

// chooser is a scripted model that can be asked for another language model, as both
// shipped adapters can (ADR-0256).
type chooser struct{ *scripted }

func (c chooser) ForModel(id string) agent.Model {
	c.scripted.asked = id
	return c.scripted
}

const okAnswer = `{"type":"default","components":[{"type":"textfield","key":"grund","label":"Grund"}]}`

// harness builds a service over a scripted model and whatever design-time state the test
// needs, and hands back the pieces every assertion reaches for.
type harness struct {
	svc   *Service
	model *scripted
}

func newHarness(t *testing.T, workers []Worker, m agent.Model, sources map[string]Source) harness {
	t.Helper()
	script, _ := m.(*scripted)
	if c, ok := m.(chooser); ok {
		script = c.scripted
	}
	svc := New(
		func(*http.Request) ([]Worker, error) { return workers, nil },
		func(_ *http.Request, name string) (agent.Model, error) {
			for _, w := range workers {
				if w.Name == name {
					return m, nil
				}
			}
			return nil, fmt.Errorf("no worker %q", name)
		},
		func(_ *http.Request, id string) (Source, bool, error) {
			src, ok := sources[id]
			return src, ok, nil
		},
	)
	return harness{svc: svc, model: script}
}

// oneWorker is the ordinary installation: an operator added one AI Worker in the Console
// and every generation goes to it without anybody choosing.
func oneWorker() []Worker {
	return []Worker{{Name: "anthropic_pb", Model: "claude-opus-5", Provider: "messages"}}
}

func post(t *testing.T, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/v1/forms/generate", strings.NewReader(string(raw)))
}

// The whole feature in one pass: prose in, a form out, nothing stored, and the answer
// says which model wrote it.
func TestProseBecomesAForm(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	got, status, err := h.svc.Generate(post(t, Request{}), Request{
		Description: "Ein Antrag auf Sonderurlaub mit Grund und Zeitraum.",
		FormID:      "sonderurlaub",
	})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if got.Worker != "anthropic_pb" || got.Model != "claude-opus-5" {
		t.Errorf("answer does not say who wrote it: %+v", got)
	}
	if got.Schema["id"] != "sonderurlaub" {
		t.Errorf("schema id = %v, want the id the editor is holding", got.Schema["id"])
	}
	if len(h.model.seen) != 1 {
		t.Fatalf("asked the model %d times, want 1", len(h.model.seen))
	}
	asked := h.model.seen[0]
	if !strings.Contains(asked.Goal, "Sonderurlaub") {
		t.Errorf("the author's brief did not reach the model:\n%s", asked.Goal)
	}
	if len(asked.Tools) != 0 {
		t.Errorf("tools = %v; generation offers none — there is nothing here to call", asked.Tools)
	}
	if !strings.Contains(asked.System, "form-js") {
		t.Errorf("the model was not told what to answer with:\n%s", asked.System)
	}
	if strings.Contains(asked.System, "running business process") {
		t.Error("the model was told it is inside a running instance; it is on an authoring screen")
	}
}

// Naming a process is what turns "a form" into "the form for this process", and it is
// the half of the request the author does not have to type.
func TestTheProcessTheFormBelongsToReachesTheModel(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, map[string]Source{
		"urlaubsantrag": {ProcessID: "urlaubsantrag", Name: "Urlaubsantrag", XML: sampleBPMN, Origin: "draft"},
	})

	got, status, err := h.svc.Generate(post(t, Request{}), Request{
		ProcessID: "urlaubsantrag", ElementID: "Task_Pruefen",
	})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if got.ProcessID != "urlaubsantrag" || got.ProcessSource != "draft" || got.ElementID != "Task_Pruefen" {
		t.Errorf("the answer does not say what it was generated against: %+v", got)
	}
	goal := h.model.seen[0].Goal
	for _, want := range []string{"Antrag prüfen", "Führungskraft entscheidet", "entscheidung"} {
		if !strings.Contains(goal, want) {
			t.Errorf("the process did not reach the model — %q missing:\n%s", want, goal)
		}
	}
}

// A process id that names nothing is the author's typo or a draft somebody deleted, and
// it must not silently become a generation with no context at all: the form that comes
// back would look fine and be about nothing.
func TestAnUnknownProcessIsRefusedRatherThanIgnored(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{ProcessID: "gibtsnicht"})
	if err == nil || status != http.StatusNotFound {
		t.Fatalf("status = %d, err = %v, want 404", status, err)
	}
	if len(h.model.seen) != 0 {
		t.Error("the model was asked anyway")
	}
}

// A request with neither a brief nor a process says nothing at all, and asking a model
// to write "a form" would spend somebody's money on a guess.
func TestARequestWithNothingInItIsRefused(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "   "})
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("status = %d, err = %v, want 400", status, err)
	}
	if len(h.model.seen) != 0 {
		t.Error("the model was asked anyway")
	}
}

// The remedy for "no AI Worker" is a different screen — an operator adds one in the
// Console — so it is its own status and its own sentence, not a 500.
func TestWithNoAIWorkerTheFeatureSaysSo(t *testing.T) {
	h := newHarness(t, nil, &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "Ein Formular."})
	if status != http.StatusConflict || err == nil {
		t.Fatalf("status = %d, err = %v, want 409", status, err)
	}
	if !strings.Contains(err.Error(), "Workers") {
		t.Errorf("err = %v, want it to say where to add one", err)
	}
}

// Two Workers is two models, two prices and two answers. Picking one for the author
// would be choosing what their generation costs, so it is asked rather than assumed.
func TestWithSeveralAIWorkersTheAuthorChooses(t *testing.T) {
	workers := []Worker{{Name: "anthropic_pb", Model: "claude-opus-5"}, {Name: "lokal", Model: "qwen3"}}
	h := newHarness(t, workers, &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "Ein Formular."})
	if status != http.StatusBadRequest || err == nil {
		t.Fatalf("status = %d, err = %v, want 400", status, err)
	}
	if !strings.Contains(err.Error(), "lokal") {
		t.Errorf("err = %v, want it to list what there is to choose from", err)
	}

	// Naming one gets on with it.
	got, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "Ein Formular.", Worker: "lokal"})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if got.Worker != "lokal" || got.Model != "qwen3" {
		t.Errorf("answer = %+v, want the Worker the author named", got)
	}
}

// A Worker that is not in the list is not in reach, whether it never existed or belongs
// to somebody else (ADR-0205). Both read the same way from here.
func TestAnUnknownAIWorkerIsRefused(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x", Worker: "fremd"})
	if status != http.StatusNotFound || err == nil {
		t.Fatalf("status = %d, err = %v, want 404", status, err)
	}
}

// One Worker, one credential, and a cheap model for a short form: naming a model is the
// authoring decision ADR-0256 separated from the provider, and it is the same decision
// here as on a task.
func TestTheAuthorMayNameTheModel(t *testing.T) {
	script := &scripted{answer: okAnswer}
	h := newHarness(t, oneWorker(), chooser{script}, nil)

	got, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x", Model: "claude-haiku-4-5"})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if script.asked != "claude-haiku-4-5" {
		t.Errorf("ForModel called with %q, want the model the author named", script.asked)
	}
	if got.Model != "claude-haiku-4-5" {
		t.Errorf("answer says %q wrote it, want the model actually asked", got.Model)
	}
}

// An adapter that genuinely serves one model — a local runtime with one file loaded —
// declines by not implementing the choice, and must be told to decline rather than
// quietly answering from a different model than the author asked for (ADR-0256).
func TestAWorkerThatServesOneModelSaysSo(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x", Model: "gpt-4o"})
	if status != http.StatusBadRequest || err == nil {
		t.Fatalf("status = %d, err = %v, want 400", status, err)
	}
	if len(h.model.seen) != 0 {
		t.Error("it asked anyway, from a model the author did not choose")
	}
}

// An endpoint that refuses is not this server's failure, and the status it refused with
// is what decides what an operator does about it — so the message carries it out.
func TestAnEndpointThatRefusesIsABadGateway(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{err: errors.New("model endpoint returned 401: invalid x-api-key")}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x"})
	if status != http.StatusBadGateway || err == nil {
		t.Fatalf("status = %d, err = %v, want 502", status, err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the endpoint's own status in it", err)
	}
}

// The model answered and its answer was not a form. Neither party could have prevented
// it, so it is reported as itself — with the reason, so the author can decide whether to
// rephrase or simply try again.
func TestAnAnswerThatIsNotAFormIsReportedAsSuch(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: "Ich kann das leider nicht."}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x"})
	if status != http.StatusUnprocessableEntity || err == nil {
		t.Fatalf("status = %d, err = %v, want 422", status, err)
	}
}

// A model that answers with neither a form nor words has ended the request with nothing,
// which is indistinguishable from success until somebody looks at the empty editor.
func TestAnEmptyAnswerIsAFailure(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: ""}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{Description: "x"})
	if status != http.StatusBadGateway || err == nil {
		t.Fatalf("status = %d, err = %v, want 502", status, err)
	}
}

// Refining is the normal second step, and it only works if the model sees what it is
// changing — otherwise "add a date field" returns a form with one field in it.
func TestARefinementCarriesTheFormItIsRefining(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{
		Description: "Füge ein Feld für den Zeitraum hinzu.",
		Schema:      json.RawMessage(`{"type":"default","components":[{"type":"textfield","key":"grund"}]}`),
	})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if !strings.Contains(h.model.seen[0].Goal, `"grund"`) {
		t.Errorf("the form being refined did not reach the model:\n%s", h.model.seen[0].Goal)
	}
}

// The editor asks this before it offers the affordance at all: a button whose only
// possible outcome is "not configured" teaches an author that the feature is broken.
func TestCapabilitySaysWhetherThereIsAnythingToAsk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		workers []Worker
		want    bool
	}{
		{"configured", oneWorker(), true},
		{"none", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.workers, &scripted{}, nil)
			rec := httptest.NewRecorder()
			h.svc.HandleCapability(rec, httptest.NewRequest(http.MethodGet, "/api/v1/forms/generate", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var got Capability
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body)
			}
			if got.Available != tc.want {
				t.Errorf("available = %v, want %v", got.Available, tc.want)
			}
			if got.Workers == nil {
				t.Error("workers is null; the picker would have to guard for it")
			}
		})
	}
}

// The handler is the thin half, and its own failures are its own: a body that is not
// JSON never reaches a model endpoint.
func TestTheHandlerRefusesRubbishBeforeSpendingAnything(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)
	rec := httptest.NewRecorder()
	h.svc.HandleGenerate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/forms/generate", strings.NewReader("nope")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(h.model.seen) != 0 {
		t.Error("the model was asked with an unreadable request")
	}
}

// The generated form travels as JSON and lands in an editor, so the handler's own round
// trip is worth one test of its own.
func TestTheHandlerReturnsTheProposal(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)
	rec := httptest.NewRecorder()
	h.svc.HandleGenerate(rec, post(t, Request{Description: "Ein Formular für den Urlaubsantrag.", FormID: "urlaub"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got Response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if got.Schema["id"] != "urlaub" || got.Worker != "anthropic_pb" {
		t.Errorf("response = %+v", got)
	}
}

// A failure that came from the service reaches the author as the sentence the service
// wrote, under the status it chose.
func TestTheHandlerCarriesTheServicesOwnStatus(t *testing.T) {
	h := newHarness(t, nil, &scripted{}, nil)
	rec := httptest.NewRecorder()
	h.svc.HandleGenerate(rec, post(t, Request{Description: "x"}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AI Worker") {
		t.Errorf("body = %s", rec.Body)
	}
}

// Reading the Workers can fail — the store is on disk — and that is a server failure
// rather than a request the author got wrong.
func TestAStoreFailureIsTheServersOwn(t *testing.T) {
	svc := New(
		func(*http.Request) ([]Worker, error) { return nil, errors.New("disk gone") },
		func(*http.Request, string) (agent.Model, error) { return nil, nil },
		func(*http.Request, string) (Source, bool, error) { return Source{}, false, nil },
	)
	if _, status, err := svc.Generate(post(t, Request{}), Request{Description: "x"}); status != http.StatusInternalServerError || err == nil {
		t.Fatalf("status = %d, err = %v, want 500", status, err)
	}
	rec := httptest.NewRecorder()
	svc.HandleCapability(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("capability status = %d, want 500", rec.Code)
	}
}

// Reading the process can fail the same way, and it is worth its own line because the
// alternative — treating a read error as "no such process" — would tell an author their
// draft is gone when the disk is merely busy.
func TestAProcessReadFailureIsNotAMissingProcess(t *testing.T) {
	svc := New(
		func(*http.Request) ([]Worker, error) { return oneWorker(), nil },
		func(*http.Request, string) (agent.Model, error) { return &scripted{answer: okAnswer}, nil },
		func(*http.Request, string) (Source, bool, error) { return Source{}, false, errors.New("disk gone") },
	)
	if _, status, err := svc.Generate(post(t, Request{}), Request{ProcessID: "x"}); status != http.StatusInternalServerError || err == nil {
		t.Fatalf("status = %d, err = %v, want 500", status, err)
	}
}

// A Worker that cannot be reached at all — its vault key is gone, its record names a
// protocol nothing implements — is a gateway failure, not a missing Worker: it is
// configured, and configured wrongly.
func TestAWorkerThatCannotBeDialledIsAGatewayFailure(t *testing.T) {
	svc := New(
		func(*http.Request) ([]Worker, error) { return oneWorker(), nil },
		func(*http.Request, string) (agent.Model, error) { return nil, errors.New("no credential in the vault") },
		func(*http.Request, string) (Source, bool, error) { return Source{}, false, nil },
	)
	_, status, err := svc.Generate(post(t, Request{}), Request{Description: "x"})
	if status != http.StatusBadGateway || err == nil {
		t.Fatalf("status = %d, err = %v, want 502", status, err)
	}
	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("err = %v, want the reason it could not be reached", err)
	}
}

// A refinement whose current form cannot be read is simply a generation. Failing the
// request over the editor's own state would be refusing to help because of something the
// author cannot see or fix.
func TestARefinementWithAnUnreadableFormIsStillAGeneration(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)

	_, status, err := h.svc.Generate(post(t, Request{}), Request{
		Description: "Ein Formular.", Schema: json.RawMessage(`{"components":`),
	})
	if err != nil {
		t.Fatalf("Generate: %d %v", status, err)
	}
	if strings.Contains(h.model.seen[0].Goal, "The form as it stands") {
		t.Errorf("an unreadable form was shown to the model anyway:\n%s", h.model.seen[0].Goal)
	}
}

// errReader fails mid-body, the way a client that hung up does.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// A request whose body cannot be read is the author's browser going away, not a
// generation — and certainly not a model call.
func TestABodyThatCannotBeReadCostsNothing(t *testing.T) {
	h := newHarness(t, oneWorker(), &scripted{answer: okAnswer}, nil)
	rec := httptest.NewRecorder()
	h.svc.HandleGenerate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/forms/generate", errReader{}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(h.model.seen) != 0 {
		t.Error("the model was asked with a body nobody could read")
	}
}
