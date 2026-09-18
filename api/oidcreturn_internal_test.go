package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A federated login lands where it started
// (ADR-draft-portal-carries-its-own-sign-in).
//
// The callback has always redirected to "/", which was right while the Console's
// login screen was the only place a federated login could begin. It is not any
// more: the service portal carries its own sign-in, and its readers hold `user`
// and no Console role — so landing them on the Console is landing them in a shell
// whose every entry is missing, one click after they asked to order a laptop.
//
// The page travels as a request parameter through the browser, which is exactly
// what makes an open redirect, so it is an allowlist and not a validated path:
// two surfaces can start a federated login, and naming them is both sufficient
// and the whole of the surface.

// TestAFederatedLoginReturnsToThePageItStartedFrom.
func TestAFederatedLoginReturnsToThePageItStartedFrom(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath + "?returnTo=" + url.QueryEscape(portalPage))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer start.Body.Close()
	code, state := idp.authorize(t, start.Header.Get("Location"))
	resp, err := c.Get(ts.URL + oidcCallbackPath + "?code=" + code + "&state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Location"); got != portalPage {
		t.Errorf("landed on %q, want %q — a portal visitor who signs in belongs on the "+
			"portal, not in a console they hold no role for", got, portalPage)
	}
}

// TestAFederatedLoginStillLandsOnTheConsoleByDefault. The Console's own sign-in
// passes nothing, and nothing is what it has always meant.
func TestAFederatedLoginStillLandsOnTheConsoleByDefault(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	resp := federate(t, ts, idp, c)
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("landed on %q, want /", got)
	}
}

// TestAFailedFederatedLoginReturnsToThePageItStartedFrom. The failure has to come
// back to the same screen as the success, or a portal visitor whose provider
// refused them is told so on a page they never opened.
func TestAFailedFederatedLoginReturnsToThePageItStartedFrom(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath + "?returnTo=" + url.QueryEscape(portalPage))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer start.Body.Close()
	_, state := idp.authorize(t, start.Header.Get("Location"))

	// No code: the provider came back with nothing to exchange.
	resp, err := c.Get(ts.URL + oidcCallbackPath + "?state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != portalPage+oidcFailedQuery {
		t.Errorf("a refused login landed on %q, want %q", got, portalPage+oidcFailedQuery)
	}
}

// TestTheReturnPageIsAnAllowlist.
//
// The value reaches the server through the browser, so anything that could
// express an arbitrary destination would be an open redirect carrying a login's
// authority: a link that signs somebody in and drops them, session and all, on a
// page somebody else chose. Two pages can start a federated login and no third
// value is a page.
func TestTheReturnPageIsAnAllowlist(t *testing.T) {
	for _, raw := range []string{
		"https://evil.example/steal",
		"//evil.example/steal",
		"/index.html\n/evil",
		"/../portal.html",
		"/portal.html?next=https://evil.example",
		"javascript:alert(1)",
		"",
		"/some/other/page",
	} {
		if got := oidcReturnPage(raw); got != "/" {
			t.Errorf("oidcReturnPage(%q) = %q, want / — only a page that may start a "+
				"federated login may end one", raw, got)
		}
	}
	if got := oidcReturnPage(portalPage); got != portalPage {
		t.Errorf("oidcReturnPage(%q) = %q, want it through", portalPage, got)
	}
	if got := oidcReturnPage("/"); got != "/" {
		t.Errorf("oidcReturnPage(/) = %q, want /", got)
	}
}

// TestAForgedReturnCookieCannotChooseTheDestination.
//
// The cookie is the server's own, but a browser is not a safe place to keep
// anything, and the value is read back on a request anybody can shape. It goes
// through the same allowlist on the way out as on the way in, so a cookie planted
// by hand redirects nowhere new.
func TestAForgedReturnCookieCannotChooseTheDestination(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	start, err := c.Get(ts.URL + oidcStartPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer start.Body.Close()
	code, state := idp.authorize(t, start.Header.Get("Location"))

	req, err := http.NewRequest(http.MethodGet,
		ts.URL+oidcCallbackPath+"?code="+code+"&state="+state, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// The forged one has to be the *only* cookie of its name, or the jar's own —
	// set by the start with no page to return to — is what the server reads first
	// and the test proves nothing.
	for _, ck := range c.Jar.Cookies(req.URL) {
		if ck.Name != oidcReturnCookie {
			req.AddCookie(ck)
		}
	}
	req.AddCookie(&http.Cookie{Name: oidcReturnCookie, Value: "https://evil.example/steal"})
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("a planted cookie sent the login to %q, want /", got)
	}
}

// TestTheStartRecordsWhereToReturnInTheBrowser. The cookie is scoped to the
// callback and expires with the login window, like the state it travels beside:
// a return page outliving its login is a value nothing will ever spend.
func TestTheStartRecordsWhereToReturnInTheBrowser(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	c := noRedirects(t)

	resp, err := c.Get(ts.URL + oidcStartPath + "?returnTo=" + url.QueryEscape(portalPage))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	var got *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == oidcReturnCookie {
			got = ck
		}
	}
	if got == nil {
		t.Fatal("the start set no return cookie, so the callback has nothing to read")
	}
	if got.Value != portalPage {
		t.Errorf("return cookie = %q, want %q", got.Value, portalPage)
	}
	if got.Path != oidcCallbackPath {
		t.Errorf("return cookie path = %q, want it scoped to the callback that reads it", got.Path)
	}
	if !got.HttpOnly {
		t.Error("the return cookie is readable by script, which is a value a page can rewrite")
	}
}

// TestThePortalStartsItsFederatedLoginWithItsOwnPage. The server half is useless
// unless the portal asks for it, and the Console must keep asking for nothing.
func TestThePortalStartsItsFederatedLoginWithItsOwnPage(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function renderSignIn(", "\n}")
	// The value and not the word: "returnTo" is in the comment that explains this,
	// so a guard looking for the word passes over a link that stopped carrying it.
	if !strings.Contains(body, "returnTo=${encodeURIComponent(location.pathname)}") {
		t.Error("the portal's provider link does not say where to come back to, so a " +
			"federated sign-in lands in the Console")
	}
}
