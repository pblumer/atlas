package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// Exchange is worker-only, and the reason a supervised worker can serve it at all is
// that the engine hands its organizations over: an Exchange task names its Worker and
// nothing more, and the bundle that says which organization and as whom is a Worker
// record and a vault secret, which a supervised worker cannot read for itself.

// exchangeOnlineBundle and exchangeOnPremBundle are well-formed bundles of the two
// targets, the shapes an operator stores in the vault under a record's credentialsRef.
const (
	exchangeOnlineBundle = `{"organization":"contoso.onmicrosoft.com","clientId":"app-1","clientSecret":"s3cr3t"}`
	exchangeOnPremBundle = `{"target":"onprem","username":"CONTOSO\\svc","password":"pw"}`
)

func saveExchangeWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindExchange,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its backends from: the rendered
// environment is handed to worker.BuiltinConnectors, so a variable named differently on
// either side fails here rather than in a parked job.
func TestSupervisedExchangeEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveExchangeWorker(t, srv, "1", "contoso", "", "exo-online")
	saveExchangeWorker(t, srv, "2", "intern", "http://exch01.contoso.local/PowerShell/", "exo-intern")
	t.Setenv("ATLAS_CONNECTOR_EXO_ONLINE_TOKEN", exchangeOnlineBundle)
	t.Setenv("ATLAS_CONNECTOR_EXO_INTERN_TOKEN", exchangeOnPremBundle)

	env := envOf(t, srv.exchangeWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindExchange)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "contoso") || !slices.Contains(built.Names, "intern") {
		t.Errorf("the worker holds %v, want both organizations", built.Names)
	}
	if got := env["ATLAS_EXCHANGE_INTERN_URL"]; got != "http://exch01.contoso.local/PowerShell/" {
		t.Errorf("the handed endpoint = %q", got)
	}
	if _, ok := env["ATLAS_EXCHANGE_CONTOSO_URL"]; ok {
		t.Error("an endpoint was rendered for a record that authored none")
	}
	if got := env["ATLAS_EXCHANGE_CONTOSO_CREDENTIALS"]; got != exchangeOnlineBundle {
		t.Errorf("the handed bundle = %q, want it whole", got)
	}
}

// An organization whose bundle does not build is left out rather than handed over: the
// worker refuses at startup on a named organization it cannot build, which would take
// down every other organization it serves.
func TestSupervisedExchangeEnvSkipsAnUnusableBundle(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"never set", ""},
		{"malformed", "{"},
		{"no credential", `{"organization":"contoso.onmicrosoft.com","clientId":"a"}`},
		{"server without URI", `{"target":"onprem"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveExchangeWorker(t, srv, "1", "contoso", "", "exo")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_EXO_TOKEN", tc.secret)
			}
			if env := srv.exchangeWorkerEnv(); env != nil {
				t.Errorf("rendered %v for an organization with no usable bundle; want nothing", env)
			}
		})
	}
}

func TestSupervisedExchangeEnvSkipsAFoldingCollision(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveExchangeWorker(t, srv, "1", "contoso-eu", "", "exo")
	saveExchangeWorker(t, srv, "2", "contoso_eu", "", "exo")
	t.Setenv("ATLAS_CONNECTOR_EXO_TOKEN", exchangeOnlineBundle)

	env := envOf(t, srv.exchangeWorkerEnv())
	if got := env["ATLAS_EXCHANGE_CONNECTORS"]; strings.Count(got, ",") != 0 {
		t.Errorf("ATLAS_EXCHANGE_CONNECTORS = %q, want only the first of two names that fold together", got)
	}
}

// The intent behind the default, pinned apart from the mechanism: Exchange is
// supervised from the first boot, parked until an organization is added, and its
// bundle is handed over once one is.
func TestExchangeIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindExchange) {
		t.Error("exchange is not supervised by default: adding an organization in the Console would need a deployment change")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindExchange]; !provisioned {
		t.Error("exchange is supervised but its bundle is not handed over")
	}
}

// A record without a bundle is refused where the operator is looking: without one the
// worker cannot even tell whether the organization is in the cloud or on a server.
func TestValidateExchangeWorkerNeedsABundle(t *testing.T) {
	p := createConnectorParams{Provider: "x", Sender: "y"}
	if msg := validateExchangeConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{CredentialsRef: "exo", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateExchangeConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}
