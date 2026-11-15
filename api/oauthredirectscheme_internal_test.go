package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The scheme of a redirect URI.
//
// A redirect URI is where this server sends a person's browser: with the
// authorization code after an approval, with an error after a refusal. The consent
// page does the sending, from this server's origin, so what a browser does with the
// URI's scheme is this server's concern and not only the client's. The rule was
// always written as "https, or http on loopback"; these tests hold the code to the
// sentence, at registration and — for a record written before the rule was complete —
// at every place a stored URI is used.

// TestRedirectURIsNeedHTTPSOrLoopbackHTTP: the loopback carve-out is for plain http
// and for nothing else. A host that denotes this machine says where a request goes,
// not what the browser does with the URI, so it cannot make another scheme
// acceptable.
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
		"http://client.example.com/cb",
	}
	for _, uri := range refused {
		if got, err := validRedirectURIs([]string{uri}); err == nil {
			t.Errorf("%s was accepted as a redirect URI (%v)", uri, got)
		}
	}
}

// TestARedirectStoredUnderAnotherSchemeIsNotUsed: tightening registration does nothing
// for a record that is already on disk, and a client that registered itself is exactly
// such a record. So the rule is asked again where a stored URI is matched and where a
// redirect is built from one, and the client's other URIs keep working.
func TestARedirectStoredUnderAnotherSchemeIsNotUsed(t *testing.T) {
	const stored = "javascript://localhost/%0aalert(document.domain)//"
	c := oauthClient{RedirectURIs: []string{stored, "http://localhost:8123/cb"}}
	if c.allowsRedirect(stored) {
		t.Errorf("a client may still use %s", stored)
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

// TestNoCodeIsMintedForARedirectUnderAnotherScheme: the approval is the third place a
// redirect is built, and the one that carries a code. A request that got this far with
// such a URI is refused before the code exists, so nothing is left behind to spend.
func TestNoCodeIsMintedForARedirectUnderAnotherScheme(t *testing.T) {
	s := &Server{oauthCodes: newOAuthCodeStore()}
	p := &httpapi.Principal{UserID: "u1", Username: "alice"}

	for _, uri := range []string{
		"javascript://localhost/%0aalert(document.domain)//",
		"myapp://127.0.0.1/cb",
		"http://client.example.com/cb",
		"://not a url",
	} {
		got, err := s.issueCode(oauthRequest{redirectURI: uri, state: "abc"}, p, nil)
		if err == nil || got != "" {
			t.Errorf("issueCode for %s = %q, %v; want no redirect and an error", uri, got, err)
		}
	}
	if n := len(s.oauthCodes.byHash); n != 0 {
		t.Errorf("%d codes were minted for redirect URIs that are not used", n)
	}

	got, err := s.issueCode(oauthRequest{redirectURI: "http://localhost:8123/cb", state: "abc"}, p, nil)
	if err != nil || !strings.HasPrefix(got, "http://localhost:8123/cb?") || !strings.Contains(got, "code="+oauthCodePrefix) {
		t.Errorf("issueCode for a loopback redirect URI = %q, %v", got, err)
	}
}

// TestTheConsentPageLeavesOnlyToHTTPAndHTTPS: the page is the last thing between a
// redirect and the browser, and it runs in this server's origin with the person's
// session. It navigates in one place, and that place looks at the protocol — so an
// answer the server should never have given still cannot become script here.
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
	leave := webRegion(t, page, "function leaveTo(", "\n}")
	if !strings.Contains(leave, "location.href = ") {
		t.Error("the consent page's one navigation is not the one inside leaveTo")
	}
}
