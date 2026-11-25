package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// browserRequest sends one request the way a browser would describe it: with the
// Sec-Fetch-Site and Origin headers a page's script cannot set or forge. An empty
// value leaves the header out.
func browserRequest(t *testing.T, srv *httptest.Server, method, path, site, origin, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// deployedCount is how many process definitions the server holds.
func deployedCount(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	code, body := doReq(t, srv, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: %d %s", code, body)
	}
	var out []json.RawMessage
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode process list: %v (%s)", err, body)
	}
	return len(out)
}

// TestABrowsersRequestFromAnotherOriginChangesNothing: a browser attaches a person's
// session to a request whichever page caused it. SameSite=Lax on the cookie keeps it
// off requests from another *site*, and a site is wider than an origin: another
// service under a sibling host name, or on another port of the same host, is the same
// site and its pages' requests arrived signed in. A browser also says, in headers a
// page cannot set, where a request comes from — and a request that changes something
// is now accepted only when that is Atlas's own origin, or when no browser is
// speaking at all.
func TestABrowsersRequestFromAnotherOriginChangesNothing(t *testing.T) {
	srv := newTestServer(t)
	own := srv.URL // http://127.0.0.1:port — the origin the test server answers on

	refused := []struct{ name, site, origin string }{
		{"another site", "cross-site", "https://elsewhere.example"},
		{"a sibling host or another port of this one", "same-site", "http://127.0.0.1:1"},
		{"another origin, from a browser that sends no Sec-Fetch-Site", "", "https://elsewhere.example"},
		{"an opaque origin, from such a browser", "", "null"},
	}
	for _, c := range refused {
		code, body := browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", c.site, c.origin, sampleBPMN)
		if code != http.StatusForbidden {
			t.Errorf("%s: a deployment was answered %d, want 403 (%s)", c.name, code, body)
		}
		if !strings.Contains(body, `"error"`) || !strings.Contains(body, "origin") {
			t.Errorf("%s: the refusal does not say what was refused, in the API's own shape: %s", c.name, body)
		}
	}
	if n := deployedCount(t, srv); n != 0 {
		t.Fatalf("%d definitions are deployed after four refused deployments; a refusal has to come before the handler", n)
	}

	accepted := []struct{ name, site, origin string }{
		{"Atlas's own page", "same-origin", own},
		{"something the person did in the browser itself", "none", ""},
		{"a client that is not a browser", "", ""},
		{"Atlas's own page, from a browser that sends no Sec-Fetch-Site", "", own},
	}
	for _, c := range accepted {
		code, body := browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", c.site, c.origin, sampleBPMN)
		if code != http.StatusOK {
			t.Errorf("%s: a deployment was answered %d, want 200 (%s)", c.name, code, body)
		}
	}

	// Reading is not what this is about: a page of another origin that asks for
	// something gets an answer its script cannot read, and nothing has changed.
	if code, _ := browserRequest(t, srv, http.MethodGet, "/api/v1/processes", "cross-site", "https://elsewhere.example", ""); code != http.StatusOK {
		t.Errorf("a read from another origin was answered %d, want 200", code)
	}
}

// TestWhatIsMeantToBeCalledFromElsewhereStillIs: two families of routes are called
// from other origins on purpose, and neither acts on a session. A public start form
// and the wizard behind it are submitted from the page that embeds them (ADR-0186);
// the OAuth endpoints authenticate a client from the request's own body. They are
// answered as before — whatever each says to a request that names nothing real, it
// is not the cross-origin refusal.
func TestWhatIsMeantToBeCalledFromElsewhereStillIs(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{
		"/public/forms/0123456789abcdef/start",
		"/public/forms/0123456789abcdef/fields/x/options",
		"/public/wizard/steps/1/complete",
		"/public/wizard/steps/1/fields/x/options",
		"/oauth/token",
		"/oauth/revoke",
	} {
		code, body := browserRequest(t, srv, http.MethodPost, path, "cross-site", "https://elsewhere.example", "{}")
		if code == http.StatusForbidden && strings.Contains(body, "origin") {
			t.Errorf("POST %s from another origin was refused as cross-origin: %s", path, body)
		}
	}
}

// TestTheConfiguredExternalOriginIsAtlassOwn: behind a proxy that does not pass the
// Host header on, the origin a browser names and the host the request arrives for are
// two different strings for the same server. A browser that sends Sec-Fetch-Site is
// not affected — it has already said "same-origin" — but one that does not is judged
// by that comparison. An operator who told the server its outside address
// (--external-url) has answered the question, so that origin is accepted as Atlas's
// own.
func TestTheConfiguredExternalOriginIsAtlassOwn(t *testing.T) {
	srv := newTestServerWith(t, api.WithExternalURL("https://atlas.example.com/"))
	if code, body := browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", "", "https://atlas.example.com", sampleBPMN); code != http.StatusOK {
		t.Errorf("a request from the configured external origin was answered %d, want 200 (%s)", code, body)
	}
	if code, _ := browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", "", "https://atlas.example.com.elsewhere.example", sampleBPMN); code != http.StatusForbidden {
		t.Errorf("a request from an origin that only starts like the external one was answered %d, want 403", code)
	}
	// What a browser says outright is not overruled by the setting.
	if code, _ := browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", "cross-site", "https://elsewhere.example", sampleBPMN); code != http.StatusForbidden {
		t.Errorf("a cross-site request was answered %d on a server with an external URL, want 403", code)
	}
}

// TestACrossOriginRefusalIsLogged: the refusal is the one sign an operator gets of
// either thing it can mean — a page somewhere trying to act as a signed-in person, or
// a proxy in front that rewrites Host — so it is in the log with what tells the two
// apart: the path, the origin the browser named and the host the request arrived for.
func TestACrossOriginRefusalIsLogged(t *testing.T) {
	sink := captureLog(t)
	srv := newTestServer(t)
	browserRequest(t, srv, http.MethodPost, "/api/v1/deployments", "cross-site", "https://elsewhere.example", sampleBPMN)
	line := requireOneLine(t, sink, "auth.cross_origin_refused")
	if line["path"] != "/api/v1/deployments" {
		t.Errorf("path = %v, want the path that was refused", line["path"])
	}
	if line["origin"] != "https://elsewhere.example" {
		t.Errorf("origin = %v, want the origin the browser named", line["origin"])
	}
	if host, _ := line["host"].(string); !strings.HasPrefix(host, "127.0.0.1:") {
		t.Errorf("host = %v, want the host the request arrived for", line["host"])
	}
}
