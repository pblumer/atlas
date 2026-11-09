package api

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/sharepointserver"
	"github.com/pblumer/atlas/logging"
)

// Environment a SharePoint Server worker reads its Workers from: the same names an
// operator sets by hand on the farm (there is no private channel, ADR-0157).
const (
	sharePointServerEnvPrefix     = "ATLAS_SHAREPOINTSERVER_"
	sharePointServerConnectorsEnv = sharePointServerEnvPrefix + "CONNECTORS"
)

// sharePointServerWorkerEnv renders the web applications a supervised SharePoint Server
// worker needs, one set of variables per Worker the store holds
// (ADR-draft-sharepoint-server-worker). The kind is off by default because the cmdlets
// exist only on a farm server; this is what `--supervise-connector sharepointserver`
// on such a server reads. It reads the Worker store, so it takes the run loop (I3).
func (s *Server) sharePointServerWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool
	)
	seen := map[string]bool{}
	for _, name := range splitConnectorList(os.Getenv(sharePointServerConnectorsEnv)) {
		if name = strings.TrimSpace(name); name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised sharepointserver worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]bool{}
		for _, c := range recs {
			if c.Kind != connectorKindSharePointServer || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			endpoint := strings.TrimSpace(c.Endpoint)
			bundle := s.resolveConnectorSecret(c.CredentialsRef)
			if envKey == "" || taken[envKey] || sharepointserver.CheckBundle(endpoint, bundle) != nil {
				continue
			}
			taken[envKey] = true
			key := sharePointServerEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint)
			if bundle != "" {
				env = append(env, key+"CREDENTIALS="+bundle)
			}
			if !seen[c.Name] {
				seen[c.Name] = true
				names = append(names, c.Name)
			}
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, sharePointServerConnectorsEnv+"="+strings.Join(names, ","))
}
