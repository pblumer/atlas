package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// Google Contacts is worker-only, and the reason a supervised worker can serve it at all
// is that the engine hands its address books over: a task names its Worker and nothing
// more, and the bundle that says whose address book and as whom is a Worker record and
// a vault secret, which a supervised worker cannot read for itself.

// googleContactsBundle is a well-formed bundle, the shape an operator stores in the
// vault under a record's credentialsRef.
const googleContactsBundle = `{"method":"refreshToken","clientId":"id","clientSecret":"s3cr3t","refreshToken":"r"}`

func saveGoogleContactsWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindGoogleContacts,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its clients from: the rendered
// environment is handed to worker.BuiltinConnectors, so a variable named differently on
// either side fails here rather than in a parked job.
func TestSupervisedGoogleContactsEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleContactsWorker(t, srv, "1", "verkauf", "", "gc-verkauf")
	saveGoogleContactsWorker(t, srv, "2", "service", "https://proxy.example/people", "gc-service")
	t.Setenv("ATLAS_CONNECTOR_GC_VERKAUF_TOKEN", googleContactsBundle)
	t.Setenv("ATLAS_CONNECTOR_GC_SERVICE_TOKEN", googleContactsBundle)

	env := envOf(t, srv.googleContactsWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindGoogleContacts)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "verkauf") || !slices.Contains(built.Names, "service") {
		t.Errorf("the worker holds %v, want both address books", built.Names)
	}
	if got := env["ATLAS_GOOGLECONTACTS_SERVICE_URL"]; got != "https://proxy.example/people" {
		t.Errorf("the handed endpoint = %q", got)
	}
	if _, ok := env["ATLAS_GOOGLECONTACTS_VERKAUF_URL"]; ok {
		t.Error("an endpoint was rendered for a record that authored none")
	}
	if got := env["ATLAS_GOOGLECONTACTS_VERKAUF_CREDENTIALS"]; got != googleContactsBundle {
		t.Errorf("the handed bundle = %q, want it whole", got)
	}
}

// An address book whose bundle does not build is left out rather than handed over: the
// worker refuses at startup on a named address book it cannot build, which would take
// down every other one it serves.
func TestSupervisedGoogleContactsEnvSkipsAnUnusableBundle(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"never set", ""},
		{"malformed", "{"},
		{"service account without subject", `{"method":"serviceAccount","clientEmail":"a@p.iam.gserviceaccount.com","privateKey":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveGoogleContactsWorker(t, srv, "1", "verkauf", "", "gc")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_GC_TOKEN", tc.secret)
			}
			if env := srv.googleContactsWorkerEnv(); env != nil {
				t.Errorf("rendered %v for an address book with no usable bundle; want nothing", env)
			}
		})
	}
}

func TestSupervisedGoogleContactsEnvSkipsAFoldingCollision(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleContactsWorker(t, srv, "1", "verkauf-eu", "", "gc")
	saveGoogleContactsWorker(t, srv, "2", "verkauf_eu", "", "gc")
	t.Setenv("ATLAS_CONNECTOR_GC_TOKEN", googleContactsBundle)

	env := envOf(t, srv.googleContactsWorkerEnv())
	if got := env["ATLAS_GOOGLECONTACTS_CONNECTORS"]; strings.Count(got, ",") != 0 {
		t.Errorf("ATLAS_GOOGLECONTACTS_CONNECTORS = %q, want only the first of two names that fold together", got)
	}
}

// An address book an operator set on the host stays in the list the supervised worker
// gets, beside the ones from the store.
func TestSupervisedGoogleContactsEnvKeepsTheHostsAddressBooks(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleContactsWorker(t, srv, "1", "verkauf", "", "gc")
	t.Setenv("ATLAS_CONNECTOR_GC_TOKEN", googleContactsBundle)
	t.Setenv("ATLAS_GOOGLECONTACTS_CONNECTORS", "host-buch")

	env := envOf(t, srv.googleContactsWorkerEnv())
	if got := env["ATLAS_GOOGLECONTACTS_CONNECTORS"]; got != "host-buch,verkauf" {
		t.Errorf("ATLAS_GOOGLECONTACTS_CONNECTORS = %q", got)
	}
}

// The intent behind the default, pinned apart from the mechanism: Google Contacts is
// supervised from the first boot, parked until an address book is added, and its
// bundle is handed over once one is.
func TestGoogleContactsIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindGoogleContacts) {
		t.Error("googlecontacts is not supervised by default: adding an address book in the Console would need a deployment change")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindGoogleContacts]; !provisioned {
		t.Error("googlecontacts is supervised but its bundle is not handed over")
	}
}

// A record without a bundle is refused where the operator is looking.
func TestValidateGoogleContactsWorkerNeedsABundle(t *testing.T) {
	p := createConnectorParams{Provider: "x", Sender: "y"}
	if msg := validateGoogleContactsConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{CredentialsRef: "gc", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateGoogleContactsConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}
