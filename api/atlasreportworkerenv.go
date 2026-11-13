package api

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/atlasreport"
	"github.com/pblumer/atlas/logging"
)

// Environment a supervised atlasreport worker reads its Atlas servers from — the same
// names an operator sets by hand for an external worker (there is no private channel,
// ADR-0157). atlasReportEnvPrefix matches the worker's own constant of the same name;
// TestSupervisedAtlasReportEnvUsesTheWorkersOwnNames holds the two together.
const (
	atlasReportEnvPrefix     = "ATLAS_ATLASREPORT_"
	atlasReportConnectorsEnv = atlasReportEnvPrefix + "CONNECTORS"
)

// atlasReportWorkerEnv renders the Atlas servers a supervised atlasreport worker reads
// reports from out of the vault, one set of variables per atlasreport Worker the store
// holds (ADR-0555 §3).
//
// It is the wiki's arrangement: the endpoint is always rendered, because the server a
// report reads may be another installation than this one. [atlasreport.CheckBundle] is
// the one reading of an endpoint and a bundle, here as in the worker, so the two cannot
// disagree, and a Worker whose pair does not build is left out rather than handed over —
// the worker refuses at startup on a *named* server it cannot build, which would take
// down every other one it serves.
//
// It reads the Worker store and the vault, so it runs on the run-loop goroutine (their
// owner, invariant I3).
func (s *Server) atlasReportWorkerEnv() []string {
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
	for _, name := range splitConnectorList(os.Getenv(atlasReportConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised atlasreport worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindAtlasReport || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two atlasreport workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			bundle := s.resolveConnectorSecret(c.CredentialsRef)
			endpoint := strings.TrimSpace(c.Endpoint)
			if atlasreport.CheckBundle(endpoint, bundle) != nil {
				continue
			}
			taken[envKey] = c.Name
			key := atlasReportEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint, key+"CREDENTIALS="+bundle)
			addName(c.Name)
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, atlasReportConnectorsEnv+"="+strings.Join(names, ","))
}
