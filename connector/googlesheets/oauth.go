package googlesheets

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// TokenSource yields a valid OAuth2 bearer access token for the Sheets and Drive APIs.
// The mechanism — caching, refresh timing, the token exchange, the JWT-bearer
// assertion — is the shared [oauth2] package's; what stays here is this connector's
// policy: which grants it accepts and what its credential bundle looks like.
type TokenSource = oauth2.TokenSource

// OAuth2 grant methods a Google connector supports
// (ADR-draft-google-sheets-worker). serviceAccount is the normal shape for a server
// workflow: a service account signs a JWT-bearer assertion with its own key and acts
// as itself, or — with subject — as a Workspace user through domain-wide delegation.
// refreshToken exchanges a pre-obtained refresh token, which is how a consumer Google
// account is reached without a browser.
//
// The client-credentials grant the Microsoft connectors use has no counterpart here:
// Google does not issue app-only tokens that way, and offering the name would only
// produce a confusing failure at the token endpoint.
const (
	methodServiceAccount = "serviceAccount"
	methodRefreshToken   = "refreshToken"
)

// googleTokenURL is Google's OAuth2 token endpoint, and googleScope the scopes a
// bundle gets when it names none: the spreadsheet half and the file half, which is
// what the eight operations between them need.
//
// An operator who wants less says so in the bundle. `drive.file` in place of `drive`
// is the common narrowing — it grants access only to files the credential itself
// created, which is enough when the process creates every spreadsheet it touches and
// nothing when a person shares one with it.
const (
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleScope    = "https://www.googleapis.com/auth/spreadsheets https://www.googleapis.com/auth/drive"
)

// credentialBundle is the JSON an operator stores in the vault under a Google
// connector's credentialsRef. method selects the OAuth2 grant; the remaining fields
// configure it. Non-secret fields (the account address, the impersonated subject) and
// secret ones (privateKey, clientSecret, refreshToken) live together in this one vault
// secret, so a model never carries any of them (I6). tokenUrl and scope are optional
// overrides; the connector supplies Google's defaults.
//
// The field names are deliberately those of the JSON key file Google hands out —
// client_email and private_key, camel-cased — so an operator transcribing one is
// copying rather than translating.
type credentialBundle struct {
	Method       string `json:"method"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ClientEmail  string `json:"clientEmail,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	Subject      string `json:"subject,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// newTokenSource builds a cached TokenSource for a fully-resolved credential bundle
// (the connector has already filled in the tokenUrl and scope defaults). It validates
// the fields the chosen method requires, so a misconfigured bundle fails here — where
// the operator is looking — rather than on the first job that reaches Google. httpc and
// now are injected so the token flow is testable without a live endpoint.
func newTokenSource(b credentialBundle, httpc *http.Client, now func() time.Time) (TokenSource, error) {
	if strings.TrimSpace(b.TokenURL) == "" {
		return nil, fmt.Errorf("googlesheets: credential has no token URL")
	}
	var f oauth2.Fetcher
	switch b.Method {
	case methodServiceAccount:
		if b.ClientEmail == "" || b.PrivateKey == "" {
			return nil, fmt.Errorf("googlesheets: serviceAccount needs clientEmail and privateKey " +
				"(the client_email and private_key of the service account's JSON key file)")
		}
		key, err := oauth2.ParseRSAPrivateKey("googlesheets", b.PrivateKey)
		if err != nil {
			return nil, err
		}
		f = oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
			HTTPClient: httpc, Kind: "googlesheets", TokenURL: b.TokenURL,
			ClientEmail: b.ClientEmail, PrivateKey: key, Scope: b.Scope, Subject: b.Subject,
		}, now)
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("googlesheets: refreshToken needs clientId, clientSecret and refreshToken")
		}
		f = oauth2.RefreshToken(oauth2.Config{
			HTTPClient: httpc, Kind: "googlesheets", TokenURL: b.TokenURL,
			ClientID: b.ClientID, ClientSecret: b.ClientSecret, RefreshToken: b.RefreshToken, Scope: b.Scope,
		})
	default:
		return nil, fmt.Errorf("googlesheets: unknown auth method %q (want %q or %q)",
			b.Method, methodServiceAccount, methodRefreshToken)
	}
	return oauth2.NewCached(f, now), nil
}
