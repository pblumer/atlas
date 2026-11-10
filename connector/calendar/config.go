package calendar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Providers a Worker's bundle may name.
const (
	// ProviderGraph is Microsoft 365 / Exchange Online through Microsoft Graph.
	ProviderGraph = "microsoft-graph"
	// ProviderMock is the in-memory calendars of [MockCalendars]: the worker's switch,
	// not the model's (ADR-0221), so a model runs unchanged against either.
	ProviderMock = "mock"
)

// Providers lists the providers this build implements.
var Providers = []string{ProviderGraph, ProviderMock}

// Bundle is what a calendar Worker's vault entry holds: which provider, and as whom.
//
//	{"provider": "microsoft-graph", "tenantId": "…", "clientId": "…", "clientSecret": "…"}
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
	switch b.Provider {
	case ProviderGraph:
		if b.Calendars != nil {
			return nil, fmt.Errorf("calendar: calendars belong to the mock provider; a Microsoft Graph Worker reads them from the tenant")
		}
		return newGraph(endpoint, b, opts)
	case ProviderMock:
		if b.TenantID != "" || b.ClientID != "" || b.ClientSecret != "" {
			return nil, fmt.Errorf("calendar: the mock provider takes no credential; remove tenantId, clientId and clientSecret")
		}
		if strings.TrimSpace(endpoint) != "" {
			return nil, fmt.Errorf("calendar: the mock provider has no endpoint; leave it empty")
		}
		return NewMockCalendars(b.Calendars), nil
	case "":
		return nil, fmt.Errorf("calendar: the bundle names no provider (want %s)", strings.Join(Providers, ", "))
	}
	return nil, fmt.Errorf("calendar: the bundle names the provider %q, which this build does not implement (want %s)", b.Provider, strings.Join(Providers, ", "))
}

// CheckBundle reports whether an endpoint and a bundle build a backend. It is the one
// reading of the two, used by the worker at startup and by the server before it hands a
// Worker to a supervised worker, so the two cannot disagree.
func CheckBundle(endpoint, raw string) error {
	_, err := NewBackend(endpoint, raw, Options{})
	return err
}
