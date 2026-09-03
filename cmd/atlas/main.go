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
	"crypto/tls"
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
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/checkpoint"
	remedymock "github.com/pblumer/atlas/connector/remedy/mock"
	"github.com/pblumer/atlas/connector/rest/openapimock"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/jobtype"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/mimimport"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/promquery"
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
	case "mock-remedy":
		if err := runMockRemedy(args); err != nil {
			fatal("atlas mock-remedy", err)
		}
	case "mock-openapi":
		if err := runMockOpenAPI(args); err != nil {
			fatal("atlas mock-openapi", err)
		}
	case "playground":
		if err := runPlaygroundScenario(args, os.Stdout); err != nil {
			// A run that happened and did not meet its expectations leaves its own
			// status: a CI job has to tell "the process no longer holds up" from "the
			// server was unreachable", and it can only do that if the two differ. The
			// checks are already printed, so there is nothing left to say.
			if errors.Is(err, errScenarioFailed) {
				os.Exit(exitScenarioFailed)
			}
			fatal("atlas playground", err)
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
  atlas mock-remedy    [flags]      Run a mock BMC Remedy AR System for the Remedy worker
  atlas mock-openapi   [flags]      Serve a mock REST API from an OpenAPI document
  atlas playground     [flags]      Run a saved Playground scenario and exit on its verdict
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
	// TLS, where an operator supplies a certificate (ADR-0191). Both files or
	// neither: naming one without the other stops the server rather than falling
	// back to plaintext on the port somebody believed they had just secured. Unset
	// is today's behaviour exactly — plaintext, behind a reverse proxy. Turning this
	// on removes the cryptographic reason to run that proxy and not the
	// authorization one: /metrics, /healthz and /readyz stay unauthenticated by
	// design (ADR-0142), because a kubelet has no credential to offer.
	tlsCert := fs.String("tls-cert", os.Getenv("ATLAS_TLS_CERT"), "PEM certificate chain to serve --addr with, e.g. /etc/atlas/tls.crt. Set it together with --tls-key to have this server terminate TLS 1.3 itself instead of a reverse proxy doing it (ADR-0191); leave both unset for plaintext. The pair is re-read when either file changes, so a renewal needs no restart. TLS 1.3 only: there is no cipher list to configure and no --tls-min-version (or ATLAS_TLS_CERT)")
	tlsKey := fs.String("tls-key", os.Getenv("ATLAS_TLS_KEY"), "PEM private key for --tls-cert, e.g. /etc/atlas/tls.key. Both or neither (or ATLAS_TLS_KEY)")
	tlsCA := fs.String("tls-ca", os.Getenv("ATLAS_TLS_CA"), "PEM bundle of certificate authorities to trust *in addition to* the host's, when this server calls another Atlas — publishing an application to a deployment target, and reading that target's status back (ADR-0129). Point it at your internal CA where the other server's certificate comes from one; without it the host trust store is the only answer, and an internally issued certificate is refused. It never replaces the system roots, it is never a way to skip verification, and it does not touch the REST, mail or Graph workers, whose endpoints are somebody else's (or ATLAS_TLS_CA)")
	dataDir := fs.String("data-dir", "atlas-data", "directory for the write-ahead log and state store")
	shutdownTimeout := fs.Duration("shutdown-timeout", 10*time.Second, "grace period for in-flight requests on shutdown")
	docs := fs.Bool("docs", true, "serve the OpenAPI spec (/api/v1/openapi.json) and the Scalar API explorer (/api/docs); pass --docs=false to disable")
	auth := fs.Bool("auth", true, "require login for the API, the UI and /mcp; on by default (opt-out) — pass --auth=false to run open, which is for development and demos only. On the first start with an empty user store it seeds an admin from ATLAS_ADMIN_USERNAME/ATLAS_ADMIN_PASSWORD, generating and logging a password once if none is set")
	// The origin this server is reachable under from outside. Atlas terminates no
	// TLS, so behind the proxy every real deployment has, the scheme a request
	// arrives with is http and an origin derived from it names a URL no client can
	// use. Only the RFC 9728 discovery documents and the WWW-Authenticate challenge
	// read it today (ADR-0200); unset means derive from the request.
	externalURL := fs.String("external-url", os.Getenv("ATLAS_EXTERNAL_URL"), "public origin this server is reachable under, e.g. https://atlas.example.com, for the absolute URLs in the OAuth protected-resource metadata and the WWW-Authenticate challenge (ADR-0200); empty derives them from the request (or ATLAS_EXTERNAL_URL)")
	oidcIssuer := fs.String("oidc-issuer", os.Getenv("ATLAS_OIDC_ISSUER"), "OpenID Connect issuer URL people may sign in with, e.g. https://login.example.com/realms/atlas; empty means the local password is the only way in (or ATLAS_OIDC_ISSUER)")
	oidcClientID := fs.String("oidc-client-id", os.Getenv("ATLAS_OIDC_CLIENT_ID"), "client id this server was registered under at the OIDC issuer (or ATLAS_OIDC_CLIENT_ID)")
	oidcClientSecret := fs.String("oidc-client-secret", os.Getenv("ATLAS_OIDC_CLIENT_SECRET"), "client secret for the OIDC token exchange; empty is allowed for a public client, since PKCE covers the flow either way (or ATLAS_OIDC_CLIENT_SECRET)")
	oidcScopes := fs.String("oidc-scopes", os.Getenv("ATLAS_OIDC_SCOPES"), "space-separated OIDC scopes to request; empty asks for \"openid profile email\" (or ATLAS_OIDC_SCOPES)")
	oidcName := fs.String("oidc-name", os.Getenv("ATLAS_OIDC_NAME"), "what the sign-in button on the login screen says; empty uses the issuer's host (or ATLAS_OIDC_NAME)")
	// RFC 7591 self-registration, off by default. It is the one unauthenticated
	// endpoint that writes durable state, so an operator turns it on deliberately or
	// not at all; a client that would use it discovers its absence from the metadata
	// rather than by being refused (ADR-0200).
	oauthRegistration := fs.Bool("oauth-dynamic-registration", os.Getenv("ATLAS_OAUTH_DYNAMIC_REGISTRATION") == "1", "let an OAuth client register itself (RFC 7591), so a hosted MCP worker can be connected with nothing but this server's URL; off by default, because it lets anyone who can reach this port create a client record and appear on your people's consent screen under a name they chose. Each such client is marked as self-registered on that screen, and the number kept is bounded (ADR-0200). Leave it off and register clients with POST /api/v1/oauth-clients instead (or ATLAS_OAUTH_DYNAMIC_REGISTRATION=1)")
	publicFormsCORS := fs.String("public-forms-cors", os.Getenv("ATLAS_PUBLIC_FORMS_CORS_ORIGINS"), "comma-separated web origins allowed to embed a public start form cross-origin (ADR-0186); empty (default) blocks cross-origin access, \"*\" allows any origin. Opens only the cookieless /public/forms endpoints, never /api/v1 (or ATLAS_PUBLIC_FORMS_CORS_ORIGINS)")
	userProvisioning := fs.Bool("user-provisioning", true, "enable the user-provisioning worker for the protected system project's processes (create/set-password/disable Atlas logins); on by default (opt-out) — disable with --user-provisioning=false. It only ever acts for the protected system project's processes, behind their human approval step, so the boundary it reopens stays gated (ADR-0123)")
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
	offload := fs.String("offload-connectors", "", "comma-separated Worker Types this server must NOT run itself (e.g. clio,sharepoint): their jobs park for a worker instead (ADR-0168). Adds to the default set unless --in-process-connectors turns that off. A kind whose credentials live in this server's worker store and that the supervisor cannot hand over needs its secret moved to the worker by hand, so those are never defaulted. An unknown kind is refused at startup rather than ignored")
	history := fs.String("worker-history", "", "name of a clio worker to append every settled job run to, so a worker's history outlives this process (ADR-0036). The console reads it back under a worker's recent jobs; retention and querying are then your clio's, not another flag here. Off unless given, in which case the console keeps only its in-memory tail")
	historyScope := fs.String("worker-history-scope", api.HistoryScopeAll, "what --worker-history writes: \"all\" settled jobs, or \"failed\" only. All is what \"how long does a mail send take\" needs and the larger bill; failed is much less volume and still answers most of what a history is asked")
	superviseConnectors := fs.String("supervise-connector", "", "comma-separated Worker Types this server runs a worker for itself, beyond the ones it supervises by default (e.g. ad,entra). Each named kind gets its own supervised worker — handed this server's token and environment at spawn, like the default ones — and is taken off the engine, so that worker is what leases its jobs. It is the missing half of --offload-connectors, which parks a kind's jobs for a worker somebody else runs: on a server with --auth there is no credential an outside worker could hold, so without this a kind outside the defaults cannot be served at all. An unknown kind is refused at startup rather than ignored")
	inProcess := fs.Bool("in-process-connectors", false, "run every worker inside the engine, as before ADR-0164. Off by default: "+strings.Join(api.DefaultOffloadedKinds(), ", ")+" run in a worker this server starts and supervises itself, so the loop cannot stall behind them — behind an SMTP handshake above all — and trying Atlas still needs no configuration")
	supervise := superviseFlag{}
	fs.Var(&supervise, "supervise", "run a worker process for these job types and keep it running, as id=type=command; repeat for more workers, and repeat the type=command part for a worker that serves several types (ADR-0157). Off unless given: under systemd or Kubernetes the platform owns process lifecycle")
	metricsOn := fs.Bool("metrics", true, "serve the Prometheus exposition at /metrics (ADR-0142); pass --metrics=false to disable. It is unauthenticated like /healthz — put a reverse proxy in front of anything exposed beyond the host")
	// The read side of the exposition above, and a different server: this is where
	// somebody else keeps what they scraped. Panorama queries it for a node's recent
	// history (ADR-0189 P5b-ii) and stores none of the answer.
	metricsURL := fs.String("metrics-url", os.Getenv("ATLAS_METRICS_URL"), "base URL of a Prometheus-compatible store to read node history from for Panorama's architecture views (ADR-0189); empty disables it, and the metrics half of every answer then reports itself not-configured. Read-only and unrelated to --metrics, which is what this server exposes. Credentials come from ATLAS_METRICS_USERNAME/ATLAS_METRICS_PASSWORD")
	metricsInstance := fs.String("metrics-instance", os.Getenv("ATLAS_METRICS_INSTANCE"), "how this node appears in --metrics-url's `instance` label, e.g. atlas-01.internal. Atlas cannot derive it: a scrape target is your configuration, and guessing would answer about a different process while looking exactly like an answer about this one. Left empty, a Panorama element bound to this server's own runtime reports itself unidentifiable and says why")
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
	metricsCfg := promquery.Config{
		URL:      strings.TrimSpace(*metricsURL),
		Username: os.Getenv("ATLAS_METRICS_USERNAME"),
		Password: os.Getenv("ATLAS_METRICS_PASSWORD"),
		Instance: strings.TrimSpace(*metricsInstance),
	}
	retention := retentionConfig{maxAge: *retentionAge, interval: *retentionInterval, batch: *retentionBatch}
	trace := tracing.Config{
		Endpoint:    *traceEndpoint,
		ServiceName: envOr("OTEL_SERVICE_NAME", "atlas"),
		Version:     api.Version,
		SampleRatio: *traceRatio,
	}
	// Shut down cleanly on SIGINT/SIGTERM. The signal handling belongs out here with
	// the rest of the process's lifecycle, so serve is driven by a context and can
	// be started and stopped by a test.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, *addr, *dataDir, *shutdownTimeout, *docs, *auth, oauthConfig{externalURL: *externalURL, dynamicRegistration: *oauthRegistration, oidc: api.OIDCConfig{
		Issuer:       *oidcIssuer,
		ClientID:     *oidcClientID,
		ClientSecret: *oidcClientSecret,
		Scopes:       *oidcScopes,
		Name:         *oidcName,
	}}, tlsConfig{certFile: *tlsCert, keyFile: *tlsKey, caFile: *tlsCA}, *vault, *userProvisioning, enabled, *scriptTimeout, osCfg, metricsCfg, retention, *checkpointInterval, *checkpointKeep, *compactWAL, *metricsOn, logging.Format(*logFormat), trace, supervise, splitList(*offload), splitList(*superviseConnectors), *inProcess, *history, *historyScope, *publicFormsCORS)
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

// oauthConfig is the OAuth-facing half of the serve flags, kept together so the
// origin the discovery documents publish and the decision to open self-registration
// travel as one thing rather than as two more positional arguments (ADR-0200).
type oauthConfig struct {
	externalURL         string
	dynamicRegistration bool

	// oidc is the identity provider people may sign in with, when an operator named
	// one (ADR-0210). It rides along here because it shares
	// the one thing that has to be right for both — the origin this server is
	// reachable under, which is what a redirect URI is built from.
	oidc api.OIDCConfig
}

// tlsConfig is the transport half of the serve flags (ADR-0191), kept together for
// the reason oauthConfig is: what this server serves TLS with and what it verifies
// another Atlas against are one subject, not three more positional arguments.
type tlsConfig struct {
	// certFile and keyFile are the pair --addr is served with. Both empty is
	// plaintext, which is what every deployment behind a proxy keeps.
	certFile, keyFile string

	// caFile is an operator's CA bundle, trusted *in addition to* the host's roots
	// by the clients that call another Atlas. It is deliberately not global: a
	// worker reaching a third party has its own endpoint and its own trust.
	caFile string
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

func serve(ctx context.Context, addr, dataDir string, shutdownTimeout time.Duration, docs, auth bool, oauth oauthConfig, tlsCfg tlsConfig, vault, userProvisioning bool, scriptLangs map[string]bool, scriptTimeout time.Duration, osExport opensearch.Config, metricsQuery promquery.Config, retention retentionConfig, checkpointInterval time.Duration, checkpointKeep int, compactWAL, metricsOn bool, logFormat logging.Format, traceCfg tracing.Config, supervise superviseFlag, offloadKinds, superviseConnectors []string, inProcessConnectors bool, historyConnector, historyScope, publicFormsCORS string) error {
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

	// TLS, where the operator supplied a certificate (ADR-0191). Before the data
	// directory, so a wrong path or a mismatched pair stops the server without
	// having touched anything durable.
	tlsOn, err := tlsConfigured(tlsCfg.certFile, tlsCfg.keyFile)
	if err != nil {
		return err
	}
	var (
		serverTLS  *tls.Config
		loopbackLn net.Listener
	)
	if tlsOn {
		if serverTLS, err = newServerTLSConfig(tlsCfg.certFile, tlsCfg.keyFile); err != nil {
			return err
		}
		// The plaintext listener this process's own children use, bound here because
		// its port has to be known before the handler that hands it to them is built.
		// Nothing accepts on it until serveUntil below; a child that gets there first
		// waits in the backlog rather than being refused.
		if loopbackLn, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return fmt.Errorf("bind the loopback listener: %w", err)
		}
		defer loopbackLn.Close()
	}
	// What the MCP adapter's loopback client and every supervised worker call back
	// on. Without TLS it is this server's own address, exactly as before.
	internal := internalURL(addr, loopbackLn)

	// The other half of TLS: what this server trusts when it calls another Atlas.
	// A peer on-prem usually presents a certificate an internal CA issued, and
	// without its bundle the https:// a deployment target demands (ADR-0129) fails
	// at verification instead of connecting.
	targetRoots, err := trustPool(tlsCfg.caFile)
	if err != nil {
		return err
	}

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

	// This binary links the database drivers (through the worker package), so it can
	// hand the server a way to check a SQL worker's connection string. The api
	// package deliberately links none of them (ADR-0173), which is why the check is
	// wired in here rather than imported there.
	apiOpts := []api.Option{api.WithLogBuffer(logs), api.WithSystemProcesses(), api.WithSQLProbe(worker.ProbeSQL)}
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
	// Read a node's recent history back from a metrics store for Panorama's
	// architecture views (ADR-0189 P5b-ii). Read-only, and a different server from
	// the exposition above: nothing is copied here, each answer is queried when
	// somebody asks for it.
	if metricsQuery.Enabled() {
		apiOpts = append(apiOpts, api.WithMetricsQuery(metricsQuery))
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
	if targetRoots != nil {
		apiOpts = append(apiOpts, api.WithTargetTLSRoots(targetRoots))
	}
	if oauth.externalURL != "" {
		apiOpts = append(apiOpts, api.WithExternalURL(oauth.externalURL))
	}
	if oauth.oidc.Issuer != "" {
		apiOpts = append(apiOpts, api.WithOIDC(oauth.oidc))
		// Said at startup because it is the one configuration that makes this server
		// depend on somebody else being up, and because an operator reading the log
		// after a failed login needs to know which issuer was asked.
		logging.Info(logging.AuthOIDCConfigured,
			"an identity provider is configured: people may sign in with it, and a first login "+
				"creates an account with the user role and nothing else unless a claim mapping "+
				"is switched on under Organization",
			slog.String("issuer", oauth.oidc.Issuer), slog.String("client_id", oauth.oidc.ClientID))
	}
	if oauth.dynamicRegistration {
		apiOpts = append(apiOpts, api.WithDynamicClientRegistration())
		// Said out loud, once, at startup — the same reason --auth=false is. An open
		// registration endpoint is a legitimate thing to want, and it is the sort of
		// thing that gets turned on for one worker and then forgotten.
		logging.Warn(logging.AuthOAuthRegistrationOpen,
			"OAuth self-registration is OPEN: anyone who can reach this port may register a client "+
				"and be shown on your people's consent screens under a name they chose. Each is marked "+
				"there as self-registered, and no client reaches anything until a person approves it. "+
				"Drop --oauth-dynamic-registration to require an administrator to register clients")
	}
	if auth {
		apiOpts = append(apiOpts, api.WithAuth())
	} else {
		// The one line that says this instance is open. Running without a login is a
		// legitimate thing to want — a laptop, a demo, a throwaway container — but it
		// is now the deliberate exception, and an exception nobody is told about is
		// how a demo becomes a deployment (ADR-0195).
		logging.Warn(logging.AuthDisabled,
			"running WITHOUT authentication: the API, the web UI and /mcp are open to "+
				"anyone who can reach this port, and /mcp can deploy and run processes. "+
				"Drop --auth=false to require a login")
	}
	if strings.TrimSpace(publicFormsCORS) != "" {
		apiOpts = append(apiOpts, api.WithPublicFormsCORS(strings.Split(publicFormsCORS, ",")))
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

	// ADR-0164's default, opt-out: the Worker Types a supervised worker can serve
	// run in a worker this server starts, so the engine's loop cannot stall behind them
	// and somebody trying Atlas configures nothing to get there. That includes mail,
	// whose configuration the server hands to the child at spawn out of its own
	// worker store — the SMTP handshake being the stall an operator actually
	// notices — and Active Directory, whose per-task bind-password references are
	// handed over the same way (ADR-0182). --in-process-connectors
	// returns to the old arrangement wholesale; --offload-connectors adds the remaining
	// credential-bearing kinds on top, once their secrets have been moved to a worker
	// by hand.
	//
	// One worker per kind, not one for all of them. Three reasons, and the first is
	// not about tidiness: a script task inherits its worker's whole environment, so a
	// single worker holding both the mail credential and the script interpreters
	// would let a model-authored script read the SMTP password. Separate processes
	// put that secret only where it is used. The other two follow from the same
	// split — a restart, a state and a log per kind in the Workers view, and a script
	// that pegs a core or leaks memory taking nothing else down with it, which is the
	// isolation the script kind is moved out for in the first place.
	if inProcessConnectors {
		// The escape hatch of ADR-0233: still supported,
		// and no longer silent. An operator has reasons — a host only the engine can
		// reach, a credential not yet moved — but a flag that quietly puts every
		// integration back on the run loop is how an installation ends up there without
		// anyone having decided it.
		logging.Warn(logging.WorkerSupervisorFailed,
			"--in-process-connectors puts every connector kind back on the engine's run loop; "+
				"a slow or hung host then stalls the whole engine (ADR-0164). This is deprecated and "+
				"becomes an error once every kind has a worker",
			slog.String("kinds", strings.Join(api.DefaultOffloadedKinds(), ",")))
	}
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
	// Worker-only kinds Atlas supervises by default (ADR-0172): a kind with no in-engine
	// form at all, so --in-process-connectors cannot apply and there is nothing to
	// offload. The worker parks until an operator configures a tenant in the Console,
	// then refresh brings it up — the tenant a Console entry, not a deployment change.
	for _, kind := range api.DefaultSupervisedWorkerOnlyKinds() {
		specs = append(specs, api.SuperviseSpec{
			ID: kind, Kinds: []string{kind}, Connectors: []string{kind},
		})
		handles = append(handles, nil)
	}
	// Kinds the operator asked this server to run a worker for. After the defaults, so
	// asking for one of them is the no-op it should be rather than a second worker
	// racing the first for the same jobs.
	askedSpecs, askedOffload, err := superviseConnectorSpecs(superviseConnectors, specs)
	if err != nil {
		return err
	}
	for _, spec := range askedSpecs {
		specs = append(specs, spec)
		handles = append(handles, nil)
	}
	offloadKinds = append(offloadKinds, askedOffload...)
	if len(offloadKinds) > 0 {
		apiOpts = append(apiOpts, api.WithOffloadedConnectorKinds(offloadKinds))
	}
	if len(specs) > 0 {
		apiOpts = append(apiOpts, api.WithSupervisedWorkers(internal, specs, handles))
	}
	// A worker's job history, when an operator named a clio worker for it. Atlas
	// keeps none of its own: the console's tail is memory, and everything beyond it
	// belongs in an event store where retention is already a policy (ADR-0036).
	if strings.TrimSpace(historyConnector) != "" {
		apiOpts = append(apiOpts, api.WithWorkerHistory(historyConnector, historyScope))
	}
	// The MCP "Streamable HTTP" transport, so a remote MCP client (e.g. a claude.ai
	// custom connector) can reach the same tools the stdio adapter exposes. It stays
	// a pure adapter (ADR-0016): it proxies to this server's own HTTP API over
	// loopback rather than touching the engine, so the single-writer invariant is
	// untouched.
	//
	// It is handed to the api server rather than mounted beside it, so /mcp passes
	// the same access boundary as every other route. Two things follow, and they are
	// the point (ADR-0196): under --auth a request that
	// carries no credential is refused at /mcp before the adapter sees it, and the
	// adapter is given no credential of its own to make up the difference — it
	// forwards whatever authenticated the caller, so a tool call is exactly as
	// privileged as whoever made it.
	//
	// This used to be a second mux out here, with the server's internal service
	// token attached to every loopback call (ADR-0049). withAuth never saw those
	// requests, so anything that could reach the port drove the whole API.
	apiOpts = append(apiOpts, api.WithMCP(mcp.NewServer(mcp.NewClient(internal))))

	srv, err := api.New(proc, store, dataDir, apiOpts...)
	if err != nil {
		return err
	}
	defer srv.Close()

	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), TLSConfig: serverTLS}
	listeners := []httpListener{{srv: httpSrv, serve: func() error {
		if !tlsOn {
			return httpSrv.ListenAndServe()
		}
		// The pair is served by TLSConfig's GetCertificate, which re-reads it when it
		// changes; the filename arguments here would read it once and never again,
		// so they are deliberately empty (ADR-0191).
		return httpSrv.ListenAndServeTLS("", "")
	}}}
	if loopbackLn != nil {
		loopbackSrv := &http.Server{Handler: srv.Handler()}
		listeners = append(listeners, httpListener{srv: loopbackSrv, serve: func() error {
			return loopbackSrv.Serve(loopbackLn)
		}})
	}

	// The origin a person can use, which with a TLS listener is no longer the one
	// this process's children were handed.
	base := reachableOrigin(oauth.externalURL, addr, tlsOn)
	logging.Info(logging.ServerListening, "listening; recovery is complete and this instance is ready",
		slog.String("addr", addr), slog.String("ui", base+"/"), slog.String("mcp", base+"/mcp"),
		slog.Bool("tls", tlsOn))
	if docs {
		logging.Info(logging.ServerDocsEnabled, "API explorer enabled",
			slog.String("docs", base+"/api/docs"), slog.String("openapi", base+"/api/v1/openapi.json"))
	}
	if metricsOn {
		logging.Info(logging.ServerMetrics,
			"Prometheus metrics enabled (unauthenticated; proxy it if exposed beyond the host)",
			slog.String("metrics", base+"/metrics"))
	}
	return serveUntil(ctx, shutdownTimeout, listeners...)
}

// runMCP serves the Model Context Protocol adapter on stdio, proxying tool calls
// to the Atlas server at --server, authenticating with --token (or ATLAS_TOKEN)
// where that server requires a login. Protocol traffic uses stdin/stdout; all logs
// go to stderr so they never corrupt the JSON-RPC stream.
func runMCP(args []string) error {
	// Protocol traffic owns stdout, so logs go to stderr and nothing else.
	if err := logging.Setup(os.Stderr, logging.DefaultFormat); err != nil {
		return err
	}
	return runMCPOn(args, os.Stdin, os.Stdout)
}

// runMCPOn is runMCP with its streams supplied, so a test can drive a JSON-RPC
// message through the real flag parsing, client construction and dispatch — the
// credential's path in particular, which is wiring and therefore exactly the part
// a unit test of the mcp package cannot reach.
func runMCPOn(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the Atlas server to proxy to")
	// The adapter is a per-agent process with one identity for its whole life, so
	// unlike the HTTP transport — which forwards each request's own caller — it
	// authenticates with a credential given here. Without one it cannot work against
	// a server running --auth at all: every tool call comes back 401
	// (ADR-0196). The shape is `atlas worker --token`'s,
	// because it is the same need.
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"),
		"bearer token, when the server requires authentication (or ATLAS_TOKEN)")
	// The adapter is a client of a remote Atlas like the promotion path and
	// `atlas worker` are, so it hits the same wall: a server whose certificate an
	// internal CA issued is refused, and there is deliberately no way to skip that
	// (ADR-0191). Usually unnecessary — this runs on a person's own machine, whose
	// trust store already carries the company CA — which is why it took a record's
	// follow-up rather than the record itself.
	tlsCA := fs.String("tls-ca", os.Getenv("ATLAS_TLS_CA"),
		"PEM bundle of certificate authorities to trust *in addition to* the host's, when --server is https and its certificate comes from an internal CA (ADR-0191). Never a way to skip verification: there is none (or ATLAS_TLS_CA)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Before the first tool call, so a bundle that cannot be read stops a process
	// the agent can see did not start, rather than failing every call for a reason
	// nothing in the answer explains.
	roots, err := trustPool(*tlsCA)
	if err != nil {
		return err
	}
	// Trimmed because a token exported from a shell profile or read out of a file
	// routinely carries a trailing newline, and a bearer sent with one is refused
	// for a reason nothing in the 401 explains.
	bearer := strings.TrimSpace(*token)

	// Whether a credential is configured, never the credential itself: an attribute
	// is what a log shipper extracts, indexes and keeps. It is here because "every
	// tool returns 401" and "no token was set" are the same incident, and an
	// operator should not have to guess that.
	logging.Info(logging.MCPProxying, "proxying MCP over stdio",
		slog.String("server", *server), slog.Bool("authenticated", bearer != ""),
		slog.Bool("extra_ca", roots != nil))

	s := mcp.NewServer(mcp.NewClient(*server, mcp.WithBearer(bearer), mcp.WithTLSRoots(roots)))
	return s.Serve(in, out)
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

// superviseConnectorSpecs turns --supervise-connector kinds into the workers this
// server starts for them, and the kinds it must therefore stop working itself.
//
// It exists because offloading and supervising were only ever paired for the four
// default kinds (ADR-0164). --offload-connectors takes a kind away from the engine
// and leaves its jobs parked for a worker somebody else runs; --supervise names a
// *job type* and an external command, so it cannot ask for a built-in worker. A
// kind outside the defaults was therefore reachable only by running `atlas worker
// --connector <kind>` yourself — which a server with --auth makes impossible, since
// the job pull is authenticated and the only bearer credentials are this server's
// ephemeral internal token (handed to its own children, never published) and a
// deploy token allowlisted to two endpoints. ADR-0181 named the friction as a
// follow-up for AD's mock mode; on an authenticated server it is not friction but a
// wall, and the same one stands in front of every worker-only kind.
//
// The pairing is the same one the defaults get: a worker for the kind, and the kind
// removed from the engine so that worker is the one that leases its jobs. A
// worker-only kind (entra) is supervised without being offloaded — there are no
// in-process handlers to remove, and naming it in the offload list is refused at
// startup as an unknown kind.
func superviseConnectorSpecs(kinds []string, supervised []api.SuperviseSpec) ([]api.SuperviseSpec, []string, error) {
	var (
		specs   []api.SuperviseSpec
		offload []string
	)
	for _, kind := range kinds {
		if !slices.Contains(worker.KnownConnectorKinds(), kind) {
			return nil, nil, fmt.Errorf("atlas: cannot supervise a worker for Worker Type %q: no such kind (have %s)",
				kind, strings.Join(worker.KnownConnectorKinds(), ", "))
		}
		// Two workers leasing one kind is not a configuration error the operator would
		// ever see reported — it is two processes racing for the same jobs — so asking
		// for a kind the defaults already supervise is a no-op rather than a second one.
		if slices.ContainsFunc(supervised, func(s api.SuperviseSpec) bool { return s.ID == kind }) ||
			slices.ContainsFunc(specs, func(s api.SuperviseSpec) bool { return s.ID == kind }) {
			continue
		}
		specs = append(specs, api.SuperviseSpec{ID: kind, Kinds: []string{kind}, Connectors: []string{kind}})
		if api.IsOffloadableKind(kind) {
			offload = append(offload, kind)
		}
	}
	return specs, offload, nil
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
	tlsCA := fs.String("tls-ca", os.Getenv("ATLAS_TLS_CA"), "PEM bundle of certificate authorities to trust *in addition to* the host's, when --server is https and its certificate comes from an internal CA (ADR-0191). Without it the host trust store is the only answer. It is never a way to skip verification: there is none (or ATLAS_TLS_CA)")
	lease := fs.Duration("lease", worker.DefaultLease, "how long the engine holds a job for this worker; must comfortably exceed how long the work takes")
	wait := fs.Duration("wait", worker.DefaultWait, "how long a poll waits for work before asking again; the server caps it")
	maxJobs := fs.Int("max-jobs", worker.DefaultMaxJobs, "how many jobs one poll may lease; keep it to what this worker can actually run at once")
	once := fs.Bool("once", false, "poll each type once and exit, instead of working until interrupted")
	handles := handleFlag{}
	fs.Var(handles, "handle", "a job type and the command that works it, as type=command; repeat for each type")
	connectors := fs.String("connector", "", "comma-separated built-in Worker Types this worker serves (currently: ad, csv, entra, jira, ldif, mail, mariadb, mssql, postgres, remedy, rest, script, webscrape). The server must be offloading them (it offloads ad, csv, jira, mail, remedy, script and webscrape by default; --in-process-connectors turns that off), or it still works them itself (ADR-0168). A kind with credentials reads them from the environment, never from a flag: mail takes ATLAS_MAIL_CONNECTORS plus, per name, ATLAS_MAIL_<NAME>_PROVIDER with _ENDPOINT, _SENDER and _SECRET — or, in the SMTP-only form, ATLAS_MAIL_<NAME>_ENDPOINT with the optional _USERNAME, _PASSWORD and _FROM. Each SQL kind takes ATLAS_<KIND>_CONNECTORS plus ATLAS_<KIND>_<NAME>_DSN — or, with ATLAS_<KIND>_MOCK=1, no DSN at all: the worker then answers that product's statements from seeded answers in its own memory, so a model that reads or writes a database runs end to end without one, and ATLAS_<KIND>_MOCK_SEED names the JSON file of answers it starts with (a statement nobody seeded fails naming itself rather than answering no rows). entra takes ATLAS_ENTRA_CONNECTORS plus ATLAS_ENTRA_<NAME>_TENANT_ID, _CLIENT_ID and _CLIENT_SECRET, remedy takes ATLAS_REMEDY_CONNECTORS plus ATLAS_REMEDY_<NAME>_ENDPOINT, _USERNAME and _PASSWORD, and jira takes ATLAS_JIRA_CONNECTORS plus ATLAS_JIRA_<NAME>_URL and exactly one credential shape — _EMAIL with _API_TOKEN for Jira Cloud, or _TOKEN alone for a Data Center personal access token, because that shape also decides how an assignee is addressed and which search endpoint is used; ad and ldif need no startup configuration, ad resolving each task's bind-password reference from ATLAS_CONNECTOR_<REF>_TOKEN. Set ATLAS_AD_MOCK=1 to serve Active Directory tasks against a mock directory in this worker's memory instead of a real one — the models stay unchanged, nothing reaches a domain controller, and ATLAS_AD_MOCK_SEED names an LDIF or DSML file of entries it starts with. Point ATLAS_AD_MOCK_VIEW_URL at an Atlas's /api/v1/ad/mock-directory and the worker reports the forest it holds, so it shows up under Operations > Mock directory instead of only in this worker's log. A worker this server supervises is switched from Console > Workers instead, which needs no restart; these variables are for a worker you run yourself, and for what a server does before anyone has used that switch. A worker Atlas supervises is handed all of that at spawn from the worker store, so it needs none of it set by hand")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kinds := splitList(*connectors)
	// The workers are built from the environment alone, and one of them has to know
	// this worker's own id: a mock AD directory is reported to the Console under it
	// (ADR-0213). --id is therefore read back out of
	// the environment rather than threaded through as a parameter, so an external
	// worker that sets ATLAS_WORKER_ID by hand and one started with --id look the same
	// from in there.
	env := func(name string) string {
		if name == worker.WorkerIDEnv {
			return *id
		}
		return os.Getenv(name)
	}
	builtin, err := worker.BuiltinConnectors(env, kinds...)
	if err != nil {
		return err
	}

	if err := logging.Setup(os.Stderr, logging.DefaultFormat); err != nil {
		return err
	}
	if len(handles) == 0 && len(builtin.Handlers) == 0 {
		// Nothing to serve. For a worker an operator ran by hand this is a typo, and
		// saying so and stopping is right. For a supervised one it is an ordinary
		// state — a mail worker on a server with no mail worker yet — and the
		// supervisor must park it rather than restart it into the same emptiness
		// forever, so the two are told apart by the exit status rather than by the
		// message. Configure the kind and the supervisor brings this worker back
		// holding it.
		logging.Warn(logging.WorkerStarting, "nothing to serve: give at least one --handle type=command, or configure a Worker Type this worker was given",
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
	// A worker on the server's own host reaches it over loopback and needs none of
	// this; one on another host is the case --tls-ca exists for, and its bearer
	// token is what would otherwise cross a network in the clear.
	serverRoots, err := trustPool(*tlsCA)
	if err != nil {
		return err
	}
	opts := worker.Options{
		Server: strings.TrimRight(*server, "/"), ID: *id, Token: *token,
		Handlers: execs, Lease: *lease, Wait: *wait, MaxJobs: *maxJobs,
		Connectors: builtin.Names,
	}
	if serverRoots != nil {
		// Only where a CA was named: otherwise worker.New builds the client, and a
		// worker that does not need this keeps exactly the one it always had. One
		// request may block for a whole poll and is followed by the work itself, so
		// the ceiling is the poll plus the lease, as that default is.
		opts.HTTP = &http.Client{
			Timeout:   *wait + *lease + 30*time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: serverRoots, MinVersion: tls.VersionTLS12}},
		}
	}
	w := worker.New(opts)

	logging.Info(logging.WorkerStarting, "working jobs for a running Atlas",
		slog.String("server", *server), slog.String("id", *id),
		slog.String("types", strings.Join(sortedKeys(execs), ",")),
		slog.String("connectors", strings.Join(builtin.Names, ",")), slog.Duration("lease", *lease))
	// A kind this worker was asked to serve and holds nothing for is not an error —
	// it serves the rest — but it is the answer to "why is that task waiting", so it
	// is said once, here, where the Workers console shows it.
	for _, kind := range builtin.Unconfigured {
		logging.Warn(logging.WorkerStarting, "not serving a Worker Type: nothing is configured for it here",
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

// runMockRemedy runs an in-memory mock BMC Remedy AR System REST API (ADR-0106), so a
// Remedy worker can be exercised end to end without a real Remedy / Helix ITSM
// instance. Point a managed Remedy worker's base URL at the address it prints, put
// the worker's {"username","password"} bundle in the vault, and a Remedy worker
// task creates entries against the mock; GET /mock/entries shows what was created.
func runMockRemedy(args []string) error {
	fs := flag.NewFlagSet("mock-remedy", flag.ExitOnError)
	addr := fs.String("addr", ":8008", "HTTP listen address for the mock AR System")
	user := fs.String("user", "", "required login username (empty accepts any non-empty credentials)")
	password := fs.String("password", "", "required login password (empty accepts any non-empty credentials)")
	idPrefix := fs.String("id-prefix", "INC", "prefix for generated entry ids (e.g. INC for incidents, CRQ for changes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := []remedymock.Option{remedymock.WithIDPrefix(*idPrefix)}
	if *user != "" || *password != "" {
		opts = append(opts, remedymock.WithCredentials(*user, *password))
	}
	mock := remedymock.New(opts...)

	// The banner is a human-facing hint printed straight to stderr; the mock is a dev
	// aid, so it stays out of the structured logging pipeline the server uses.
	base := loopbackURL(*addr)
	fmt.Fprintf(os.Stderr, "atlas mock-remedy: mock BMC Remedy AR System listening on %s\n", *addr)
	fmt.Fprintf(os.Stderr, "  login:   POST %s/api/jwt/login\n", base)
	fmt.Fprintf(os.Stderr, "  create:  POST %s/api/arsys/v1/entry/{form}\n", base)
	fmt.Fprintf(os.Stderr, "  inspect: GET  %s/mock/entries\n", base)
	if *user == "" && *password == "" {
		fmt.Fprintln(os.Stderr, "  credentials: any non-empty username/password is accepted (set --user/--password to require a match)")
	}
	fmt.Fprintf(os.Stderr, "wire it up: set a Remedy worker's endpoint to %s and store its {\"username\":…,\"password\":…} bundle in the vault\n", base)

	return runMockServer(*addr, mock.Handler())
}

// runMockServer serves a mock until the process is interrupted. Both mock
// subcommands land here: a mock is a foreground dev aid, so it stays out of the
// server's structured logging and shuts down on the signal a terminal sends.
func runMockServer(addr string, handler http.Handler) error {
	httpSrv := &http.Server{Addr: addr, Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
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
		fmt.Fprintln(os.Stderr, "shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

// runMockOpenAPI serves a mock REST API from an OpenAPI 3 document, so a process with
// a REST task can be run before the API it calls exists — or without
// pointing a draft at the real one. Where `atlas mock-remedy` hand-implements the
// endpoints of one Worker Type, this serves whatever a document describes: point a
// REST task's url at the address it prints and nothing else about the model changes.
//
// What it answers is the document's own examples where it states them and values
// generated from its schemas where it does not, always the same way for the same
// document. GET /__mock/calls is the journal of what a run actually did, and
// GET /__mock/report is that journal in the envelope the Console's Mockups view takes
// (ADR-0216).
func runMockOpenAPI(args []string) error {
	fs := flag.NewFlagSet("mock-openapi", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to the OpenAPI 3 document (JSON or YAML) to mock — required")
	addr := fs.String("addr", ":8009", "HTTP listen address for the mock API")
	basePath := fs.String("base-path", "", `serve the document's paths under this prefix instead of the path in its first server URL ("/" serves them at the root)`)
	specRoot := fs.String("spec-root", "", "directory the document's $refs to other files may read (default: the document's own directory)")
	id := fs.String("id", defaultWorkerID(), "name this mock reports itself under")
	quiet := fs.Bool("quiet", false, "do not print one line per served call")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*specPath) == "" {
		return errors.New("--spec is required: the OpenAPI document to mock")
	}
	// A document published as a tree of files is read, with each reference resolved
	// against the directory of the file it is written in. What may be read is bounded:
	// this mock serves what it reads and authenticates nobody, so the default root is
	// the document's own directory and widening it is the operator's decision.
	root := *specRoot
	if strings.TrimSpace(root) == "" {
		root = filepath.Dir(*specPath)
	}
	spec, err := openapimock.LoadFileUnder(*specPath, root)
	if err != nil {
		return err
	}
	opts := []openapimock.Option{openapimock.WithID(*id)}
	if !*quiet {
		opts = append(opts, openapimock.WithLog(os.Stderr))
	}
	if *basePath != "" {
		opts = append(opts, openapimock.WithBasePath(*basePath))
	}
	mock := openapimock.New(spec, opts...)

	printMockOpenAPIBanner(os.Stderr, spec, mock, *specPath, *addr, loopbackURL(*addr))
	return runMockServer(*addr, mock.Handler())
}

// plural renders a count with its noun: "1 file", "5 files".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// printMockOpenAPIBanner tells the operator what was loaded and what to do with it.
// Like the Remedy mock's, it is a human-facing hint written straight to stderr: a mock
// is a dev aid, and stays out of the structured logging pipeline the server uses.
func printMockOpenAPIBanner(w io.Writer, spec *openapimock.Spec, mock *openapimock.Server, specPath, addr, base string) {
	from := specPath
	if spec.Files > 0 {
		from = fmt.Sprintf("%s and %s", specPath, plural(spec.Files, "file"))
	}
	fmt.Fprintf(w, "atlas mock-openapi: %s — %d operations from %s, listening on %s\n",
		spec.Name(), len(spec.Operations), from, addr)
	// The routes are the thing an operator needs in front of them, but a large
	// document would bury the rest of the banner, so a long list is cut short and the
	// journal below answers what was actually called.
	const shown = 10
	// The compiled order is the matcher's — most specific first — which reads as
	// shuffled to a person. A banner is for reading, so it goes back to path order.
	routes := make([]openapimock.Operation, len(spec.Operations))
	copy(routes, spec.Operations)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	for i, op := range routes {
		if i == shown {
			fmt.Fprintf(w, "  … and %d more\n", len(routes)-shown)
			break
		}
		fmt.Fprintf(w, "  %-6s %s%s%s\n", op.Method, base, mock.BasePath(), op.Path)
	}
	// A dropped media type is a small hole in the mock. Saying so here costs three
	// lines and saves the afternoon somebody would otherwise spend on the 406.
	if len(spec.Skipped) > 0 {
		const shownSkipped = 3
		for i, entry := range spec.Skipped {
			if i == shownSkipped {
				fmt.Fprintf(w, "  … and %d more\n", len(spec.Skipped)-shownSkipped)
				break
			}
			fmt.Fprintf(w, "  no body for %s — this mock generates JSON and copies text through\n", entry)
		}
	}
	fmt.Fprintf(w, "  journal: GET %s/__mock/calls\n", base)
	fmt.Fprintf(w, "  report:  GET %s/__mock/report\n", base)
	fmt.Fprintf(w, "wire it up: point a REST worker task's url at %s%s/… — the model needs no other change\n", base, mock.BasePath())
	fmt.Fprintln(w, "  a caller asks for a stated error path with the header `Prefer: code=404`")
}

// runResetPassword sets a local user's password directly against the on-disk user
// store, without a running server or a login. It is the operator recovery path
// for a self-hosted, admin-managed instance whose admin is locked out — there is
// no self-service reset, and MCP is gated too (ADR-0044,
// ADR-0196), so recovery has to be reachable from a
// shell (e.g. `docker exec … reset-password`).
//
// By default it generates a strong password and prints it once; --password-stdin
// reads one from stdin instead, keeping the secret out of the process arguments
// (where `ps` or shell history would expose it).
// runCheckJobTypes answers one question about a data directory: does its job-type
// table hold a model-authored type on an index a built-in has since taken?
//
// Dynamic indices are issued from one past the reserved range, and that range grows
// with every built-in worker added. A store written before such an addition has
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
