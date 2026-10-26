package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/wikiwikibangbang"
)

// wikiEnvPrefix is where a wikiwikibangbang worker's wikis live.
const wikiEnvPrefix = "ATLAS_WIKIWIKIBANGBANG_"

// wikiRegistryFromEnv builds the wikis this worker holds. ATLAS_WIKIWIKIBANGBANG_CONNECTORS
// lists the names; each name contributes ATLAS_WIKIWIKIBANGBANG_<NAME>_URL — the wiki's
// base URL — and ATLAS_WIKIWIKIBANGBANG_<NAME>_CREDENTIALS, the bundle
// [wikiwikibangbang.Bundle] describes.
//
// The credential arrives as the whole bundle, one opaque JSON value, for CardDAV's
// reason: [wikiwikibangbang.NewClient] is the one place that reads it, here as in the
// server's supervision check. It comes from the environment and not from a flag because
// argv is readable by anyone who can list processes.
func wikiRegistryFromEnv(env func(string) string) (*wikiwikibangbang.Registry, []string, error) {
	names := splitAndTrim(env(wikiEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error. A *named* wiki
		// that does not build, below, is still an error.
		return nil, nil, nil
	}
	reg := wikiwikibangbang.NewRegistry()
	for _, name := range names {
		key := wikiEnvPrefix + envFold(name) + "_"
		c, err := wikiwikibangbang.NewClient(env(key+"URL"), env(key+"CREDENTIALS"), wikiwikibangbang.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: wikiwikibangbang worker %q is not usable: %w (set %sURL to the wiki's base URL and %sCREDENTIALS to the credential bundle)", name, err, key, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunWikiJob performs a resolved wikiwikibangbang task through a registry the caller
// owns. It is exported for the reason RunCardDAVJob is: a caller embedding this package
// can build a registry from its own secret store and get the identical call.
func RunWikiJob(ctx context.Context, j Job, reg *wikiwikibangbang.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's values when it hands the job out and sends nothing
		// when it cannot — a propertiesVariable that is not set, blocks that are no list —
		// as well as when it predates this Worker Type.
		return nil, fmt.Errorf("wikiwikibangbang: the job carried no resolved task; the server could not evaluate its values (is the properties variable set, and an object? do the blocks evaluate to a list?) or does not resolve wikiwikibangbang tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task wikiwikibangbang.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("wikiwikibangbang: cannot read the resolved detail: %w", err)
	}
	return wikiwikibangbang.Run(ctx, task, reg)
}
