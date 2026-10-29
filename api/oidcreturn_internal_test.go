package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A federated login lands where it started
// (ADR-0399).
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
		"/../shop.html",
		"/shop.html?next=https://evil.example",
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
	body := webRegion(t, readWeb(t, "shop.js"), "function renderSignIn(", "\n}")
	// The value and not the word: "returnTo" is in the comment that explains this,
	// so a guard looking for the word passes over a link that stopped carrying it.
	if !strings.Contains(body, "returnTo=${encodeURIComponent(location.pathname)}") {
		t.Error("the portal's provider link does not say where to come back to, so a " +
			"federated sign-in lands in the Console")
	}
}

// The consent page is the third page that can start a federated login (ADR-0510).
//
// A person who signs in only through their identity provider has no password to type
// on the consent page, so without a way back to that very request there is nothing
// left for them to approve — the connected system asks, and they can never answer.
// The page is admitted with its query, and only on terms that keep it a page of this
// server: exactly its path, the query parsed and written back, and a bound on the
// length.

// consentQuery is an authorization request as a client builds it.
func consentQuery() url.Values {
	return url.Values{
		"client_id":             {"3f2a9c"},
		"redirect_uri":          {"https://wiki.example/oauth/callback?from=atlas"},
		"response_type":         {"code"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"resource":              {"https://atlas.example/tasks"},
		"scope":                 {"tasks"},
		"state":                 {"a state with spaces & an ampersand"},
	}
}

// TestTheConsentPageIsAReturnTargetWithItsQueryReencoded.
func TestTheConsentPageIsAReturnTargetWithItsQueryReencoded(t *testing.T) {
	q := consentQuery()
	want := oauthAuthorizePath + "?" + q.Encode()
	if got := oidcReturnPage(oauthAuthorizePath + "?" + q.Encode()); got != want {
		t.Errorf("the consent request came back as %q, want %q", got, want)
	}

	// Written the way a browser may send it — unescaped, in another order — it comes
	// out in the one spelling Encode gives, with every value escaped.
	raw := oauthAuthorizePath + "?state=<b>x</b>&client_id=3f2a9c&redirect_uri=https://wiki.example/cb"
	wantRaw := oauthAuthorizePath + "?" + url.Values{
		"client_id": {"3f2a9c"}, "redirect_uri": {"https://wiki.example/cb"}, "state": {"<b>x</b>"},
	}.Encode()
	if got := oidcReturnPage(raw); got != wantRaw {
		t.Errorf("oidcReturnPage(%q) = %q, want it re-encoded as %q", raw, got, wantRaw)
	}
	if strings.ContainsAny(oidcReturnPage(raw), "<>") {
		t.Error("a value came back unescaped; the query has to be written anew, not passed through")
	}

	// The page with no query at all is still the page.
	if got := oidcReturnPage(oauthAuthorizePath); got != oauthAuthorizePath {
		t.Errorf("oidcReturnPage(%q) = %q, want it through", oauthAuthorizePath, got)
	}

	// The server's own failure marker does not survive the trip, or a second attempt
	// that worked would come back saying the first one failed.
	marked := oauthAuthorizePath + "?client_id=3f2a9c&sso=failed"
	if got := oidcReturnPage(marked); got != oauthAuthorizePath+"?client_id=3f2a9c" {
		t.Errorf("oidcReturnPage(%q) = %q, want the sso marker dropped", marked, got)
	}

	// Whatever goes in, what comes out is this path on this origin.
	for _, raw := range []string{
		oauthAuthorizePath + "?redirect_uri=//evil.example/x",
		oauthAuthorizePath + "?next=https://evil.example/&x=%2F%2Fevil.example",
	} {
		got := oidcReturnPage(raw)
		if !strings.HasPrefix(got, oauthAuthorizePath+"?") || strings.Contains(got, "//") {
			t.Errorf("oidcReturnPage(%q) = %q, want the authorize path with an escaped query", raw, got)
		}
	}
}

// TestOnlyTheConsentPathCarriesAQuery. Everything that is not exactly the consent
// page's path — another path, another host, a scheme, a backslash a browser reads as
// a slash, a fragment, a header split, a query that does not parse, or one too long
// to carry — lands on the Console, as an unknown page always has.
func TestOnlyTheConsentPathCarriesAQuery(t *testing.T) {
	long := oauthAuthorizePath + "?state=" + strings.Repeat("a", oidcReturnMax)
	// Short enough going in, too long once every one of its characters is escaped.
	grows := oauthAuthorizePath + "?state=" + strings.Repeat("<", oidcReturnMax/3+1)
	for _, raw := range []string{
		"//evil.example" + oauthAuthorizePath + "?client_id=x",
		"https://evil.example" + oauthAuthorizePath + "?client_id=x",
		"https:" + oauthAuthorizePath + "?client_id=x",
		"javascript:alert(1)//" + oauthAuthorizePath,
		`\\evil.example` + oauthAuthorizePath + "?client_id=x",
		`/\evil.example` + oauthAuthorizePath,
		oauthAuthorizePath + `\..\evil?client_id=x`,
		oauthAuthorizePath + `?client_id=x\y`,
		oauthAuthorizePath + "/../evil?client_id=x",
		oauthAuthorizePath + "/?client_id=x",
		"/oauth//authorize?client_id=x",
		"/OAUTH/AUTHORIZE?client_id=x",
		oauthAuthorizePath + "x?client_id=x",
		oauthAuthorizePath + "%3Fclient_id=x",
		" " + oauthAuthorizePath + "?client_id=x",
		oauthAuthorizePath + "?client_id=x#https://evil.example",
		oauthAuthorizePath + "?client_id=x\r\nSet-Cookie: a=b",
		oauthAuthorizePath + "?client_id=%zz",
		oauthAuthorizePath + "?client_id=x;state=y",
		long,
		grows,
	} {
		if got := oidcReturnPage(raw); got != "/" {
			t.Errorf("oidcReturnPage(%q) = %q, want / — only the consent page itself may carry a query back", raw, got)
		}
	}
}

// TestAFederatedLoginReturnsToTheConsentRequest is the whole flow: the consent page
// starts the login with its own request, and the callback lands on that request,
// re-encoded, both when the login works and when it does not.
func TestAFederatedLoginReturnsToTheConsentRequest(t *testing.T) {
	idp := newFakeIdP(t)
	ts, _ := oidcServer(t, idp)
	q := consentQuery()
	want := oauthAuthorizePath + "?" + q.Encode()

	t.Run("signed in", func(t *testing.T) {
		c := noRedirects(t)
		start, err := c.Get(ts.URL + oidcStartPath + "?returnTo=" + url.QueryEscape(want))
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
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("landed on %q, want the consent request %q — a person who signs in "+
				"through their provider has to come back to the question they were asked", got, want)
		}
	})

	t.Run("refused", func(t *testing.T) {
		c := noRedirects(t)
		start, err := c.Get(ts.URL + oidcStartPath + "?returnTo=" + url.QueryEscape(want))
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		defer start.Body.Close()
		_, state := idp.authorize(t, start.Header.Get("Location"))
		resp, err := c.Get(ts.URL + oidcCallbackPath + "?state=" + state) // no code
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		defer resp.Body.Close()
		got := resp.Header.Get("Location")
		if got != want+"&sso=failed" {
			t.Errorf("a refused login landed on %q, want %q", got, want+"&sso=failed")
		}
		u, err := url.Parse(got)
		if err != nil || u.Path != oauthAuthorizePath || u.Query().Get("client_id") != q.Get("client_id") ||
			u.Query().Get("sso") != "failed" {
			t.Errorf("the failure marker broke the request it joined: %q", got)
		}
	})
}

// TestTheConsentPageOffersTheProvidersAndComesBack. The server half is useless unless
// the consent page asks for it: it has to offer the providers the Console's sign-in
// offers, and start each with its own request as the page to come back to.
func TestTheConsentPageOffersTheProvidersAndComesBack(t *testing.T) {
	page := readWeb(t, "oauth-consent.html")

	signIn := webRegion(t, page, "function signIn(", "\n}")
	if !strings.Contains(signIn, "offerProviders()") {
		t.Error("the consent page's sign-in no longer offers the identity providers, so a person " +
			"without a password here cannot reach the question")
	}
	if !strings.Contains(signIn, `q.get("sso") === "failed"`) {
		t.Error("the consent page does not say when a federated sign-in failed")
	}

	offer := webRegion(t, page, "async function offerProviders(", "\n}")
	for _, want := range []string{
		`"/api/v1/auth/providers"`,
		"providers.map(",
		"encodeURIComponent(consentReturn())",
		"returnTo=${back}",
	} {
		if !strings.Contains(offer, want) {
			t.Errorf("offerProviders no longer contains %q: the provider links do not start from, "+
				"or come back to, this request", want)
		}
	}

	back := webRegion(t, page, "function consentReturn(", "\n}")
	for _, want := range []string{"location.search", "location.pathname", `back.delete("sso")`} {
		if !strings.Contains(back, want) {
			t.Errorf("consentReturn no longer contains %q: it has to name this page and its query, "+
				"without the failure marker", want)
		}
	}
}
