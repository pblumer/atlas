package order

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The same inventory the catalogue keeps, for the same reason: per-route tests
// prove what their author thought of, and cannot prove that a new route was
// thought about at all. Every handler here is named, either as gated — an
// outsider is refused, and the test asks rather than reading — or as deliberately
// not, with the reason.

type gateKind int

const (
	gated gateKind = iota
	ungated
)

type handlerGate struct {
	name   string
	kind   gateKind
	want   int
	method string
	body   string
	id     bool
	why    string
}

var orderGates = []handlerGate{
	{name: "HandlePlace", kind: gated, want: http.StatusForbidden, method: "POST",
		body: `{"releaseId":"rel_1","items":["account"]}`},
	{name: "HandleGet", kind: gated, want: http.StatusNotFound, method: "GET", id: true},
	{name: "HandleNext", kind: ungated, method: "GET", id: true,
		why: "orchestrator work, gated by the operator role rather than by the object: an operator drives orders that are not theirs, which is what the role is for (ADR-0209). It exposes which lines are ready and nothing about who ordered them"},
	{name: "HandleReport", kind: ungated, method: "POST", id: true,
		why: "the other half of the same pair, and the same reasoning: nobody reports the result of their own provisioning. It accepts only the two outcomes a provisioning attempt produces — a rejection and an abandonment are decisions with an author and have their own transitions"},
	{name: "HandleDecide", kind: ungated, method: "POST", id: true,
		why: "an approver's refusal, reaching the order through an approval process the catalogue bound. Gated by the operator role like the orchestrator pair, and by the transition itself, which will not record a refusal without naming who decided and why — the authority that matters here is the approver's, and it is carried in the call rather than held by the caller"},
	{name: "HandleList", kind: ungated, method: "GET",
		why: "returns only the caller's own orders, so it names no object to gate; somebody else's simply is not in it, proved in TestAnOrderIsReadBackByItsOwner"},
}

// TestEveryOrderHandlerIsClassified is the drift half.
func TestEveryOrderHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range orderGates {
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
		if !reflect.FuncOf([]reflect.Type{m.Type.In(1), m.Type.In(2)}, nil, false).ConvertibleTo(handler) {
			continue
		}
		if !named[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("handlers with no entry in orderGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down.", missing)
	}
	for _, g := range orderGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("orderGates names %q, which is not a handler on this service", g.name)
		}
	}
}

// TestEveryGatedOrderHandlerRefusesAnOutsider is the behaviour half.
//
// Placing is 403 rather than 404: the outsider named a release they were given,
// and the catalogue it belongs to is not a secret they learned here. Reading
// somebody else's order is 404, because its existence is not theirs to learn.
func TestEveryGatedOrderHandlerRefusesAnOutsider(t *testing.T) {
	for _, g := range orderGates {
		if g.kind != gated {
			continue
		}
		t.Run(g.name, func(t *testing.T) {
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			quit := make(chan struct{})
			loop := runloop.New(quit)
			go loop.Run()
			t.Cleanup(func() { close(quit) })

			rel := testRelease(t)
			// Somebody else's order exists, placed by an insider.
			insider := New(loop, store, func() int64 { return 1700 },
				func(string) (catalog.Release, bool, error) { return rel, true, nil },
				func(*httpapi.Principal, string) (bool, error) { return true, nil },
				func(message, orderID string, vars map[string]string) error { return nil },
				func() string { return "https://atlas.example.ch" })
			theirs := decode[Order](t, do(t, insider.HandlePlace, someone("usr_in"), "POST",
				`{"releaseId":"rel_1","items":["account"]}`))

			// And the same store seen by somebody the catalogue does not admit.
			s := New(loop, store, func() int64 { return 1700 },
				func(string) (catalog.Release, bool, error) { return rel, true, nil },
				func(*httpapi.Principal, string) (bool, error) { return false, nil },
				func(message, orderID string, vars map[string]string) error { return nil },
				func() string { return "https://atlas.example.ch" })

			h := reflect.ValueOf(s).MethodByName(g.name).
				Interface().(func(http.ResponseWriter, *http.Request))

			outsider := someone("usr_out")
			var rec = do(t, h, outsider, g.method, g.body)
			if g.id {
				rec = do(t, h, outsider, g.method, g.body, "id", theirs.ID)
			}
			if rec.Code != g.want {
				t.Fatalf("%s gave an outsider %d (%s), want %d",
					g.name, rec.Code, rec.Body, g.want)
			}
		})
	}
}

func TestEveryUngatedOrderHandlerSaysWhy(t *testing.T) {
	for _, g := range orderGates {
		if g.kind == ungated && g.why == "" {
			t.Errorf("%s is ungated with no reason given", g.name)
		}
		if g.kind == gated && g.why != "" {
			t.Errorf("%s is gated but carries an exemption reason", g.name)
		}
	}
}
