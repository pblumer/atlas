package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/googlecontacts"
)

// googleContactsEnvPrefix is where a Google Contacts worker's address books live.
const googleContactsEnvPrefix = "ATLAS_GOOGLECONTACTS_"

// googleContactsRegistryFromEnv builds the address books this worker holds.
// ATLAS_GOOGLECONTACTS_CONNECTORS lists the names; each name contributes
// ATLAS_GOOGLECONTACTS_<NAME>_CREDENTIALS — the bundle [googlecontacts.Bundle] describes
// — and optionally ATLAS_GOOGLECONTACTS_<NAME>_URL, a People API base for a host behind
// a proxy.
//
// The credential arrives as the whole bundle, one opaque JSON value, for Exchange's
// reason: [googlecontacts.NewClient] is the one place that reads it, here as in the
// server's supervision check. It comes from the environment and not from a flag
// because argv is readable by anyone who can list processes — and the bundle reads a
// person's whole address book.
func googleContactsRegistryFromEnv(env func(string) string) (*googlecontacts.Registry, []string, error) {
	names := splitAndTrim(env(googleContactsEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. A *named* address book
		// whose bundle does not build, below, is still an error.
		return nil, nil, nil
	}
	reg := googlecontacts.NewRegistry()
	for _, name := range names {
		key := googleContactsEnvPrefix + envFold(name) + "_"
		c, err := googlecontacts.NewClient(env(key+"URL"), env(key+"CREDENTIALS"), googlecontacts.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: googlecontacts worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunGoogleContactsJob performs a resolved Google Contacts task through a registry the
// caller owns. It is exported for the reason RunExchangeJob is: the environment is only
// the default place a worker's credentials come from, and a caller embedding this
// package can build a registry from its own secret store and get the identical call.
func RunGoogleContactsJob(ctx context.Context, j Job, reg *googlecontacts.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's values when it hands the job out and sends
		// nothing when it cannot — a contactVariable that is not set, a contact that
		// is no object — as well as when it predates this Worker Type.
		return nil, fmt.Errorf("googlecontacts: the job carried no resolved task; the server could not evaluate its values (is the contact variable set, and an object?) or does not resolve Google Contacts tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task googlecontacts.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("googlecontacts: cannot read the resolved detail: %w", err)
	}
	return googlecontacts.Run(ctx, task, reg)
}
