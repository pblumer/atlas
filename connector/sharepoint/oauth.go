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
// is app-only (a confidential client acts as itself, the norm for server workflows),
// proving itself with a client secret or with a certificate; refreshToken exchanges a
// pre-obtained refresh token (works for delegated / consumer scenarios). The
// service-account (JWT-bearer) grant is Google-specific and omitted here.
const (
	methodClientCredentials = "clientCredentials"
	methodRefreshToken      = "refreshToken"
)

// credentialBundle is the JSON an operator stores in the vault under a SharePoint
// worker's credentialsRef (ADR-0141). method selects the OAuth2 grant — empty is
// clientCredentials, the one a server workflow uses; the remaining fields configure
// it. Non-secret fields (ids, tenant) and secret fields (clientSecret, certificate,
// refreshToken) live together in this one vault secret, so a model never carries any
// of them (I6). tokenUrl and scope are optional overrides; the worker supplies
// sensible Graph defaults (from tenantId).
//
// certificate is the app's certificate and its unencrypted private key as one PEM
// text, in place of clientSecret: the credential Microsoft recommends for an app that
// runs unattended, because the private key never leaves this host — what the token
// endpoint sees is an assertion signed with it (ADR-0462's grant, in the shared oauth2
// package).
//
// certificateThumbprint names such a certificate in the Windows certificate store
// instead, whose key signs there and is never read — so a key marked non-exportable
// serves as it serves PowerShell. It is the Entra worker's third credential, read the
// same way (ADR-0172, amended), so one certificate in a host's store can serve both
// (ADR-0141, amended). A bundle carries one of the three.
type credentialBundle struct {
	Method                string `json:"method"`
	TokenURL              string `json:"tokenUrl,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	TenantID              string `json:"tenantId,omitempty"`
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"`
	Certificate           string `json:"certificate,omitempty"`
	CertificateThumbprint string `json:"certificateThumbprint,omitempty"`
	RefreshToken          string `json:"refreshToken,omitempty"`
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
	pemText, thumbprint := strings.TrimSpace(b.Certificate), strings.TrimSpace(b.CertificateThumbprint)
	var f oauth2.Fetcher
	switch b.Method {
	case methodClientCredentials, "":
		// Exactly one credential: two would leave the choice to an accident of
		// precedence, and an operator rotating from a secret to a certificate removes
		// the secret.
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
			return nil, fmt.Errorf("sharepoint: clientCredentials needs clientId and a clientSecret, a certificate or a certificateThumbprint")
		case len(set) == 2:
			return nil, fmt.Errorf("sharepoint: the bundle carries a %s and a %s; give one, not both", set[0], set[1])
		case len(set) > 2:
			return nil, fmt.Errorf("sharepoint: the bundle carries a %s; give one of them", strings.Join(set, ", a "))
		}
		certificate := func(cert *x509.Certificate, signer crypto.Signer) oauth2.Fetcher {
			return oauth2.ClientCertificate(oauth2.CertificateConfig{
				HTTPClient: httpc, Kind: "sharepoint", TokenURL: b.TokenURL, ClientID: b.ClientID,
				Certificate: cert, PrivateKey: signer, Scope: b.Scope,
			}, now)
		}
		switch {
		case thumbprint != "":
			// The form is checked now, the store is not: see storeCertificate.
			if _, err := certstore.ParseThumbprint(thumbprint); err != nil {
				return nil, fmt.Errorf("sharepoint: %w", err)
			}
			f = &storeCertificate{thumbprint: thumbprint, grant: certificate}
		case pemText != "":
			cert, key, err := oauth2.ParseCertificateBundle("sharepoint", b.Certificate)
			if err != nil {
				return nil, err
			}
			f = certificate(cert, key)
		default:
			f = oauth2.ClientCredentials(cfg)
		}
	case methodRefreshToken:
		if b.ClientID == "" || b.ClientSecret == "" || b.RefreshToken == "" {
			return nil, fmt.Errorf("sharepoint: refreshToken needs clientId, clientSecret and refreshToken")
		}
		if pemText != "" || thumbprint != "" {
			return nil, fmt.Errorf("sharepoint: a certificate belongs to the clientCredentials method, not to refreshToken")
		}
		f = oauth2.RefreshToken(cfg)
	default:
		return nil, fmt.Errorf("sharepoint: unknown auth method %q (want %q or %q)", b.Method, methodClientCredentials, methodRefreshToken)
	}
	return oauth2.NewCached(f, now), nil
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
