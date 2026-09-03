package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// Active Directory as a Console worker (ADR-0206).
//
// AD used to be the one credential-bearing kind an operator could not create: the
// directory lived in the model, so there was nothing to configure and nowhere
// per-directory to hang anything — which is what made the mockup switch org-wide and
// made "I serve two forests" unanswerable. A record fixes the shape; these tests pin
// that the record actually reaches the worker that has to bind with it.

// The payoff: an operator adds a directory in the Console and the supervised worker
// binds to it, having been told nothing by hand.
func TestASupervisedWorkerIsHandedTheADDirectoriesFromTheStore(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://localhost:8080", nil, nil))
	if _, err := srv.vault.Set("ad-prod", `{"bindDN":"cn=svc-atlas,ou=Dienstkonten,dc=example,dc=com","password":"hunter2"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "Prod Forest", Kind: connectorKindAD,
		Endpoint: "ldaps://dc.example.com:636", CredentialsRef: "ad-prod",
		Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}

	env := envOf(t, srv.adWorkerEnv())
	if got := env["ATLAS_AD_CONNECTORS"]; got != "Prod Forest" {
		t.Errorf("ATLAS_AD_CONNECTORS = %q, want the directory's own name", got)
	}
	for name, want := range map[string]string{
		"ATLAS_AD_PROD_FOREST_URL":      "ldaps://dc.example.com:636",
		"ATLAS_AD_PROD_FOREST_BIND_DN":  "cn=svc-atlas,ou=Dienstkonten,dc=example,dc=com",
		"ATLAS_AD_PROD_FOREST_PASSWORD": "hunter2",
	} {
		if got := env[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// The engine and the worker must agree on every variable name, or the handover is a
// set of variables nobody reads. Building a real worker from what the engine rendered
// is the only check that cannot drift.
func TestSupervisedADDirectoryEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if _, err := srv.vault.Set("ad-prod", `{"bindDN":"cn=svc,dc=x","password":"pw"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "forest", Kind: connectorKindAD,
		Endpoint: "ldaps://dc:636", CredentialsRef: "ad-prod", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}

	env := envOf(t, srv.adWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindAD)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if len(built.Names) != 1 || built.Names[0] != "forest" {
		t.Errorf("the worker holds %v, want the directory the engine handed it", built.Names)
	}
}

// Two forests, two records, one worker. This is the case that could not be expressed
// before: a task naming "prod" and a task naming "test" reach different domain
// controllers, served by the same worker, because the directory is a record and not a
// property of the process that serves it.
func TestTwoDirectoriesAreBothHandedOverAndStaySeparate(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []struct{ id, name, ref, url, bind string }{
		{"1", "prod", "ad-prod", "ldaps://dc-prod.example.com:636", "cn=svc-prod,dc=example,dc=com"},
		{"2", "test", "ad-test", "ldaps://dc-test.example.com:636", "cn=svc-test,dc=example,dc=com"},
	} {
		if _, err := srv.vault.Set(c.ref, `{"bindDN":"`+c.bind+`","password":"pw-`+c.name+`"}`); err != nil {
			t.Fatalf("vault.Set(%s): %v", c.ref, err)
		}
		if err := srv.connectors.Save(connector{
			ID: c.id, Name: c.name, Kind: connectorKindAD, Endpoint: c.url,
			CredentialsRef: c.ref, Enabled: true, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("connectors.Save(%s): %v", c.name, err)
		}
	}

	env := envOf(t, srv.adWorkerEnv())
	names := strings.Split(env["ATLAS_AD_CONNECTORS"], ",")
	if len(names) != 2 {
		t.Fatalf("ATLAS_AD_CONNECTORS = %q, want both directories", env["ATLAS_AD_CONNECTORS"])
	}
	// Each keeps its own URL and its own service account — the whole point.
	if env["ATLAS_AD_PROD_URL"] == env["ATLAS_AD_TEST_URL"] {
		t.Error("both directories were rendered with the same URL")
	}
	if env["ATLAS_AD_PROD_PASSWORD"] != "pw-prod" || env["ATLAS_AD_TEST_PASSWORD"] != "pw-test" {
		t.Errorf("passwords = %q / %q, want each directory's own", env["ATLAS_AD_PROD_PASSWORD"], env["ATLAS_AD_TEST_PASSWORD"])
	}
}

// A record whose bundle does not resolve yet is left out entirely — including its name.
// A name in CONNECTORS with no URL behind it is the misconfiguration the worker refuses
// to start on, which would take down every other kind that worker serves.
func TestAnUnusableADDirectoryIsNotHandedOver(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("good", `{"bindDN":"cn=svc,dc=x","password":"pw"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if _, err := srv.vault.Set("half", `{"bindDN":"cn=svc,dc=x"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	for _, c := range []connector{
		{ID: "1", Name: "nosecret", Kind: connectorKindAD, Endpoint: "ldaps://a:636", CredentialsRef: "absent", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "halffilled", Kind: connectorKindAD, Endpoint: "ldaps://b:636", CredentialsRef: "half", Enabled: true, CreatedAt: 2},
		{ID: "3", Name: "nourl", Kind: connectorKindAD, CredentialsRef: "good", Enabled: true, CreatedAt: 3},
		{ID: "4", Name: "off", Kind: connectorKindAD, Endpoint: "ldaps://d:636", CredentialsRef: "good", Enabled: false, CreatedAt: 4},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}

	for _, line := range srv.adWorkerEnv() {
		if strings.HasPrefix(line, "ATLAS_AD_CONNECTORS") {
			t.Errorf("rendered %q; not one of these directories is usable", line)
		}
	}
}

// Two names that fold to one variable would give one directory the other's service
// account — the mail/entra/remedy collision, and the worst of them: the credential
// opens a forest.
func TestTwoADDirectoriesThatFoldToOneVariableDoNotShareACredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []struct{ id, name, ref string }{
		{"1", "haus forest", "a"}, {"2", "haus-forest", "b"},
	} {
		if _, err := srv.vault.Set(c.ref, `{"bindDN":"cn=`+c.ref+`,dc=x","password":"pw-`+c.ref+`"}`); err != nil {
			t.Fatalf("vault.Set: %v", err)
		}
		if err := srv.connectors.Save(connector{
			ID: c.id, Name: c.name, Kind: connectorKindAD, Endpoint: "ldaps://dc:636",
			CredentialsRef: c.ref, Enabled: true, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("Save(%s): %v", c.name, err)
		}
	}

	env := envOf(t, srv.adWorkerEnv())
	names := strings.Split(env["ATLAS_AD_CONNECTORS"], ",")
	if len(names) != 1 {
		t.Fatalf("ATLAS_AD_CONNECTORS = %q, want exactly one of the two colliding names", env["ATLAS_AD_CONNECTORS"])
	}
	want := map[string]string{"haus forest": "pw-a", "haus-forest": "pw-b"}[names[0]]
	if got := env["ATLAS_AD_HAUS_FOREST_PASSWORD"]; got != want {
		t.Errorf("password = %q, want %q — the surviving directory's own", got, want)
	}
}

// A bundle missing either half is refused. A bind DN with no password is an *anonymous*
// bind to Active Directory: it succeeds, and then the operation fails on permissions
// somewhere far from the cause — or worse, quietly reads what anonymous may read.
func TestAnADBundleNeedsBothHalves(t *testing.T) {
	for _, raw := range []string{
		"", "   ", "not json",
		`{"bindDN":"cn=svc,dc=x"}`,
		`{"password":"pw"}`,
		`{"bindDN":"","password":"pw"}`,
		`{"bindDN":"cn=svc,dc=x","password":"  "}`,
	} {
		if _, ok := adBundleParse(raw); ok {
			t.Errorf("adBundleParse(%q) parsed, want it refused", raw)
		}
	}
	got, ok := adBundleParse(`{"bindDN":"cn=svc,dc=x","password":"pw"}`)
	if !ok || got.BindDN != "cn=svc,dc=x" || got.Password != "pw" {
		t.Errorf("adBundleParse of a complete bundle = %+v, %v", got, ok)
	}
}

// What the create form refuses. The scheme check earns its place: Active Directory
// refuses to set a password over an unencrypted channel, so an ldap:// directory works
// for every operation *except* the one a joiner needs most — and without this it would
// only say so on the first real run, against a real account.
func TestCreatingAnADConnectorNeedsAnLDAPURLAndABundle(t *testing.T) {
	k, ok := lookupManagedConnectorKind(connectorKindAD)
	if !ok {
		t.Fatal("ad is not a managed Worker Type")
	}
	for _, tc := range []struct{ name, endpoint, ref, want string }{
		{"no endpoint", "", "ad-bind", "endpoint"},
		{"an https URL, which is not a directory", "https://dc.example.com", "ad-bind", "LDAP URL"},
		{"a bare host", "dc.example.com:636", "ad-bind", "LDAP URL"},
		{"ldaps but no credentials", "ldaps://dc.example.com:636", "", "credentialsRef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := k.validateCreate(&createConnectorParams{
				Name: "forest", Endpoint: tc.endpoint, CredentialsRef: tc.ref,
			})
			if msg == "" {
				t.Fatal("an incomplete active directory worker was accepted")
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("message = %q, want it to mention %q", msg, tc.want)
			}
		})
	}

	// Both schemes are directories; StartTLS is what makes the plain one usable, and
	// that is the operator's call rather than the form's.
	for _, url := range []string{"ldaps://dc.example.com:636", "ldap://dc.example.com:389"} {
		p := &createConnectorParams{Name: "forest", Endpoint: url, CredentialsRef: "ad-bind", Provider: "smtp", Sender: "bot@x"}
		if msg := k.validateCreate(p); msg != "" {
			t.Errorf("a complete active directory worker on %s was refused: %s", url, msg)
		}
		// The mail-only fields are cleared rather than stored, so a record carried over
		// from another kind cannot leave a provider or a sender behind on a directory.
		if p.Provider != "" || p.Sender != "" {
			t.Errorf("provider/sender = %q/%q, want both cleared", p.Provider, p.Sender)
		}
	}
}

// A directory name with no letter or digit in it folds to no variable name at all.
// Rendered anyway it would become ATLAS_AD__URL — a variable no operator could set, and
// one the next such name would collide with.
func TestAnADDirectoryNameThatFoldsToNothingIsLeftOut(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("good", `{"bindDN":"cn=svc,dc=x","password":"pw"}`); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "---", Kind: connectorKindAD, Endpoint: "ldaps://dc:636",
		CredentialsRef: "good", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}

	for _, line := range srv.adWorkerEnv() {
		if strings.HasPrefix(line, "ATLAS_AD_") && !strings.HasPrefix(line, "ATLAS_AD_MOCK") {
			t.Errorf("rendered %q for a name with no variable name in it", line)
		}
	}
}
