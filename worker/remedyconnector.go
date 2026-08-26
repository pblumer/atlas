package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/remedy"
)

// remedyEnvPrefix is where a Remedy worker's AR System credentials live.
const remedyEnvPrefix = "ATLAS_REMEDY_"

// remedyRegistryFromEnv builds the Remedy instances this worker holds.
// ATLAS_REMEDY_CONNECTORS lists the names; each name contributes
// ATLAS_REMEDY_<NAME>_ENDPOINT (the AR System REST base URL), _USERNAME and
// _PASSWORD — the three values [remedy.Connector] is built from, so a worker builds
// the identical client the engine would have.
//
// The service account comes from the environment and not from a flag because argv is
// readable by anyone who can list processes, and a Remedy service account is
// typically allowed to file against every form in the instance.
func remedyRegistryFromEnv(env func(string) string) (*remedy.Registry, []string, error) {
	names := splitAndTrim(env(remedyEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* instance
		// missing a field, below, is still an error: the operator named it, so the
		// omission is a mistake to report at startup rather than a queue to lease work
		// from and then fail.
		return nil, nil, nil
	}
	reg := remedy.NewRegistry()
	for _, name := range names {
		key := remedyEnvPrefix + envFold(name) + "_"
		endpoint, user, password := env(key+"ENDPOINT"), env(key+"USERNAME"), env(key+"PASSWORD")
		for _, want := range []struct{ what, v string }{
			{"ENDPOINT", endpoint}, {"USERNAME", user}, {"PASSWORD", password},
		} {
			if want.v == "" {
				return nil, nil, fmt.Errorf("worker: remedy connector %q is missing its %s: set %s%s", name, want.what, key, want.what)
			}
		}
		reg.Register(name, remedy.NewHTTPClient(remedy.Connector{
			BaseURL:  endpoint,
			Username: user,
			Password: password,
		}))
	}
	return reg, names, nil
}

// RunRemedyJob creates a resolved Remedy job's entry through a registry the caller
// owns. It is exported for the same reason RunMailJob and RunEntraJob are: the
// environment is only the default place a worker's credentials come from, and a
// caller embedding this package can build a registry from a vault or an instance
// profile and get the identical call.
//
// It shares [remedy.Run] with the in-process path, so no two of those can disagree
// about what a resolved Remedy task means — only about which credentials are in reach.
func RunRemedyJob(ctx context.Context, j Job, reg *remedy.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("remedy: the job carried no resolved connector detail; is this server offloading the remedy kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task remedy.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("remedy: cannot read the resolved detail: %w", err)
	}
	res, err := remedy.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	if task.ResultVariable == "" {
		return nil, nil // the model discards the entry id
	}
	return map[string]any{task.ResultVariable: res.EntryID}, nil
}
