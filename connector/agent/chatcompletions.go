package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/model"
)

// The second adapter: the Chat Completions format, which is OpenAI's and, through it,
// most of the ecosystem's — OpenRouter, vLLM, llama.cpp, Together and anything else
// that advertises itself as "OpenAI-compatible".
//
// It exists because a wire format is the only real difference between providers, and
// ADR-0117's decision — no provider SDK in the binary — means the way to add one is a
// file like this rather than a dependency. Everything above it is unchanged: the same
// one-call [Model] interface, the same toolbox built from the compiled process, the
// same typed arguments coming back.
//
// Three things differ from the Messages format, and they are the whole of this file:
// tools are declared under a "function" wrapper, the answer arrives in choices rather
// than content blocks, and a tool call's arguments come back as a JSON *string* rather
// than an object.

const (
	// DefaultChatCompletionsEndpoint is OpenAI's own. An operator overrides it for
	// OpenRouter, a gateway or a self-hosted deployment.
	DefaultChatCompletionsEndpoint = "https://api.openai.com/v1/chat/completions"
)

// ChatCompletionsModel satisfies [Model] against a Chat-Completions endpoint.
//
// Unlike [HTTPModel] it has **no default model**. Which model an installation may use
// is a question about that account's access, not something Atlas can know, and a
// default that turns out not to exist would surface as a 404 on the first round of a
// running process rather than as a configuration error at startup.
type ChatCompletionsModel struct {
	Endpoint       string        // "" uses DefaultChatCompletionsEndpoint
	APIKey         string        // resolved from the vault by the caller
	Model          string        // required; there is no sensible default
	MaxTokens      int           // 0 sends no cap at all
	AnswerVariable string        // "" uses DefaultAnswerVariable
	Auth           string        // "" uses AuthBearer
	Timeout        time.Duration // 0 uses 2 minutes
	Client         *http.Client  // nil uses a client with Timeout
}

// Decide asks the model for one round. Like [HTTPModel.Decide] every round is a fresh
// request — the worker holds nothing between rounds, because the process is the loop.
func (m *ChatCompletionsModel) Decide(ctx context.Context, req Request) (Decision, error) {
	if strings.TrimSpace(m.Model) == "" {
		return Decision{}, fmt.Errorf("agent: no model configured for this endpoint; name one")
	}
	endpoint := m.Endpoint
	if endpoint == "" {
		endpoint = DefaultChatCompletionsEndpoint
	}
	auth := m.Auth
	if auth == "" {
		auth = AuthBearer
	}
	payload, err := postJSON(ctx, m.Client, m.Timeout, endpoint, func(h http.Header) error {
		return applyAuth(h, auth, m.APIKey)
	}, m.request(req))
	if err != nil {
		return Decision{}, err
	}
	return m.decode(payload)
}

// --- request ---------------------------------------------------------------

type chatRequest struct {
	Model    string     `json:"model"`
	Messages []chatTurn `json:"messages"`
	Tools    []chatTool `json:"tools,omitempty"`
	// MaxCompletionTokens is the current name for the cap; "max_tokens" is the older
	// one and newer models refuse it. Omitted entirely when no cap is configured,
	// which is the portable answer: an agent round's answer is short, and every
	// endpoint has a default of its own.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

type chatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func (m *ChatCompletionsModel) request(req Request) chatRequest {
	return chatRequest{
		Model: m.Model,
		Messages: []chatTurn{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: roundPrompt(req)},
		},
		Tools:               chatToolSchemas(req.Tools),
		MaxCompletionTokens: m.MaxTokens,
	}
}

// chatToolSchemas is [toolSchemas] in this format's wrapper. The schema itself is the
// same object — one function builds it, so the two providers cannot come to disagree
// about what a container's tools accept.
func chatToolSchemas(tools []Tool) []chatTool {
	out := make([]chatTool, 0, len(tools))
	for _, t := range toolSchemas(tools) {
		out = append(out, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// --- response --------------------------------------------------------------

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
					// Arguments is a JSON *string* in this format, not an object.
					// Some gateways send the object anyway, which is why it is read
					// as raw JSON and unwrapped rather than decoded as a string.
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func (m *ChatCompletionsModel) decode(payload []byte) (Decision, error) {
	var resp chatResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return Decision{}, fmt.Errorf("decode response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Decision{}, fmt.Errorf("the model returned no choices")
	}
	choice := resp.Choices[0]
	var calls []model.ToolCall
	for _, c := range choice.Message.ToolCalls {
		args, err := arguments(unwrapJSONString(c.Function.Arguments))
		if err != nil {
			return Decision{}, fmt.Errorf("tool call %q: %w", c.Function.Name, err)
		}
		calls = append(calls, model.ToolCall{Tool: c.Function.Name, CallId: c.ID, Arguments: args})
	}
	if len(calls) > 0 {
		// Tool calls are the answer for this round; any text alongside them is the
		// model thinking out loud on its way there, and the process has no use for it.
		return Decision{ToolCalls: calls}, nil
	}
	return answerDecision(m.AnswerVariable, choice.Message.Content, choice.FinishReason)
}

// unwrapJSONString takes the arguments as they actually arrive. The format says a JSON
// string carrying JSON, so the usual case is one unquote; an endpoint that sends the
// object directly is passed through untouched rather than refused, because refusing it
// would fail a round over an encoding detail the model had no part in.
func unwrapJSONString(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, `"`) {
		return raw
	}
	var inner string
	if err := json.Unmarshal([]byte(trimmed), &inner); err != nil {
		return raw
	}
	return json.RawMessage(inner)
}
