package infomodel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// Deriving the information model from the processes that use it
// (ADR-0301).
//
// ADR-0230 and ADR-0259 both run one way: a person models the vocabulary, and the
// processes are checked against it. Neither priced what that costs before the first
// class exists — a blank page, and until somebody fills it every surface is silent.
//
// This reads the other way. The derived model is what is *built*: the classes the
// processes actually carry, the members they actually write, the states they actually
// reach. What a person models by hand is a different statement — a target, not yet
// reality — so nothing here is ever written into an authored model, and the two are
// never merged. Their difference is the interesting part, and it is the work not yet
// done rather than drift to be reconciled away.
//
// Everything below is a read. No event, no record, no storage.

// The kinds of thing a derivation could not read. They are stable machine names for
// the same reason the rule slugs are: a view groups by them without parsing prose.
const (
	// GapNoBusinessKey is stated once for the whole reading, not per class. Nothing in
	// BPMN says which attribute identifies a thing — it is the single fact ADR-0230
	// exists for, every cross-process capability rests on it, and derivation can never
	// produce it. Every derived class is therefore keyless.
	GapNoBusinessKey = "no-business-key"
	// GapNoAttributeTypes, likewise once: a FEEL expression's result type is not a
	// static fact of the model, so a derived attribute is untyped.
	GapNoAttributeTypes = "no-attribute-types"
	// GapNamedAfterObject marks a class named after the data object because no
	// itemSubjectRef named it — usually the wrong name, and the commonest real case.
	GapNamedAfterObject = "named-after-object"
	// GapStructuredMember marks a member a dotted write path proved has members of its
	// own, whose class nothing in BPMN names.
	GapStructuredMember = "structured-member"
	// GapWholeObjectWrite marks a class some write replaces entirely. The value is a
	// FEEL expression evaluated at run time, so what is *inside* it cannot be read from
	// the model at all — and a reader who takes the resulting empty member list for the
	// class's members concludes that fields the process demonstrably writes are missing.
	GapWholeObjectWrite = "whole-object-write"
)

// Gap is one thing the reading could not see. A gap with no Class is a fact about
// derivation itself and is said once; a gap with one is about that class.
type Gap struct {
	Class string `json:"class,omitempty"`
	Kind  string `json:"kind"`
	Note  string `json:"note"`
}

// Derivation is one application's information model as its processes imply it, with
// what could not be read stated beside it.
//
// The Model is an ordinary Model so the class canvas draws it with no new code, and
// so it can be validated by the same rules an authored one is. It is never saved.
type Derivation struct {
	Model Model `json:"model"`
	Gaps  []Gap `json:"gaps"`
}

// derivedClass accumulates one class across every process that touches it. Order is
// kept — of attributes and of states — because the order a reader meets them in is the
// order the processes reach them, which reads as the life the thing has.
type derivedClass struct {
	name       string
	fromObject bool // named after the data object, because nothing declared a type
	attrs      []string
	attrSeen   map[string]bool
	structured []string
	// wroteWhole records that some write replaced the object entirely rather than
	// naming a member. It is the one thing the empty-path case *does* teach.
	wroteWhole bool
	states     []string
	stateSeen  map[string]bool
	initial    string
	moves      map[[2]string]bool
}

// Derive reads the classes, their members and their lifecycles off a set of compiled
// processes — the application's, as CheckApplication assembles it.
//
// Deriving from nothing is an empty reading and not an empty claim: with no classes
// there is no drawing, and the sentences that qualify a drawing would qualify nothing.
func Derive(cps []*compiler.CompiledProcess) Derivation {
	byName := map[string]*derivedClass{}
	var order []string

	for _, cp := range cps {
		if cp == nil {
			continue
		}
		reaches := reachability(cp)
		for _, do := range cp.DataObjects() {
			object := cp.Intern(do.Name)
			// The type where one is declared; the object's own name otherwise, which is
			// usually the wrong name and is reported as such rather than dressed up.
			name := cp.Intern(do.ItemType)
			fromObject := name == ""
			if fromObject {
				name = object
			}
			if name == "" {
				continue // an object with neither a type nor a name is not a class
			}
			c := byName[name]
			if c == nil {
				c = &derivedClass{
					name: name, fromObject: fromObject,
					attrSeen: map[string]bool{}, stateSeen: map[string]bool{},
					moves: map[[2]string]bool{},
				}
				byName[name] = c
				order = append(order, name)
			}
			// One process declaring the type settles the naming for all of them: the
			// gap is about not knowing, and one process knowing is enough.
			if !fromObject {
				c.fromObject = false
			}

			seeded := cp.Intern(do.InitialState)
			if seeded != "" {
				c.addState(seeded)
				if c.initial == "" {
					c.initial = seeded
				}
			}

			// Every write to this object, with the node that makes it, so the pairs the
			// graph allows can be read off it.
			type write struct {
				node  int32
				state string
			}
			var writes []write
			for id := int32(0); int(id) < cp.NodeCount(); id++ {
				for _, a := range cp.DataOutputAssociations(id) {
					if cp.Intern(a.DataObject) != object {
						continue
					}
					// A path names a member of this class. A write with no path replaces
					// the whole value and says nothing about what is inside it — which
					// addPath refuses on the empty string it interns to. That refusal is
					// itself a fact worth keeping, so it is recorded rather than dropped.
					if path := cp.Intern(a.TargetPath); path == "" {
						c.wroteWhole = true
					} else {
						c.addPath(path)
					}
					if a.TargetState < 0 {
						continue
					}
					state := cp.Intern(a.TargetState)
					c.addState(state)
					writes = append(writes, write{node: id, state: state})
				}
			}

			// A transition is an ordered pair of writes the graph says can follow one
			// another. Two branches of a fork can reach neither the other, so neither is
			// a move from the other — reading the pair as one would invent a path the
			// process does not have.
			for _, w := range writes {
				if seeded != "" && seeded != w.state {
					c.moves[[2]string{seeded, w.state}] = true
				}
				for _, other := range writes {
					if other.node == w.node || other.state == w.state || !reaches[other.node][w.node] {
						continue
					}
					c.moves[[2]string{other.state, w.state}] = true
				}
			}
		}
	}

	var d Derivation
	if len(order) == 0 {
		return d
	}
	for i, name := range order {
		d.Model.Classes = append(d.Model.Classes, byName[name].class(i))
	}
	d.Gaps = append(d.Gaps, Gap{Kind: GapNoBusinessKey,
		Note: "No class here has a business key. Nothing in BPMN says which attribute identifies a thing, " +
			"so it cannot be read from a process — and it is the fact every cross-process capability rests on. " +
			"It is the first thing to add by hand."})
	d.Gaps = append(d.Gaps, Gap{Kind: GapNoAttributeTypes,
		Note: "Attributes are untyped. What a write puts into a member is a FEEL expression, and its result " +
			"type is not a fact of the model."})
	for _, name := range order {
		c := byName[name]
		if c.fromObject {
			d.Gaps = append(d.Gaps, Gap{Class: name, Kind: GapNamedAfterObject,
				Note: fmt.Sprintf("Named after the data object %q, because no itemSubjectRef declared a type. "+
					"The class a person would write is probably spelled differently.", name)})
		}
		if c.wroteWhole {
			d.Gaps = append(d.Gaps, Gap{Class: name, Kind: GapWholeObjectWrite,
				Note: fmt.Sprintf("A write replaces the whole value of %s rather than naming a member, "+
					"so what is inside it cannot be read here. Its members are whatever that expression "+
					"evaluates to at run time — this list is not them.", name)})
		}
		for _, member := range c.structured {
			d.Gaps = append(d.Gaps, Gap{Class: name, Kind: GapStructuredMember,
				Note: fmt.Sprintf("A write targets %q inside %q, so %q has members of its own — but nothing in "+
					"BPMN names the class it is.", member, name, member)})
		}
	}
	return d
}

func (c *derivedClass) addState(name string) {
	if name == "" || c.stateSeen[name] {
		return
	}
	c.stateSeen[name] = true
	c.states = append(c.states, name)
}

// addPath records what a write's target path teaches. Only the first segment is a
// member of *this* class; a deeper path proves that member is structured, which is a
// different and weaker fact than knowing what it is.
func (c *derivedClass) addPath(path string) {
	head, rest, nested := strings.Cut(path, ".")
	// The one refusal, and it covers both cases: a write with no path interns to the
	// empty string, which has no head either.
	if head == "" {
		return
	}
	if !c.attrSeen[head] {
		c.attrSeen[head] = true
		c.attrs = append(c.attrs, head)
	}
	if nested && rest != "" && !contains(c.structured, head) {
		c.structured = append(c.structured, head)
	}
}

func contains(all []string, one string) bool {
	for _, s := range all {
		if s == one {
			return true
		}
	}
	return false
}

// Layout of the derived drawing. There is no author's arrangement to preserve, so one
// is made: a class at 0,0 beside another at 0,0 is a pile rather than a diagram.
const (
	deriveStepX  = 300
	deriveStepY  = 260
	derivePerRow = 4
)

// class renders the accumulation as an ordinary Class, laid out at its index.
func (c *derivedClass) class(i int) Class {
	out := Class{
		ID:         "derived-" + c.name,
		Name:       c.name,
		Stereotype: StereotypeBusinessObject,
		Attributes: make([]Attribute, 0, len(c.attrs)),
		// Keyless, always: see GapNoBusinessKey. Left empty rather than guessed, so
		// nothing downstream can believe a derived class has an identity.
		Identity: nil,
		X:        float64((i % derivePerRow) * deriveStepX),
		Y:        float64((i / derivePerRow) * deriveStepY),
	}
	for _, a := range c.attrs {
		// Untyped and unbounded: see GapNoAttributeTypes. "1" is the multiplicity the
		// document requires, and says only that the member exists.
		out.Attributes = append(out.Attributes, Attribute{Name: a, Multiplicity: "1"})
	}
	if len(c.states) == 0 {
		return out
	}

	lc := &Lifecycle{}
	// Where instances start. Where no object declared one, the first state anything
	// reached stands in — a machine with no start is one the document refuses, and a
	// drawing nobody can open is worse than a start that is merely the earliest known.
	initial := c.initial
	if initial == "" {
		initial = c.states[0]
	}
	for j, name := range c.states {
		lc.States = append(lc.States, LifecycleState{
			Name: name, Initial: name == initial,
			// Final is a statement about intent. A process only shows what nothing
			// happens to do next, which is a different thing, so none is marked.
			X: 0, Y: float64(j * 90),
		})
	}
	moves := make([][2]string, 0, len(c.moves))
	for m := range c.moves {
		moves = append(moves, m)
	}
	// Sorted, because a map's order is not one and a drawing that reshuffles between
	// two readings of the same processes reads as processes that changed.
	sort.Slice(moves, func(a, b int) bool {
		if moves[a][0] != moves[b][0] {
			return moves[a][0] < moves[b][0]
		}
		return moves[a][1] < moves[b][1]
	})
	for _, m := range moves {
		lc.Transitions = append(lc.Transitions, LifecycleTransition{
			ID: fmt.Sprintf("derived-%s-%s-%s", c.name, m[0], m[1]), From: m[0], To: m[1],
		})
	}
	out.Lifecycle = lc
	return out
}
