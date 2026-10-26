package formlookup

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area has an object axis, and it is the task's: a directory search is made
// through one task, and only by somebody who may work on it — the question completing
// it asks (ADR-0317), asked by the server's Task collaborator before anything else.
// The Worker a search goes through is a second object, and it is the Worker's editors'
// switch that opens it (ADR-0205, ADR-0473). The editor's
// sources listing is the catalog entry every author may see. Reflection supplies the
// actual set of handlers, so adding a route and forgetting to classify it fails the
// build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted (the modeler role).
	// Nothing narrows it further, because there is no object in it to narrow by.
	roleGated gateKind = iota
	// taskGated: answered only to somebody who may work on the task the request
	// names, and only through a Worker that allows forms to search it.
	taskGated
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var lookupGates = []handlerGate{
	{"HandleQuery", taskGated, "the Task collaborator asks mayWorkTask before the form is read, and usable() refuses a Worker whose editors have not opened it to forms"},
	{"HandleSources", roleGated, "names the remote data sources and the Workers of their types with two switches each — the catalog entry every author may see (ADR-0205), with no endpoint and no credential"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range lookupGates {
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
		t.Fatalf("handlers with no entry in lookupGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range lookupGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("lookupGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range lookupGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
