package agent

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The two provider formats a model's turn can be kept in.
const (
	WireMessages        = "messages"
	WireChatCompletions = "chat-completions"
)

// PastRound is one earlier round of the run, as it is put in front of the model again
// (ADR-draft-agent-transcript-and-answer): what that call added to the conversation, the
// model's own turn exactly as its provider returned it, and what each tool it called
// returned.
//
// Turn is opaque here and to the engine. It is in the format named by Wire, and only an
// adapter speaking that format replays it; any other falls back to stating the results
// in words, which is what every round did before this existed.
type PastRound struct {
	Wire    string       `json:"wire"`
	Turn    string       `json:"turn"`
	Said    string       `json:"said"`
	Results []ToolResult `json:"results,omitempty"`
}

// ToolResult is what one tool call returned, paired with the call by the model's own id.
type ToolResult struct {
	CallId string `json:"callId"`
	Tool   string `json:"tool,omitempty"`
	Text   string `json:"text"`
}

// RunReader is the part of the store a transcript is read from.
type RunReader interface {
	AgentRunsOf(piKey, elKey uint64, fn func(ts int64, pos uint64, v *model.AgentRunValue) error) error
}

// WithTranscript adds what the run's record knows to a resolved round: which round this
// really is, the earlier rounds to replay, and which of the context's values have
// changed since the model was last told them.
//
// open gives a stored text back readable, and reports false when it cannot — a text
// enciphered under a key that has since been destroyed, or one that was withheld. A run
// with any such round is not replayed: half a conversation is worse than the summary in
// words every adapter can still give.
func WithTranscript(req Request, runs RunReader, cp *compiler.CompiledProcess, piKey, containerKey uint64,
	open func(v *model.AgentRunValue) (prompt, turn, context string, ok bool)) Request {
	var (
		history  []PastRound
		complete = true
		last     map[string]string
		rounds   int
	)
	err := runs.AgentRunsOf(piKey, containerKey, func(_ int64, _ uint64, v *model.AgentRunValue) error {
		if v.Kind == model.AgentRunToolResult {
			if i := int(v.Round) - 1; i >= 0 && i < len(history) {
				history[i].Results = append(history[i].Results, ToolResult{
					CallId: v.CallId, Tool: cp.ElementBpmnId(v.ElementId), Text: v.Answer,
				})
			}
			return nil
		}
		rounds++
		prompt, turn, context, ok := open(v)
		if !ok || turn == "" || v.Wire == "" {
			complete = false
		}
		history = append(history, PastRound{Wire: v.Wire, Turn: turn, Said: prompt})
		last = nil
		if context != "" {
			_ = json.Unmarshal([]byte(context), &last)
		}
		return nil
	})
	if err != nil || rounds == 0 {
		return req
	}
	// The record counts rounds; the number of results does not, as soon as a round
	// calls two tools or the model names no result collection.
	req.Round = rounds + 1
	if !complete || history[0].Said == "" {
		return req
	}
	req.History = history
	req.Changed = changedContext(last, req.Context)
	return req
}

// changedContext is what the model has not been told yet: every value that differs from
// the one it was last given. Telling it only that keeps the conversation append-only —
// which is what lets a provider reuse everything before it — without leaving the model
// on a value a tool has since replaced.
func changedContext(last, now map[string]string) map[string]string {
	var changed map[string]string
	for k, v := range now {
		if old, told := last[k]; !told || old != v {
			if changed == nil {
				changed = map[string]string{}
			}
			changed[k] = v
		}
	}
	return changed
}

// changedText renders that as the text a round adds after its tool results.
func changedText(changed map[string]string) string {
	if len(changed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("What the process knows has changed since you were last asked:")
	for _, k := range slices.Sorted(maps.Keys(changed)) {
		fmt.Fprintf(&b, "\n- %s: %s", k, changed[k])
	}
	return b.String()
}

// replayable reports whether an adapter speaking wire can replay this history as it was.
func replayable(history []PastRound, wire string) bool {
	if len(history) == 0 || history[0].Said == "" {
		return false
	}
	for _, past := range history {
		if past.Wire != wire || past.Turn == "" {
			return false
		}
	}
	return true
}

// resultOf finds what a call returned. A call with no result on the record did not
// finish — its activity was interrupted or cancelled — and the model is told so rather
// than left with a call it never hears back from, which a provider refuses outright.
func resultOf(past PastRound, callID string) (text string, finished bool) {
	for _, r := range past.Results {
		if r.CallId == callID {
			if r.Text == "" {
				return "(the tool finished and stated no result)", true
			}
			return r.Text, true
		}
	}
	return "(this call did not finish: its activity was interrupted or cancelled)", false
}

// saidAfter is the text the conversation carries after round i's tool results: what the
// next recorded round added, or — after the last one — what this round is about to add.
func saidAfter(req Request, i int) string {
	if i+1 < len(req.History) {
		return req.History[i+1].Said
	}
	return changedText(req.Changed)
}

// historyFromPayload reads the earlier rounds back, in either shape they can arrive in.
func historyFromPayload(v any) []PastRound {
	switch raw := v.(type) {
	case []PastRound:
		return raw
	case []any:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var out []PastRound
		if json.Unmarshal(data, &out) != nil {
			return nil
		}
		return out
	}
	return nil
}
