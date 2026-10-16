package userprefs

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object axis is the shortest one in the codebase: a record belongs to
// the signed-in principal, both handlers address *that* record and no other, and
// there is no path parameter, no listing and no admin view through which another
// person's could be named. The gate is therefore not a check a handler performs —
// it is the absence of any way to express somebody else's record, which is worth
// writing down precisely because there is no code to point at.
//
// The one thing a reviewer must keep true: the id comes from the principal. A
// `userId` read from a request body would turn both rows below from selfOnly into
// an unchecked write, and nothing else in the file would have to change for it to
// happen — which is why there is a test for exactly that in service_test.go.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// selfOnly: the handler addresses the authenticated principal's own record,
	// derived from the principal and never from the request. With no principal
	// there is no record to address and the answer is 404.
	selfOnly gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var userPrefsGates = []handlerGate{
	{"HandleGet", selfOnly, "reads the principal's own record; no parameter can name another, and no identity means 404"},
	{"HandleSave", selfOnly, "writes the principal's own record; a userId in the body is not read, so a request cannot address another"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range userPrefsGates {
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
		t.Fatalf("handlers with no entry in userPrefsGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops
	// being read.
	for _, g := range userPrefsGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("userPrefsGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range userPrefsGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
