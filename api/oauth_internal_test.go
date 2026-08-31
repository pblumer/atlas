package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"
)

// The pieces the authorization flow is built from, pinned where an HTTP round trip
// can only show them indirectly (ADR-0200): what a code does when replayed or
// stale, what PKCE accepts, what a rotated grant leaves behind, and which of these
// refuse a credential of the wrong kind rather than hashing it and comparing.

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// TestVerifyPKCE covers the shape of the check, including the two empty cases:
// a missing challenge or verifier must be a refusal, never a match, or a client
// that sent neither would be treated as one that proved something.
func TestVerifyPKCE(t *testing.T) {
	const verifier = "a-verifier-that-is-plausibly-long-0123456789"
	challenge := challengeFor(verifier)

	if !verifyPKCE(challenge, verifier) {
		t.Error("the matching pair was refused")
	}
	for _, tc := range []struct{ name, challenge, verifier string }{
		{"a different verifier", challenge, "something-else-entirely-0123456789012345"},
		{"an empty verifier", challenge, ""},
		{"an empty challenge", "", verifier},
		{"both empty", "", ""},
		// "plain" is not accepted: a challenge equal to its verifier must not pass.
		{"plain-style", verifier, verifier},
	} {
		if verifyPKCE(tc.challenge, tc.verifier) {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// TestAuthorizationCodeIsSingleUseAndExpires: the two properties that make a code
// safe to put in a browser redirect.
func TestAuthorizationCodeIsSingleUseAndExpires(t *testing.T) {
	const secret = oauthCodePrefix + "abc123"
	now := time.Unix(1_000_000, 0)
	store := newOAuthCodeStore()
	store.issue(oauthCode{hash: hashAPIToken(secret), clientID: "c1", expires: now.Add(time.Minute)})

	if _, ok := store.spend(secret, now); !ok {
		t.Fatal("a fresh code was refused")
	}
	if _, ok := store.spend(secret, now); ok {
		t.Error("the code was spendable twice — a replayed exchange would succeed")
	}

	// A code past its expiry is refused, and is consumed on the way: leaving it in
	// the map would let it be probed for indefinitely.
	store.issue(oauthCode{hash: hashAPIToken(secret), expires: now.Add(time.Minute)})
	if _, ok := store.spend(secret, now.Add(2*time.Minute)); ok {
		t.Error("an expired code was accepted")
	}
	if _, ok := store.spend(secret, now); ok {
		t.Error("an expired code survived being presented")
	}
}

// TestOAuthCodeStoreSweepsWhatExpired: abandoned flows must not accumulate.
func TestOAuthCodeStoreSweepsWhatExpired(t *testing.T) {
	now := time.Now()
	store := newOAuthCodeStore()
	for i := 0; i < 5; i++ {
		store.issue(oauthCode{hash: hashAPIToken(oauthCodePrefix + string(rune('a'+i))), expires: now.Add(-time.Hour)})
	}
	// Issuing sweeps, so only the fresh one is left behind.
	store.issue(oauthCode{hash: hashAPIToken(oauthCodePrefix + "fresh"), expires: now.Add(time.Minute)})
	if got := len(store.byHash); got != 1 {
		t.Errorf("%d codes retained, want 1 — expired codes are not being swept", got)
	}
}

// TestOAuthGrantScopeFollowsTheResource: what the person approved decides what the
// token reaches. This is the audience made into something enforced rather than
// merely recorded.
func TestOAuthGrantScopeFollowsTheResource(t *testing.T) {
	if got := (oauthGrant{Resource: "https://atlas.example.com/mcp"}).scope(); got != apiScopeMCP {
		t.Errorf("an /mcp grant scoped %q, want %q", got, apiScopeMCP)
	}
	if got := (oauthGrant{Resource: "https://atlas.example.com"}).scope(); got != "" {
		t.Errorf("a whole-server grant scoped %q, want unconfined", got)
	}

	// And the scope actually confines: the mcp scope reaches the transport and not
	// the API. apiScopeMayReach is the one check every confined credential goes
	// through, so this is the same enforcement deploy and worker tokens get.
	if !apiScopeMayReach(apiScopeMCP, httptest.NewRequest("POST", "/mcp", nil)) {
		t.Error("the mcp scope cannot reach /mcp")
	}
	if apiScopeMayReach(apiScopeMCP, httptest.NewRequest("GET", "/api/v1/processes", nil)) {
		t.Error("the mcp scope reached the API surface")
	}
}

// TestOAuthGrantIndexRotationDropsTheOldHashes is what makes refresh-token
// rotation mean anything: after a rotation the previous pair must resolve to
// nothing, not merely be superseded.
func TestOAuthGrantIndexRotationDropsTheOldHashes(t *testing.T) {
	const (
		access1  = oauthAccessPrefix + "a1"
		refresh1 = oauthRefreshPrefix + "r1"
		access2  = oauthAccessPrefix + "a2"
		refresh2 = oauthRefreshPrefix + "r2"
	)
	idx := newOAuthGrantIndex()
	idx.put(oauthGrant{ID: "g1", UserID: "u1", AccessHash: hashAPIToken(access1), RefreshHash: hashAPIToken(refresh1)})
	if _, ok := idx.matchAccess(access1, 0); !ok {
		t.Fatal("the first access token did not resolve")
	}

	idx.put(oauthGrant{ID: "g1", UserID: "u1", AccessHash: hashAPIToken(access2), RefreshHash: hashAPIToken(refresh2)})
	if _, ok := idx.matchAccess(access1, 0); ok {
		t.Error("the superseded access token still resolves")
	}
	if _, ok := idx.matchRefresh(refresh1); ok {
		t.Error("the superseded refresh token still resolves — rotation would be cosmetic")
	}
	if _, ok := idx.matchRefresh(refresh2); !ok {
		t.Error("the new refresh token does not resolve")
	}

	if got := idx.forUser("u1"); len(got) != 1 || got[0] != "g1" {
		t.Errorf("forUser = %v, want one entry for g1 — a rotated grant is still one grant", got)
	}
	idx.remove("g1")
	if _, ok := idx.matchAccess(access2, 0); ok {
		t.Error("a removed grant still resolves")
	}
}

// TestOAuthGrantIndexRefusesForeignAndExpiredTokens: each scheme must decline a
// credential that is not its own so the next one gets a turn, and an access token
// past its expiry must be as good as unknown.
func TestOAuthGrantIndexRefusesForeignAndExpiredTokens(t *testing.T) {
	const (
		access  = oauthAccessPrefix + "a1"
		refresh = oauthRefreshPrefix + "r1"
	)
	idx := newOAuthGrantIndex()
	idx.put(oauthGrant{
		ID: "g1", AccessHash: hashAPIToken(access), RefreshHash: hashAPIToken(refresh),
		AccessExpires: 1_000,
	})

	if _, ok := idx.matchAccess(access, 999); !ok {
		t.Error("a token inside its lifetime was refused")
	}
	// At the expiry second, not after it — a token that expires "at" a time is not
	// valid at that time.
	if _, ok := idx.matchAccess(access, 1_000); ok {
		t.Error("an access token was accepted at the second it expires")
	}

	// A refresh token has no expiry of its own: a grant ends by revocation.
	if _, ok := idx.matchRefresh(refresh); !ok {
		t.Error("the refresh token was refused while the access token was expired")
	}

	// Each scheme declines what is not its own, so the next one in principalFor
	// still gets a turn. Presenting a refresh token where an access token belongs is
	// refused by prefix rather than by hash mismatch.
	for _, tc := range []struct{ name, secret string }{
		{"a refresh token", refresh},
		{"an API token", apiTokenPrefix + "abc"},
		{"a client secret", oauthClientSecretPrefix + "abc"},
		{"a bare string", "abc"},
		{"nothing", ""},
	} {
		if _, ok := idx.matchAccess(tc.secret, 0); ok {
			t.Errorf("%s resolved as an access token", tc.name)
		}
	}
	if _, ok := idx.matchRefresh(access); ok {
		t.Error("an access token resolved as a refresh token")
	}
}

// TestOAuthClientAuthentication: the client id is public, the secret is not, and a
// wrong secret must be indistinguishable from an unregistered client.
func TestOAuthClientAuthentication(t *testing.T) {
	const secret = oauthClientSecretPrefix + "s3cret"
	idx := newOAuthClientIndex()
	idx.add(oauthClient{ID: "c1", Name: "Test", SecretHash: hashAPIToken(secret),
		RedirectURIs: []string{"https://client.example.com/cb"}})

	if _, ok := idx.authenticate("c1", secret); !ok {
		t.Error("the right secret was refused")
	}
	for _, tc := range []struct{ name, id, secret string }{
		{"a wrong secret", "c1", oauthClientSecretPrefix + "nope"},
		{"an unknown client", "c2", secret},
		// A secret of another kind must fall through rather than be hashed and
		// compared, so presenting an API token here is refused as what it is.
		{"an API token presented as a client secret", "c1", apiTokenPrefix + "abc"},
		{"nothing at all", "c1", ""},
	} {
		if _, ok := idx.authenticate(tc.id, tc.secret); ok {
			t.Errorf("%s authenticated", tc.name)
		}
	}

	// Redirect matching is exact. A prefix match is how an open redirector is built.
	c, _ := idx.lookup("c1")
	if !c.allowsRedirect("https://client.example.com/cb") {
		t.Error("the registered redirect was rejected")
	}
	for _, uri := range []string{
		"https://client.example.com/cb/evil",
		"https://client.example.com/cb?x=1",
		"https://client.example.com",
		"https://evil.example.com/cb",
	} {
		if c.allowsRedirect(uri) {
			t.Errorf("%q was accepted as the registered redirect", uri)
		}
	}

	idx.remove("c1")
	if _, ok := idx.lookup("c1"); ok {
		t.Error("a removed client still resolves")
	}
}

// TestExternalBaseDerivesTheOrigin covers what the discovery documents and the
// challenge are built from when no origin was configured.
//
// The scheme matters more than it looks. Behind a proxy — which is still where
// most certificates live, and was the only place before ADR-0191 — every request
// arrives as plain http, so without X-Forwarded-Proto the documents would name
// http:// URLs that no client can use. Where the server holds the certificate
// itself, r.TLS answers the same question directly.
func TestExternalBaseDerivesTheOrigin(t *testing.T) {
	s := &Server{}

	plain := httptest.NewRequest("GET", "http://atlas.example.com/x", nil)
	if got := s.externalBase(plain); got != "http://atlas.example.com" {
		t.Errorf("plain request = %q", got)
	}

	fwd := httptest.NewRequest("GET", "http://atlas.example.com/x", nil)
	fwd.Header.Set("X-Forwarded-Proto", "https")
	if got := s.externalBase(fwd); got != "https://atlas.example.com" {
		t.Errorf("behind a TLS proxy = %q, want https", got)
	}

	// A proxy chain sends a list; the client-facing hop is the first entry.
	chain := httptest.NewRequest("GET", "http://atlas.example.com/x", nil)
	chain.Header.Set("X-Forwarded-Proto", "https, http")
	if got := s.externalBase(chain); got != "https://atlas.example.com" {
		t.Errorf("proxy chain = %q, want the first hop's scheme", got)
	}

	// Anything that is not a scheme is ignored rather than interpolated into a URL.
	junk := httptest.NewRequest("GET", "http://atlas.example.com/x", nil)
	junk.Header.Set("X-Forwarded-Proto", "javascript:")
	if got := s.externalBase(junk); got != "http://atlas.example.com" {
		t.Errorf("a junk scheme = %q, want it ignored", got)
	}

	// A configured origin wins over all of it, which is why an operator behind a
	// proxy should set one.
	configured := &Server{externalURL: "https://atlas.example.com"}
	if got := configured.externalBase(junk); got != "https://atlas.example.com" {
		t.Errorf("configured origin = %q", got)
	}

	// No Host at all means no absolute URL can be built, and every caller treats an
	// empty answer as "say nothing" rather than emitting something wrong.
	hostless := httptest.NewRequest("GET", "/x", nil)
	hostless.Host = ""
	if got := s.externalBase(hostless); got != "" {
		t.Errorf("a request with no Host = %q, want empty", got)
	}
	if got := s.resourceMetadataURL(hostless); got != "" {
		t.Errorf("resourceMetadataURL with no origin = %q, want empty", got)
	}
	if s.isCanonicalResource(hostless, "https://atlas.example.com") {
		t.Error("a resource was accepted against no origin at all")
	}
}
