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
