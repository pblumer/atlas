package worker

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/internal/certstore"
)

// entraEnvPrefix is where an Entra worker's tenant credentials live.
const entraEnvPrefix = "ATLAS_ENTRA_"

// openStoreCertificate finds a certificate in the Windows certificate store by
// thumbprint. A variable so the worker's half is testable on every platform.
var openStoreCertificate = certstore.Open

// entraRegistryFromEnv builds the Entra tenants this worker holds.
// ATLAS_ENTRA_CONNECTORS lists the names; each name contributes
// ATLAS_ENTRA_<NAME>_TENANT_ID and _CLIENT_ID, exactly one credential — _CLIENT_SECRET;
// _CLIENT_CERTIFICATE, a PEM text holding the app's certificate and its private key; or
// _CLIENT_CERTIFICATE_THUMBPRINT, a certificate in this Windows host's store whose key
// signs there and is never read (ADR-0172, amended) — plus the optional _BASE_URL (a
// national cloud) and _SCOPE.
//
// The app credential comes from the environment and not from a flag because argv is
// readable by anyone who can list processes — and an app registration with
// User.ReadWrite.All can create and disable accounts across the whole directory,
// which makes this the most valuable secret any Atlas worker holds.
func entraRegistryFromEnv(env func(string) string) (*entra.Registry, []string, error) {
	names := splitAndTrim(env(entraEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. Entra is supervised by
		// default (ADR-0172), so a server with no tenant yet starts one of these on
		// every boot; failing here would restart it with a growing backoff forever,
		// which is what exitNothingToServe exists to avoid. A *named* tenant missing a
		// field, below, is still an error.
		return nil, nil, nil
	}
	reg := entra.NewRegistry()
	for _, name := range names {
		key := entraEnvPrefix + envFold(name) + "_"
		tenant, clientID := env(key+"TENANT_ID"), env(key+"CLIENT_ID")
		for what, v := range map[string]string{"TENANT_ID": tenant, "CLIENT_ID": clientID} {
			if v == "" {
				return nil, nil, fmt.Errorf("worker: entra worker %q is missing its %s: set %s%s", name, what, key, what)
			}
		}
		secret, certPEM := env(key+"CLIENT_SECRET"), entraCertificatePEM(env(key+"CLIENT_CERTIFICATE"))
		thumbprint := strings.TrimSpace(env(key + "CLIENT_CERTIFICATE_THUMBPRINT"))
		var set []string
		for _, c := range []struct{ name, value string }{
			{"CLIENT_SECRET", secret}, {"CLIENT_CERTIFICATE", certPEM}, {"CLIENT_CERTIFICATE_THUMBPRINT", thumbprint},
		} {
			if c.value != "" {
				set = append(set, key+c.name)
			}
		}
		switch {
		case len(set) == 0:
			return nil, nil, fmt.Errorf("worker: entra worker %q is missing its credential: set %sCLIENT_SECRET, %sCLIENT_CERTIFICATE or %sCLIENT_CERTIFICATE_THUMBPRINT", name, key, key, key)
		case len(set) > 1:
			// Two credentials would leave the choice to an accident of precedence; an
			// operator rotating from a secret to a certificate removes the secret.
			return nil, nil, fmt.Errorf("worker: entra worker %q has %s: set exactly one", name, strings.Join(set, " and "))
		}
		scope := env(key + "SCOPE")
		if scope == "" {
			scope = entra.DefaultScope
		}
		tokenURL := "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
		// A confidential client acting as itself: the only grant that makes sense for
		// unattended provisioning, so unlike mail and SharePoint this worker offers no
		// choice of grant — only of how the client proves itself, by a secret or by a
		// certificate.
		certificate := func(cert *x509.Certificate, signer crypto.Signer) oauth2.Fetcher {
			return oauth2.ClientCertificate(oauth2.CertificateConfig{
				HTTPClient:  nettimeout.HTTPClient(),
				Kind:        "entra",
				TokenURL:    tokenURL,
				ClientID:    clientID,
				Certificate: cert,
				PrivateKey:  signer,
				Scope:       scope,
			}, nil)
		}
		var fetch oauth2.Fetcher
		switch {
		case thumbprint != "":
			// The store is asked once now, so a certificate that is missing or a key
			// this account may not use is a startup error, not a failed token request.
			cert, signer, err := openStoreCertificate(thumbprint)
			if err != nil {
				return nil, nil, fmt.Errorf("worker: entra worker %q: %w", name, err)
			}
			fetch = certificate(cert, signer)
		case certPEM != "":
			cert, priv, err := oauth2.ParseCertificateBundle("entra", certPEM)
			if err != nil {
				return nil, nil, fmt.Errorf("worker: entra worker %q: %w", name, err)
			}
			fetch = certificate(cert, priv)
		default:
			fetch = oauth2.ClientCredentials(oauth2.Config{
				HTTPClient:   nettimeout.HTTPClient(),
				Kind:         "entra",
				TokenURL:     tokenURL,
				ClientID:     clientID,
				ClientSecret: secret,
				Scope:        scope,
			})
		}
		tokens := oauth2.NewCached(fetch, nil)
		reg.Register(name, entra.NewGraphClient(tokens, env(key+"BASE_URL"), nil))
	}
	return reg, names, nil
}

// entraCertificatePEM returns the PEM an operator set for _CLIENT_CERTIFICATE. A PEM
// is several lines, and some places an operator sets a variable — a systemd unit, a
// Kubernetes value written on one line — cannot hold a line break, so a value written
// with literal \n sequences and no real line break is read as the PEM it spells.
func entraCertificatePEM(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "\n") && strings.Contains(raw, `\n`) {
		raw = strings.ReplaceAll(raw, `\n`, "\n")
	}
	return raw
}

// RunEntraJob performs a resolved Entra job through a registry the caller owns. It is
// exported for the same reason RunMailJob and RunSQLJob are: the environment is only
// the default place a worker's credentials come from, and a caller embedding this
// package can build a registry from a vault or a managed identity and get the
// identical call.
func RunEntraJob(ctx context.Context, j Job, reg *entra.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("entra: the job carried no resolved worker detail; is this server running a version that resolves Entra tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task entra.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("entra: cannot read the resolved detail: %w", err)
	}
	return entra.Run(ctx, task, reg)
}
