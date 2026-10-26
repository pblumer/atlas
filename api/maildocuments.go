package api

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/s3"
	"github.com/pblumer/atlas/logging"
)

// serverDocuments is where an export-attachment run inside this server puts its
// document: the S3 Workers this server holds (ADR-0478).
//
// It reads s.s3Registry at call time rather than capturing it when the mail handler is
// registered, because the two kinds are set up one after the other and a rebuild
// replaces the registry's contents — the store an export reaches is the one configured
// now, not the one there was when the server started.
type serverDocuments struct{ s *Server }

func (d serverDocuments) PutDocument(ctx context.Context, worker, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error) {
	return s3.Documents(d.s.s3Registry).PutDocument(ctx, worker, bucket, key, contentType, disposition, data, metadata)
}

// mailStoresEnv lists the S3 Workers a supervised mail worker holds to export
// attachments into. It is a list of its own rather than ATLAS_S3_CONNECTORS because a
// worker serving both kinds is handed both renderings, and the s3 kind's list must
// not be replaced by this narrower one. It matches the worker's own constant;
// TestSupervisedMailEnvUsesTheWorkersOwnNames holds the two together.
const mailStoresEnv = mailEnvPrefix + "STORES"

// mailExportStores lists the S3 Workers a process's export-attachment tasks write into.
func mailExportStores(cp *compiler.CompiledProcess) []string {
	var out []string
	for _, u := range cp.MailboxUses() {
		if u.Store != "" {
			out = append(out, u.Store)
		}
	}
	return out
}

// mailStoreEnvLocked renders the S3 identities a supervised mail worker needs to run
// the deployed export-attachment tasks: only the S3 Workers those tasks name, and only
// those that build an identity at all — the narrowest set that still works, as for an
// AD worker's bind passwords. recs is the worker store, already read.
//
// It reads the deployment map and the vault, so it runs on the run-loop goroutine
// (invariant I3), their owner.
func (s *Server) mailStoreEnvLocked(recs []connector) []string {
	wanted := map[string]bool{}
	for _, d := range s.deployments {
		if d == nil || d.cp == nil {
			continue
		}
		for _, name := range mailExportStores(d.cp) {
			wanted[name] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	sorted := append([]connector(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var env, names []string
	taken := map[string]string{}
	for _, c := range sorted {
		if c.Kind != connectorKindS3 || !c.Enabled || !wanted[c.Name] {
			continue
		}
		envKey := connectorEnvKey(c.Name)
		if envKey == "" {
			continue
		}
		if first, dup := taken[envKey]; dup {
			logging.Warn(logging.WorkerSupervisorFailed,
				"two s3 workers share one environment name; the second is not handed to the supervised mail worker",
				slog.String("connector", c.Name), slog.String("collidesWith", first))
			continue
		}
		bundle := s.resolveConnectorSecret(c.CredentialsRef)
		if !s3BundleUsable(bundle) {
			continue
		}
		taken[envKey] = c.Name
		env = append(env, s3IdentityEnv(envKey, c, bundle)...)
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return nil
	}
	return append(env, mailStoresEnv+"="+strings.Join(names, ","))
}
