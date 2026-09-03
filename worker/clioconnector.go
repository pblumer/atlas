package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/model"
)

// clioEnvPrefix is where a clio worker's event-store endpoints and tokens live.
const clioEnvPrefix = "ATLAS_CLIO_"

// clioRegistryFromEnv builds the clio instances this worker holds.
// ATLAS_CLIO_CONNECTORS lists the names; each name contributes
// ATLAS_CLIO_<NAME>_ENDPOINT (the clio base URL) and, optionally,
// ATLAS_CLIO_<NAME>_TOKEN — the two values [clio.Connector] is built from, so a
// worker builds the identical client the engine would have.
//
// The token is optional where Remedy's password is not: a clio instance may be
// reachable without one (a loopback store an operator runs beside Atlas), and
// demanding a credential that installation has no concept of would refuse to serve a
// connector that works. An instance that *does* require one answers 401, which is a
// job failure naming the instance rather than a worker that will not start.
func clioRegistryFromEnv(env func(string) string) (*clio.Registry, []string, error) {
	names := splitAndTrim(env(clioEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* instance
		// missing its endpoint, below, is still an error: the operator named it, so
		// the omission is a mistake to report at startup rather than a queue to lease
		// work from and then fail.
		return nil, nil, nil
	}
	reg := clio.NewRegistry()
	for _, name := range names {
		key := clioEnvPrefix + envFold(name) + "_"
		endpoint := env(key + "ENDPOINT")
		if endpoint == "" {
			return nil, nil, fmt.Errorf("worker: clio connector %q is missing its ENDPOINT: set %sENDPOINT", name, key)
		}
		reg.Register(name, clio.NewHTTPClient(clio.Connector{
			Endpoint: endpoint,
			Token:    env(key + "TOKEN"),
		}))
	}
	return reg, names, nil
}

// RunClioJob performs a resolved clio task through a registry the caller owns. It is
// exported for the same reason RunRemedyJob and RunMailJob are: the environment is
// only the default place a worker's instances come from, and a caller embedding this
// package can build a registry from a vault or an instance profile and get the
// identical call.
//
// It shares [clio.Run] with the in-process path, so no two of those can disagree
// about what a resolved clio task means — only about which endpoints are in reach.
func RunClioJob(ctx context.Context, j Job, reg *clio.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("clio: the job carried no resolved connector detail; is this server offloading the clio kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task clio.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("clio: cannot read the resolved detail: %w", err)
	}
	client, ok := reg.Client(task.Connector)
	if !ok {
		return nil, reg.Unresolved("clio", task.Connector)
	}
	res, err := clio.Run(ctx, task, client)
	if err != nil {
		return nil, err
	}
	return clioVariables(res), nil
}

// clioVariables renders a run's result as the variables the job completes with. It
// goes through [clio.Result.Variables] rather than building a map from the value, so
// the offloaded path writes what the in-process one writes — a read's rows and a
// query's projection reach a model in the same shape, and a write completes with
// nothing rather than with an empty object.
func clioVariables(res clio.Result) map[string]any {
	vars := res.Variables()
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]any, len(vars))
	for _, v := range vars {
		out[v.Name] = variableValue(v)
	}
	return out
}

// variableValue unwraps a stored variable into the JSON value a completion carries.
// A structured value is re-parsed so it crosses as a real object or array rather
// than as JSON inside a string.
func variableValue(v model.VariableValue) any {
	switch v.Kind {
	case model.VarBool:
		return v.Bool
	case model.VarNumber:
		return json.Number(v.Text)
	case model.VarString:
		return v.Text
	case model.VarJSON:
		var out any
		dec := json.NewDecoder(strings.NewReader(v.Text))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			return nil
		}
		return out
	default:
		return nil
	}
}
