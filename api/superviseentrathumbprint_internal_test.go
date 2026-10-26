package api

import (
	"encoding/json"
	"testing"
)

// An Entra tenant whose certificate stays in the Windows certificate store, named by its
// thumbprint (ADR-0172, amended). The thumbprint travels the way a secret or a PEM does:
// out of the vault bundle, into the supervised worker's environment — but only where
// there is a store to find it in.

func withCertStore(t *testing.T, supported bool) {
	t.Helper()
	prev := certStoreSupported
	certStoreSupported = supported
	t.Cleanup(func() { certStoreSupported = prev })
}

func saveEntraBundle(t *testing.T, srv *Server, bundle map[string]string) {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	if _, err := srv.vault.Set("entra-blumer", string(raw)); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "blumer", Kind: "entra", CredentialsRef: "entra-blumer", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
}

func TestEntraBundleParseWithAThumbprint(t *testing.T) {
	withCertStore(t, true)
	b, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","certificateThumbprint":" 02CB6038F2E91693AE8D31C983569366034B2100 "}`)
	if !ok || b.CertificateThumbprint != "02CB6038F2E91693AE8D31C983569366034B2100" {
		t.Errorf("a thumbprint bundle should parse with its thumbprint trimmed, got %+v, %v", b, ok)
	}
	for name, raw := range map[string]string{
		"thumbprint and secret":      `{"tenantId":"t","clientId":"c","clientSecret":"s","certificateThumbprint":"AB"}`,
		"thumbprint and certificate": `{"tenantId":"t","clientId":"c","certificate":"PEM","certificateThumbprint":"AB"}`,
		"blank thumbprint":           `{"tenantId":"t","clientId":"c","certificateThumbprint":"  "}`,
	} {
		if _, ok := entraBundleParse(raw); ok {
			t.Errorf("%s parsed, want it refused", name)
		}
	}

	withCertStore(t, false)
	if _, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","certificateThumbprint":"AB"}`); ok {
		t.Error("a thumbprint bundle parsed on a host without a certificate store; the worker could only refuse it")
	}
}

// A Console tenant whose bundle names a thumbprint is handed CLIENT_CERTIFICATE_THUMBPRINT
// and neither of the other two credentials.
func TestAConsoleEntraTenantWithAThumbprintIsHandedIt(t *testing.T) {
	withCertStore(t, true)
	srv, _ := newValidateServer(t)
	saveEntraBundle(t, srv, map[string]string{"tenantId": "tid-1", "clientId": "cid-1", "certificateThumbprint": "02CB6038F2E91693AE8D31C983569366034B2100"})
	env := envOf(t, srv.entraWorkerEnv())
	want := map[string]string{
		"ATLAS_ENTRA_CONNECTORS":                           "blumer",
		"ATLAS_ENTRA_BLUMER_TENANT_ID":                     "tid-1",
		"ATLAS_ENTRA_BLUMER_CLIENT_ID":                     "cid-1",
		"ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE_THUMBPRINT": "02CB6038F2E91693AE8D31C983569366034B2100",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for _, k := range []string{"ATLAS_ENTRA_BLUMER_CLIENT_SECRET", "ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE"} {
		if _, ok := env[k]; ok {
			t.Errorf("a thumbprint tenant was also handed %s", k)
		}
	}
}

// Where there is no certificate store, the tenant is left out like any bundle that does
// not resolve — rather than handed to a worker that would refuse it and, refusing,
// stop serving every other tenant with it.
func TestAConsoleEntraTenantWithAThumbprintIsLeftOutWithoutAStore(t *testing.T) {
	withCertStore(t, false)
	srv, _ := newValidateServer(t)
	saveEntraBundle(t, srv, map[string]string{"tenantId": "tid-1", "clientId": "cid-1", "certificateThumbprint": "02CB6038F2E91693AE8D31C983569366034B2100"})
	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want the tenant left out", env)
	}
}

// A thumbprint the operator set on the host is the tenant's credential, so no vault
// reference is resolved beside it.
func TestAnOperatorSetEntraThumbprintIsNotOverriddenByTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer-secret", "from-vault"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE_THUMBPRINT", "02CB6038F2E91693AE8D31C983569366034B2100")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_SECRET_REF", "entra-blumer-secret")
	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment = %v, want nothing when the operator set a thumbprint", env)
	}
}
