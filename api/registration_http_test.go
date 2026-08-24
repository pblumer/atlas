package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// regConfig mirrors the public registration-config response.
type regConfig struct {
	Enabled   bool   `json:"enabled"`
	ProcessID string `json:"processId"`
	URL       string `json:"url"`
}

// TestRegistrationEndToEnd drives the self-service registration wiring over HTTP
// with the system processes deployed (ADR-0126): a fresh instance reports
// registration enabled and points at the intake process's public start form; that
// public form carries no role field (an anonymous registrant cannot request an
// elevated account); an operator can reconfigure and switch it off.
func TestRegistrationEndToEnd(t *testing.T) {
	ts := newTestServerWith(t, api.WithSystemProcesses())

	readCfg := func() regConfig {
		code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/registration", "", "")
		if code != http.StatusOK {
			t.Fatalf("GET registration: status=%d body=%s", code, body)
		}
		var c regConfig
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("decode registration %s: %v", body, err)
		}
		return c
	}

	// Fresh instance: registration is enabled and points at the intake process.
	cfg := readCfg()
	if !cfg.Enabled || cfg.ProcessID != "proc_benutzer_aufnahme" {
		t.Fatalf("default registration = %+v; want enabled proc_benutzer_aufnahme", cfg)
	}
	if !strings.HasPrefix(cfg.URL, "/public/forms/") {
		t.Fatalf("registration URL = %q; want a /public/forms/<token> link", cfg.URL)
	}

	// The published public start form must not offer a role choice — the security
	// boundary that makes public registration safe (the admin assigns the role at
	// approval).
	code, body := doReq(t, ts, http.MethodGet, cfg.URL+"/schema", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET public schema: status=%d body=%s", code, body)
	}
	schema := string(body)
	if strings.Contains(schema, `"key":"rolle"`) {
		t.Fatalf("public registration form still exposes the role field:\n%s", schema)
	}
	if !strings.Contains(schema, `"key":"vorname"`) {
		t.Fatalf("public registration form is missing expected fields:\n%s", schema)
	}

	// Reading again returns the same link (idempotent mint).
	if again := readCfg(); again.URL != cfg.URL {
		t.Fatalf("registration URL changed between reads: %q vs %q", cfg.URL, again.URL)
	}

	// Configuring a non-existent process is rejected.
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/settings/registration",
		`{"processId":"no-such-process"}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("PUT bogus process: status=%d; want 400", code)
	}

	// Re-configuring to the intake process explicitly is accepted and keeps it on.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/settings/registration",
		`{"processId":"proc_benutzer_aufnahme"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("PUT intake process: status=%d body=%s", code, body)
	}
	if cfg := readCfg(); !cfg.Enabled {
		t.Fatalf("registration off after re-enabling: %+v", cfg)
	}

	// Switching it off hides the link.
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/settings/registration", "", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE registration: status=%d body=%s", code, body)
	}
	if cfg := readCfg(); cfg.Enabled {
		t.Fatalf("registration still enabled after DELETE: %+v", cfg)
	}
}
