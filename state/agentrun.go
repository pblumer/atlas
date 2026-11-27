package state

import (
	"github.com/pblumer/atlas/model"
)

// --- Agent-run history ---
//
// Every call an agent makes to a language model, and what each tool it called
// returned, is retained here in the order it happened, keyed under the owning process
// instance (ADR-0117, as built by ADR-0566). The shape is the decision
// evaluation's (ADR-0066): the worker did the work off the processor goroutine, the
// handler froze its report into an event, and this writer runs only from applyToState,
// from that event alone — so replay rebuilds the same rows without asking any model
// again (I4/I6).
//
// Beside the history sits one small row per element instance holding what its calls add
// up to. The event already carries those sums, so the fold is two plain writes and never
// a read-modify-write, and the engine weighs another round in one point read.

// agentRunInstancePrefix scans every agent-run fact of one instance, in the order the
// facts were appended.
func agentRunInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfAgentRun)}, piKey)
}

// agentRunElementPrefix scans the facts of one agent — an ai task or a container, with
// what its tools returned — in the order they happened. The element instance sits in the
// key before the time so that building a round's transcript reads exactly that agent's
// facts and nothing of the instance's others: an instance running a thousand agents in a
// multi-instance activity must not make each of their rounds scan all of them.
func agentRunElementPrefix(piKey, elKey uint64) []byte {
	return appendBE64(agentRunInstancePrefix(piKey), elKey)
}

func keyAgentRun(piKey, elKey uint64, ts int64, pos uint64) []byte {
	return appendBE64(appendOrderedInt64(agentRunElementPrefix(piKey, elKey), ts), pos)
}

// agentRunTotalInstancePrefix covers the totals of every agent of one instance, so the
// instance purge drops them with the history they summarise.
func agentRunTotalInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfAgentRunTotal)}, piKey)
}

func keyAgentRunTotal(piKey, elKey uint64) []byte {
	return appendBE64(agentRunTotalInstancePrefix(piKey), elKey)
}

// RecordAgentRun folds one AgentRun fact. ts and pos come from the event header. A model
// call also overwrites its element instance's totals with the sums the event carries; a
// tool result is history only.
func (t *Tx) RecordAgentRun(ts int64, pos uint64, v *model.AgentRunValue) error {
	if v.ProcessInstanceKey == 0 {
		return nil
	}
	if err := t.b.Set(keyAgentRun(v.ProcessInstanceKey, v.ElementInstanceKey, ts, pos), t.encodeValue(v), nil); err != nil {
		return err
	}
	if v.Kind != model.AgentRunModelCall || v.ElementInstanceKey == 0 {
		return nil
	}
	t.scratch = model.AppendAgentRunTotal(t.scratch[:0], model.AgentRunTotal{
		Rounds:       v.Round,
		InputTokens:  v.SumInputTokens,
		OutputTokens: v.SumOutputTokens,
		Limit:        v.Limit,
	})
	return t.b.Set(keyAgentRunTotal(v.ProcessInstanceKey, v.ElementInstanceKey), t.scratch, nil)
}

// AgentRunTotal reads what an element instance's model calls add up to so far, through
// the in-flight transaction. An element that has made no call reads as the zero total.
func (t *Tx) AgentRunTotal(piKey, elKey uint64) (model.AgentRunTotal, error) {
	raw, ok, err := getCopy(t.b, keyAgentRunTotal(piKey, elKey))
	if err != nil || !ok {
		return model.AgentRunTotal{}, err
	}
	return model.DecodeAgentRunTotal(raw), nil
}

// AgentRunTotal is the committed counterpart of Tx.AgentRunTotal, for a reader building
// the next round off the processor.
func (q queries) AgentRunTotal(piKey, elKey uint64) (model.AgentRunTotal, error) {
	raw, ok, err := getCopy(q.r, keyAgentRunTotal(piKey, elKey))
	if err != nil || !ok {
		return model.AgentRunTotal{}, err
	}
	return model.DecodeAgentRunTotal(raw), nil
}

// AgentRunsOf folds the facts of one agent — the model calls of an element instance and
// what its tools returned — in the order they happened.
func (q queries) AgentRunsOf(piKey, elKey uint64, fn func(ts int64, pos uint64, v *model.AgentRunValue) error) error {
	return q.scanPrefix(agentRunElementPrefix(piKey, elKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTAgentRun, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromDecisionEvalKey(k), positionFromDecisionEvalKey(k), v.(*model.AgentRunValue))
	})
}

// AgentRunHistory folds the retained agent-run facts of one process instance, calling fn
// with each fact's event timestamp and log position. The facts come agent by agent, each
// agent's in the order they happened; a caller that wants the instance's own order sorts
// by position.
func (q queries) AgentRunHistory(piKey uint64, fn func(ts int64, pos uint64, v *model.AgentRunValue) error) error {
	return q.scanPrefix(agentRunInstancePrefix(piKey), func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTAgentRun, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromDecisionEvalKey(k), positionFromDecisionEvalKey(k), v.(*model.AgentRunValue))
	})
}

// EachAgentRun folds every retained agent-run fact across all instances, in key order:
// by instance, then by agent, then in the order the facts happened. It scans the whole family, so it
// grows with the history and must not be called on the run loop.
func (q queries) EachAgentRun(fn func(ts int64, pos uint64, v *model.AgentRunValue) error) error {
	return q.scanPrefix([]byte{byte(cfAgentRun)}, func(k, raw []byte) error {
		v, err := model.DecodeValue(model.VTAgentRun, raw)
		if err != nil {
			return err
		}
		return fn(timestampFromDecisionEvalKey(k), positionFromDecisionEvalKey(k), v.(*model.AgentRunValue))
	})
}
