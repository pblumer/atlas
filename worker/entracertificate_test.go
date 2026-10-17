package worker

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// entraTestCertificate is a self-signed certificate and its key as one PEM text — the
// shape an operator puts into ATLAS_ENTRA_<NAME>_CLIENT_CERTIFICATE.
func entraTestCertificate(t *testing.T) string {
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

func entraCertEnv(cert string) map[string]string {
	return map[string]string{
		"ATLAS_ENTRA_CONNECTORS":                 "contoso",
		"ATLAS_ENTRA_CONTOSO_TENANT_ID":          "11111111-2222-3333-4444-555555555555",
		"ATLAS_ENTRA_CONTOSO_CLIENT_ID":          "app-1",
		"ATLAS_ENTRA_CONTOSO_CLIENT_CERTIFICATE": cert,
	}
}

// A tenant whose app proves itself with a certificate is served like one with a
// secret: the credential only changes how the token is fetched (ADR-0172, amended).
func TestBuiltinConnectorsServesAnEntraTenantWithACertificate(t *testing.T) {
	got, err := BuiltinConnectors(envMap(entraCertEnv(entraTestCertificate(t))), "entra")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.EntraJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.EntraJobType, got.Handlers)
	}
	if len(got.Names) != 1 || got.Names[0] != "contoso" {
		t.Errorf("names = %v, want [contoso]", got.Names)
	}
}

// A certificate written on one line with \n sequences — all a systemd unit or a
// one-line Kubernetes value can hold — is read as the PEM it spells.
func TestAnEntraCertificateMayBeWrittenOnOneLine(t *testing.T) {
	oneLine := strings.ReplaceAll(strings.TrimSpace(entraTestCertificate(t)), "\n", `\n`)
	if strings.Contains(oneLine, "\n") {
		t.Fatal("test setup: the one-line form still has a line break")
	}
	if _, err := BuiltinConnectors(envMap(entraCertEnv(oneLine)), "entra"); err != nil {
		t.Fatalf("a one-line certificate must be accepted: %v", err)
	}
}

// Two credentials for one tenant leave the choice to an accident of precedence, so the
// worker refuses at startup and names both variables.
func TestAnEntraTenantWithASecretAndACertificateIsRefused(t *testing.T) {
	env := entraCertEnv(entraTestCertificate(t))
	env["ATLAS_ENTRA_CONTOSO_CLIENT_SECRET"] = "s3cr3t"
	_, err := BuiltinConnectors(envMap(env), "entra")
	if err == nil {
		t.Fatal("a tenant with both a secret and a certificate must be refused")
	}
	for _, want := range []string{"ATLAS_ENTRA_CONTOSO_CLIENT_SECRET", "ATLAS_ENTRA_CONTOSO_CLIENT_CERTIFICATE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// A certificate the worker cannot use is refused at startup, while the operator is
// watching, naming the tenant and never quoting the PEM it was handed.
func TestAnUnusableEntraCertificateIsRefusedAtStartup(t *testing.T) {
	cert := entraTestCertificate(t)
	certOnly := cert[:strings.Index(cert, "-----BEGIN PRIVATE KEY-----")]
	for name, in := range map[string]string{"certificate without its key": certOnly, "not a PEM": "paste the certificate here"} {
		_, err := BuiltinConnectors(envMap(entraCertEnv(in)), "entra")
		if err == nil {
			t.Errorf("%s: want a startup error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), `"contoso"`) {
			t.Errorf("%s: error %q does not name the tenant", name, err)
		}
		if strings.Contains(err.Error(), "BEGIN") {
			t.Errorf("%s: error %q quotes the PEM", name, err)
		}
	}
}
