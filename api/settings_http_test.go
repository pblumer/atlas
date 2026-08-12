package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

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
