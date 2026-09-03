package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
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

// TestBuildGoogleSheetsClients keeps a connector out of the registry unless it is
// enabled and its credentialsRef resolves to a usable bundle. Each exclusion records
// *why* on the connector instead (ADR-0158), because "no connector registered as X"
// reads as "you never configured it" when the truth is that the key is malformed.
//
// Unlike Jira there is no endpoint to be missing: Google's API bases are the same for
// everyone, so a record with only a credential is complete.
func TestBuildGoogleSheetsClients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// The bundle lives in the vault; here it resolves from the env fallback
	// (ATLAS_CONNECTOR_<REF>_TOKEN), never from the record itself.
	good, err := json.Marshal(googleSheetsCredentials{
		Method: "serviceAccount", ClientEmail: "atlas@x.iam.gserviceaccount.com", PrivateKey: googleKeyPEM(t),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", string(good))
	t.Setenv("ATLAS_CONNECTOR_BAD_GOOGLE_TOKEN", `not valid json`)
	t.Setenv("ATLAS_CONNECTOR_HALF_GOOGLE_TOKEN", `{"method":"serviceAccount","clientEmail":"a@b"}`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "google", Kind: connectorKindGoogleSheets, CredentialsRef: "google_creds", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "off", Kind: connectorKindGoogleSheets, CredentialsRef: "google_creds", Enabled: false, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "nocred", Kind: connectorKindGoogleSheets, Enabled: true, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "broken", Kind: connectorKindGoogleSheets, CredentialsRef: "bad_google", Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "halfbundle", Kind: connectorKindGoogleSheets, CredentialsRef: "half_google", Enabled: true, CreatedAt: 5})
	_ = srv.connectors.Save(connector{ID: "6", Name: "amail", Kind: connectorKindMail, Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 6})

	clients, problems, err := srv.buildGoogleSheetsClients()
	if err != nil {
		t.Fatalf("buildGoogleSheetsClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %v, want only the usable record", clients)
	}
	if _, ok := clients["google"]; !ok {
		t.Errorf("clients = %v, want the service-account record among them", clients)
	}
	// And every exclusion says why, in words pointed at the fix.
	for _, tc := range []struct{ name, want string }{
		{"off", "disabled"},
		{"nocred", "no credential"},
		{"broken", "not valid JSON"},
		{"halfbundle", "clientEmail and privateKey"},
		{"amail", "not \"googlesheets\""}, // a mail connector named by a Sheets task
	} {
		got, ok := problems[tc.name]
		if !ok {
			t.Errorf("no problem recorded for %q; a parked task would say only that nothing is registered", tc.name)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("problem[%q] = %q, want it to mention %q", tc.name, got, tc.want)
		}
	}
}

// TestBuildGoogleSheetsClientsLoadError covers the store-read failure.
func TestBuildGoogleSheetsClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildGoogleSheetsClients(); err == nil {
		t.Error("buildGoogleSheetsClients with a broken store: want error")
	}
}

// TestDescribeGoogleWatch renders each of the two watches in its own words — what the
// Modeler's message-sources view shows beside a message-start event, so an author can
// see where the messages it waits for actually come from.
func TestDescribeGoogleWatch(t *testing.T) {
	for name, tc := range map[string]struct {
		sub  inboundSubscription
		want string
	}{
		"a row watch":           {inboundSubscription{SpreadsheetID: "1B", WatchRange: "Antraege!A:D"}, "new rows in spreadsheet 1B (Antraege!A:D)"},
		"a row watch, no range": {inboundSubscription{SpreadsheetID: "1B"}, "new rows in spreadsheet 1B (" + sheetsDefaultRange + ")"},
		"a folder watch":        {inboundSubscription{FolderID: "fold"}, "files created in Drive folder fold"},
		"a changed-file watch":  {inboundSubscription{FolderID: "fold", CursorField: "modified"}, "files modified in Drive folder fold"},
	} {
		if got := describeInboundWatch(connectorKindGoogleSheets, tc.sub); got != tc.want {
			t.Errorf("%s: describeInboundWatch = %q, want %q", name, got, tc.want)
		}
	}
}
