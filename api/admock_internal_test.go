package api

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

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

// Switched on, the worker is told so — and told where to start from when a seed is
// named. Both under the names the worker itself reads (ADR-0181).
func TestTheStoredADMockSettingReachesTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	var saveErr error
	srv.do(func() { saveErr = srv.settings.saveADMock(adMockSetting{Enabled: true, Seed: "/srv/forest.ldif"}) })
	if saveErr != nil {
		t.Fatalf("saveADMock: %v", saveErr)
	}

	env := envOf(t, srv.adWorkerEnv())
	if env["ATLAS_AD_MOCK"] != "1" {
		t.Errorf("ATLAS_AD_MOCK = %q, want the switch on", env["ATLAS_AD_MOCK"])
	}
	if env["ATLAS_AD_MOCK_SEED"] != "/srv/forest.ldif" {
		t.Errorf("ATLAS_AD_MOCK_SEED = %q, want the stored seed", env["ATLAS_AD_MOCK_SEED"])
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
		`{"enabled":true,"seed":"/srv/forest.ldif"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", code, body)
	}
	code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/settings/ad-mock", "", "")
	if !strings.Contains(string(body), `"enabled":true`) || !strings.Contains(string(body), "forest.ldif") {
		t.Errorf("GET after PUT returned %s (status %d), want what was stored", body, code)
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
