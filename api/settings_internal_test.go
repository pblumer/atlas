package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
func TestThemeWritesRequireAdmin(t *testing.T) {
	s := &Server{authEnabled: true}
	for _, tc := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		meth string
	}{
		{"set", s.handleSetTheme, http.MethodPut},
		{"delete", s.handleDeleteTheme, http.MethodDelete},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.meth, "/api/v1/settings/theme", nil)
		tc.fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s theme without an admin principal: status=%d, want 403", tc.name, rec.Code)
		}
	}
}

// TestThemeReadIsPublic: GET /api/v1/settings/theme is not gated even when auth is
// enforced, so the login screen can apply the brand colour before sign-in.
func TestThemeReadIsPublic(t *testing.T) {
	if requiresAuth("/api/v1/settings/theme") {
		t.Fatal("GET theme must be public (applied before login)")
	}
}
