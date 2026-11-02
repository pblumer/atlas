package api

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// A connected system authenticates with a signed assertion (RFC 7523, ADR-0512 §1).
//
// A client named by its metadata document holds no secret here: it proves who it is at
// the token and revocation endpoints by signing a short-lived JWT with a key its
// document publishes. So no shared secret exists to copy into an environment file, to
// leak from one, or to rotate by hand — a key is rotated by changing the document.
//
// The verifier is written here for the reason oidctoken.go gives for its own: the part
// that has to be right is small and exactly specified, and a JWT library brings an
// algorithm matrix Atlas would never use. The same two mistakes are refused by
// construction — the algorithm is checked against the key, never taken from the token
// alone, and nothing the token says is believed before its signature verified — and one
// more that a client assertion adds: an assertion is good once. A copy of one taken off
// the wire is worth nothing after the system itself used it, because its `jti` is
// remembered for as long as the assertion could still be presented.

const (
	// clientAssertionType is the one assertion type accepted (RFC 7523 §2.2).
	clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

	// assertionLifetime is the longest an assertion may be good for, from its iat to
	// its exp. Short because a system signs a fresh one for every request; a long one
	// would only be a longer window for a copy.
	assertionLifetime = 5 * time.Minute

	// assertionLeeway is how far the system's clock may be from this one — the same
	// minute oidcClockSkew allows an identity provider.
	assertionLeeway = 60 * time.Second

	// maxAssertionReplay bounds how many assertions are remembered at once. Five
	// minutes at most each, so this is over fifty a second sustained — far past what a
	// token endpoint throttled per address lets through — and the cache refuses rather
	// than forgets when it is full: forgetting would make a replay succeed.
	maxAssertionReplay = 16384
)

// errAssertionKey says an assertion named a key the document does not hold, or its
// signature did not verify against the ones it does. Those two are what a rotated key
// looks like from here, so the caller reads the document again before refusing.
var errAssertionKey = errors.New("the assertion is not signed by a key the client publishes")

// assertionClaims is the wire shape of a client assertion's claim set.
type assertionClaims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  audience `json:"aud"`
	Expiry    int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	ID        string   `json:"jti"`
}

// unverifiedIssuer reads the iss of an assertion without checking anything, for the
// one use that needs it before a key is known: finding which document to read the keys
// from when the request does not say which client it is. Nothing else may read it; the
// claim is checked against the client_id once the signature has verified.
func unverifiedIssuer(raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c assertionClaims
	if json.Unmarshal(body, &c) != nil {
		return ""
	}
	return c.Issuer
}

// verifyClientAssertion checks an assertion's signature against a client's keys, then
// its claims against the client and this server, and returns them. It does not spend
// the jti: a caller that reads the document again after errAssertionKey verifies twice,
// and only the verification that succeeds may count as the assertion's one use.
func verifyClientAssertion(raw, clientID string, keys []clientKey, audiences []string, now time.Time) (assertionClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return assertionClaims{}, fmt.Errorf("the assertion has %d segments, want 3", len(parts))
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return assertionClaims{}, errors.New("the assertion's header is not base64url")
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return assertionClaims{}, errors.New("the assertion's header is not JSON")
	}
	if header.Alg != "EdDSA" && header.Alg != "ES256" {
		return assertionClaims{}, fmt.Errorf("the assertion names algorithm %q; EdDSA and ES256 are accepted", header.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return assertionClaims{}, errors.New("the assertion's signature is not base64url")
	}
	signed := []byte(parts[0] + "." + parts[1])
	verified := false
	for _, k := range keys {
		// The key decides the algorithm, not the token: a key published for EdDSA is
		// never tried as anything else.
		if k.alg != header.Alg || (header.Kid != "" && k.kid != "" && k.kid != header.Kid) {
			continue
		}
		if verifyWithKey(k, signed, sig) {
			verified = true
			break
		}
	}
	if !verified {
		return assertionClaims{}, errAssertionKey
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return assertionClaims{}, errors.New("the assertion's claims are not base64url")
	}
	var c assertionClaims
	if err := json.Unmarshal(claimsJSON, &c); err != nil {
		return assertionClaims{}, errors.New("the assertion's claims are not JSON")
	}
	exp := time.Unix(c.Expiry, 0)
	switch {
	case c.Issuer != clientID || c.Subject != clientID:
		return assertionClaims{}, errors.New("the assertion's iss and sub must both be the client_id")
	case !audienceAny(c.Audience, audiences):
		return assertionClaims{}, fmt.Errorf("the assertion's audience %v is not this server's token endpoint", []string(c.Audience))
	case c.Expiry == 0:
		return assertionClaims{}, errors.New("the assertion carries no exp")
	case now.After(exp.Add(assertionLeeway)):
		return assertionClaims{}, errors.New("the assertion has expired")
	case c.IssuedAt != 0 && time.Unix(c.IssuedAt, 0).After(now.Add(assertionLeeway)):
		return assertionClaims{}, errors.New("the assertion was issued in the future")
	case c.NotBefore != 0 && time.Unix(c.NotBefore, 0).After(now.Add(assertionLeeway)):
		return assertionClaims{}, errors.New("the assertion is not valid yet")
	case c.IssuedAt != 0 && exp.Sub(time.Unix(c.IssuedAt, 0)) > assertionLifetime:
		return assertionClaims{}, errors.New("the assertion is good for longer than five minutes")
	case c.IssuedAt == 0 && exp.After(now.Add(assertionLifetime+assertionLeeway)):
		return assertionClaims{}, errors.New("the assertion is good for longer than five minutes")
	case c.ID == "" || len(c.ID) > 256:
		return assertionClaims{}, errors.New("the assertion carries no usable jti")
	}
	return c, nil
}

// verifyWithKey checks one signature with one key, by the key's algorithm.
func verifyWithKey(k clientKey, signed, sig []byte) bool {
	switch k.alg {
	case "EdDSA":
		return k.ed != nil && ed25519.Verify(k.ed, signed, sig)
	case "ES256":
		// A JWS ES256 signature is r and s, 32 bytes each, not ASN.1 (RFC 7518 §3.4).
		if k.ec == nil || len(sig) != 64 {
			return false
		}
		sum := sha256.Sum256(signed)
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(k.ec, sum[:], r, s)
	}
	return false
}

func audienceAny(have audience, want []string) bool {
	for _, w := range want {
		if have.contains(w) {
			return true
		}
	}
	return false
}

// assertionReplayCache remembers the assertions already used, each until it could no
// longer be presented anyway. In memory, like the authorization codes: a restart
// forgets them, and an assertion is good for five minutes at most, so what a restart
// opens is a window as long as the restart and as wide as one copied assertion.
type assertionReplayCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
	max  int
}

func newAssertionReplayCache(max int) *assertionReplayCache {
	return &assertionReplayCache{seen: map[string]time.Time{}, max: max}
}

// spend records an assertion's one use and reports whether this was it: false for one
// seen before within its lifetime, and false when the cache is full of assertions that
// are all still live — refusing is the direction that cannot let a replay through.
func (c *assertionReplayCache) spend(clientID, jti string, until, now time.Time) bool {
	key := clientID + "\x00" + jti
	c.mu.Lock()
	defer c.mu.Unlock()
	if at, ok := c.seen[key]; ok && !now.After(at) {
		return false
	}
	if len(c.seen) >= c.max {
		for k, at := range c.seen {
			if now.After(at) {
				delete(c.seen, k)
			}
		}
		if len(c.seen) >= c.max {
			return false
		}
	}
	c.seen[key] = until.Add(assertionLeeway)
	return true
}

// authenticateClient settles which client is calling the token or revocation endpoint:
// by its secret, as every client entered here does (ADR-0200), or by a signed assertion,
// as a client named by its metadata document does (ADR-0512 §1). audiences are the
// values this endpoint accepts as the assertion's aud.
//
// A refused assertion is audited with the reason, because the caller is told only
// invalid_client — the specification's answer — and whoever runs the system needs to
// find out which check it failed.
func (s *Server) authenticateClient(r *http.Request, audiences ...string) (oauthClient, bool) {
	assertionType := r.PostFormValue("client_assertion_type")
	assertion := r.PostFormValue("client_assertion")
	if assertionType == "" && assertion == "" {
		return s.oauthClients.authenticate(r.PostFormValue("client_id"), r.PostFormValue("client_secret"))
	}
	client, err := s.authenticateAssertion(r, assertionType, assertion, audiences)
	if err != nil {
		auditRefusal(r, logging.AuthOAuthDenied, "oauth: a client assertion was refused",
			slog.String("client_id", r.PostFormValue("client_id")), slog.String("reason", err.Error()))
		return oauthClient{}, false
	}
	return client, true
}

// authenticateAssertion is the assertion half of authenticateClient.
func (s *Server) authenticateAssertion(r *http.Request, assertionType, assertion string, audiences []string) (oauthClient, error) {
	if assertionType != clientAssertionType || assertion == "" {
		return oauthClient{}, errors.New("client_assertion_type must be " + clientAssertionType + ", with a client_assertion")
	}
	if r.PostFormValue("client_secret") != "" {
		return oauthClient{}, errors.New("a client authenticates one way, not with a secret and an assertion")
	}
	clientID := r.PostFormValue("client_id")
	if clientID == "" {
		clientID = unverifiedIssuer(assertion)
	}
	if !isMetadataClientID(clientID) {
		return oauthClient{}, errors.New("only a client named by its metadata document authenticates with an assertion")
	}
	if s.clientMetadata == nil || s.assertionReplay == nil {
		return oauthClient{}, errNoMetadataReader
	}
	now := time.Now()
	md, err := s.clientMetadata.get(r.Context(), clientID, false)
	if err != nil {
		return oauthClient{}, err
	}
	claims, err := verifyClientAssertion(assertion, clientID, md.keys, audiences, now)
	if errors.Is(err, errAssertionKey) {
		// A key the cached document does not hold may be one the system rotated in
		// since: read the document once more before refusing.
		if md, err = s.clientMetadata.get(r.Context(), clientID, true); err != nil {
			return oauthClient{}, err
		}
		claims, err = verifyClientAssertion(assertion, clientID, md.keys, audiences, now)
	}
	if err != nil {
		return oauthClient{}, err
	}
	if !s.assertionReplay.spend(clientID, claims.ID, time.Unix(claims.Expiry, 0), now) {
		return oauthClient{}, errors.New("the assertion was used before")
	}
	return s.withStoredFields(md.client), nil
}
