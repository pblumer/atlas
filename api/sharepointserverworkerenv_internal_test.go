package api

import (
	"slices"
	"testing"

	"github.com/pblumer/atlas/worker"
)

func saveSPServerWorker(t *testing.T, srv *Server, id, name, endpoint, ref string, enabled bool) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindSharePointServer,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: enabled, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds from, so a variable named differently
// on either side fails here rather than in a parked job.
func TestSupervisedSharePointServerEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveSPServerWorker(t, srv, "1", "farm", "http://sp01.example.ch", "", true)
	saveSPServerWorker(t, srv, "2", "bad", "not-a-url", "", true)
	saveSPServerWorker(t, srv, "3", "off", "http://sp02.example.ch", "", false)
	t.Setenv("ATLAS_CONNECTOR_SPSET_TOKEN", `{"managedPath":"teams"}`)
	saveSPServerWorker(t, srv, "4", "teams", "http://sp03.example.ch", "spset", true)

	env := envOf(t, srv.sharePointServerWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindSharePointServer)
	if err != nil {
		t.Fatalf("a worker could not be built from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "farm") || !slices.Contains(built.Names, "teams") || slices.Contains(built.Names, "bad") || slices.Contains(built.Names, "off") {
		t.Errorf("the worker holds %v", built.Names)
	}
	if got := env["ATLAS_SHAREPOINTSERVER_TEAMS_CREDENTIALS"]; got != `{"managedPath":"teams"}` {
		t.Errorf("settings = %q", got)
	}
}

// With no record the engine stays out of the way: nothing is handed over, so an
// operator's own environment on the farm stands.
func TestSupervisedSharePointServerEnvIsEmptyWithoutAWorker(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if got := srv.sharePointServerWorkerEnv(); got != nil {
		t.Errorf("env = %v, want none", got)
	}
}

// The kind is not supervised by default: a worker beside Atlas has no farm to reach.
func TestSharePointServerIsNotSupervisedByDefault(t *testing.T) {
	if slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindSharePointServer) {
		t.Error("the cmdlets exist only on a farm server")
	}
}

func TestValidateSharePointServerWorker(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		ok       bool
	}{{"", false}, {"ftp://x", false}, {"http://sp01.contoso.local", true}} {
		p := createConnectorParams{Endpoint: tc.endpoint}
		if got := validateSharePointServerConnector(&p); (got == "") != tc.ok {
			t.Errorf("%q: %q", tc.endpoint, got)
		}
	}
}
