package releasenotes

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area has no object axis, and the table says so rather than leaving it to be
// inferred: the release notes are the binary's own CHANGELOG, the same text for every
// caller, with no record anybody owns or shares. Both routes are reachable by anybody
// the role boundary let through, and nothing narrows them further. Reflection supplies
// the actual set of handlers, so adding a route and forgetting to classify it fails
// the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted. The notes are not
	// narrowed further, because there is nothing per-caller in them to narrow.
	roleGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var releaseNotesGates = []handlerGate{
	{"HandleList", roleGated, "the releases of the CHANGELOG this binary embeds; the same list for every signed-in caller"},
	{"HandleGet", roleGated, "one release of that same CHANGELOG; nothing in it belongs to a caller"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range releaseNotesGates {
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
		t.Fatalf("handlers with no entry in releaseNotesGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range releaseNotesGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("releaseNotesGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range releaseNotesGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
