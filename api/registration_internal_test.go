package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsStoreRegistrationRoundTrip covers the durable registration record
// directly: a fresh store reports "no record" (so the caller falls back to the
// default), a non-empty save round-trips, and an empty save is a real stored value
// meaning "disabled" — distinct from the no-record state.
func TestSettingsStoreRegistrationRoundTrip(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}

	// Unset → no record.
	if got, ok, err := st.getRegistration(); err != nil || ok || got.ProcessID != "" {
		t.Fatalf("getRegistration (unset) = %+v, ok=%v, err=%v; want zero, false, nil", got, ok, err)
	}

	if err := st.saveRegistration(registrationSetting{ProcessID: "proc_x"}); err != nil {
		t.Fatalf("saveRegistration: %v", err)
	}
	if got, ok, err := st.getRegistration(); err != nil || !ok || got.ProcessID != "proc_x" {
		t.Fatalf("getRegistration = %+v, ok=%v, err=%v; want proc_x, true, nil", got, ok, err)
	}

	// Empty ProcessID is a stored "disabled" value: a record exists, but it names
	// no process.
	if err := st.saveRegistration(registrationSetting{ProcessID: ""}); err != nil {
		t.Fatalf("saveRegistration (disable): %v", err)
	}
	if got, ok, err := st.getRegistration(); err != nil || !ok || got.ProcessID != "" {
		t.Fatalf("getRegistration (disabled) = %+v, ok=%v, err=%v; want empty, true, nil", got, ok, err)
	}
}

// TestSettingsStoreRegistrationErrors covers the registration read's I/O-failure
// branches: a corrupt file is a decode error (not silently "no record"), and a
// directory in the file's place is a read error.
func TestSettingsStoreRegistrationErrors(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}

	if err := os.WriteFile(st.regFile, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, _, err := st.getRegistration(); err == nil {
		t.Fatal("getRegistration on a corrupt file: want error, got nil")
	}

	if err := os.Remove(st.regFile); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Mkdir(st.regFile, 0o755); err != nil {
		t.Fatalf("mkdir in file's place: %v", err)
	}
	if _, _, err := st.getRegistration(); err == nil {
		t.Fatal("getRegistration on a directory: want error, got nil")
	}
	// A directory in the file's place also fails the atomic save.
	if err := st.saveRegistration(registrationSetting{ProcessID: "x"}); err == nil {
		t.Fatal("saveRegistration over a directory: want error, got nil")
	}
}

// TestRegistrationReadIsPublic: GET /api/v1/settings/registration is not gated even
// when auth is enforced, so the login screen can read it before sign-in.
func TestRegistrationReadIsPublic(t *testing.T) {
	if requiresAuth("/api/v1/settings/registration") {
		t.Fatal("GET registration must be public (read before login)")
	}
}

// TestRegistrationWritesRequireAdmin: with auth enabled, PUT and DELETE demand the
// admin role. The gate runs before any store access, so a bare Server suffices.
func TestRegistrationWritesRequireAdmin(t *testing.T) {
	s := &Server{authEnabled: true}
	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		meth string
	}{
		{"set", s.handleSetRegistration, http.MethodPut},
		{"delete", s.handleDeleteRegistration, http.MethodDelete},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.meth, "/api/v1/settings/registration", nil)
		tc.fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s registration without an admin principal: status=%d, want 403", tc.name, rec.Code)
		}
	}
}

// TestSetRegistrationRejectsBadBodies covers handleSetRegistration's
// request-validation branches, which return before any store access (so a bare
// Server with auth off suffices): an unreadable body and malformed JSON are each a
// 400.
func TestSetRegistrationRejectsBadBodies(t *testing.T) {
	s := &Server{} // auth off → admin gate passes; these fail before s.do
	for _, tc := range []struct {
		name string
		body interface{ Read([]byte) (int, error) }
	}{
		{"unreadable body", errReader{}},
		{"malformed JSON", strings.NewReader("{ not json")},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/registration", tc.body)
		s.handleSetRegistration(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", tc.name, rec.Code)
		}
	}
}

// TestEffectiveRegistrationPID covers the three resolutions: no record → the
// built-in default, a stored process id → that id, a stored empty id → "" (an
// operator switched registration off).
func TestEffectiveRegistrationPID(t *testing.T) {
	srv := newServerForErrors(t)

	if pid, err := srv.effectiveRegistrationPID(); err != nil || pid != defaultRegistrationProcessID {
		t.Fatalf("effective (unset) = %q, %v; want default %q", pid, err, defaultRegistrationProcessID)
	}
	if err := srv.settings.saveRegistration(registrationSetting{ProcessID: "proc_custom"}); err != nil {
		t.Fatalf("save custom: %v", err)
	}
	if pid, err := srv.effectiveRegistrationPID(); err != nil || pid != "proc_custom" {
		t.Fatalf("effective (custom) = %q, %v; want proc_custom", pid, err)
	}
	if err := srv.settings.saveRegistration(registrationSetting{ProcessID: ""}); err != nil {
		t.Fatalf("save disabled: %v", err)
	}
	if pid, err := srv.effectiveRegistrationPID(); err != nil || pid != "" {
		t.Fatalf("effective (disabled) = %q, %v; want empty", pid, err)
	}
}

// TestRegistrationLinkForNotPublishable covers registrationLinkForLocked's two
// no-op returns: an empty process id, and a process id that is not deployed as a
// publishable public form. Both return ("", nil) so registration stays hidden.
func TestRegistrationLinkForNotPublishable(t *testing.T) {
	srv := newServerForErrors(t)
	if url, err := srv.registrationLinkForLocked("", 1); err != nil || url != "" {
		t.Fatalf("empty pid = %q, %v; want empty, nil", url, err)
	}
	if url, err := srv.registrationLinkForLocked("nope-not-deployed", 1); err != nil || url != "" {
		t.Fatalf("undeployed pid = %q, %v; want empty, nil", url, err)
	}
}

// TestHandleGetRegistrationDisabled: with a stored empty process id, the public GET
// reports registration off without minting anything.
func TestHandleGetRegistrationDisabled(t *testing.T) {
	srv := newServerForErrors(t)
	if err := srv.settings.saveRegistration(registrationSetting{ProcessID: ""}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.handleGetRegistration(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings/registration", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET registration: status=%d", rec.Code)
	}
	var cfg registrationConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("disabled registration reported enabled: %+v", cfg)
	}
}

// TestHandleGetRegistrationDefaultNotDeployed: without the system processes the
// default registration process is not deployed, so GET reports off (the link is
// not publishable) rather than erroring.
func TestHandleGetRegistrationDefaultNotDeployed(t *testing.T) {
	srv := newServerForErrors(t) // no system processes
	rec := httptest.NewRecorder()
	srv.handleGetRegistration(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings/registration", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET registration: status=%d", rec.Code)
	}
	var cfg registrationConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("registration reported enabled with no deployed process: %+v", cfg)
	}
}

// TestHandleSetRegistrationNotPublishable: configuring a process that is not a
// publishable public form is a 400, so an operator gets immediate feedback.
func TestHandleSetRegistrationNotPublishable(t *testing.T) {
	srv := newServerForErrors(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/registration",
		strings.NewReader(`{"processId":"does-not-exist"}`))
	srv.handleSetRegistration(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unpublishable process: status=%d, want 400", rec.Code)
	}
}

// TestHandleSetRegistrationDisable: an empty process id switches registration off
// through the write path (no link minted), returning enabled:false.
func TestHandleSetRegistrationDisable(t *testing.T) {
	srv := newServerForErrors(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/registration",
		strings.NewReader(`{"processId":""}`))
	srv.handleSetRegistration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT disable: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cfg registrationConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("disable returned enabled: %+v", cfg)
	}
	// And it persists as an explicit "off" record.
	if _, ok, _ := srv.settings.getRegistration(); !ok {
		t.Fatal("disable did not persist a record")
	}
}

// TestRegistrationWriteStoreErrors covers the 500 branches of the get/set/delete
// handlers by making the registration file unwritable/unreadable (a directory in
// its place).
func TestRegistrationWriteStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	// Replace the settings store's registration file with a directory: reads and
	// atomic writes both fail.
	if err := os.Mkdir(srv.settings.regFile, 0o755); err != nil {
		t.Fatalf("mkdir regFile: %v", err)
	}

	// GET → 500 (read fails).
	rec := httptest.NewRecorder()
	srv.handleGetRegistration(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings/registration", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET with broken store: status=%d, want 500", rec.Code)
	}

	// DELETE → 500 (save fails).
	rec = httptest.NewRecorder()
	srv.handleDeleteRegistration(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/settings/registration", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE with broken store: status=%d, want 500", rec.Code)
	}

	// PUT (disable) → 500 (save fails; empty pid skips the link mint and hits save).
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/registration", strings.NewReader(`{"processId":""}`))
	srv.handleSetRegistration(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT with broken store: status=%d, want 500", rec.Code)
	}
}

// TestRegistrationLinkReuseAndStoreErrors covers registrationLinkForLocked's reuse
// branch (an existing link is returned, not re-minted) and its store-error branch,
// plus the GET/PUT handlers' link-error 500s — all with the intake process actually
// deployed, which the pure error tests above cannot reach.
func TestRegistrationLinkReuseAndStoreErrors(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	const pid = defaultRegistrationProcessID

	// Bootstrap already minted a link; two calls reuse the same URL (idempotent).
	var url1, url2 string
	var err1, err2 error
	srv.do(func() {
		url1, err1 = srv.registrationLinkForLocked(pid, 1)
		url2, err2 = srv.registrationLinkForLocked(pid, 2)
	})
	if err1 != nil || url1 == "" {
		t.Fatalf("first link = %q, %v; want a URL", url1, err1)
	}
	if err2 != nil || url2 != url1 {
		t.Fatalf("reuse link = %q, %v; want same %q", url2, err2, url1)
	}

	// A broken public-link store fails loadAll inside the mint path.
	srv.publicLinks = &publicLinkStore{dir: filepath.Join(t.TempDir(), "nope", "deeper")}
	var errBroken error
	srv.do(func() { _, errBroken = srv.registrationLinkForLocked(pid, 3) })
	if errBroken == nil {
		t.Fatal("registrationLinkForLocked with a broken store: want error")
	}

	// The GET and PUT handlers surface that as 500 (the process is deployed, so the
	// link mint is reached and fails).
	rec := httptest.NewRecorder()
	srv.handleGetRegistration(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings/registration", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET with broken link store: status=%d, want 500", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/registration",
		strings.NewReader(`{"processId":"`+pid+`"}`))
	srv.handleSetRegistration(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT with broken link store: status=%d, want 500", rec.Code)
	}
}

// TestEnsureRegistrationLinkSettingsError covers ensureRegistrationLink's
// settings-error branch: an unreadable registration record makes the effective-pid
// read fail, which the bootstrap surfaces rather than swallowing.
func TestEnsureRegistrationLinkSettingsError(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	if err := os.Mkdir(srv.settings.regFile, 0o755); err != nil {
		t.Fatalf("mkdir regFile: %v", err)
	}
	if err := srv.ensureRegistrationLink(1); err == nil {
		t.Fatal("ensureRegistrationLink with a broken settings store: want error")
	}
}
