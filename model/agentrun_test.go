package model

import (
	"reflect"
	"testing"
)

// A record must come back from the log exactly as it went in: replay rebuilds the
// history and the totals from these bytes alone (I4/I6).
func TestAgentRunValueRoundTrips(t *testing.T) {
	want := &AgentRunValue{
		ProcessInstanceKey: 11, ElementInstanceKey: 12, JobKey: 13, ProcessDefKey: 14,
		ElementId: 5, Kind: AgentRunModelCall, Round: 3,
		Model: "claude-opus-5-5", StopReason: "tool_use",
		InputTokens: 1200, OutputTokens: 340, CacheReadTokens: 900, CacheWriteTokens: 100,
		DurationMs: 4200, SumInputTokens: 3000, SumOutputTokens: 800,
		Limit: AgentRunLimitTokens, ToolCalls: `[{"callId":"c1","tool":"lookup"}]`,
		TextState: AgentRunTextEnciphered, System: "s", Prompt: "p", Answer: "a",
		Wire: "messages", Turn: `[{"type":"text"}]`, Context: `{"k":"v"}`, CallId: "c1", Fresh: true,
	}
	v, err := DecodeValue(VTAgentRun, want.encode(nil))
	if err != nil || !reflect.DeepEqual(v, want) {
		t.Fatalf("DecodeValue = %+v, %v\nwant %+v", v, err, want)
	}
}

// A record written before Fresh existed ends one byte earlier and reads as not fresh.
func TestAgentRunValueReadsARecordWithoutItsAppendedField(t *testing.T) {
	full := (&AgentRunValue{Model: "m", Fresh: true}).encode(nil)
	var v AgentRunValue
	if err := v.decode(full[:len(full)-1]); err != nil || v.Fresh || v.Model != "m" {
		t.Errorf("decode = %+v, %v, want the record without Fresh", v, err)
	}
}

func TestAgentRunValueRefusesATruncatedRecord(t *testing.T) {
	full := (&AgentRunValue{Model: "m", Answer: "a"}).encode(nil)
	for _, n := range []int{0, agentRunFixed - 1, agentRunFixed + 2, len(full) - 2} {
		if err := (&AgentRunValue{}).decode(full[:n]); err == nil {
			t.Errorf("decode of %d of %d bytes succeeded, want an error", n, len(full))
		}
	}
}

func TestAgentRunTotalRoundTrips(t *testing.T) {
	want := AgentRunTotal{Rounds: 7, InputTokens: 9000, OutputTokens: 1200, Limit: AgentRunLimitRounds}
	raw := AppendAgentRunTotal(nil, want)
	if len(raw) != AgentRunTotalSize {
		t.Fatalf("encoded %d bytes, want %d", len(raw), AgentRunTotalSize)
	}
	if got := DecodeAgentRunTotal(raw); got != want {
		t.Errorf("DecodeAgentRunTotal = %+v, want %+v", got, want)
	}
	// A row that is not there reads as "no call yet", not as an error.
	if got := DecodeAgentRunTotal(nil); got != (AgentRunTotal{}) {
		t.Errorf("an absent row = %+v, want the zero total", got)
	}
}
