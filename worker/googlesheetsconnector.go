package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/googlesheets"
)

// googleSheetsEnvPrefix is where a Google Sheets worker's credentials live.
const googleSheetsEnvPrefix = "ATLAS_GOOGLESHEETS_"

// googleSheetsRegistryFromEnv builds the Google identities this worker holds.
// ATLAS_GOOGLESHEETS_CONNECTORS lists the names; each name contributes
// ATLAS_GOOGLESHEETS_<NAME>_ENDPOINT (a Sheets API base override, blank for Google's
// own) and ATLAS_GOOGLESHEETS_<NAME>_CREDENTIALS — the two values
// [googlesheets.ProviderConfig] is built from, so a worker builds the identical client
// the engine would have.
//
// The credential arrives as *the whole bundle*, one opaque JSON value, rather than as
// one variable per field. That is SharePoint's and the SQL kinds' arrangement, chosen
// for their reason: the bundle has no public half worth splitting — the service
// account's address sits in the same vault secret as its private key — and splitting
// it would mean deciding the grant's shape a second time, here, where getting it wrong
// produces a worker that fails every job rather than one that will not start.
// [googlesheets.NewProviderClient] parses it, validates the grant and applies Google's
// token-endpoint and scope defaults, exactly as it does in the engine.
//
// The credential comes from the environment and not from a flag because argv is
// readable by anyone who can list processes — which matters more here than for most
// kinds, since a service-account private key is the whole identity.
func googleSheetsRegistryFromEnv(env func(string) string) (*googlesheets.Registry, []string, error) {
	names := splitAndTrim(env(googleSheetsEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* identity
		// whose bundle does not build, below, is still an error: the operator named
		// it, so the omission is a mistake to report at startup rather than a queue to
		// lease work from and then fail.
		return nil, nil, nil
	}
	reg := googlesheets.NewRegistry()
	for _, name := range names {
		key := googleSheetsEnvPrefix + envFold(name) + "_"
		client, err := googlesheets.NewProviderClient(googlesheets.ProviderConfig{
			Endpoint: env(key + "ENDPOINT"),
			Secret:   env(key + "CREDENTIALS"),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: google sheets worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		reg.Register(name, client)
	}
	return reg, names, nil
}

// RunGoogleSheetsJob performs a resolved Google Sheets task through a registry the
// caller owns. It is exported for the same reason RunJiraJob and RunSharePointJob are:
// the environment is only the default place a worker's identities come from, and a
// caller embedding this package can build a registry from a vault or an instance
// profile and get the identical call.
//
// It shares [googlesheets.Run] with the in-process path, so no two of those can
// disagree about what a resolved spreadsheet task means — only about which identities
// are in reach.
func RunGoogleSheetsJob(ctx context.Context, j Job, reg *googlesheets.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("googlesheets: the job carried no resolved worker detail; is this server offloading the googlesheets kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task googlesheets.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("googlesheets: cannot read the resolved detail: %w", err)
	}
	res, err := googlesheets.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	if task.ResultVariable == "" || res == nil {
		// Either the model discards the answer, or the operation is one that answers
		// with nothing — the same distinction the in-process handler makes, so an
		// offloaded delete does not write a null where a read would write a value.
		return nil, nil
	}
	return map[string]any{task.ResultVariable: res}, nil
}
