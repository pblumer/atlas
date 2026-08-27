package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"
)

// Authorization codes (ADR-0200).
//
// A code is the one-use ticket a person's approval turns into, which the client
// then exchanges for tokens. It lives about a minute and is spent on first use, so
// unlike clients and grants it is held in memory only: losing them on a restart
// costs a client one retry, and writing them to disk would put a bearer-equivalent
// secret in the data directory for the sake of a value that is worthless a minute
// later.
//
// Everything a code carries is fixed at approval time and re-checked at exchange,
// because the exchange arrives on a different connection from a different party:
//
//   - the client it was issued to, so a code leaked to another client is useless;
//   - the redirect URI it was issued for, which RFC 6749 requires be identical at
//     the token endpoint;
//   - the PKCE challenge, so only the party that started the flow can finish it —
//     the whole point of PKCE being that a code intercepted in a browser redirect
//     cannot be spent by whoever intercepted it;
//   - the resource, so what the person approved is what the token is minted for.

// oauthCodeLifetime is how long a code may be exchanged for. RFC 6749 says a
// maximum of ten minutes and recommends one; a code travels from the person's
// browser to the client's server, which is fast, so this is the short end.
const oauthCodeLifetime = 60 * time.Second

// oauthCode is one issued authorization code.
type oauthCode struct {
	hash     string // sha256 of the code, so the store holds nothing spendable
	clientID string

	// The person, captured at approval, with what they could do at that moment.
	// The grant carries this forward; see oauthGrant for why it is a snapshot and
	// what keeps it current.
	userID   string
	username string
	roles    []string
	groupIDs []string

	redirectURI string
	challenge   string // the PKCE code_challenge, S256
	resource    string
	expires     time.Time
}

// oauthCodeStore holds unspent codes. Touched from handler goroutines, so it
// guards itself; never persisted, so it sits outside the single-writer invariant
// exactly as sessionStore does.
type oauthCodeStore struct {
	mu     sync.Mutex
	byHash map[string]oauthCode
}

func newOAuthCodeStore() *oauthCodeStore {
	return &oauthCodeStore{byHash: map[string]oauthCode{}}
}

// issue records a code and returns nothing: the caller already holds the secret.
func (s *oauthCodeStore) issue(rec oauthCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	s.byHash[rec.hash] = rec
}

// spend resolves a code and removes it in the same critical section, so a code
// presented twice concurrently succeeds at most once. That single-use property is
// what makes a replayed exchange fail, and it has to be atomic to be true.
func (s *oauthCodeStore) spend(secret string, now time.Time) (oauthCode, bool) {
	want := hashAPIToken(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		found oauthCode
		ok    bool
	)
	for h, rec := range s.byHash {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			found, ok = rec, true
		}
	}
	if !ok {
		return oauthCode{}, false
	}
	delete(s.byHash, found.hash)
	if now.After(found.expires) {
		return oauthCode{}, false
	}
	return found, true
}

// sweepLocked drops codes that have run out. Called on issue, so the map cannot
// grow without bound on a server nobody ever completes a flow against — an
// unauthenticated caller cannot reach the authorize endpoint's issuing half, but
// an authenticated one abandoning flows should still not accumulate anything.
func (s *oauthCodeStore) sweepLocked(now time.Time) {
	for h, rec := range s.byHash {
		if now.After(rec.expires) {
			delete(s.byHash, h)
		}
	}
}

// verifyPKCE reports whether verifier matches challenge under S256.
//
// S256 only. RFC 7636 also defines "plain", which offers nothing: a challenge
// equal to its verifier is protection against an attacker who cannot read the
// authorization request, and one who cannot read it needs no PKCE to be stopped.
// The MCP specification requires clients to use S256 and to refuse a server that
// does not advertise it, so accepting plain would buy compatibility with nothing
// while weakening every flow that used it.
func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
