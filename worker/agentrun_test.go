package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/model"
)

type accountingModel struct{ decision agent.Decision }

func (m accountingModel) Decide(context.Context, agent.Request) (agent.Decision, error) {
	return m.decision, nil
}

// blockingModel answers only when its context ends, as a provider that has stopped
// sending does.
type blockingModel struct{}

func (blockingModel) Decide(ctx context.Context, _ agent.Request) (agent.Decision, error) {
	<-ctx.Done()
	return agent.Decision{}, ctx.Err()
}

var accountedCall = agent.Call{
	Model: "claude-opus-5-5", StopReason: "end_turn",
	Usage:  agent.Usage{InputTokens: 1040, OutputTokens: 25, CacheReadTokens: 900, CacheWriteTokens: 100},
	System: "s", Prompt: "p", Answer: "a",
}

func agentRoundJob(extra map[string]any) Job {
	fields := map[string]any{"connector": "w", "goal": "g", "round": 1, "tools": []any{map[string]any{"name": "t"}}}
	for k, v := range extra {
		fields[k] = v
	}
	return Job{Connector: &ConnectorPayload{Kind: "agent", Fields: fields}}
}

// A round and an ai task both complete with the account of their call, which is what
// the engine keeps as the record of it (ADR-draft-agent-run-record).
func TestAModelCallIsReportedWithItsOutcome(t *testing.T) {
	round := accountingModel{agent.Decision{Call: accountedCall, ToolCalls: []model.ToolCall{{Tool: "t", CallId: "c"}}}}
	out, err := RunAgentRound(context.Background(), agentRoundJob(nil), map[string]agent.Model{"w": round})
	if err != nil {
		t.Fatalf("RunAgentRound: %v", err)
	}
	for name, rep := range map[string]*AgentRunReport{"round": out.AgentRun} {
		if rep == nil || rep.Model != "claude-opus-5-5" || rep.StopReason != "end_turn" || rep.InputTokens != 1040 ||
			rep.OutputTokens != 25 || rep.CacheReadTokens != 900 || rep.CacheWriteTokens != 100 ||
			rep.System != "s" || rep.Prompt != "p" || rep.Answer != "a" {
			t.Errorf("%s report = %+v, want the adapter's account", name, rep)
		}
	}

	task := accountingModel{agent.Decision{Call: accountedCall, Outputs: []model.VariableValue{{Name: "x", Kind: model.VarString, Text: "a"}}}}
	out, err = RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "w", "prompt": "p", "resultVariable": "v",
	}), map[string]agent.Model{"w": task})
	if err != nil {
		t.Fatalf("RunAiTask: %v", err)
	}
	if out.AgentRun == nil || out.AgentRun.OutputTokens != 25 || out.Variables["v"] != "a" {
		t.Errorf("ai task outcome = %+v / %+v, want the answer and the account", out.Variables, out.AgentRun)
	}
}

// The bound a model states on the element ends a call that does not come back, and the
// job fails rather than waiting out its lease.
func TestACallPastItsAuthoredTimeoutFails(t *testing.T) {
	models := map[string]agent.Model{"w": blockingModel{}}
	if _, err := RunAgentRound(context.Background(), agentRoundJob(map[string]any{"timeoutMs": 20}), models); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("round past its timeout: %v, want a deadline error", err)
	}
	if _, err := RunAiTask(context.Background(), aiTaskJob(map[string]any{
		"connector": "w", "prompt": "p", "resultVariable": "v", "timeoutMs": 20,
	}), models); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ai task past its timeout: %v, want a deadline error", err)
	}
}
