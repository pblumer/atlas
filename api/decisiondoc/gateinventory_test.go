package decisiondoc

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's object axis is unusual and worth stating rather than leaving to be
// inferred: a decision documentation version is **not** gated by a member list.
// Every authenticated route here is reachable by anybody the role boundary let
// through, and the one route that is reachable without a login at all is gated by
// an opaque share token that is the whole authorization
// (ADR-0029's mechanism, ADR-draft-decision-documentation).
//
// That is the same posture api/processdoc has, and the reason it carries a
// sentence in areasWithoutAGateInventory instead of a table. A table is better
// than a sentence, so this area writes one: reflection supplies the actual set of
// handlers, so adding a route and forgetting to classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// tokenGated: reachable with no account, and the token in the URL is the whole
	// authorization. Its shape is guarded before the store is touched.
	tokenGated gateKind = iota
	// roleGated: reachable by anybody the role boundary admitted. A decision's
	// documentation is not narrowed further, which is a choice, not an oversight.
	roleGated
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var decisionDocGates = []handlerGate{
	{"HandleCreate", roleGated, "publishing needs the modeler role; there is no per-decision member list to check against"},
	{"HandleList", roleGated, "a decision's documentation history is readable by anybody signed in, like the decision itself"},
	{"HandleGet", roleGated, "same as the listing, for one version"},
	{"HandleGetPDF", roleGated, "same as the listing; the authenticated download and the public one serve identical bytes"},
	{"HandleShare", roleGated, "minting a public link needs the modeler role — the decision to publish is the gate"},
	{"HandleUnshare", roleGated, "revoking one is the same act in reverse"},
	{"HandleDelete", roleGated, "pruning a published artifact needs the modeler role"},
	{"HandlePrune", roleGated, "retention over published history needs the modeler role"},
	{"HandlePublic", tokenGated, "no login at all: the opaque token is the authorization, its shape is checked before the store is read, and an unknown one is an indistinguishable 404"},
	{"LoadVersions", roleGated, "not a route — it runs at startup, before the server serves anything"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers
// that exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range decisionDocGates {
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
		t.Fatalf("handlers with no entry in decisionDocGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops
	// being read.
	for _, g := range decisionDocGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("decisionDocGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range decisionDocGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}

// TestTheTokenGatedRouteChecksTheTokenBeforeTheStore is the behaviour half of the
// one entry that matters: the public route must not reveal whether a document
// exists behind a token, and must not let a crafted one reach the filesystem.
func TestTheTokenGatedRouteChecksTheTokenBeforeTheStore(t *testing.T) {
	svc, _ := newService(t)
	publish(t, svc, "eligibility", "")

	for _, tok := range []string{"../../etc/passwd", "not hex", "", "0123456789abcdef"} {
		rec := call(t, svc.HandlePublic, http.MethodGet, "", map[string]string{"token": tok})
		if rec.Code != http.StatusNotFound {
			t.Errorf("public token %q = %d, want an indistinguishable 404", tok, rec.Code)
		}
	}
}
