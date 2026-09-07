package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

// captured is one request the fake endpoint received, decoded far enough to assert on.
type captured struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	System    string `json:"system"`
	Thinking  struct {
		Type string `json:"type"`
	} `json:"thinking"`
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	} `json:"tools"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// endpoint stands in for the Messages API: it records what it was sent and answers with
// the canned body the test gives it. No network, no key, no vendor library.
func endpoint(t *testing.T, status int, answer string) (*httptest.Server, *[]captured, *http.Header) {
	t.Helper()
	var seen []captured
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		var c captured
		if err := json.Unmarshal(body, &c); err != nil {
			t.Errorf("request is not JSON: %v", err)
		}
		seen = append(seen, c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &headers
}

func requestWithOneTool() agent.Request {
	return agent.Request{
		Goal:  "Ermittle die aktuellen Zinssätze.",
		Round: 1,
		Tools: []agent.Tool{{
			Name:        "zinsen_holen",
			Description: "Liest die Zinstabelle einer Bank.",
			Params: []agent.Param{
				{Name: "url", Type: "string", Description: "Die Zinsseite", Required: true},
				{Name: "maxRows", Type: "number"},
			},
		}},
	}
}

// TestHTTPModelSendsTheToolboxAsSchemas: the container's tools reach the endpoint as tool
// definitions — names, the modeler's documentation, and a JSON Schema built from the
// declared parameters, with only the required ones marked required.
func TestHTTPModelSendsTheToolboxAsSchemas(t *testing.T) {
	srv, seen, headers := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"fertig"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	if _, err := m.Decide(context.Background(), requestWithOneTool()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.Model != agent.DefaultModel {
		t.Errorf("model = %q, want %q", req.Model, agent.DefaultModel)
	}
	if req.Thinking.Type != "adaptive" {
		t.Errorf("thinking = %q, want adaptive", req.Thinking.Type)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(req.Tools))
	}
	tool := req.Tools[0]
	if tool.Name != "zinsen_holen" || tool.Description != "Liest die Zinstabelle einer Bank." {
		t.Errorf("tool = %+v, want the element id and its documentation", tool)
	}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if len(props) != 2 {
		t.Fatalf("schema properties = %v, want two", props)
	}
	if tool.InputSchema["additionalProperties"] != false {
		t.Error("schema accepts extra properties; a tool takes what it declared")
	}
	required, _ := tool.InputSchema["required"].([]any)
	if len(required) != 1 || required[0] != "url" {
		t.Errorf("required = %v, want only url — required is opt-in", required)
	}
	// The round's prompt carries the goal, and the headers carry the pinned version.
	if !strings.Contains(req.Messages[0].Content, "Ermittle die aktuellen Zinssätze.") {
		t.Errorf("prompt = %q, want the goal", req.Messages[0].Content)
	}
	if got := headers.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want the pinned 2023-06-01", got)
	}
	if got := headers.Get("x-api-key"); got != "k" {
		t.Errorf("x-api-key = %q, want the configured key", got)
	}
}

// TestHTTPModelReadsToolCalls: tool_use blocks become the round's tool calls, and the
// call's JSON input becomes typed variables — numbers as numbers, booleans as booleans —
// so the activated activity's FEEL compares them as what they are.
func TestHTTPModelReadsToolCalls(t *testing.T) {
	const answer = `{"stop_reason":"tool_use","content":[
		{"type":"text","text":"Ich sehe nach."},
		{"type":"tool_use","id":"toolu_1","name":"zinsen_holen",
		 "input":{"url":"https://example.ch","maxRows":20,"strict":true,"filter":{"laufzeit":10}}}]}`
	srv, _, _ := endpoint(t, http.StatusOK, answer)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	d, err := m.Decide(context.Background(), requestWithOneTool())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(d.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(d.ToolCalls))
	}
	call := d.ToolCalls[0]
	if call.Tool != "zinsen_holen" || call.CallId != "toolu_1" {
		t.Errorf("call = %+v, want the tool name and the model's call id", call)
	}
	if len(d.Outputs) != 0 {
		t.Error("a round with tool calls also produced outputs; the text is thinking out loud")
	}
	byName := map[string]model.VariableValue{}
	for _, v := range call.Arguments {
		byName[v.Name] = v
	}
	if v := byName["url"]; v.Kind != model.VarString || v.Text != "https://example.ch" {
		t.Errorf("url = %+v, want a string", v)
	}
	if v := byName["maxRows"]; v.Kind != model.VarNumber || v.Text != "20" {
		t.Errorf("maxRows = %+v, want the number 20", v)
	}
	if v := byName["strict"]; v.Kind != model.VarBool || !v.Bool {
		t.Errorf("strict = %+v, want true as a boolean", v)
	}
	if v := byName["filter"]; v.Kind != model.VarJSON {
		t.Errorf("filter = %+v, want JSON for a structured argument", v)
	}
}

// TestHTTPModelReadsAnAnswer: no tool calls means the agent is done, and its text becomes
// the output variable the process carries on with.
func TestHTTPModelReadsAnAnswer(t *testing.T) {
	srv, _, _ := endpoint(t, http.StatusOK,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"  10 Jahre: 1.33 %  "}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client(), AnswerVariable: "befund"}

	d, err := m.Decide(context.Background(), requestWithOneTool())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(d.ToolCalls) != 0 {
		t.Fatalf("tool calls = %v, want none", d.ToolCalls)
	}
	if len(d.Outputs) != 1 || d.Outputs[0].Name != "befund" || d.Outputs[0].Text != "10 Jahre: 1.33 %" {
		t.Errorf("outputs = %+v, want the trimmed answer under the configured name", d.Outputs)
	}
}

// TestHTTPModelCarriesEarlierResults: round two tells the model what its earlier calls
// returned. The worker holds nothing between rounds, so this is the whole of the agent's
// memory — if it is not in the request, the model does not have it.
func TestHTTPModelCarriesEarlierResults(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"fertig"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	req := requestWithOneTool()
	req.Round = 2
	req.Results = []string{"1.33 %"}
	req.Context = map[string]string{"bank": "Migros Bank"}
	if _, err := m.Decide(context.Background(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	prompt := (*seen)[0].Messages[0].Content
	for _, want := range []string{"round 2", "1.33 %", "Migros Bank"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q, want it to carry %q", prompt, want)
		}
	}
}

// TestHTTPModelFailsLoudly: an endpoint that refuses, and a model that answers with
// neither a call nor words, both have to fail the round. Returning an empty decision
// would end the agent's run as if it had finished — silently, and with nothing to show.
func TestHTTPModelFailsLoudly(t *testing.T) {
	t.Run("http error carries the status", func(t *testing.T) {
		srv, _, _ := endpoint(t, http.StatusUnauthorized, `{"error":{"message":"invalid x-api-key"}}`)
		m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "wrong", Client: srv.Client()}
		_, err := m.Decide(context.Background(), requestWithOneTool())
		if err == nil {
			t.Fatal("Decide succeeded on a 401")
		}
		if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid x-api-key") {
			t.Errorf("error = %v, want the status and what the endpoint said", err)
		}
	})

	t.Run("an empty answer is not an ending", func(t *testing.T) {
		srv, _, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[]}`)
		m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}
		_, err := m.Decide(context.Background(), requestWithOneTool())
		if err == nil {
			t.Fatal("Decide succeeded on an empty answer, which would end the run silently")
		}
	})
}

// TestHTTPModelDescribesAnUndocumentedTool: the compiler warns about a tool with no
// documentation; the model is told too, so it treats it with suspicion rather than
// inventing a purpose for a bare element id.
func TestHTTPModelDescribesAnUndocumentedTool(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"x"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	req := agent.Request{Goal: "g", Round: 1, Tools: []agent.Tool{{Name: "namenlos"}}}
	if _, err := m.Decide(context.Background(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d := (*seen)[0].Tools[0].Description; d == "" || !strings.Contains(d, "no documentation") {
		t.Errorf("description = %q, want it to say the activity carries none", d)
	}
}

// A request offering no tools is an ai task, and it is put to the model as a question
// rather than as a round (ADR-0256).
//
// This is not a cosmetic difference. The round prompt tells the model to choose among
// tools and reports which round it is; handed that with no tools, a model looks for the
// tools — it asks for one, or it hedges about not having any — and either is a worse
// answer than the question deserved. What lands in a process variable is read by a
// program, so the framing says that too.
//
// No tools is unambiguous: the compiler refuses an agent-driven ad-hoc with no contained
// activity (ADR-0253), so a request with an empty toolbox is never a round.
func TestAOneShotCallIsFramedAsAQuestionNotARound(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"Dachsanierung"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	if _, err := m.Decide(context.Background(), agent.Request{Goal: "Klassifiziere: Dachdecker AG", Round: 1}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("saw %d requests, want 1", len(*seen))
	}
	got := (*seen)[0]
	if len(got.Tools) != 0 {
		t.Errorf("tools = %v, want the field omitted entirely for a task", got.Tools)
	}
	if got.Messages[0].Content != "Klassifiziere: Dachdecker AG" {
		t.Errorf("message = %q, want the author's question verbatim with no round bookkeeping", got.Messages[0].Content)
	}
	if strings.Contains(got.System, "tools you are given") {
		t.Errorf("system prompt talks about tools this call has none of:\n%s", got.System)
	}
	if !strings.Contains(got.System, "process variable") {
		t.Errorf("system prompt does not say where the answer goes:\n%s", got.System)
	}

	// And a round still gets the round framing, so the two did not collapse into one.
	if _, err := m.Decide(context.Background(), requestWithOneTool()); err != nil {
		t.Fatalf("Decide (round): %v", err)
	}
	round := (*seen)[1]
	if !strings.Contains(round.System, "tools you are given") {
		t.Errorf("a round's system prompt lost its instruction to choose among tools:\n%s", round.System)
	}
	if !strings.Contains(round.Messages[0].Content, "round 1") {
		t.Errorf("a round's message = %q, want it to say which round this is", round.Messages[0].Content)
	}
}

// The context reaches the model, under the heading the prompt has always had. This is the
// end of the wire the bug was invisible from: the rendering was there, the payload carried
// the field, and the map was empty every time because nothing filled it
// (ADR-0257).
func TestARoundPutsWhatTheProcessKnowsToTheModel(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"fertig"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	req := requestWithOneTool()
	req.Context = map[string]string{"dossier": "Kaufpreis CHF 1'150'000", "fehlt": "(not set)"}
	if _, err := m.Decide(context.Background(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	body := (*seen)[0].Messages[0].Content
	if !strings.Contains(body, "What the process knows") {
		t.Errorf("the round's message has no context section:\n%s", body)
	}
	for _, want := range []string{"dossier: Kaufpreis CHF 1'150'000", "fehlt: (not set)"} {
		if !strings.Contains(body, want) {
			t.Errorf("the round's message does not carry %q:\n%s", want, body)
		}
	}
}

// A caller that is not a running process says so itself. Both standing system prompts
// open by telling the model it is "one step inside a running business process", which is
// true of every caller this package had until design-time form generation
// (ADR-draft-ai-form-generation) — and a model told it is inside a process it is not
// inside answers as if it were, asking for the case it is working on. Request.System
// replaces that sentence and nothing else about the call.
func TestAStatedSystemPromptReplacesTheStandingOne(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"{}"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	const own = "You design forms. Answer with one JSON document and nothing else."
	if _, err := m.Decide(context.Background(), agent.Request{Goal: "Ein Urlaubsantrag", System: own, Round: 1}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	got := (*seen)[0]
	if got.System != own {
		t.Errorf("system = %q, want the caller's own prompt verbatim", got.System)
	}
	if got.Messages[0].Content != "Ein Urlaubsantrag" {
		t.Errorf("message = %q, want the goal unchanged by the override", got.Messages[0].Content)
	}

	// Stating none still gets the standing prompt: this is an override, not a
	// requirement every caller now has to meet.
	if _, err := m.Decide(context.Background(), agent.Request{Goal: "Klassifiziere", Round: 1}); err != nil {
		t.Fatalf("Decide (no override): %v", err)
	}
	if !strings.Contains((*seen)[1].System, "running business process") {
		t.Errorf("a request that states no system prompt lost the standing one:\n%s", (*seen)[1].System)
	}
}
