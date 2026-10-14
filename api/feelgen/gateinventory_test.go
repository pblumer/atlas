package feelgen

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area has no object axis, and the table says so rather than leaving it to be
// inferred: the FEEL assistant stores nothing and reads no record anybody owns or
// shares. A request carries its own conversation and test variables and gets a
// proposal back; the history and favourites live in the author's browser, not here.
// What it does reach is the configured agent Workers, and those are the catalog-entry
// rule's (ADR-0205), applied by the server's closures exactly as for form generation.
// Reflection supplies the actual set of handlers, so adding a route and forgetting to
// classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted (the modeler role).
	// Nothing narrows it further, because there is no object in it to narrow by.
	roleGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var feelGates = []handlerGate{
	{"HandleCapability", roleGated, "names the enabled AI Workers and their models — the catalog entry every author may see (ADR-0205), with no endpoint and no credential"},
	{"HandleGenerate", roleGated, "answers the caller's own conversation and test variables and stores nothing; the only shared thing it reaches is the AI Worker, through the same dial form generation uses"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range feelGates {
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
		t.Fatalf("handlers with no entry in feelGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range feelGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("feelGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range feelGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
