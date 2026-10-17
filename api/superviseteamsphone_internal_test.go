package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// A Teams Phone tenant added in the Console reaches its supervised worker the way an
// Entra tenant does: out of the vault, under the names the worker reads — and what it is
// handed configures a real worker, so the names are the worker's own.
func TestAConsoleTeamsPhoneTenantConfiguresTheWorker(t *testing.T) {
	for name, cred := range map[string]map[string]string{
		"certificate": {"certificate": entraCertPEM(t)},
		"thumbprint":  {"certificateThumbprint": "0123456789abcdef0123456789abcdef01234567"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := newValidateServer(t)
			bundle := map[string]string{"tenantId": "tid-1", "clientId": "cid-1"}
			for k, v := range cred {
				bundle[k] = v
			}
			raw, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := srv.vault.Set("teams-contoso", string(raw)); err != nil {
				t.Fatalf("vault.Set: %v", err)
			}
			if err := srv.connectors.Save(connector{ID: "1", Name: "contoso", Kind: connectorKindTeamsPhone, CredentialsRef: "teams-contoso", Enabled: true, CreatedAt: 1}); err != nil {
				t.Fatalf("connectors.Save: %v", err)
			}
			env := envOf(t, srv.teamsPhoneWorkerEnv())
			if env["ATLAS_TEAMSPHONE_CONNECTORS"] != "contoso" || env["ATLAS_TEAMSPHONE_CONTOSO_TENANT_ID"] != "tid-1" || env["ATLAS_TEAMSPHONE_CONTOSO_CLIENT_ID"] != "cid-1" {
				t.Errorf("rendered environment lacks the tenant: %v", tenantEnvKeys(env))
			}
			_, hasPEM := env["ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE"]
			_, hasThumb := env["ATLAS_TEAMSPHONE_CONTOSO_CERTIFICATE_THUMBPRINT"]
			if hasPEM == hasThumb {
				t.Errorf("want exactly one certificate variable, got PEM %v, thumbprint %v", hasPEM, hasThumb)
			}
			got, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "teamsphone")
			if err != nil {
				t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
			}
			if _, ok := got.Handlers[compiler.TeamsPhoneJobType]; !ok {
				t.Error("the configured worker serves no Teams Phone jobs")
			}
		})
	}
}

// A bundle that names neither or both ways to the certificate, or lacks the app, is left
// out rather than handed over half-filled.
func TestTeamsPhoneBundleParse(t *testing.T) {
	if b, ok := teamsPhoneBundleParse(`{"tenantId":" t ","clientId":"c","certificateThumbprint":" AB "}`); !ok || b.TenantID != "t" || b.CertificateThumbprint != "AB" {
		t.Errorf("a thumbprint bundle should parse trimmed, got %+v, %v", b, ok)
	}
	for _, raw := range []string{
		``, `not json`,
		`{"tenantId":"t","clientId":"c"}`,
		`{"tenantId":"t","clientId":"c","certificate":"PEM","certificateThumbprint":"AB"}`,
		`{"clientId":"c","certificate":"PEM"}`,
		`{"tenantId":"t","clientId":"c","clientSecret":"s"}`,
	} {
		if _, ok := teamsPhoneBundleParse(raw); ok {
			t.Errorf("bundle %q parsed, want it refused", raw)
		}
	}
}

// A disabled tenant, or one whose bundle does not resolve, is not handed over.
func TestADisabledTeamsPhoneTenantIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("teams-contoso", `{"tenantId":"t","clientId":"c","certificateThumbprint":"AB"}`); err != nil {
		t.Fatal(err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "contoso", Kind: connectorKindTeamsPhone, CredentialsRef: "teams-contoso", Enabled: false, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := srv.connectors.Save(connector{ID: "2", Name: "fabrikam", Kind: connectorKindTeamsPhone, CredentialsRef: "missing", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if env := srv.teamsPhoneWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a disabled tenant and an unresolved bundle", env)
	}
}

// The create form refuses an endpoint, which this kind would only store and ignore, and
// a record with nothing to sign in with.
func TestValidateTeamsPhoneConnector(t *testing.T) {
	if msg := validateTeamsPhoneConnector(&createConnectorParams{CredentialsRef: "teams-contoso"}); msg != "" {
		t.Errorf("a record with a credentialsRef was refused: %s", msg)
	}
	if msg := validateTeamsPhoneConnector(&createConnectorParams{}); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("a record without credentialsRef: %q", msg)
	}
	if msg := validateTeamsPhoneConnector(&createConnectorParams{CredentialsRef: "x", Endpoint: "https://teams"}); !strings.Contains(msg, "no endpoint") {
		t.Errorf("a record with an endpoint: %q", msg)
	}
}

func tenantEnvKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
