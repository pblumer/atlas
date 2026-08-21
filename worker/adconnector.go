package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/ad"
)

// adSecretPrefix is where an AD worker's bind passwords live. It is deliberately the
// *same* convention the engine uses (ADR-0041 A2): offloading the kind moves the
// variable from the server's environment to the worker's, and changes nothing about
// the model or the reference it authored.
const adSecretPrefix = "ATLAS_CONNECTOR_"

// adSecretFromEnv resolves an AD bind-password reference against this worker's own
// environment.
//
// Unlike mail, SQL and Entra there is nothing to validate at startup, and that is a
// property of AD's shape rather than an omission: a reference is authored per *task*,
// not per connector name, so the worker cannot know which references the models it
// will serve use — and an anonymous bind authors no reference at all. A reference
// nothing answers to fails that job with the variable named, which is the same
// failure the in-process path gives today.
func adSecretFromEnv(env func(string) string) ad.SecretResolver {
	return func(ref string) string {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return ""
		}
		return strings.TrimSpace(env(adSecretPrefix + envFold(ref) + "_TOKEN"))
	}
}

// RunADJob performs a resolved AD job with the caller's own dialer and secret store.
// It is exported for the same reason RunMailJob and the rest are: the environment is
// only the default place a worker's credentials come from, and a caller embedding
// this package can resolve references from a vault of its own and get the identical
// operation.
func RunADJob(ctx context.Context, j Job, dialer ad.Dialer, secret ad.SecretResolver) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("ad: the job carried no resolved connector detail; is this server offloading the ad kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task ad.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("ad: cannot read the resolved detail: %w", err)
	}
	// Every AD operation but sync writes to the directory rather than back to a
	// variable, and returns nothing; sync returns the changes and the next cookie.
	return ad.Run(ctx, task, dialer, secret)
}
