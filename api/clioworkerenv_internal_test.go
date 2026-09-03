package api

import (
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// clio is offloaded by default, and the reason it can be is that the engine hands its
// event stores over: the endpoint lives in the connector store and the token in the
// vault, neither of which a supervised worker can read.
//
// This is Remedy's property with an event store in place of an ITSM instance, and it
// guards the same failure: defaulting the kind without the handover would move every
// clio task to a worker with nothing to reach.

func saveClioConnector(t *testing.T, srv *Server, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: name, Name: name, Kind: connectorKindClio,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// The whole handover, checked the way it is used: the engine renders the environment,
// a worker is built from exactly that, and it comes up serving the store — which is
// what "the names match" actually means (there is no private channel, ADR-0157).
func TestSupervisedClioEnvBuildsAWorkerThatServesTheStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveClioConnector(t, srv, "events", "https://clio.example.com", "clio-token")
	t.Setenv("ATLAS_CONNECTOR_CLIO_TOKEN_TOKEN", "tok-123")

	env := envOf(t, srv.clioWorkerEnv())
	if got := env["ATLAS_CLIO_EVENTS_TOKEN"]; got != "tok-123" {
		t.Errorf("token = %q, want the secret behind the connector's credentialsRef", got)
	}

	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindClio)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "events") {
		t.Errorf("the worker holds %v, want the store the engine handed it", built.Names)
	}
	// All three operations are one kind and one registry, so a worker given clio must
	// come up able to lease every one of them — a write that parks while reads run
	// would be the worst possible half-migration.
	for _, jobType := range []string{compiler.ClioWriteJobType, compiler.ClioQueryJobType, compiler.ClioReadJobType} {
		if _, ok := built.Handlers[jobType]; !ok {
			t.Errorf("the worker serves no %s handler", jobType)
		}
	}
}

// A store that needs no token is still served. clio may be reached without one — a
// store an operator runs beside Atlas — and dropping such a connector would leave a
// working instance unserved. That is the difference from Remedy, where a missing
// password means the instance is not configured at all.
func TestClioWorkerEnvServesAStoreWithoutAToken(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveClioConnector(t, srv, "lokal", "http://127.0.0.1:9000", "")

	env := envOf(t, srv.clioWorkerEnv())
	if got := env["ATLAS_CLIO_LOKAL_ENDPOINT"]; got != "http://127.0.0.1:9000" {
		t.Errorf("endpoint = %q, want the token-less store handed over", got)
	}
	if _, handed := env["ATLAS_CLIO_LOKAL_TOKEN"]; handed {
		t.Error("an empty token was rendered; a blank variable reads as a configured empty credential")
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindClio)
	if err != nil {
		t.Fatalf("a worker could not be configured for a token-less store: %v", err)
	}
	if !slices.Contains(built.Names, "lokal") {
		t.Errorf("the worker holds %v, want the token-less store served", built.Names)
	}
}

// A connector with no endpoint is left out rather than handed over half-filled: the
// worker then starts and serves the stores that are configured, and the Console shows
// this one as configured-not-working.
func TestClioWorkerEnvSkipsAStoreWithNoEndpoint(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveClioConnector(t, srv, "halb", "", "")
	if env := srv.clioWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a store with no endpoint", env)
	}
}

// The intent, pinned beside the mechanism: three round trips to somebody else's event
// store no longer happen on the loop that owns the partition's state.
func TestClioIsOffloadedByDefault(t *testing.T) {
	if !slices.Contains(DefaultOffloadedKinds(), connectorKindClio) {
		t.Error("clio is not offloaded by default: a fresh install still writes events from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindClio]; !provisioned {
		t.Error("clio is defaulted onto a worker but its endpoints and tokens are not handed over")
	}
	if !slices.Contains(worker.KnownConnectorKinds(), connectorKindClio) {
		t.Error("clio is defaulted onto a worker that cannot serve it; the jobs would park forever")
	}
}
