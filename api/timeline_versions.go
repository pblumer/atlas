package api

import (
	"sort"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// A migrated instance's history spans more than one definition (ADR-0162). Element
// records name their element by its *compiled index*, and an index means whatever the
// graph it was compiled against says it means — so a row written before a migration and
// read back through the version the instance is on now names a different element, or
// none. The instance's own record says only where it is now; each migration's operator
// action says which definition it came from, and at which log position. Together those
// are the definition in force at every position in the instance's history.
//
// This is the reader's half of the record ADR-0162 designed: nothing here rewrites
// history, because history is fact. It re-reads it under the version it was written on.

// migrationBoundary is one migration on one instance: the log position of its operator
// action, and the definition the instance ran under *before* it.
type migrationBoundary struct {
	pos  uint64
	from uint64
}

// versionBound is one half-open span of an instance's history: every log position below
// until was recorded against def.
type versionBound struct {
	until uint64
	def   uint64
}

// versionAt resolves a log position to the definition in force at it, and reads element
// indices through that definition's compiled graph.
//
// A definition no longer deployed resolves to no graph, and every read through it
// answers empty rather than guessing. That is a real case rather than a defensive one:
// once an instance migrates away, its old version has no instances left and may be
// deleted outright — and a step labelled with the wrong element is worse than a step
// labelled with none, because only the second is visibly missing.
type versionAt struct {
	bounds []versionBound
	cps    map[uint64]*compiler.CompiledProcess
}

// newVersionAt builds the resolver from an instance's migrations and the definition it
// is on now. migrations need not be sorted. lookup returns the compiled graph of a
// definition key, or nil when it is no longer deployed.
//
// The chain closes on itself: the definition in force before the first migration is that
// migration's "from", the one between migration i and i+1 is migration i+1's "from" — a
// migration's target is by construction the next one's source — and the one after the
// last is the definition the instance record names. No migration record needs to carry
// its target, and none does.
func newVersionAt(migrations []migrationBoundary, current uint64, lookup func(uint64) *compiler.CompiledProcess) *versionAt {
	sorted := append([]migrationBoundary(nil), migrations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].pos < sorted[j].pos })

	v := &versionAt{
		bounds: make([]versionBound, 0, len(sorted)+1),
		cps:    make(map[uint64]*compiler.CompiledProcess, len(sorted)+1),
	}
	for _, m := range sorted {
		v.bounds = append(v.bounds, versionBound{until: m.pos, def: m.from})
	}
	v.bounds = append(v.bounds, versionBound{until: ^uint64(0), def: current})
	for _, b := range v.bounds {
		if _, seen := v.cps[b.def]; !seen {
			v.cps[b.def] = lookup(b.def)
		}
	}
	return v
}

// migrated reports whether this instance crossed a migration at all — the whole
// resolver collapses to the single current definition when it did not.
func (v *versionAt) migrated() bool { return len(v.bounds) > 1 }

// def is the definition key in force at a log position.
func (v *versionAt) def(pos uint64) uint64 {
	i := sort.Search(len(v.bounds), func(i int) bool { return pos < v.bounds[i].until })
	if i == len(v.bounds) { // unreachable: the last bound's until is ^0
		return v.bounds[len(v.bounds)-1].def
	}
	return v.bounds[i].def
}

// cp is the compiled graph in force at a log position, or nil when that definition is
// no longer deployed.
func (v *versionAt) cp(pos uint64) *compiler.CompiledProcess { return v.cps[v.def(pos)] }

// elementID is the BPMN element id an index means at a log position, or "" when the
// definition it was recorded under is gone.
func (v *versionAt) elementID(pos uint64, idx int32) string {
	cp := v.cp(pos)
	if cp == nil {
		return ""
	}
	return cp.ElementBpmnId(idx)
}

// node is the compiled node an index means at a log position, and whether the
// definition it was recorded under is still deployed. Callers that only want a type or
// a count read it through this rather than through the instance's current graph, where
// the same index may be a different element entirely.
func (v *versionAt) node(pos uint64, idx int32) (*compiler.CompiledNode, bool) {
	cp := v.cp(pos)
	if cp == nil {
		return nil, false
	}
	return cp.Node(idx), true
}

// nodeType is the element type an index means at a log position, or "" when unknown.
func (v *versionAt) nodeType(pos uint64, idx int32) string {
	n, ok := v.node(pos, idx)
	if !ok {
		return ""
	}
	return n.Type.String()
}

// isType reports whether the element at an index is of the given type at a log
// position. An unresolvable definition answers false: an element nobody can identify is
// not a call activity, not a subprocess, and not treated as one.
func (v *versionAt) isType(pos uint64, idx int32, t compiler.BpmnType) bool {
	n, ok := v.node(pos, idx)
	return ok && n.Type == t
}

// isLoop reports whether the element at an index is a looping activity — multi-instance
// or a standard loop — at a log position (ADR-0077/0133). An unresolvable definition
// answers false, the same as isType: an element nobody can identify carries no loop.
func (v *versionAt) isLoop(pos uint64, idx int32) bool {
	n, ok := v.node(pos, idx)
	return ok && n.MultiInstance >= 0
}

// flowSource is the element index a flow leaves from at a log position, and whether it
// resolved. Used both to name the element a token came from and to link a deferred
// completion to the activation that succeeds it.
func (v *versionAt) flowSource(pos uint64, flowID int32) (int32, bool) {
	cp := v.cp(pos)
	if cp == nil || flowID < 0 {
		return 0, false
	}
	return cp.Flow(flowID).Source, true
}

// sameElement reports whether two rows — each with its own log position — name the same
// modelled element.
//
// Within one definition that is index equality, which is exact. Across a migration the
// indices come from different graphs and comparing them is meaningless, so the
// comparison falls back to the BPMN element id: the identity a modeler controls, and the
// one migration pairs elements by in the first place. The fallback is deliberately not
// the default — a process built through the builder API rather than parsed from XML has
// no BPMN ids at all, and comparing every element by an empty string would make all of
// them equal.
func (v *versionAt) sameElement(posA uint64, idxA int32, posB uint64, idxB int32) bool {
	if v.def(posA) == v.def(posB) {
		return idxA == idxB
	}
	idA, idB := v.elementID(posA, idxA), v.elementID(posB, idxB)
	return idA != "" && idA == idB
}

// pendingCompletion is one element completion held back until the activation it causes
// appears, together with the log position it was recorded at — which is what says
// which definition its element index should be read through.
type pendingCompletion struct {
	v   state.ElementReplayValue
	pos uint64
}

// isLeaf reports whether an element has no outgoing flow, so nothing will ever activate
// from it and its token must be removed at once rather than deferred.
//
// A definition that is gone answers true. That is the safe direction: a deferred token
// whose successor can never be identified would stay on the frame forever, outliving the
// instance — the ghost ADR-0136 removed. Dropping it early can at worst make a token
// blink out one frame sooner on a replay whose old version was already deleted.
func (v *versionAt) isLeaf(pos uint64, idx int32) bool {
	n, ok := v.node(pos, idx)
	return !ok || n.OutgoingCount == 0
}

// anyHasCallActivities reports whether any definition this instance has run under has a
// call activity — the question that decides whether the caller pays for a full scan of
// child instances. Asking only the current version would miss a call activity that ran
// before a migration, and so drop the drill-in link on exactly the step that has one.
func (v *versionAt) anyHasCallActivities() bool {
	for _, cp := range v.cps {
		if cp != nil && len(cp.CallActivities()) > 0 {
			return true
		}
	}
	return false
}
