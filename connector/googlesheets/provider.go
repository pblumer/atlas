package googlesheets

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// ProviderConfig is the per-Worker data the server resolves before building a client:
// an optional Sheets API base override (Endpoint, for an operator behind a proxy) and
// the resolved Secret — the OAuth credential JSON bundle held in the vault under the
// Worker's credentialsRef. The secret lives only here at build time, never in a model
// or an event (I6).
type ProviderConfig struct {
	Endpoint string
	Secret   string
}

// NewProviderClient builds the Google client for one configured Worker: it parses the
// credential bundle, applies Google's token-endpoint and scope defaults, builds a
// token source, and returns a client speaking Sheets v4 and Drive v3. A misconfigured
// Worker returns an error so the caller can skip it — its tasks then park with a
// reason (ADR-0158) rather than acting wrongly.
func NewProviderClient(cfg ProviderConfig) (Client, error) {
	tokens, err := tokenSource(cfg)
	if err != nil {
		return nil, err
	}
	return NewHTTPClient(Account{Tokens: tokens, SheetsBase: cfg.Endpoint}), nil
}

// tokenSource parses a Worker's credential bundle from the resolved secret, applies
// Google's defaults, and builds a cached token source.
func tokenSource(cfg ProviderConfig) (TokenSource, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, fmt.Errorf("googlesheets: this Worker has no credential " +
			"(set credentialsRef to a JSON auth bundle in the vault)")
	}
	var b credentialBundle
	if err := json.Unmarshal([]byte(cfg.Secret), &b); err != nil {
		return nil, fmt.Errorf("googlesheets: credential is not valid JSON: %w", err)
	}
	applyGoogleDefaults(&b)
	return newTokenSource(b, nettimeout.HTTPClient(), nil)
}

// applyGoogleDefaults fills the token endpoint and scope a bundle leaves unset, so an
// operator specifies only the parts that vary — which, for the service-account bundle
// that is the normal case, is the two fields copied out of the key file.
func applyGoogleDefaults(b *credentialBundle) {
	if b.TokenURL == "" {
		b.TokenURL = googleTokenURL
	}
	if b.Scope == "" {
		b.Scope = googleScope
	}
}
