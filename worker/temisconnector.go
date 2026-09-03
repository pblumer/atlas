package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/temis"
)

// temisEnvPrefix is where a temis worker's decision services live. It is deliberately
// the same convention the engine reads (ADR-0050): offloading the kind moves the
// variables from the server's environment to the worker's and changes nothing about
// how a service is named.
const temisEnvPrefix = "ATLAS_TEMIS_"

// temisRegistryFromEnv builds the decision services this worker holds.
// ATLAS_TEMIS_CONNECTORS lists the names; each name contributes
// ATLAS_TEMIS_<NAME>_URL and, optionally, ATLAS_TEMIS_<NAME>_TOKEN — the same two
// values the engine builds its own registry from, so a worker reaches the identical
// service.
//
// The token is optional for clio's reason rather than Remedy's: a decision service
// run beside Atlas may be reachable without one, and demanding a credential that
// installation has no concept of would refuse to serve a service that works.
func temisRegistryFromEnv(env func(string) string) (*temis.Registry, []string, error) {
	names := splitAndTrim(env(temisEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error, which the
		// caller reports as a kind this worker does not serve.
		return nil, nil, nil
	}
	reg := temis.NewRegistry()
	for _, name := range names {
		key := temisEnvPrefix + envFold(name) + "_"
		url := env(key + "URL")
		if url == "" {
			return nil, nil, fmt.Errorf("worker: temis service %q is missing its URL: set %sURL", name, key)
		}
		reg.Register(name, temis.NewHTTPClient(temis.Connector{Endpoint: url, Token: env(key + "TOKEN")}))
	}
	return reg, names, nil
}

// RunTemisJob evaluates a resolved central decision through a registry the caller
// owns, and reports it as an [Outcome] — the variables the task writes back *and* the
// evaluation to retain.
//
// It is the only runner that returns a decision, because a business rule task is the
// only job whose completion carries one (ADR-0066). The inputs are echoed back in the
// report rather than recomputed: they are what this evaluation was actually asked,
// and the engine cannot rebuild them at completion time because the instance has
// moved on since the lease.
func RunTemisJob(ctx context.Context, j Job, reg *temis.Registry) (Outcome, error) {
	if j.Connector == nil {
		return Outcome{}, fmt.Errorf("temis: the job carried no resolved decision detail; is this server offloading the temis kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return Outcome{}, err
	}
	var task temis.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return Outcome{}, fmt.Errorf("temis: cannot read the resolved detail: %w", err)
	}
	res, err := temis.Run(ctx, task, reg)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Decision: &DecisionReport{
		DecisionID: task.DecisionID,
		Inputs:     task.Inputs,
		Outputs:    res.Outputs,
		Trace:      string(res.Trace),
	}}
	// Through Result.Variables rather than the raw outputs, so an offloaded decision
	// writes back what an in-engine one writes: a single-output decision reaches a
	// gateway as a scalar, which is what a condition on it reads.
	if vars := res.Variables(); len(vars) > 0 {
		out.Variables = make(map[string]any, len(vars))
		for _, v := range vars {
			out.Variables[v.Name] = variableValue(v)
		}
	}
	return out, nil
}
