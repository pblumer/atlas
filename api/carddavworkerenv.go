package api

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/carddav"
	"github.com/pblumer/atlas/logging"
)

// Environment a supervised CardDAV worker reads its address books from — the same names
// an operator sets by hand for an external worker (there is no private channel,
// ADR-0157). cardDAVEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedCardDAVEnvUsesTheWorkersOwnNames holds the two together.
const (
	cardDAVEnvPrefix     = "ATLAS_CARDDAV_"
	cardDAVConnectorsEnv = cardDAVEnvPrefix + "CONNECTORS"
)

// cardDAVWorkerEnv renders the address books a supervised CardDAV worker needs out of
// the vault, one set of variables per CardDAV Worker the store holds
// (ADR-0490).
//
// It is Google Contacts' arrangement with one difference: the endpoint is always
// rendered, because CardDAV has no default server. [carddav.CheckBundle] is the one
// reading of an endpoint and a bundle, here as in the worker, so the two cannot disagree
// about what they say, and a Worker whose pair does not build is left out rather than
// handed over — the worker refuses at startup on a *named* address book it cannot build,
// which would take down every other one it serves.
//
// It reads the Worker store and the vault, so it runs on the run-loop goroutine (their
// owner, invariant I3).
func (s *Server) cardDAVWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	// Address books an operator set on the host are inherited by the child as they are
	// and only kept in the list, so a store record cannot take them away.
	for _, name := range splitConnectorList(os.Getenv(cardDAVConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised carddav worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindCardDAV || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two carddav workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			bundle := s.resolveConnectorSecret(c.CredentialsRef)
			endpoint := strings.TrimSpace(c.Endpoint)
			if carddav.CheckBundle(endpoint, bundle) != nil {
				continue
			}
			taken[envKey] = c.Name
			key := cardDAVEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint, key+"CREDENTIALS="+bundle)
			addName(c.Name)
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, cardDAVConnectorsEnv+"="+strings.Join(names, ","))
}
