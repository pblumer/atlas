package api

import (
	"slices"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// SharePoint is offloaded by default (ADR-0233, slice 5), and the reason it can be is
// that the engine hands its instances over. A SharePoint task names its instance and
// nothing more — the Graph endpoint and the OAuth bundle are a worker record and a
// vault secret (ADR-0141), which a supervised worker can read no more than it can read
// the engine's memory.
//
// Without this handover the default would have moved every SharePoint task to a worker
// with no site to create items in, which is the failure mail had before ADR-0168.

// spBundle is a well-formed client-credentials bundle, the shape an operator stores
// in the vault under the record's credentialsRef.
const spBundle = `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1","clientSecret":"s3cr3t"}`

func saveSharePointConnector(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindSharePoint,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds a client from. This is the test that
// actually holds the two halves together: it takes the rendered environment and asks
// worker.BuiltinConnectors to configure itself from it, so a variable named
// differently on either side fails here rather than in a parked job.
func TestSupervisedSharePointEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSharePointConnector(t, srv, "1", "intranet", "https://graph.microsoft.com/v1.0", "sp-creds")
	t.Setenv("ATLAS_CONNECTOR_SP_CREDS_TOKEN", spBundle)

	env := envOf(t, srv.sharepointWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindSharePoint)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "intranet") {
		t.Errorf("the worker holds %v, want the instance the engine handed it", built.Names)
	}
}

// The credential travels as the whole bundle, one value, rather than field by field.
// That is the decision worth pinning: the bundle has no public half worth splitting,
// and a renderer that took it apart would be deciding the grant's shape a second time
// — where getting it wrong yields a worker that fails every job.
func TestSupervisedSharePointEnvHandsOverTheWholeBundle(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSharePointConnector(t, srv, "1", "intranet", "", "sp-creds")
	t.Setenv("ATLAS_CONNECTOR_SP_CREDS_TOKEN", spBundle)

	env := envOf(t, srv.sharepointWorkerEnv())
	if got := env["ATLAS_SHAREPOINT_INTRANET_CREDENTIALS"]; got != spBundle {
		t.Errorf("CREDENTIALS = %q, want the bundle verbatim", got)
	}
	// No endpoint on the record means the Graph default, which is what
	// buildSharePointClients passes too — so rendering an empty one would make the
	// worker's client differ from the engine's for a record that works today.
	if _, rendered := env["ATLAS_SHAREPOINT_INTRANET_ENDPOINT"]; rendered {
		t.Error("an empty endpoint was rendered; blank must mean the Graph default on both sides")
	}
}

// An instance whose bundle does not resolve is left out rather than handed over
// empty. Handing it over would make the worker refuse at startup on a *named*
// instance it cannot build — taking down every other kind that worker serves.
func TestSupervisedSharePointEnvSkipsAnUnresolvedBundle(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSharePointConnector(t, srv, "1", "broken", "", "missing-ref")
	saveSharePointConnector(t, srv, "2", "works", "", "sp-creds")
	t.Setenv("ATLAS_CONNECTOR_SP_CREDS_TOKEN", spBundle)

	env := envOf(t, srv.sharepointWorkerEnv())
	if _, handed := env["ATLAS_SHAREPOINT_BROKEN_CREDENTIALS"]; handed {
		t.Error("an instance with no resolvable bundle was handed over")
	}
	if got := env["ATLAS_SHAREPOINT_CONNECTORS"]; got != "works" {
		t.Errorf("CONNECTORS = %q, want only the instance that can actually be built", got)
	}
	// And the one that does resolve still reaches a worker that starts.
	if _, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindSharePoint); err != nil {
		t.Fatalf("the worker refused the environment: %v", err)
	}
}

// A server with no SharePoint records renders nothing, so a child inherits whatever an
// operator set on the host untouched. Rendering an empty CONNECTORS would take a
// host-configured instance away from it.
func TestSupervisedSharePointEnvIsEmptyWithoutRecords(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if env := srv.sharepointWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing when the store holds no instance", env)
	}
}

// The kind this slice moves is in the default set and its handover is registered.
func TestSharePointIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults[connectorKindSharePoint] {
		t.Error("sharepoint is not offloaded by default: a fresh install still calls Graph from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindSharePoint]; !provisioned {
		t.Error("sharepoint is defaulted onto a worker but its instances are not handed over")
	}
}
