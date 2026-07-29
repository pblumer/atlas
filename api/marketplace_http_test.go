package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

const (
	restPkgID   = "atlas.rest-outbound"
	scriptPkgID = "community.powershell-ad-lookup"
)

type manifestDTO struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	CarriesCode bool   `json:"carriesCode"`
	Checksum    string `json:"checksum"`
}

func listPackages(t *testing.T, ts *httptest.Server, query string) []manifestDTO {
	t.Helper()
	code, body := doReq(t, ts, "GET", "/api/v1/marketplace/packages"+query, "", "")
	if code != 200 {
		t.Fatalf("list%s = %d, want 200 (%s)", query, code, body)
	}
	var out []manifestDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out
}

// TestMarketplaceListFilters covers the full catalog list, the kind filter, and the
// free-text search.
func TestMarketplaceListFilters(t *testing.T) {
	ts := newTestServer(t)

	all := listPackages(t, ts, "")
	if len(all) < 4 {
		t.Fatalf("catalog has %d packages, want >= 4", len(all))
	}
	for _, m := range all {
		if m.Checksum == "" {
			t.Errorf("package %s served without a checksum", m.ID)
		}
	}

	connectors := listPackages(t, ts, "?kind=connector")
	if len(connectors) == 0 {
		t.Fatal("kind=connector returned nothing")
	}
	for _, m := range connectors {
		if m.Kind != "connector" {
			t.Errorf("kind filter leaked a %q package", m.Kind)
		}
	}

	scripts := listPackages(t, ts, "?kind=script-task")
	for _, m := range scripts {
		if !m.CarriesCode {
			t.Errorf("script-task %s should report carriesCode=true", m.ID)
		}
	}

	slack := listPackages(t, ts, "?q=slack")
	if len(slack) != 1 {
		t.Fatalf("q=slack returned %d packages, want 1", len(slack))
	}

	none := listPackages(t, ts, "?q=zzz-nothing-matches")
	if len(none) != 0 {
		t.Fatalf("q with no match returned %d, want 0", len(none))
	}
}

// TestMarketplaceGetPackage covers the detail endpoint (with template payload) and
// the 404 for an unknown id.
func TestMarketplaceGetPackage(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, "GET", "/api/v1/marketplace/packages/"+restPkgID, "", "")
	if code != 200 {
		t.Fatalf("get = %d, want 200 (%s)", code, body)
	}
	var view struct {
		ID       string          `json:"id"`
		Template json.RawMessage `json:"template"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.ID != restPkgID || len(view.Template) == 0 {
		t.Fatalf("get returned %+v, want the package with a template", view)
	}

	if code, _ := doReq(t, ts, "GET", "/api/v1/marketplace/packages/nope", "", ""); code != 404 {
		t.Fatalf("get unknown = %d, want 404", code)
	}
}

// TestMarketplaceInstallFlow covers installing a data-only package and a code-
// bearing one (auth off, so both succeed), the reviewRequired flag, listing, and
// idempotent uninstall.
func TestMarketplaceInstallFlow(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, "POST", "/api/v1/marketplace/packages/"+restPkgID+"/install", "", "")
	if code != 200 {
		t.Fatalf("install connector = %d, want 200 (%s)", code, body)
	}
	var installed struct {
		ID             string `json:"id"`
		Kind           string `json:"kind"`
		ReviewRequired bool   `json:"reviewRequired"`
	}
	if err := json.Unmarshal(body, &installed); err != nil {
		t.Fatalf("decode install: %v", err)
	}
	if installed.ID != restPkgID || installed.ReviewRequired {
		t.Errorf("connector install = %+v, want id=%s reviewRequired=false", installed, restPkgID)
	}

	// A script task installs (auth is off) but must flag reviewRequired.
	code, body = doReq(t, ts, "POST", "/api/v1/marketplace/packages/"+scriptPkgID+"/install", "", "")
	if code != 200 {
		t.Fatalf("install script = %d, want 200 (%s)", code, body)
	}
	if err := json.Unmarshal(body, &installed); err != nil {
		t.Fatalf("decode install: %v", err)
	}
	if !installed.ReviewRequired {
		t.Error("script-task install should set reviewRequired=true")
	}

	// Installing an unknown package is a 404.
	if code, _ := doReq(t, ts, "POST", "/api/v1/marketplace/packages/nope/install", "", ""); code != 404 {
		t.Fatalf("install unknown = %d, want 404", code)
	}

	// Both installs show up.
	code, body = doReq(t, ts, "GET", "/api/v1/marketplace/installed", "", "")
	if code != 200 {
		t.Fatalf("list installed = %d, want 200 (%s)", code, body)
	}
	var recs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &recs); err != nil {
		t.Fatalf("decode installed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("installed count = %d, want 2", len(recs))
	}

	// Uninstall is idempotent.
	if code, _ := doReq(t, ts, "DELETE", "/api/v1/marketplace/installed/"+restPkgID, "", ""); code != 204 {
		t.Fatalf("uninstall = %d, want 204", code)
	}
	if code, _ := doReq(t, ts, "DELETE", "/api/v1/marketplace/installed/"+restPkgID, "", ""); code != 204 {
		t.Fatalf("uninstall again = %d, want 204 (idempotent)", code)
	}

	code, body = doReq(t, ts, "GET", "/api/v1/marketplace/installed", "", "")
	if code != 200 {
		t.Fatalf("list installed after uninstall = %d (%s)", code, body)
	}
	if err := json.Unmarshal(body, &recs); err != nil {
		t.Fatalf("decode installed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("installed count after uninstall = %d, want 1", len(recs))
	}
}
