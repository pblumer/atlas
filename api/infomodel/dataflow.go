package infomodel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// Data-flow checking: what the information model is *for*.
//
// Slice 2 gave a data object's type somewhere to point. This is where the pointing
// starts to mean something. Three questions the model can now answer that BPMN
// alone cannot:
//
//   - Does the type this data object declares exist? `itemSubjectRef="Order"` was,
//     until there was an information model, a string nobody could check.
//   - Does the member this activity writes exist on that type? ADR-0060 let an
//     output association target `customer.name`; nothing said whether a Customer
//     has a name.
//   - Is the object written before it is read? ADR-0053 named this as the payoff of
//     having a type at all: "task *Approve* reads `order` but no upstream element
//     produces it".
//
// The third needs no vocabulary — it is a property of the process graph — so it is
// reported whether or not anybody has modeled their data yet. The first two need
// the model, and say nothing when there is none: a warning on every process in an
// instance that has not started modeling is a warning nobody reads.
//
// **Nothing here refuses a deploy.** These are findings, not gates. A model is
// routinely deployed before the vocabulary it names exists, exactly as it is
// deployed before its connectors exist (ADR-0158), and the author is told now
// rather than by the first token to read a null.

// The rule slugs. Like the compiler's own, they are stable machine names a UI can
// group and filter by without parsing the message.
const (
	// RuleDataUnresolvedType marks a data object whose itemSubjectRef names no class
	// in the owning application's information model.
	RuleDataUnresolvedType = "data.unresolved-type"
	// RuleDataUntyped marks a data object that declares no type at all, in an
	// application that does model its data.
	RuleDataUntyped = "data.untyped"
	// RuleDataUnknownMember marks a write targeting a member the resolved class has
	// no attribute for.
	RuleDataUnknownMember = "data.unknown-member"
	// RuleDataMemberThroughScalar marks a dotted write path that walks through a
	// primitive or an enumeration, which has no members to walk into.
	RuleDataMemberThroughScalar = "data.member-through-scalar"
	// RuleDataMemberThroughCollection marks a dotted write path that walks through a
	// collection attribute — legal to write, but it sets a member of the list value
	// rather than of each element (ADR-0060 leaves list indexing to a follow-up).
	RuleDataMemberThroughCollection = "data.member-through-collection"
	// RuleDataNeverWritten marks a data object an activity reads that nothing in the
	// process ever writes.
	RuleDataNeverWritten = "data.never-written"
	// RuleDataReadBeforeWrite marks a read no writer can precede — the parallel
	// branch that reads what the other branch produces.
	RuleDataReadBeforeWrite = "data.read-before-write"
	// RuleDataUnknownStore marks a <dataStoreReference> naming a store the owning
	// application's information model does not declare.
	RuleDataUnknownStore = "data.unknown-store"
	// RuleDataStoreUnbound marks a store that is modeled but has no Worker behind it,
	// so nothing can actually reach what it holds.
	RuleDataStoreUnbound = "data.store-unbound"
)

// Vocabulary is what an application's information models say, flattened for
// resolution: a class by name, and every attribute it has including the ones it
// inherits. Built once per check rather than walked per lookup, because a check
// resolves the same class once per association.
type Vocabulary struct {
	classes map[string]Class
	// rels indexes each class's associations from its own side, so the object graph
	// can ask "what does an Order relate to" without walking every model's
	// association list per node.
	rels map[string][]relation
	// stores indexes the application's data stores by name — what a process's
	// <dataStore> resolves against.
	stores map[string]DataStore
	// members holds each class's attributes with inherited ones first, so a
	// specialization reads as "everything the general thing has, plus these".
	members map[string][]Attribute
	// modeled reports whether any information model exists at all. It is the
	// difference between "you have not modeled this type" and "you have not started
	// modeling", and only the first is worth saying.
	modeled bool
}

// NewVocabulary flattens the models of one application. A later model wins a name
// clash, matching the order the store lists them in.
func NewVocabulary(models []Model) *Vocabulary {
	v := &Vocabulary{
		classes: map[string]Class{}, members: map[string][]Attribute{},
		rels: map[string][]relation{}, stores: map[string]DataStore{}, modeled: len(models) > 0,
	}
	for _, m := range models {
		for _, c := range m.Classes {
			v.classes[c.Name] = c
		}
	}
	for _, m := range models {
		for _, c := range m.Classes {
			v.members[c.Name] = flattenAttributes(m, c)
		}
		v.indexRelations(m)
		for _, st := range m.Stores {
			v.stores[st.Name] = st
		}
	}
	return v
}

// indexRelations records both sides of every association. An end's role names the
// member on the *opposite* class — the reading the class canvas and the JSON Schema
// projection both use — so the To end's role is a member of the From class, and the
// From end's role a member of the To class. A generalization is not a relation
// between two objects and is skipped: it is a statement about the types.
func (v *Vocabulary) indexRelations(m Model) {
	for _, a := range m.Associations {
		if a.Kind == KindGeneralization {
			continue
		}
		from, okFrom := m.ClassByID(a.From.ClassID)
		to, okTo := m.ClassByID(a.To.ClassID)
		if !okFrom || !okTo {
			continue
		}
		if a.To.Role != "" {
			v.rels[from.Name] = append(v.rels[from.Name], relation{member: a.To.Role, target: to.Name, kind: a.Kind})
		}
		// The reverse side is a reference in both directions except containment: the
		// parts of a composition live inside the whole, so the whole is not a member
		// of a part.
		if a.From.Role != "" && a.Kind != KindComposition {
			v.rels[to.Name] = append(v.rels[to.Name], relation{member: a.From.Role, target: from.Name, kind: a.Kind})
		}
	}
}

// flattenAttributes walks a class's generalization chain, most general first, and
// concatenates the attributes. Validation has already ruled out cycles.
func flattenAttributes(m Model, c Class) []Attribute {
	var chain []Class
	seen := map[string]bool{c.ID: true}
	cur := c
	for {
		var parent *Class
		for _, a := range m.Associations {
			if a.Kind != KindGeneralization || a.From.ClassID != cur.ID {
				continue
			}
			if found, ok := m.ClassByID(a.To.ClassID); ok && !seen[found.ID] {
				parent = found
			}
			break
		}
		if parent == nil {
			break
		}
		seen[parent.ID] = true
		chain = append([]Class{*parent}, chain...)
		cur = *parent
	}
	out := []Attribute{}
	for _, ancestor := range chain {
		out = append(out, ancestor.Attributes...)
	}
	return append(out, c.Attributes...)
}

// Modeled reports whether the application has any information model.
func (v *Vocabulary) Modeled() bool { return v != nil && v.modeled }

// Store resolves a data store by the name a process refers to it with.
func (v *Vocabulary) Store(name string) (DataStore, bool) {
	if v == nil {
		return DataStore{}, false
	}
	st, ok := v.stores[name]
	return st, ok
}

// Class resolves a declared type name.
func (v *Vocabulary) Class(name string) (Class, bool) {
	if v == nil {
		return Class{}, false
	}
	c, ok := v.classes[name]
	return c, ok
}

func (v *Vocabulary) attribute(className, attr string) (Attribute, bool) {
	for _, a := range v.members[className] {
		if a.Name == attr {
			return a, true
		}
	}
	return Attribute{}, false
}

// CheckDataFlow inspects a compiled process against an application's vocabulary and
// returns every finding, in a deterministic order.
//
// It runs at deploy and on the Problems panel's dry run — never at runtime. That is
// invariant I5 as it applies here: whether a write can land is knowable from the
// model and the vocabulary, so it is decided once, at deploy, and the engine keeps
// reading integer indices.
func CheckDataFlow(cp *compiler.CompiledProcess, vocab *Vocabulary) []compiler.Problem {
	if cp == nil {
		return nil
	}
	ps := []compiler.Problem{}
	ps = append(ps, checkDeclaredTypes(cp, vocab)...)
	ps = append(ps, checkMemberWrites(cp, vocab)...)
	ps = append(ps, checkReadOrder(cp)...)
	ps = append(ps, checkDataStores(cp, vocab)...)
	sort.SliceStable(ps, func(a, b int) bool {
		if ps[a].Element != ps[b].Element {
			return ps[a].Element < ps[b].Element
		}
		return ps[a].Rule < ps[b].Rule
	})
	return ps
}

// checkDeclaredTypes resolves every data object's itemSubjectRef.
func checkDeclaredTypes(cp *compiler.CompiledProcess, vocab *Vocabulary) []compiler.Problem {
	if !vocab.Modeled() {
		return nil // nothing to resolve against, so nothing to say
	}
	var ps []compiler.Problem
	for _, do := range cp.DataObjects() {
		name := cp.Intern(do.Name)
		declared := cp.Intern(do.ItemType)
		if declared == "" {
			ps = append(ps, compiler.Problem{
				Severity: compiler.SeverityWarning, Rule: RuleDataUntyped,
				Message: fmt.Sprintf("Data object %q declares no type. This application models its data, so give it an itemSubjectRef naming one of its classes — otherwise nothing says what this datum is, and no write to it can be checked.", name),
			})
			continue
		}
		if _, ok := vocab.Class(declared); !ok {
			ps = append(ps, compiler.Problem{
				Severity: compiler.SeverityWarning, Rule: RuleDataUnresolvedType,
				Message: fmt.Sprintf("Data object %q declares the type %q, and no class of that name is modeled in this application. Model it, or correct the itemSubjectRef — until then this object's writes cannot be checked against anything.", name, declared),
			})
		}
	}
	return ps
}

// checkMemberWrites resolves each output association's member target against the
// class its data object is — ADR-0060's named follow-up.
func checkMemberWrites(cp *compiler.CompiledProcess, vocab *Vocabulary) []compiler.Problem {
	var ps []compiler.Problem
	byName := map[string]compiler.CompiledDataObject{}
	for _, do := range cp.DataObjects() {
		byName[cp.Intern(do.Name)] = do
	}
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		element := cp.ElementBpmnId(id)
		for _, a := range cp.DataOutputAssociations(id) {
			path := cp.Intern(a.TargetPath)
			if path == "" {
				continue // a whole-object write: the value is FEEL, not a member name
			}
			objName := cp.Intern(a.DataObject)
			do, ok := byName[objName]
			if !ok {
				continue // the association's target is not one of this process's objects
			}
			class, ok := vocab.Class(cp.Intern(do.ItemType))
			if !ok {
				continue // untyped or unresolved: already reported, and nothing to check against
			}
			ps = append(ps, checkPath(vocab, class, objName, path, element)...)
		}
	}
	return ps
}

// checkPath walks a dotted member target through the vocabulary.
func checkPath(vocab *Vocabulary, class Class, objName, path, element string) []compiler.Problem {
	segments := strings.Split(path, ".")
	current := class
	for i, seg := range segments {
		attr, ok := vocab.attribute(current.Name, seg)
		if !ok {
			return []compiler.Problem{{
				Element: element, Severity: compiler.SeverityError, Rule: RuleDataUnknownMember,
				Message: fmt.Sprintf("This activity writes %s.%s, and %s has no attribute %q. Add it to the class, or correct the write target.", objName, path, current.Name, seg),
			}}
		}
		if i == len(segments)-1 {
			return nil // the last segment is the member being written; its type is free
		}
		// More segments follow, so this one has to be something with members.
		next, isClass := vocab.Class(attr.Type)
		if !isClass || next.Stereotype == StereotypeEnumeration {
			return []compiler.Problem{{
				Element: element, Severity: compiler.SeverityError, Rule: RuleDataMemberThroughScalar,
				Message: fmt.Sprintf("This activity writes %s.%s, but %s.%s is a %s — it has no members to write inside.", objName, path, current.Name, seg, describeType(attr.Type, isClass)),
			}}
		}
		if mult, known := MultiplicityOf(attr.Multiplicity); known && mult.Collection {
			return []compiler.Problem{{
				Element: element, Severity: compiler.SeverityWarning, Rule: RuleDataMemberThroughCollection,
				Message: fmt.Sprintf("This activity writes %s.%s, and %s.%s is a list. The write sets a member of the list value itself, not of each %s in it — Atlas does not address a list element by index yet.", objName, path, current.Name, seg, next.Name),
			}}
		}
		current = next
	}
	return nil
}

func describeType(typeName string, isClass bool) string {
	if isClass {
		return "closed set of values (" + typeName + ")"
	}
	return typeName + " value"
}

// checkReadOrder answers ADR-0053's question — "reads `order`, but does anything
// upstream produce it?" — over the compiled graph, and needs no vocabulary: it is a
// property of the process.
//
// It is deliberately conservative. A reader is reported only when *no* writer can
// reach it, which is a fact about the graph rather than a guess about a run. A
// writer on a loop's back edge does reach its reader (from the second round on) and
// is therefore not reported, which is the correct answer for a model that is right.
// An activity's own output association writes on *completion*, so it never
// satisfies the read its own input association makes on activation — the one case
// where "reaches" has to mean a path of at least one edge.
func checkReadOrder(cp *compiler.CompiledProcess) []compiler.Problem {
	readers := map[string][]int32{} // object name → nodes reading it
	writers := map[string][]int32{} // object name → nodes writing it
	for id := int32(0); int(id) < cp.NodeCount(); id++ {
		for _, a := range cp.DataInputAssociations(id) {
			name := cp.Intern(a.DataObject)
			readers[name] = append(readers[name], id)
		}
		for _, a := range cp.DataOutputAssociations(id) {
			name := cp.Intern(a.DataObject)
			writers[name] = append(writers[name], id)
		}
	}
	if len(readers) == 0 {
		return nil
	}
	reaches := reachability(cp)

	names := make([]string, 0, len(readers))
	for name := range readers {
		names = append(names, name)
	}
	sort.Strings(names)

	var ps []compiler.Problem
	for _, name := range names {
		for _, reader := range readers[name] {
			preceded := false
			for _, writer := range writers[name] {
				if reaches[writer][reader] {
					preceded = true
					break
				}
			}
			if preceded {
				continue
			}
			element := cp.ElementBpmnId(reader)
			if len(writers[name]) == 0 {
				ps = append(ps, compiler.Problem{
					Element: element, Severity: compiler.SeverityWarning, Rule: RuleDataNeverWritten,
					Message: fmt.Sprintf("This activity reads the data object %q, and nothing in this process writes it. The read yields null — give some activity a data output association to it, or read a variable instead.", name),
				})
				continue
			}
			ps = append(ps, compiler.Problem{
				Element: element, Severity: compiler.SeverityWarning, Rule: RuleDataReadBeforeWrite,
				Message: fmt.Sprintf("This activity reads the data object %q, and no activity that writes it can run first — they are on branches that do not precede this one, or the write happens on completion of this same activity. The read yields whatever %q held before, which is null on the first pass.", name, name),
			})
		}
	}
	return ps
}

// reachability is the transitive closure of "can run after", over sequence flows
// plus the two containment edges a token also travels: into a scope's start events,
// and onto an activity's boundary events.
//
// Paths are of length one or more, so a node reaches itself only when it sits on a
// cycle — which is exactly what distinguishes a loop's writer (it does precede the
// next round's read) from an activity's own output association (it does not precede
// its own input).
func reachability(cp *compiler.CompiledProcess) []map[int32]bool {
	n := cp.NodeCount()
	succ := make([][]int32, n)
	for id := int32(0); int(id) < n; id++ {
		// Outgoing yields *flow* ids, not node ids, so each has to be read through to
		// its target — the one place these two index spaces meet.
		for _, flow := range cp.Outgoing(id) {
			succ[id] = append(succ[id], cp.Flow(flow).Target)
		}
		succ[id] = append(succ[id], cp.ScopeStartEvents(id)...)
		succ[id] = append(succ[id], cp.BoundaryEvents(id)...)
	}
	out := make([]map[int32]bool, n)
	for start := int32(0); int(start) < n; start++ {
		seen := map[int32]bool{}
		stack := append([]int32(nil), succ[start]...)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if int(cur) < 0 || int(cur) >= n || seen[cur] {
				continue
			}
			seen[cur] = true
			stack = append(stack, succ[cur]...)
		}
		out[start] = seen
	}
	return out
}

// checkDataStores resolves every <dataStoreReference> the process names against the
// application's information model.
//
// A store is where a class's instances outlive the process that made them, and it is
// declared once for an application rather than in each process that reaches it — so a
// process naming one is making a claim about something outside itself, and this is
// where that claim is checked. Both findings are warnings: a diagram is routinely
// drawn before the store it names is modeled, and a store is routinely modeled before
// somebody configures the Worker behind it. Those are different days' work.
func checkDataStores(cp *compiler.CompiledProcess, vocab *Vocabulary) []compiler.Problem {
	stores := cp.DataStores()
	if len(stores) == 0 || !vocab.Modeled() {
		return nil
	}
	var ps []compiler.Problem
	for _, ref := range stores {
		name := cp.Intern(ref.Name)
		element := cp.Intern(ref.ElementId)
		store, ok := vocab.Store(name)
		if !ok {
			ps = append(ps, compiler.Problem{
				Element: element, Severity: compiler.SeverityWarning, Rule: RuleDataUnknownStore,
				Message: fmt.Sprintf("This process reads from the data store %q, and this application declares no store of that name. Model it under Data — a store says which class it holds and which Worker keeps it, and until it exists nothing says what reading from it would mean.", name),
			})
			continue
		}
		if strings.TrimSpace(store.Worker) == "" {
			ps = append(ps, compiler.Problem{
				Element: element, Severity: compiler.SeverityWarning, Rule: RuleDataStoreUnbound,
				Message: fmt.Sprintf("The data store %q holds %s but no Worker backs it yet, so nothing can reach what it keeps. Configure one under Workers and name it on the store.", name, store.Class),
			})
		}
	}
	return ps
}
