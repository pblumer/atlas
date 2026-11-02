package mihistory

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// The multi-instance history has exactly the object axis of the instance reads it
// sits beside — GET /api/v1/instances/{key}/timeline, /variables, /decisions — and
// today that axis is the role: every one of those routes is reachable by an
// Operator for any instance key, and none is narrowed per instance or per process.
// These routes must not reach further than the timeline does, and they do not: they
// read a subset of what the timeline already returns (keys, log positions,
// lifecycle states and incident attributions), never more than those routes already return. Should the
// instance reads gain a per-instance gate, these handlers have to take the same
// one, which is why they are listed here rather than left to be inferred.
// Reflection supplies the actual set of handlers, so adding a route and forgetting
// to classify it fails the build.

type gateKind int

const (
	// roleGated: reachable by an Operator for any instance, like the timeline.
	roleGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	why  string
}

var miHistoryGates = []handlerGate{
	{"HandleBodies", roleGated, "the multi-instance bodies of one instance: keys, positions, states and counters the timeline already exposes"},
	{"HandleBody", roleGated, "one body of one instance, with incident counts the instance's incident list already exposes"},
	{"HandleIterations", roleGated, "iteration keys, numbers, states and incident counts of one body; no variable values"},
	{"HandleIteration", roleGated, "one iteration's incidents, nested bodies, its own scope's variables and its decisions — what /timeline and /decisions already show an Operator for the same instance"},
	{"HandleOutline", roleGated, "an instance's steps outside its MI iterations: a subset of the rows /timeline returns"},
}

func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range miHistoryGates {
		named[g.name] = true
	}
	var missing []string
	typ := reflect.TypeOf(&Service{})
	handler := reflect.TypeOf(http.HandlerFunc(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if m.Type.NumIn() != 3 || m.Type.NumOut() != 0 {
			continue
		}
		fn := reflect.FuncOf([]reflect.Type{m.Type.In(1), m.Type.In(2)}, nil, false)
		if !fn.ConvertibleTo(handler) {
			continue
		}
		if !named[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("handlers with no entry in miHistoryGates: %v", missing)
	}
	for _, g := range miHistoryGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("miHistoryGates names %q, which is not a method on this service", g.name)
		}
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
