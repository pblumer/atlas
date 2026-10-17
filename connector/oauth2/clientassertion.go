package oauth2

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The client-credentials grant with a certificate instead of a secret: the client
// proves who it is with a JWT it signs with its certificate's private key (RFC 7523,
// section 2.2, "using JWTs for client authentication"), which Microsoft's identity
// platform calls a certificate credential (ADR-0172, amended).
//
// It is still the client-credentials grant — the token it yields is the same app-only
// token a client secret yields — so it sits beside [ClientCredentials] rather than
// replacing it. Which of the two a worker accepts stays the worker's policy.

// clientAssertionTTL is how long a signed client assertion claims to be valid. The
// assertion is exchanged immediately, so its lifetime bounds nothing but a replay
// window; ten minutes is the value Microsoft's own libraries use.
const clientAssertionTTL = 10 * time.Minute

// ClientAssertionConfig configures the certificate-authenticated client-credentials
// grant. Certificate is the app registration's uploaded certificate and PrivateKey its
// RSA key, both parsed by [ParseCertificateKeyPair].
type ClientAssertionConfig struct {
	HTTPClient  *http.Client
	Kind        string
	TokenURL    string
	ClientID    string
	Certificate *x509.Certificate
	PrivateKey  *rsa.PrivateKey
	Scope       string
}

// ClientAssertion builds the uncached certificate-authenticated client-credentials
// grant. Wrap it in [NewCached] like any other [Fetcher]. now is injectable so the
// assertion's time window is testable; nil means time.Now.
func ClientAssertion(c ClientAssertionConfig, now func() time.Time) Fetcher {
	return &caFetcher{c: c, now: now}
}

type caFetcher struct {
	c   ClientAssertionConfig
	now func() time.Time
}

func (f *caFetcher) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *caFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	assertion, err := f.signAssertion()
	if err != nil {
		return "", 0, err
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {f.c.ClientID},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	if f.c.Scope != "" {
		form.Set("scope", f.c.Scope)
	}
	return PostForm(ctx, f.c.HTTPClient, f.c.Kind, f.c.TokenURL, form)
}

// signAssertion builds and RS256-signs the client assertion. The header names the
// certificate by its thumbprints — x5t#S256, and x5t for an endpoint that only knows
// the older SHA-1 form — so the token endpoint can find which of the app's uploaded
// certificates to verify against. The claims are the ones RFC 7523 requires of a
// client: iss and sub are the client id, aud the token endpoint, and jti a fresh nonce
// so a captured assertion cannot be replayed.
func (f *caFetcher) signAssertion() (string, error) {
	if f.c.Certificate == nil || f.c.PrivateKey == nil {
		return "", fmt.Errorf("%s: client assertion needs a certificate and its private key", f.c.Kind)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("%s: client assertion nonce: %w", f.c.Kind, err)
	}
	// x5t is defined as a SHA-1 thumbprint. It only names the certificate; the
	// signature below is what secures the assertion.
	s1 := sha1.Sum(f.c.Certificate.Raw)
	s256 := sha256.Sum256(f.c.Certificate.Raw)
	header := map[string]string{
		"alg":      "RS256",
		"typ":      "JWT",
		"x5t":      base64.RawURLEncoding.EncodeToString(s1[:]),
		"x5t#S256": base64.RawURLEncoding.EncodeToString(s256[:]),
	}
	now := f.clock()
	claims := map[string]any{
		"aud": f.c.TokenURL,
		"iss": f.c.ClientID,
		"sub": f.c.ClientID,
		"jti": hex.EncodeToString(nonce),
		"nbf": now.Unix(),
		"iat": now.Unix(),
		"exp": now.Add(clientAssertionTTL).Unix(),
	}
	return signRS256(f.c.Kind, f.c.PrivateKey, header, claims)
}

// ParseCertificateKeyPair decodes one PEM text holding both a certificate and its
// private key — the shape `openssl pkcs12 -nodes` and PowerShell's
// ExportCertificatePem/ExportPkcs8PrivateKeyPem produce, and the shape the LDAP Worker
// Type already accepts for a client certificate. It refuses a key that does not belong
// to the certificate, and a key that is not RSA, because RS256 is what an Entra client
// assertion is signed with. kind prefixes every error so an operator reading it learns
// which Worker Type refused the credential; the PEM itself is never quoted.
func ParseCertificateKeyPair(kind, pemStr string) (*x509.Certificate, *rsa.PrivateKey, error) {
	pair, err := tls.X509KeyPair([]byte(pemStr), []byte(pemStr))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: certificate is not a PEM certificate with its unencrypted private key: %w", kind, err)
	}
	key, ok := pair.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("%s: certificate key is not an RSA key (an RS256 client assertion needs one)", kind)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("%s: cannot parse the certificate: %w", kind, err)
	}
	return cert, key, nil
}
