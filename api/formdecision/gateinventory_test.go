package formdecision

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object is a deployed decision, and a deployed decision has no member
// list: it is engine-wide runtime state, whose whole model any signed-in caller reads
// through GET /api/v1/decision-deployments/{key}/xml. Both handlers are therefore gated
// by role alone, and what keeps the options route from being more than that read is the
// refusal of a decision that takes inputs: nothing is evaluated against values the
// caller chose. Reflection supplies the actual set of handlers, so adding a route and
// forgetting to classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// roleGated: reachable by anybody the role boundary admitted. Nothing narrows it
	// further, because the object it reads is readable by the same role already.
	roleGated gateKind = iota
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var decisionGates = []handlerGate{
	{"HandleList", roleGated, "names the deployed decisions without inputs, each by id, name and model — what the decision-deployments listing already shows every signed-in caller, for the form editor (modeler role)"},
	{"HandleOptions", roleGated, "evaluates the newest deployment of one decision without inputs — a function of a model the same caller may read whole — and refuses a decision that takes inputs, so no value of the caller's choosing reaches an evaluation"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range decisionGates {
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
		t.Fatalf("handlers with no entry in decisionGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range decisionGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("decisionGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range decisionGates {
		if g.why == "" || g.kind != roleGated {
			t.Errorf("%s carries no reason, or a kind this area does not have", g.name)
		}
	}
}
