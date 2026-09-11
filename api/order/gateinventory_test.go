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
				func(*httpapi.Principal, string) (bool, error) { return true, nil })
			theirs := decode[Order](t, do(t, insider.HandlePlace, someone("usr_in"), "POST",
				`{"releaseId":"rel_1","items":["account"]}`))

			// And the same store seen by somebody the catalogue does not admit.
			s := New(loop, store, func() int64 { return 1700 },
				func(string) (catalog.Release, bool, error) { return rel, true, nil },
				func(*httpapi.Principal, string) (bool, error) { return false, nil })

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
