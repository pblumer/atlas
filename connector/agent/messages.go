package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
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

// roundSystemPrompt is what every round tells the model about the shape of its work. It is
// short on purpose: the specifics — the goal and what each tool is for — come from the
// model of the process, written by the person who modelled it, not from here.
const roundSystemPrompt = `You are one step inside a running business process. Each time you are asked, decide what to do next.

Call one or more of the tools you are given, or answer if the work is done. A tool is an activity in the process; calling it runs that activity for real, and you will see its result the next time you are asked. Do not claim to have done something you did not call a tool for.

Answer only when the goal is met or you cannot get further with the tools you have — and then say plainly what you found and what remains open.`

// taskSystemPrompt is what an ai task says instead: one call, one answer, no tools
// (ADR-0256).
//
// It is a second prompt rather than the same one, because the first describes a situation
// an ai task is not in. Told to choose among tools it has not been given, a model looks
// for them — it asks for one, or it hedges about not having any — and either is a worse
// answer than the question deserved. What lands in the variable is read by the next step
// of a program, so it says that too: the answer, and nothing wrapped around it.
const taskSystemPrompt = `You are one step inside a running business process. Answer the question you are given, using only what it tells you.

Write the answer itself and nothing else: no preamble, no restatement of the question, no offer to do more. What you write is stored in a process variable and read by the next step, which is a program.

If the question cannot be answered from what it gives you, say so plainly in one sentence rather than guessing.`

// systemFor picks between them. No tools is unambiguous: the compiler refuses an
// agent-driven ad-hoc with no contained activity (ADR-0253), so a request offering none is
// an ai task and never a round.
//
// A request that states its own prompt gets that one instead. Both standing prompts
// describe a situation — one step inside a running instance — and a caller that is not in
// it has to be able to say so (see [Request.System]).
func systemFor(req Request) string {
	if s := strings.TrimSpace(req.System); s != "" {
		return s
	}
	if len(req.Tools) == 0 {
		return taskSystemPrompt + outputInstruction(req.Outputs)
	}
	return roundSystemPrompt + outputInstruction(req.Outputs)
}

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
	body := m.request(req)
	payload, err := postJSON(ctx, m.Client, callTimeout(m.Timeout, req), endpoint, func(h http.Header) error {
		// Pinned rather than tracked, for the reason apiVersion gives.
		h.Set("anthropic-version", apiVersion)
		return applyAuth(h, m.Auth, m.APIKey)
	}, body)
	if err != nil {
		return Decision{}, err
	}
	d, err := m.decode(payload)
	if err != nil {
		return Decision{}, err
	}
	d.Call.System, d.Call.Prompt = body.System, m.said(req)
	if err := applyOutputs(req, &d); err != nil {
		return Decision{}, err
	}
	return d, nil
}

// --- request ---------------------------------------------------------------

type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Tools     []messagesTool   `json:"tools,omitempty"`
	Messages  []messagesTurn   `json:"messages"`
	Thinking  *thinkingSetting `json:"thinking,omitempty"`
	// OutputConfig asks for the answer as an object of the declared fields. Sent only
	// when the element declares any, so a request without them is byte for byte what it
	// was before.
	OutputConfig *messagesOutputConfig `json:"output_config,omitempty"`
	// CacheControl lets the provider reuse what it has already read. A run's rounds
	// share their tools, their system prompt and — once replayed — every earlier turn,
	// so each round after the first re-reads a prefix it has seen. Sent for a round and
	// not for an ai task, which is asked once.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type messagesOutputConfig struct {
	Format messagesOutputFormat `json:"format"`
}

type messagesOutputFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

func messagesOutput(outputs []Output) *messagesOutputConfig {
	if len(outputs) == 0 {
		return nil
	}
	schema, _ := answerSchema(outputs)
	return &messagesOutputConfig{Format: messagesOutputFormat{Type: "json_schema", Schema: schema}}
}

type thinkingSetting struct {
	Type string `json:"type"`
}

type messagesTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// messagesTurn is one turn. Content is a string for a turn of plain text, and a list of
// content blocks for a replayed turn of the model's or the tool results that answer it.
type messagesTurn struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type cacheControl struct {
	Type string `json:"type"`
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
		System:    systemFor(req),
		Tools:     toolSchemas(req.Tools),
		Messages:  m.turns(req),
		// Adaptive thinking lets the model spend more where a round is hard and less
		// where it is not, which is the shape of agent work. Off is for an endpoint
		// that does not implement it.
		Thinking:     m.thinking(),
		OutputConfig: messagesOutput(req.Outputs),
		CacheControl: roundCache(req),
	}
}

func roundCache(req Request) *cacheControl {
	if len(req.Tools) == 0 {
		return nil
	}
	return &cacheControl{Type: "ephemeral"}
}

// turns is the conversation this round is put as.
//
// With the run's earlier rounds on the record in this format, it is the run as it
// happened: the first prompt, then for each round the model's own turn — unchanged,
// thinking blocks included, which is what lets the model pick its reasoning up — and the
// result of every call that turn made. Only what is new is appended, so everything
// before it is a prefix the provider has already read.
//
// Without them it is one message stating the results in words, as before.
func (m *HTTPModel) turns(req Request) []messagesTurn {
	if !replayable(req.History, WireMessages) {
		return []messagesTurn{{Role: "user", Content: promptFor(req)}}
	}
	turns := []messagesTurn{{Role: "user", Content: req.History[0].Said}}
	for i, past := range req.History {
		results, ok := messagesToolResults(past, saidAfter(req, i))
		if !ok {
			// A stored turn this adapter cannot read back is not replayed in part.
			return []messagesTurn{{Role: "user", Content: promptFor(req)}}
		}
		turns = append(turns,
			messagesTurn{Role: "assistant", Content: json.RawMessage(past.Turn)},
			messagesTurn{Role: "user", Content: results})
	}
	return turns
}

// messagesToolResults answers every tool call of a stored turn, in the turn's own order
// and in one message — a provider reads results split across messages as a model that
// should stop calling tools together. said, when there is any, follows them as text.
func messagesToolResults(past PastRound, said string) ([]map[string]any, bool) {
	var blocks []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(past.Turn), &blocks); err != nil {
		return nil, false
	}
	var out []map[string]any
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		text, finished := resultOf(past, b.ID)
		block := map[string]any{"type": "tool_result", "tool_use_id": b.ID, "content": text}
		if !finished {
			block["is_error"] = true
		}
		out = append(out, block)
	}
	if len(out) == 0 {
		return nil, false // a past round that called nothing would have ended the run
	}
	if said != "" {
		out = append(out, map[string]any{"type": "text", "text": said})
	}
	return out, true
}

// said is what this call adds to the conversation, which is what its record keeps.
func (m *HTTPModel) said(req Request) string {
	if replayable(req.History, WireMessages) {
		if _, ok := messagesToolResults(req.History[len(req.History)-1], ""); ok {
			return changedText(req.Changed)
		}
	}
	return promptFor(req)
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
func promptFor(req Request) string {
	if len(req.Tools) == 0 {
		return taskPrompt(req)
	}
	return roundPrompt(req)
}

// taskPrompt is an ai task's message: the question as the author wrote it, and nothing
// added. Round bookkeeping ("this is round 1", "your earlier tool calls returned") would
// be describing a run that is not happening, and every sentence a model reads before the
// question is a sentence competing with it.
func taskPrompt(req Request) string {
	if req.Goal == "" {
		return "(the process states no question for this step)"
	}
	return req.Goal
}

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
		// In name order, every time. A Go map has no order of its own, so ranging over
		// it sent the same facts in a different sequence on every round — which a model
		// reads as a different prompt and a provider's cache cannot reuse.
		for _, k := range slices.Sorted(maps.Keys(req.Context)) {
			fmt.Fprintf(&b, "\n- %s: %s", k, req.Context[k])
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
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	// Usage as this format reports it: input_tokens is only the part that was neither
	// read from nor written to the cache, so the three are summed into one figure.
	Usage struct {
		InputTokens              uint64 `json:"input_tokens"`
		OutputTokens             uint64 `json:"output_tokens"`
		CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Content []struct {
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
	// The turn itself, untouched: the next round hands it back exactly as it came.
	var raw struct {
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(payload, &raw)
	call := Call{
		Model:      resp.Model,
		StopReason: resp.StopReason,
		Wire:       WireMessages,
		Turn:       string(raw.Content),
		Usage: Usage{
			InputTokens:      resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
		Answer: strings.TrimSpace(text.String()),
	}
	if len(calls) > 0 {
		// Tool calls are the answer for this round; any text alongside them is the
		// model thinking out loud on its way there. The process has no use for it as a
		// variable, but it is part of what the model said, so it stays on the record.
		return Decision{ToolCalls: calls, Call: call}, nil
	}
	d, err := answerDecision(m.AnswerVariable, text.String(), resp.StopReason)
	d.Call = call
	return d, err
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

// ForModel implements [ModelChooser]: the model id is one field of the request body, so
// asking a different one is a copy with that field replaced. A copy rather than a
// mutation because a worker holds one adapter and works many jobs at once — writing the
// field would race, and the loser would ask the wrong model.
func (m *HTTPModel) ForModel(id string) Model {
	if id == "" || id == m.Model {
		return m
	}
	c := *m
	c.Model = id
	return &c
}
