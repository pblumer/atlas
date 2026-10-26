package contentsource

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach in this area, in one list (ADR-0209, the
// catalogue's gateinventory_test.go is the worked example).
//
// There is no object axis here, and the list says so handler by handler: a content
// source is installation configuration that names a credential, like a Worker. The
// route table admits administrators alone, and no record carries a member list a
// narrower gate could read. The product manager's door to the sources is the
// catalogue's content routes, which are gated on the product's home catalogue and
// show no credential — inventoried in api/catalog.
var sourceGates = map[string]string{
	"HandleList":   "admin-only configuration; lists every source, which is the administrator's whole view",
	"HandleGet":    "admin-only configuration; no source belongs to anybody narrower than the administrator",
	"HandleCreate": "admin-only configuration; creation has no object yet",
	"HandleUpdate": "admin-only configuration",
	"HandleDelete": "admin-only configuration, and refused while a product is bound to the source",
	"HandleCheck":  "admin-only; reads the technical account's standing, never a token",
}

func TestEveryConfigurationHandlerIsClassified(t *testing.T) {
	typ := reflect.TypeOf(&Service{})
	handler := reflect.TypeOf(http.HandlerFunc(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if m.Type.NumIn() != 3 || m.Type.NumOut() != 0 {
			continue
		}
		fn := reflect.FuncOf([]reflect.Type{m.Type.In(1), m.Type.In(2)}, nil, false)
		if fn.ConvertibleTo(handler) && sourceGates[m.Name] == "" {
			t.Errorf("%s has no entry in sourceGates: say why it needs no object gate, or give it one", m.Name)
		}
	}
	for name := range sourceGates {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("sourceGates names %s, which is no handler", name)
		}
	}
}
