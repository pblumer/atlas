package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/ldif"
)

// adSeedLDIF is one entry, which is all any of these tests needs: what is under test
// is where the seed lives and how it reaches a worker, never what a directory does
// with it.
const adSeedLDIF = "dn: cn=Ada,dc=example,dc=com\ncn: Ada\n"

// The Console's Active-Directory mockup switch.
//
// ADR-0181 put the switch on the worker rather than in the model, and that half is
// unchanged: a model still says nothing about being mocked. What changes here is
// *where the operator reaches it*. An environment variable is set once, at start, by
// whoever runs the process — which is the wrong ceremony for a thing you flip while
// trying a process out. The setting lives in the org-wide store, is rendered into the
// supervised AD worker's environment, and the refresh that already exists brings the
// worker back holding it.

// With nothing stored, nothing is rendered: a server whose operator set ATLAS_AD_MOCK
// by hand keeps deciding for itself, which is what makes this backwards compatible.
func TestNoStoredADMockSettingRendersNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, line := range srv.adWorkerEnv() {
		if strings.HasPrefix(line, "ATLAS_AD_MOCK") {
			t.Errorf("rendered %q with nothing stored; the inherited environment must decide", line)
		}
	}
}

// Switched on, the worker is told so — and pointed at a seed file *Atlas* wrote, under
// the names the worker itself reads (ADR-0181,
// ADR-draft-atlas-manages-the-ad-mock-seed). The path is Atlas's to know; the operator
// never types one.
func TestTheStoredADMockSettingReachesTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	var saveErr error
	srv.do(func() {
		saveErr = srv.settings.saveADMock(adMockSetting{
			Enabled: true, Seed: adSeedLDIF, SeedFormat: ldif.FormatLDIF, SeedEntries: 1,
		})
	})
	if saveErr != nil {
		t.Fatalf("saveADMock: %v", saveErr)
	}

	env := envOf(t, srv.adWorkerEnv())
	if env["ATLAS_AD_MOCK"] != "1" {
		t.Errorf("ATLAS_AD_MOCK = %q, want the switch on", env["ATLAS_AD_MOCK"])
	}
	// The file is there, and holds what was stored: the worker reads a path, so the
	// handover is only real if something is at the end of it.
	seed := env["ATLAS_AD_MOCK_SEED"]
	if seed == "" {
		t.Fatalf("environment = %v, want a seed file to start from", env)
	}
	data, err := os.ReadFile(seed)
	if err != nil {
		t.Fatalf("the worker was pointed at a file that is not there: %v", err)
	}
	if string(data) != adSeedLDIF {
		t.Errorf("seed file holds %q, want the stored seed", data)
	}
}

// Replacing the seed has to actually reach a running worker. The supervisor restarts a
// child only when its rendered environment differs, so a seed file with a fixed name
// would render the identical variable for new content and leave the worker serving the
// directory it started with. The path carries a digest of the content for exactly this.
func TestReplacingTheADMockSeedChangesWhatTheWorkerIsHanded(t *testing.T) {
	srv, _ := newValidateServer(t)
	save := func(seed string) string {
		t.Helper()
		var err error
		srv.do(func() {
			err = srv.settings.saveADMock(adMockSetting{Enabled: true, Seed: seed, SeedFormat: ldif.FormatLDIF})
		})
		if err != nil {
			t.Fatalf("saveADMock: %v", err)
		}
		return envOf(t, srv.adWorkerEnv())["ATLAS_AD_MOCK_SEED"]
	}

	first := save(adSeedLDIF)
	second := save("dn: cn=Grace,dc=example,dc=com\ncn: Grace\n")
	if first == "" || second == "" {
		t.Fatalf("seed variable = %q then %q, want a path both times", first, second)
	}
	if first == second {
		t.Error("the same variable was rendered for different seeds; a running worker would never be restarted")
	}
	// And the one it replaced is gone rather than left behind for an older environment
	// to find.
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("the replaced seed file is still there (%v); a stale one is a directory nobody asked for", err)
	}
	// Storing the same seed again renders the same path: the restart happens when the
	// content changes, not on every save.
	if again := save("dn: cn=Grace,dc=example,dc=com\ncn: Grace\n"); again != second {
		t.Errorf("re-saving an unchanged seed rendered %q, want the unchanged %q", again, second)
	}
}

// Clearing the seed takes the file away with it. A worker restarted afterwards must
// find nothing rather than the directory an operator just removed.
func TestClearingTheADMockSeedRemovesItsFile(t *testing.T) {
	srv, _ := newValidateServer(t)
	var path string
	srv.do(func() {
		_ = srv.settings.saveADMock(adMockSetting{Enabled: true, Seed: adSeedLDIF, SeedFormat: ldif.FormatLDIF})
	})
	path = envOf(t, srv.adWorkerEnv())["ATLAS_AD_MOCK_SEED"]
	if path == "" {
		t.Fatal("no seed was rendered to begin with")
	}

	srv.do(func() { _ = srv.settings.saveADMock(adMockSetting{Enabled: true}) })

	if got := envOf(t, srv.adWorkerEnv())["ATLAS_AD_MOCK_SEED"]; got != "" {
		t.Errorf("ATLAS_AD_MOCK_SEED = %q after the seed was cleared, want nothing", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the cleared seed file is still on disk (%v)", err)
	}
}

// Switched off, the worker is told *that* too, rather than being left to whatever it
// inherits. A stored decision is a decision: an operator who turns the mockup off in
// the Console and still sees simulated writes has been lied to by the switch.
func TestTheStoredADMockSettingCanTurnAnInheritedOneOff(t *testing.T) {
	srv, _ := newValidateServer(t)
	t.Setenv("ATLAS_AD_MOCK", "1")
	var saveErr error
	srv.do(func() { saveErr = srv.settings.saveADMock(adMockSetting{Enabled: false}) })
	if saveErr != nil {
		t.Fatalf("saveADMock: %v", saveErr)
	}

	env := envOf(t, srv.adWorkerEnv())
	if env["ATLAS_AD_MOCK"] != "0" {
		t.Errorf("ATLAS_AD_MOCK = %q, want the stored off to override the inherited on", env["ATLAS_AD_MOCK"])
	}
	if _, seeded := env["ATLAS_AD_MOCK_SEED"]; seeded {
		t.Error("a seed was rendered for a switched-off mockup")
	}
}

// A seed with nothing but whitespace is no seed: rendering it would hand the worker a
// path it cannot read and fail its start, from a field somebody merely tabbed through.
func TestAnEmptyADMockSeedIsNotRendered(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() { _ = srv.settings.saveADMock(adMockSetting{Enabled: true, Seed: "   "}) })

	env := envOf(t, srv.adWorkerEnv())
	if env["ATLAS_AD_MOCK"] != "1" {
		t.Errorf("ATLAS_AD_MOCK = %q, want the switch on", env["ATLAS_AD_MOCK"])
	}
	if _, seeded := env["ATLAS_AD_MOCK_SEED"]; seeded {
		t.Error("a blank seed path was rendered")
	}
}

// The bind passwords are still handed over while the mockup is on. They are the same
// references the models name, and a mockup run that could not bind would prove nothing
// about a model that has to.
func TestTheMockupSwitchDoesNotDisplaceTheBindSecrets(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("AD_BIND", "s3cr3t"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deployADModel(t, srv, adModel("joiner", `bindSecret="AD_BIND"`))
	srv.do(func() { _ = srv.settings.saveADMock(adMockSetting{Enabled: true}) })

	env := envOf(t, srv.adWorkerEnv())
	if env["ATLAS_AD_MOCK"] != "1" || env["ATLAS_CONNECTOR_AD_BIND_TOKEN"] != "s3cr3t" {
		t.Errorf("environment = %v, want both the switch and the bind password", env)
	}
}

// The switch is org-wide operator configuration, so reading it is open to anyone
// signed in and writing it is not.
func TestTheADMockSettingIsReadableAndAdminGated(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/settings/ad-mock", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET: status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Errorf("GET returned %s, want the switch reported off", body)
	}

	code, body = serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock",
		`{"enabled":true,"seed":"dn: cn=Ada,dc=example,dc=com\ncn: Ada\n","seedName":"forest.ldif"}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", code, body)
	}
	code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/settings/ad-mock", "", "")
	if !strings.Contains(string(body), `"enabled":true`) || !strings.Contains(string(body), "forest.ldif") {
		t.Errorf("GET after PUT returned %s (status %d), want what was stored", body, code)
	}
	// The count is what tells an operator the upload landed. A silent "saved" over a
	// file that parsed to nothing is the outcome this reports its way out of.
	if !strings.Contains(string(body), `"seedEntries":1`) {
		t.Errorf("GET after PUT returned %s, want the entry count the seed parsed to", body)
	}
}

// A seed that does not parse is refused where the person who can fix it is looking,
// rather than stored and discovered later as a mock that quietly started empty.
func TestAnUnparseableADMockSeedIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, tc := range []struct{ name, seed string }{
		{"a path, which is what this field used to take", `/srv/forest.ldif`},
		{"LDIF with no attribute separator", `dn cn=Ada`},
		{"XML that is not DSML", `<nonsense/>`},
		{"LDIF holding only a comment", "# only a comment\n"},
		{"a DSML document with no entries in it", "<dsml><directory-entries></directory-entries></dsml>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"enabled": true, "seed": tc.seed})
			code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock", string(body), "application/json")
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", code, resp)
			}
		})
	}
	// And nothing was stored on the way past: a refused save leaves the previous state.
	var stored bool
	srv.do(func() { _, stored, _ = srv.settings.getADMock() })
	if stored {
		t.Error("a refused seed still wrote a record")
	}
}

// DSML and LDIF are told apart by looking at the seed, because there is no longer a
// file name to read it off — an operator pastes text as often as they pick a file.
func TestTheADMockSeedFormatIsReadOffTheContent(t *testing.T) {
	for _, tc := range []struct{ seed, want string }{
		{"dn: cn=Ada,dc=example,dc=com\ncn: Ada\n", ldif.FormatLDIF},
		{"<dsml><directory-entries/></dsml>", ldif.FormatDSML},
		{"\n\n   <dsml/>", ldif.FormatDSML},
		{"", ldif.FormatLDIF}, // the ambiguous case defaults to what a directory exports
	} {
		if got := adSeedFormat(tc.seed); got != tc.want {
			t.Errorf("adSeedFormat(%q) = %q, want %q", tc.seed, got, tc.want)
		}
	}
}

// A body that is not the switch is refused rather than stored as an off switch.
func TestTheADMockSettingRefusesABodyItCannotRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code, _ := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock", "kaputt", "application/json"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body that is not JSON", code)
	}
}

// The promise the switch makes: flip it, and the worker that serves directory tasks
// comes back mocked — without anybody restarting Atlas. That is the whole reason it
// is here rather than on the command line, so it is the property worth a test.
func TestFlippingTheSwitchBringsTheADWorkerBackMocked(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	srv, _ := newValidateServer(t)

	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond
	spec := SuperviseSpec{ID: "ad", Connectors: []string{"ad"}}
	sup.add(spec, []string{"-c", "sleep 30"}, srv.superviseEnv(spec))
	sup.start()
	srv.supervisor = sup
	t.Cleanup(func() { close(quit); sup.wait() })

	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running"
	})
	first := sup.list()[0].Starts

	if code, body := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock",
		`{"enabled":true}`, "application/json"); code != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", code, body)
	}
	waitFor(t, "the ad worker to come back in mockup mode", func() bool {
		return sup.list()[0].Starts > first
	})
	if got := envOf(t, srv.superviseEnv(spec)())["ATLAS_AD_MOCK"]; got != "1" {
		t.Errorf("the restarted worker's ATLAS_AD_MOCK = %q, want it on", got)
	}

	// And back off again, because a switch that only goes one way is a trap.
	settled := sup.list()[0].Starts
	if code, _ := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock",
		`{"enabled":false}`, "application/json"); code != http.StatusOK {
		t.Fatalf("PUT off: status=%d", code)
	}
	waitFor(t, "the ad worker to come back serving the real directory", func() bool {
		return sup.list()[0].Starts > settled
	})
	if got := envOf(t, srv.superviseEnv(spec)())["ATLAS_AD_MOCK"]; got != "0" {
		t.Errorf("the restarted worker's ATLAS_AD_MOCK = %q, want it off", got)
	}
}

// A stored record that is not the switch — a file edited by hand, a half-written
// save — must be reported, not read as "off". Silently serving a real directory
// because a JSON file is corrupt is the one outcome this switch exists around.
func TestAnUnreadableADMockSettingIsReported(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := os.WriteFile(srv.settings.adFile, []byte("kaputt"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var (
		err    error
		stored bool
	)
	srv.do(func() { _, stored, err = srv.settings.getADMock() })
	if err == nil {
		t.Error("a corrupt record was read without complaint")
	}
	if stored {
		t.Error("a corrupt record was reported as a stored decision")
	}

	// The HTTP read says so rather than answering "off", and the worker is handed
	// nothing — so it keeps whatever it was started with instead of being switched
	// by a broken file.
	if code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/settings/ad-mock", "", ""); code != http.StatusInternalServerError {
		t.Errorf("GET status = %d, want the read failure reported", code)
	}
	for _, line := range srv.adWorkerEnv() {
		if strings.HasPrefix(line, "ATLAS_AD_MOCK") {
			t.Errorf("rendered %q from a record that cannot be read", line)
		}
	}
}

// Writing the switch is admin-gated: it decides whether this instance writes to a
// real directory, which is not a thing every signed-in user may flip.
func TestWritingTheADMockSettingNeedsAnAdmin(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	code, body := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock", `{"enabled":true}`, "application/json")
	if code == http.StatusOK {
		t.Errorf("an unauthenticated caller flipped the switch: status=%d body=%s", code, body)
	}
}

// A settings directory that cannot be written or read — the file replaced by a
// directory, a permission an operator changed — must fail loudly at both ends. A
// switch whose save silently did nothing would leave the Console showing a state the
// worker is not in.
func TestAnUnwritableADMockSettingFailsBothWays(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := os.Mkdir(srv.settings.adFile, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if code, body := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock",
		`{"enabled":true}`, "application/json"); code != http.StatusInternalServerError {
		t.Errorf("PUT status = %d body=%s, want the save failure reported", code, body)
	}
	if code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/settings/ad-mock", "", ""); code != http.StatusInternalServerError {
		t.Errorf("GET status = %d, want the read failure reported", code)
	}
}

// The switch's *state* is readable by anyone signed in — it answers "did that account
// really get created?", which is not a question to hide from the person watching a run.
// The starting entries are not: they are invented directory data, but shaped like a
// staff list, and there is no reason for every signed-in user to read one.
func TestTheADMockSeedContentIsAdminOnlyWhileTheSwitchIsNot(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	srv.do(func() {
		_ = srv.settings.saveADMock(adMockSetting{
			Enabled: true, Seed: adSeedLDIF, SeedName: "forest.ldif",
			SeedFormat: ldif.FormatLDIF, SeedEntries: 1,
		})
	})

	// The handler is called directly with a principal in context: what is under test is
	// the field it keeps back, not the middleware that established who is asking.
	get := func(roles ...string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/ad-mock", nil)
		r = r.WithContext(httpapi.WithPrincipal(r.Context(),
			&httpapi.Principal{UserID: "u1", Username: "ben", Roles: roles}))
		rec := httptest.NewRecorder()
		srv.handleGetADMock(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: status=%d body=%s — reading the switch must stay open", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}

	signedIn := get("user")
	if strings.Contains(signedIn, "cn=Ada") {
		t.Errorf("the seed's entries were returned to a caller who is not an admin: %s", signedIn)
	}
	// But enough of it to say there is one, and what it is called: the Console has to
	// show "a seed is loaded" without showing the seed.
	for _, want := range []string{`"enabled":true`, `"hasSeed":true`, "forest.ldif"} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("the response was %s, want it to still carry %s", signedIn, want)
		}
	}

	// An admin gets the entries, or the Console could never show them what is loaded.
	if admin := get(RoleAdmin); !strings.Contains(admin, "cn=Ada") {
		t.Errorf("an admin was not given the seed: %s", admin)
	}
}

// A record written before the format was stored still resolves to a file the worker can
// read: LDIF is the default, because it is what a directory exports and what the field
// took before DSML was accepted.
func TestASeedWithNoStoredFormatIsTreatedAsLDIF(t *testing.T) {
	srv, _ := newValidateServer(t)
	var path string
	srv.do(func() { path = srv.settings.adSeedPath(adMockSetting{Enabled: true, Seed: adSeedLDIF}) })
	if !strings.HasSuffix(path, "."+ldif.FormatLDIF) {
		t.Errorf("adSeedPath = %q, want an %s file", path, ldif.FormatLDIF)
	}
	// And no seed at all resolves to no file, rather than to a name with nothing behind
	// it that adWorkerEnv would then hand to a worker.
	srv.do(func() { path = srv.settings.adSeedPath(adMockSetting{Enabled: true}) })
	if path != "" {
		t.Errorf("adSeedPath = %q for a setting with no seed, want nothing", path)
	}
}

// A seed larger than a colour. The switch's body used to share the theme's 4 KiB
// limit, read through a LimitReader — which truncates rather than refuses, so a
// perfectly good LDIF export came back as "invalid JSON body" the moment it outgrew
// four kilobytes. A realistic seed has to fit, and one that genuinely does not has to
// say so.
func TestASeedLargerThanASettingIsStoredRatherThanTruncated(t *testing.T) {
	srv, _ := newValidateServer(t)

	// ~40 KiB of entries: ten times the old limit, and unremarkable for a directory
	// export of a few hundred accounts.
	var big strings.Builder
	for i := 0; big.Len() < 40<<10; i++ {
		fmt.Fprintf(&big, "dn: cn=Person%d,ou=Mitarbeitende,dc=example,dc=com\ncn: Person%d\nsAMAccountName: person%d\n\n", i, i, i)
	}
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": big.String(), "seedName": "export.ldif"})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock", string(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%s), want a 40 KiB seed accepted", code, resp)
	}
	if !strings.Contains(string(resp), `"seedEntries":`) {
		t.Errorf("body = %s, want the entry count of what was stored", resp)
	}

	// And what the worker is handed is the whole thing, not a prefix of it.
	seed := envOf(t, srv.adWorkerEnv())["ATLAS_AD_MOCK_SEED"]
	stored, err := os.ReadFile(seed)
	if err != nil {
		t.Fatalf("read the stored seed: %v", err)
	}
	if len(stored) != len(strings.TrimSpace(big.String())) {
		t.Errorf("stored %d bytes of a %d byte seed", len(stored), len(big.String()))
	}
}

// One that is genuinely too large is refused, and says that rather than blaming the
// JSON. A truncating reader is what made this the wrong error before.
func TestAnOversizedSeedIsRefusedAsTooLargeNotAsBadJSON(t *testing.T) {
	srv, _ := newValidateServer(t)
	body, _ := json.Marshal(map[string]any{"enabled": true, "seed": strings.Repeat("x", maxADMockBytes+1)})
	code, resp := serveInternal(t, srv, http.MethodPut, "/api/v1/settings/ad-mock", string(body), "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400", code, resp)
	}
	if !strings.Contains(string(resp), "too large") {
		t.Errorf("body = %s, want it to name the size rather than the JSON", resp)
	}
}
