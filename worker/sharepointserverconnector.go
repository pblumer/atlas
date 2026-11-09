package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/sharepointserver"
)

// sharePointServerEnvPrefix is where a SharePoint Server worker's Workers live.
const sharePointServerEnvPrefix = "ATLAS_SHAREPOINTSERVER_"

// sharePointServerRegistryFromEnv builds the web applications this worker serves.
// ATLAS_SHAREPOINTSERVER_CONNECTORS lists the names; each contributes
// ATLAS_SHAREPOINTSERVER_<NAME>_URL, the web application, and optionally
// ATLAS_SHAREPOINTSERVER_<NAME>_CREDENTIALS, the settings [sharepointserver.Bundle]
// describes (no credential; the name is the convention every Worker Type shares).
// ATLAS_SHAREPOINTSERVER_POWERSHELL names the interpreter, powershell.exe when unset.
func sharePointServerRegistryFromEnv(env func(string) string) (*sharepointserver.Registry, []string, error) {
	names := splitAndTrim(env(sharePointServerEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, nil
	}
	opts := sharepointserver.Options{PowerShell: env(sharePointServerEnvPrefix + "POWERSHELL")}
	reg := sharepointserver.NewRegistry()
	for _, name := range names {
		key := sharePointServerEnvPrefix + envFold(name) + "_"
		c, err := sharepointserver.NewClient(env(key+"URL"), env(key+"CREDENTIALS"), opts)
		if err != nil {
			return nil, nil, fmt.Errorf("worker: sharepointserver worker %q is not usable: %w (set %sURL to the web application)", name, err, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunSharePointServerJob performs a resolved task through a registry the caller owns.
func RunSharePointServerJob(ctx context.Context, j Job, reg *sharepointserver.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("sharepointserver: the job carried no resolved worker detail; is this server running a version that resolves SharePoint Server tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task sharepointserver.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("sharepointserver: cannot read the resolved detail: %w", err)
	}
	return sharepointserver.Run(ctx, task, reg)
}
