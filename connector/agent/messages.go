package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/model"
)

// This file is the one adapter that knows a model provider's wire format — the Messages
// API, spoken over plain HTTP. It is deliberately *not* a vendor SDK: ADR-0117 keeps
// inference and provider libraries out of the binary, and connector/jira and
// connector/googlesheets speak their services' HTTP the same way. Another provider is
// another file like this one behind the same one-call [Model] interface, not a fork.

const (
	// DefaultEndpoint is where an unconfigured Worker points. An operator overrides it
	// for a gateway, a proxy or a self-hosted deployment; nothing here assumes the
	// default is reachable.
	DefaultEndpoint = "https://api.anthropic.com/v1/messages"
	// DefaultModel is the model a Worker uses unless it names another.
	DefaultModel = "claude-opus-5"
	// apiVersion is the Messages API version header. It is pinned rather than tracked:
	// a wire format that changes under a running engine is the failure this avoids.
	apiVersion = "2023-06-01"
	// DefaultAnswerVariable is where the agent's final answer lands when the Worker
	// names no other variable.
	DefaultAnswerVariable = "agentAnswer"
	// ThinkingOff omits the thinking setting from the request. See [HTTPModel.Thinking].
	ThinkingOff = "off"
)

// HTTPModel satisfies [Model] against a Messages-API endpoint. Endpoint and APIKey come
// from the Worker an operator configured — the key is resolved from the vault at run
// time and never travels in a model (ADR-0041/0069).
type HTTPModel struct {
	Endpoint       string        // "" uses DefaultEndpoint
	APIKey         string        // resolved from the vault by the caller
	Model          string        // "" uses DefaultModel
	MaxTokens      int           // 0 uses 4096
	AnswerVariable string        // "" uses DefaultAnswerVariable
	Timeout        time.Duration // 0 uses 2 minutes
	Client         *http.Client  // nil uses a client with Timeout
	// Auth is how the credential is presented: "" or [AuthAPIKey] for Anthropic's own
	// x-api-key header, [AuthBearer] for Authorization: Bearer. It exists because the
	// Messages format and the credential scheme are separate choices — a gateway can
	// speak this wire format and still want a bearer token, which is exactly what
	// OpenRouter's Messages-compatible endpoint does.
	Auth string
	// Thinking is the extended-thinking setting sent with every round: "" keeps the
	// adaptive default, [ThinkingOff] omits the field entirely. Omitting it is what an
	// endpoint that speaks the Messages format without implementing this extension
	// needs — a request it refuses outright is worse than a round without it.
	Thinking string
}

// systemPrompt is what every round tells the model about the shape of its work. It is
// short on purpose: the specifics — the goal and what each tool is for — come from the
// model of the process, written by the person who modelled it, not from here.
const systemPrompt = `You are one step inside a running business process. Each time you are asked, decide what to do next.

Call one or more of the tools you are given, or answer if the work is done. A tool is an activity in the process; calling it runs that activity for real, and you will see its result the next time you are asked. Do not claim to have done something you did not call a tool for.

Answer only when the goal is met or you cannot get further with the tools you have — and then say plainly what you found and what remains open.`

// Decide asks the model for one round. Every round is a fresh request: the worker holds
// nothing between rounds, so what the model knows of its own run is what the process
// carried for it — the goal, the tools, and the results its earlier calls returned. That
// costs the model's own train of thought between rounds and buys a loop that is durable,
// replayable and interruptible at every step, which is the trade ADR-0253 makes.
func (m *HTTPModel) Decide(ctx context.Context, req Request) (Decision, error) {
	endpoint := m.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	payload, err := postJSON(ctx, m.Client, m.Timeout, endpoint, func(h http.Header) error {
		// Pinned rather than tracked, for the reason apiVersion gives.
		h.Set("anthropic-version", apiVersion)
		return applyAuth(h, m.Auth, m.APIKey)
	}, m.request(req))
	if err != nil {
		return Decision{}, err
	}
	return m.decode(payload)
}

// --- request ---------------------------------------------------------------

type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Tools     []messagesTool   `json:"tools,omitempty"`
	Messages  []messagesTurn   `json:"messages"`
	Thinking  *thinkingSetting `json:"thinking,omitempty"`
}

type thinkingSetting struct {
	Type string `json:"type"`
}

type messagesTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type messagesTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m *HTTPModel) request(req Request) messagesRequest {
	name := m.Model
	if name == "" {
		name = DefaultModel
	}
	maxTokens := m.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return messagesRequest{
		Model:     name,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Tools:     toolSchemas(req.Tools),
		Messages:  []messagesTurn{{Role: "user", Content: roundPrompt(req)}},
		// Adaptive thinking lets the model spend more where a round is hard and less
		// where it is not, which is the shape of agent work. Off is for an endpoint
		// that does not implement it.
		Thinking: m.thinking(),
	}
}

func (m *HTTPModel) thinking() *thinkingSetting {
	switch m.Thinking {
	case ThinkingOff:
		return nil
	case "":
		return &thinkingSetting{Type: "adaptive"}
	default:
		return &thinkingSetting{Type: m.Thinking}
	}
}

// toolSchemas turns the container's tools into the API's tool definitions. The parameter
// declarations become a JSON Schema object with additionalProperties false, so the model
// is told exactly what a tool takes and nothing else is accepted.
func toolSchemas(tools []Tool) []messagesTool {
	out := make([]messagesTool, 0, len(tools))
	for _, t := range tools {
		props := map[string]any{}
		var required []string
		for _, p := range t.Params {
			prop := map[string]any{"type": p.Type}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		description := t.Description
		if description == "" {
			// The compiler warns about this (RuleAgentTool); saying so here too is what
			// the model needs to treat the tool with suspicion rather than confidence.
			description = "(this activity carries no documentation)"
		}
		out = append(out, messagesTool{Name: t.Name, Description: description, InputSchema: schema})
	}
	return out
}

// roundPrompt states the round: the goal, what the process already knows, and what the
// earlier calls returned.
func roundPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("Goal: ")
	if req.Goal != "" {
		b.WriteString(req.Goal)
	} else {
		b.WriteString("(the process states no goal for this step)")
	}
	if len(req.Context) > 0 {
		b.WriteString("\n\nWhat the process knows:")
		for k, v := range req.Context {
			fmt.Fprintf(&b, "\n- %s: %s", k, v)
		}
	}
	if len(req.Results) == 0 {
		b.WriteString("\n\nNothing has been called yet. This is round 1.")
		return b.String()
	}
	fmt.Fprintf(&b, "\n\nThis is round %d. Your earlier tool calls returned:", req.Round)
	for i, r := range req.Results {
		fmt.Fprintf(&b, "\n%d. %s", i+1, r)
	}
	return b.String()
}

// --- response --------------------------------------------------------------

type messagesResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

func (m *HTTPModel) decode(payload []byte) (Decision, error) {
	var resp messagesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return Decision{}, fmt.Errorf("decode response: %w", err)
	}
	var (
		calls []model.ToolCall
		text  strings.Builder
	)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args, err := arguments(block.Input)
			if err != nil {
				return Decision{}, fmt.Errorf("tool call %q: %w", block.Name, err)
			}
			calls = append(calls, model.ToolCall{Tool: block.Name, CallId: block.ID, Arguments: args})
		}
	}
	if len(calls) > 0 {
		// Tool calls are the answer for this round; any text alongside them is the
		// model thinking out loud on its way there, and the process has no use for it.
		return Decision{ToolCalls: calls}, nil
	}
	return answerDecision(m.AnswerVariable, text.String(), resp.StopReason)
}

// arguments turns a tool call's JSON input object into the variables the activated
// activity will see in its own scope. Scalars keep their kind so FEEL compares them as
// numbers and booleans rather than as text; anything structured is carried as JSON, the
// kind the variable store already has for it (ADR-0037).
func arguments(raw json.RawMessage) ([]model.VariableValue, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("input is not an object: %w", err)
	}
	out := make([]model.VariableValue, 0, len(fields))
	for name, value := range fields {
		out = append(out, variableOf(name, value))
	}
	return out, nil
}

func variableOf(name string, raw json.RawMessage) model.VariableValue {
	var any any
	if err := json.Unmarshal(raw, &any); err != nil {
		return model.VariableValue{Name: name, Kind: model.VarJSON, Text: string(raw)}
	}
	switch v := any.(type) {
	case nil:
		return model.VariableValue{Name: name, Kind: model.VarNull}
	case bool:
		return model.VariableValue{Name: name, Kind: model.VarBool, Bool: v}
	case float64:
		return model.VariableValue{Name: name, Kind: model.VarNumber, Text: strconv.FormatFloat(v, 'f', -1, 64)}
	case string:
		return model.VariableValue{Name: name, Kind: model.VarString, Text: v}
	default:
		return model.VariableValue{Name: name, Kind: model.VarJSON, Text: string(raw)}
	}
}
