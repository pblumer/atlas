package worker

import (
	"crypto"
	"crypto/x509"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/internal/certstore"
)

// A SharePoint instance whose app certificate stays in the Windows certificate store,
// named by its thumbprint — the Entra worker's arrangement (ADR-0172, amended), so one
// certificate in the host's store serves both (ADR-0141, amended).

func sharePointThumbprintEnv(thumbprint string) map[string]string {
	return map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS": "intranet",
		"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1",` +
			`"certificateThumbprint":"` + thumbprint + `"}`,
	}
}

// The worker asks the store for the certificate at startup, as the Entra worker does,
// and serves the instance with what the store answers.
func TestASharePointInstanceMayNameItsCertificateByThumbprint(t *testing.T) {
	cert, key := storeTestCertificate(t)
	asked := stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) { return cert, key, nil })
	got, err := BuiltinConnectors(envMap(sharePointThumbprintEnv(" 02CB6038F2E91693AE8D31C983569366034B2100 ")), "sharepoint")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.SharePointJobType]; !ok {
		t.Errorf("no handler for %s", compiler.SharePointJobType)
	}
	if len(*asked) != 1 || (*asked)[0] != "02CB6038F2E91693AE8D31C983569366034B2100" {
		t.Errorf("the store was asked for %q, want the thumbprint once", *asked)
	}
}

// A certificate the store cannot give — not there, a key the account may not use — is a
// startup error naming the instance, while the operator is watching, not a failed token
// request in the middle of a run.
func TestASharePointThumbprintTheStoreCannotServeIsRefusedAtStartup(t *testing.T) {
	stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		return nil, nil, errors.New("certstore: no certificate with this thumbprint")
	})
	_, err := BuiltinConnectors(envMap(sharePointThumbprintEnv("02CB6038F2E91693AE8D31C983569366034B2100")), "sharepoint")
	if err == nil || !strings.Contains(err.Error(), `"intranet"`) || !strings.Contains(err.Error(), "no certificate") {
		t.Errorf("err = %v, want a startup error naming the instance and the store's reason", err)
	}
}

// A bundle that is refused on its own terms — here a thumbprint beside a secret — is
// refused before the store is asked.
func TestASharePointThumbprintBesideASecretIsRefusedWithoutAskingTheStore(t *testing.T) {
	stubStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		t.Error("the store was asked although the bundle is refused")
		return nil, nil, errors.New("unreachable")
	})
	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS": "intranet",
		"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1",` +
			`"clientSecret":"s3cr3t","certificateThumbprint":"02CB6038F2E91693AE8D31C983569366034B2100"}`,
	}
	_, err := BuiltinConnectors(envMap(env), "sharepoint")
	if err == nil || !strings.Contains(err.Error(), "give one, not both") {
		t.Errorf("err = %v, want the bundle refused for carrying two credentials", err)
	}
}

// Outside Windows the real store refuses, and the worker passes on why — rather than
// claiming the certificate is missing.
func TestASharePointThumbprintOutsideWindowsSaysWhy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this is the refusal outside Windows")
	}
	_, err := BuiltinConnectors(envMap(sharePointThumbprintEnv("02CB6038F2E91693AE8D31C983569366034B2100")), "sharepoint")
	if !errors.Is(err, certstore.ErrUnsupported) {
		t.Errorf("err = %v, want certstore.ErrUnsupported passed on", err)
	}
}
