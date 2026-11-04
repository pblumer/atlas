package sharepoint

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// graphScope is the default OAuth2 scope a SharePoint worker requests when a
// credential bundle does not override it: the app's configured Graph permissions
// (e.g. Sites.ReadWrite.All) via the ".default" scope.
const graphScope = "https://graph.microsoft.com/.default"

// ProviderConfig is the per-connector data the server resolves before building a
// client: an optional Graph base override (Endpoint) and the resolved Secret — the
// OAuth credential JSON bundle held in the vault under the worker's credentialsRef
// (ADR-0141). The secret lives only here at build time, never in a model or an event
// (I6).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// NewProviderClient builds the SharePoint client for a managed worker: it parses
// the credential bundle, applies the Graph token-endpoint and scope defaults, builds
// an OAuth token source, and returns a Graph client. A misconfigured worker
// returns an error so the caller can skip it (its tasks park) rather than acting
// wrongly. This is the single place a provider variant would be added.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	b, err := readBundle(cfg)
	if err != nil {
		return nil, err
	}
	httpc := nettimeout.HTTPClient()
	tokens, err := newTokenSource(b, httpc, nil)
	if err != nil {
		return nil, err
	}
	c := NewGraphClient(tokens, cfg.Endpoint)
	c.admin = adminAccessFor(b, httpc)
	return c, nil
}

// readBundle parses a worker's credential bundle from the resolved secret and applies
// the Graph token-endpoint and scope defaults.
func readBundle(cfg ProviderConfig) (credentialBundle, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return credentialBundle{}, fmt.Errorf("sharepoint: worker has no credential (set credentialsRef to a JSON auth bundle in the vault)")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(cfg.Secret), &b); err != nil {
		return credentialBundle{}, fmt.Errorf("sharepoint: credential is not valid JSON: %w", err)
	}
	applyGraphDefaults(&b)
	return b, nil
}

// adminAccess is how a client reaches SharePoint's tenant administration for
// set-site-quota (ADR-0141, amended 2026-11-04): the same app and certificate as for
// Graph, with a token for the admin host instead. url is the admin host the credential
// names (adminUrl), empty to derive it from the site. A token source is built per admin
// host on first use and kept, so a quota costs a token request once an hour, not once a
// job.
type adminAccess struct {
	url       string
	newTokens func(scope string) (TokenSource, error)

	mu     sync.Mutex
	tokens map[string]TokenSource
}

// adminAccessFor answers the tenant-administration access a credential bundle allows:
// none for a client secret or a refresh token, because SharePoint's own APIs refuse an
// app-only token that was not signed with a certificate — sending one would only earn
// a 401 that reads like a missing permission.
func adminAccessFor(b credentialBundle, httpc *http.Client) *adminAccess {
	if b.Method != "" && b.Method != methodClientCredentials {
		return nil
	}
	if strings.TrimSpace(b.Certificate) == "" && strings.TrimSpace(b.CertificateThumbprint) == "" {
		return nil
	}
	return &adminAccess{
		url: strings.TrimSpace(b.AdminURL),
		newTokens: func(scope string) (TokenSource, error) {
			admin := b
			admin.Scope = scope
			return newTokenSource(admin, httpc, nil)
		},
	}
}

// tokensFor answers the token source for one admin host.
func (a *adminAccess) tokensFor(base string) (TokenSource, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ts, ok := a.tokens[base]; ok {
		return ts, nil
	}
	ts, err := a.newTokens(base + "/.default")
	if err != nil {
		return nil, err
	}
	if a.tokens == nil {
		a.tokens = map[string]TokenSource{}
	}
	a.tokens[base] = ts
	return ts, nil
}

// CertificateThumbprint returns the thumbprint a credential bundle names in its
// certificateThumbprint field, or "" when it names none or is not a bundle at all. It
// is how code that hands a bundle on without building a client from it — the worker's
// startup check, the supervised worker's environment — asks whether the bundle depends
// on this host's certificate store, without deciding the bundle's shape a second time.
func CertificateThumbprint(secret string) string {
	var b credentialBundle
	if err := json.Unmarshal([]byte(secret), &b); err != nil {
		return ""
	}
	return strings.TrimSpace(b.CertificateThumbprint)
}

// applyGraphDefaults fills a credential bundle's token URL (from the tenant) and
// scope from Graph conventions when the bundle leaves them unset, so an operator only
// specifies the parts that vary.
func applyGraphDefaults(b *credentialBundle) {
	if b.TokenURL == "" && b.TenantID != "" {
		b.TokenURL = "https://login.microsoftonline.com/" + url.PathEscape(b.TenantID) + "/oauth2/v2.0/token"
	}
	if b.Scope == "" {
		b.Scope = graphScope
	}
}
