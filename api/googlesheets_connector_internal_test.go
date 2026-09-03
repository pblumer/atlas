package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/googlesheets"
)

// googleKeyPEM is a fresh service-account signing key in the format Google's JSON key
// file carries.
func googleKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// TestGoogleSheetsBundleShapeMatchesTheConnector is the drift guard between this
// package's description of the credential bundle — which is what the Console's shape
// hint and the vault documentation are written from — and the decoder that actually
// reads it. The two are separate types because the connector's is unexported, so
// nothing but a test holds them together.
func TestGoogleSheetsBundleShapeMatchesTheConnector(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle googleSheetsCredentials
	}{
		{"service account", googleSheetsCredentials{
			Method: "serviceAccount", ClientEmail: "atlas@x.iam.gserviceaccount.com", PrivateKey: googleKeyPEM(t),
		}},
		{"service account with delegation", googleSheetsCredentials{
			Method: "serviceAccount", ClientEmail: "atlas@x.iam.gserviceaccount.com",
			PrivateKey: googleKeyPEM(t), Subject: "person@example.com",
		}},
		{"refresh token", googleSheetsCredentials{
			Method: "refreshToken", ClientID: "c", ClientSecret: "s", RefreshToken: "r",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.bundle)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := googlesheets.NewProviderClient(googlesheets.ProviderConfig{Secret: string(raw)}); err != nil {
				t.Fatalf("the connector package rejected a bundle this package describes (%s): %v", raw, err)
			}
		})
	}
}

// TestValidateGoogleSheetsConnectorNeedsACredential: for this kind the credential *is*
// the whole configuration — Google's API bases are the same for everyone — so a record
// without one is a Worker that can never do anything, and the refusal names both
// bundle shapes rather than leaving an operator to guess.
func TestValidateGoogleSheetsConnectorNeedsACredential(t *testing.T) {
	p := &createConnectorParams{Kind: connectorKindGoogleSheets, Provider: "gmail", Sender: "x@y"}
	msg := validateGoogleSheetsConnector(p)
	if !strings.Contains(msg, "credentialsRef") {
		t.Errorf("message %q should name the missing credentialsRef", msg)
	}
	for _, want := range []string{"serviceAccount", "refreshToken"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should name the %s bundle shape", msg, want)
		}
	}
	// The mail-only fields are cleared rather than stored on a kind that has no use
	// for them.
	if p.Provider != "" || p.Sender != "" {
		t.Errorf("params = %+v; want the mail-only fields cleared", p)
	}
	if msg := validateGoogleSheetsConnector(&createConnectorParams{
		Kind: connectorKindGoogleSheets, CredentialsRef: "google_auth",
	}); msg != "" {
		t.Errorf("a record with a credentialsRef was refused: %s", msg)
	}
}

// TestGoogleSheetsIsAManagedKind ties the registry entry to the reserved job type it
// serves: an entry naming the wrong index would compile and then leave every Google
// Sheets task unhandled.
func TestGoogleSheetsIsAManagedKind(t *testing.T) {
	kind, ok := lookupManagedConnectorKind(connectorKindGoogleSheets)
	if !ok {
		t.Fatal("googlesheets is not a managed connector kind")
	}
	if kind.workerOnly {
		t.Error("googlesheets is marked worker-only, but the engine registers a handler for it")
	}
	if len(kind.jobTypes) != 1 {
		t.Fatalf("jobTypes = %v; want the one reserved index", kind.jobTypes)
	}
}
