package openapimocks

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// A mock API has no owner and nobody shares one: it is server-wide configuration, in
// the same category as a managed Worker record, so the management handlers are gated on
// the role boundary and nothing narrows them further. There is one handler here that is
// gated by *nothing*, and it is the point of the whole area — a mock has to answer a
// caller that presents no credential, or a model would carry one to reach its mock and
// then differ between a mockup run and a real one (ADR-0181). That handler is the one
// line of this table worth re-reading, and what bounds it is written beside it.
//
// Reflection supplies the actual set of handlers, so adding a route and forgetting to
// classify it fails the build.

type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted (the modeler role).
	// There is no object in it to narrow by.
	roleGated gateKind = iota
	// ungated: served to an unauthenticated caller, deliberately.
	ungated
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var mockGates = []handlerGate{
	{"HandleList", roleGated, "lists this server's own mock APIs and the documents' descriptions; a mock belongs to the installation, not to an account, so there is nothing to narrow by"},
	{"HandleCreate", roleGated, "stores a document the caller uploaded under a name of their choosing; it reaches no other record"},
	{"HandleUpdate", roleGated, "addresses one mock by its own id and changes only that record"},
	{"HandleDelete", roleGated, "addresses one mock by its own id and removes only that record and its document"},
	{"HandleDocument", roleGated, "returns the document a modeler uploaded, which every modeler may already read through the list"},
	{"HandleMock", ungated, "a mock must authenticate nobody (ADR-0181), so this is public. What bounds it: it serves only a document an authenticated operator uploaded *and* switched on, a name that is not being served is indistinguishable from one that was never stored, it reads and writes no engine state, and the journal — the one part holding data a real instance sent — is refused here and read through the authenticated Mockups view instead"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range mockGates {
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
		t.Fatalf("handlers with no entry in mockGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	for _, g := range mockGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("mockGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range mockGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}

// TestOnlyTheMockItselfIsUngated pins the exception. An area whose public surface grew
// a second handler is an area somebody has to look at again.
func TestOnlyTheMockItselfIsUngated(t *testing.T) {
	for _, g := range mockGates {
		if g.kind == ungated && g.name != "HandleMock" {
			t.Errorf("%s is ungated; only the served mock may be", g.name)
		}
	}
}
