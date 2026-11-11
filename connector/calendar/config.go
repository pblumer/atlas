package calendar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Providers a Worker's bundle may name.
const (
	// ProviderGraph is Microsoft 365 / Exchange Online through Microsoft Graph.
	ProviderGraph = "microsoft-graph"
	// ProviderGoogle is Google Workspace through the Google Calendar API.
	ProviderGoogle = "google-calendar"
	// ProviderCalDAV is any CalDAV server (RFC 4791): Nextcloud, iCloud, Fastmail,
	// Synology, SOGo, Radicale, Baïkal, most hosting providers.
	ProviderCalDAV = "caldav"
	// ProviderEWS is Exchange Server on premises through Exchange Web Services, which
	// Microsoft Graph does not reach.
	ProviderEWS = "exchange-ews"
	// ProviderMock is the in-memory calendars of [MockCalendars]: the worker's switch,
	// not the model's (ADR-0221), so a model runs unchanged against either.
	ProviderMock = "mock"
)

// Providers lists the providers this build implements.
var Providers = []string{ProviderGraph, ProviderGoogle, ProviderCalDAV, ProviderEWS, ProviderMock}

// Bundle is what a calendar Worker's vault entry holds: which provider, and as whom.
//
//	{"provider": "microsoft-graph", "tenantId": "…", "clientId": "…", "clientSecret": "…"}
//	{"provider": "google-calendar", "clientEmail": "…@….iam.gserviceaccount.com", "privateKey": "-----BEGIN PRIVATE KEY-----…"}
//	{"provider": "caldav", "username": "verkauf@firma.ch", "password": "app-specific password", "timeZone": "Europe/Zurich"}
//	{"provider": "exchange-ews", "username": "CONTOSO\\svc-booking", "password": "…", "authentication": "ntlm"}
//	{"provider": "mock", "calendars": {"werkstatt@firma.ch": {"busy": [ … ]}}}
//
// The provider is the Worker's, not the model's: a booking process names a Worker and a
// calendar, and which kind of calendar answers is decided where the credential is.
type Bundle struct {
	Provider string `json:"provider"`

	// Microsoft Graph: an app registration's client-credentials grant. Its
	// application permission Calendars.ReadWrite reaches every mailbox of the tenant
	// unless Exchange Online restricts it to the booking calendars; the setup must.
	TenantID     string `json:"tenantId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`

	// Google Calendar: a service account's key. The booking calendars are shared with
	// the service account ("make changes to events"); no domain-wide delegation is
	// needed for that. Subject, when set, makes the service account act as that
	// Workspace user through domain-wide delegation — which is what Google requires
	// before a service account may invite attendees.
	ClientEmail string `json:"clientEmail,omitempty"`
	PrivateKey  string `json:"privateKey,omitempty"`
	Subject     string `json:"subject,omitempty"`

	// CalDAV: the account's username and password — an app-specific password where the
	// provider issues one — and the zone a floating or all-day time is read in, an IANA
	// name, UTC when empty. The endpoint is the calendar home.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`

	// Exchange Web Services: the service account's username and password, the same two
	// fields as CalDAV's, and how they are sent — "ntlm" (the default) or "basic". The
	// endpoint is the server's EWS URL.
	Authentication string `json:"authentication,omitempty"`

	// Mock: the calendars and what they already hold.
	Calendars map[string]MockCalendarSpec `json:"calendars,omitempty"`
}

// ParseBundle reads a bundle. An unknown field is refused, so a misspelt key is an
// error at configuration, not a Worker that behaves as if the key were absent.
func ParseBundle(raw string) (Bundle, error) {
	if strings.TrimSpace(raw) == "" {
		return Bundle{}, fmt.Errorf("calendar: the credential bundle is empty")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		// The decoder's message for an unknown field names the field and nothing else,
		// so it is passed on; any other failure could quote a fragment of the secret.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return Bundle{}, fmt.Errorf("calendar: the credential bundle is not a JSON object (syntax error at byte %d)", syn.Offset)
		}
		if strings.Contains(err.Error(), "unknown field") {
			return Bundle{}, fmt.Errorf("calendar: the credential bundle has an %s", strings.TrimPrefix(err.Error(), "json: "))
		}
		return Bundle{}, fmt.Errorf("calendar: the credential bundle is not a JSON object of the expected shape")
	}
	return b, nil
}

// Options are the parts of a backend that belong to the host or the test rather than
// to the Worker.
type Options struct {
	// HTTPClient reaches the provider; nil is the shared bounded one.
	HTTPClient *http.Client
	// TokenURL replaces the provider's token endpoint; empty is the provider's own.
	TokenURL string
}

// NewBackend builds the backend a Worker's endpoint and bundle describe. The endpoint
// is the provider's API root and optional: empty is the provider's public one.
func NewBackend(endpoint, raw string, opts Options) (Backend, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	if f := b.foreignTo(b.Provider); f != "" {
		return nil, fmt.Errorf("calendar: %s belongs to another provider's bundle, not to %q; remove it", f, b.Provider)
	}
	switch b.Provider {
	case ProviderGraph:
		return newGraph(endpoint, b, opts)
	case ProviderGoogle:
		return newGoogle(endpoint, b, opts)
	case ProviderCalDAV:
		return newCalDAV(endpoint, b, opts)
	case ProviderEWS:
		return newEWS(endpoint, b, opts)
	case ProviderMock:
		if strings.TrimSpace(endpoint) != "" {
			return nil, fmt.Errorf("calendar: the mock provider has no endpoint; leave it empty")
		}
		return NewMockCalendars(b.Calendars), nil
	case "":
		return nil, fmt.Errorf("calendar: the bundle names no provider (want %s)", strings.Join(Providers, ", "))
	}
	return nil, fmt.Errorf("calendar: the bundle names the provider %q, which this build does not implement (want %s)", b.Provider, strings.Join(Providers, ", "))
}

// fieldsOf are the bundle fields each provider takes besides provider itself.
var fieldsOf = map[string][]string{
	ProviderGraph:  {"tenantId", "clientId", "clientSecret"},
	ProviderGoogle: {"clientEmail", "privateKey", "subject"},
	ProviderCalDAV: {"username", "password", "timeZone"},
	ProviderEWS:    {"username", "password", "authentication"},
	ProviderMock:   {"calendars"},
}

// foreignTo names the first field set in the bundle that the provider does not take,
// or "" when there is none. A field of another provider is a bundle written for that
// provider, and is refused rather than ignored.
func (b Bundle) foreignTo(provider string) string {
	set := map[string]bool{
		"tenantId": b.TenantID != "", "clientId": b.ClientID != "", "clientSecret": b.ClientSecret != "",
		"clientEmail": b.ClientEmail != "", "privateKey": b.PrivateKey != "", "subject": b.Subject != "",
		"username": b.Username != "", "password": b.Password != "", "timeZone": b.TimeZone != "",
		"authentication": b.Authentication != "",
		"calendars":      b.Calendars != nil,
	}
	takes, known := fieldsOf[provider]
	if !known {
		return ""
	}
	for _, f := range []string{"tenantId", "clientId", "clientSecret", "clientEmail", "privateKey", "subject", "username", "password", "timeZone", "authentication", "calendars"} {
		if set[f] && !slices.Contains(takes, f) {
			return f
		}
	}
	return ""
}

// CheckBundle reports whether an endpoint and a bundle build a backend. It is the one
// reading of the two, used by the worker at startup and by the server before it hands a
// Worker to a supervised worker, so the two cannot disagree.
func CheckBundle(endpoint, raw string) error {
	_, err := NewBackend(endpoint, raw, Options{})
	return err
}
