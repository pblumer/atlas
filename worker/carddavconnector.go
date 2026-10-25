package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/carddav"
)

// cardDAVEnvPrefix is where a CardDAV worker's address books live.
const cardDAVEnvPrefix = "ATLAS_CARDDAV_"

// cardDAVRegistryFromEnv builds the address books this worker holds.
// ATLAS_CARDDAV_CONNECTORS lists the names; each name contributes ATLAS_CARDDAV_<NAME>_URL
// — the address book's URL or the server's — and ATLAS_CARDDAV_<NAME>_CREDENTIALS, the
// bundle [carddav.Bundle] describes.
//
// The credential arrives as the whole bundle, one opaque JSON value, for Google Contacts'
// reason: [carddav.NewClient] is the one place that reads it, here as in the server's
// supervision check. It comes from the environment and not from a flag because argv is
// readable by anyone who can list processes.
func cardDAVRegistryFromEnv(env func(string) string) (*carddav.Registry, []string, error) {
	names := splitAndTrim(env(cardDAVEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured: a nil registry and no error. A *named*
		// address book that does not build, below, is still an error.
		return nil, nil, nil
	}
	reg := carddav.NewRegistry()
	for _, name := range names {
		key := cardDAVEnvPrefix + envFold(name) + "_"
		c, err := carddav.NewClient(env(key+"URL"), env(key+"CREDENTIALS"), carddav.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: carddav worker %q is not usable: %w (set %sURL to the address book's or the server's URL and %sCREDENTIALS to the credential bundle)", name, err, key, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunCardDAVJob performs a resolved CardDAV task through a registry the caller owns. It
// is exported for the reason RunGoogleContactsJob is: a caller embedding this package
// can build a registry from its own secret store and get the identical call.
func RunCardDAVJob(ctx context.Context, j Job, reg *carddav.Registry) (map[string]any, error) {
	if j.Connector == nil {
		// The server resolves a task's values when it hands the job out and sends nothing
		// when it cannot — a contactVariable that is not set, a contact that is no object —
		// as well as when it predates this Worker Type.
		return nil, fmt.Errorf("carddav: the job carried no resolved task; the server could not evaluate its values (is the contact variable set, and an object?) or does not resolve CardDAV tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task carddav.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("carddav: cannot read the resolved detail: %w", err)
	}
	return carddav.Run(ctx, task, reg)
}
