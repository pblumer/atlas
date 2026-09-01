package playground

import (
	"fmt"

	"github.com/pblumer/atlas/state"
)

// ElementUse is how often the run passed through one element of the diagram.
type ElementUse struct {
	Id    string
	Count int64
}

// FlowUse is how often the run took one sequence flow, named by the two elements
// it joins rather than by its own BPMN id.
//
// A compiled flow does not carry the diagram's id: the engine never needs it, and
// putting one there for a Playground feature would add a field to a structure
// every deployment builds. The pair identifies the flow just as well for the only
// caller there is — a client that already holds the diagram and can find the
// connection between two elements in its own registry.
type FlowUse struct {
	From, To string
	Count    int64
}

// HeatMap is how often the run used each part of the root process: every element
// and every sequence flow the model has, including the ones no token ever
// reached.
//
// The zeroes are half the point. "This branch never ran with your data" is a
// statement the report can only make if it lists what it did *not* see, and a
// map drawn from the visit counters alone cannot: those exist only for elements
// a token has been to.
//
// It covers the root process. A call activity's child runs on its own compiled
// graph, whose element indices mean nothing here — and the diagram on screen is
// the root's, which is what a heat map is drawn onto.
type HeatMap struct {
	Elements []ElementUse
	Flows    []FlowUse
}

// HeatMap folds the run into the diagram's shape.
//
// Element counts come from the maintained visit counters (ADR-0080), which are
// O(elements) to read. Flow counts have no counter — the engine aggregates
// elements, not edges — so they are folded out of the causal token history
// (ADR-0136) in a single scan, at the cost of one pass over the run's
// activations rather than one per case.
func (s *Sandbox) HeatMap() (HeatMap, error) {
	visits, err := s.ElementVisits()
	if err != nil {
		return HeatMap{}, err
	}
	flows, err := s.flowCounts()
	if err != nil {
		return HeatMap{}, err
	}

	h := HeatMap{
		Elements: make([]ElementUse, 0, s.root.NodeCount()),
		Flows:    make([]FlowUse, 0, s.root.NodeCount()),
	}
	// Nodes carry the flows leaving them, so one walk names every element and
	// every flow exactly once — a flow leaves exactly one node.
	for n := int32(0); n < int32(s.root.NodeCount()); n++ {
		id := s.root.ElementBpmnId(n)
		if id == "" {
			continue // a node the compiler made that the diagram has no element for
		}
		h.Elements = append(h.Elements, ElementUse{Id: id, Count: visits[id]})
		for _, fid := range s.root.Outgoing(n) {
			f := s.root.Flow(fid)
			from, to := s.root.ElementBpmnId(f.Source), s.root.ElementBpmnId(f.Target)
			if from == "" || to == "" {
				continue
			}
			h.Flows = append(h.Flows, FlowUse{From: from, To: to, Count: flows[fid]})
		}
	}
	return h, nil
}

// flowCounts is how often each sequence flow of the root process carried a
// token, keyed by compiled flow index.
//
// Only activations count, and only the root's own cases: a completion record
// repeats the flow the token arrived on, and a call activity's child indexes its
// flows against a different compiled graph.
func (s *Sandbox) flowCounts() (map[int32]int64, error) {
	keys, err := s.caseKeyList()
	if err != nil {
		return nil, err
	}
	counts := map[int32]int64{}
	// The scan is grouped by instance in ascending key order and keys is sorted
	// the same way, so membership is a walk rather than a set: one cursor, no
	// allocation for fifty thousand cases.
	next := 0
	err = s.store.AllElementReplay(func(piKey uint64, _ int64, _ uint64, v state.ElementReplayValue) error {
		for next < len(keys) && keys[next] < piKey {
			next++
		}
		if next >= len(keys) || keys[next] != piKey {
			return nil // a call activity's child, or a case that is not the root's
		}
		if v.Action == state.ReplayActivated && v.SourceFlowID >= 0 {
			counts[v.SourceFlowID]++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("playground: fold token history: %w", err)
	}
	return counts, nil
}
