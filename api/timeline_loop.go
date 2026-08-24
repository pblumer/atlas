package api

import (
	"sort"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/state"
)

// A replay of a looping activity could say which round a step was, and what that round
// read and wrote, but not the one thing an author actually asks when a loop does not do
// what they meant: *what was it told to repeat while, and what did it decide?* Both
// halves were unreachable — the condition lives in the model, which the timeline never
// read for anything but element ids, and the decision lives in the shape of the log,
// which nobody folded into an answer.
//
// Nothing here re-evaluates FEEL. The condition text and the cap are model facts, read
// through the definition in force at that step's own log position (ADR-0162). Whether a
// round was followed by another is a fact about the log: a round that repeated has a
// successor under the same body, and one that did not, ended the loop. The values are
// the ones already folded for the step. What cannot be established from the record —
// which of several possible reasons ended a multi-instance — is left unsaid rather than
// guessed.

// loopView explains one loop on the replay (ADR-0077/0133): what the model says, and
// what happened. It rides on the loop's body step (the activity as a whole) and on each
// round, which are the two questions an operator asks — "what is this loop" and "why did
// this round not lead to another".
type loopView struct {
	// Kind is "loop" for the ↻ standard loop (repeat while a condition holds) or
	// "multi-instance" for ∥/≡ (once per collection element). The two read their
	// condition in opposite directions, so the frontend must not label them alike.
	Kind string `json:"kind"`
	// Condition is the FEEL as the author wrote it: a standard loop's *repeat while*,
	// or a multi-instance's completion condition. Empty when the loop states none —
	// which is itself the answer in the common "why did it run to the cap" case.
	Condition string `json:"condition,omitempty"`
	// TestBefore marks a standard loop that checks its condition before the first round
	// (a while loop that may run zero times) rather than after each (BPMN's default).
	TestBefore bool `json:"testBefore,omitempty"`
	// Maximum is the stated loopMaximum, 0 when the loop states none. With no maximum a
	// standard loop is bounded only by its condition and the engine's safety ceiling.
	Maximum int32 `json:"maximum,omitempty"`
	// Round is which round this step is, 1-based; 0 on the loop's body.
	Round int `json:"round,omitempty"`
	// Rounds is how many rounds ran in total. Only on the body, where it is the summary
	// of a loop the operator is looking at as one activity.
	Rounds int `json:"rounds,omitempty"`
	// Outcome is what followed this round: "repeated" (another round ran), "stopped"
	// (this was the last one), "running" (it has not finished), or "terminated" (it was
	// torn down — a cancelled instance, an interrupting boundary event). Absent on the
	// body.
	Outcome string `json:"outcome,omitempty"`
	// StopReason names what ended the loop, on the round that ended it: "maximum" (the
	// stated cap), "condition" (it no longer held), or "ceiling" (the engine's backstop
	// for a loop with no cap). Absent where the record cannot say — a multi-instance
	// ends when its collection runs out or its completion condition holds, and the two
	// are not distinguishable after the fact.
	StopReason string `json:"stopReason,omitempty"`
	// Reads are the values the condition's own variables held for this round, nearest
	// scope first — the round's locals over the loop body's over the process's, which is
	// the chain the engine resolves them in. Absent for a loop with no condition.
	Reads []variableView `json:"reads,omitempty"`
	// Missing names the condition's variables that nothing in scope had a value for.
	// FEEL is null-propagating, so such a condition is simply never true — the loop then
	// runs to its cap, which looks like the condition being ignored.
	Missing []string `json:"missing,omitempty"`
}

// annotateLoops fills in the loopView of every step that is a looping activity — its
// body, or one round of it. bodyTokens are the token ids of loop bodies (a round is
// activated carrying its body's token as ParentTokenID, which is how they are known);
// acts maps a log position to the activation recorded there; torn marks the element
// instances that were terminated rather than completed.
func annotateLoops(steps []timelineStep, ver *versionAt, acts map[uint64]state.ElementReplayValue, bodyTokens map[uint64]struct{}, torn map[uint64]bool) {
	if len(steps) == 0 {
		return
	}
	// The rounds of each body, in log order, so a round can ask whether another followed
	// it — the log's own answer to "did the loop go round again".
	roundsOf := map[uint64][]uint64{}
	for pos, rv := range acts {
		if rv.ParentTokenID == 0 {
			continue
		}
		if _, ok := bodyTokens[rv.ParentTokenID]; ok {
			roundsOf[rv.ParentTokenID] = append(roundsOf[rv.ParentTokenID], pos)
		}
	}
	for _, list := range roundsOf {
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	}

	for i := range steps {
		s := &steps[i]
		rv, ok := acts[s.Position]
		if !ok {
			continue
		}
		n, ok := ver.node(s.Position, rv.ElementID)
		if !ok || n.MultiInstance < 0 {
			continue // not a looping activity (or its definition is gone)
		}
		d := ver.cp(s.Position).MultiInstance(n.MultiInstance)
		lv := loopView{Kind: "multi-instance"}
		cond := d.CompletionCondition
		if d.Standard {
			lv.Kind, cond = "loop", d.LoopCondition
			lv.TestBefore, lv.Maximum = d.TestBefore, d.LoopMaximum
		}
		if cond != nil {
			lv.Condition = cond.Source()
		}
		if _, isRound := bodyTokens[rv.ParentTokenID]; !isRound || rv.ParentTokenID == 0 {
			// The body: the activity as a whole, summarised by how many rounds it ran.
			lv.Rounds = len(roundsOf[rv.TokenID])
			s.Loop = &lv
			continue
		}
		lv.Round = s.Iteration
		lv.Reads, lv.Missing = conditionReads(cond, s)
		siblings := roundsOf[rv.ParentTokenID]
		switch {
		case torn[s.ElementInstanceKey]:
			lv.Outcome = "terminated"
		case s.EndAt == 0:
			lv.Outcome = "running"
		case len(siblings) > 0 && siblings[len(siblings)-1] > s.Position:
			lv.Outcome = "repeated"
		default:
			lv.Outcome = "stopped"
			lv.StopReason = stopReason(d, lv.Round)
		}
		s.Loop = &lv
	}
}

// stopReason names what ended a standard loop on its last round. A multi-instance is
// left unexplained on purpose: it ends when its collection runs out or when its
// completion condition holds, and the log records neither as a distinct fact.
func stopReason(d *compiler.MultiInstanceDetail, round int) string {
	switch {
	case !d.Standard:
		return ""
	case d.LoopMaximum > 0 && int32(round) >= d.LoopMaximum:
		return "maximum"
	case d.LoopCondition != nil:
		return "condition"
	case round > 0 && round%compiler.SafeLoopCeiling == 0:
		return "ceiling"
	}
	return ""
}

// conditionReads resolves the variables a loop's condition reads to the values they held
// for this round, and names the ones nothing in scope had. The engine evaluates the
// condition over the finished round's scope chain, so the round's own locals come first,
// then everything as it stood when the round completed.
func conditionReads(cond *expr.Compiled, s *timelineStep) ([]variableView, []string) {
	if cond == nil {
		return nil, nil
	}
	var reads []variableView
	var missing []string
	for _, name := range cond.Inputs() {
		if v, ok := lookupVar(s.Inputs, name, s.ElementID); ok {
			reads = append(reads, v)
			continue
		}
		if v, ok := lookupVar(s.VariablesAfter, name, s.ElementID); ok {
			reads = append(reads, v)
			continue
		}
		missing = append(missing, name)
	}
	return reads, missing
}

// lookupVar finds a variable by name in one of a step's folded sets, preferring the
// scope named by prefer — a loop's body scope carries the element's own id, and it
// shadows a process variable of the same name for everything inside the loop.
func lookupVar(list []variableView, name, prefer string) (variableView, bool) {
	var out variableView
	var found bool
	for _, v := range list {
		if v.Name != name {
			continue
		}
		if v.Scope == prefer {
			return v, true
		}
		if !found {
			out, found = v, true
		}
	}
	return out, found
}
