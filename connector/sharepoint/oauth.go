package sharepoint

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// TokenSource yields a valid OAuth2 bearer access token for the Graph API. The
// mechanism — caching, refresh timing, the token exchange — is the shared
// [oauth2] package's; what stays here is this worker's policy: which grants it
// accepts and what its credential bundle looks like.
type TokenSource = oauth2.TokenSource

// OAuth2 grant methods a SharePoint worker supports (ADR-0141). clientCredentials
// is app-only (a confidential client acts as itself, the norm for server workflows),
// proving itself with a client secret or with a certificate; refreshToken exchanges a
// pre-obtained refresh token (works for delegated / consumer scenarios). The
// service-account (JWT-bearer) grant is Google-specific and omitted here.
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
)

// credentialBundle is the JSON an operator stores in the vault under a SharePoint
// worker's credentialsRef (ADR-0141). method selects the OAuth2 grant — empty is
// clientCredentials, the one a server workflow uses; the remaining fields configure
// it. Non-secret fields (ids, tenant) and secret fields (clientSecret, certificate,
// refreshToken) live together in this one vault secret, so a model never carries any
// of them (I6). tokenUrl and scope are optional overrides; the worker supplies
// sensible Graph defaults (from tenantId).
//
// certificate is the app's certificate and its unencrypted private key as one PEM
// text, in place of clientSecret: the credential Microsoft recommends for an app that
// runs unattended, because the private key never leaves this host — what the token
// endpoint sees is an assertion signed with it (ADR-0462's grant, in the shared oauth2
// package).
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Certificate  string `json:"certificate,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the worker has already filled in tokenUrl/scope defaults). It validates the
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
	hasCert := strings.TrimSpace(b.Certificate) != ""
	var f oauth2.Fetcher
	switch b.Method {
	case methodClientCredentials, "":
		switch {
		case b.ClientID == "":
			return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and a clientSecret or a certificate")
		case hasCert && b.ClientSecret != "":
			return nil, fmt.Errorf("sharepoint: the bundle carries a clientSecret and a certificate; give one, not both")
		case hasCert:
			cert, key, err := oauth2.ParseCertificateBundle("sharepoint", b.Certificate)
			if err != nil {
				return nil, err
			}
			f = oauth2.ClientCertificate(oauth2.CertificateConfig{
				HTTPClient: httpc, Kind: "sharepoint", TokenURL: b.TokenURL, ClientID: b.ClientID,
				Certificate: cert, PrivateKey: key, Scope: b.Scope,
			}, now)
		case b.ClientSecret == "":
			return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and a clientSecret or a certificate")
		default:
			f = oauth2.ClientCredentials(cfg)
		}
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("sharepoint: refreshToken needs clientId, clientSecret and refreshToken")
		}
		if hasCert {
			return nil, fmt.Errorf("sharepoint: a certificate belongs to the clientCredentials method, not to refreshToken")
		}
		f = oauth2.RefreshToken(cfg)
	default:
		return nil, fmt.Errorf("sharepoint: unknown auth method %q (want %q or %q)", b.Method, methodClientCredentials, methodRefreshToken)
	}
	return oauth2.NewCached(f, now), nil
}
