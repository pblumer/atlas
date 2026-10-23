package workerquery

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's objects are queries, and a query's axis is its lease: it is handed only
// to a Worker Instance reporting the Worker it names, and its answer is taken only
// with the lease token it went out under. The routes themselves are the worker
// protocol's, reachable under the fail-closed worker scope (ADR-0194, ADR-0297) as the
// job routes are (ADR-0473). Reflection supplies the actual
// set of handlers, so adding a route and forgetting to classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// leaseGated: reachable by a worker-protocol caller, and narrowed to the queries
	// addressed to the Workers it holds, or fenced by the lease it was handed.
	leaseGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var queryGates = []handlerGate{
	{"HandleActivate", leaseGated, "leases only queries naming a Worker the polling Instance reports holding, as the job pull reports its connectors"},
	{"HandleAnswer", leaseGated, "takes an answer only from the Instance the query was leased to, with the lease token it was handed"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range queryGates {
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
		t.Fatalf("handlers with no entry in queryGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range queryGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("queryGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range queryGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
