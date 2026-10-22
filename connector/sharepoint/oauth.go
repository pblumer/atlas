package sharepoint

import (
	"context"
	"crypto"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/internal/certstore"
)

// TokenSource yields a valid OAuth2 bearer access token for the Graph API. The
// mechanism — caching, refresh timing, the token exchange — is the shared
// [oauth2] package's; what stays here is this worker's policy: which grants it
// accepts and what its credential bundle looks like.
type TokenSource = oauth2.TokenSource

// OAuth2 grant methods a SharePoint worker supports (ADR-0141). clientCredentials
// is app-only (a confidential client acts as itself, the norm for server workflows);
// refreshToken exchanges a pre-obtained refresh token (works for delegated /
// consumer scenarios). These mirror the native mail providers' grants (ADR-0093);
// the service-account (JWT-bearer) grant is Google-specific and omitted here.
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
)

// credentialBundle is the JSON an operator stores in the vault under a SharePoint
// worker's credentialsRef (ADR-0141). method selects the OAuth2 grant; the
// remaining fields configure it. Non-secret fields (ids, tenant) and secret fields
// (clientSecret, refreshToken) live together in this one vault secret, so a model
// never carries any of them (I6). tokenUrl and scope are optional overrides; the
// worker supplies sensible Graph defaults (from tenantId).
//
// A clientCredentials app proves itself with exactly one of clientSecret, certificate
// — the certificate and its unencrypted private key as one PEM text — or
// certificateThumbprint, a certificate in the Windows certificate store whose key
// signs there and is never read. They are the Entra worker's three (ADR-0172, amended),
// read the same way, so one app certificate serves both (ADR-0141, amended).
type credentialBundle struct {
	Method                string `json:"method"`
	TokenURL              string `json:"tokenUrl,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	TenantID              string `json:"tenantId,omitempty"`
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"`
	RefreshToken          string `json:"refreshToken,omitempty"`
	Certificate           string `json:"certificate,omitempty"`
	CertificateThumbprint string `json:"certificateThumbprint,omitempty"`
}

// openStoreCertificate finds a certificate in the Windows certificate store by
// thumbprint. A variable so the thumbprint path is testable on every platform.
var openStoreCertificate = certstore.Open

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
	var f oauth2.Fetcher
	switch b.Method {
	case methodClientCredentials:
		var err error
		if f, err = clientCredentials(b, cfg, now); err != nil {
			return nil, err
		}
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("sharepoint: refreshToken needs clientId, clientSecret and refreshToken")
		}
		// Refused rather than ignored: an operator who added a certificate expects it to
		// be what signs in, and a grant that quietly kept using the secret would hide that.
		if strings.TrimSpace(b.Certificate) != "" || strings.TrimSpace(b.CertificateThumbprint) != "" {
			return nil, fmt.Errorf("sharepoint: refreshToken signs in with clientSecret; a certificate or certificateThumbprint belongs to clientCredentials, where the app acts as itself")
		}
		f = oauth2.RefreshToken(cfg)
	default:
		return nil, fmt.Errorf("sharepoint: unknown auth method %q (want %q or %q)", b.Method, methodClientCredentials, methodRefreshToken)
	}
	return oauth2.NewCached(f, now), nil
}

// clientCredentials builds the app-only grant from the one credential the bundle
// carries. Two would leave the choice to an accident of precedence — an operator
// rotating from a secret to a certificate removes the secret — so they are refused,
// naming each.
func clientCredentials(b credentialBundle, cfg oauth2.Config, now func() time.Time) (oauth2.Fetcher, error) {
	pemText, thumbprint := strings.TrimSpace(b.Certificate), strings.TrimSpace(b.CertificateThumbprint)
	var set []string
	for _, c := range []struct{ name, value string }{
		{"clientSecret", b.ClientSecret}, {"certificate", pemText}, {"certificateThumbprint", thumbprint},
	} {
		if c.value != "" {
			set = append(set, c.name)
		}
	}
	switch {
	case b.ClientID == "" || len(set) == 0:
		return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and one of clientSecret, certificate or certificateThumbprint")
	case len(set) > 1:
		return nil, fmt.Errorf("sharepoint: clientCredentials has %s: set exactly one", strings.Join(set, " and "))
	}
	certificate := func(cert *x509.Certificate, signer crypto.Signer) oauth2.Fetcher {
		return oauth2.ClientCertificate(oauth2.CertificateConfig{
			HTTPClient:  cfg.HTTPClient,
			Kind:        cfg.Kind,
			TokenURL:    cfg.TokenURL,
			ClientID:    cfg.ClientID,
			Certificate: cert,
			PrivateKey:  signer,
			Scope:       cfg.Scope,
		}, now)
	}
	switch {
	case thumbprint != "":
		// The form is checked now, the store is not: see storeCertificate.
		if _, err := certstore.ParseThumbprint(thumbprint); err != nil {
			return nil, fmt.Errorf("sharepoint: %w", err)
		}
		return &storeCertificate{thumbprint: thumbprint, grant: certificate}, nil
	case pemText != "":
		cert, key, err := oauth2.ParseCertificateBundle(cfg.Kind, pemText)
		if err != nil {
			return nil, err
		}
		return certificate(cert, key), nil
	default:
		return oauth2.ClientCredentials(cfg), nil
	}
}

// storeCertificate is the certificate grant for a certificate named by its thumbprint
// in the Windows certificate store. The store is asked on the first token request, not
// when the client is built: the engine builds its SharePoint clients on its run loop
// (buildSharePointClients), where a call into a key storage provider would hold up every
// other request for as long as the provider takes. A worker that wants the answer at
// startup asks the store itself, as worker.sharepointRegistryFromEnv does.
//
// A failed open is not remembered, so a certificate installed or a key permission
// granted after the failure is picked up by the next request without a restart. A
// successful one is: the thumbprint names exactly one certificate, and its signer opens
// the store again for every signature, so nothing stale is held.
type storeCertificate struct {
	thumbprint string
	grant      func(*x509.Certificate, crypto.Signer) oauth2.Fetcher

	mu    sync.Mutex
	fetch oauth2.Fetcher
}

func (s *storeCertificate) Fetch(ctx context.Context) (string, time.Duration, error) {
	s.mu.Lock()
	f := s.fetch
	if f == nil {
		cert, signer, err := openStoreCertificate(s.thumbprint)
		if err != nil {
			s.mu.Unlock()
			return "", 0, fmt.Errorf("sharepoint: %w", err)
		}
		f = s.grant(cert, signer)
		s.fetch = f
	}
	s.mu.Unlock()
	return f.Fetch(ctx)
}
