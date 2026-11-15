package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scheme of a redirect URI, over the routes that take one and the routes that
// use one. The rule itself is pinned in oauthredirectscheme_internal_test.go; these
// are the doors it has to hold at.

// schemesNoRedirectMayHave are redirect URIs on a host that denotes this machine,
// under schemes the loopback carve-out was never meant for.
var schemesNoRedirectMayHave = []string{
	"javascript://localhost/%0aalert(document.domain)//",
	"JavaScript://127.0.0.1/%0aalert(document.domain)//",
	"data://localhost/text/html,x",
	"myapp://[::1]/cb",
}

// jsonField reads one string field out of a JSON object, and answers "" for a body
// that is not one or lacks it.
func jsonField(t *testing.T, data []byte, name string) string {
	t.Helper()
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	v, _ := doc[name].(string)
	return v
}

// TestRegistrationRefusesARedirectUnderAnotherScheme: both ways a client comes to be
// stored — an administrator entering it, and the client registering itself where an
// operator opened that — refuse it, and self-registration with the error code RFC 7591
// has for it.
func TestRegistrationRefusesARedirectUnderAnotherScheme(t *testing.T) {
	ts := newOpenRegistrationServer(t)
	admin := signedInClient(t, ts.URL)

	for _, uri := range schemesNoRedirectMayHave {
		status, out := registerDynamic(t, ts.URL, `{"client_name":"X","redirect_uris":["`+uri+`"]}`)
		if status != http.StatusBadRequest || out["error"] != "invalid_redirect_uri" {
			t.Errorf("self-registering %s = %d %v, want 400 invalid_redirect_uri", uri, status, out)
		}
		if status, data := postJSONAs(t, admin, ts.URL+"/api/v1/oauth-clients",
			map[string]any{"name": "X", "redirectUris": []string{uri}}); status != http.StatusBadRequest {
			t.Errorf("an administrator registering %s = %d: %s, want 400", uri, status, data)
		}
	}

	// What the carve-out is for keeps working at both doors.
	if status, out := registerDynamic(t, ts.URL, `{"client_name":"Desktop","redirect_uris":["http://localhost:8123/cb"]}`); status != http.StatusCreated {
		t.Errorf("self-registering a loopback redirect URI = %d %v, want 201", status, out)
	}
}

// TestAClientStoredWithAnotherSchemeIsAnsweredWithoutARedirect: a record written
// before registration refused such a URI is still on disk after the upgrade, and the
// request that names it must end on this server's page with nothing to go to — on the
// public context route the page asks, and on the approval.
func TestAClientStoredWithAnotherSchemeIsAnsweredWithoutARedirect(t *testing.T) {
	const (
		loopback = "http://localhost:8123/cb"
		stored   = "javascript://localhost/%0aalert(document.domain)//"
	)
	dir := t.TempDir()
	first := newServerOn(t, dir)
	admin := signedInClient(t, first.URL)
	status, data := postJSONAs(t, admin, first.URL+"/api/v1/oauth-clients",
		map[string]any{"name": "Old Record", "redirectUris": []string{loopback, "https://client.example.com/cb"}})
	if status != http.StatusOK {
		t.Fatalf("register = %d: %s", status, data)
	}
	clientID := jsonField(t, data, "id")
	first.stop()

	// The record as an earlier version could have written it: the same file, with the
	// loopback URI under another scheme.
	rewritten := 0
	files, _ := filepath.Glob(filepath.Join(dir, "oauth-clients", "*"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(raw), loopback) {
			continue
		}
		if err := os.WriteFile(f, []byte(strings.Replace(string(raw), loopback, stored, 1)), 0o600); err != nil {
			t.Fatalf("rewrite %s: %v", f, err)
		}
		rewritten++
	}
	if rewritten != 1 {
		t.Fatalf("rewrote %d client records, want the one just registered", rewritten)
	}

	second := newServerOn(t, dir)
	admin = signedInClient(t, second.URL)

	// A request that is the client's own mistake (no PKCE challenge) is what would be
	// answered with a redirect carrying the error.
	query := url.Values{"client_id": {clientID}, "redirect_uri": {stored}, "response_type": {"code"}, "state": {"abc"}}
	_, _, doc := getJSON(t, second.URL+"/api/v1/oauth/authorize-context?"+query.Encode())
	if doc["error"] == nil {
		t.Errorf("the context for a redirect URI under another scheme reports no error: %v", doc)
	}
	if doc["redirect"] != nil {
		t.Errorf("the context offers a redirect to %v", doc["redirect"])
	}

	// With prompt=none every outcome is a redirect — except to a URI that is not used.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	query.Set("prompt", "none")
	resp, err := noFollow.Get(second.URL + "/oauth/authorize?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /oauth/authorize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Location") != "" {
		t.Errorf("a silent request for it = %d, Location %q; want the page and no redirect",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	status, data = postJSONAs(t, admin, second.URL+"/api/v1/oauth/authorize", map[string]any{
		"clientId": clientID, "redirectUri": stored, "state": "abc",
		"codeChallenge": testChallenge(), "resource": second.URL, "approve": true,
	})
	if status != http.StatusBadRequest || strings.Contains(string(data), "redirect\"") {
		t.Errorf("approving it = %d: %s, want 400 and no redirect", status, data)
	}

	// The client's other redirect URI is untouched by the one that is not used.
	status, data = postJSONAs(t, admin, second.URL+"/api/v1/oauth/authorize", map[string]any{
		"clientId": clientID, "redirectUri": "https://client.example.com/cb", "state": "abc",
		"codeChallenge": testChallenge(), "resource": second.URL, "approve": true,
	})
	if status != http.StatusOK || !strings.HasPrefix(jsonField(t, data, "redirect"), "https://client.example.com/cb?") {
		t.Errorf("approving the client's https redirect URI = %d: %s", status, data)
	}
}

// TestTheConsentPageRefusesToBeFramed: the page asks a signed-in person to approve an
// application with one click, so another site must not be able to show it inside a
// frame of its own and lay its controls over that button.
func TestTheConsentPageRefusesToBeFramed(t *testing.T) {
	ts := newMCPServer(t)

	resp, err := http.Get(ts.URL + "/oauth/authorize?client_id=whatever")
	if err != nil {
		t.Fatalf("GET /oauth/authorize: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}
