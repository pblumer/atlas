package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The whole security of a federated login is in this file's subject: an ID token
// is a bearer statement about who somebody is, and everything downstream — the
// account, the session, the roles — follows from believing it. So the checks are
// tested one at a time, each by a token that is valid except for the one thing
// under test (ADR-0210).

// signingKey is a test provider's key pair plus the key id it publishes under.
type signingKey struct {
	priv *rsa.PrivateKey
	kid  string
}

// testKeys is a small pool of RSA keys, generated once for the whole package.
//
// 2048 bits is what a real provider uses, and generating one per test was the
// difference between this package finishing and this package timing out under
// -race: the tests want *a* key, and only a handful want two that differ. The pool
// hands out a different key on each call, which is what those need, and costs
// three key generations for the run instead of twenty.
var (
	testKeysOnce sync.Once
	testKeys     [3]*rsa.PrivateKey
	testKeyNext  atomic.Uint32
)

func newSigningKey(t *testing.T, kid string) signingKey {
	t.Helper()
	testKeysOnce.Do(func() {
		for i := range testKeys {
			priv, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatalf("generate key: %v", err)
			}
			testKeys[i] = priv
		}
	})
	next := testKeyNext.Add(1) - 1
	return signingKey{priv: testKeys[next%uint32(len(testKeys))], kid: kid}
}

// jwks renders the public half the way a provider's jwks_uri does.
func (k signingKey) jwks() []byte {
	n := base64.RawURLEncoding.EncodeToString(k.priv.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.priv.E)).Bytes())
	return []byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"` + k.kid +
		`","n":"` + n + `","e":"` + e + `"}]}`)
}

// sign renders a JWT with the given header and claim JSON, signed with this key.
func (k signingKey) sign(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	enc := func(v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(header) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// tokenFor is a valid token for these expectations, which each test then bends in
// exactly one way.
func (k signingKey) tokenFor(t *testing.T, want oidcExpect, mutate func(header, claims map[string]any)) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": k.kid}
	claims := map[string]any{
		"iss": want.issuer, "sub": "subject-1", "aud": want.clientID,
		"exp": want.now.Add(time.Hour).Unix(), "iat": want.now.Unix(),
		"nonce": want.nonce, "email": "ada@example.org", "name": "Ada Lovelace",
		"preferred_username": "ada",
	}
	if mutate != nil {
		mutate(header, claims)
	}
	return k.sign(t, header, claims)
}

func testExpect(now time.Time) oidcExpect {
	return oidcExpect{
		issuer:   "https://idp.example.org",
		clientID: "atlas",
		nonce:    "nonce-1",
		now:      now,
	}
}

// TestAValidIDTokenYieldsItsClaims is the happy path, and the only test here that
// is allowed to pass.
func TestAValidIDTokenYieldsItsClaims(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "k1")
	keys, err := parseJWKS(key.jwks())
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	want := testExpect(now)

	claims, err := verifyIDToken(key.tokenFor(t, want, nil), keys, want)
	if err != nil {
		t.Fatalf("verifyIDToken: %v", err)
	}
	if claims.Subject != "subject-1" {
		t.Errorf("subject = %q, want subject-1", claims.Subject)
	}
	if claims.Email != "ada@example.org" || claims.Name != "Ada Lovelace" ||
		claims.PreferredUsername != "ada" {
		t.Errorf("claims = %+v, want the profile fields carried through", claims)
	}
}

// TestAnIDTokenIsRefused walks the checks one at a time. Each case is a token that
// a provider could plausibly produce, or an attacker plausibly forge, and differs
// from the valid one in exactly one respect.
func TestAnIDTokenIsRefused(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "k1")
	other := newSigningKey(t, "k1") // same key id, different key: the substitution
	keys, err := parseJWKS(key.jwks())
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	want := testExpect(now)

	cases := []struct {
		name  string
		token func() string
		says  string
	}{
		{"another issuer", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["iss"] = "https://evil.example" })
		}, "issuer"},
		{"a token for another client", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["aud"] = "somebody-else" })
		}, "audience"},
		{"an audience list this client is not in", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["aud"] = []any{"a", "b"} })
		}, "audience"},
		{"expired", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["exp"] = now.Add(-10 * time.Minute).Unix() })
		}, "expired"},
		{"no expiry at all", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { delete(c, "exp") })
		}, "expired"},
		{"issued in the future", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() })
		}, "issued"},
		{"another login's nonce", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { c["nonce"] = "somebody-elses" })
		}, "nonce"},
		{"no subject", func() string {
			return key.tokenFor(t, want, func(_, c map[string]any) { delete(c, "sub") })
		}, "subject"},
		{"signed by a different key under the same id", func() string {
			return other.tokenFor(t, want, nil)
		}, "signature"},
		{"an unknown key id", func() string {
			return key.tokenFor(t, want, func(h, _ map[string]any) { h["kid"] = "k2" })
		}, "key"},
		// The classic two: an attacker strips the signature, or swaps the algorithm
		// for one whose "key" is the public key everybody has.
		{"alg none", func() string {
			return key.tokenFor(t, want, func(h, _ map[string]any) { h["alg"] = "none" })
		}, "algorithm"},
		{"alg HS256", func() string {
			return key.tokenFor(t, want, func(h, _ map[string]any) { h["alg"] = "HS256" })
		}, "algorithm"},
		{"not a token at all", func() string { return "not.a.token" }, ""},
		{"two segments", func() string { return "aGVhZGVy.Y2xhaW1z" }, "segments"},
		{"empty", func() string { return "" }, "segments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyIDToken(tc.token(), keys, want)
			if err == nil {
				t.Fatal("accepted a token that must be refused")
			}
			if tc.says != "" && !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to name %q", err, tc.says)
			}
		})
	}
}

// TestTheClockSkewIsTheOneItSaysItIs pins the tolerance rather than leaving it to
// be discovered: a token that expired within the skew is still accepted, and one
// that expired beyond it is not. Both ends are machines with a clock, so the window
// is small — but it is not zero, and a test that avoided the boundary would leave
// the value free to drift.
func TestTheClockSkewIsTheOneItSaysItIs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "k1")
	keys, _ := parseJWKS(key.jwks())
	want := testExpect(now)

	justExpired := key.tokenFor(t, want, func(_, c map[string]any) {
		c["exp"] = now.Add(-oidcClockSkew / 2).Unix()
	})
	if _, err := verifyIDToken(justExpired, keys, want); err != nil {
		t.Errorf("a token expired within the skew was refused: %v", err)
	}
	longExpired := key.tokenFor(t, want, func(_, c map[string]any) {
		c["exp"] = now.Add(-2 * oidcClockSkew).Unix()
	})
	if _, err := verifyIDToken(longExpired, keys, want); err == nil {
		t.Error("a token expired beyond the skew was accepted")
	}
}

// TestAnAudienceListContainingThisClientIsAccepted: the aud claim is a string or a
// list, and a provider that issues the list form to a client that is in it is
// issuing a valid token. Refusing it would be a compatibility bug that looks like
// a security decision.
func TestAnAudienceListContainingThisClientIsAccepted(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "k1")
	keys, _ := parseJWKS(key.jwks())
	want := testExpect(now)

	token := key.tokenFor(t, want, func(_, c map[string]any) { c["aud"] = []any{"other", "atlas"} })
	if _, err := verifyIDToken(token, keys, want); err != nil {
		t.Errorf("verifyIDToken: %v, want the list form accepted", err)
	}
}

// TestASingleKeySetWithoutKeyIdsStillVerifies. Some providers publish one key and
// no kid, and some tokens carry no kid. Demanding one would refuse a correct
// provider; picking a key by trying every one of them is what a JWKS is for.
func TestASingleKeySetWithoutKeyIdsStillVerifies(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "")
	keys, err := parseJWKS(key.jwks())
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	want := testExpect(now)
	token := key.tokenFor(t, want, func(h, _ map[string]any) { delete(h, "kid") })
	if _, err := verifyIDToken(token, keys, want); err != nil {
		t.Errorf("verifyIDToken: %v, want a keyless set to still verify", err)
	}
}

// TestParseJWKSRefusesWhatItCannotUse: a key set that carries nothing usable is a
// misconfiguration, and the failure has to happen where it can be read — at the
// fetch — rather than later as "signature invalid" on every login.
func TestParseJWKSRefusesWhatItCannotUse(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not JSON", "{"},
		{"no keys", `{"keys":[]}`},
		{"only an elliptic key", `{"keys":[{"kty":"EC","kid":"e1","crv":"P-256","x":"a","y":"b"}]}`},
		{"a modulus that is not base64url", `{"keys":[{"kty":"RSA","kid":"k","n":"!!!","e":"AQAB"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseJWKS([]byte(tc.body)); err == nil {
				t.Error("accepted a key set with nothing usable in it")
			}
		})
	}
}

// signRaw signs a payload verbatim, for the cases where the claim set is not a
// claim set at all. The signature still has to verify — the point of these is that
// a *correctly signed* token can still be nonsense, and the reading has to fail
// rather than the trust.
func (k signingKey) signRaw(t *testing.T, payload string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"` + k.kid + `"}`))
	signing := header + "." + payload
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// TestASignedTokenCanStillBeUnreadable. Signature and content are different
// questions: the first says the provider wrote it, the second whether what it
// wrote is a claim set. Failing the second must not read as passing the first.
func TestASignedTokenCanStillBeUnreadable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := newSigningKey(t, "k1")
	keys, _ := parseJWKS(key.jwks())
	want := testExpect(now)

	for _, tc := range []struct{ name, payload, says string }{
		{"claims that are not base64url", "!!!not-base64!!!", "base64url"},
		{"claims that are not JSON", base64.RawURLEncoding.EncodeToString([]byte("not json")), "not JSON"},
		{"an audience that is neither a string nor a list",
			base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"x","aud":42}`)), "not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyIDToken(key.signRaw(t, tc.payload), keys, want)
			if err == nil {
				t.Fatal("accepted a token whose claims could not be read")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to name %q", err, tc.says)
			}
		})
	}

	// A signature that is not base64url at all fails before any of that.
	parts := strings.Split(key.tokenFor(t, want, nil), ".")
	if _, err := verifyIDToken(parts[0]+"."+parts[1]+".!!!", keys, want); err == nil {
		t.Error("accepted a token whose signature is not base64url")
	}
	// And a header that is valid base64url but not JSON.
	bad := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	if _, err := verifyIDToken(bad+"."+parts[1]+"."+parts[2], keys, want); err == nil {
		t.Error("accepted a token whose header could not be read")
	}
}

// TestParseJWKSKeepsOnlyWhatItCanVerifyWith. A real key set carries keys for other
// purposes and other algorithms; taking one of those would be verifying against
// something the provider never offered for signatures.
func TestParseJWKSKeepsOnlyWhatItCanVerifyWith(t *testing.T) {
	key := newSigningKey(t, "sig")
	usable := string(key.jwks())
	inner := strings.TrimSuffix(strings.TrimPrefix(usable, `{"keys":[`), `]}`)

	mixed := `{"keys":[` +
		`{"kty":"RSA","use":"enc","kid":"e","n":"AQAB","e":"AQAB"},` +
		`{"kty":"RSA","alg":"RS512","kid":"other","n":"AQAB","e":"AQAB"},` +
		`{"kty":"RSA","kid":"noexp","n":"AQAB","e":""},` +
		inner + `]}`
	set, err := parseJWKS([]byte(mixed))
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	if len(set.keys) != 1 || set.keys[0].kid != "sig" {
		t.Fatalf("kept %d keys, want only the signing one", len(set.keys))
	}
	// An exponent too large to be an int is not a key either.
	huge := `{"keys":[{"kty":"RSA","kid":"k","n":"AQAB","e":"AQABAQABAQAB"}]}`
	if _, err := parseJWKS([]byte(huge)); err == nil {
		t.Error("accepted a key whose exponent does not fit")
	}
}
