package startfavourites

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object axis is as short as the display preferences': a record belongs
// to the signed-in principal, every handler addresses *that* record and no other,
// and the one path parameter names a process, never a person. There is no listing
// and no admin view through which another account's marks could be named. The gate
// is therefore not a check a handler performs — it is the absence of any way to
// express somebody else's record, which is worth writing down precisely because
// there is no code to point at.
//
// The one thing a reviewer must keep true: the id comes from the principal. A user
// id read from the request would turn every row below from selfOnly into an
// unchecked read or write, and nothing else in the file would have to change for it
// to happen.

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

var startFavouritesGates = []handlerGate{
	{"HandleGet", selfOnly, "reads the principal's own record; no parameter can name another, and no identity means 404"},
	{"HandleMark", selfOnly, "adds to the principal's own record; the path names a process, never a person"},
	{"HandleUnmark", selfOnly, "removes from the principal's own record; the path names a process, never a person"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range startFavouritesGates {
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
		t.Fatalf("handlers with no entry in startFavouritesGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops
	// being read.
	for _, g := range startFavouritesGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("startFavouritesGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range startFavouritesGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
