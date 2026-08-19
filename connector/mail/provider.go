package mail

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Provider identifiers for a managed mail connector. SMTP (the default) reaches any
// submission server; Gmail and Microsoft are the native provider APIs (ADR-0079/0081).
// [ProviderPreview], declared beside its outbox, is the fourth: it frames a message
// like the others and delivers it in-server instead of sending it (ADR-0149).
const (
	ProviderSMTP      = "smtp"
	ProviderGmail     = "gmail"
	ProviderMicrosoft = "microsoft"
)

// Default OAuth2 token endpoints and scopes per native provider, used when a
// credential bundle does not override them.
const (
	gmailTokenURL = "https://oauth2.googleapis.com/token"
	gmailScope    = "https://www.googleapis.com/auth/gmail.send"
	graphScope    = "https://graph.microsoft.com/.default"
)

// ProviderConfig is the per-connector data the server resolves before building a
// client: the provider, an optional endpoint override, the default sender, and the
// resolved Secret — an SMTP password, or (for a native provider) the OAuth credential
// JSON bundle held in the vault under the connector's credentialsRef (ADR-0093). The
// secret lives only here at build time, never in a model or an event (I6).
//
// Name and Outbox serve the preview provider (ADR-0149), which delivers into the
// server's outbox under the connector's own name; every other provider ignores them.
type ProviderConfig struct {
	Provider string
	Endpoint string
	Sender   string
	Secret   string
	Name     string
	Outbox   *Outbox
}

// NewProviderClient builds the mail client for a managed connector, dispatching on its
// provider. SMTP is the default; Gmail and Microsoft Graph parse the credential bundle
// and build an OAuth token source. A misconfigured connector returns an error so the
// caller can skip it (its tasks park) rather than sending wrongly. This is the single
// place a new provider is added.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", ProviderSMTP:
		// Normalized here, not only where a connector is saved: a record written
		// before the endpoint was checked at all (or through a partial update) would
		// otherwise keep failing deep in the send, one parked token at a time.
		endpoint, err := NormalizeSMTPEndpoint(cfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("mail: %w", err)
		}
		return NewSMTPClient(Connector{
			Endpoint: endpoint,
			Username: cfg.Sender,
			Password: cfg.Secret,
			From:     cfg.Sender,
		}), nil
	case ProviderPreview:
		if cfg.Outbox == nil {
			return nil, fmt.Errorf("mail: preview connector %q has no outbox", cfg.Name)
		}
		return NewPreviewClient(cfg.Outbox, cfg.Name, cfg.Sender), nil
	case ProviderGmail:
		tokens, err := oauthTokenSource(ProviderGmail, cfg)
		if err != nil {
			return nil, err
		}
		return NewGmailClient(tokens, cfg.Endpoint, cfg.Sender), nil
	case ProviderMicrosoft:
		tokens, err := oauthTokenSource(ProviderMicrosoft, cfg)
		if err != nil {
			return nil, err
		}
		return NewGraphClient(tokens, cfg.Endpoint, cfg.Sender), nil
	default:
		return nil, fmt.Errorf("mail: unknown provider %q (want %q, %q, %q, or %q)", cfg.Provider, ProviderSMTP, ProviderGmail, ProviderMicrosoft, ProviderPreview)
	}
}

// oauthTokenSource parses a native provider's credential bundle from the resolved
// secret, applies the provider's token-endpoint and scope defaults, and builds a
// cached token source.
func oauthTokenSource(provider string, cfg ProviderConfig) (TokenSource, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, fmt.Errorf("mail: %s connector has no credential (set credentialsRef to a JSON auth bundle in the vault)", provider)
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(cfg.Secret), &b); err != nil {
		return nil, fmt.Errorf("mail: %s credential is not valid JSON: %w", provider, err)
	}
	applyProviderDefaults(provider, &b, cfg.Sender)
	return newTokenSource(b, http.DefaultClient, nil)
}

// applyProviderDefaults fills a credential bundle's token URL, scope, and (for a Gmail
// service account) impersonation subject from the provider's conventions when the
// bundle leaves them unset, so an operator only specifies the parts that vary.
func applyProviderDefaults(provider string, b *credentialBundle, sender string) {
	switch provider {
	case ProviderGmail:
		if b.TokenURL == "" {
			b.TokenURL = gmailTokenURL
		}
		if b.Scope == "" {
			b.Scope = gmailScope
		}
		// A service account impersonates the sender mailbox unless the bundle names
		// another subject.
		if b.Method == methodServiceAccount && b.Subject == "" {
			b.Subject = sender
		}
	case ProviderMicrosoft:
		if b.TokenURL == "" && b.TenantID != "" {
			b.TokenURL = "https://login.microsoftonline.com/" + url.PathEscape(b.TenantID) + "/oauth2/v2.0/token"
		}
		if b.Scope == "" {
			b.Scope = graphScope
		}
	}
}
