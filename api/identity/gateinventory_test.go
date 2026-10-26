package identity

import (
	"net/http"
	"reflect"
	"testing"
)

// What a credential can reach has to be provable by reading one list (ADR-0209's
// discipline, applied to the object axis rather than the role one).
//
// This area's objects are personal: an external identity and the connections it
// authorizes belong to the Atlas user who linked them, and consenting to a Worker
// acting through one is that user's act alone. The handlers narrow every request to
// the caller's own records; the two exceptions are an administrator's — the route
// table admits only admins to the per-user view, and revocation also admits an
// administrator, because cutting off a person's delegated access is an act of
// protection, not of use. A connection somebody else owns is answered as one that does
// not exist (writeError), so the routes cannot be used to learn which ids exist.
// TestTheRoutesShowOnlyWhatTheCallerMayActOn holds the behaviour; this table holds the
// classification. Reflection supplies the actual set of handlers, so adding a route
// and forgetting to classify it fails the build.

// gateKind says what a handler does about the object axis.
type gateKind int

const (
	// callerScoped: answers only with the caller's own records.
	callerScoped gateKind = iota
	// ownerOnly: acts on one record, and only for its owner.
	ownerOnly
	// ownerOrAdmin: acts on one record for its owner, or for an administrator.
	ownerOrAdmin
	// adminView: reachable by administrators only (the route table's RoleAdmin), and
	// then for any user.
	adminView
)

type handlerGate struct {
	name string
	kind gateKind
	// why says what stands behind this classification, in one line.
	why string
}

var identityGates = []handlerGate{
	{"HandleMine", callerScoped, "ForOwner(caller): the caller's identities and connections, nobody else's"},
	{"HandleSetWorkers", ownerOnly, "SetAllowedWorkers refuses a caller who is not the owner: allowing a Worker is consent, and only the owner gives it"},
	{"HandleRevoke", ownerOrAdmin, "Revoke admits the owner, or an administrator protecting the account; anybody else is told the connection does not exist"},
	{"HandleOfUser", adminView, "an administrator answering what can act as a given person; the route table admits nobody else"},
}

// TestEveryHandlerIsClassified is the drift half. Reflection gives the handlers that
// exist; the table gives the ones somebody thought about.
func TestEveryHandlerIsClassified(t *testing.T) {
	named := map[string]bool{}
	for _, g := range identityGates {
		named[g.name] = true
	}

	var missing []string
	typ := reflect.TypeOf(&Service{})
	handler := reflect.TypeOf(http.HandlerFunc(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		// A handler is a method taking (w, r), or one that builds such a handler.
		switch {
		case m.Type.NumIn() == 3 && m.Type.NumOut() == 0:
			fn := reflect.FuncOf([]reflect.Type{m.Type.In(1), m.Type.In(2)}, nil, false)
			if !fn.ConvertibleTo(handler) {
				continue
			}
		case m.Type.NumOut() == 1 && m.Type.Out(0) == handler:
		default:
			continue
		}
		if !named[m.Name] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("handlers with no entry in identityGates: %v\n"+
			"Every handler is either gated on the object axis or deliberately not, "+
			"with the reason written down. A route nobody classified is a route "+
			"nobody checked.", missing)
	}

	// And no stale entries: a table naming handlers that no longer exist stops being
	// read.
	for _, g := range identityGates {
		if _, ok := typ.MethodByName(g.name); !ok {
			t.Errorf("identityGates names %q, which is not a method on this service", g.name)
		}
	}
}

// TestEveryEntrySaysWhy: an entry with no reason is an entry nobody can review.
func TestEveryEntrySaysWhy(t *testing.T) {
	for _, g := range identityGates {
		if g.why == "" {
			t.Errorf("%s carries no reason", g.name)
		}
	}
}
