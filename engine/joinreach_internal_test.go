package engine

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestTokenCanStillReachReadsOnlyActivations pins what an inclusive join counts as
// "a token still in flight". The scan walks the rest of the batch's queue and the
// followups generated so far, and those hold every kind of command — a job
// completion, a timer firing, a message. Only an element *activation* carries a
// token toward a node; everything else has a zero element payload, and reading it
// as one would let an unrelated command hold a join open forever.
//
// It also checks the scope filter on the same scan: an activation for a sibling
// execution of the same subprocess is on the same node ids and can never arrive
// here (ADR-draft-join-scope-identity).
func TestTokenCanStillReachReadsOnlyActivations(t *testing.T) {
	s := openStore(t)
	p := &Processor{store: s, clock: &wbClock{}}
	tx := s.NewTransaction()
	defer tx.Close()
	c := &ProcessingContext{p: p, tx: tx}

	const proc, scope, sibling = uint64(1), uint64(10), uint64(20)
	const join, upstream = int32(3), int32(7)
	reaches := map[int32]bool{upstream: true}
	p.batchPos = -1 // so queue[batchPos+1:] is the whole queue

	activation := func(scopeKey uint64, node int32) Command {
		return Command{
			ValueType: model.VTElementInstance,
			Intent:    model.IntentActivating,
			Value: inflightValue{element: model.ElementInstanceValue{
				ProcessInstanceKey: proc, FlowScopeKey: scopeKey, ElementId: node,
			}},
		}
	}

	p.queue = []Command{{ValueType: model.VTJob, Intent: model.IntentJobCompleted, Key: 99}}
	if c.TokenCanStillReach(proc, scope, join, reaches) {
		t.Error("a queued job completion was read as a token in flight")
	}

	p.queue = []Command{activation(sibling, upstream)}
	if c.TokenCanStillReach(proc, scope, join, reaches) {
		t.Error("an activation in a sibling scope was read as a token that can arrive here")
	}

	p.queue = nil
	p.followups = []Command{activation(scope, upstream)}
	if !c.TokenCanStillReach(proc, scope, join, reaches) {
		t.Error("an activation upstream in this scope was not seen")
	}
}
