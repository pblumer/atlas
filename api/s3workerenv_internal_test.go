package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// S3 is offloaded by default, and the reason it can be is that the engine hands its
// identities over. An object-store task names its Worker and nothing more — the access
// key is a Worker record and a vault secret, which a supervised worker can read no more
// than it can read the engine's memory.
//
// Without this handover the default would have moved every document a process handles to
// a worker with no bucket to put it in, which is the failure mail had before ADR-0168.

// s3Bundle is a well-formed access key, the shape an operator stores in the vault under
// the record's credentialsRef.
const s3Bundle = `{"accessKeyId":"AKIAEXAMPLE","secretAccessKey":"s3cr3t","region":"eu-central-1"}`

func saveS3Worker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindS3,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds a client from. This is the test that
// actually holds the two halves together: it takes the rendered environment and asks
// worker.BuiltinConnectors to configure itself from it, so a variable named differently
// on either side fails here rather than in a parked job.
func TestSupervisedS3EnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveS3Worker(t, srv, "1", "archiv", "", "s3-creds")
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", s3Bundle)

	env := envOf(t, srv.s3WorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindS3)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "archiv") {
		t.Errorf("the worker holds %v, want the identity the engine handed it", built.Names)
	}
}

// The credential travels as the whole bundle rather than one variable per field, and the
// endpoint travels beside it as itself. Splitting the bundle would mean deciding the
// credential's shape a second time on the far side; splitting the endpoint out is right
// because it is not secret and is the one piece an operator changes without reissuing a
// key.
func TestSupervisedS3EnvHandsOverTheBundleAndTheEndpoint(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveS3Worker(t, srv, "1", "minio", "https://minio.example:9000", "s3-creds")
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", s3Bundle)

	env := envOf(t, srv.s3WorkerEnv())
	if got := env["ATLAS_S3_MINIO_CREDENTIALS"]; got != s3Bundle {
		t.Errorf("the handed credential = %q, want the whole bundle", got)
	}
	if got := env["ATLAS_S3_MINIO_URL"]; got != "https://minio.example:9000" {
		t.Errorf("the handed endpoint = %q, want the store's base URL", got)
	}
	if got := env["ATLAS_S3_CONNECTORS"]; got != "minio" {
		t.Errorf("ATLAS_S3_CONNECTORS = %q, want the identity the store contributed", got)
	}
}

// A record with no endpoint is AWS at the bundle's region, and nothing is rendered for
// it — which is what buildS3Clients passes too, so both sides build the same client.
func TestSupervisedS3EnvRendersNoEndpointForAWS(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveS3Worker(t, srv, "1", "archiv", "", "s3-creds")
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", s3Bundle)

	env := envOf(t, srv.s3WorkerEnv())
	if _, ok := env["ATLAS_S3_ARCHIV_URL"]; ok {
		t.Error("an endpoint was rendered for a record that authored none")
	}
}

// An identity whose vault secret is not set yet, or whose bundle is short of a region,
// is left out rather than handed over empty. Handed over empty, the worker refuses at
// startup on a *named* identity it cannot build — which takes down every other kind that
// worker serves.
func TestSupervisedS3EnvSkipsAnUnresolvedCredential(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"never set", ""},
		{"malformed", "{"},
		{"no access key", `{"region":"eu-central-1"}`},
		{"no region", `{"accessKeyId":"A","secretAccessKey":"B"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveS3Worker(t, srv, "1", "archiv", "", "s3-creds")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", tc.secret)
			}
			if env := srv.s3WorkerEnv(); env != nil {
				t.Errorf("rendered %v for an identity with no usable credential; want nothing", env)
			}
		})
	}
}

// Two Worker names that fold to one environment variable would silently give one the
// other's key — a worker writing into the wrong bucket under the wrong identity, which
// is the mail/jira collision and is left out for the same reason.
func TestSupervisedS3EnvSkipsAFoldingCollision(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveS3Worker(t, srv, "1", "archiv-eu", "", "s3-creds")
	saveS3Worker(t, srv, "2", "archiv_eu", "", "s3-creds")
	t.Setenv("ATLAS_CONNECTOR_S3_CREDS_TOKEN", s3Bundle)

	env := envOf(t, srv.s3WorkerEnv())
	if got := env["ATLAS_S3_CONNECTORS"]; strings.Count(got, ",") != 0 {
		t.Errorf("ATLAS_S3_CONNECTORS = %q, want only the first of two names that fold together", got)
	}
}

// The intent behind the default, pinned separately from the mechanism: a fresh install
// must not hold an access key on the engine's run loop, and must not move a document's
// bytes through it. ADR-0164 puts every connector task on a worker anyway.
func TestS3IsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults[connectorKindS3] {
		t.Error("s3 is not offloaded by default: a fresh install still calls the object store from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindS3]; !provisioned {
		t.Error("s3 is defaulted onto a worker but its credential is not handed over")
	}
}

// And the outcome the author actually sees. The badge is computed from what the job
// runner holds after applyOffloadedKinds has run, so this exercises the default the way
// a booted server does rather than re-deriving it from the list.
func TestS3BadgeSaysOnAWorker(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(DefaultOffloadedKinds()))
	var got string
	srv.do(func() { got = srv.placementOfCatalogKind(connectorKindS3) })
	if got != placementWorker {
		t.Errorf("the Modeler shows s3 as %q, want %q — a connector task belongs on a worker (ADR-0164)", got, placementWorker)
	}
}
