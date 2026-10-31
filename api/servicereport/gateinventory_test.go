package servicereport

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one). Reflection
// supplies the actual set of handlers, so adding a route and forgetting to classify
// it fails the build. The object axis of each is proved in service_test.go.

type gateKind int

const (
	// scoped: answers only what the caller's own relationship reaches — catalogues
	// they maintain, people who name them as manager, an audience whose reporting
	// group they are in — and answers 404 or an empty report otherwise.
	scoped gateKind = iota
	// ownOnly: answers about the caller and nobody else.
	ownOnly
)

type handlerGate struct {
	name string
	kind gateKind
	why  string
}

var reportGates = []handlerGate{
	{"HandleDeliveryReport", scoped, "only catalogues the caller may edit, counts without names; a stranger gets an empty report, ?catalog= not theirs a 404 (TestTheDeliveryRouteCountsAndAuditsNothing)"},
	{"HandleMyReports", ownOnly, "which reports the caller may open: their own team count and their own reporting groups"},
	{"HandleTeamReport", scoped, "only accounts whose managerId is the caller; 404 when there are none (TestTheReportsWithNamesReachTheirReaders)"},
	{"HandleOrganisationReport", scoped, "only a catalogue whose reporting groups include the caller; 404 otherwise, whether or not it exists (TestTheReportsWithNamesReachTheirReaders)"},
}

func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range reportGates {
		named[g.name] = true
		if g.why == "" {
			t.Errorf("%s has no reason", g.name)
		}
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
		if fn.ConvertibleTo(handler) && !named[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("handlers with no entry in reportGates: %v", missing)
	}
	for _, g := range reportGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("reportGates names %q, which is not a method on this service", g.name)
		}
	}
}
