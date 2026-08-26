package api

import (
	"net/http/httptest"
	"testing"
)

// The parts of an API token whose behaviour depends on the clock or on a record
// this binary did not write — neither of which an HTTP test can produce.

// TestAPITokenIndexRefusesAnExpiredToken: a lifetime that has run out is refused
// exactly like an unknown credential. The record is deliberately left in the index
// rather than dropped, so a listing still shows an operator what needs reissuing.
func TestAPITokenIndexRefusesAnExpiredToken(t *testing.T) {
	const secret = apiTokenPrefix + "abc123"
	idx := newAPITokenIndex()
	idx.add(apiToken{ID: "t1", Name: "stale", Hash: hashAPIToken(secret), ExpiresAt: 1_000})

	if _, ok := idx.match(secret, 999); !ok {
		t.Error("a token inside its lifetime was refused")
	}
	// At the expiry second, not after it: a token that expires "at" a time is not
	// valid at that time.
	if _, ok := idx.match(secret, 1_000); ok {
		t.Error("a token was accepted at the second it expires")
	}
	if _, ok := idx.match(secret, 10_000); ok {
		t.Error("an expired token was accepted")
	}
}

// TestAPITokenIndexIgnoresForeignSecrets: a deploy token, a session cookie value or
// anything else presented as a bearer must fall through to the next scheme rather
// than be hashed and compared here.
func TestAPITokenIndexIgnoresForeignSecrets(t *testing.T) {
	idx := newAPITokenIndex()
	idx.add(apiToken{ID: "t1", Hash: hashAPIToken(apiTokenPrefix + "abc")})

	for _, secret := range []string{"", "atlasdt_abc", "abc", apiTokenPrefix} {
		if _, ok := idx.match(secret, 0); ok {
			t.Errorf("%q was matched as an API token", secret)
		}
	}
}

// TestAPITokenRemovedFromIndexStopsMatching covers revocation at the index level.
func TestAPITokenRemovedFromIndexStopsMatching(t *testing.T) {
	const secret = apiTokenPrefix + "abc123"
	idx := newAPITokenIndex()
	idx.add(apiToken{ID: "t1", Hash: hashAPIToken(secret)})
	if _, ok := idx.match(secret, 0); !ok {
		t.Fatal("the token did not match before revocation")
	}
	idx.remove("t1")
	if _, ok := idx.match(secret, 0); ok {
		t.Error("a revoked token still matches")
	}
}

// TestAPITokenScopeDefaultsToFull: a record written before scopes existed, or one
// whose scope field is absent for any other reason, keeps working rather than
// silently becoming a credential that can do nothing.
func TestAPITokenScopeDefaultsToFull(t *testing.T) {
	if got := (apiToken{}).scope(); got != apiScopeFull {
		t.Errorf("an empty scope resolved to %q, want %q", got, apiScopeFull)
	}
}

// TestUnknownScopeReachesNothing is the fail-closed direction, and it is the case
// that cannot be produced through the API: a record naming a scope this binary does
// not know — written by a newer version, or hand-edited — must be inert rather than
// unconfined.
func TestUnknownScopeReachesNothing(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/processes", nil)
	if apiScopeMayReach("a-scope-from-the-future", r) {
		t.Error("an unknown scope reached an endpoint; it must reach nothing")
	}
	// And the one unconfined scope stays unconfined, so the check above is not
	// simply refusing everything.
	if !apiScopeMayReach(apiScopeFull, r) {
		t.Error("the full scope was confined")
	}
}

// TestEveryScopePatternIsARegisteredRoute catches the failure mode an allowlist of
// strings has: an entry that matches nothing. A typo there is silent — the
// operation it meant to permit stays refused while the list reads as if it were
// allowed — so the entries are checked against the route table itself.
func TestEveryScopePatternIsARegisteredRoute(t *testing.T) {
	registered := map[string]bool{}
	for _, r := range accessTestServer(t).apiRoutes() {
		registered[r.method+" "+r.pattern] = true
	}
	for scope, patterns := range apiScopeAllowed {
		for _, pattern := range patterns {
			if !registered[pattern] {
				t.Errorf("scope %q allows %q, which the route table does not register", scope, pattern)
			}
		}
	}
}

// TestMintableScopesAreAllKnown: every scope an operator may ask for must be one
// the enforcement understands, or minting would hand out a credential that reaches
// nothing.
func TestMintableScopesAreAllKnown(t *testing.T) {
	for _, scope := range apiMintableScopes {
		if !validAPIScope(scope) {
			t.Errorf("scope %q is offered but not valid", scope)
		}
		if scope == apiScopeFull {
			continue
		}
		if _, ok := apiScopeAllowed[scope]; !ok {
			t.Errorf("confined scope %q has no allowlist, so it reaches nothing", scope)
		}
	}
	// The deploy scope belongs to a credential with its own store; nothing may mint
	// one here.
	if validAPIScope(apiScopeDeploy) {
		t.Error("the deploy scope must not be mintable as an API token")
	}
}
