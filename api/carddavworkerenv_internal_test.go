package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// CardDAV is worker-only, and the reason a supervised worker can serve it at all is that
// the engine hands its address books over: a task names its Worker and nothing more, and
// the endpoint and the bundle with the password are a Worker record and a vault secret,
// which a supervised worker cannot read for itself.

// cardDAVBundle is a well-formed bundle, the shape an operator stores in the vault under
// a record's credentialsRef.
const cardDAVBundle = `{"username":"verkauf@firma.ch","password":"s3cr3t","groups":"categories"}`

func saveCardDAVWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindCardDAV,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its clients from: the rendered
// environment is handed to worker.BuiltinConnectors, so a variable named differently on
// either side fails here rather than in a parked job.
func TestSupervisedCardDAVEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveCardDAVWorker(t, srv, "1", "verkauf", "https://cloud.example.ch/remote.php/dav", "dav-verkauf")
	saveCardDAVWorker(t, srv, "2", "privat", "https://contacts.icloud.com", "dav-privat")
	t.Setenv("ATLAS_CONNECTOR_DAV_VERKAUF_TOKEN", cardDAVBundle)
	t.Setenv("ATLAS_CONNECTOR_DAV_PRIVAT_TOKEN", `{"username":"anna@icloud.com","password":"p","groups":"group-vcards"}`)

	env := envOf(t, srv.cardDAVWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindCardDAV)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "verkauf") || !slices.Contains(built.Names, "privat") {
		t.Errorf("the worker holds %v, want both address books", built.Names)
	}
	if got := env["ATLAS_CARDDAV_VERKAUF_URL"]; got != "https://cloud.example.ch/remote.php/dav" {
		t.Errorf("the handed endpoint = %q", got)
	}
	if got := env["ATLAS_CARDDAV_VERKAUF_CREDENTIALS"]; got != cardDAVBundle {
		t.Errorf("the handed bundle = %q, want it whole", got)
	}
}

// An address book whose endpoint or bundle does not build is left out rather than handed
// over: the worker refuses at startup on a named address book it cannot build, which
// would take down every other one it serves.
func TestSupervisedCardDAVEnvSkipsAnUnusableWorker(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, secret string }{
		{"never set", "https://dav.example.ch", ""},
		{"malformed", "https://dav.example.ch", "{"},
		{"no password", "https://dav.example.ch", `{"username":"u"}`},
		{"plain http to another host", "http://dav.example.ch", cardDAVBundle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveCardDAVWorker(t, srv, "1", "verkauf", tc.endpoint, "dav")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_DAV_TOKEN", tc.secret)
			}
			if env := srv.cardDAVWorkerEnv(); env != nil {
				t.Errorf("rendered %v for an address book that cannot be used; want nothing", env)
			}
		})
	}
}

func TestSupervisedCardDAVEnvSkipsAFoldingCollisionAndKeepsTheHosts(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveCardDAVWorker(t, srv, "1", "verkauf-eu", "https://dav.example.ch", "dav")
	saveCardDAVWorker(t, srv, "2", "verkauf_eu", "https://dav.example.ch", "dav")
	t.Setenv("ATLAS_CONNECTOR_DAV_TOKEN", cardDAVBundle)
	t.Setenv("ATLAS_CARDDAV_CONNECTORS", "host-buch")

	env := envOf(t, srv.cardDAVWorkerEnv())
	if got := env["ATLAS_CARDDAV_CONNECTORS"]; got != "host-buch,verkauf-eu" {
		t.Errorf("ATLAS_CARDDAV_CONNECTORS = %q, want the host's book and only the first of two names that fold together", got)
	}
}

// The intent behind the default, pinned apart from the mechanism: CardDAV is supervised
// from the first boot, parked until an address book is added, and its endpoint and
// bundle are handed over once one is.
func TestCardDAVIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindCardDAV) {
		t.Error("carddav is not supervised by default: adding an address book in the Console would need a deployment change")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindCardDAV]; !provisioned {
		t.Error("carddav is supervised but its bundle is not handed over")
	}
}

// A record without an endpoint or a bundle is refused where the operator is looking.
func TestValidateCardDAVWorker(t *testing.T) {
	p := createConnectorParams{CredentialsRef: "dav"}
	if msg := validateCardDAVConnector(&p); !strings.Contains(msg, "endpoint") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://dav.example.ch"}
	if msg := validateCardDAVConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://dav.example.ch", CredentialsRef: "dav", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateCardDAVConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}
