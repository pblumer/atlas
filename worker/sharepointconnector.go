package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/sharepoint"
)

// sharepointEnvPrefix is where a SharePoint worker's Graph endpoints and credentials
// live.
const sharepointEnvPrefix = "ATLAS_SHAREPOINT_"

// sharepointMockURLEnv is the Atlas server route a mockup instance
// (ATLAS_SHAREPOINT_<NAME>_MOCK=1) forwards its operations to: the server keeps the
// site, so what one run wrote survives this worker being restarted
// (ADR-0484).
const sharepointMockURLEnv = sharepointEnvPrefix + "MOCK_URL"

// sharepointRegistryFromEnv builds the SharePoint instances this worker holds.
// ATLAS_SHAREPOINT_CONNECTORS lists the names; each name contributes
// ATLAS_SHAREPOINT_<NAME>_ENDPOINT (the Graph base URL, blank for the default) and
// ATLAS_SHAREPOINT_<NAME>_CREDENTIALS — the two values [sharepoint.ProviderConfig] is
// built from, so a worker builds the identical client the engine would have. A
// clientCredentials bundle proves the app with a client secret, a certificate as PEM,
// or the thumbprint of a certificate in this Windows host's store (ADR-0141, amended).
//
// The credential arrives as *the whole bundle*, one opaque JSON value, rather than as
// one variable per field. That is the SQL kinds' arrangement and it is chosen for
// their reason: the bundle has no public half worth splitting — the tenant and client
// ids sit in the same vault secret as the client secret and the refresh token
// (ADR-0141) — and splitting it would mean deciding its shape a second time, here,
// where getting the grant's required fields wrong produces a worker that fails every
// job rather than one that will not start. [sharepoint.NewProviderClient] parses it,
// validates the grant and applies the Graph defaults, exactly as it does in the
// engine.
//
// The credential comes from the environment and not from a flag because argv is
// readable by anyone who can list processes.
//
// ATLAS_SHAREPOINT_<NAME>_MOCK=1 makes an instance a mockup instead: it holds no
// credential and reaches no tenant, and forwards each operation to the Atlas server
// at ATLAS_SHAREPOINT_MOCK_URL, which keeps the site (ADR-0484).
func sharepointRegistryFromEnv(env func(string) string) (*sharepoint.Registry, []string, error) {
	reg, names, _, err := sharepointSourcesFromEnv(env)
	return reg, names, err
}

// sharepointSourcesFromEnv is sharepointRegistryFromEnv plus the instances whose
// credential is brokered per job (ADR-0475 §4):
// ATLAS_SHAREPOINT_<NAME>_BROKERED=1 says the worker holds no credential for that
// instance at all and asks the server for a short-lived token each time it is allowed
// to act. Only its endpoint is known here.
func sharepointSourcesFromEnv(env func(string) string) (*sharepoint.Registry, []string, map[string]string, error) {
	names := splitAndTrim(env(sharepointEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* instance
		// whose bundle does not build, below, is still an error: the operator named
		// it, so the omission is a mistake to report at startup rather than a queue to
		// lease work from and then fail.
		return nil, nil, nil, nil
	}
	reg := sharepoint.NewRegistry()
	brokered := map[string]string{}
	for _, name := range names {
		key := sharepointEnvPrefix + envFold(name) + "_"
		mock, err := envBool(env, key+"MOCK")
		if err != nil {
			return nil, nil, nil, err
		}
		if mock {
			// A mockup reaches no tenant and holds no credential; it needs only the
			// server that keeps its site. Without that address every job would fail, so
			// it is a startup error while the operator is watching.
			base := strings.TrimSpace(env(sharepointMockURLEnv))
			if base == "" {
				return nil, nil, nil, fmt.Errorf("worker: sharepoint worker %q is a mockup (%sMOCK) but %s is not set, so its operations have no site to run against", name, key, sharepointMockURLEnv)
			}
			reg.Register(name, sharepoint.NewRemoteMock(base, name, env("ATLAS_TOKEN")))
			continue
		}
		if env(key+"BROKERED") == "1" {
			brokered[name] = env(key + "ENDPOINT")
			continue
		}
		secret := env(key + "CREDENTIALS")
		client, err := sharepoint.NewProviderClient(sharepoint.ProviderConfig{
			Endpoint: env(key + "ENDPOINT"),
			Secret:   secret,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("worker: sharepoint worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		// A certificate named by its thumbprint is asked for now, as the Entra worker asks
		// for its own (ADR-0172, amended): one the store does not hold, or a key this
		// account may not use, is a startup error while the operator is watching rather
		// than a failed token request in the middle of a run. The client asks the store
		// again on its first token request; it does not ask while it is built, because the
		// engine builds the same client on its run loop (ADR-0141, amended).
		if thumbprint := sharepoint.CertificateThumbprint(secret); thumbprint != "" {
			if _, _, err := openStoreCertificate(thumbprint); err != nil {
				return nil, nil, nil, fmt.Errorf("worker: sharepoint worker %q: %w", name, err)
			}
		}
		reg.Register(name, client)
	}
	return reg, names, brokered, nil
}

// RunSharePointJob performs a resolved SharePoint task through a registry the caller
// owns. It is exported for the same reason RunJiraJob and RunMailJob are: the
// environment is only the default place a worker's instances come from, and a caller
// embedding this package can build a registry from a vault or an instance profile and
// get the identical call.
//
// It shares [sharepoint.Run] with the in-process path, so no two of those can
// disagree about what a resolved SharePoint task means — only about which instances
// are in reach.
func RunSharePointJob(ctx context.Context, j Job, reg *sharepoint.Registry) (map[string]any, error) {
	return runSharePointJob(ctx, j, reg, nil)
}

// runSharePointJob asks the server first (ADR-0475
// §5). A governed Worker's action runs only on a permit or in shadow, on the
// parameters the decision covers, and — for a brokered instance — with the credential
// that came with the permit and nothing else.
func runSharePointJob(ctx context.Context, j Job, reg *sharepoint.Registry, brokered map[string]string) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("sharepoint: the job carried no resolved connector detail; is this server offloading the sharepoint kind?")
	}
	auth, err := Authorize(ctx)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: could not ask whether this action may run: %w", err)
	}
	fields := j.Connector.Fields
	if auth.Governed() {
		if auth.Enforced && !auth.Permitted {
			return nil, Refused()
		}
		// The parameters the decision covers, not the ones handed over at lease time.
		if auth.Job != nil {
			fields = auth.Job
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var task sharepoint.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("sharepoint: cannot read the resolved detail: %w", err)
	}
	if endpoint, ok := brokered[task.Connector]; ok {
		// A brokered instance acts only with the credential its permit carried: there is
		// no other one here to fall back to, by design.
		if auth == nil || auth.Credential == nil || auth.Credential.AccessToken == "" {
			return nil, fmt.Errorf("sharepoint: worker %q acts only with a credential brokered for a permitted action, and none came with this job", task.Connector)
		}
		one := sharepoint.NewRegistry()
		one.Register(task.Connector, sharepoint.NewGraphClient(staticToken(auth.Credential.AccessToken), endpoint))
		reg = one
	} else if auth != nil && auth.Credential != nil {
		// The server released a person's token: it recorded this action as theirs. An
		// instance not set up to act with one — its environment predates the Worker
		// becoming delegated — must not act as itself instead, or the evidence would
		// name a person for what the Worker did with its own credential.
		return nil, fmt.Errorf("sharepoint: the permit for worker %q carries a person's credential, but this Worker Instance is not set up to act with one; it acts after its restart", task.Connector)
	}
	res, err := sharepoint.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	// Through Result.Variables rather than the raw item, so an offloaded create
	// writes what an in-engine one writes — and a task naming no result variable
	// completes with nothing rather than with an empty object.
	vars := res.Variables()
	if len(vars) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(vars))
	for _, v := range vars {
		out[v.Name] = variableValue(v)
	}
	return out, nil
}

// staticToken is a brokered access token: valid for one job, never refreshed here.
type staticToken string

func (t staticToken) Token(context.Context) (string, error) { return string(t), nil }
