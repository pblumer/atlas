package sharepoint

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// graphScope is the default OAuth2 scope a SharePoint connector requests when a
// credential bundle does not override it: the app's configured Graph permissions
// (e.g. Sites.ReadWrite.All) via the ".default" scope.
const graphScope = "https://graph.microsoft.com/.default"

// ProviderConfig is the per-connector data the server resolves before building a
// client: an optional Graph base override (Endpoint) and the resolved Secret — the
// OAuth credential JSON bundle held in the vault under the connector's credentialsRef
// (ADR-0105). The secret lives only here at build time, never in a model or an event
// (I6).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// NewProviderClient builds the SharePoint client for a managed connector: it parses
// the credential bundle, applies the Graph token-endpoint and scope defaults, builds
// an OAuth token source, and returns a Graph client. A misconfigured connector
// returns an error so the caller can skip it (its tasks park) rather than acting
// wrongly. This is the single place a provider variant would be added.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	tokens, err := oauthTokenSource(cfg)
	if err != nil {
		return nil, err
	}
	return NewGraphClient(tokens, cfg.Endpoint), nil
}

// oauthTokenSource parses a connector's credential bundle from the resolved secret,
// applies the Graph token-endpoint and scope defaults, and builds a cached token
// source.
func oauthTokenSource(cfg ProviderConfig) (TokenSource, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, fmt.Errorf("sharepoint: connector has no credential (set credentialsRef to a JSON auth bundle in the vault)")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(cfg.Secret), &b); err != nil {
		return nil, fmt.Errorf("sharepoint: credential is not valid JSON: %w", err)
	}
	applyGraphDefaults(&b)
	return newTokenSource(b, http.DefaultClient, nil)
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
