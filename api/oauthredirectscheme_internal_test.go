package api

import (
	"strings"
	"testing"
)

// TestRedirectURIsNeedHTTPSOrLoopbackHTTP holds the rule to what its own refusal has
// always said: "must be https, or http on loopback". The check behind it asked only
// that a URI off this machine be https, so on localhost, 127.0.0.1 and [::1] every
// scheme passed — a script-capable one among them. The consent page sends the browser
// to a registered redirect URI by itself, a second and a half after it has said that
// a request cannot be approved, so with self-registration open such a URI was a
// stranger's script running in this origin for whoever opened their link.
func TestRedirectURIsNeedHTTPSOrLoopbackHTTP(t *testing.T) {
	accepted := []string{
		"https://client.example.com/cb",
		"https://localhost/cb",
		"http://localhost:8123/cb",
		"http://127.0.0.1/cb",
		"http://[::1]:5000/cb",
	}
	for _, uri := range accepted {
		if _, err := validRedirectURIs([]string{uri}); err != nil {
			t.Errorf("%s was refused: %v", uri, err)
		}
	}
	refused := []string{
		"javascript://localhost/%0aalert(document.domain)//",
		"javascript://127.0.0.1/%0aalert(document.domain)//",
		"JavaScript://[::1]/%0aalert(document.domain)//",
		"data://localhost/text/html,x",
		"ftp://localhost/cb",
		"myapp://127.0.0.1/cb",
		// Refused before this rule too; here so the list says the whole of it.
		"http://client.example.com/cb",
	}
	for _, uri := range refused {
		if got, err := validRedirectURIs([]string{uri}); err == nil {
			t.Errorf("%s was accepted as a redirect URI (%v); only https, or http to this machine, may be one", uri, got)
		}
	}
}

// TestARedirectStoredUnderAnotherSchemeIsNotUsed: a client record written before the
// rule keeps what it registered, and no restart rewrites it. So the rule is asked
// again where a request names its redirect URI and where an answer is built for one —
// a stored URI of another scheme is not one the client may use, and nothing is sent
// to it.
func TestARedirectStoredUnderAnotherSchemeIsNotUsed(t *testing.T) {
	const stored = "javascript://localhost/%0aalert(document.domain)//"
	c := oauthClient{RedirectURIs: []string{stored, "http://localhost:8123/cb"}}
	if c.allowsRedirect(stored) {
		t.Errorf("a client may still use %s, which it registered before the rule", stored)
	}
	if !c.allowsRedirect("http://localhost:8123/cb") {
		t.Error("the client's loopback redirect URI stopped working")
	}
	if got := errorRedirect(stored, "invalid_request", "abc"); got != "" {
		t.Errorf("an error answer was built for %s: %q", stored, got)
	}
	if got := errorRedirect("http://localhost:8123/cb", "invalid_request", "abc"); !strings.HasPrefix(got, "http://localhost:8123/cb?") {
		t.Errorf("the error answer for a loopback redirect URI = %q", got)
	}
}

// TestTheConsentPageLeavesOnlyToHTTPAndHTTPS: the page is the last step, and it used
// to hand the browser whatever the server answered. It now asks the same question
// once more, in one function, so that a URI which got past the server is still not
// given to the browser as a script.
func TestTheConsentPageLeavesOnlyToHTTPAndHTTPS(t *testing.T) {
	page := readWeb(t, "oauth-consent.html")
	if n := strings.Count(page, "location.href = "); n != 1 {
		t.Errorf("the consent page assigns location.href in %d places; want one, inside leaveTo", n)
	}
	for _, want := range []string{"function leaveTo(", `u.protocol === "https:"`, `u.protocol === "http:"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the consent page lacks %q", want)
		}
	}
}
