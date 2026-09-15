package infomodel

import (
	"fmt"
	"sort"

	"github.com/pblumer/atlas/compiler"
)

// Reading the difference between what is built and what is planned
// (ADR-0310).
//
// ADR-0301 settled that Atlas holds two statements about the same subject and must not
// merge them: what is derived from the processes is what is *built*, what a person
// models by hand is what is *wanted*, and their difference is the work not yet done.
// This reads that difference.
//
// It is stateless, and that is what makes it possible at all. ADR-0301 §4 named a
// blocker — a reconciliation needs an identity for a derived element that survives a
// re-derivation, because it has to remember which change you rejected last time.
// Nothing here remembers anything: both sides are computed fresh and compared by name,
// so there is no identity to keep across anything.
//
// The names are already the mechanism. `itemSubjectRef` resolves a data object's type
// against a class by name (ADR-0230), a write path names a member (ADR-0060), and a
// lifecycle state's name *is* its identity because it is the string every process
// writes (ADR-0259). Nothing new is invented to compare with.

// Which document a finding is about. They are never mixed into one list: a reader acts
// on them differently, so they are counted, grouped and labelled apart.
const (
	// SidePlanned is in the model and in no process — the backlog, and the direction
	// the pair exists for. A class declaring `cancelled` that nothing writes is not a
	// defect; it is a decision taken and not yet implemented.
	SidePlanned = "planned-not-built"
	// SideBuilt is in the processes and in no model. Usually that means write it down;
	// occasionally it means a process is doing something nobody agreed to, which is the
	// more interesting reading and the reason this direction is not dropped.
	SideBuilt = "built-not-described"
)

// What kind of thing differs.
const (
	KindDiffClass      = "class"
	KindDiffMember     = "member"
	KindDiffState      = "state"
	KindDiffTransition = "transition"
)

// DiffFinding is one difference, located precisely enough to act on.
type DiffFinding struct {
	Side  string `json:"side"`
	Kind  string `json:"kind"`
	Class string `json:"class"`
	// Name is the member or the state; From and To are a transition's ends. A finding
	// about the class as a whole carries none of them.
	Name string `json:"name,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Note string `json:"note"`
}

// Diff is the whole reading.
type Diff struct {
	Planned []DiffFinding `json:"planned"`
	Built   []DiffFinding `json:"built"`
	// Excluded is what the comparison did not look at, said where it lists rather than
	// in a footnote: a reader who does not know what was excluded cannot tell a short
	// list from a clean bill.
	Excluded []string `json:"excluded"`
	// Modeled reports whether anything is authored at all. An application that models
	// nothing produces no findings — nothing has been planned, so nothing is missing
	// from the plan.
	Modeled bool `json:"modeled"`
}

// excluded is ADR-0301 §2 read as a rule: derivation cannot see these, so a difference
// in them is a fact about derivation and not about the system. Every one of them would
// otherwise be on every class, for ever — and the first person to find three inventions
// on this list stops reading the other twelve.
var excluded = []string{
	"The business key, which every derived class lacks by construction — nothing in BPMN says which attribute identifies a thing.",
	"Attribute types and multiplicity: what a write puts into a member is a FEEL expression, and its result type is not a fact of the model.",
	"Which states are final. \"Nothing leaves it\" is intent; a process graph shows only what nothing happens to do next.",
	"Associations beyond the containment a dotted write path implies, and documentation, which derivation can never produce.",
}

// Difference reads one application's processes against the model authored for it.
//
// It derives the built side itself rather than taking one, so a caller cannot hand in a
// derivation of one application and a vocabulary of another.
func Difference(cps []*compiler.CompiledProcess, vocab *Vocabulary) Diff {
	d := Diff{Planned: []DiffFinding{}, Built: []DiffFinding{}, Modeled: vocab.Modeled()}
	if !d.Modeled {
		return d
	}
	d.Excluded = excluded

	built := map[string]Class{}
	for _, c := range Derive(cps).Model.Classes {
		built[c.Name] = c
	}

	for _, planned := range vocab.Classes() {
		// An enumeration is machinery of the model — an attribute's type, or the states
		// a lifecycle takes (ADR-0306). No process carries one as a data object, so
		// comparing it would put every enumeration on the backlog for ever.
		if planned.Stereotype == StereotypeEnumeration {
			continue
		}
		b, isBuilt := built[planned.Name]
		if !isBuilt {
			d.Planned = append(d.Planned, DiffFinding{
				Side: SidePlanned, Kind: KindDiffClass, Class: planned.Name,
				Note: "No process in this application carries one. The class is modelled and " +
					"nothing builds it yet."})
			// Reported once as the class rather than once per member it also lacks: the
			// work is "build it", and a list of its parts is the same fact said again.
			continue
		}
		d.comparePresent(planned, b, vocab)
	}

	for _, b := range Derive(cps).Model.Classes {
		if _, isPlanned := vocab.Class(b.Name); isPlanned {
			continue
		}
		d.Built = append(d.Built, DiffFinding{
			Side: SideBuilt, Kind: KindDiffClass, Class: b.Name,
			Note: "The processes carry one and no model describes it. Either write it down, or ask " +
				"what a process is doing that nobody agreed to."})
	}
	return d
}

// comparePresent compares a class both sides know, member by member and state by state.
func (d *Diff) comparePresent(planned, built Class, vocab *Vocabulary) {
	name := planned.Name

	// The business key is excluded, so the attributes that form it are too: nothing
	// writes an identity through a data association, and reporting it would put the one
	// fact derivation can never produce on the backlog of every class.
	key := map[string]bool{}
	for _, k := range planned.Identity {
		key[k] = true
	}
	builtMembers := map[string]bool{}
	for _, a := range built.Attributes {
		builtMembers[a.Name] = true
	}
	for _, a := range vocab.Members(name) {
		if key[a.Name] || builtMembers[a.Name] {
			continue
		}
		d.Planned = append(d.Planned, DiffFinding{
			Side: SidePlanned, Kind: KindDiffMember, Class: name, Name: a.Name,
			Note: fmt.Sprintf("%s declares %s and no process writes it.", name, a.Name)})
	}
	plannedMembers := map[string]bool{}
	for _, a := range vocab.Members(name) {
		plannedMembers[a.Name] = true
	}
	for _, a := range built.Attributes {
		if plannedMembers[a.Name] {
			continue
		}
		d.Built = append(d.Built, DiffFinding{
			Side: SideBuilt, Kind: KindDiffMember, Class: name, Name: a.Name,
			Note: fmt.Sprintf("A process writes %s.%s and the class does not declare it.", name, a.Name)})
	}

	plannedStates, builtStates := map[string]bool{}, map[string]bool{}
	if planned.Lifecycle != nil {
		for _, s := range planned.Lifecycle.States {
			plannedStates[s.Name] = true
		}
	}
	if built.Lifecycle != nil {
		for _, s := range built.Lifecycle.States {
			builtStates[s.Name] = true
		}
	}
	for s := range plannedStates {
		if !builtStates[s] {
			d.Planned = append(d.Planned, DiffFinding{
				Side: SidePlanned, Kind: KindDiffState, Class: name, Name: s,
				Note: fmt.Sprintf("%s may be %s, and no process ever puts one there.", name, s)})
		}
	}
	for s := range builtStates {
		if !plannedStates[s] {
			d.Built = append(d.Built, DiffFinding{
				Side: SideBuilt, Kind: KindDiffState, Class: name, Name: s,
				Note: fmt.Sprintf("A process puts a %s into %s, and the lifecycle does not declare it.", name, s)})
		}
	}

	// A transition is named by its ends, not by its id: an id is documentation, and the
	// pair of state names is what the transition actually says.
	moves := func(c Class) map[[2]string]bool {
		out := map[[2]string]bool{}
		if c.Lifecycle == nil {
			return out
		}
		for _, t := range c.Lifecycle.Transitions {
			out[[2]string{t.From, t.To}] = true
		}
		return out
	}
	plannedMoves, builtMoves := moves(planned), moves(built)
	for m := range plannedMoves {
		// A move out of a state nothing reaches is the same fact as the unreached state,
		// said again. The state is the finding; the moves it carries are not.
		if !builtMoves[m] && builtStates[m[0]] && builtStates[m[1]] {
			d.Planned = append(d.Planned, DiffFinding{
				Side: SidePlanned, Kind: KindDiffTransition, Class: name, From: m[0], To: m[1],
				Note: fmt.Sprintf("%s may go from %s to %s, and no process makes that move.", name, m[0], m[1])})
		}
	}
	for m := range builtMoves {
		if !plannedMoves[m] && plannedStates[m[0]] && plannedStates[m[1]] {
			d.Built = append(d.Built, DiffFinding{
				Side: SideBuilt, Kind: KindDiffTransition, Class: name, From: m[0], To: m[1],
				Note: fmt.Sprintf("A process moves a %s from %s to %s, and the lifecycle does not allow it.",
					name, m[0], m[1])})
		}
	}
	d.sort()
}

// sort keeps two readings of the same inputs the same *list* rather than the same set in
// a new order: the maps above have no order, and a list that reshuffles between two
// readings reads as a system that changed.
func (d *Diff) sort() {
	less := func(fs []DiffFinding) func(a, b int) bool {
		return func(a, b int) bool {
			x, y := fs[a], fs[b]
			if x.Class != y.Class {
				return x.Class < y.Class
			}
			if x.Kind != y.Kind {
				return x.Kind < y.Kind
			}
			if x.Name != y.Name {
				return x.Name < y.Name
			}
			if x.From != y.From {
				return x.From < y.From
			}
			return x.To < y.To
		}
	}
	sort.SliceStable(d.Planned, less(d.Planned))
	sort.SliceStable(d.Built, less(d.Built))
}
