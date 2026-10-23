package googlecontacts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
)

// The grants a Google Contacts Worker accepts, the same two the Google Sheets Worker
// Type accepts (ADR-0235): a service account acting as a Workspace user, or a refresh
// token a person granted once.
const (
	MethodServiceAccount = "serviceAccount"
	MethodRefreshToken   = "refreshToken"
)

// DefaultBaseURL is the People API; DefaultTokenURL is Google's token endpoint.
const (
	DefaultBaseURL  = "https://people.googleapis.com"
	DefaultTokenURL = "https://oauth2.googleapis.com/token"
)

// ScopeContacts is the scope a service account asks for when its bundle names none. It
// reads and writes the user's contacts and labels, which is what every operation but
// search-directory needs; that one also needs [ScopeDirectory], which the bundle then
// names beside it. It is not asked for by default: domain-wide delegation grants a
// service account exactly the scopes an administrator listed, and asking for one more
// than that fails the token request for every operation, not only the directory search.
const (
	ScopeContacts  = "https://www.googleapis.com/auth/contacts"
	ScopeDirectory = "https://www.googleapis.com/auth/directory.readonly"
)

// Bundle is what a Google Contacts Worker's vault entry holds: which address book, and
// as whom.
//
// A service account in a Google Workspace organization:
//
//	{"method": "serviceAccount", "clientEmail": "atlas@projekt.iam.gserviceaccount.com",
//	 "privateKey": "-----BEGIN PRIVATE KEY-----…", "subject": "verkauf@example.ch"}
//
// The subject is required, not optional as it is for Google Sheets: a service account
// has no address book of its own, so without one it would read an empty list and
// report it as "no such customer". The address book is the subject's, reached through
// domain-wide delegation of the contacts scope to the service account's client id.
//
// A consumer account (gmail.com), which has no domain-wide delegation:
//
//	{"method": "refreshToken", "clientId": "…", "clientSecret": "…", "refreshToken": "…"}
//
// "scope" and "tokenUrl" override Google's defaults. Field names follow the JSON key
// file Google hands out (client_email, private_key), camel-cased like the Google Sheets
// bundle's, so one transcription serves both.
type Bundle struct {
	Method       string `json:"method"`
	ClientEmail  string `json:"clientEmail,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	Subject      string `json:"subject,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TokenURL     string `json:"tokenUrl,omitempty"`
}

// ParseBundle reads a bundle. An unknown field is refused, for Exchange's reason: a
// misspelt "subjekt" would otherwise read as a service account with no subject, and the
// message would name the wrong thing.
func ParseBundle(raw string) (Bundle, error) {
	if strings.TrimSpace(raw) == "" {
		return Bundle{}, fmt.Errorf("googlecontacts: the credential bundle is empty")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		// The decoder's message for an unknown field names the field and nothing else,
		// so it is passed on; any other failure could quote a fragment of the key.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return Bundle{}, fmt.Errorf("googlecontacts: the credential bundle is not a JSON object (syntax error at byte %d)", syn.Offset)
		}
		if strings.Contains(err.Error(), "unknown field") {
			return Bundle{}, fmt.Errorf("googlecontacts: the credential bundle has an %s", strings.TrimPrefix(err.Error(), "json: "))
		}
		return Bundle{}, fmt.Errorf("googlecontacts: the credential bundle is not a JSON object of the expected shape")
	}
	return b, nil
}

// Options are the parts of a client that belong to the host or the test rather than to
// the Worker.
type Options struct {
	// HTTPClient reaches Google; nil is the shared bounded one.
	HTTPClient *http.Client
	// Now is the service-account grant's clock; nil is time.Now.
	Now func() time.Time
}

// NewClient builds the client a Worker's endpoint and bundle describe. endpoint is the
// Worker's own address: empty for the People API, or a base URL for an operator behind
// a proxy.
//
// Nothing here reaches the network — a token is fetched on the first call — so a Worker
// that builds is one whose configuration is complete, not one known to work.
func NewClient(endpoint, raw string, opts Options) (*Client, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("googlecontacts: the Worker's endpoint %q must be an https URL (empty for %s)", endpoint, DefaultBaseURL)
	}
	httpc := opts.HTTPClient
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	tokenURL := strings.TrimSpace(b.TokenURL)
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	var fetch oauth2.Fetcher
	switch b.Method {
	case MethodServiceAccount:
		for field, set := range map[string]bool{"clientId": b.ClientID != "", "clientSecret": b.ClientSecret != "", "refreshToken": b.RefreshToken != ""} {
			if set {
				return nil, fmt.Errorf("googlecontacts: %s belongs to a refreshToken bundle, not to a serviceAccount one", field)
			}
		}
		if b.ClientEmail == "" || b.PrivateKey == "" {
			return nil, fmt.Errorf("googlecontacts: serviceAccount needs clientEmail and privateKey " +
				"(the client_email and private_key of the service account's JSON key file)")
		}
		if strings.TrimSpace(b.Subject) == "" {
			return nil, fmt.Errorf("googlecontacts: serviceAccount needs a subject, the Workspace user whose contacts this Worker reads; " +
				"a service account has no address book of its own (grant it domain-wide delegation for the contacts scope)")
		}
		key, err := oauth2.ParseRSAPrivateKey("googlecontacts", b.PrivateKey)
		if err != nil {
			return nil, err
		}
		scope := strings.TrimSpace(b.Scope)
		if scope == "" {
			scope = ScopeContacts
		}
		fetch = oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
			HTTPClient: httpc, Kind: "googlecontacts", TokenURL: tokenURL,
			ClientEmail: b.ClientEmail, PrivateKey: key, Scope: scope, Subject: strings.TrimSpace(b.Subject),
		}, opts.Now)
	case MethodRefreshToken:
		for field, set := range map[string]bool{"clientEmail": b.ClientEmail != "", "privateKey": b.PrivateKey != "", "subject": b.Subject != ""} {
			if set {
				return nil, fmt.Errorf("googlecontacts: %s belongs to a serviceAccount bundle, not to a refreshToken one", field)
			}
		}
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("googlecontacts: refreshToken needs clientId, clientSecret and refreshToken")
		}
		// No default scope: a refresh token carries the scopes its consent granted, and
		// asking for any other one is refused as invalid_scope. A bundle that names one
		// narrows the token to it.
		fetch = oauth2.RefreshToken(oauth2.Config{
			HTTPClient: httpc, Kind: "googlecontacts", TokenURL: tokenURL,
			ClientID: b.ClientID, ClientSecret: b.ClientSecret, RefreshToken: b.RefreshToken,
			Scope: strings.TrimSpace(b.Scope),
		})
	default:
		return nil, fmt.Errorf("googlecontacts: unknown auth method %q (want %q or %q)", b.Method, MethodServiceAccount, MethodRefreshToken)
	}
	return &Client{tokens: oauth2.NewCached(fetch, opts.Now), base: base, http: httpc}, nil
}

// CheckBundle reports whether a Worker's endpoint and bundle build a client at all —
// the question supervision asks before it hands a bundle to a worker, answered by the
// same code the worker then builds with.
func CheckBundle(endpoint, raw string) error {
	_, err := NewClient(endpoint, raw, Options{})
	return err
}
