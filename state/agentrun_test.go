package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func agentRuns(t *testing.T, s *state.Store, piKey uint64) []model.AgentRunValue {
	t.Helper()
	var out []model.AgentRunValue
	if err := s.AgentRunHistory(piKey, func(_ int64, _ uint64, v *model.AgentRunValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// An agent's facts are read back in the order they happened, each element instance's
// totals follow its latest model call, a tool result moves no total, and all of it goes
// with the instance's history (ADR-0566).
func TestAgentRunsAreKeptInOrderSummedPerElementAndPurgedWithTheirInstance(t *testing.T) {
	s := openStore(t)
	pi, other := model.NewKey(1, 10), model.NewKey(1, 11)
	agent, task := model.NewKey(1, 20), model.NewKey(1, 21)
	commit(t, s, func(tx *state.Tx) error {
		for _, f := range []struct {
			ts int64
			v  model.AgentRunValue
		}{
			{100, model.AgentRunValue{ProcessInstanceKey: pi, ElementInstanceKey: agent, Round: 1, InputTokens: 100, OutputTokens: 10, SumInputTokens: 100, SumOutputTokens: 10}},
			{110, model.AgentRunValue{ProcessInstanceKey: pi, ElementInstanceKey: agent, Kind: model.AgentRunToolResult, Round: 1, CallId: "c1", Answer: "42"}},
			{120, model.AgentRunValue{ProcessInstanceKey: pi, ElementInstanceKey: agent, Round: 2, InputTokens: 200, OutputTokens: 20, SumInputTokens: 300, SumOutputTokens: 30, Limit: model.AgentRunLimitRounds}},
			{130, model.AgentRunValue{ProcessInstanceKey: pi, ElementInstanceKey: task, Round: 1, SumInputTokens: 7, SumOutputTokens: 3}},
			{140, model.AgentRunValue{ProcessInstanceKey: other, ElementInstanceKey: agent, Round: 1, SumInputTokens: 1}},
		} {
			v := f.v
			if err := tx.RecordAgentRun(f.ts, uint64(f.ts), &v); err != nil {
				return err
			}
		}
		// A fact that names no instance has nowhere to be kept.
		return tx.RecordAgentRun(150, 150, &model.AgentRunValue{ElementInstanceKey: agent, Round: 9})
	})

	got := agentRuns(t, s, pi)
	if len(got) != 4 || got[0].Round != 1 || got[1].CallId != "c1" || got[2].Round != 2 || got[3].ElementInstanceKey != task {
		t.Fatalf("history = %+v, want the instance's four facts in order", got)
	}
	// One agent's facts are read without the instance's others.
	var own []uint32
	if err := s.AgentRunsOf(pi, agent, func(_ int64, _ uint64, v *model.AgentRunValue) error {
		own = append(own, v.Round)
		return nil
	}); err != nil || len(own) != 3 {
		t.Fatalf("the agent's own facts = %v, %v, want its three", own, err)
	}
	total, err := s.AgentRunTotal(pi, agent)
	if err != nil {
		t.Fatal(err)
	}
	if want := (model.AgentRunTotal{Rounds: 2, InputTokens: 300, OutputTokens: 30, Limit: model.AgentRunLimitRounds}); total != want {
		t.Errorf("total = %+v, want %+v", total, want)
	}
	if none, err := s.AgentRunTotal(pi, model.NewKey(1, 99)); err != nil || none != (model.AgentRunTotal{}) {
		t.Errorf("an element that called no model = %+v, %v, want the zero total", none, err)
	}

	commit(t, s, func(tx *state.Tx) error {
		// The in-flight transaction sees its own write, which is what lets the engine
		// number the next round in the batch that recorded the last.
		if err := tx.RecordAgentRun(160, 160, &model.AgentRunValue{ProcessInstanceKey: pi, ElementInstanceKey: task, Round: 2, SumInputTokens: 9}); err != nil {
			return err
		}
		if tot, err := tx.AgentRunTotal(pi, task); err != nil || tot.Rounds != 2 || tot.InputTokens != 9 {
			t.Errorf("in-flight total = %+v, %v, want round 2 with 9 input tokens", tot, err)
		}
		return tx.PurgeInstanceHistory(pi, 0, 0)
	})
	if left := agentRuns(t, s, pi); len(left) != 0 {
		t.Errorf("after the purge the instance still has %d facts", len(left))
	}
	if tot, _ := s.AgentRunTotal(pi, agent); tot != (model.AgentRunTotal{}) {
		t.Errorf("after the purge a total is left: %+v", tot)
	}
	if kept := agentRuns(t, s, other); len(kept) != 1 {
		t.Errorf("the purge reached another instance: %d facts left, want 1", len(kept))
	}
}
