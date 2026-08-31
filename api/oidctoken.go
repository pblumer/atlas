package api

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Validating what an identity provider says (ADR-0210).
//
// An ID token is a bearer statement about who somebody is. Everything downstream
// — the account, the session, the roles that decide what it may do — follows from
// believing it, so this file is the whole security of a federated login and is
// deliberately the least clever code in the package.
//
// It is written here rather than pulled in as a dependency for the same reason the
// authorization server of ADR-0200 is: the part that has to be right is small,
// exactly specified, and testable, while a JWT library brings an algorithm matrix
// Atlas will never use. What it does bring is the two mistakes this file exists to
// not make — accepting the algorithm the token names, and accepting a token whose
// signature was never checked. Both are refused by construction below.

const (
	// oidcSigningAlg is the only signature algorithm accepted. RS256 is the one
	// OpenID Connect requires a provider to implement, and it is what Entra,
	// Keycloak, Google and Auth0 all issue by default.
	//
	// An allowlist of one is the point. "alg" travels *inside the token*, so a
	// verifier that honours it lets the sender choose how it is checked: "none"
	// removes the signature, and a symmetric algorithm invites verification against
	// the public key everybody already has. Neither is a subtle attack; both are
	// what happens when this constant becomes a variable.
	oidcSigningAlg = "RS256"

	// oidcClockSkew is how far the provider's clock may be from this one. Small,
	// because both ends are machines with NTP, and it applies to the two claims a
	// skew actually breaks: an expiry that has just passed and an issue time that
	// is just ahead.
	oidcClockSkew = 60 * time.Second
)

// oidcExpect is what a token has to say to be believed: who issued it, who it is
// for, which login it belongs to, and what the time is.
type oidcExpect struct {
	issuer   string
	clientID string
	nonce    string
	now      time.Time
}

// oidcClaims is what Atlas reads out of a validated token. The subject is the
// identity; the rest is display material for the account it creates.
type oidcClaims struct {
	Subject           string
	Email             string
	Name              string
	PreferredUsername string

	// Raw is the whole validated claim set, for the one caller that cannot know in
	// advance which claim it needs: the mapping from a provider's groups onto roles
	// reads a claim an operator named (oidcmapping.go). It is what the token said and
	// nothing more — it is filled in only after every check has passed, so there is
	// no path by which an unverified claim reaches a decision.
	Raw map[string]any
}

// jwkKey is one usable signing key from a provider's key set.
type jwkKey struct {
	kid string
	pub *rsa.PublicKey
}

// jwkSet is the provider's published signing keys, already parsed. Only the RSA
// ones survive parsing: a key this build cannot verify with is not a key.
type jwkSet struct {
	keys []jwkKey
}

// rawJWKS is the wire shape of a JWKS document, with only the fields that decide
// whether a key is usable here.
type rawJWKS struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// parseJWKS turns a provider's key set into the keys this build can verify with.
//
// It fails when nothing usable is left, and that is deliberate: a misconfiguration
// — the wrong URL, a provider that signs with elliptic keys — has to surface where
// an operator can read it, at the fetch, rather than as "invalid signature" on
// every login for the rest of the day.
func parseJWKS(body []byte) (*jwkSet, error) {
	var doc rawJWKS
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("oidc: jwks is not JSON: %w", err)
	}
	set := &jwkSet{}
	for _, k := range doc.Keys {
		// A key published for encryption is not one to verify signatures with, and a
		// key that names another algorithm is not one this build can use.
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") ||
			(k.Alg != "" && k.Alg != oidcSigningAlg) {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil || len(n) == 0 {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(e) == 0 {
			continue
		}
		exp := new(big.Int).SetBytes(e)
		if !exp.IsInt64() || exp.Int64() <= 0 || exp.Int64() > 1<<31 {
			continue
		}
		set.keys = append(set.keys, jwkKey{
			kid: k.Kid,
			pub: &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp.Int64())},
		})
	}
	if len(set.keys) == 0 {
		return nil, errors.New("oidc: jwks carries no usable RS256 signing key")
	}
	return set, nil
}

// candidates returns the keys a token naming this key id may be verified against:
// the one that matches, or every key when the token names none.
//
// Trying each key rather than demanding a kid is not laxity — a signature either
// verifies or it does not, and no number of attempts makes a forged one verify.
// Demanding a kid would refuse a correct provider that publishes a single key
// without one, which is a compatibility bug wearing a security costume.
func (s *jwkSet) candidates(kid string) []jwkKey {
	if kid == "" {
		return s.keys
	}
	var out []jwkKey
	for _, k := range s.keys {
		if k.kid == kid || k.kid == "" {
			out = append(out, k)
		}
	}
	return out
}

// jwtHeader is the part of a JOSE header this file consults.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// audience is an "aud" claim, which OpenID Connect allows to be one string or a
// list of them.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a audience) contains(want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}

// idTokenClaims is the wire shape of the claim set.
type idTokenClaims struct {
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	Audience          audience `json:"aud"`
	Expiry            int64    `json:"exp"`
	IssuedAt          int64    `json:"iat"`
	Nonce             string   `json:"nonce"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	PreferredUsername string   `json:"preferred_username"`
}

// verifyIDToken checks a signed ID token against a key set and returns its claims.
//
// The order is the one that matters: the signature is verified *before* any claim
// is believed, so nothing an unsigned or wrongly signed token says ever reaches a
// decision. Every failure is an error naming which check failed, for the audit
// line; the caller answers the browser with something uniform.
func verifyIDToken(raw string, keys *jwkSet, want oidcExpect) (oidcClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return oidcClaims{}, fmt.Errorf("oidc: id token has %d segments, want 3", len(parts))
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token header is not base64url: %w", err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token header is not JSON: %w", err)
	}
	if header.Alg != oidcSigningAlg {
		return oidcClaims{}, fmt.Errorf("oidc: id token names algorithm %q, only %s is accepted",
			header.Alg, oidcSigningAlg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token signature is not base64url: %w", err)
	}
	signed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	for _, k := range keys.candidates(header.Kid) {
		if rsa.VerifyPKCS1v15(k.pub, crypto.SHA256, signed[:], sig) == nil {
			verified = true
			break
		}
	}
	if !verified {
		if len(keys.candidates(header.Kid)) == 0 {
			return oidcClaims{}, fmt.Errorf("oidc: no signing key %q in the provider's key set", header.Kid)
		}
		return oidcClaims{}, errors.New("oidc: id token signature does not verify")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token claims are not base64url: %w", err)
	}
	var c idTokenClaims
	if err := json.Unmarshal(claimsJSON, &c); err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token claims are not JSON: %w", err)
	}
	claimSet := map[string]any{}
	if err := json.Unmarshal(claimsJSON, &claimSet); err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id token claims are not an object: %w", err)
	}
	switch {
	case c.Issuer != want.issuer:
		return oidcClaims{}, fmt.Errorf("oidc: id token issuer is %q, want %q", c.Issuer, want.issuer)
	case !c.Audience.contains(want.clientID):
		return oidcClaims{}, fmt.Errorf("oidc: id token audience %v does not include this client", []string(c.Audience))
	case c.Subject == "":
		return oidcClaims{}, errors.New("oidc: id token carries no subject")
	case c.Expiry == 0 || want.now.After(time.Unix(c.Expiry, 0).Add(oidcClockSkew)):
		return oidcClaims{}, errors.New("oidc: id token has expired")
	case c.IssuedAt != 0 && time.Unix(c.IssuedAt, 0).After(want.now.Add(oidcClockSkew)):
		return oidcClaims{}, errors.New("oidc: id token was issued in the future")
	case c.Nonce != want.nonce:
		// The nonce ties the token to the login that started it, which is what stops
		// a token obtained elsewhere from being replayed into somebody's browser.
		return oidcClaims{}, errors.New("oidc: id token nonce does not match this login")
	}
	return oidcClaims{
		Subject:           c.Subject,
		Email:             c.Email,
		Name:              c.Name,
		PreferredUsername: c.PreferredUsername,
		Raw:               claimSet,
	}, nil
}
