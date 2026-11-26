package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

type recordedRuns []model.AgentRunValue

func (r recordedRuns) AgentRunsOf(_, _ uint64, fn func(int64, uint64, *model.AgentRunValue) error) error {
	for i := range r {
		if err := fn(0, uint64(i), &r[i]); err != nil {
			return err
		}
	}
	return nil
}

func openClear(v *model.AgentRunValue) (string, string, string, bool) {
	return v.Prompt, v.Turn, v.Context, true
}

const (
	turnOne = `[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"t1","name":"lookup","input":{"id":"7"}},{"type":"tool_use","id":"t2","name":"lookup","input":{"id":"8"}}]`
	turnTwo = `[{"type":"tool_use","id":"t3","name":"lookup","input":{"id":"9"}}]`
)

func twoRounds() recordedRuns {
	return recordedRuns{
		{Round: 1, Wire: WireMessages, Turn: turnOne, Prompt: "Goal: prüfe", Context: `{"dossier":"D1","kunde":"K"}`},
		{Kind: model.AgentRunToolResult, Round: 1, CallId: "t1", Answer: "sieben"},
		{Round: 2, Wire: WireMessages, Turn: turnTwo, Prompt: "", Context: `{"dossier":"D1","kunde":"K"}`},
		{Kind: model.AgentRunToolResult, Round: 2, CallId: "t3", Answer: ""},
	}
}

func emptyProcess(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	bl := compiler.NewBuilder(1, "p", 1)
	s, e := bl.AddStartEvent(), bl.AddEndEvent()
	bl.Connect(s, e)
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// The record says which round this is and what the earlier ones were; the round is told
// only the context that changed since (ADR-draft-agent-transcript-and-answer).
func TestARoundIsGivenItsRunsTranscript(t *testing.T) {
	cp := emptyProcess(t)
	req := Request{Goal: "prüfe", Tools: oneTool, Round: 1, Context: map[string]string{"dossier": "D2", "kunde": "K", "neu": "x"}}
	got := WithTranscript(req, twoRounds(), cp, 1, 2, openClear)
	if got.Round != 3 || len(got.History) != 2 {
		t.Fatalf("round %d with %d past rounds, want round 3 with 2", got.Round, len(got.History))
	}
	if got.History[0].Said != "Goal: prüfe" || got.History[0].Turn != turnOne || len(got.History[0].Results) != 1 ||
		got.History[0].Results[0].CallId != "t1" || got.History[1].Results[0].CallId != "t3" {
		t.Errorf("history = %+v", got.History)
	}
	if len(got.Changed) != 2 || got.Changed["dossier"] != "D2" || got.Changed["neu"] != "x" {
		t.Errorf("changed = %v, want the replaced value and the new one, not the unchanged", got.Changed)
	}

	// No record yet: the round is what it was.
	if first := WithTranscript(req, recordedRuns{}, cp, 1, 2, openClear); first.Round != 1 || first.History != nil {
		t.Errorf("a first round = %+v", first)
	}
	// A round whose texts cannot be read back is counted and not replayed.
	sealed := func(*model.AgentRunValue) (string, string, string, bool) { return "", "", "", false }
	if blind := WithTranscript(req, twoRounds(), cp, 1, 2, sealed); blind.Round != 3 || blind.History != nil || blind.Changed != nil {
		t.Errorf("an unreadable run = round %d, history %v", blind.Round, blind.History)
	}
	// So is one recorded before turns were kept.
	old := twoRounds()
	old[0].Turn, old[0].Wire = "", ""
	if legacy := WithTranscript(req, old, cp, 1, 2, openClear); legacy.Round != 3 || legacy.History != nil {
		t.Errorf("a run without stored turns = round %d, history %v", legacy.Round, legacy.History)
	}
}

func replayRequest(t *testing.T) Request {
	t.Helper()
	req := Request{Goal: "prüfe", Tools: oneTool, Round: 1, Context: map[string]string{"dossier": "D2", "kunde": "K"}}
	return WithTranscript(req, twoRounds(), emptyProcess(t), 1, 2, openClear)
}

// The Messages format is handed the run as it happened: the first prompt, each of the
// model's turns untouched, every call of a turn answered in one message in the turn's
// own order — a call that never finished included — and only what is new at the end.
func TestMessagesReplaysTheRunAsItHappened(t *testing.T) {
	var sent map[string]any
	m := &HTTPModel{APIKey: "k", Endpoint: recordingEndpoint(t,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"fertig"}]}`, &sent)}
	d, err := m.Decide(context.Background(), replayRequest(t))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	msgs := sent["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("%d messages, want prompt, turn, results, turn, results: %v", len(msgs), msgs)
	}
	if first := msgs[0].(map[string]any); first["role"] != "user" || first["content"] != "Goal: prüfe" {
		t.Errorf("first message = %v, want the first prompt as it was sent", first)
	}
	if raw, _ := json.Marshal(msgs[1].(map[string]any)["content"]); !strings.Contains(string(raw), `"signature":"sig"`) {
		t.Errorf("the model's turn was not handed back whole: %s", raw)
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("round 1 results = %v, want one per call in one message", results)
	}
	r1, r2 := results[0].(map[string]any), results[1].(map[string]any)
	if r1["tool_use_id"] != "t1" || r1["content"] != "sieben" || r1["is_error"] != nil {
		t.Errorf("first result = %v", r1)
	}
	if r2["tool_use_id"] != "t2" || r2["is_error"] != true {
		t.Errorf("a call with no result on the record = %v, want it answered as not finished", r2)
	}
	last := msgs[4].(map[string]any)["content"].([]any)
	if len(last) != 2 || last[0].(map[string]any)["content"] != "(the tool finished and stated no result)" ||
		!strings.Contains(last[1].(map[string]any)["text"].(string), "- dossier: D2") {
		t.Errorf("last message = %v, want the result and then what changed", last)
	}
	if sent["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("a round is not sent cacheable: %v", sent["cache_control"])
	}
	// What this call added is what its record keeps — not the whole conversation again.
	if !strings.HasPrefix(d.Call.Prompt, "What the process knows has changed") || d.Call.Wire != WireMessages ||
		d.Call.Turn != `[{"type":"text","text":"fertig"}]` {
		t.Errorf("call = %+v", d.Call)
	}

	// Another format's turns are not replayed; the results are stated in words instead.
	foreign := replayRequest(t)
	foreign.History[0].Wire = WireChatCompletions
	foreign.Results = []string{"sieben"}
	sent = nil
	if _, err := m.Decide(context.Background(), foreign); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if msgs := sent["messages"].([]any); len(msgs) != 1 || !strings.Contains(msgs[0].(map[string]any)["content"].(string), "Your earlier tool calls returned") {
		t.Errorf("fallback = %v, want one message in words", sent["messages"])
	}
	// So is a stored turn that is not a list of blocks.
	broken := replayRequest(t)
	broken.History[1].Turn = `"not blocks"`
	if got := m.turns(broken); len(got) != 1 {
		t.Errorf("a turn that cannot be read back was replayed in part: %v", got)
	}
	if got := m.said(broken); !strings.HasPrefix(got, "Goal: ") {
		t.Errorf("said = %q, want the whole prompt when nothing is replayed", got)
	}
}

// An ai task is asked once, so there is nothing to reuse.
func TestAnAiTaskIsNotSentCacheable(t *testing.T) {
	var sent map[string]any
	m := &HTTPModel{APIKey: "k", Endpoint: recordingEndpoint(t, `{"content":[{"type":"text","text":"ok"}]}`, &sent)}
	if _, err := m.Decide(context.Background(), Request{Goal: "g", Round: 1}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, has := sent["cache_control"]; has {
		t.Error("an ai task carries cache_control")
	}
}

// Chat Completions gets the same run in its own shape: the assistant message, one tool
// message per call, and what changed as a user message.
func TestChatCompletionsReplaysTheRunAsItHappened(t *testing.T) {
	runs := recordedRuns{
		{Round: 1, Wire: WireChatCompletions, Prompt: "Goal: prüfe", Context: `{"dossier":"D1"}`,
			Turn: `{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`},
		{Kind: model.AgentRunToolResult, Round: 1, CallId: "c1", Answer: "sieben"},
	}
	req := WithTranscript(Request{Goal: "prüfe", Tools: oneTool, Round: 1, Context: map[string]string{"dossier": "D2"}},
		runs, emptyProcess(t), 1, 2, openClear)
	var sent map[string]any
	c := &ChatCompletionsModel{APIKey: "k", Model: "gpt", Endpoint: recordingEndpoint(t,
		`{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"refusal":null,
		  "tool_calls":[{"id":"c2","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"1\"}"}}]}}]}`, &sent)}
	d, err := c.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	msgs := sent["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("%d messages, want system, prompt, turn, tool, changed: %v", len(msgs), msgs)
	}
	if msgs[1].(map[string]any)["content"] != "Goal: prüfe" || msgs[2].(map[string]any)["role"] != "assistant" {
		t.Errorf("messages = %v", msgs)
	}
	if tool := msgs[3].(map[string]any); tool["role"] != "tool" || tool["tool_call_id"] != "c1" || tool["content"] != "sieben" {
		t.Errorf("tool message = %v", tool)
	}
	if !strings.Contains(msgs[4].(map[string]any)["content"].(string), "- dossier: D2") {
		t.Errorf("last message = %v, want what changed", msgs[4])
	}
	// The stored turn keeps the role, the words and the calls, and none of the rest.
	if d.Call.Wire != WireChatCompletions || strings.Contains(d.Call.Turn, "refusal") || !strings.Contains(d.Call.Turn, `"id":"c2"`) {
		t.Errorf("stored turn = %s", d.Call.Turn)
	}
	if d.Call.Prompt == "" || !strings.HasPrefix(d.Call.Prompt, "What the process knows has changed") {
		t.Errorf("prompt = %q", d.Call.Prompt)
	}
	// A stored message that made no call cannot be replayed.
	req.History[0].Turn = `{"role":"assistant","content":"hm"}`
	if got := chatTurns(req); len(got) != 2 {
		t.Errorf("fallback = %v, want system and one message in words", got)
	}
	if got := chatStoredTurn([]byte(`nonsense`)); got != "" {
		t.Errorf("a body that is not a response = %q", got)
	}
}

// The transcript travels to the worker like the rest of the round.
func TestTheTranscriptTravelsInThePayload(t *testing.T) {
	r := Round{Request: replayRequest(t)}
	raw, _ := json.Marshal(ResolveJobPayload(r))
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	back, err := RoundFromPayload(fields)
	if err != nil || len(back.History) != 2 || back.History[0].Turn != turnOne || back.History[0].Results[0].Text != "sieben" ||
		back.Changed["dossier"] != "D2" || back.Round != 3 {
		t.Errorf("round = %+v, %v", back, err)
	}
	if got := historyFromPayload(7); got != nil {
		t.Errorf("a payload of the wrong shape = %v", got)
	}
}
