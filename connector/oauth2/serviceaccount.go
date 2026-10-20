package oauth2

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The Google service-account grant: a JWT-bearer assertion signed with a service
// account's private key and exchanged for an access token
// (ADR-0235).
//
// This package's doc comment used to name it as the one grant deliberately left with
// its caller — "the mail Worker Type's Google service-account JWT-bearer assertion is
// the one such case". A second caller needs it now, and two copies of a JWT signer is
// the duplication this package was extracted to end, so the mechanism moved here and
// the policy stayed with each caller: which grants it accepts, what its credential
// bundle looks like, and what its endpoint and scope defaults are.

// assertionTTL is how long a signed assertion claims to be valid. One hour is the
// maximum Google honours, and the assertion is exchanged immediately, so its lifetime
// bounds nothing that matters — the *access token's* expiry is what [NewCached] tracks.
const assertionTTL = time.Hour

// ServiceAccountConfig configures the JWT-bearer grant. ClientEmail is the service
// account's own address (the assertion's issuer) and PrivateKey its RSA signing key,
// parsed by [ParseRSAPrivateKey]. Subject is the user the account impersonates through
// domain-wide delegation; leaving it empty makes the account act as itself, which is
// what a service account owning its own files does.
type ServiceAccountConfig struct {
	HTTPClient  *http.Client
	Kind        string
	TokenURL    string
	ClientEmail string
	PrivateKey  *rsa.PrivateKey
	Scope       string
	Subject     string
}

// ServiceAccount builds the uncached JWT-bearer grant. Wrap it in [NewCached] like any
// other [Fetcher]. now is injectable so the assertion's time window is testable
// without a clock; nil means time.Now.
func ServiceAccount(c ServiceAccountConfig, now func() time.Time) Fetcher {
	return &saFetcher{c: c, now: now}
}

type saFetcher struct {
	c   ServiceAccountConfig
	now func() time.Time
}

func (f *saFetcher) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *saFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	assertion, err := f.signAssertion()
	if err != nil {
		return "", 0, err
	}
	return PostForm(ctx, f.c.HTTPClient, f.c.Kind, f.c.TokenURL, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
}

// signAssertion builds and RS256-signs the assertion the token endpoint exchanges: iss
// is the service account, sub the impersonated user (omitted when there is none, since
// an empty sub is rejected rather than ignored), and aud the token endpoint itself.
func (f *saFetcher) signAssertion() (string, error) {
	now := f.clock()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   f.c.ClientEmail,
		"scope": f.c.Scope,
		"aud":   f.c.TokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(assertionTTL).Unix(),
	}
	if f.c.Subject != "" {
		claims["sub"] = f.c.Subject
	}
	return signRS256(f.c.Kind, header, claims, f.c.PrivateKey)
}

// signRS256 encodes and RS256-signs a JWT. It is the one signer every assertion grant
// here shares — Google's service account and Microsoft's certificate credential differ
// in their claims, not in how a JWT is put together.
//
// It takes a crypto.Signer rather than a key, because a key in the Windows certificate
// store signs on request and is never handed over (ADR-0172, amended). An RSA key's
// Sign with a plain hash is PKCS#1 v1.5, which is what RS256 is; any other key is
// refused here, before a token endpoint sees a signature it cannot check.
func signRS256(kind string, header map[string]string, claims map[string]any, key crypto.Signer) (string, error) {
	if _, ok := key.Public().(*rsa.PublicKey); !ok {
		return "", fmt.Errorf("%s: the signing key is not RSA; an RS256 assertion needs an RSA key", kind)
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("%s: encode JWT header: %w", kind, err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%s: encode JWT claims: %w", kind, err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := key.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("%s: sign JWT assertion: %w", kind, err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ParseRSAPrivateKey decodes a PEM-encoded RSA private key for JWT signing: PKCS#8, as
// a Google service-account JSON file carries it, or PKCS#1. kind prefixes the error so
// an operator reading it learns which Worker Type refused the key.
func ParseRSAPrivateKey(kind, pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("%s: service-account private key is not valid PEM", kind)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: service-account private key is not an RSA key", kind)
		}
		return rk, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("%s: cannot parse service-account private key (want PKCS#8 or PKCS#1 RSA)", kind)
}
