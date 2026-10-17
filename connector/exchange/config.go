package exchange

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
)

// Bundle is what an Exchange Worker's vault entry holds: everything that says which
// organization the Worker administers and as whom. One shape serves both targets; the
// fields of the other target are refused rather than ignored, because a password in an
// online bundle is an operator who meant to configure something else.
//
// Exchange Online:
//
//	{"organization": "contoso.onmicrosoft.com", "clientId": "…",
//	 "certificate": "-----BEGIN CERTIFICATE-----…-----BEGIN PRIVATE KEY-----…"}
//
// with "clientSecret" in place of "certificate" where a tenant accepts one, and the
// optional "tenantId", "authorityUrl" (a sovereign cloud) and "anchorMailbox".
//
// Exchange Server:
//
//	{"target": "onprem", "authentication": "Kerberos",
//	 "username": "CONTOSO\\svc-atlas", "password": "…"}
//
// where the Worker's endpoint is the server's Remote PowerShell URI ("connectionUri"
// here does the same for an operator who keeps it all in one place), the account is
// optional — without one the session runs as the worker's own identity — and
// "timeoutSeconds" bounds one call.
type Bundle struct {
	Target string `json:"target,omitempty"`

	Organization  string `json:"organization,omitempty"`
	TenantID      string `json:"tenantId,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	Certificate   string `json:"certificate,omitempty"`
	AuthorityURL  string `json:"authorityUrl,omitempty"`
	AnchorMailbox string `json:"anchorMailbox,omitempty"`

	ConnectionURI  string `json:"connectionUri,omitempty"`
	Authentication string `json:"authentication,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// ParseBundle reads a bundle. An unknown field is refused: a misspelt clientSecret
// would otherwise read as a bundle with no credential, and the error would point
// everywhere but at the typo.
func ParseBundle(raw string) (Bundle, error) {
	if strings.TrimSpace(raw) == "" {
		return Bundle{}, fmt.Errorf("exchange: the credential bundle is empty")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		// The decoder's message names a field for an unknown one and quotes nothing
		// else, so it is safe to pass on; a syntax error is reported without it, since
		// it could quote a fragment of a secret.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return Bundle{}, fmt.Errorf("exchange: the credential bundle is not a JSON object (syntax error at byte %d)", syn.Offset)
		}
		if strings.Contains(err.Error(), "unknown field") {
			return Bundle{}, fmt.Errorf("exchange: the credential bundle has an %s", strings.TrimPrefix(err.Error(), "json: "))
		}
		return Bundle{}, fmt.Errorf("exchange: the credential bundle is not a JSON object of the expected shape")
	}
	return b, nil
}

// Options are the parts of a backend that belong to the host rather than to the
// Worker.
type Options struct {
	// HTTPClient is the online backend's client; nil is the shared bounded one.
	HTTPClient *http.Client
	// PowerShell is the interpreter the on-premises backend starts; empty is pwsh.
	PowerShell string
	// Now is the certificate grant's clock; nil is time.Now.
	Now func() time.Time
}

// NewBackend builds the backend a Worker's endpoint and bundle describe. endpoint is the
// Worker's own address: for Exchange Online an optional base URL (a sovereign cloud),
// for Exchange Server the Remote PowerShell URI.
//
// Nothing here reaches the network — a token is fetched on the first call — so a
// Worker that builds is one whose configuration is complete, not one that is known to
// work.
func NewBackend(endpoint, raw string, opts Options) (Backend, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	target, err := catalog.ParseTarget(b.Target)
	if err != nil {
		return nil, err
	}
	if target == catalog.OnPrem {
		return onPremBackend(endpoint, b, opts)
	}
	return onlineBackend(endpoint, b, opts)
}

// CheckBundle reports whether a Worker's endpoint and bundle build a backend at all —
// the question supervision asks before it hands a bundle to a worker, answered by the
// same code the worker then builds with.
func CheckBundle(endpoint, raw string) error {
	_, err := NewBackend(endpoint, raw, Options{})
	return err
}

func onlineBackend(endpoint string, b Bundle, opts Options) (Backend, error) {
	for field, set := range map[string]bool{
		"connectionUri": b.ConnectionURI != "", "authentication": b.Authentication != "",
		"username": b.Username != "", "password": b.Password != "", "timeoutSeconds": b.TimeoutSeconds != 0,
	} {
		if set {
			return nil, fmt.Errorf("exchange: %s belongs to an on-premises Worker; set \"target\": \"onprem\" or remove it", field)
		}
	}
	org := strings.TrimSpace(b.Organization)
	if org == "" {
		return nil, fmt.Errorf("exchange: an Exchange Online Worker needs its organization, the tenant's initial domain (contoso.onmicrosoft.com)")
	}
	if strings.TrimSpace(b.ClientID) == "" {
		return nil, fmt.Errorf("exchange: an Exchange Online Worker needs the clientId of its app registration")
	}
	hasSecret, hasCert := b.ClientSecret != "", strings.TrimSpace(b.Certificate) != ""
	switch {
	case !hasSecret && !hasCert:
		return nil, fmt.Errorf("exchange: an Exchange Online Worker needs a clientSecret or a certificate")
	case hasSecret && hasCert:
		return nil, fmt.Errorf("exchange: the bundle carries a clientSecret and a certificate; give one, not both")
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" {
		base = DefaultOnlineBaseURL
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("exchange: the Worker's endpoint %q must be an https URL (empty for %s)", endpoint, DefaultOnlineBaseURL)
	}
	authority := strings.TrimRight(strings.TrimSpace(b.AuthorityURL), "/")
	if authority == "" {
		authority = DefaultAuthorityURL
	}
	tenant := strings.TrimSpace(b.TenantID)
	if tenant == "" {
		tenant = org
	}
	httpc := opts.HTTPClient
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	tokenURL := authority + "/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
	// The scope is the endpoint's own .default: Exchange.ManageAsApp is an application
	// permission on that resource, and a sovereign cloud's resource is its own host.
	scope := base + "/.default"
	var fetch oauth2.Fetcher
	if hasCert {
		cert, key, err := oauth2.ParseCertificateBundle("exchange", b.Certificate)
		if err != nil {
			return nil, err
		}
		fetch = oauth2.ClientCertificate(oauth2.CertificateConfig{
			HTTPClient: httpc, Kind: "exchange", TokenURL: tokenURL, ClientID: b.ClientID,
			Certificate: cert, PrivateKey: key, Scope: scope,
		}, opts.Now)
	} else {
		fetch = oauth2.ClientCredentials(oauth2.Config{
			HTTPClient: httpc, Kind: "exchange", TokenURL: tokenURL, ClientID: b.ClientID,
			ClientSecret: b.ClientSecret, Scope: scope,
		})
	}
	return NewOnlineClient(OnlineConfig{
		Tokens: oauth2.NewCached(fetch, opts.Now), BaseURL: base, Organization: org,
		AnchorMailbox: b.AnchorMailbox, HTTPClient: httpc,
	}), nil
}

func onPremBackend(endpoint string, b Bundle, opts Options) (Backend, error) {
	for field, set := range map[string]bool{
		"organization": b.Organization != "", "tenantId": b.TenantID != "", "clientId": b.ClientID != "",
		"clientSecret": b.ClientSecret != "", "certificate": b.Certificate != "",
		"authorityUrl": b.AuthorityURL != "", "anchorMailbox": b.AnchorMailbox != "",
	} {
		if set {
			return nil, fmt.Errorf("exchange: %s belongs to an Exchange Online Worker, not to an on-premises one", field)
		}
	}
	if b.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("exchange: timeoutSeconds must not be negative")
	}
	uri := strings.TrimSpace(endpoint)
	if uri == "" {
		uri = b.ConnectionURI
	}
	return NewOnPremClient(OnPremConfig{
		ConnectionURI: uri, Authentication: b.Authentication, Username: b.Username, Password: b.Password,
		PowerShell: opts.PowerShell, Timeout: time.Duration(b.TimeoutSeconds) * time.Second,
	})
}
