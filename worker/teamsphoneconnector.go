package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/connector/teamsphone"
	"github.com/pblumer/atlas/logging"
)

// teamsPhoneEnvPrefix is where a Teams Phone worker's tenants and settings live.
const teamsPhoneEnvPrefix = "ATLAS_TEAMSPHONE_"

const (
	// teamsPhoneMockEnv serves every tenant from a [teamsphone.MockTenant] in this
	// process's memory instead of running PowerShell — the AD mock's idea (ADR-0181) for
	// a system with no directory to read.
	teamsPhoneMockEnv = teamsPhoneEnvPrefix + "MOCK"
	// teamsPhoneMockUsersEnv names the users resolve-users finds in mock mode; unset,
	// every name resolves.
	teamsPhoneMockUsersEnv = teamsPhoneEnvPrefix + "MOCK_USERS"
	// teamsPhoneMockNumbersEnv names the Calling Plan and Operator Connect numbers the
	// mock tenant has, which are also the inventory list-free-phone-numbers offers from;
	// unset, it has every number and lists none.
	teamsPhoneMockNumbersEnv = teamsPhoneEnvPrefix + "MOCK_NUMBERS"
	// teamsPhoneTimeoutEnv bounds one job's PowerShell run.
	teamsPhoneTimeoutEnv = teamsPhoneEnvPrefix + "TIMEOUT"
	// teamsPhonePwshEnv names the PowerShell 7 executable; unset, pwsh on the PATH.
	teamsPhonePwshEnv = teamsPhoneEnvPrefix + "PWSH"
)

// thumbprintPattern is a SHA-1 thumbprint once the spaces and invisible characters a
// copy from the certificate dialog brings along are gone; notHex is what removes them.
var (
	thumbprintPattern = regexp.MustCompile(`^[0-9A-F]{40}$`)
	notHex            = regexp.MustCompile(`[^0-9A-Fa-f]`)
)

// teamsPhoneRegistryFromEnv builds the tenants this worker holds.
// ATLAS_TEAMSPHONE_CONNECTORS lists the names; each name contributes
// ATLAS_TEAMSPHONE_<NAME>_TENANT_ID and _CLIENT_ID and exactly one way to the app's
// certificate — _CERTIFICATE, the certificate and its unencrypted key as one PEM text,
// or _CERTIFICATE_THUMBPRINT, a certificate already in the worker host's store.
//
// The credential comes from the environment and not a flag because argv is readable by
// anyone who can list processes, and a Teams administrator app can change every voice
// app in the tenant. Everything that can be checked without a tenant is checked here,
// while the operator is watching: a PEM that holds no RSA key, a thumbprint that is no
// thumbprint, two credentials for one tenant.
func teamsPhoneRegistryFromEnv(env func(string) string) (*teamsphone.Registry, []string, error) {
	names := splitAndTrim(env(teamsPhoneEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — Entra's reason exactly: the kind is
		// supervised by default, so a server with no tenant yet starts this worker on
		// every boot, and it must park rather than restart forever.
		return nil, nil, nil
	}
	mock, err := envBool(env, teamsPhoneMockEnv)
	if err != nil {
		return nil, nil, err
	}
	timeout := teamsphone.DefaultTimeout
	if raw := strings.TrimSpace(env(teamsPhoneTimeoutEnv)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return nil, nil, fmt.Errorf("worker: %s=%q is not a positive duration such as 4m", teamsPhoneTimeoutEnv, raw)
		}
		timeout = d
	}
	reg := teamsphone.NewRegistry()
	for _, name := range names {
		key := teamsPhoneEnvPrefix + envFold(name) + "_"
		if mock {
			// The model is identical either way; what differs is which worker leases its
			// jobs. So a mock tenant needs no credential — that is the point of trying a
			// process before anybody has registered an app.
			reg.Register(name, teamsphone.NewMockTenant(splitAndTrim(env(teamsPhoneMockUsersEnv))...).
				WithNumbers(splitAndTrim(env(teamsPhoneMockNumbersEnv))...))
			continue
		}
		cred, err := teamsPhoneCredential(name, key, env)
		if err != nil {
			return nil, nil, err
		}
		reg.Register(name, &teamsphone.Pwsh{Credential: cred, Bin: strings.TrimSpace(env(teamsPhonePwshEnv)), Timeout: timeout})
	}
	if mock {
		logging.Warn(logging.TeamsPhoneMockMode,
			"the Teams Phone worker serves its tenants from memory: nothing it creates reaches Microsoft Teams, and everything is gone when it stops",
			slog.String("tenants", strings.Join(names, ",")))
	}
	return reg, names, nil
}

func teamsPhoneCredential(name, key string, env func(string) string) (teamsphone.Credential, error) {
	cred := teamsphone.Credential{TenantID: strings.TrimSpace(env(key + "TENANT_ID")), ClientID: strings.TrimSpace(env(key + "CLIENT_ID"))}
	for what, v := range map[string]string{"TENANT_ID": cred.TenantID, "CLIENT_ID": cred.ClientID} {
		if v == "" {
			return cred, fmt.Errorf("worker: teamsphone worker %q is missing its %s: set %s%s", name, what, key, what)
		}
	}
	certPEM := entraCertificatePEM(env(key + "CERTIFICATE"))
	thumbprint := strings.ToUpper(notHex.ReplaceAllString(env(key+"CERTIFICATE_THUMBPRINT"), ""))
	switch {
	case certPEM == "" && thumbprint == "":
		return cred, fmt.Errorf("worker: teamsphone worker %q is missing its certificate: set %sCERTIFICATE or %sCERTIFICATE_THUMBPRINT", name, key, key)
	case certPEM != "" && thumbprint != "":
		return cred, fmt.Errorf("worker: teamsphone worker %q has both %sCERTIFICATE and %sCERTIFICATE_THUMBPRINT: set exactly one", name, key, key)
	case certPEM != "":
		// Parsed here only to refuse an unusable one at startup; the PEM itself travels
		// to pwsh, which signs with it. The error never quotes the PEM.
		if _, _, err := oauth2.ParseCertificateBundle("teamsphone", certPEM); err != nil {
			return cred, fmt.Errorf("worker: teamsphone worker %q: %w", name, err)
		}
		cred.Certificate = certPEM
	default:
		if !thumbprintPattern.MatchString(thumbprint) {
			return cred, fmt.Errorf("worker: teamsphone worker %q: %sCERTIFICATE_THUMBPRINT is not a certificate thumbprint (40 hexadecimal characters)", name, key)
		}
		cred.CertificateThumbprint = thumbprint
	}
	return cred, nil
}

// RunTeamsPhoneJob performs a resolved Teams Phone job through a registry the caller
// owns. It is exported for RunEntraJob's reason: the environment is only the default
// place a worker's tenants come from.
func RunTeamsPhoneJob(ctx context.Context, j Job, reg *teamsphone.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("teamsphone: the job carried no resolved worker detail; is this server running a version that resolves Teams Phone tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task teamsphone.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("teamsphone: cannot read the resolved detail: %w", err)
	}
	return teamsphone.Run(ctx, task, reg)
}
