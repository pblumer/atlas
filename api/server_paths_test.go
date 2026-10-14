package api_test

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The application scope New wires into the Panorama and information-model services.
//
// Neither service owns a notion of "application": New hands each of them a resolver
// that reads the project store and answers whether the application exists and what the
// caller may do in it (ADR-0147, ADR-0189). That resolver is the whole of their access
// control, so its two failure answers matter as much as its role arithmetic: an
// application that does not exist must refuse the create, and a project record that
// cannot be read must refuse it too — as a failure, not as "no such application", and
// never by filing a model under an application nobody can see.

// TestServerModelServicesRefuseAnApplicationTheyCannotResolve creates a model of each
// kind under an application that does not exist, and under one whose project record is
// cut short, and checks nothing was filed either way.
func TestServerModelServicesRefuseAnApplicationTheyCannotResolve(t *testing.T) {
	panoramaBody := func(appID string) string {
		b, err := json.Marshal(map[string]any{
			"applicationId": appID, "name": "Landscape", "notation": "archimate-3.2", "xml": panoramaExchangeXML,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	for _, svc := range []struct {
		name, path string
		body       func(appID string) string
	}{
		{"information model", "/api/v1/infomodel/models", func(appID string) string {
			return `{"applicationId":"` + appID + `","name":"Sales data"}`
		}},
		{"panorama", "/api/v1/panorama/models", panoramaBody},
	} {
		t.Run(svc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := boot(t, dir)
			defer s.shutdown()
			const broken = "app-broken"
			if err := os.WriteFile(filepath.Join(dir, "projects", hex.EncodeToString([]byte(broken))+".json"), []byte(`{"id":`), 0o600); err != nil {
				t.Fatalf("break project record: %v", err)
			}

			code, body := doReq(t, s.ts, http.MethodPost, svc.path, svc.body("app-ghost"), "application/json")
			if code != http.StatusBadRequest || !strings.Contains(string(body), "unknown application") {
				t.Errorf("create under an application that does not exist: status=%d body=%s, want 400 naming it unknown", code, body)
			}
			code, body = doReq(t, s.ts, http.MethodPost, svc.path, svc.body(broken), "application/json")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), "projectstore") {
				t.Errorf("create under an unreadable application: status=%d body=%s, want 500 naming the project store", code, body)
			}

			code, body = doReq(t, s.ts, http.MethodGet, svc.path, "", "")
			if code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
				t.Errorf("after two refused creates the %s listing is status=%d %s, want empty", svc.name, code, body)
			}
		})
	}
}
