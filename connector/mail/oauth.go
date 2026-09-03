package mail

import (
	"crypto/rsa"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// TokenSource yields a valid OAuth2 bearer access token for a provider API. The
// mechanism — caching, refresh timing, the token exchange — is the shared [oauth2]
// package's; what stays here is this worker's policy: which grants it accepts,
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
// worker's credentialsRef (ADR-0093). method selects the OAuth2 grant; the
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

// parseRSAPrivateKey decodes a PEM-encoded RSA private key (PKCS#8, as Google service
// account JSON carries, or PKCS#1) for JWT signing. The mechanism is the shared
// [oauth2] package's since a second caller needed it
// (ADR-0235); what stays here is this package's name on the
// error an operator reads.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	return oauth2.ParseRSAPrivateKey("mail", pemStr)
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
		if b.ClientEmail == "" || b.PrivateKey == "" {
			return nil, fmt.Errorf("mail: serviceAccount needs clientEmail and privateKey")
		}
		key, err := parseRSAPrivateKey(b.PrivateKey)
		if err != nil {
			return nil, err
		}
		f = oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
			HTTPClient: httpc, Kind: "mail", TokenURL: b.TokenURL,
			ClientEmail: b.ClientEmail, PrivateKey: key, Scope: b.Scope, Subject: b.Subject,
		}, now)
	default:
		return nil, fmt.Errorf("mail: unknown auth method %q (want %q, %q, or %q)", b.Method, methodClientCredentials, methodRefreshToken, methodServiceAccount)
	}
	return oauth2.NewCached(f, now), nil
}
