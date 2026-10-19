package worker

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/internal/certstore"
)

func entraThumbprintEnv(thumbprint string) map[string]string {
	return map[string]string{
		"ATLAS_ENTRA_CONNECTORS":                            "contoso",
		"ATLAS_ENTRA_CONTOSO_TENANT_ID":                     "11111111-2222-3333-4444-555555555555",
		"ATLAS_ENTRA_CONTOSO_CLIENT_ID":                     "app-1",
		"ATLAS_ENTRA_CONTOSO_CLIENT_CERTIFICATE_THUMBPRINT": thumbprint,
	}
}

// stubStore stands in for the Windows certificate store, so the worker's half — which
// variable it reads, what it does with what the store answers — is tested anywhere.
func stubStore(t *testing.T, answer func(string) (*x509.Certificate, crypto.Signer, error)) *[]string {
	t.Helper()
	var asked []string
	prev := openStoreCertificate
	openStoreCertificate = func(tp string) (*x509.Certificate, crypto.Signer, error) {
		asked = append(asked, tp)
		return answer(tp)
	}
	t.Cleanup(func() { openStoreCertificate = prev })
	return &asked
}

func storeTestCertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "atlas-entra-store"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// A tenant may name its certificate by thumbprint: the worker asks the certificate store
// for it, as the Teams Phone Worker and PowerShell do, and serves the tenant with what
// the store answers (ADR-0172, amended).
func TestAnEntraTenantMayNameItsCertificateByThumbprint(t *testing.T) {
	cert, key := storeTestCertificate(t)
	asked := stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) { return cert, key, nil })
	got, err := BuiltinConnectors(envMap(entraThumbprintEnv(" 02CB6038F2E91693AE8D31C983569366034B2100 ")), "entra")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.EntraJobType]; !ok {
		t.Errorf("no handler for %s", compiler.EntraJobType)
	}
	if len(*asked) != 1 || (*asked)[0] != "02CB6038F2E91693AE8D31C983569366034B2100" {
		t.Errorf("the store was asked for %q, want the thumbprint once", *asked)
	}
}

// A certificate the store cannot give — not there, a key the account may not use — is a
// startup error naming the tenant, while the operator is watching.
func TestAnEntraThumbprintTheStoreCannotServeIsRefusedAtStartup(t *testing.T) {
	stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		return nil, nil, errors.New("certstore: no certificate with this thumbprint")
	})
	_, err := BuiltinConnectors(envMap(entraThumbprintEnv("02CB6038F2E91693AE8D31C983569366034B2100")), "entra")
	if err == nil || !strings.Contains(err.Error(), `"contoso"`) || !strings.Contains(err.Error(), "no certificate") {
		t.Errorf("err = %v, want a startup error naming the tenant and the store's reason", err)
	}
}

// One credential per tenant: a thumbprint beside a secret or a PEM leaves the choice to
// precedence, so it is refused naming every variable that is set.
func TestAnEntraThumbprintBesideAnotherCredentialIsRefused(t *testing.T) {
	stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		t.Error("the store was asked although the configuration is refused")
		return nil, nil, errors.New("unreachable")
	})
	for other, value := range map[string]string{
		"ATLAS_ENTRA_CONTOSO_CLIENT_SECRET":      "s3cr3t",
		"ATLAS_ENTRA_CONTOSO_CLIENT_CERTIFICATE": entraTestCertificate(t),
	} {
		env := entraThumbprintEnv("02CB6038F2E91693AE8D31C983569366034B2100")
		env[other] = value
		_, err := BuiltinConnectors(envMap(env), "entra")
		if err == nil {
			t.Errorf("%s beside a thumbprint was accepted", other)
			continue
		}
		for _, want := range []string{other, "ATLAS_ENTRA_CONTOSO_CLIENT_CERTIFICATE_THUMBPRINT", "exactly one"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
	}
}

// With no credential at all, the error lists all three ways to give one.
func TestAnEntraTenantWithoutACredentialNamesAllThree(t *testing.T) {
	env := entraThumbprintEnv("")
	_, err := BuiltinConnectors(envMap(env), "entra")
	if err == nil {
		t.Fatal("a tenant without a credential was accepted")
	}
	for _, want := range []string{"_CLIENT_SECRET,", "_CLIENT_CERTIFICATE or", "_CLIENT_CERTIFICATE_THUMBPRINT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// Outside Windows the real store refuses, and the worker passes on why — rather than
// claiming the certificate is missing.
func TestAnEntraThumbprintOutsideWindowsSaysWhy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this is the refusal outside Windows")
	}
	_, err := BuiltinConnectors(envMap(entraThumbprintEnv("02CB6038F2E91693AE8D31C983569366034B2100")), "entra")
	if !errors.Is(err, certstore.ErrUnsupported) {
		t.Errorf("err = %v, want certstore.ErrUnsupported passed on", err)
	}
}
