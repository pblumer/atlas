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

// The second provider, tested the way the first one is: against an httptest endpoint,
// with no network, no key and no vendor library anywhere in it.
//
// What is worth testing here is exactly what differs from the Messages format, because
// everything else is shared code — the toolbox, the round prompt, the typed arguments,
// the rule that an answer with nothing in it fails the round. Three things differ, and
// each of them is a way a round could silently do the wrong thing: tools sit under a
// "function" wrapper, the answer arrives in choices, and a call's arguments come back
// as a JSON *string*.

// chatCaptured is one request the fake endpoint received.
type chatCaptured struct {
	Model               string `json:"model"`
	MaxCompletionTokens int    `json:"max_completion_tokens"`
	MaxTokens           int    `json:"max_tokens"`
	Messages            []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

func chatEndpoint(t *testing.T, status int, answer string) (*httptest.Server, *[]chatCaptured, *http.Header) {
	t.Helper()
	var seen []chatCaptured
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		var c chatCaptured
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

func chatRound() agent.Request {
	return agent.Request{
		Goal:  "Finde den günstigsten Zins.",
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

// The request a Chat-Completions endpoint is actually sent: the toolbox under the
// wrapper this format wants, the system prompt as its own turn, and the credential as a
// bearer token — which is the default here and the reason this is a second adapter
// rather than a second endpoint for the first one.
func TestChatCompletionsSendsTheToolboxAsFunctions(t *testing.T) {
	srv, seen, headers := chatEndpoint(t, http.StatusOK,
		`{"choices":[{"finish_reason":"stop","message":{"content":"1.13 %"}}]}`)
	m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk-test", Model: "gpt-4o", Client: srv.Client()}

	if _, err := m.Decide(context.Background(), chatRound()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	got := (*seen)[0]
	if got.Model != "gpt-4o" {
		t.Errorf("model = %q, want the configured one", got.Model)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want a system turn and a user turn", got.Messages)
	}
	if !strings.Contains(got.Messages[1].Content, "Finde den günstigsten Zins.") {
		t.Errorf("user turn = %q, want the container's goal in it", got.Messages[1].Content)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %+v, want the container's one tool", got.Tools)
	}
	tool := got.Tools[0]
	if tool.Type != "function" {
		t.Errorf("tool type = %q, want function", tool.Type)
	}
	if tool.Function.Name != "zinsen_holen" || tool.Function.Description != "Liest die Zinstabelle einer Bank." {
		t.Errorf("function = %+v, want the element id and its documentation", tool.Function)
	}
	// The schema is the same object the Messages adapter sends: one builder, so the
	// two providers cannot come to disagree about what a tool accepts.
	props, _ := tool.Function.Parameters["properties"].(map[string]any)
	if _, ok := props["url"]; !ok {
		t.Errorf("parameters = %v, want the declared url", tool.Function.Parameters)
	}
	if req, _ := tool.Function.Parameters["required"].([]any); len(req) != 1 || req[0] != "url" {
		t.Errorf("required = %v, want only url", tool.Function.Parameters["required"])
	}
	// No cap was configured, so none is sent — under either name. An endpoint that
	// wants one of them and refuses the other is served by leaving it unset.
	if got.MaxCompletionTokens != 0 || got.MaxTokens != 0 {
		t.Errorf("a token cap was sent (%d/%d) though none was configured", got.MaxCompletionTokens, got.MaxTokens)
	}
	if auth := headers.Get("Authorization"); auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want a bearer token: this format's endpoints take the key that way", auth)
	}
	if key := headers.Get("x-api-key"); key != "" {
		t.Errorf("x-api-key = %q, want none: that is the other provider's header", key)
	}
}

// A configured cap travels under the current name. max_tokens is the older one and
// newer models refuse it, so sending the current name is what makes a cap usable at all.
func TestChatCompletionsSendsAConfiguredCapUnderTheCurrentName(t *testing.T) {
	srv, seen, _ := chatEndpoint(t, http.StatusOK,
		`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	m := &agent.ChatCompletionsModel{
		Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", MaxTokens: 2048, Client: srv.Client(),
	}
	if _, err := m.Decide(context.Background(), chatRound()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := (*seen)[0]; got.MaxCompletionTokens != 2048 || got.MaxTokens != 0 {
		t.Errorf("cap sent as max_completion_tokens=%d max_tokens=%d, want only the former",
			got.MaxCompletionTokens, got.MaxTokens)
	}
}

// The decode this adapter exists for: a tool call whose arguments arrive as a JSON
// string, becoming the same typed variables the other provider produces. A number that
// stayed a string here would reach the activated activity's FEEL as text, and every
// comparison on it would quietly be the wrong one (ADR-0037).
func TestChatCompletionsReadsToolCallsWithTypedArguments(t *testing.T) {
	srv, _, _ := chatEndpoint(t, http.StatusOK, `{"choices":[{"finish_reason":"tool_calls","message":{
		"content": null,
		"tool_calls":[{"id":"call_1","type":"function","function":{
			"name":"zinsen_holen",
			"arguments":"{\"url\":\"https://bank.example\",\"maxRows\":25,\"nurAktuelle\":true,\"filter\":{\"kanton\":\"ZH\"}}"
		}}]}}]}`)
	m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}

	decision, err := m.Decide(context.Background(), chatRound())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(decision.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one", decision.ToolCalls)
	}
	call := decision.ToolCalls[0]
	if call.Tool != "zinsen_holen" || call.CallId != "call_1" {
		t.Errorf("call = %+v, want the tool's element id and the model's own call id", call)
	}
	byName := map[string]model.VariableValue{}
	for _, v := range call.Arguments {
		byName[v.Name] = v
	}
	for _, want := range []struct {
		name string
		kind model.VarKind
		text string
	}{
		{"url", model.VarString, "https://bank.example"},
		{"maxRows", model.VarNumber, "25"},
		{"nurAktuelle", model.VarBool, ""},
		{"filter", model.VarJSON, `{"kanton":"ZH"}`},
	} {
		got := byName[want.name]
		if got.Kind != want.kind {
			t.Errorf("%s: kind = %v, want %v", want.name, got.Kind, want.kind)
		}
		if want.text != "" && got.Text != want.text {
			t.Errorf("%s: text = %q, want %q", want.name, got.Text, want.text)
		}
	}
	if !byName["nurAktuelle"].Bool {
		t.Error("nurAktuelle = false, want true")
	}
}

// Some OpenAI-compatible gateways send the arguments as an object rather than the
// string the format specifies. Refusing that would fail a round over an encoding detail
// the model had no part in, so both shapes are read.
func TestChatCompletionsAcceptsArgumentsSentAsAnObject(t *testing.T) {
	srv, _, _ := chatEndpoint(t, http.StatusOK, `{"choices":[{"finish_reason":"tool_calls","message":{
		"tool_calls":[{"id":"c1","type":"function","function":{
			"name":"zinsen_holen","arguments":{"url":"https://bank.example"}}}]}}]}`)
	m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}

	decision, err := m.Decide(context.Background(), chatRound())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(decision.ToolCalls) != 1 || len(decision.ToolCalls[0].Arguments) != 1 {
		t.Fatalf("tool calls = %+v, want one call carrying its url", decision.ToolCalls)
	}
	if got := decision.ToolCalls[0].Arguments[0]; got.Name != "url" || got.Text != "https://bank.example" {
		t.Errorf("argument = %+v, want the url the gateway sent as an object", got)
	}
}

// The ending: no calls, so the text becomes the answer variable and the container
// finishes with something to show for the run.
func TestChatCompletionsAnswersIntoTheAnswerVariable(t *testing.T) {
	srv, _, _ := chatEndpoint(t, http.StatusOK,
		`{"choices":[{"finish_reason":"stop","message":{"content":"  Die Bank ist am günstigsten. "}}]}`)
	m := &agent.ChatCompletionsModel{
		Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o",
		AnswerVariable: "antwort", Client: srv.Client(),
	}

	decision, err := m.Decide(context.Background(), chatRound())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(decision.ToolCalls) != 0 {
		t.Errorf("tool calls = %+v, want none: this round is the ending", decision.ToolCalls)
	}
	if len(decision.Outputs) != 1 {
		t.Fatalf("outputs = %+v, want the answer", decision.Outputs)
	}
	if got := decision.Outputs[0]; got.Name != "antwort" || got.Text != "Die Bank ist am günstigsten." {
		t.Errorf("answer = %+v, want it trimmed under the configured name", got)
	}
}

// Both loud failures, which are loud on purpose. An endpoint that refuses carries its
// status into the message because that is what decides what an operator does about the
// incident; and an answer with neither a call nor words would end the run silently and
// leave the process with nothing.
func TestChatCompletionsFailsLoudly(t *testing.T) {
	t.Run("a refusal carries its status", func(t *testing.T) {
		srv, _, _ := chatEndpoint(t, http.StatusTooManyRequests, `{"error":{"message":"rate limit"}}`)
		m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}
		_, err := m.Decide(context.Background(), chatRound())
		if err == nil || !strings.Contains(err.Error(), "429") {
			t.Errorf("err = %v, want it to name the 429 an operator has to act on", err)
		}
	})
	t.Run("neither a call nor text", func(t *testing.T) {
		srv, _, _ := chatEndpoint(t, http.StatusOK, `{"choices":[{"finish_reason":"length","message":{"content":""}}]}`)
		m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}
		_, err := m.Decide(context.Background(), chatRound())
		if err == nil || !strings.Contains(err.Error(), "length") {
			t.Errorf("err = %v, want a failure naming the stop reason rather than a silent ending", err)
		}
	})
	t.Run("no choices at all", func(t *testing.T) {
		srv, _, _ := chatEndpoint(t, http.StatusOK, `{"choices":[]}`)
		m := &agent.ChatCompletionsModel{Endpoint: srv.URL, APIKey: "sk", Model: "gpt-4o", Client: srv.Client()}
		if _, err := m.Decide(context.Background(), chatRound()); err == nil {
			t.Error("an empty answer succeeded, want a failed round")
		}
	})
	t.Run("no model configured", func(t *testing.T) {
		m := &agent.ChatCompletionsModel{Endpoint: "http://127.0.0.1:1", APIKey: "sk"}
		_, err := m.Decide(context.Background(), chatRound())
		if err == nil || !strings.Contains(err.Error(), "model") {
			t.Errorf("err = %v, want it to say no model is configured — there is no sensible default", err)
		}
	})
}

// The two knobs on the Messages adapter, which exist for a gateway that speaks that
// wire format without behaving quite like its originator — OpenRouter's
// Messages-compatible endpoint being the case that prompted them.
func TestMessagesAdapterTakesABearerTokenAndCanDropThinking(t *testing.T) {
	srv, seen, headers := endpoint(t, http.StatusOK,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"fertig"}]}`)
	m := &agent.HTTPModel{
		Endpoint: srv.URL, APIKey: "sk-router", Model: "anthropic/claude-opus-5",
		Auth: agent.AuthBearer, Thinking: agent.ThinkingOff, Client: srv.Client(),
	}

	if _, err := m.Decide(context.Background(), chatRound()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if auth := headers.Get("Authorization"); auth != "Bearer sk-router" {
		t.Errorf("Authorization = %q, want the bearer token the gateway asked for", auth)
	}
	if key := headers.Get("x-api-key"); key != "" {
		t.Errorf("x-api-key = %q, want none once a bearer token was chosen", key)
	}
	// Still the Messages format — the version header is what says so.
	if v := headers.Get("anthropic-version"); v == "" {
		t.Error("anthropic-version is missing; the wire format did not change, only the credential")
	}
	if got := (*seen)[0].Thinking.Type; got != "" {
		t.Errorf("thinking = %q, want the field omitted so an endpoint without it does not refuse the round", got)
	}
}
