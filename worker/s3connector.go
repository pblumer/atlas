package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/s3"
)

// s3EnvPrefix is where an S3 worker's object-store identities live.
const s3EnvPrefix = "ATLAS_S3_"

// s3RegistryFromEnv builds the object-store identities this worker holds.
// ATLAS_S3_CONNECTORS lists the names; each name contributes
// ATLAS_S3_<NAME>_CREDENTIALS — the access-key bundle — and optionally
// ATLAS_S3_<NAME>_URL, the store's base URL, blank for AWS at the bundle's region. Those
// are the two values [s3.ProviderConfig] is built from, so a worker builds the identical
// client the engine would have.
//
// The credential arrives as *the whole bundle*, one opaque JSON value, rather than as one
// variable per field. That is SharePoint's, Google Sheets' and the SQL kinds' arrangement,
// chosen for their reason: the bundle has no public half worth splitting — the region is
// in the same secret as the secret access key because SigV4 signs with both — and
// splitting it would mean deciding the credential's shape a second time, here, where
// getting it wrong produces a worker that fails every job rather than one that will not
// start. [s3.NewProviderClient] parses it and insists on the region, exactly as it does in
// the engine.
//
// The credential comes from the environment and not from a flag because argv is readable
// by anyone who can list processes — which matters here as much as anywhere, since an
// access key is the whole of a worker's authority over a bucket.
func s3RegistryFromEnv(env func(string) string) (*s3.Registry, []string, error) {
	names := splitAndTrim(env(s3EnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the caller
		// reports as a kind this worker does not serve. A *named* identity whose bundle
		// does not build, below, is still an error: the operator named it, so the
		// omission is a mistake to report at startup rather than a queue to lease work
		// from and then fail.
		return nil, nil, nil
	}
	reg := s3.NewRegistry()
	for _, name := range names {
		key := s3EnvPrefix + envFold(name) + "_"
		client, err := s3.NewProviderClient(s3.ProviderConfig{
			Endpoint: env(key + "URL"),
			Secret:   env(key + "CREDENTIALS"),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("worker: s3 worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		reg.Register(name, client)
	}
	return reg, names, nil
}

// RunS3Job performs a resolved object-store task through a registry the caller owns. It is
// exported for the same reason RunJiraJob and RunGoogleSheetsJob are: the environment is
// only the default place a worker's identities come from, and a caller embedding this
// package can build a registry from a vault or an instance profile and get the identical
// call.
//
// It shares [s3.Run] with the in-process path, so no two of those can disagree about what
// a resolved object-store task means — only about which identities are in reach. On an
// offloaded installation this is also where a put's bytes are: they go from here to the
// store, and the engine only ever held the variable they were composed from.
func RunS3Job(ctx context.Context, j Job, reg *s3.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("s3: the job carried no resolved worker detail; is this server offloading the s3 kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task s3.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("s3: cannot read the resolved detail: %w", err)
	}
	res, err := s3.Run(ctx, task, reg)
	if err != nil {
		return nil, err
	}
	if task.ResultVariable == "" || res == nil {
		// Either the model discards the answer, or the operation is the one that answers
		// with nothing — the same distinction the in-process handler makes, so an
		// offloaded delete does not write a null where a read would write a value.
		return nil, nil
	}
	return map[string]any{task.ResultVariable: res}, nil
}
