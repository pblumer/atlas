package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsStoreThemeRoundTrip covers the durable singleton store directly: a
// fresh store reports the default (empty accent), a save round-trips, and clear
// restores the default. Clear is idempotent.
func TestSettingsStoreThemeRoundTrip(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}

	// Unset → default.
	if got, err := st.getTheme(); err != nil || got.Accent != "" {
		t.Fatalf("getTheme (unset) = %+v, %v; want empty accent", got, err)
	}

	if err := st.saveTheme(uiTheme{Accent: "#7c3aed"}); err != nil {
		t.Fatalf("saveTheme: %v", err)
	}
	if got, err := st.getTheme(); err != nil || got.Accent != "#7c3aed" {
		t.Fatalf("getTheme = %+v, %v; want #7c3aed", got, err)
	}

	// Overwrite.
	if err := st.saveTheme(uiTheme{Accent: "#0d7a63"}); err != nil {
		t.Fatalf("saveTheme overwrite: %v", err)
	}
	if got, _ := st.getTheme(); got.Accent != "#0d7a63" {
		t.Fatalf("getTheme after overwrite = %q; want #0d7a63", got.Accent)
	}

	if err := st.clearTheme(); err != nil {
		t.Fatalf("clearTheme: %v", err)
	}
	if got, _ := st.getTheme(); got.Accent != "" {
		t.Fatalf("getTheme after clear = %q; want empty", got.Accent)
	}
	// Idempotent.
	if err := st.clearTheme(); err != nil {
		t.Fatalf("clearTheme (already clear): %v", err)
	}
}

// TestSettingsStoreErrors covers the store's I/O-failure branches: a corrupt file,
// an unreadable file, a clear that can't remove, and a directory that can't be
// created. Each must surface an error rather than silently returning a default.
func TestSettingsStoreErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := newSettingsStore(filepath.Join(dir, "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}

	// A corrupt theme file → decode error (not treated as "no override").
	if err := os.WriteFile(st.file, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := st.getTheme(); err == nil {
		t.Fatal("getTheme on a corrupt file: want error, got nil")
	}

	// A read failure that isn't "not found" (a directory in the file's place) →
	// error, and clearTheme on that non-empty directory can't remove it → error.
	if err := os.Remove(st.file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Mkdir(st.file, 0o755); err != nil {
		t.Fatalf("mkdir in file's place: %v", err)
	}
	if _, err := st.getTheme(); err == nil {
		t.Fatal("getTheme on a directory: want error, got nil")
	}
	if err := os.WriteFile(filepath.Join(st.file, "x"), []byte("y"), 0o644); err != nil {
		t.Fatalf("populate dir: %v", err)
	}
	if err := st.clearTheme(); err == nil {
		t.Fatal("clearTheme on a non-empty directory: want error, got nil")
	}

	// newSettingsStore where the directory can't be created (a file blocks the path).
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := newSettingsStore(filepath.Join(blocker, "settings")); err == nil {
		t.Fatal("newSettingsStore under a file: want error, got nil")
	}
}

// TestSetThemeRejectsBadBodies covers handleSetTheme's request-validation branches,
// which return before any store access (so a bare Server with auth off suffices):
// an unreadable body, malformed JSON, and a syntactically valid but non-colour
// accent are each a 400.
func TestSetThemeRejectsBadBodies(t *testing.T) {
	s := &Server{} // auth off → admin gate passes; these all fail before s.do
	for _, tc := range []struct {
		name string
		body interface{ Read([]byte) (int, error) }
	}{
		{"unreadable body", errReader{}},
		{"malformed JSON", strings.NewReader("{ not json")},
		{"empty accent", strings.NewReader(`{"accent":""}`)},
		{"non-colour accent", strings.NewReader(`{"accent":"periwinkle"}`)},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/theme", tc.body)
		s.handleSetTheme(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", tc.name, rec.Code)
		}
	}
}

// TestNormalizeAccent covers the server-side hex validation/canonicalisation that
// gates what can be persisted.
func TestNormalizeAccent(t *testing.T) {
	for _, tc := range []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"#0b5cff", "#0b5cff", true},
		{"0b5cff", "#0b5cff", true},      // leading # optional
		{"#0B5CFF", "#0b5cff", true},     // lowercased
		{"  #0b5cff  ", "#0b5cff", true}, // trimmed
		{"#abc", "#aabbcc", true},        // 3-digit shorthand
		{"abc", "#aabbcc", true},
		{"", "", false},
		{"#12345", "", false},   // wrong length
		{"#1234567", "", false}, // too long
		{"#gggggg", "", false},  // non-hex
		{"blue", "", false},     // named colours are not accepted
	} {
		got, ok := normalizeAccent(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("normalizeAccent(%q) = (%q, %v); want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestThemeWritesRequireAdmin: with auth enabled, PUT and DELETE demand the admin
// role. The gate runs before any store access, so a bare Server (no run loop)
// suffices — mirroring TestBackupRestoreRequireAdmin.
// The brand accent is read by the login screen and written by an administrator.
// Asserted at the boundary, where the role is decided now (routeroles.go).
func TestThemeWritesRequireAdmin(t *testing.T) {
	ordinary := []string{RoleModeler, RoleOperator, RoleUser}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if got := boundaryStatus(t, ordinary, method, "/api/v1/settings/theme"); got != http.StatusForbidden {
			t.Errorf("%s theme without an admin principal: status=%d, want 403", method, got)
		}
	}
}

// TestThemeReadIsPublic: GET /api/v1/settings/theme is not gated even when auth is
// enforced, so the login screen can apply the brand colour before sign-in.
func TestThemeReadIsPublic(t *testing.T) {
	if classifyPath(t, http.MethodGet, "/api/v1/settings/theme") != accessPublic {
		t.Fatal("GET theme must be public (applied before login)")
	}
	if classifyPath(t, http.MethodPut, "/api/v1/settings/theme") != accessAuthenticated {
		t.Fatal("PUT theme must be gated")
	}
}

// TestSettingsStoreLogoRoundTrip covers the logo store directly: none by default,
// a PNG round-trips, switching to an SVG replaces it (the stale PNG is removed so
// getLogo returns the SVG), and clear restores the default and is idempotent.
func TestSettingsStoreLogoRoundTrip(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}

	if _, _, ok, err := st.getLogo(); err != nil || ok {
		t.Fatalf("getLogo (unset) = ok=%v, %v; want ok=false", ok, err)
	}

	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2}
	if err := st.saveLogo(png, "image/png"); err != nil {
		t.Fatalf("saveLogo png: %v", err)
	}
	if data, ct, ok, err := st.getLogo(); err != nil || !ok || ct != "image/png" || string(data) != string(png) {
		t.Fatalf("getLogo png = %q, %q, %v, %v", data, ct, ok, err)
	}

	// Switching format replaces the file: the SVG is served and the PNG is gone.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if err := st.saveLogo(svg, "image/svg+xml"); err != nil {
		t.Fatalf("saveLogo svg: %v", err)
	}
	if _, ct, ok, _ := st.getLogo(); !ok || ct != "image/svg+xml" {
		t.Fatalf("getLogo after switch = ct=%q ok=%v; want image/svg+xml", ct, ok)
	}
	if _, err := os.Stat(st.logoPath("png")); !os.IsNotExist(err) {
		t.Fatalf("stale logo.png still present after switch: err=%v", err)
	}

	if err := st.clearLogo(); err != nil {
		t.Fatalf("clearLogo: %v", err)
	}
	if _, _, ok, _ := st.getLogo(); ok {
		t.Fatal("getLogo after clear: want ok=false")
	}
	if err := st.clearLogo(); err != nil {
		t.Fatalf("clearLogo (idempotent): %v", err)
	}

	// An unsupported content type is rejected by the store's own guard.
	if err := st.saveLogo(png, "image/gif"); err == nil {
		t.Fatal("saveLogo with unsupported type: want error, got nil")
	}
}

// TestValidLogo covers the byte-level sanity gate for each accepted type and the
// default (unknown type) branch.
func TestValidLogo(t *testing.T) {
	pngOK := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}
	for _, tc := range []struct {
		name string
		ct   string
		data []byte
		want bool
	}{
		{"png ok", "image/png", pngOK, true},
		{"png bad magic", "image/png", []byte("not-a-png"), false},
		{"svg ok", "image/svg+xml", []byte(`<svg></svg>`), true},
		{"svg no root", "image/svg+xml", []byte("<html></html>"), false},
		{"svg invalid utf8", "image/svg+xml", []byte{0xff, 0xfe, '<', 's', 'v', 'g'}, false},
		{"unknown type", "image/gif", pngOK, false},
	} {
		if got := validLogo(tc.ct, tc.data); got != tc.want {
			t.Errorf("%s: validLogo = %v; want %v", tc.name, got, tc.want)
		}
	}
}

// TestNormalizeLogoType strips media-type parameters and canonicalises case.
func TestNormalizeLogoType(t *testing.T) {
	for in, want := range map[string]string{
		"image/png":                    "image/png",
		"IMAGE/PNG":                    "image/png",
		"image/svg+xml; charset=utf-8": "image/svg+xml",
		"  image/svg+xml ":             "image/svg+xml",
	} {
		if got := normalizeLogoType(in); got != want {
			t.Errorf("normalizeLogoType(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestSetLogoRejectsUnreadableBody covers handleSetLogo's read-body error branch,
// which returns 400 before any store access (a bare Server with auth off suffices).
func TestSetLogoRejectsUnreadableBody(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/logo", errReader{})
	req.Header.Set("Content-Type", "image/png")
	s.handleSetLogo(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable logo body: status=%d, want 400", rec.Code)
	}
}

// TestLogoWritesRequireAdmin: with auth enabled, PUT and DELETE demand the admin
// role, and GET stays public so the login screen can show the mark before sign-in.
func TestLogoWritesRequireAdmin(t *testing.T) {
	// Refused at the boundary, where the role is decided now (routeroles.go).
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser}, method, "/api/v1/settings/logo")
		if got != http.StatusForbidden {
			t.Errorf("%s logo without an admin principal: status=%d, want 403", method, got)
		}
	}
	if classifyPath(t, http.MethodGet, "/api/v1/settings/logo") != accessPublic {
		t.Fatal("GET logo must be public (shown before login)")
	}
	if classifyPath(t, http.MethodPut, "/api/v1/settings/logo") != accessAuthenticated {
		t.Fatal("PUT logo must be gated")
	}
}

// TestSettingsStoreReportsAStaleArtifactItCannotRemove covers the half of these
// writes that is easy to overlook: the cleanup. Both the logo and the AD mock seed
// are content-addressed, so saving a new one has to delete the old one — and a
// stale file that survives is not untidy, it is a second answer to a question that
// should have exactly one.
//
// A logo left behind under another extension is served alongside the new one; an
// AD seed left behind is a directory a restarted worker can still find and start
// from, which is the outage that design exists to end. So a removal that fails is
// reported rather than swallowed, and these drive it by putting a non-empty
// directory where the stale file would be.
func TestSettingsStoreReportsAStaleArtifactItCannotRemove(t *testing.T) {
	t.Run("logo", func(t *testing.T) {
		st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
		if err != nil {
			t.Fatalf("newSettingsStore: %v", err)
		}
		if err := st.saveLogo([]byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), "image/svg+xml"); err != nil {
			t.Fatalf("save the first logo: %v", err)
		}
		// The stale path becomes something os.Remove cannot delete.
		stale := st.logoPath("svg")
		if err := os.Remove(stale); err != nil {
			t.Fatalf("remove the stale logo: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(stale, "occupied"), 0o755); err != nil {
			t.Fatalf("occupy the stale path: %v", err)
		}

		err = st.saveLogo([]byte("\x89PNG\r\n\x1a\n"), "image/png")
		if err == nil {
			t.Fatal("a stale logo that could not be removed was reported as a clean save")
		}
		if !strings.Contains(err.Error(), "stale logo") {
			t.Errorf("error = %v, want it to name what could not be removed", err)
		}
	})

	t.Run("ad mock seed", func(t *testing.T) {
		st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
		if err != nil {
			t.Fatalf("newSettingsStore: %v", err)
		}
		first := adMockSetting{Enabled: true, Seed: "dn: dc=example,dc=test\n"}
		if err := st.saveADMock(first); err != nil {
			t.Fatalf("save the first seed: %v", err)
		}
		stale := st.adSeedPath(first)
		if err := os.Remove(stale); err != nil {
			t.Fatalf("remove the stale seed: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(stale, "occupied"), 0o755); err != nil {
			t.Fatalf("occupy the stale path: %v", err)
		}

		// The seed write has to fail the whole save: the record is what a restarted
		// worker is pointed at, and a record naming a seed that is not there yet is
		// worse than no new record at all.
		err = st.saveADMock(adMockSetting{Enabled: true, Seed: "dn: dc=other,dc=test\n"})
		if err == nil {
			t.Fatal("a stale seed that could not be removed was reported as a clean save")
		}
		if !strings.Contains(err.Error(), "stale ad mock seed") {
			t.Errorf("error = %v, want it to name what could not be removed", err)
		}
	})
}

// TestWriteADSeedReportsASeedItCannotWrite: the seed is written under a
// content-addressed name, and if that path is not writable the save must fail
// before the record lands. A record that named an unwritten seed would point a
// restarted worker at nothing.
func TestWriteADSeedReportsASeedItCannotWrite(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	a := adMockSetting{Enabled: true, Seed: "dn: dc=example,dc=test\n"}
	// Occupy the seed's own path with a directory, so creating its temp file is
	// fine but the rename onto it cannot succeed.
	if err := os.MkdirAll(st.adSeedPath(a), 0o755); err != nil {
		t.Fatalf("occupy the seed path: %v", err)
	}
	if err := st.saveADMock(a); err == nil {
		t.Fatal("an unwritable seed was reported as a clean save")
	}
	// And no record was written, so nothing points at the seed that is not there.
	if _, ok, err := st.getADMock(); err != nil || ok {
		t.Errorf("a record landed despite the seed failing: ok=%v err=%v", ok, err)
	}
}
