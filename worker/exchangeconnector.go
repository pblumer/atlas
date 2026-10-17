package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/exchange"
)

// exchangeEnvPrefix is where an Exchange worker's organizations live.
const exchangeEnvPrefix = "ATLAS_EXCHANGE_"

// exchangeRegistryFromEnv builds the Exchange organizations this worker holds.
// ATLAS_EXCHANGE_CONNECTORS lists the names; each name contributes
// ATLAS_EXCHANGE_<NAME>_CREDENTIALS — the bundle [exchange.Bundle] describes — and
// optionally ATLAS_EXCHANGE_<NAME>_URL, the Worker's endpoint: a sovereign cloud's
// Exchange Online host, or an Exchange server's Remote PowerShell URI.
// ATLAS_EXCHANGE_POWERSHELL names the interpreter on-premises Workers start (pwsh when
// unset; powershell.exe on a Windows host without PowerShell 7) — a property of this
// host rather than of any one organization.
//
// The credential arrives as the whole bundle, one opaque JSON value, for S3's and
// SharePoint's reason: its shape depends on the target, and [exchange.NewBackend] is
// the one place that reads it, here as in the server's supervision check, so the two
// cannot read the same JSON differently. It comes from the environment and not from a
// flag because argv is readable by anyone who can list processes — and an Exchange
// administrator credential reads and changes every mailbox in an organization.
func exchangeRegistryFromEnv(env func(string) string) (*exchange.Registry, []string, error) {
	names := splitAndTrim(env(exchangeEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. Exchange is supervised
		// by default, so a server with no organization yet starts one of these on
		// every boot; failing here would restart it forever. A *named* organization
		// whose bundle does not build, below, is still an error.
		return nil, nil, nil
	}
	opts := exchange.Options{PowerShell: env(exchangeEnvPrefix + "POWERSHELL")}
	reg := exchange.NewRegistry()
	for _, name := range names {
		key := exchangeEnvPrefix + envFold(name) + "_"
		backend, err := exchange.NewBackend(env(key+"URL"), env(key+"CREDENTIALS"), opts)
		if err != nil {
			return nil, nil, fmt.Errorf("worker: exchange worker %q is not usable: %w (set %sCREDENTIALS to the credential bundle)", name, err, key)
		}
		reg.Register(name, backend)
	}
	return reg, names, nil
}

// RunExchangeJob performs a resolved Exchange task through a registry the caller owns.
// It is exported for the reason RunEntraJob is: the environment is only the default
// place a worker's credentials come from, and a caller embedding this package can
// build a registry from a vault or a managed identity and get the identical call.
func RunExchangeJob(ctx context.Context, j Job, reg *exchange.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("exchange: the job carried no resolved worker detail; is this server running a version that resolves Exchange tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task exchange.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("exchange: cannot read the resolved detail: %w", err)
	}
	return exchange.Run(ctx, task, reg)
}
