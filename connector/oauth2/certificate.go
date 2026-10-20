package oauth2

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The Microsoft identity platform's certificate credential: a client-credentials grant
// in which the app proves itself with a JWT signed by its certificate's private key
// rather than with a shared secret (ADR-0462).
//
// It lives here and not with the Exchange Worker for this package's standing reason:
// it is mechanism, not policy. Entra and SharePoint speak to the same token endpoint and
// can take a certificate the day an operator asks, and a second JWT signer beside
// [ServiceAccount]'s is the duplication this package exists to prevent — which is why
// both now sign through [signRS256].

// certificateAssertionTTL is how long a client assertion claims to be valid. It is
// exchanged at once, so ten minutes is ample clock skew between this host and the
// identity platform and short enough that an intercepted assertion is worth little.
const certificateAssertionTTL = 10 * time.Minute

// clientAssertionType is the RFC 7523 name of a JWT used as client authentication.
const clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// CertificateConfig configures the certificate grant. Certificate is the one registered
// on the app; PrivateKey signs the assertion and must belong to it. It is a signer, not
// a key: an *rsa.PrivateKey read from a PEM is one, and so is a key that stays in the
// Windows certificate store and only signs on request (ADR-0172, amended).
type CertificateConfig struct {
	HTTPClient  *http.Client
	Kind        string
	TokenURL    string
	ClientID    string
	Certificate *x509.Certificate
	PrivateKey  crypto.Signer
	Scope       string
}

// ClientCertificate builds the uncached certificate grant. Wrap it in [NewCached] like
// any other [Fetcher]. now is injectable so the assertion's time window is testable;
// nil means time.Now.
func ClientCertificate(c CertificateConfig, now func() time.Time) Fetcher {
	return &certFetcher{c: c, now: now}
}

type certFetcher struct {
	c   CertificateConfig
	now func() time.Time
}

func (f *certFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	assertion, err := f.assertion()
	if err != nil {
		return "", 0, err
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {f.c.ClientID},
		"client_assertion_type": {clientAssertionType},
		"client_assertion":      {assertion},
	}
	if f.c.Scope != "" {
		form.Set("scope", f.c.Scope)
	}
	return PostForm(ctx, f.c.HTTPClient, f.c.Kind, f.c.TokenURL, form)
}

// assertion builds the signed client assertion. The certificate is named by both of the
// thumbprint headers the identity platform reads — x5t#S256, which it recommends, and
// the SHA-1 x5t it has always accepted — so neither a tenant that has moved on nor one
// that has not refuses it. jti makes every assertion unique, because a replayed one is
// refused.
func (f *certFetcher) assertion() (string, error) {
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("%s: generate assertion id: %w", f.c.Kind, err)
	}
	s1 := sha1.Sum(f.c.Certificate.Raw)
	s256 := sha256.Sum256(f.c.Certificate.Raw)
	header := map[string]string{
		"alg":      "RS256",
		"typ":      "JWT",
		"x5t":      base64.RawURLEncoding.EncodeToString(s1[:]),
		"x5t#S256": base64.RawURLEncoding.EncodeToString(s256[:]),
	}
	claims := map[string]any{
		"aud": f.c.TokenURL,
		"iss": f.c.ClientID,
		"sub": f.c.ClientID,
		"jti": hex.EncodeToString(jti),
		"nbf": now.Unix(),
		"iat": now.Unix(),
		"exp": now.Add(certificateAssertionTTL).Unix(),
	}
	return signRS256(f.c.Kind, header, claims, f.c.PrivateKey)
}

// ParseCertificateBundle reads an app's certificate and its private key from one PEM
// text, in either order — the shape an operator gets by concatenating the .crt and the
// .key, and the one a vault bundle can carry as a single string. The key must belong to
// the certificate: a mismatched pair signs assertions the identity platform rejects
// with a message about the client, not about the key, so it is refused here instead.
func ParseCertificateBundle(kind, pemStr string) (*x509.Certificate, *rsa.PrivateKey, error) {
	var (
		cert *x509.Certificate
		key  *rsa.PrivateKey
		rest = []byte(pemStr)
	)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			if cert != nil {
				continue // a chain: the first certificate is the app's own
			}
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: cannot parse the certificate: %w", kind, err)
			}
			cert = c
		case "PRIVATE KEY", "RSA PRIVATE KEY":
			k, err := ParseRSAPrivateKey(kind, string(pem.EncodeToMemory(block)))
			if err != nil {
				return nil, nil, err
			}
			key = k
		}
	}
	if cert == nil {
		return nil, nil, fmt.Errorf("%s: the certificate bundle holds no CERTIFICATE block", kind)
	}
	if key == nil {
		return nil, nil, fmt.Errorf("%s: the certificate bundle holds no private key (a PRIVATE KEY or RSA PRIVATE KEY block)", kind)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, nil, fmt.Errorf("%s: the private key does not belong to the certificate", kind)
	}
	return cert, key, nil
}
