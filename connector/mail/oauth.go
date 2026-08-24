package mail

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
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// TokenSource yields a valid OAuth2 bearer access token for a provider API. The
// mechanism — caching, refresh timing, the token exchange — is the shared [oauth2]
// package's; what stays here is this connector's policy: which grants it accepts,
// what its credential bundle looks like, and the one grant nobody else has.
type TokenSource = oauth2.TokenSource

// OAuth2 grant methods a native mail provider supports (ADR-0093). clientCredentials
// is app-only (a confidential client sends as a fixed mailbox); refreshToken exchanges
// a pre-obtained refresh token (works with consumer accounts); serviceAccount is a
// Google service account with domain-wide delegation (a signed JWT-bearer assertion,
// Workspace only).
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
	methodServiceAccount    = "serviceAccount"
)

// credentialBundle is the JSON an operator stores in the vault under a mail
// connector's credentialsRef (ADR-0093). method selects the OAuth2 grant; the
// remaining fields configure it. Non-secret fields (ids, tenant) and secret fields
// (clientSecret, refreshToken, privateKey) live together in this one vault secret, so
// a model never carries any of them (I6). tokenUrl and scope are optional overrides;
// the provider supplies sensible defaults.
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ClientEmail  string `json:"clientEmail,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	Subject      string `json:"subject,omitempty"`
}

// serviceAccountFetcher signs a JWT-bearer assertion with a Google service account's
// private key and exchanges it for an access token (domain-wide delegation). subject
// is the mailbox the service account impersonates.
type serviceAccountFetcher struct {
	httpc       *http.Client
	tokenURL    string
	clientEmail string
	privateKey  *rsa.PrivateKey
	scope       string
	subject     string
	now         func() time.Time
}

func (f *serviceAccountFetcher) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *serviceAccountFetcher) Fetch(ctx context.Context) (string, time.Duration, error) {
	assertion, err := f.signAssertion()
	if err != nil {
		return "", 0, err
	}
	return oauth2.PostForm(ctx, f.httpc, "mail", f.tokenURL, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
}

// signAssertion builds and RS256-signs the JWT-bearer assertion a Google service
// account presents: iss is the SA email, sub the impersonated user, aud the token
// endpoint, with a one-hour validity window.
func (f *serviceAccountFetcher) signAssertion() (string, error) {
	now := f.clock()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   f.clientEmail,
		"scope": f.scope,
		"aud":   f.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	if f.subject != "" {
		claims["sub"] = f.subject
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("mail: encode JWT header: %w", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("mail: encode JWT claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("mail: sign JWT assertion: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key (PKCS#8, as Google service
// account JSON carries, or PKCS#1) for JWT signing.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("mail: service-account private key is not valid PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("mail: service-account private key is not an RSA key")
		}
		return rk, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("mail: cannot parse service-account private key (want PKCS#8 or PKCS#1 RSA)")
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the provider has already filled in tokenUrl/scope defaults and, for a service
// account, the impersonation subject). It validates the fields the chosen method
// requires, so a misconfigured bundle fails here rather than at send time. httpc and
// now are injected so the token flow is testable without a live OAuth endpoint.
func newTokenSource(b credentialBundle, httpc *http.Client, now func() time.Time) (TokenSource, error) {
	if strings.TrimSpace(b.TokenURL) == "" {
		return nil, fmt.Errorf("mail: credential has no token URL")
	}
	cfg := oauth2.Config{
		HTTPClient: httpc, Kind: "mail", TokenURL: b.TokenURL,
		ClientID: b.ClientID, ClientSecret: b.ClientSecret,
		RefreshToken: b.RefreshToken, Scope: b.Scope,
	}
	var f oauth2.Fetcher
	switch b.Method {
	case methodClientCredentials:
		if b.ClientID == "" || b.ClientSecret == "" {
			return nil, fmt.Errorf("mail: clientCredentials needs clientId and clientSecret")
		}
		f = oauth2.ClientCredentials(cfg)
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("mail: refreshToken needs clientId, clientSecret and refreshToken")
		}
		f = oauth2.RefreshToken(cfg)
	case methodServiceAccount:
		// The one grant the shared package does not carry: it is Google-specific, so
		// it lives with the connector that needs it and only borrows the exchange.
		if b.ClientEmail == "" || b.PrivateKey == "" {
			return nil, fmt.Errorf("mail: serviceAccount needs clientEmail and privateKey")
		}
		key, err := parseRSAPrivateKey(b.PrivateKey)
		if err != nil {
			return nil, err
		}
		f = &serviceAccountFetcher{httpc: httpc, tokenURL: b.TokenURL, clientEmail: b.ClientEmail, privateKey: key, scope: b.Scope, subject: b.Subject, now: now}
	default:
		return nil, fmt.Errorf("mail: unknown auth method %q (want %q, %q, or %q)", b.Method, methodClientCredentials, methodRefreshToken, methodServiceAccount)
	}
	return oauth2.NewCached(f, now), nil
}
