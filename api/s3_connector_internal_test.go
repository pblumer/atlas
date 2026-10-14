package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/s3"
)

// TestS3BundleShapeMatchesTheWorker is the drift guard between this package's
// description of the credential bundle — which is what the Console's shape hint, the
// worker dialog and the handbook are written from — and the decoder that actually reads
// it. The two are separate types because connector/s3's is unexported, so nothing but a
// test holds them together.
func TestS3BundleShapeMatchesTheWorker(t *testing.T) {
	raw, err := json.Marshal(s3Credentials{
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "s3cr3t", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := s3.NewProviderClient(s3.ProviderConfig{Secret: string(raw)}); err != nil {
		t.Fatalf("connector/s3 rejected a bundle this package describes (%s): %v", raw, err)
	}
	// The session-token field is what an STS-issued key needs, and it has to survive the
	// same round trip — a field this package names and the worker ignores would be a
	// Console hint pointing at nothing.
	raw, _ = json.Marshal(s3Credentials{
		AccessKeyID: "A", SecretAccessKey: "B", Region: "us-east-1", SessionToken: "T",
	})
	if _, _, _, token, err := s3.CredentialsFromBundle(string(raw)); err != nil || token != "T" {
		t.Errorf("session token = %q (%v), want it read back", token, err)
	}
}

// TestValidateS3WorkerNeedsACredential: an unsigned request to an object store is
// refused by every store worth using, so a record without a credential is a Worker that
// can never do anything — and the refusal names the bundle shape rather than leaving an
// operator to guess.
func TestValidateS3WorkerNeedsACredential(t *testing.T) {
	p := &createConnectorParams{Kind: connectorKindS3, Provider: "gmail", Sender: "x@y", Model: "claude"}
	msg := validateS3Connector(p)
	if !strings.Contains(msg, "credentialsRef") {
		t.Errorf("message %q should name the missing credentialsRef", msg)
	}
	for _, field := range []string{"accessKeyId", "secretAccessKey", "region"} {
		if !strings.Contains(msg, field) {
			t.Errorf("message %q should name the bundle field %q", msg, field)
		}
	}
	// The fields of other Worker Types are cleared rather than stored on one that has no
	// use for them.
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("params = %+v; want the fields of other kinds cleared", p)
	}
	// No endpoint is demanded: an empty one means AWS at the bundle's region, which is
	// the whole configuration an AWS installation needs.
	if msg := validateS3Connector(&createConnectorParams{
		Kind: connectorKindS3, CredentialsRef: "s3_archiv",
	}); msg != "" {
		t.Errorf("a record with a credentialsRef and no endpoint was refused: %s", msg)
	}
}

// TestS3IsAConfigurableWorkerType ties the registry entry to the reserved job type it
// serves: an entry naming the wrong index would compile and then leave every object-store
// task unhandled.
func TestS3IsAConfigurableWorkerType(t *testing.T) {
	kind, ok := lookupManagedConnectorKind(connectorKindS3)
	if !ok {
		t.Fatal("s3 is not an operator-configurable Worker Type")
	}
	if kind.workerOnly {
		t.Error("s3 is marked worker-only, but the engine registers a handler for it")
	}
	if len(kind.jobTypes) != 1 || kind.jobTypes[0] != compiler.S3JobTypeIndex {
		t.Fatalf("jobTypes = %v; want the one reserved index", kind.jobTypes)
	}
}

// TestBuildS3Clients keeps a Worker out of the registry unless it is enabled and its
// credentialsRef resolves to a usable bundle. Each exclusion records *why* on the Worker
// instead (ADR-0158), because "nothing registered under that name" reads as "you never
// configured it" when the truth is that the region is missing.
func TestBuildS3Clients(t *testing.T) {
	srv, _ := newValidateServer(t)
	// The bundle lives in the vault; here it resolves from the env fallback
	// (ATLAS_CONNECTOR_<REF>_TOKEN), never from the record itself.
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", `{"accessKeyId":"AKIA","secretAccessKey":"s3cr3t","region":"eu-central-1"}`)
	t.Setenv("ATLAS_CONNECTOR_BAD_S3_TOKEN", `not valid json`)
	t.Setenv("ATLAS_CONNECTOR_NOREGION_S3_TOKEN", `{"accessKeyId":"AKIA","secretAccessKey":"s3cr3t"}`)

	_ = srv.connectors.Save(connector{ID: "1", Name: "archiv", Kind: connectorKindS3, CredentialsRef: "s3_creds", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "minio", Kind: connectorKindS3, Endpoint: "https://minio.example:9000", CredentialsRef: "s3_creds", Enabled: true, CreatedAt: 2})
	_ = srv.connectors.Save(connector{ID: "3", Name: "off", Kind: connectorKindS3, CredentialsRef: "s3_creds", Enabled: false, CreatedAt: 3})
	_ = srv.connectors.Save(connector{ID: "4", Name: "nocred", Kind: connectorKindS3, Enabled: true, CreatedAt: 4})
	_ = srv.connectors.Save(connector{ID: "5", Name: "broken", Kind: connectorKindS3, CredentialsRef: "bad_s3", Enabled: true, CreatedAt: 5})
	_ = srv.connectors.Save(connector{ID: "6", Name: "noregion", Kind: connectorKindS3, CredentialsRef: "noregion_s3", Enabled: true, CreatedAt: 6})
	_ = srv.connectors.Save(connector{ID: "7", Name: "amail", Kind: connectorKindMail, Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 7})

	clients, problems, err := srv.buildS3Clients()
	if err != nil {
		t.Fatalf("buildS3Clients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients = %v, want the two usable records", clients)
	}
	for _, name := range []string{"archiv", "minio"} {
		if _, ok := clients[name]; !ok {
			t.Errorf("clients = %v, want %q among them", clients, name)
		}
	}
	// And every exclusion says why, in words pointed at the fix.
	for _, tc := range []struct{ name, want string }{
		{"off", "disabled"},
		{"nocred", "no credential"},
		{"broken", "not valid JSON"},
		{"noregion", "region"},
		{"amail", "not \"s3\""}, // a mail Worker named by an object-store task
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

// TestBuildS3ClientsLoadError covers the store-read failure.
func TestBuildS3ClientsLoadError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if _, _, err := srv.buildS3Clients(); err == nil {
		t.Error("buildS3Clients with a broken store: want error")
	}
}
