package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
)

// entraEnvPrefix is where an Entra worker's tenant credentials live.
const entraEnvPrefix = "ATLAS_ENTRA_"

// entraRegistryFromEnv builds the Entra tenants this worker holds.
// ATLAS_ENTRA_CONNECTORS lists the names; each name contributes
// ATLAS_ENTRA_<NAME>_TENANT_ID, _CLIENT_ID and _CLIENT_SECRET, plus the optional
// _BASE_URL (a national cloud) and _SCOPE.
//
// The app credential comes from the environment and not from a flag because argv is
// readable by anyone who can list processes — and an app registration with
// User.ReadWrite.All can create and disable accounts across the whole directory,
// which makes this the most valuable secret any Atlas worker holds.
func entraRegistryFromEnv(env func(string) string) (*entra.Registry, []string, error) {
	names := splitAndTrim(env(entraEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("worker: --connector entra needs at least one connector: set %sCONNECTORS to the tenant names this worker serves", entraEnvPrefix)
	}
	reg := entra.NewRegistry()
	for _, name := range names {
		key := entraEnvPrefix + envFold(name) + "_"
		tenant, clientID, secret := env(key+"TENANT_ID"), env(key+"CLIENT_ID"), env(key+"CLIENT_SECRET")
		for what, v := range map[string]string{"TENANT_ID": tenant, "CLIENT_ID": clientID, "CLIENT_SECRET": secret} {
			if v == "" {
				return nil, nil, fmt.Errorf("worker: entra connector %q is missing its %s: set %s%s", name, what, key, what)
			}
		}
		scope := env(key + "SCOPE")
		if scope == "" {
			scope = entra.DefaultScope
		}
		tokens := oauth2.NewCached(oauth2.ClientCredentials(oauth2.Config{
			HTTPClient: nettimeout.HTTPClient(),
			Kind:       "entra",
			TokenURL:   "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0/token",
			ClientID:   clientID,
			// A confidential client acting as itself: the only grant that makes sense
			// for unattended provisioning, so unlike mail and SharePoint this
			// connector offers no choice of grant.
			ClientSecret: secret,
			Scope:        scope,
		}), nil)
		reg.Register(name, entra.NewGraphClient(tokens, env(key+"BASE_URL"), nil))
	}
	return reg, names, nil
}

// RunEntraJob performs a resolved Entra job through a registry the caller owns. It is
// exported for the same reason RunMailJob and RunSQLJob are: the environment is only
// the default place a worker's credentials come from, and a caller embedding this
// package can build a registry from a vault or a managed identity and get the
// identical call.
func RunEntraJob(ctx context.Context, j Job, reg *entra.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("entra: the job carried no resolved connector detail; is this server running a version that resolves Entra tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task entra.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("entra: cannot read the resolved detail: %w", err)
	}
	return entra.Run(ctx, task, reg)
}
