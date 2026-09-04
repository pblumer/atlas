package api

import (
	"net/http"
	"testing"
)

// temis is offloaded by default (ADR-0233, slice 7) on clio's condition: the decision
// service's endpoint is a connector record and its token a vault reference behind it,
// neither of which a supervised worker can read.

func addTemisConnector(t *testing.T, srv *Server, body string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors", body, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("add temis connector: status=%d body=%s", code, raw)
	}
}

// The service an operator configured reaches the worker under the names the worker
// reads it by, token included.
func TestTemisWorkerEnvHandsOverTheConfiguredServices(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("rules_cred", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	addTemisConnector(t, srv, `{"name":"rules","kind":"temis","enabled":true,
		"endpoint":"https://rules.example.com","credentialsRef":"rules_cred"}`)

	env := envOf(t, srv.temisWorkerEnv())
	if got := env["ATLAS_TEMIS_RULES_URL"]; got != "https://rules.example.com" {
		t.Errorf("ATLAS_TEMIS_RULES_URL = %q, want the configured endpoint", got)
	}
	if got := env["ATLAS_TEMIS_RULES_TOKEN"]; got != "s3cr3t" {
		t.Errorf("ATLAS_TEMIS_RULES_TOKEN = %q, want the token resolved from the vault", got)
	}
	if got := env["ATLAS_TEMIS_CONNECTORS"]; got != "rules" {
		t.Errorf("ATLAS_TEMIS_CONNECTORS = %q, want the service listed for the worker to build", got)
	}
}

// A service with no token is handed over anyway. This is clio's rule rather than
// Remedy's, and it is a deliberate difference: a decision service run beside Atlas may
// be reachable without a credential, and dropping it would leave a working
// installation with a kind nobody serves.
func TestTemisWorkerEnvServesAServiceWithNoToken(t *testing.T) {
	srv, _ := newValidateServer(t)
	addTemisConnector(t, srv, `{"name":"offen","kind":"temis","enabled":true,
		"endpoint":"http://localhost:9000"}`)

	env := envOf(t, srv.temisWorkerEnv())
	if got := env["ATLAS_TEMIS_OFFEN_URL"]; got != "http://localhost:9000" {
		t.Errorf("ATLAS_TEMIS_OFFEN_URL = %q, want the service served without a token", got)
	}
	if _, rendered := env["ATLAS_TEMIS_OFFEN_TOKEN"]; rendered {
		t.Error("an empty token was rendered; a blank variable reads as a configured empty credential")
	}
}

// Nothing configured renders nothing: the worker then reports temis as a kind it does
// not serve, rather than starting with a service it cannot reach.
func TestTemisWorkerEnvIsEmptyWithoutServices(t *testing.T) {
	srv, _ := newValidateServer(t)
	if env := srv.temisWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a server with no decision services", env)
	}
}

// The last kind is in the default set and its handover is registered — which is what
// makes ADR-0233's table empty rather than merely shorter.
func TestTemisIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults[connectorKindTemis] {
		t.Error("temis is not offloaded by default: a fresh install still evaluates a central decision on the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindTemis]; !provisioned {
		t.Error("temis is defaulted onto a worker but its decision services are not handed over")
	}
}
