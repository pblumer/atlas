package api

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/wikiwikibangbang"
	"github.com/pblumer/atlas/logging"
)

// Environment a supervised wikiwikibangbang worker reads its wikis from — the same names
// an operator sets by hand for an external worker (there is no private channel,
// ADR-0157). wikiEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedWikiEnvUsesTheWorkersOwnNames holds the two together.
const (
	wikiEnvPrefix     = "ATLAS_WIKIWIKIBANGBANG_"
	wikiConnectorsEnv = wikiEnvPrefix + "CONNECTORS"
)

// wikiWorkerEnv renders the wikis a supervised wikiwikibangbang worker needs out of the
// vault, one set of variables per wikiwikibangbang Worker the store holds
// (ADR-draft-wikiwikibangbang-worker).
//
// It is CardDAV's arrangement: the endpoint is always rendered, because every wiki is an
// installation of its own. [wikiwikibangbang.CheckBundle] is the one reading of an
// endpoint and a bundle, here as in the worker, so the two cannot disagree about what
// they say, and a Worker whose pair does not build is left out rather than handed over —
// the worker refuses at startup on a *named* wiki it cannot build, which would take down
// every other one it serves.
//
// It reads the Worker store and the vault, so it runs on the run-loop goroutine (their
// owner, invariant I3).
func (s *Server) wikiWorkerEnv() []string {
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
	// Wikis an operator set on the host are inherited by the child as they are
	// and only kept in the list, so a store record cannot take them away.
	for _, name := range splitConnectorList(os.Getenv(wikiConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised wikiwikibangbang worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindWiki || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two wikiwikibangbang workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			bundle := s.resolveConnectorSecret(c.CredentialsRef)
			endpoint := strings.TrimSpace(c.Endpoint)
			if wikiwikibangbang.CheckBundle(endpoint, bundle) != nil {
				continue
			}
			taken[envKey] = c.Name
			key := wikiEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint, key+"CREDENTIALS="+bundle)
			addName(c.Name)
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, wikiConnectorsEnv+"="+strings.Join(names, ","))
}
