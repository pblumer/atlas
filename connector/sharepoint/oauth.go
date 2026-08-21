package sharepoint

import (
	"net/http"
	"strings"
	"time"

	"fmt"

	"github.com/pblumer/atlas/connector/oauth2"
)

// TokenSource yields a valid OAuth2 bearer access token for the Graph API. The
// mechanism — caching, refresh timing, the token exchange — is the shared
// [oauth2] package's; what stays here is this connector's policy: which grants it
// accepts and what its credential bundle looks like.
type TokenSource = oauth2.TokenSource

// OAuth2 grant methods a SharePoint connector supports (ADR-0141). clientCredentials
// is app-only (a confidential client acts as itself, the norm for server workflows);
// refreshToken exchanges a pre-obtained refresh token (works for delegated /
// consumer scenarios). These mirror the native mail providers' grants (ADR-0093);
// the service-account (JWT-bearer) grant is Google-specific and omitted here.
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
)

// credentialBundle is the JSON an operator stores in the vault under a SharePoint
// connector's credentialsRef (ADR-0141). method selects the OAuth2 grant; the
// remaining fields configure it. Non-secret fields (ids, tenant) and secret fields
// (clientSecret, refreshToken) live together in this one vault secret, so a model
// never carries any of them (I6). tokenUrl and scope are optional overrides; the
// connector supplies sensible Graph defaults (from tenantId).
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the connector has already filled in tokenUrl/scope defaults). It validates the
// fields the chosen method requires, so a misconfigured bundle fails here rather than
// at call time. httpc and now are injected so the token flow is testable without a
// live OAuth endpoint.
func newTokenSource(b credentialBundle, httpc *http.Client, now func() time.Time) (TokenSource, error) {
	if strings.TrimSpace(b.TokenURL) == "" {
		return nil, fmt.Errorf("sharepoint: credential has no token URL (set tokenUrl or tenantId)")
	}
	cfg := oauth2.Config{
		HTTPClient: httpc, Kind: "sharepoint", TokenURL: b.TokenURL,
		ClientID: b.ClientID, ClientSecret: b.ClientSecret,
		RefreshToken: b.RefreshToken, Scope: b.Scope,
	}
	var f oauth2.Fetcher
	switch b.Method {
	case methodClientCredentials:
		if b.ClientID == "" || b.ClientSecret == "" {
			return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and clientSecret")
		}
		f = oauth2.ClientCredentials(cfg)
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("sharepoint: refreshToken needs clientId, clientSecret and refreshToken")
		}
		f = oauth2.RefreshToken(cfg)
	default:
		return nil, fmt.Errorf("sharepoint: unknown auth method %q (want %q or %q)", b.Method, methodClientCredentials, methodRefreshToken)
	}
	return oauth2.NewCached(f, now), nil
}
