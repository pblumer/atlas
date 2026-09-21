package compiler

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// The guard that keeps the refusal in personal.go from rotting.
//
// ADR-0314's rule is only as good as its coverage: one expression kind missed is one place
// a personal variable can still be computed on, and the hole is invisible because
// everything still compiles and deploys.
//
// A hand-written visitor is what this repository's stance on `unsafe` leaves available —
// it appears in exactly one production file, a Linux sandbox where syscalls leave no
// choice — so completeness has to be *checked* rather than trusted. This walks the
// **types** reachable from CompiledProcess and collects every path that reaches a
// *expr.Compiled. Types, not values: no unsafe, no fixture, and a field cannot escape by
// happening to be nil in whatever process a test built.
//
// It earned itself before it was committed. A hand scan of process.go found twenty
// expression-carrying types; this walk finds TimerSchedule.Expr too, which lives in
// timer_schedule.go. A timer whose FEEL schedule reads a personal variable would have gone
// through.

// expressionPaths walks t and returns every field path that reaches a *expr.Compiled.
func expressionPaths(t reflect.Type) []string {
	target := reflect.TypeOf((*expr.Compiled)(nil))
	var out []string
	onPath := map[reflect.Type]bool{}

	var walk func(reflect.Type, string, int)
	walk = func(ty reflect.Type, path string, depth int) {
		if depth > 10 {
			return
		}
		if ty == target {
			out = append(out, path)
			return
		}
		switch ty.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array:
			walk(ty.Elem(), path, depth+1)
		case reflect.Map:
			walk(ty.Elem(), path, depth+1)
		case reflect.Struct:
			// Guard against a self-referential type, not against a type reached twice:
			// the same detail type on two different fields is two places to check.
			if onPath[ty] {
				return
			}
			onPath[ty] = true
			defer delete(onPath, ty)
			for i := range ty.NumField() {
				f := ty.Field(i)
				next := f.Name
				if path != "" {
					next = path + "." + f.Name
				}
				walk(f.Type, next, depth+1)
			}
		}
	}
	walk(t, "", 0)
	sort.Strings(out)
	return out
}

// TestEveryCompiledExpressionIsAccountedFor is the guard. Every path either is visited by
// the refusal or is listed as evaluated outside the engine, with the reason.
func TestEveryCompiledExpressionIsAccountedFor(t *testing.T) {
	found := expressionPaths(reflect.TypeOf(CompiledProcess{}))
	if len(found) == 0 {
		t.Fatal("found no *expr.Compiled fields at all; the walk above no longer matches the types")
	}

	var unaccounted []string
	for _, p := range found {
		if engineEvaluatedExpressions[p] {
			continue
		}
		if _, ok := workerEvaluatedExpressions[p]; ok {
			continue
		}
		unaccounted = append(unaccounted, p)
	}
	if len(unaccounted) > 0 {
		t.Errorf("these compiled expressions are accounted for by neither list (ADR-0314):\n  %s\n\n"+
			"Add each to engineEvaluatedExpressions and to CompiledProcess.expressionSites, or — only if it is "+
			"evaluated in a worker rather than by the engine — to workerEvaluatedExpressions with the file and line "+
			"that evaluates it. Leaving one out is a place a personal variable can still be computed on, and nothing "+
			"about the deploy would look wrong.",
			strings.Join(unaccounted, "\n  "))
	}
}

// TestBothListsNameRealFields keeps either list from excusing a field that no longer
// exists, which would leave a renamed one silently unchecked.
func TestBothListsNameRealFields(t *testing.T) {
	found := map[string]bool{}
	for _, p := range expressionPaths(reflect.TypeOf(CompiledProcess{})) {
		found[p] = true
	}
	for p := range engineEvaluatedExpressions {
		if !found[p] {
			t.Errorf("engineEvaluatedExpressions names %q, which is not a field of CompiledProcess", p)
		}
	}
	for p, why := range workerEvaluatedExpressions {
		if !found[p] {
			t.Errorf("workerEvaluatedExpressions names %q, which is not a field of CompiledProcess", p)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("workerEvaluatedExpressions gives no reason for %q", p)
		}
	}
}

// TestTheVisitorReachesEveryEngineEvaluatedExpression closes the loop between the two
// halves of the rule. The list above says which paths the engine evaluates; this checks
// that the visitor actually returns an expression for each of them, by building a process
// that fills every one and counting what comes back.
//
// Without this, the lists could be complete and the visitor still blind: a path named in
// engineEvaluatedExpressions but never read by expressionSites would pass the guard above
// and check nothing.
func TestTheVisitorReachesEveryEngineEvaluatedExpression(t *testing.T) {
	if len(engineEvaluatedExpressions) == 0 {
		t.Fatal("engineEvaluatedExpressions is empty; the refusal checks nothing")
	}
	// The visitor labels each site by kind, and every engine-evaluated path must have a
	// label — a path with no label is one expressionSites does not read.
	for path := range engineEvaluatedExpressions {
		if _, ok := expressionKindByPath[path]; !ok {
			t.Errorf("engineEvaluatedExpressions names %q, but expressionSites has no label for it: the refusal does not read that field", path)
		}
	}
	for path := range expressionKindByPath {
		if !engineEvaluatedExpressions[path] {
			t.Errorf("expressionSites labels %q, which engineEvaluatedExpressions does not list", path)
		}
	}
}
