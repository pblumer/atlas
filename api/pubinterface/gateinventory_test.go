package pubinterface

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object is a published interface, and its axis is two-fold. The
// publishing application's sharing scope (ADR-0071) decides who administers it:
// a viewer reads the binding, an editor publishes, rebinds and deprecates, the
// owner grants and reads the audit. The interface's own grants decide who outside
// that application may read it (public, view); send is answered to the peer
// endpoint of ADR-0372 and to no route here. An interface the caller may not see
// answers 404 exactly as one that does not exist, and the binding — element ids,
// message names, the process — is answered to the application's members alone.
// Every check runs in the loop turn that reads or writes.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// visibleRead: answers only interfaces the caller may see (public, view grant or
	// a member of the publishing application); the binding only to members.
	visibleRead gateKind = iota
	// memberRead: reads a deployed definition, for a viewer of its application.
	memberRead
	// editorWrite: authentication required; an editor of the definition's
	// application, checked with the write in one loop turn.
	editorWrite
	// ownerOnly: the application owner; a write must come from their own session.
	ownerOnly
)

type handlerGate struct {
	name string
	kind gateKind
	why  string
}

var pubInterfaceGates = []handlerGate{
	{"HandleList", visibleRead, "lists only the interfaces the caller may see; processId and applicationId only for members"},
	{"HandleGet", visibleRead, "one interface the caller may see, else 404; binding for members, grants for the owner"},
	{"HandleGetVersion", visibleRead, "one contract version, visible as its interface is"},
	{"HandleCandidates", memberRead, "derives from one definition for a viewer of the application it is filed under; 404 to anybody else"},
	{"HandlePublish", editorWrite, "publishes from a definition the caller edits, into an interface of the same application"},
	{"HandleRebind", editorWrite, "rebinds to a definition of the same process and application the caller edits"},
	{"HandleDeprecate", editorWrite, "deprecates a version of an interface whose application the caller edits"},
	{"HandleHistory", ownerOnly, "the audit names people and audiences; the application owner's"},
	{"HandleSetGrants", ownerOnly, "replaces view and send from the owner's own session, every id checked in the same turn"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range pubInterfaceGates {
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
		t.Fatalf("handlers with no entry in pubInterfaceGates: %v", missing)
	}
	for _, g := range pubInterfaceGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("pubInterfaceGates names %q, which is not a method on this service", g.name)
		}
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
