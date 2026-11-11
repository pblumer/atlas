package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/atlasreport"
)

// atlasReportEnvPrefix is where an atlasreport worker's Atlas servers live.
const atlasReportEnvPrefix = "ATLAS_ATLASREPORT_"

// atlasReportRegistryFromEnv builds the Atlas servers this worker reads reports from.
// ATLAS_ATLASREPORT_CONNECTORS lists the names; each name contributes
// ATLAS_ATLASREPORT_<NAME>_URL — the server's base URL — and
// ATLAS_ATLASREPORT_<NAME>_CREDENTIALS, the bundle [atlasreport.Bundle] describes: a
// technical principal's API token (ADR-0555 §3).
//
// The credential arrives as the whole bundle, one opaque JSON value, for the wiki's
// reason: [atlasreport.NewClient] is the one place that reads it, here as in the
// server's supervision check. It comes from the environment and not from a flag because
// argv is readable by anyone who can list processes.
func atlasReportRegistryFromEnv(env func(string) string) (*atlasreport.Registry, []string, error) {
	names := splitAndTrim(env(atlasReportEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error. A *named* server
		// that does not build, below, is still an error.
		return nil, nil, nil
	}
	reg := atlasreport.NewRegistry()
	for _, name := range names {
		key := atlasReportEnvPrefix + envFold(name) + "_"
		c, err := atlasreport.NewClient(env(key+"URL"), env(key+"CREDENTIALS"))
		if err != nil {
			return nil, nil, fmt.Errorf("worker: atlasreport worker %q is not usable: %w (set %sURL to the Atlas server's base URL and %sCREDENTIALS to the credential bundle)", name, err, key, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunAtlasReportJob performs a resolved Atlas report task through a registry the caller
// owns. It is exported for the reason RunWikiJob is: a caller embedding this package can
// build a registry from its own secret store and get the identical call.
func RunAtlasReportJob(ctx context.Context, j Job, reg *atlasreport.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's input when it hands the job out and sends nothing
		// when it cannot — an =expression over a variable that is not set, a value of the
		// wrong type — as well as when it predates this Worker Type.
		return nil, fmt.Errorf("atlasreport: the job carried no resolved task; the server could not evaluate its input (are the variables its =expressions read set?) or does not resolve Atlas report tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task atlasreport.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("atlasreport: cannot read the resolved detail: %w", err)
	}
	return atlasreport.Run(ctx, task, reg)
}
