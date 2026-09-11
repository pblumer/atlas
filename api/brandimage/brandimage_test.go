package brandimage_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/brandimage"
)

// The gate: what this server will store as somebody's brand, and what it will
// not. These cases moved here from the instance logo's own tests when the
// catalogue grew a mark of its own — one check, so a hardening lands on both.

func TestValid(t *testing.T) {
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
		if got := brandimage.Valid(tc.ct, tc.data); got != tc.want {
			t.Errorf("%s: Valid = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	for in, want := range map[string]string{
		"image/png":                    "image/png",
		"IMAGE/PNG":                    "image/png",
		"image/svg+xml; charset=utf-8": "image/svg+xml",
		"  image/svg+xml ":             "image/svg+xml",
	} {
		if got := brandimage.NormalizeType(in); got != want {
			t.Errorf("NormalizeType(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestServeNeutralisesTheImage is the half of the mitigation that is not a check.
// An SVG is a scriptable document; what makes a stored one harmless is this
// response, so the headers are asserted rather than assumed.
func TestServeNeutralisesTheImage(t *testing.T) {
	rec := httptest.NewRecorder()
	brandimage.Serve(rec, "image/svg+xml", []byte(`<svg/>`))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff missing: %q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP = %q; want a sandboxed default-src 'none' policy", csp)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q; a replaced mark would go unnoticed", got)
	}
	if rec.Body.String() != `<svg/>` {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestTheTwoTablesAgree: an extension that maps back to a different type would
// serve a PNG as an SVG, which nosniff then pins.
func TestTheTwoTablesAgree(t *testing.T) {
	for ct, ext := range brandimage.ExtByType {
		if back := brandimage.TypeByExt[ext]; back != ct {
			t.Errorf("%s → %s → %s", ct, ext, back)
		}
	}
	if len(brandimage.Exts) != len(brandimage.ExtByType) {
		t.Errorf("Exts has %d entries, ExtByType %d — a format nobody can read back, or one nobody lists",
			len(brandimage.Exts), len(brandimage.ExtByType))
	}
	for _, ext := range brandimage.Exts {
		if _, ok := brandimage.TypeByExt[ext]; !ok {
			t.Errorf("stored extension %q has no media type", ext)
		}
	}
}
