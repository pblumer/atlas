// Package differential is the conformance suite's cross-engine oracle: it runs the
// same process on Atlas and on an independent reference BPMN engine and compares a
// normalized outcome, so a control-flow bug shows up as disagreement with a second
// implementation — the one oracle the suite's other checks (golden, replay,
// invariants, metamorphic) can't provide, since they all trust Atlas alone.
//
// The reference engine is Node's bpmn-engine (github.com/paed01/bpmn-engine), a
// mature, independent BPMN 2.0 executor, driven by the runner under reference/.
// Because Atlas's executable dialect (zeebe: extensions, FEEL) is not portable, the
// reference models under reference/models/ are hand-translations that keep the same
// element ids and control flow but express scripts and conditions in the reference
// dialect (JavaScript). The set of translated scenarios is a control-flow subset;
// see README.md.
//
// The live comparison lives in differential_test.go behind the `differential` build
// tag, so it stays out of the default `go test ./...` and the coverage gate — it
// needs Node and an `npm ci` in reference/. This file holds only the pure, tested
// projection logic.
package differential

import "sort"

// Projection is the engine-independent outcome compared across engines: whether the
// instance completed, and the set of activities that ran. Variables are excluded on
// purpose — engines format values differently (Atlas FEEL vs. the reference's
// JavaScript), so the reliable cross-engine signal is the control-flow set, not the
// data.
type Projection struct {
	Completed  bool
	Activities []string
}

// Project normalizes a raw (completed, activities) pair: the activity list is
// deduplicated and sorted, so token-visit multiplicity and order — which legitimately
// differ across engines (Atlas records a join once per arriving token; the reference
// may not) — never cause a false mismatch.
func Project(completed bool, activities []string) Projection {
	seen := make(map[string]bool, len(activities))
	uniq := make([]string, 0, len(activities))
	for _, a := range activities {
		if !seen[a] {
			seen[a] = true
			uniq = append(uniq, a)
		}
	}
	sort.Strings(uniq)
	return Projection{Completed: completed, Activities: uniq}
}

// Equal reports whether two projections agree — same completion status and the same
// set of activities.
func (p Projection) Equal(o Projection) bool {
	if p.Completed != o.Completed || len(p.Activities) != len(o.Activities) {
		return false
	}
	for i := range p.Activities {
		if p.Activities[i] != o.Activities[i] {
			return false
		}
	}
	return true
}
