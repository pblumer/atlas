package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// An Entra tenant whose app proves itself with a certificate instead of a client
// secret (ADR-0172, amended). The certificate travels the way the secret does: out of
// the vault, into the supervised worker's environment, under the name the worker reads.

// entraCertPEM is a self-signed certificate and its key as one PEM text.
func entraCertPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "atlas-entra"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

// A Console tenant whose bundle carries a certificate is handed CLIENT_CERTIFICATE and
// no CLIENT_SECRET, and what it is handed configures a real Entra worker — so the
// variable name is the worker's own, not merely a plausible one.
func TestAConsoleEntraTenantWithACertificateConfiguresTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	cert := entraCertPEM(t)
	bundle, err := json.Marshal(map[string]string{"tenantId": "tid-1", "clientId": "cid-1", "certificate": cert})
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	if _, err := srv.vault.Set("entra-blumer", string(bundle)); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{ID: "1", Name: "blumer", Kind: "entra", CredentialsRef: "entra-blumer", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	env := envOf(t, srv.entraWorkerEnv())
	if got := env["ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE"]; got != strings.TrimSpace(cert) {
		t.Errorf("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE is not the certificate from the bundle")
	}
	if _, ok := env["ATLAS_ENTRA_BLUMER_CLIENT_SECRET"]; ok {
		t.Error("a certificate tenant was also handed a CLIENT_SECRET")
	}
	getter := func(k string) string { return env[k] }
	if _, err := worker.BuiltinConnectors(getter, "entra"); err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
}

// A host-set tenant may name its certificate by a vault reference, the way it may name
// its secret: the engine resolves it and hands the PEM over under CLIENT_CERTIFICATE.
func TestASupervisedEntraWorkerGetsItsCertificateFromTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	cert := entraCertPEM(t)
	if _, err := srv.vault.Set("entra-blumer-cert", cert); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_TENANT_ID", "27f3923b-tenant")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_ID", "a4500604-client")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE_REF", "entra-blumer-cert")

	env := envOf(t, srv.entraWorkerEnv())
	if got := env["ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE"]; got != cert {
		t.Error("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE is not the certificate out of the vault")
	}
	if len(env) != 1 {
		t.Errorf("environment has %d variables, want only the bridged certificate", len(env))
	}
	getter := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	if _, err := worker.BuiltinConnectors(getter, "entra"); err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
}

// A certificate the operator set directly is not overridden by a vault reference, as
// a directly set secret is not.
func TestAnOperatorSetEntraCertificateIsNotOverriddenByTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("entra-blumer-cert", "from-vault"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	t.Setenv("ATLAS_ENTRA_CONNECTORS", "blumer")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE", "from-operator")
	t.Setenv("ATLAS_ENTRA_BLUMER_CLIENT_CERTIFICATE_REF", "entra-blumer-cert")
	if env := srv.entraWorkerEnv(); len(env) != 0 {
		t.Errorf("environment has %d variables, want nothing when the operator set the certificate directly", len(env))
	}
}

// A bundle carrying a secret and a certificate together leaves the choice to
// precedence, so it is left out like any bundle that does not resolve.
func TestEntraBundleParseWithACertificate(t *testing.T) {
	if b, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","certificate":" PEM "}`); !ok || b.Certificate != "PEM" {
		t.Errorf("a certificate bundle should parse with its certificate trimmed, got %+v, %v", b, ok)
	}
	if _, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","clientSecret":"s","certificate":"PEM"}`); ok {
		t.Error("a bundle with both a secret and a certificate parsed, want it refused")
	}
	if _, ok := entraBundleParse(`{"tenantId":"t","clientId":"c","certificate":"  "}`); ok {
		t.Error("a bundle with a blank certificate and no secret parsed, want it refused")
	}
}
