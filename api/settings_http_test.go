package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// readAllClose drains and closes an HTTP response body, returning its bytes.
func readAllClose(res *http.Response) ([]byte, error) {
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

// pngMagicBytes is the 8-byte PNG signature, duplicated here (the server's copy is
// unexported) so tests can build a body the server accepts as a PNG.
var pngMagicBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func samplePNG() []byte { return append(append([]byte{}, pngMagicBytes...), 0x00, 0x01, 0x02) }

const sampleSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24"></svg>`

// TestThemeEndpointRoundTrip drives the org-wide theme over HTTP end to end: the
// default is empty, a PUT persists and is echoed back by GET, a bad colour is
// rejected, and DELETE resets to the default. Single-user mode (auth off), so the
// admin gate passes and this exercises the store wiring.
func TestThemeEndpointRoundTrip(t *testing.T) {
	ts := newTestServer(t)

	readAccent := func() string {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/theme", "", "")
		if code != http.StatusOK {
			t.Fatalf("GET theme: status=%d body=%s", code, body)
		}
		var r struct {
			Accent string `json:"accent"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("GET theme: decode %s: %v", body, err)
		}
		return r.Accent
	}

	// Default: no override.
	if a := readAccent(); a != "" {
		t.Fatalf("default accent = %q; want empty", a)
	}

	// Set a brand colour.
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"#7c3aed"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT theme: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "#7c3aed" {
		t.Fatalf("accent after PUT = %q; want #7c3aed", a)
	}

	// A 3-digit shorthand is normalised to #rrggbb on the way in.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"#abc"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("PUT shorthand: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "#aabbcc" {
		t.Fatalf("accent after shorthand PUT = %q; want #aabbcc", a)
	}

	// Garbage is rejected and does not change the stored value.
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"not-a-colour"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("PUT invalid: status=%d; want 400", code)
	}
	if a := readAccent(); a != "#aabbcc" {
		t.Fatalf("accent after rejected PUT = %q; want unchanged #aabbcc", a)
	}

	// Reset to default.
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/theme", "", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE theme: status=%d body=%s", code, body)
	}
	if a := readAccent(); a != "" {
		t.Fatalf("accent after DELETE = %q; want empty", a)
	}
}

// TestLogoEndpointRoundTrip drives the org-wide brand logo over HTTP end to end:
// none by default (404), a PNG upload is stored and served back verbatim with the
// hardening headers, switching to an SVG replaces it, and DELETE restores the
// default. Single-user mode (auth off), so the admin gate passes.
func TestLogoEndpointRoundTrip(t *testing.T) {
	ts := newTestServer(t)

	// Default: no logo.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/settings/logo", "", ""); code != http.StatusNotFound {
		t.Fatalf("GET logo default: status=%d; want 404", code)
	}

	// Upload a PNG.
	png := samplePNG()
	if code, body := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", png, "image/png"); code != http.StatusNoContent {
		t.Fatalf("PUT png: status=%d body=%s", code, body)
	}

	// GET returns the exact bytes, the stored content type, and the hardening
	// headers that keep an uploaded SVG from executing script.
	res, err := http.Get(ts.URL + "/api/v1/settings/logo")
	if err != nil {
		t.Fatalf("GET logo: %v", err)
	}
	got, _ := readAllClose(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET png: status=%d", res.StatusCode)
	}
	if !bytes.Equal(got, png) {
		t.Fatalf("GET png body = %v; want %v", got, png)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type=%q; want image/png", ct)
	}
	if ns := res.Header.Get("X-Content-Type-Options"); ns != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q; want nosniff", ns)
	}
	if csp := res.Header.Get("Content-Security-Policy"); csp == "" {
		t.Fatalf("Content-Security-Policy missing on logo response")
	}

	// Switching to an SVG replaces the PNG (only one logo at a time).
	if code, body := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", []byte(sampleSVG), "image/svg+xml; charset=utf-8"); code != http.StatusNoContent {
		t.Fatalf("PUT svg: status=%d body=%s", code, body)
	}
	res2, err := http.Get(ts.URL + "/api/v1/settings/logo")
	if err != nil {
		t.Fatalf("GET logo svg: %v", err)
	}
	got2, _ := readAllClose(res2)
	if ct := res2.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("Content-Type after svg = %q; want image/svg+xml", ct)
	}
	if !bytes.Equal(got2, []byte(sampleSVG)) {
		t.Fatalf("GET svg body mismatch")
	}

	// Delete restores the default (no logo).
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/logo", "", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE logo: status=%d body=%s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/settings/logo", "", ""); code != http.StatusNotFound {
		t.Fatalf("GET after DELETE: status=%d; want 404", code)
	}
}

// TestLogoUploadValidation rejects everything that is not a small, well-formed PNG
// or SVG: a wrong media type, bytes that don't match the declared type, an empty
// body, and an over-limit upload.
func TestLogoUploadValidation(t *testing.T) {
	ts := newTestServer(t)

	// Unsupported media type.
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", samplePNG(), "image/jpeg"); code != http.StatusUnsupportedMediaType {
		t.Fatalf("PUT jpeg: status=%d; want 415", code)
	}
	// Declared PNG but not actually a PNG.
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", []byte("not a png"), "image/png"); code != http.StatusBadRequest {
		t.Fatalf("PUT bogus png: status=%d; want 400", code)
	}
	// Declared SVG but no <svg root.
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", []byte("<html></html>"), "image/svg+xml"); code != http.StatusBadRequest {
		t.Fatalf("PUT bogus svg: status=%d; want 400", code)
	}
	// Empty body.
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", nil, "image/png"); code != http.StatusBadRequest {
		t.Fatalf("PUT empty: status=%d; want 400", code)
	}
	// Over the size limit.
	big := append(append([]byte{}, pngMagicBytes...), make([]byte, (512<<10)+1)...)
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", big, "image/png"); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT oversize: status=%d; want 413", code)
	}
	// After every rejection, no logo was stored.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/settings/logo", "", ""); code != http.StatusNotFound {
		t.Fatalf("GET after rejects: status=%d; want 404", code)
	}
}

// TestLogoAdminGated confirms writes are admin-only when auth is on, and that the
// two refusals are told apart: reading the logo is public, writing it with no
// credential at all is 401, and writing it as a signed-in non-admin is 403.
//
// The anonymous write used to answer 403 as well, because the whole path was
// exempt from the boundary and requireAdmin inside the handler was the only thing
// refusing. Only the GET is public now (access.go), so a caller who presented
// nothing is told to authenticate rather than that they lack a role.
func TestLogoAdminGated(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	// GET is public even before login.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/settings/logo", "", ""); code != http.StatusNotFound {
		t.Fatalf("public GET logo: status=%d; want 404 (no logo)", code)
	}
	// No credential: refused at the boundary.
	if code, _ := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", samplePNG(), "image/png"); code != http.StatusUnauthorized {
		t.Fatalf("anon PUT logo: status=%d; want 401", code)
	}
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/settings/logo", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("anon DELETE logo: status=%d; want 401", code)
	}

	// Signed in, but not an admin: past the boundary, refused by requireAdmin.
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("admin login: got %d", code)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	alice := newClient(t)
	if code := login(t, alice, ts, "alice", "password1"); code != http.StatusOK {
		t.Fatalf("alice login: got %d", code)
	}
	if code, _ := cReq(t, alice, ts, http.MethodDelete, "/api/v1/settings/logo", ""); code != http.StatusForbidden {
		t.Fatalf("non-admin DELETE logo: status=%d; want 403", code)
	}
}

// TestLogoHandlerIOErrors drives the handlers' 5xx branches by putting a directory
// where the logo file belongs: the read fails (GET → 500), the atomic rename fails
// (PUT → 500), and the remove fails (DELETE → 500).
func TestLogoHandlerIOErrors(t *testing.T) {
	ts, dir := newBackupServer(t)
	settingsDir := filepath.Join(dir, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	// A non-empty directory in the logo file's place makes read, rename and remove
	// all fail with a non-NotExist error.
	logoDir := filepath.Join(settingsDir, "logo.png")
	if err := os.Mkdir(logoDir, 0o755); err != nil {
		t.Fatalf("mkdir logo.png: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logoDir, "x"), []byte("y"), 0o644); err != nil {
		t.Fatalf("populate dir: %v", err)
	}

	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/logo", "", ""); code != http.StatusInternalServerError {
		t.Fatalf("GET with dir in place: status=%d body=%s; want 500", code, body)
	}
	if code, body := doReqBytes(t, ts, http.MethodPut, "/api/v1/settings/logo", samplePNG(), "image/png"); code != http.StatusInternalServerError {
		t.Fatalf("PUT with dir in place: status=%d body=%s; want 500", code, body)
	}
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/logo", "", ""); code != http.StatusInternalServerError {
		t.Fatalf("DELETE with dir in place: status=%d body=%s; want 500", code, body)
	}
}

// TestThemeHandlerIOErrors drives the handlers' 5xx branches by making the on-disk
// theme file unusable: a corrupt file fails the read (GET → 500), and a directory
// in the file's place fails both the atomic save (PUT → 500) and the remove
// (DELETE → 500). Uses newBackupServer, which exposes the data dir.
func TestThemeHandlerIOErrors(t *testing.T) {
	ts, dir := newBackupServer(t)
	themeFile := filepath.Join(dir, "settings", "theme.json")
	if err := os.MkdirAll(filepath.Dir(themeFile), 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}

	// Corrupt file → GET surfaces a read/decode error as 500.
	if err := os.WriteFile(themeFile, []byte("{ corrupt"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/theme", "", ""); code != http.StatusInternalServerError {
		t.Fatalf("GET with corrupt file: status=%d body=%s, want 500", code, body)
	}

	// A directory in the file's place → the atomic rename (PUT) and the remove
	// (DELETE) both fail, surfaced as 500.
	if err := os.Remove(themeFile); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Mkdir(themeFile, 0o755); err != nil {
		t.Fatalf("mkdir in file's place: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themeFile, "x"), []byte("y"), 0o644); err != nil {
		t.Fatalf("populate dir: %v", err)
	}
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/theme", `{"accent":"#123456"}`, "application/json"); code != http.StatusInternalServerError {
		t.Fatalf("PUT with dir in place of file: status=%d body=%s, want 500", code, body)
	}
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/theme", "", ""); code != http.StatusInternalServerError {
		t.Fatalf("DELETE with non-empty dir: status=%d body=%s, want 500", code, body)
	}
}
