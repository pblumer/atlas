package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// Google Sheets is offloaded by default, and the reason it can be is that the engine
// hands its identities over. A Google Sheets task names its Worker and nothing more —
// the credential is a worker record and a vault secret, which a supervised worker can
// read no more than it can read the engine's memory.
//
// Without this handover the default would have moved every spreadsheet task to a
// worker with no identity to act as, which is the failure mail had before ADR-0168.

// googleBundle is a well-formed service-account bundle, the shape an operator stores
// in the vault under the record's credentialsRef. The key is generated rather than
// pasted because the far side parses it: a placeholder would make this test pass on a
// handover a real worker refuses.
func googleBundle(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return `{"method":"serviceAccount","clientEmail":"atlas@x.iam.gserviceaccount.com","privateKey":` +
		quoteJSON(pemKey) + `}`
}

// quoteJSON renders a string as a JSON string literal, so the PEM's newlines survive
// into the bundle as \n rather than breaking it.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func saveGoogleSheetsWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindGoogleSheets,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds a client from. This is the test that
// actually holds the two halves together: it takes the rendered environment and asks
// worker.BuiltinConnectors to configure itself from it, so a variable named differently
// on either side fails here rather than in a parked job.
func TestSupervisedGoogleSheetsEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "acme", "", "google-creds")
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", googleBundle(t))

	env := envOf(t, srv.googleSheetsWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindGoogleSheets)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "acme") {
		t.Errorf("the worker holds %v, want the identity the engine handed it", built.Names)
	}
}

// The credential travels as the whole bundle, one value, rather than field by field.
// That is the decision worth pinning: the service account's address sits in the same
// vault secret as its private key, so there is no public half worth splitting, and a
// renderer that took it apart would be deciding the grant's shape a second time —
// where getting it wrong yields a worker that fails every job.
func TestSupervisedGoogleSheetsEnvHandsOverTheWholeBundle(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "acme", "", "google-creds")
	bundle := googleBundle(t)
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", bundle)

	env := envOf(t, srv.googleSheetsWorkerEnv())
	if got := env["ATLAS_GOOGLESHEETS_ACME_CREDENTIALS"]; got != bundle {
		t.Errorf("the handed credential = %q, want the bundle verbatim", got)
	}
	// No endpoint was authored, so none is rendered: blank means Google's own API
	// bases, which is what the engine's own client build passes too.
	if _, ok := env["ATLAS_GOOGLESHEETS_ACME_ENDPOINT"]; ok {
		t.Error("an endpoint was rendered for a record that authored none")
	}
}

// An identity whose vault secret is not set yet is left out rather than handed over
// empty. Handed over empty, the worker refuses at startup on a *named* identity it
// cannot build — which takes down every other kind that worker serves.
func TestSupervisedGoogleSheetsEnvSkipsAnUnresolvedCredential(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "acme", "", "never-set")

	if env := srv.googleSheetsWorkerEnv(); env != nil {
		t.Errorf("rendered %v for an identity with no credential; want nothing", env)
	}
}

// The intent behind the default, pinned separately from the mechanism: a fresh install
// must not hold a service-account private key on the engine's run loop. It is the most
// dangerous credential in this integration — it is the whole identity — and ADR-0164
// puts every connector task on a worker anyway.
func TestGoogleSheetsIsOffloadedByDefault(t *testing.T) {
	defaults := map[string]bool{}
	for _, kind := range DefaultOffloadedKinds() {
		defaults[kind] = true
	}
	if !defaults[connectorKindGoogleSheets] {
		t.Error("googlesheets is not offloaded by default: a fresh install still calls Google from the engine's run loop")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindGoogleSheets]; !provisioned {
		t.Error("googlesheets is defaulted onto a worker but its credential is not handed over")
	}
}

// And the outcome the author actually sees. The badge is computed from what the job
// runner holds after applyOffloadedKinds has run, so this exercises the default the
// way a booted server does rather than re-deriving it from the list.
//
// It is worth a test of its own because the badge was the only place the omission was
// visible: everything compiled, every test passed, and the properties panel said
// IN-ENGINE beside twelve Worker Types that said otherwise.
func TestGoogleSheetsBadgeSaysOnAWorker(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(DefaultOffloadedKinds()))
	var got string
	srv.do(func() { got = srv.placementOfCatalogKind(connectorKindGoogleSheets) })
	if got != placementWorker {
		t.Errorf("the Modeler shows googlesheets as %q, want %q — a connector task belongs on a worker (ADR-0164)", got, placementWorker)
	}
}

// An identity an operator set on the host is inherited by the child already, so
// nothing is rendered for it — but its name must stay in the list, or a store identity
// would silently take the whole list away from it and the host one would stop being
// served the moment somebody added a record in the Console.
func TestSupervisedGoogleSheetsEnvKeepsHostNamesInTheList(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "aus-dem-store", "", "google-creds")
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", googleBundle(t))
	t.Setenv("ATLAS_GOOGLESHEETS_CONNECTORS", "vom-host")

	env := envOf(t, srv.googleSheetsWorkerEnv())
	names := strings.Split(env["ATLAS_GOOGLESHEETS_CONNECTORS"], ",")
	if !slices.Contains(names, "vom-host") || !slices.Contains(names, "aus-dem-store") {
		t.Errorf("the rendered list is %v, want the union of host and store identities", names)
	}
	// Nothing is rendered for the host one: the child inherits its variables as they are.
	if _, ok := env["ATLAS_GOOGLESHEETS_VOM_HOST_CREDENTIALS"]; ok {
		t.Error("a credential was rendered for an identity the child already inherits")
	}
}

// Two names that fold to one environment variable would silently give one the other's
// credential — a service-account key handed to the wrong identity. The second is left
// out rather than overwriting the first.
func TestSupervisedGoogleSheetsEnvRefusesACollidingName(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "acme-eins", "", "google-creds")
	saveGoogleSheetsWorker(t, srv, "2", "acme.eins", "", "google-creds")
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", googleBundle(t))

	env := envOf(t, srv.googleSheetsWorkerEnv())
	names := strings.Split(env["ATLAS_GOOGLESHEETS_CONNECTORS"], ",")
	if len(names) != 1 {
		t.Errorf("the rendered list is %v, want only the identity that won the fold", names)
	}
	// Whichever won, exactly one credential variable exists for the folded name.
	if _, ok := env["ATLAS_GOOGLESHEETS_ACME_EINS_CREDENTIALS"]; !ok {
		t.Errorf("env = %v, want the one folded credential", env)
	}
}

// An authored endpoint is rendered; a record of another kind, and a disabled one, are
// not this worker's to serve.
func TestSupervisedGoogleSheetsEnvRendersAnEndpointAndSkipsWhatIsNotItsOwn(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "acme", "https://sheets.internal", "google-creds")
	if err := srv.connectors.Save(connector{
		ID: "2", Name: "aus", Kind: connectorKindGoogleSheets, CredentialsRef: "google-creds", Enabled: false, CreatedAt: 2,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "3", Name: "post", Kind: connectorKindMail, Endpoint: "smtp:587", Sender: "a@x", Enabled: true, CreatedAt: 3,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", googleBundle(t))

	env := envOf(t, srv.googleSheetsWorkerEnv())
	if got := env["ATLAS_GOOGLESHEETS_ACME_ENDPOINT"]; got != "https://sheets.internal" {
		t.Errorf("endpoint = %q, want the authored override", got)
	}
	if got := env["ATLAS_GOOGLESHEETS_CONNECTORS"]; got != "acme" {
		t.Errorf("the rendered list is %q, want only the enabled Google identity", got)
	}
}

// A store that cannot be read renders nothing rather than a half list: a supervised
// worker handed half its identities would serve some tasks and park others with no
// sign of why.
func TestSupervisedGoogleSheetsEnvOnAnUnreadableStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
	if env := srv.googleSheetsWorkerEnv(); env != nil {
		t.Errorf("rendered %v from a store that cannot be read; want nothing", env)
	}
}

// A name with nothing an environment variable can be built from is left out rather
// than rendered as ATLAS_GOOGLESHEETS__CREDENTIALS, which would be one nameless slot
// every such identity overwrote in turn.
func TestSupervisedGoogleSheetsEnvSkipsANameThatFoldsToNothing(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveGoogleSheetsWorker(t, srv, "1", "—", "", "google-creds")
	t.Setenv("ATLAS_CONNECTOR_GOOGLE_CREDS_TOKEN", googleBundle(t))

	if env := srv.googleSheetsWorkerEnv(); env != nil {
		t.Errorf("rendered %v for a name no variable can carry; want nothing", env)
	}
}
