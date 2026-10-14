package agent_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

// TestAToolCallWhoseInputIsNotAnObjectFailsTheRound: arguments become variables by
// name, and an input with no names cannot become any — so the round fails naming the
// tool, rather than activating it with nothing.
func TestAToolCallWhoseInputIsNotAnObjectFailsTheRound(t *testing.T) {
	srv, _, _ := endpoint(t, http.StatusOK, `{"stop_reason":"tool_use","content":[
		{"type":"tool_use","id":"toolu_1","name":"zinsen_holen","input":["https://example.ch"]}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}
	_, err := m.Decide(context.Background(), requestWithOneTool())
	if err == nil || !strings.Contains(err.Error(), `tool call "zinsen_holen": input is not an object`) {
		t.Fatalf("Decide = %v, want the unusable input named with its tool", err)
	}
}

// TestAToolCallMayPassNothingOrNull: a call with no input activates the tool with no
// arguments, and an explicit null arrives as a null variable rather than as the text
// "null".
func TestAToolCallMayPassNothingOrNull(t *testing.T) {
	srv, _, _ := endpoint(t, http.StatusOK, `{"stop_reason":"tool_use","content":[
		{"type":"tool_use","id":"toolu_1","name":"zinsen_holen"},
		{"type":"tool_use","id":"toolu_2","name":"zinsen_holen","input":{"maxRows":null}}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}
	d, err := m.Decide(context.Background(), requestWithOneTool())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(d.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want two", d.ToolCalls)
	}
	if args := d.ToolCalls[0].Arguments; len(args) != 0 {
		t.Errorf("first call arguments = %+v, want none", args)
	}
	if args := d.ToolCalls[1].Arguments; len(args) != 1 || args[0] != (model.VariableValue{Name: "maxRows", Kind: model.VarNull}) {
		t.Errorf("second call arguments = %+v, want maxRows as null", args)
	}
}

// TestAThinkingSettingIsSentAsConfigured: anything other than off or the default is
// passed through, for an endpoint that names its modes differently.
func TestAThinkingSettingIsSentAsConfigured(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client(), Thinking: "enabled"}
	if _, err := m.Decide(context.Background(), requestWithOneTool()); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := (*seen)[0].Thinking.Type; got != "enabled" {
		t.Fatalf("thinking = %q, want the configured mode", got)
	}
}

// TestAStepWithoutAGoalSaysSo: the model is told plainly that the process stated
// nothing, in a round and in a one-shot call alike, rather than being sent an empty
// message it would fill with a guess.
func TestAStepWithoutAGoalSaysSo(t *testing.T) {
	srv, seen, _ := endpoint(t, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`)
	m := &agent.HTTPModel{Endpoint: srv.URL, APIKey: "k", Client: srv.Client()}

	round := requestWithOneTool()
	round.Goal = ""
	if _, err := m.Decide(context.Background(), round); err != nil {
		t.Fatalf("Decide(round): %v", err)
	}
	if got := (*seen)[0].Messages[0].Content; !strings.HasPrefix(got, "Goal: (the process states no goal for this step)") {
		t.Errorf("round message = %q, want it to say there is no goal", got)
	}

	if _, err := m.Decide(context.Background(), agent.Request{Round: 1}); err != nil {
		t.Fatalf("Decide(task): %v", err)
	}
	if got := (*seen)[1].Messages[0].Content; got != "(the process states no question for this step)" {
		t.Errorf("task message = %q, want it to say there is no question", got)
	}
}
