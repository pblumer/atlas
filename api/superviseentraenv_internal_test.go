package api

import (
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// What a supervised Microsoft Entra worker is handed at spawn.
//
// Entra is worker-only (ADR-0172): the engine builds no client and holds no tenant
// credential. Tenant and client id are not secret and reach a supervised child by
// inheriting this process's environment; the client secret must not sit there in the
// clear. An operator who wants it in the vault names a vault key in
// ATLAS_ENTRA_<NAME>_CLIENT_SECRET_REF, and the engine resolves it and hands the child
// the value under the name the worker reads — the AD bind-secret story with a connector
// name in place of a reference.

// A tenant whose secret lives in the vault: the worker is handed that secret under its
// own CLIENT_SECRET name, and nothing else (tenant/client id are inherited).
func TestASupervisedEntraWorkerGetsItsClientSecretFromTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer")

	env := envOf(t, srv.entraWorkerEnv())
	if got := env["ATLAS_ENTRA_BLUMER_CLIENT_SECRET"]; got != "s3cr3t" {
		t.Errorf("ATLAS_ENTRA_BLUMER_CLIENT_SECRET = %q, want the secret out of the vault", got)
	}
	if len(env) != 1 {
		t.Errorf("environment = %v, want only the bridged secret (tenant/client id are inherited)", env)
	}
}

// A secret the operator set directly is not overridden: the child inherits it, and a
// stale vault entry must not silently win over an explicit choice.
func TestAnOperatorSetEntraSecretIsNotOverriddenByTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", "from-vault"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET", "from-operator")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer")

	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing when the operator set the secret directly", env)
	}
}

// A reference nothing answers to is left out rather than handed over empty: a blank
// variable reads as a configured blank secret, and the worker's own error is better.
func TestAnEntraSecretReferenceNothingAnswersToIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "not-set")

	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a reference that resolves to nothing", env)
	}
}

// A tenant that names no reference and sets no secret contributes nothing — its secret
// is simply not managed here (it may be set on the host by hand).
func TestAnEntraTenantWithNoReferenceContributesNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")

	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a tenant that names no reference", env)
	}
}

// Two tenants each get their own secret from the vault, once each.
func TestEntraSecretsAreCollectedPerTenant(t *testing.T) {
	srv, _ := newValidateServer(t)
	for ref, value := range map[string]string{"entra-blumer": "eins", "entra-contoso": "zwei"} {
		if _, err := srv.vault.Set(ref, value); err != nil {
			t.Fatalf("vault.Set(%s): %v", ref, err)
		}
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer, contoso")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer")
	t.Setenv("ATLAS_ENTRA_CONTOSO_CLIENT_SECRET_REF", "entra-contoso")

	env := envOf(t, srv.entraWorkerEnv())
	if env["ATLAS_ENTRA_BLUMER_CLIENT_SECRET"] != "eins" || env["ATLAS_ENTRA_CONTOSO_CLIENT_SECRET"] != "zwei" {
		t.Errorf("environment = %v, want both tenants' secrets", env)
	}
}

// A worker that does not serve Entra is not handed Entra's client secret: one worker
// per kind is what keeps another connector's worker from reading the tenant credential.
func TestANonEntraWorkerIsNeverGivenTheEntraSecret(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer")

	scripts := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "script", Connectors: []string{"script"}})())
	for name := range scripts {
		if strings.Contains(name, "ENTRA") {
			t.Errorf("a script worker was handed %s", name)
		}
	}
	entra := envOf(t, srv.superviseEnv(SuperviseSpec{ID: "entra", Connectors: []string{"entra"}})())
	if got := entra["ATLAS_ENTRA_BLUMER_CLIENT_SECRET"]; got != "s3cr3t" {
		t.Errorf("the entra worker's secret = %q, want it to hold the credential", got)
	}
}

// The name the engine writes is the name the worker reads — declared in two packages
// because the engine cannot import the worker. Here the whole path is exercised: what
// the engine rendered (plus the inherited tenant/client id) configures a real Entra
// worker registry, and the same registry cannot be built without the vault-provided
// secret — which is what makes the assertion about the name rather than luck.
func TestSupervisedEntraEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_TENANT_ID", "27f3923b-tenant")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_ID", "a4500604-client")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer")

	env := envOf(t, srv.entraWorkerEnv())
	// The child's real environment is os.Environ() (tenant/client id, set above) plus
	// what the engine rendered (the secret). The getter mirrors that precedence.
	getter := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	if _, err := worker.BuiltinConnectors(getter, "entra"); err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	// Without the vault-provided secret the same worker cannot be configured, so the
	// success above is about the name matching, not about the tenant/client alone.
	bare := func(k string) string {
		if strings.HasSuffix(k, "_CLIENT_SECRET") {
			return ""
		}
		return os.Getenv(k)
	}
	if _, err := worker.BuiltinConnectors(bare, "entra"); err == nil {
		t.Error("a worker holding no client secret was configured anyway; the name assertion proves nothing")
	}
}

// A tenant an operator added in the Console: the vault holds the whole OAuth bundle
// under the credentialsRef, and the engine renders the three variables the worker reads
// — the engine itself keeps none of them (ADR-0172, amended).
func TestASupervisedEntraWorkerGetsATenantFromTheConsole(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", `{"tenantId":"tid-1","clientId":"cid-1","clientSecret":"sec-1"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "blumer", Kind: "entra", CredentialsRef: "entra-blumer", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	env := envOf(t, srv.entraWorkerEnv())
	if env["ATLAS_ENTRA_BLUMER_TENANT_ID"] != "tid-1" || env["ATLAS_ENTRA_BLUMER_CLIENT_ID"] != "cid-1" || env["ATLAS_ENTRA_BLUMER_CLIENT_SECRET"] != "sec-1" {
		t.Errorf("environment = %v, want the three variables from the vault bundle", env)
	}
	if env["ATLAS_ENTRA_CONNECTORS"] != "blumer" {
		t.Errorf("ATLAS_ENTRA_CONNECTORS = %q, want the store tenant's name", env["ATLAS_ENTRA_CONNECTORS"])
	}
}

// A disabled tenant is kept in the store but not handed to the worker.
func TestADisabledEntraConnectorIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer", `{"tenantId":"t","clientId":"c","clientSecret":"s"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "blumer", Kind: "entra", CredentialsRef: "entra-blumer", Enabled: false, CreatedAt: 1}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a disabled connector", env)
	}
}

// A bundle that does not resolve — no secret set yet, or malformed, or missing a field —
// leaves the tenant out rather than handing over a half-filled credential.
func TestAnEntraBundleThatDoesNotResolveIsLeftOut(t *testing.T) {
	for _, tc := range []struct{ name, bundle string }{
		{"no secret in the vault", ""},
		{"not json", "not-json"},
		{"missing clientSecret", `{"tenantId":"t","clientId":"c"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t)
			if tc.bundle != "" {
				if _, err := srv.vault.Set("entra-blumer", tc.bundle); err != nil {
					t.Fatalf("vault.Set: %v", err)
				}
			}
			if err := srv.connectors.Save(connector{ID: "1", Name: "blumer", Kind: "entra", CredentialsRef: "entra-blumer", Enabled: true, CreatedAt: 1}); err != nil {
				t.Fatalf("connectors.Save: %v", err)
			}
			if env := srv.entraWorkerEnv(); len(env) != 0 {
				t.Errorf("environment = %v, want nothing when the bundle does not resolve", env)
			}
		})
	}
}

// An endpoint on the record overrides the Graph base for a national cloud.
func TestAnEntraEndpointOverridesTheGraphBase(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-usgov", `{"tenantId":"t","clientId":"c","clientSecret":"s"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "usgov", Kind: "entra", Endpoint: "https://graph.microsoft.us/v1.0", CredentialsRef: "entra-usgov", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	if got := envOf(t, srv.entraWorkerEnv())["ATLAS_ENTRA_USGOV_BASE_URL"]; got != "https://graph.microsoft.us/v1.0" {
		t.Errorf("ATLAS_ENTRA_USGOV_BASE_URL = %q, want the national-cloud base", got)
	}
}

// Entra is supervised by default: the worker exists and parks until a tenant is added,
// which is what makes a tenant a Console entry rather than a deployment change.
func TestEntraIsSupervisedByDefault(t *testing.T) {
	found := false
	for _, k := range DefaultSupervisedWorkerOnlyKinds() {
		if k == connectorKindEntra {
			found = true
		}
	}
	if !found {
		t.Errorf("DefaultSupervisedWorkerOnlyKinds() = %v, want it to include entra", DefaultSupervisedWorkerOnlyKinds())
	}
}

func TestEntraBundleParse(t *testing.T) {
	if _, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","clientSecret":"s"}`); !ok {
		t.Error("a complete bundle should parse")
	}
	for _, bad := range []string{"", "  ", "not-json", `{"tenantId":"t"}`, `{"tenantId":"t","clientId":"c"}`, `{}`} {
		if _, ok := entraBundleParse(bad); ok {
			t.Errorf("entraBundleParse(%q) parsed, want it refused", bad)
		}
	}
}
