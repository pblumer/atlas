package startgrant

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object is an application/process scope and the audience a grant
// names. The route table declares every handler admin-only, so the role axis is
// the whole question of *who* may read or change a grant: there is no member list
// on a grant, no owner, and no viewer — an application's owner or editor reaches
// none of these routes. What the handlers add is the rest of ADR-0516: nothing at
// all on a server without authentication, a change only from the administrator's
// own session, and a change only to an application that exists, a process whose
// newest version it holds, and identities that exist — checked in the loop turn
// that writes.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// adminRead: admin-only by the route table; refused without authentication;
	// reads the scope named by the path and nothing about any other application.
	adminRead gateKind = iota
	// adminWrite: as adminRead, and the actor must be a person's own session.
	adminWrite
)

type handlerGate struct {
	name string
	kind gateKind
	why  string
}

var startGrantGates = []handlerGate{
	{"HandleList", adminRead, "lists the grants of the one application the path names; ids only, no names"},
	{"HandleGet", adminRead, "reads the one scope the path names; an unknown application without a record is 404"},
	{"HandleHistory", adminRead, "reads the audit trail of the one scope the path names"},
	{"HandlePreview", adminRead, "evaluates the current grant for one named account or group; writes nothing"},
	{"HandlePut", adminWrite, "issues a grant from the administrator's own session after checking application, process and identities in the same loop turn"},
	{"HandleDelete", adminWrite, "revokes from the administrator's own session; never needs the application or process to exist"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range startGrantGates {
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
		t.Fatalf("handlers with no entry in startGrantGates: %v", missing)
	}
	for _, g := range startGrantGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("startGrantGates names %q, which is not a method on this service", g.name)
		}
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
