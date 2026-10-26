package policy

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area has no object axis, and the table says so rather than leaving it to be
// inferred: an authorization policy governs the whole server — every governed Worker's
// every action — so it is organisation-wide by construction, with no owner and no
// member list. Every route is administrator-only in the route table, and nothing
// narrows it further, because a policy an administrator could not see is a policy
// nobody could review. Reflection supplies the actual set of handlers, so adding a
// route and forgetting to classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted (administrators),
	// and not narrowed further.
	roleGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var policyGates = []handlerGate{
	{"HandleList", roleGated, "every policy of the server; administrators review all of them or the set cannot be reviewed"},
	{"HandleGet", roleGated, "one policy with its immutable versions; organisation-wide like the list"},
	{"HandleSaveDraft", roleGated, "a draft decides nothing until published; administrators only"},
	{"HandlePublish", roleGated, "publishing changes what every governed action may do; administrators only"},
	{"HandleSetEnabled", roleGated, "switching a policy changes the set every governed action is decided by; administrators only"},
	{"HandleSimulate", roleGated, "evaluates a request an administrator imagines against the same organisation-wide set; records nothing"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range policyGates {
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
		t.Fatalf("handlers with no entry in policyGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range policyGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("policyGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range policyGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
