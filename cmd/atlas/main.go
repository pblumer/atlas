// Command atlas is the single-binary Atlas server: one self-contained process
// that embeds the engine, exposes an HTTP API, and serves the web UI. See
// ADR-0011 and Milestone S in ROADMAP.md.
//
//	go run ./cmd/atlas serve --addr :8080 --data-dir ./atlas-data
//
// It also hosts the Model Context Protocol adapter, which lets an AI agent drive
// a running Atlas server (ADR-0016):
//
//	go run ./cmd/atlas mcp --server http://localhost:8080
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/jobtype"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/mimimport"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/tracing"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

func main() {
	// Subcommand dispatch. The first non-flag argument selects the mode; with no
	// subcommand (or a leading flag) we default to "serve" so existing
	// invocations like `atlas --addr :8080` keep working.
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "serve":
		if err := runServe(args); err != nil {
			fatal("atlas", err)
		}
	case "mcp":
		if err := runMCP(args); err != nil {
			fatal("atlas mcp", err)
		}
	case "worker":
		if err := runWorker(args); err != nil {
			// A worker with nothing to serve has already said so, at the level that
			// fits it. It leaves its own status so a supervisor can park it rather
			// than restart it forever; reporting it again as a failure would put a
			// red line in the console for an ordinary state.
			if errors.Is(err, errNothingToServe) {
				os.Exit(exitNothingToServe)
			}
			fatal("atlas worker", err)
		}
	case "version", "-v", "--version":
		printVersion(os.Stdout)
	case "reset-password":
		if err := runResetPassword(args); err != nil {
			fatal("atlas reset-password", err)
		}
	case "import-mim":
		if err := runImportMIM(args); err != nil {
			fatal("atlas import-mim", err)
		}
	case "check-job-types":
		if err := runCheckJobTypes(args); err != nil {
			fatal("atlas check-job-types", err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "atlas: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

// traceShutdownTimeout bounds the flush of buffered spans on the way out. Short on
// purpose: a collector that is down must not hold up an operator's restart, and the
// spans it would have received are the least valuable thing in the process.
const traceShutdownTimeout = 3 * time.Second

// loopbackURL turns a listen address (":8080", "0.0.0.0:8080", "localhost:8080")
// into a URL the process can use to reach its own HTTP server. A wildcard or
// empty host becomes 127.0.0.1 so the in-process MCP adapter can call back in.
func loopbackURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func usage() {
	fmt.Fprint(os.Stderr, `Atlas — a durable BPMN workflow engine.

Usage:
  atlas serve          [flags]      Run the engine, HTTP API, and web UI (default)
  atlas mcp            [flags]      Run the Model Context Protocol adapter on stdio
  atlas worker         [flags]      Work service-task jobs for a running Atlas, out of process
  atlas reset-password [flags] USER Reset a local user's password from the shell
  atlas import-mim     [flags] FILE Convert a MIM/FIM XOML workflow to BPMN 2.0
  atlas check-job-types [flags]     Check a data directory's job-type table for index collisions
  atlas version                     Print the version and build metadata

Run "atlas <command> -h" for the flags of a command.
`)
}

// printVersion writes the product version and the binary's embedded VCS build
// metadata (git commit, commit time, dirty flag, Go toolchain) to w. The values
// come from the api package, so the CLI, the web UI, and GET /api/v1/info all
// report exactly the same running build.
func printVersion(w io.Writer) {
	b := api.Build()
	fmt.Fprintf(w, "atlas %s\n", b.Version)
	if b.Revision != "" {
		rev := b.Revision
		if len(rev) > 12 {
			rev = rev[:12]
		}
		dirty := ""
		if b.Modified {
			dirty = " (modified)"
		}
		fmt.Fprintf(w, "  commit: %s%s\n", rev, dirty)
	}
	if b.Time != "" {
		fmt.Fprintf(w, "  built:  %s\n", b.Time)
	}
	fmt.Fprintf(w, "  go:     %s\n", b.Go)
}

// runServe boots the engine behind the HTTP API and web UI.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	dataDir := fs.String("data-dir", "atlas-data", "directory for the write-ahead log and state store")
	shutdownTimeout := fs.Duration("shutdown-timeout", 10*time.Second, "grace period for in-flight requests on shutdown")
	docs := fs.Bool("docs", true, "serve the OpenAPI spec (/api/v1/openapi.json) and the Scalar API explorer (/api/docs); pass --docs=false to disable")
	auth := fs.Bool("auth", false, "require login for the API and UI; seeds an admin from ATLAS_ADMIN_USERNAME/ATLAS_ADMIN_PASSWORD on first run")
	userProvisioning := fs.Bool("user-provisioning", true, "enable the user-provisioning connector for the protected system project's processes (create/set-password/disable Atlas logins); on by default (opt-out) — disable with --user-provisioning=false. It only ever acts for the protected system project's processes, behind their human approval step, so the boundary it reopens stays gated (ADR-0123)")
	vault := fs.Bool("vault", true, "enable the encrypted secret vault; on by default (generates a key at <data-dir>/vault.key unless ATLAS_VAULT_KEY is set), --vault=false to disable (ADR-0070)")
	powershell := fs.Bool("powershell", true, "run PowerShell script tasks by shelling out to pwsh; on by default, --powershell=false to disable (executes arbitrary interpreter code)")
	python := fs.Bool("python", true, "run Python script tasks by shelling out to python3; on by default, --python=false to disable (executes arbitrary interpreter code)")
	javascript := fs.Bool("javascript", true, "run JavaScript script tasks by shelling out to node; on by default, --javascript=false to disable (executes arbitrary interpreter code)")
	scriptTimeout := fs.Duration("script-timeout", 30*time.Second, "wall-clock limit for a single script task in any language; an overrunning script is killed and its job left pending")
	// OpenSearch event exporter (ADR-0114): opt-in, off unless a URL is set. The URL
	// and index accept a flag (defaulting to the env var) for discoverability; the
	// credentials are env-only so a secret never lands in the process arguments.
	osURL := fs.String("opensearch-url", os.Getenv("ATLAS_OPENSEARCH_URL"), "base URL of an OpenSearch cluster to mirror the event log into (ADR-0114); empty disables the exporter. Credentials come from ATLAS_OPENSEARCH_USERNAME/ATLAS_OPENSEARCH_PASSWORD")
	osIndex := fs.String("opensearch-index", envOr("ATLAS_OPENSEARCH_INDEX", opensearch.DefaultIndex), "OpenSearch index the exporter writes events to")
	// History retention (ADR-0115): opt-in, off unless a positive max-age is set.
	retentionAge := fs.Duration("retention-max-age", envDuration("ATLAS_RETENTION_MAX_AGE"), "hard-delete finished process instances older than this once their events are exported (ADR-0115), e.g. 720h; 0 disables the server-wide age, and retention then applies only to processes declaring their own atlas:historyTtl (ADR-0144)")
	// The sweep's cadence and per-tick batch bound how fast a backlog drains: batch per
	// interval. The defaults suit steady state; a bulk run that leaves tens of thousands
	// of finished instances behind is why they are reachable at all.
	retentionInterval := fs.Duration("retention-interval", envDurationOr("ATLAS_RETENTION_INTERVAL", api.DefaultRetentionInterval), "how often the retention sweep runs (ADR-0115); with --retention-batch this bounds the drain rate of a backlog")
	retentionBatch := fs.Int("retention-batch", envIntOr("ATLAS_RETENTION_BATCH", api.DefaultRetentionBatch), "how many finished instances one retention sweep evaluates (ADR-0115); the cap keeps a sweep from blocking the run loop, so raise it with the loop's headroom in mind")
	// Recovery checkpoints (ADR-0131): on by default, because bounded restart time is
	// the point of them. They only ever add a shortcut — the WAL stays the source of
	// truth and a missing or unusable checkpoint just means a full replay — so unlike
	// the exporter and retention there is nothing here to opt into.
	checkpointInterval := fs.Duration("checkpoint-interval", 5*time.Minute, "how often to snapshot applied state so a restart replays only the log past it (ADR-0131); 0 disables checkpointing")
	checkpointKeep := fs.Int("checkpoint-keep", 3, "how many recovery checkpoints to keep; each pins the state files it captured, so this bounds their disk (ADR-0131)")
	// WAL compaction (ADR-0131): opt-in, off by default. Unlike checkpointing it deletes
	// data, so — like history retention (ADR-0115) — an operator turns it on deliberately.
	compactWAL := fs.Bool("compact-wal", false, "delete WAL segments already covered by a recovery checkpoint and every consumer watermark (ADR-0131), bounding the log's disk; off by default because it is irreversible. Requires checkpointing")
	// Distributed traces (ADR-0142), off unless a collector is configured. The endpoint
	// is the OTLP/HTTP base URL — Atlas posts to <endpoint>/v1/traces — and the standard
	// OTEL_EXPORTER_OTLP_ENDPOINT is honored so a deployment that already sets it for
	// everything else does not need an Atlas-specific flag.
	traceEndpoint := fs.String("trace-endpoint", envOr("OTEL_EXPORTER_OTLP_ENDPOINT", ""), "OTLP/HTTP collector base URL, e.g. http://collector:4318, to export request traces to (ADR-0142); empty disables tracing")
	traceRatio := fs.Float64("trace-sample-ratio", tracing.DefaultSampleRatio, "fraction of traces to record, 0 to 1. A caller that already sampled a trace is always honored, so a distributed trace is never half-recorded")
	// How logs are rendered (ADR-0142). Text is the default because the console audience
	// is the one Atlas has always had; json is for a deployment shipping logs somewhere
	// that would otherwise need a parsing rule of its own. Either way every line carries
	// a stable event= name, so what an alert matches on does not depend on this flag.
	logFormat := fs.String("log-format", string(logging.DefaultFormat), "how to render logs: \"text\" (logfmt-style key=value, for a terminal) or \"json\" (one object per line, for a log shipper). Every line carries a stable event= name either way (ADR-0142)")
	// Prometheus metrics (ADR-0142): on by default. The exposition carries only
	// bounded-cardinality aggregates, so the cost of having it is a path an operator may
	// not want reachable rather than data leaking.
	offload := fs.String("offload-connectors", "", "comma-separated connector kinds this server must NOT run itself (e.g. remedy,sharepoint): their jobs park for a worker instead (ADR-0168). Adds to the default set unless --in-process-connectors turns that off. A kind whose credentials live in this server's connector store and that the supervisor cannot hand over needs its secret moved to the worker by hand, so those are never defaulted. An unknown kind is refused at startup rather than ignored")
	history := fs.String("worker-history", "", "name of a clio connector to append every settled job run to, so a worker's history outlives this process (ADR-0036). The console reads it back under a worker's recent jobs; retention and querying are then your clio's, not another flag here. Off unless given, in which case the console keeps only its in-memory tail")
	historyScope := fs.String("worker-history-scope", api.HistoryScopeAll, "what --worker-history writes: \"all\" settled jobs, or \"failed\" only. All is what \"how long does a mail send take\" needs and the larger bill; failed is much less volume and still answers most of what a history is asked")
	inProcess := fs.Bool("in-process-connectors", false, "run every connector inside the engine, as before ADR-0164. Off by default: "+strings.Join(api.DefaultOffloadedKinds(), ", ")+" run in a worker this server starts and supervises itself, so the loop cannot stall behind them — behind an SMTP handshake above all — and trying Atlas still needs no configuration")
	supervise := superviseFlag{}
	fs.Var(&supervise, "supervise", "run a worker process for these job types and keep it running, as id=type=command; repeat for more workers, and repeat the type=command part for a worker that serves several types (ADR-0157). Off unless given: under systemd or Kubernetes the platform owns process lifecycle")
	metricsOn := fs.Bool("metrics", true, "serve the Prometheus exposition at /metrics (ADR-0142); pass --metrics=false to disable. It is unauthenticated like /healthz — put a reverse proxy in front of anything exposed beyond the host")
	if err := fs.Parse(args); err != nil {
		return err
	}
	enabled := map[string]bool{"powershell": *powershell, "python": *python, "javascript": *javascript}
	osCfg := opensearch.Config{
		URL:      strings.TrimSpace(*osURL),
		Username: os.Getenv("ATLAS_OPENSEARCH_USERNAME"),
		Password: os.Getenv("ATLAS_OPENSEARCH_PASSWORD"),
		Index:    strings.TrimSpace(*osIndex),
	}
	retention := retentionConfig{maxAge: *retentionAge, interval: *retentionInterval, batch: *retentionBatch}
	trace := tracing.Config{
		Endpoint:    *traceEndpoint,
		ServiceName: envOr("OTEL_SERVICE_NAME", "atlas"),
		Version:     api.Version,
		SampleRatio: *traceRatio,
	}
	return serve(*addr, *dataDir, *shutdownTimeout, *docs, *auth, *vault, *userProvisioning, enabled, *scriptTimeout, osCfg, retention, *checkpointInterval, *checkpointKeep, *compactWAL, *metricsOn, logging.Format(*logFormat), trace, supervise, splitList(*offload), *inProcess, *history, *historyScope)
}

// envOr returns the environment variable's value, or def when it is unset/empty.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envDuration parses a duration from the environment variable, or 0 when it is
// unset, empty, or malformed.
func envDuration(key string) time.Duration { return envDurationOr(key, 0) }

// envDurationOr parses a duration from the environment variable, or def when it is
// unset, empty, or malformed. A flag whose default is a real value (rather than "off")
// uses this, so a typo in the environment leaves the documented default standing
// instead of silently zeroing the setting.
func envDurationOr(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// envIntOr parses an int from the environment variable, or def when it is unset,
// empty, or malformed. The integer counterpart of envDurationOr.
func envIntOr(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// retentionConfig is the history-retention configuration the CLI assembles: the
// server-wide max age (ADR-0115), plus the sweep's cadence and per-tick batch, which
// together bound how fast a backlog of finished instances drains. Grouped because they
// are one operator decision, and because they are only ever passed on together.
type retentionConfig struct {
	maxAge   time.Duration
	interval time.Duration
	batch    int
}

func serve(addr, dataDir string, shutdownTimeout time.Duration, docs, auth, vault, userProvisioning bool, scriptLangs map[string]bool, scriptTimeout time.Duration, osExport opensearch.Config, retention retentionConfig, checkpointInterval time.Duration, checkpointKeep int, compactWAL, metricsOn bool, logFormat logging.Format, traceCfg tracing.Config, supervise superviseFlag, offloadKinds []string, inProcessConnectors bool, historyConnector, historyScope string) error {
	// Tee the process log into a bounded in-memory buffer, exposed at
	// GET /api/v1/logs, so an operator can read recent server logs from the web UI
	// without shell access. Set before the first log line so startup is captured.
	logs := api.NewLogBuffer(2000)
	if err := logging.Setup(io.MultiWriter(os.Stderr, logs), logFormat); err != nil {
		return err
	}

	// Traces, when a collector is configured (ADR-0142). Set up before anything else so a
	// bad endpoint fails the boot here rather than after the data directory is open, and
	// shut down last so whatever is buffered is flushed on the way out.
	tp, err := tracing.Setup(traceCfg)
	if err != nil {
		return err
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), traceShutdownTimeout)
		defer cancel()
		if err := tp.Shutdown(shutCtx); err != nil {
			logging.Warn(logging.TracingShutdownFailed, "flushing buffered spans on shutdown failed",
				slog.String("error", err.Error()))
		}
	}()

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	// Apply a staged whole-instance snapshot restore, if one was uploaded before this
	// restart (ADR-0108). This MUST happen before the WAL and state stores are opened:
	// it replaces the WAL (and drops the state store so recovery rebuilds it), which
	// cannot be done while the engine holds them open under the single-writer invariant.
	if applied, err := api.ApplyPendingRestore(dataDir); err != nil {
		return fmt.Errorf("apply pending restore: %w", err)
	} else if applied {
		logging.Info(logging.RestoreApplied,
			"applied a staged full-snapshot restore; state will be rebuilt from the restored WAL")
	}

	// Open durable stores. The wal is the source of truth; the store is its
	// materialization, caught up on Recover below.
	logging.Info(logging.DataDirOpened, "opening the data directory", slog.String("dataDir", dataDir))
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dataDir, "wal")})
	if err != nil {
		return err
	}
	defer wl.Close()

	store, err := state.Open(filepath.Join(dataDir, "state"))
	if err != nil {
		return err
	}
	defer store.Close()

	// One partition for now (single-node). Recovery replays the log into the store
	// before we accept traffic, starting after the newest usable checkpoint under
	// <data-dir>/checkpoints instead of at genesis (ADR-0131). The root is resolved
	// through checkpoint.Dir, the same function the server's checkpoint cadence
	// publishes through, so the two can never point at different directories. A
	// missing, corrupt, or too-new checkpoint simply falls back to a full replay.
	engine.BuildVersion = api.Version // metadata recorded in the checkpoints we publish
	proc := engine.New(1, wl, store, nil)
	if err := proc.RecoverFrom(checkpoint.Dir(dataDir)); err != nil {
		return err
	}

	apiOpts := []api.Option{api.WithLogBuffer(logs), api.WithSystemProcesses()}
	if tp.Enabled() {
		apiOpts = append(apiOpts, api.WithTracing())
		logging.Info(logging.TracingEnabled, "exporting request traces to an OTLP collector",
			slog.String("endpoint", traceCfg.Endpoint), slog.Float64("sampleRatio", traceCfg.SampleRatio))
	}
	if !docs {
		apiOpts = append(apiOpts, api.WithoutDocs())
	}
	if !metricsOn {
		apiOpts = append(apiOpts, api.WithoutMetrics())
	}
	// Mirror the durable event log into OpenSearch when configured (ADR-0114).
	if osExport.Enabled() {
		apiOpts = append(apiOpts, api.WithOpenSearchExporter(osExport))
		logging.Info(logging.ExporterEnabled, "opensearch exporter enabled",
			slog.String("index", osExport.Index), slog.String("url", osExport.URL))
	}
	// Hard-delete finished-instance history past the max age, gated on export (ADR-0115).
	// The cadence and batch apply either way: retention also runs for a process that
	// declares its own atlas:historyTtl, with no server-wide age set (ADR-0144).
	apiOpts = append(apiOpts, api.WithRetentionInterval(retention.interval), api.WithRetentionBatch(retention.batch))
	if retention.maxAge > 0 {
		gate := "durable position"
		if osExport.Enabled() {
			gate = "OpenSearch export"
		}
		apiOpts = append(apiOpts, api.WithRetention(retention.maxAge))
		logging.Info(logging.RetentionEnabled, "history retention enabled",
			slog.Duration("maxAge", retention.maxAge), slog.String("gate", gate),
			slog.Int("batch", retention.batch), slog.Duration("interval", retention.interval))
	}
	// Periodic recovery checkpoints keep restart time a function of the cadence rather
	// than of the whole log's length (ADR-0131). Nothing is deleted by them.
	if checkpointInterval > 0 {
		apiOpts = append(apiOpts, api.WithCheckpoints(checkpointInterval), api.WithCheckpointRetention(checkpointKeep))
		logging.Info(logging.CheckpointEnabled, "recovery checkpoints enabled",
			slog.Duration("interval", checkpointInterval), slog.Int("keep", checkpointKeep))
		// Compaction rides the checkpoint tick, so it needs one to ride (ADR-0131).
		if compactWAL {
			apiOpts = append(apiOpts, api.WithWALCompaction())
			logging.Info(logging.WALCompactionEnabled,
				"wal compaction enabled: deleting segments already covered by a checkpoint and every consumer watermark")
		}
	} else if compactWAL {
		logging.Warn(logging.WALCompactionInert,
			"--compact-wal has no effect without checkpointing; set --checkpoint-interval to a "+
				"positive duration to enable it (ADR-0131)")
	}
	if auth {
		apiOpts = append(apiOpts, api.WithAuth())
	}
	if userProvisioning {
		apiOpts = append(apiOpts, api.WithUserProvisioning())
	}
	if !vault {
		apiOpts = append(apiOpts, api.WithoutVault())
	}
	// Register a worker for each enabled script language (ADR-0047). Each runs
	// arbitrary interpreter code, so a language can be turned off with its flag; a
	// missing interpreter is warned about at startup (its tasks then park) rather
	// than failing to boot.
	for _, lang := range script.Langs {
		if !scriptLangs[lang.Name] {
			continue
		}
		ex := script.New(lang)
		ex.Timeout = scriptTimeout
		if err := ex.Check(); err != nil {
			logging.Warn(logging.ScriptWorkerMissing,
				"script worker enabled but its interpreter was not found on PATH; its script tasks "+
					"will park until it is installed",
				slog.String("language", lang.Name), slog.String("binary", lang.Bin),
				slog.String("error", err.Error()))
		} else {
			logging.Info(logging.ScriptWorkerEnabled, "script worker enabled",
				slog.String("language", lang.Name), slog.String("binary", lang.Bin))
		}
		apiOpts = append(apiOpts, api.WithScriptWorker(lang.JobType, ex))
	}
	// Workers this server runs itself (ADR-0157 step 7). The configuration comes from
	// this command line and from nowhere else: the API can restart one of these, and
	// can neither introduce a worker nor name a command.
	specs, handles := supervise.build()

	// ADR-0164's default, opt-out: the connector kinds a supervised worker can serve
	// run in a worker this server starts, so the engine's loop cannot stall behind them
	// and somebody trying Atlas configures nothing to get there. That includes mail,
	// whose configuration the server hands to the child at spawn out of its own
	// connector store — the SMTP handshake being the stall an operator actually
	// notices. --in-process-connectors returns to the old arrangement wholesale;
	// --offload-connectors adds the remaining credential-bearing kinds on top, once
	// their secrets have been moved to a worker by hand.
	//
	// One worker per kind, not one for all of them. Three reasons, and the first is
	// not about tidiness: a script task inherits its worker's whole environment, so a
	// single worker holding both the mail credential and the script interpreters
	// would let a model-authored script read the SMTP password. Separate processes
	// put that secret only where it is used. The other two follow from the same
	// split — a restart, a state and a log per kind in the Workers view, and a script
	// that pegs a core or leaks memory taking nothing else down with it, which is the
	// isolation the script kind is moved out for in the first place.
	if !inProcessConnectors {
		defaults := api.DefaultOffloadedKinds()
		offloadKinds = append(defaults, offloadKinds...)
		for _, kind := range defaults {
			specs = append(specs, api.SuperviseSpec{
				ID: kind, Kinds: []string{kind}, Connectors: []string{kind},
			})
			handles = append(handles, nil)
		}
	}
	if len(offloadKinds) > 0 {
		apiOpts = append(apiOpts, api.WithOffloadedConnectorKinds(offloadKinds))
	}
	if len(specs) > 0 {
		apiOpts = append(apiOpts, api.WithSupervisedWorkers(selfURL(addr), specs, handles))
	}
	// A worker's job history, when an operator named a clio connector for it. Atlas
	// keeps none of its own: the console's tail is memory, and everything beyond it
	// belongs in an event store where retention is already a policy (ADR-0036).
	if strings.TrimSpace(historyConnector) != "" {
		apiOpts = append(apiOpts, api.WithWorkerHistory(historyConnector, historyScope))
	}
	srv, err := api.New(proc, store, dataDir, apiOpts...)
	if err != nil {
		return err
	}
	defer srv.Close()

	// Mount the MCP "Streamable HTTP" transport at /mcp alongside the API and UI,
	// so a remote MCP client (e.g. a claude.ai custom connector) can reach the
	// same tools the stdio adapter exposes. It stays a pure adapter (ADR-0016):
	// it proxies to this server's own HTTP API over loopback rather than touching
	// the engine, so the single-writer invariant is untouched.
	//
	// Under --auth the adapter authenticates its loopback calls with the server's
	// internal service token (ADR-0049), so enabling auth no longer breaks MCP.
	// The token is empty when auth is off, in which case WithBearer is a no-op.
	//
	// The /mcp transport itself is still UNAUTHENTICATED for external callers: put
	// auth in front of it (reverse proxy) before exposing it publicly.
	mcpSrv := mcp.NewServer(mcp.NewClient(loopbackURL(addr), mcp.WithBearer(srv.InternalToken())))
	root := http.NewServeMux()
	root.Handle("/mcp", mcpSrv)
	root.Handle("/mcp/", mcpSrv)
	root.Handle("/", srv.Handler())

	httpSrv := &http.Server{Addr: addr, Handler: root}

	// Shut down cleanly on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		base := loopbackURL(addr)
		logging.Info(logging.ServerListening, "listening; recovery is complete and this instance is ready",
			slog.String("addr", addr), slog.String("ui", base+"/"), slog.String("mcp", base+"/mcp"))
		if docs {
			logging.Info(logging.ServerDocsEnabled, "API explorer enabled",
				slog.String("docs", base+"/api/docs"), slog.String("openapi", base+"/api/v1/openapi.json"))
		}
		if metricsOn {
			logging.Info(logging.ServerMetrics,
				"Prometheus metrics enabled (unauthenticated; proxy it if exposed beyond the host)",
				slog.String("metrics", base+"/metrics"))
		}
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logging.Info(logging.ServerShuttingDown, "shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

// runMCP serves the Model Context Protocol adapter on stdio, proxying tool calls
// to the Atlas server at --server. Protocol traffic uses stdin/stdout; all logs
// go to stderr so they never corrupt the JSON-RPC stream.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the Atlas server to proxy to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Protocol traffic owns stdout, so logs go to stderr and nothing else.
	if err := logging.Setup(os.Stderr, logging.DefaultFormat); err != nil {
		return err
	}
	logging.Info(logging.MCPProxying, "proxying MCP over stdio", slog.String("server", *server))

	s := mcp.NewServer(mcp.NewClient(*server))
	return s.Serve(os.Stdin, os.Stdout)
}

// splitList parses a comma-separated flag into trimmed, non-empty entries.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// superviseFlag collects repeated --supervise id=type=command entries: which worker
// processes this server should run itself, and what each one works.
//
// Repeating an id adds another type to that worker, so one process can serve several
// queues. The value is parsed here, on the server's own command line, and the parsed
// result is all the API ever sees — nothing a request carries can reach it.
type superviseFlag []superviseEntry

type superviseEntry struct {
	id      string
	handles []string // "type=command", exactly as atlas worker takes them
}

func (f superviseFlag) String() string {
	ids := make([]string, 0, len(f))
	for _, e := range f {
		ids = append(ids, e.id)
	}
	return strings.Join(ids, ",")
}

func (f *superviseFlag) Set(v string) error {
	id, handle, ok := strings.Cut(v, "=")
	id, handle = strings.TrimSpace(id), strings.TrimSpace(handle)
	jobType, command, hasCommand := strings.Cut(handle, "=")
	if !ok || id == "" || !hasCommand || strings.TrimSpace(jobType) == "" || strings.TrimSpace(command) == "" {
		return fmt.Errorf("want id=type=command, got %q", v)
	}
	for i := range *f {
		if (*f)[i].id == id {
			(*f)[i].handles = append((*f)[i].handles, handle)
			return nil
		}
	}
	*f = append(*f, superviseEntry{id: id, handles: []string{handle}})
	return nil
}

// build turns the parsed entries into what the server takes: one spec per worker
// and its handles alongside, in the same order.
func (f superviseFlag) build() ([]api.SuperviseSpec, [][]string) {
	specs := make([]api.SuperviseSpec, 0, len(f))
	handles := make([][]string, 0, len(f))
	for _, e := range f {
		kinds := make([]string, 0, len(e.handles))
		for _, h := range e.handles {
			jobType, _, _ := strings.Cut(h, "=")
			kinds = append(kinds, jobType)
		}
		specs = append(specs, api.SuperviseSpec{ID: e.id, Kinds: kinds})
		handles = append(handles, e.handles)
	}
	return specs, handles
}

// selfURL is the address a supervised worker is told to work for. It is this
// server's own listen address, with a bare port meaning loopback — a child talks to
// its parent, not out across the network.
func selfURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080"
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// handleFlag collects repeated --handle type=command pairs: what this worker
// serves, and what to run for each. A repeated flag is the shape that keeps the
// pairing unambiguous — two parallel lists would let a typo silently line the wrong
// command up with a type.
type handleFlag map[string][]string

func (h handleFlag) String() string {
	types := make([]string, 0, len(h))
	for t := range h {
		types = append(types, t)
	}
	sort.Strings(types)
	return strings.Join(types, ",")
}

func (h handleFlag) Set(v string) error {
	jobType, command, ok := strings.Cut(v, "=")
	jobType = strings.TrimSpace(jobType)
	command = strings.TrimSpace(command)
	if !ok || jobType == "" || command == "" {
		return fmt.Errorf("want type=command, got %q", v)
	}
	parts := strings.Fields(command)
	if _, dup := h[jobType]; dup {
		return fmt.Errorf("job type %q is handled twice", jobType)
	}
	h[jobType] = parts
	return nil
}

// errNothingToServe ends a worker that holds no handler at all. It leaves
// [exitNothingToServe] rather than a generic failure so a supervisor can tell "this
// worker has nothing configured yet" from "this worker crashed", and park it instead
// of restarting it into an empty configuration on a backoff loop.
var errNothingToServe = errors.New("worker: nothing to serve")

// exitNothingToServe is the status [errNothingToServe] leaves. It is api's constant
// of the same name — the supervisor is the reader, and a status the two ends
// disagreed about would put a parked worker into a restart loop; TestNothingToServe
// StatusIsTheOneTheSupervisorParksOn holds them together.
const exitNothingToServe = api.ExitNothingToServe

// runWorker runs the binary as an out-of-process worker for a running Atlas
// (ADR-0157 step 5): it leases jobs of the types it was given, runs a command for
// each, and reports the outcome. The job arrives on the command's stdin as JSON and
// whatever JSON object the command prints becomes the variables the job completes
// with; a non-zero exit fails the job, carrying stderr as the message.
//
// This is what takes a service task's work off the engine's single writer: the
// latency of the work is paid by this process, not by every other request.
func runWorker(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the Atlas server to work for")
	id := fs.String("id", defaultWorkerID(), "worker id, shown in the Workers view; give each deployment its own")
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"), "bearer token, when the server requires authentication (or ATLAS_TOKEN)")
	lease := fs.Duration("lease", worker.DefaultLease, "how long the engine holds a job for this worker; must comfortably exceed how long the work takes")
	wait := fs.Duration("wait", worker.DefaultWait, "how long a poll waits for work before asking again; the server caps it")
	maxJobs := fs.Int("max-jobs", worker.DefaultMaxJobs, "how many jobs one poll may lease; keep it to what this worker can actually run at once")
	once := fs.Bool("once", false, "poll each type once and exit, instead of working until interrupted")
	handles := handleFlag{}
	fs.Var(handles, "handle", "a job type and the command that works it, as type=command; repeat for each type")
	connectors := fs.String("connector", "", "comma-separated built-in connector kinds this worker serves (currently: ad, csv, entra, ldif, mail, mariadb, mssql, postgres, rest, script, webscrape). The server must be offloading them (it offloads csv, mail, script and webscrape by default; --in-process-connectors turns that off), or it still works them itself (ADR-0168). A kind with credentials reads them from the environment, never from a flag: mail takes ATLAS_MAIL_CONNECTORS plus, per name, ATLAS_MAIL_<NAME>_PROVIDER with _ENDPOINT, _SENDER and _SECRET — or, in the SMTP-only form, ATLAS_MAIL_<NAME>_ENDPOINT with the optional _USERNAME, _PASSWORD and _FROM. Each SQL kind takes ATLAS_<KIND>_CONNECTORS plus ATLAS_<KIND>_<NAME>_DSN, and entra takes ATLAS_ENTRA_CONNECTORS plus ATLAS_ENTRA_<NAME>_TENANT_ID, _CLIENT_ID and _CLIENT_SECRET; ad and ldif need no startup configuration, ad resolving each task's bind-password reference from ATLAS_CONNECTOR_<REF>_TOKEN. A worker Atlas supervises is handed all of that at spawn from the connector store, so it needs none of it set by hand")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kinds := splitList(*connectors)
	builtin, err := worker.BuiltinConnectors(os.Getenv, kinds...)
	if err != nil {
		return err
	}

	if err := logging.Setup(os.Stderr, logging.DefaultFormat); err != nil {
		return err
	}
	if len(handles) == 0 && len(builtin.Handlers) == 0 {
		// Nothing to serve. For a worker an operator ran by hand this is a typo, and
		// saying so and stopping is right. For a supervised one it is an ordinary
		// state — a mail worker on a server with no mail connector yet — and the
		// supervisor must park it rather than restart it into the same emptiness
		// forever, so the two are told apart by the exit status rather than by the
		// message. Configure the kind and the supervisor brings this worker back
		// holding it.
		logging.Warn(logging.WorkerStarting, "nothing to serve: give at least one --handle type=command, or configure a connector kind this worker was given",
			slog.String("connectors", strings.Join(kinds, ",")))
		return errNothingToServe
	}

	execs := map[string]worker.Exec{}
	for jobType, exec := range builtin.Handlers {
		execs[jobType] = exec
	}
	for jobType, argv := range handles {
		execs[jobType] = worker.CmdExec{Name: argv[0], Args: argv[1:]}
	}
	w := worker.New(worker.Options{
		Server: strings.TrimRight(*server, "/"), ID: *id, Token: *token,
		Handlers: execs, Lease: *lease, Wait: *wait, MaxJobs: *maxJobs,
		Connectors: builtin.Names,
	})

	logging.Info(logging.WorkerStarting, "working jobs for a running Atlas",
		slog.String("server", *server), slog.String("id", *id),
		slog.String("types", strings.Join(sortedKeys(execs), ",")),
		slog.String("connectors", strings.Join(builtin.Names, ",")), slog.Duration("lease", *lease))
	// A kind this worker was asked to serve and holds nothing for is not an error —
	// it serves the rest — but it is the answer to "why is that task waiting", so it
	// is said once, here, where the Workers console shows it.
	for _, kind := range builtin.Unconfigured {
		logging.Warn(logging.WorkerStarting, "not serving a connector kind: nothing is configured for it here",
			slog.String("kind", kind))
	}

	if *once {
		return w.RunOnce(context.Background())
	}
	// Interrupt stops the loop between polls, so a job in flight is finished and
	// reported rather than abandoned to its lease.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runErr := w.Run(ctx)
	if ctx.Err() != nil {
		return nil // interrupted, which is an ordinary exit
	}
	return runErr
}

// sortedKeys is the handled job types in a stable order, for the startup line.
func sortedKeys(m map[string]worker.Exec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// defaultWorkerID names the worker after the host it runs on, which is the useful
// default when several replicas each want their own row in the Workers view.
func defaultWorkerID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "atlas-worker"
}

// runResetPassword sets a local user's password directly against the on-disk user
// store, without a running server or a login. It is the operator recovery path
// for a self-hosted, admin-managed instance whose admin is locked out — there is
// no self-service reset and the MCP adapter is not an admin (ADR-0044/0049), so
// recovery has to be reachable from a shell (e.g. `docker exec … reset-password`).
//
// By default it generates a strong password and prints it once; --password-stdin
// reads one from stdin instead, keeping the secret out of the process arguments
// (where `ps` or shell history would expose it).
// runCheckJobTypes answers one question about a data directory: does its job-type
// table hold a model-authored type on an index a built-in has since taken?
//
// Dynamic indices are issued from one past the reserved range, and that range grows
// with every built-in connector added. A store written before such an addition has
// names sitting where a built-in now sits, and the jobs already on disk under those
// indices do not move — so they would be read as the built-in's. The table cannot
// keep such a record, and until now it dropped it in silence.
//
// This reads the directory and reports; it changes nothing. It is deliberately a
// separate command rather than only a startup warning, so the question can be
// answered about a backup, or about a server one would rather not restart.
func runCheckJobTypes(args []string) error {
	fs := flag.NewFlagSet("check-job-types", flag.ExitOnError)
	dataDir := fs.String("data-dir", "atlas-data", "the server's data directory (its job-type table lives here); must match the running server's --data-dir")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: atlas check-job-types [flags]

Report job types whose stored index a built-in job type has since taken. Reads the
data directory and changes nothing; safe against a running server and against a copy
of one.

Exit status is 0 when the table is clean and 1 when it is not, so it can gate a
deploy.

Examples:
  atlas check-job-types --data-dir /data

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	found, err := jobtype.Collisions(filepath.Join(*dataDir, "jobtypes"))
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Printf("job-type table is clean: no stored type sits on a reserved index (%s)\n", *dataDir)
		return nil
	}
	fmt.Printf("%d job type(s) sit on an index a built-in has since taken (%s):\n\n", len(found), *dataDir)
	for _, c := range found {
		fmt.Printf("  %-40s index %-4d now means %s\n", c.Name, c.Index, c.NowMeans)
	}
	fmt.Print(`
Jobs already parked under these indices still carry them, so the engine would hand
them to the built-in named above; new jobs of the same type get a fresh index. Do not
deploy over this without a plan for the parked jobs.
`)
	os.Exit(1)
	return nil
}

func runResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	dataDir := fs.String("data-dir", "atlas-data", "the server's data directory (its user store lives here); must match the running server's --data-dir")
	createAdmin := fs.Bool("create-admin", false, "if no user with this name exists, create an enabled admin with it")
	passwordStdin := fs.Bool("password-stdin", false, "read the new password from stdin instead of generating one (keeps it out of the process arguments)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: atlas reset-password [flags] USERNAME

Reset a local user's password by writing directly to the data directory, so a
locked-out admin can recover without a login. Stop the server first if you like;
it is not required (login re-reads the store on every attempt).

Examples:
  atlas reset-password --data-dir /data patrick
  echo -n 'a-strong-password' | atlas reset-password --data-dir /data --password-stdin patrick

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one USERNAME argument, got %d", len(rest))
	}
	username := rest[0]

	password, generated, err := resetPasswordValue(*passwordStdin)
	if err != nil {
		return err
	}

	res, err := api.ResetPassword(api.ResetPasswordOptions{
		DataDir:     *dataDir,
		Username:    username,
		NewPassword: password,
		CreateAdmin: *createAdmin,
		Now:         time.Now().Unix(),
	})
	if err != nil {
		return err
	}

	verb := "reset the password for"
	if res.Created {
		verb = "created admin"
	}
	logging.Info(logging.AuthPasswordReset, "atlas "+verb+" user",
		slog.String("username", res.Username), slog.String("userId", res.UserID))
	if generated {
		// The generated secret goes to stdout (not the log) so it is easy to
		// capture and does not linger in a shared log buffer.
		fmt.Printf("Generated password for %q: %s\n", res.Username, password)
	}
	return nil
}

// resetPasswordValue returns the new password to set: read from stdin when
// fromStdin is true, otherwise a freshly generated one (with generated=true so
// the caller knows to print it).
func resetPasswordValue(fromStdin bool) (password string, generated bool, err error) {
	if fromStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("read password from stdin: %w", err)
		}
		return strings.Trim(string(data), "\r\n"), false, nil
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", false, fmt.Errorf("generate password: %w", err)
	}
	return hex.EncodeToString(b), true, nil
}

// runImportMIM converts a Microsoft Identity Manager (MIM/FIM) XOML workflow —
// or an Export-FIMConfig wrapper that embeds one — into Atlas-deployable BPMN
// 2.0. The BPMN goes to stdout (or --out); a per-node conversion report goes to
// stderr so the lossy points are visible without polluting the model on stdout.
func runImportMIM(args []string) error {
	fs := flag.NewFlagSet("import-mim", flag.ExitOnError)
	out := fs.String("out", "", "write the BPMN to this file instead of stdout")
	name := fs.String("name", "", "process name to use (default: the workflow's display name)")
	quiet := fs.Bool("quiet", false, "do not print the conversion report to stderr")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: atlas import-mim [flags] [FILE]

Convert a MIM/FIM workflow (XOML, or an Export-FIMConfig XML that embeds it) into
BPMN 2.0. With no FILE, or "-", the XOML is read from stdin. Constructs without a
faithful BPMN counterpart are preserved in <atlas:mimSource> and listed in the
report; re-check the model in the Modeler before deploying.

Examples:
  atlas import-mim workflow.xoml > workflow.bpmn
  Export-FIMConfig ... | atlas import-mim --name Onboarding --out onboarding.bpmn

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	var r io.Reader = os.Stdin
	if rest := fs.Args(); len(rest) > 0 && rest[0] != "-" {
		if len(rest) != 1 {
			fs.Usage()
			return fmt.Errorf("expected at most one FILE argument, got %d", len(rest))
		}
		f, err := os.Open(rest[0])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}

	res, err := mimimport.Convert(r, *name)
	if err != nil {
		return err
	}

	if *out == "" {
		if _, err := os.Stdout.Write(res.BPMN); err != nil {
			return err
		}
	} else if err := os.WriteFile(*out, res.BPMN, 0o644); err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprint(os.Stderr, res.Report.String())
	}
	return nil
}

// fatal reports a top-level command failure and exits non-zero. It replaces log.Fatalf
// so the last thing a failed command says is a record like every other one, with an
// event name an operator's log pipeline can match (ADR-0142).
func fatal(command string, err error) {
	logging.Error(logging.CommandFailed, command+" failed",
		slog.String("command", command), slog.String("error", err.Error()))
	os.Exit(1)
}
