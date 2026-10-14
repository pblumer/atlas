package infomodel

// The run-time twin of a class's lifecycle (ADR-0259 §4).
//
// A class declares the states its instances move through; an instance's data object
// carries a durable trail of every state it was actually written into, with the
// element that wrote it. Both were already on disk. What was missing was reading one
// on top of the other — which is the same relationship the object diagram has to the
// class diagram, and the reason UML was the right notation in the first place: the
// standard already draws the type and the instance as two pictures, which is exactly
// Atlas's design-time/run-time line.
//
// Nothing here is a new fact. No event, no record type, no migration, and nothing in
// `applyToState` — this is a read over what the log already says.

// TrailEntry is one durable write to a data object, as the Data tab already reports
// it: the state the object held afterwards, when, and the BPMN element that made the
// write. By is empty where no element can be named — the seeding at instance
// creation, a write recorded before attribution existed, or an element whose
// definition is gone.
type TrailEntry struct {
	State string
	At    int64
	By    string
}

// TracedState is one declared state with what this instance did in it. The declared
// half is embedded rather than copied so a state gains a field here the day the
// document gains one.
type TracedState struct {
	LifecycleState
	// Visited is whether the object was ever written into this state, Current whether
	// it is the state the last write left it in.
	Visited bool `json:"visited"`
	Current bool `json:"current"`
	// Entered counts the times the object arrived here — twice for a state it went
	// round to a second time. FirstAt and LastAt answer different questions and
	// neither substitutes for the other: when it first got here, and when it last did.
	Entered int   `json:"entered,omitempty"`
	FirstAt int64 `json:"firstAt,omitempty"`
	LastAt  int64 `json:"lastAt,omitempty"`
}

// TracedTransition is one declared transition with what this instance did with it.
type TracedTransition struct {
	LifecycleTransition
	// Taken counts the moves this instance made that this transition accounts for.
	Taken int `json:"taken"`
	// LastAt and LastBy are the most recent of those moves — the element that made it
	// being the answer no class diagram can give, and the reason to draw this at all.
	LastAt int64  `json:"lastAt,omitempty"`
	LastBy string `json:"lastBy,omitempty"`
	// Ambiguous marks a transition that shares both its ends with another. The trail
	// records states, not transition ids, so where a model declares two ways from one
	// state to the same other, nothing in the record says which was taken. Both are
	// marked and both say so, rather than one being picked and quietly believed.
	Ambiguous bool `json:"ambiguous,omitempty"`
}

// Step is a move the object actually made: it was in From and the next write put it
// in To.
type Step struct {
	From string `json:"from"`
	To   string `json:"to"`
	At   int64  `json:"at"`
	By   string `json:"by,omitempty"`
}

// Trace is one data object's declared lifecycle with its own life drawn on top.
type Trace struct {
	States      []TracedState      `json:"states"`
	Transitions []TracedTransition `json:"transitions"`
	// Current is the state the last write left the object in — the state it is in
	// now, which is not necessarily one the machine declares.
	Current string `json:"current,omitempty"`
	// Undeclared are the moves this instance made that no transition joins. It is the
	// run-time twin of the `data.illegal-transition` deploy check, and it catches what
	// that check cannot see: an instance started before the lifecycle was declared, and
	// a write by a process the check never ran against.
	Undeclared []Step `json:"undeclared,omitempty"`
	// Unknown names the states the object was written into that the machine does not
	// declare — `[aproved]` typed once, which is the failure this whole record exists
	// to make impossible. Each is named once however often it was written: it is one
	// fact about the model, not one per write.
	Unknown []string `json:"unknown,omitempty"`
}

// TraceLifecycle reads a data object's trail against the lifecycle its class
// declares. The whole machine is always returned, not just the parts that were used:
// the overlay is the declared life with the lived one on it, and a picture of only
// what happened would answer a different question.
//
// A class with no lifecycle traces to nothing at all — nil is the normal case and
// stays silent here exactly as it does everywhere else.
func TraceLifecycle(lc *Lifecycle, trail []TrailEntry) Trace {
	var tr Trace
	if lc == nil {
		return tr
	}

	states := make(map[string]int, len(lc.States))
	tr.States = make([]TracedState, 0, len(lc.States))
	for _, st := range lc.States {
		states[st.Name] = len(tr.States)
		tr.States = append(tr.States, TracedState{LifecycleState: st})
	}

	// Transitions indexed by the pair of ends, because that is all the trail can
	// address them by. A pair naming more than one transition is the ambiguity above.
	type pair struct{ from, to string }
	byPair := map[pair][]int{}
	tr.Transitions = make([]TracedTransition, 0, len(lc.Transitions))
	for _, t := range lc.Transitions {
		byPair[pair{t.From, t.To}] = append(byPair[pair{t.From, t.To}], len(tr.Transitions))
		tr.Transitions = append(tr.Transitions, TracedTransition{LifecycleTransition: t})
	}
	for _, idx := range byPair {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			tr.Transitions[i].Ambiguous = true
		}
	}

	seenUnknown := map[string]bool{}
	// prev is the state the object was in before the write being read. It is empty
	// both at the start and after a write that carried no state at all: in either case
	// the next state is an entry into the machine and not a move within it, because
	// reading "" as a state would invent an edge from nowhere.
	prev := ""
	for _, e := range trail {
		tr.Current = e.State
		if e.State == "" {
			prev = ""
			continue
		}
		if i, ok := states[e.State]; ok {
			s := &tr.States[i]
			s.Visited = true
			s.Entered++
			if s.FirstAt == 0 {
				s.FirstAt = e.At
			}
			s.LastAt = e.At
		} else if !seenUnknown[e.State] {
			seenUnknown[e.State] = true
			tr.Unknown = append(tr.Unknown, e.State)
		}
		// A write that leaves the object where it was is a value being corrected, not
		// the object moving. Counting it as a self-loop would say a process ran a
		// transition it never ran.
		if prev != "" && prev != e.State {
			if idx, ok := byPair[pair{prev, e.State}]; ok {
				for _, i := range idx {
					tt := &tr.Transitions[i]
					tt.Taken++
					tt.LastAt = e.At
					tt.LastBy = e.By
				}
			} else {
				tr.Undeclared = append(tr.Undeclared, Step{From: prev, To: e.State, At: e.At, By: e.By})
			}
		}
		prev = e.State
	}

	if tr.Current != "" {
		if i, ok := states[tr.Current]; ok {
			tr.States[i].Current = true
		}
	}
	return tr
}
