package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/sharepoint"
)

// sharepointEnvPrefix is where a SharePoint worker's Graph endpoints and credentials
// live.
const sharepointEnvPrefix = "ATLAS_SHAREPOINT_"

// sharepointRegistryFromEnv builds the SharePoint instances this worker holds.
// ATLAS_SHAREPOINT_CONNECTORS lists the names; each name contributes
// ATLAS_SHAREPOINT_<NAME>_ENDPOINT (the Graph base URL, blank for the default) and
// ATLAS_SHAREPOINT_<NAME>_CREDENTIALS — the two values [sharepoint.ProviderConfig] is
// built from, so a worker builds the identical client the engine would have.
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
func sharepointRegistryFromEnv(env func(string) string) (*sharepoint.Registry, []string, error) {
	names := splitAndTrim(env(sharepointEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* instance
		// whose bundle does not build, below, is still an error: the operator named
		// it, so the omission is a mistake to report at startup rather than a queue to
		// lease work from and then fail.
		return nil, nil, nil
	}
	reg := sharepoint.NewRegistry()
	for _, name := range names {
		key := sharepointEnvPrefix + envFold(name) + "_"
		client, err := sharepoint.NewProviderClient(sharepoint.ProviderConfig{
			Endpoint: env(key + "ENDPOINT"),
			Secret:   env(key + "CREDENTIALS"),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: sharepoint worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		reg.Register(name, client)
	}
	return reg, names, nil
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
	if j.Connector == nil {
		return nil, fmt.Errorf("sharepoint: the job carried no resolved connector detail; is this server offloading the sharepoint kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task sharepoint.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("sharepoint: cannot read the resolved detail: %w", err)
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
