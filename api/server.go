// Package api is the single-binary server surface for Atlas: it embeds one
// engine.Processor behind an HTTP API and serves an embedded web UI, so a single
// self-contained binary can deploy BPMN models, run instances, and (as the UI
// grows) view them in a browser. See ADR-0011.
//
// # Respecting the single-writer invariant
//
// The engine is a single-writer partition (invariant I3): exactly one goroutine
// may touch a partition's processor and state. HTTP handlers, by contrast, run
// concurrently. The Server bridges the two by owning a run loop goroutine that is
// the sole toucher of the processor; handlers submit closures to it via do and
// block for the result. No processor method is ever called from a handler
// goroutine directly.
//
// # Scope of the skeleton
//
// This is the Milestone S skeleton (ROADMAP.md): deploy XML, create an instance,
// read stats, health, and a static UI shell. Deployments are durable via an
// on-disk sidecar store (ADR-0019) reloaded on startup, so diagrams, versions,
// and recovered instances survive a restart; the eventual event-sourced
// deployment path arrives with the Milestone 4 public API. One honest limitation
// remains: there is no streaming job-worker transport yet (that follows the gRPC
// job protocol, ADR-0007), so an instance parks at its service task — exactly the
// "waiting token" the live viewer shows. Such a parked job can be finished by
// hand over HTTP (POST /jobs/{key}/complete, the operator mirror of .../fail from
// the incident model), but that is a synchronous operator affordance, not the
// leased, at-least-once worker protocol.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/api/collab"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/connector/envname"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/connector/ldap"
	"github.com/pblumer/atlas/connector/ldif"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/connector/rest"
	"github.com/pblumer/atlas/connector/scim"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/connector/soap"
	"github.com/pblumer/atlas/connector/temis"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/jobtype"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/metrics"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/tracing"

	"github.com/pblumer/atlas/api/processdoc"
	"github.com/pblumer/atlas/api/token"
	"github.com/pblumer/atlas/api/vault"
)

// dmnResolverFromEnv picks the DMN model source. When ATLAS_DMN_RESOLVER_URL is
// set, models are fetched from that temis model service (with an optional bearer
// token from ATLAS_DMN_RESOLVER_TOKEN); otherwise the zero-config on-disk folder
// dir is used. Both satisfy dmn.Resolver (ADR-0034/0014).
func dmnResolverFromEnv(dir string) dmn.Resolver {
	if base := strings.TrimSpace(os.Getenv("ATLAS_DMN_RESOLVER_URL")); base != "" {
		return dmn.ServiceResolver{
			BaseURL: base,
			Token:   strings.TrimSpace(os.Getenv("ATLAS_DMN_RESOLVER_TOKEN")),
		}
	}
	return dmn.DirResolver{Dir: dir}
}

// temisRegistryFromEnv builds a temis decision-connector registry from the
// environment alone (the pre-managed mechanism, ADR-0041's env secret model):
// ATLAS_TEMIS_CONNECTORS lists names, and per name N, ATLAS_TEMIS_<N>_URL /
// ATLAS_TEMIS_<N>_TOKEN configure it. Managed connector instances are layered on
// top of this by [Server.buildTemisClients]; this helper is the env-only base.
func temisRegistryFromEnv() *temis.Registry {
	reg := temis.NewRegistry()
	reg.Replace(envTemisClients())
	return reg
}

// connectorEnvKey normalizes a connector name into its environment-variable
// fragment: upper-case, with each run of non-alphanumeric characters collapsed to
// a single underscore and leading/trailing underscores trimmed.
//
// The fold itself is [envname.Key], because a worker reads the variables this
// renders and a connector has to name the one it could not resolve. Three packages
// applying the same rule separately is a bug an operator meets as "I set that
// variable and it still says it is missing".
func connectorEnvKey(name string) string { return envname.Key(name) }

//go:embed web
var webFS embed.FS

// Version is the Atlas product version reported to the UI and the CLI. It is
// UI/display metadata only and unrelated to a deployment's process version.
//
// It is a var, not a const, so a release build can stamp the tag into it with
//
//	go build -ldflags "-X github.com/pblumer/atlas/api.Version=0.4.0"
//
// A plain checkout build keeps the "-dev" suffix; the exact commit is always
// available from the embedded VCS metadata (see buildInfo).
var Version = "0.4.0-dev"

// deployment is the server-side record of a deployed definition. The compiled
// process itself lives in the processor; here we keep the metadata the UI needs
// plus the original XML so the viewer can render it. DeployedAt is server-side
// display metadata (wall-clock at deploy time), not engine state.
type deployment struct {
	Key        uint64
	ProcessID  string
	Name       string // human-readable <process name="…">, for display
	Version    int32
	DeployedAt int64  // unix seconds, for the UI's "last changed" column
	ProjectID  string // project the deployment's draft belonged to, "" when none
	DeployedBy string // account that deployed it, "" when unknown (ADR-0205)
	xml        []byte
	cp         *compiler.CompiledProcess // for the live overlay's element-id mapping
	// inactive mirrors the persisted deactivation flag (ADR-0119) so the process
	// listing can report it without re-reading the sidecar. The processor holds the
	// authoritative gate; this is the display copy, kept in sync on toggle and load.
	inactive bool
}

// Server hosts the engine behind an HTTP surface. Construct it with New, mount
// Handler on an http.Server, and call Close to stop the run loop.
type Server struct {
	proc  *engine.Processor
	store *state.Store

	// dataDir is the root under which every durable store lives (the WAL, the
	// state store, and the design-time sidecar directories). The backup/restore
	// endpoints (ADR-0107) read and write the design-time subtree of it; nothing
	// else needs it, so it is set once at construction and read-only thereafter.
	dataDir string

	// runLoop is the single-writer goroutine that owns the processor, the store
	// and the registries; every handler reaches them through it (I3, ADR-0002).
	// quit stops it, and every background sweeper alongside it.
	runLoop *runloop.Loop
	quit    chan struct{}
	wg      sync.WaitGroup

	// The fields below are touched only on the run-loop goroutine (via do), so
	// they need no locking — the same single-owner discipline as process state.
	deployments map[uint64]*deployment
	order       []uint64 // deployment keys in registration order, for stable listing
	nextKey     uint64
	versions    map[string]int32 // bpmnProcessId → highest version deployed
	deploys     *deployStore     // durable sidecar for deployments (ADR-0019)
	// jobTypes is the engine-wide job-type table (ADR-0007/0157). Compiled processes
	// are resolved through it at deploy and on reload so a job type index means the
	// same thing in every definition; it also turns an index on a job back into a name.
	jobTypes *jobtype.Registry
	// workers is the Workers view's runtime registry (ADR-0157): who pulled what,
	// this run. Not durable and not engine state — see api/workers.go.
	workers *workerRegistry
	// jobWaiters parks long-polling workers until a job of their type is durable
	// (ADR-0157). Guarded by its own mutex, deliberately NOT run-loop owned: the
	// waiting happens off the loop — see api/jobwait.go.
	jobWaiters *jobWaiters
	// driveMu serializes job driving, so two callers never claim the same job and
	// work it twice. See [Server.drive].
	driveMu sync.Mutex
	// supervisor runs the worker processes Atlas launches itself (ADR-0157 step 7).
	// nil unless the operator asked for them on the command line — never from a
	// request, which is what keeps this from being a spawn endpoint.
	supervisor *supervisor
	// SuperviseSpecs, superviseHandles and superviseURL are what to run, read off
	// the server's own command line and held in the same order.
	SuperviseSpecs   []SuperviseSpec
	superviseHandles [][]string
	superviseURL     string
	// offloadedKinds are the managed connector kinds this server does not serve
	// itself; their jobs park for an external worker. See [WithOffloadedConnectorKinds].
	offloadedKinds []string
	drafts         *draftStore // durable sidecar for saved-but-not-deployed diagrams
	// targetClient calls another Atlas — a deployment target (ADR-0129). It is set
	// only where the operator named certificate authorities of their own with
	// --tls-ca; nil means the default client, verifying against the host's roots
	// (ADR-0191).
	targetClient     *http.Client
	forms            *formStore        // durable sidecar for form definitions (ADR-0028)
	publicLinks      *publicLinkStore  // durable sidecar for public start links (ADR-0029)
	publicRate       *rateLimiter      // throttles the unauthenticated public endpoints
	registerRate     *rateLimiter      // throttles OAuth self-registration, on its own budget
	logins           *loginGuard       // throttles authentication attempts (see loginguard.go)
	projects         *projectStore     // durable sidecar for projects grouping artifacts (ADR-0034)
	releases         *releaseStore     // durable sidecar for application releases (ADR-0128)
	grantAudit       *grantAuditStore  // durable sidecar for access-control history (ADR-0186)
	deployTokenStore *deployTokenStore // durable sidecar for peer deploy tokens (ADR-0129)
	deployTokens     *deployTokenIndex // in-memory hash->token index, read on the handler goroutine
	apiTokenStore    *apiTokenStore    // durable sidecar for machine credentials (ADR-0194)
	apiTokens        *apiTokenIndex    // in-memory hash->token index, same discipline as the deploy one
	targets          *targetStore      // durable sidecar for peer deployment targets (ADR-0129)
	appVersions      map[string]int32  // applicationId → highest release version published (ADR-0128)
	// processDocs is the documentation area as a self-contained service: it owns
	// its store and version counters and reaches shared state only through the run
	// loop it was given (ADR-0143/0147).
	processDocs *processdoc.Service
	// panorama is the application-owned ArchiMate model library (ADR-0189),
	// isolated as a per-area service under ADR-0147.
	panorama *panorama.Service
	// panoramaMesh is Panorama's derived landscape altitude (ADR-0211): a graph
	// computed from this server's own resources, never stored. Separate from the
	// model library above because declared intent and derived fact must not share
	// a surface.
	panoramaMesh     *panorama.Mesh
	systemPIDs       map[string]bool     // process ids of the bootstrap-deployed platform processes, protected from deletion (ADR-0122)
	deploySysProcs   bool                // opt-in: bootstrap-deploy the embedded platform processes at startup (ADR-0122)
	userProvisioning bool                // opt-in: enable the user-provisioning connector for system processes (ADR-0123)
	dmnrefs          *dmnRefStore        // durable sidecar for DMN reference artifacts (ADR-0034)
	connectors       *connectorStore     // durable sidecar for managed connector instances (ADR-0041)
	callOverrides    *callOverrideStore  // durable sidecar for per-server call-activity target overrides (ADR-0105)
	repository       []repositoryPackage // curated, bundled repository catalog, immutable after New (ADR-0081)
	repositoryStore  *repositoryStore    // durable sidecar for installed repository templates (ADR-0081)
	settings         *settingsStore      // durable sidecar for org-wide UI settings, e.g. the brand theme (ADR-0113)
	vault            *vault.Vault        // engine-internal encrypted secret store, nil when disabled (ADR-0069/0070)
	vaultEnabled     bool                // whether to build the vault; on by default, off via WithoutVault (ADR-0070)
	users            *userStore          // durable sidecar for user accounts (ADR-0044)
	groups           *groupStore         // durable sidecar for user groups (ADR-0180)

	// sessions holds live login sessions in memory. Unlike the sidecar stores it
	// is touched from concurrent handler goroutines, so it guards itself with a
	// mutex; it is not engine state and does not persist (ADR-0044).
	sessions *sessionStore

	// collab holds live collaborative-editing sessions on drafts in memory
	// (ADR-0140). Like sessions it is reached from concurrent handler goroutines
	// (SSE streams and POSTs), guards itself with a mutex, and never persists or
	// touches the engine — it is design-time coordination around a draft, not
	// engine state.
	collab *collab.Registry

	// collabKeepalive is how often an idle SSE session stream writes a keepalive
	// comment. net/http only learns a client vanished when a write fails, so these
	// periodic writes are what detect a half-open (zombie) browser connection and
	// let its participant be reaped; they also keep proxies from timing out an idle
	// stream. WithCollabKeepaliveInterval overrides it (tests use a short value).
	collabKeepalive time.Duration

	// authEnabled gates authentication enforcement. Off by default: the server
	// stays fully open (single-user) until an operator opts in with WithAuth,
	// mirroring how docsEnabled gates the API explorer (ADR-0044/0043). Set once
	// before Handler is mounted; read-only thereafter.
	authEnabled bool

	// internalToken is a random secret minted at startup when auth is enabled, so a
	// process this server started itself can call the API without a login
	// (ADR-0049). It resolves to a non-admin service principal. Empty when auth is
	// off.
	//
	// Today that means the supervised workers, handed it at spawn (see
	// workerTokenEnv). The MCP adapter used to hold it too and no longer does: it
	// forwards its caller's credential instead, which is what stopped /mcp being a
	// way to act as this principal without presenting anything
	// (ADR-0196).
	internalToken string

	// dmnResolver turns a DMN reference handle into model XML; dmnValidator wraps
	// it with a temis compile for the deploy-time validation gate (ADR-0034).
	dmnResolver  dmn.Resolver
	dmnValidator *dmn.Validator

	// dmnRegistry holds the compiled DMN model for each deployed process (keyed by
	// def key); jobRunner drives the in-process DMN worker that evaluates business
	// rule tasks (ADR-0014). Both are touched only on the run-loop goroutine.
	dmnRegistry *dmn.Registry
	jobRunner   *job.Runner
	// ldapPool reuses bound LDAP connections across jobs (ADR-0154, amended). It is
	// held here so Close can release what it is holding at shutdown.
	ldapPool *ldap.Pool

	// scriptWorkers maps a script language's reserved job-type index to the
	// interpreter that runs it (ADR-0047). An operator registers each language with
	// WithScriptWorker; a language with no worker parks its tasks. Set once before
	// Handler is mounted; read-only thereafter.
	scriptWorkers map[int32]script.Exec

	// temisRegistry resolves a connector name to a temis service client for
	// *central* business rule tasks (ADR-0050), built from the environment at
	// startup (ADR-0041 secret model). Read only while driving jobs on the run loop.
	temisRegistry *temis.Registry

	// clioRegistry resolves a connector name to a clio event-store client for clio
	// connector tasks (write/query/read, ADR-0036), built from the managed
	// connector store at startup and rebuilt on every connector change, with each
	// endpoint token resolved from the vault (ADR-0041). Read only while driving
	// jobs on the run loop, so it needs no lock.
	clioRegistry *clio.Registry

	// history appends settled job runs to a clio connector, when an operator named
	// one (see workerhistory.go). nil means they did not, and the console's ring is
	// the whole of what this server remembers.
	history *historyExporter
	// mailRegistry resolves a connector name to a mail-provider client for outbound
	// mail connector tasks (ADR-0079), built from the managed connector store at
	// startup and rebuilt on every connector change, with each provider's credential
	// resolved from the vault (ADR-0041). Read only while driving jobs on the run
	// loop, so it needs no lock.
	mailRegistry *mail.Registry

	// mailOutbox is where the preview mail provider delivers (ADR-0150) — the
	// zero-configuration provider a first mail task uses before anyone has a
	// submission host or an OAuth bundle. It is created once and outlives every
	// registry rebuild, holds its own lock (a mail worker writes it off the run loop
	// while an HTTP read serves the Outbox view), and is deliberately not durable:
	// nothing here was ever sent, so nothing survives a restart.
	mailOutbox *mail.Outbox

	// sharePointRegistry resolves a connector name to the Microsoft Graph client for
	// SharePoint connector tasks (ADR-0141), built from the managed connector store at
	// startup and rebuilt on every connector change, with each connector's OAuth
	// credential resolved from the vault (ADR-0041). Read only while driving jobs on
	// the run loop, so it needs no lock.
	sharePointRegistry *sharepoint.Registry

	// remedyRegistry resolves a connector name to a BMC Remedy AR System client for
	// Remedy connector tasks (ADR-0106), built from the managed connector store at
	// startup and rebuilt on every connector change, with each instance's credential
	// bundle resolved from the vault (ADR-0041). Read only while driving jobs on the
	// run loop, so it needs no lock.
	remedyRegistry *remedy.Registry

	// jiraRegistry resolves a connector name to an Atlassian Jira REST client for Jira
	// connector tasks (ADR-0201), built from the managed connector
	// store at startup and rebuilt on every connector change, with each instance's
	// credential bundle resolved from the vault (ADR-0041). Read only while driving
	// jobs on the run loop, so it needs no lock.
	jiraRegistry *jira.Registry

	// inboundSubs holds the operator-configured clio inbound subscriptions the
	// inbound bridge polls (ADR-0075). Owned by the run-loop goroutine. inboundPoll
	// is that bridge's poll cadence (WithInboundPollInterval; 0 disables the bridge).
	inboundSubs *inboundSubStore
	inboundPoll time.Duration
	// inboundBatch caps how many clio events one poll of a subscription reads and
	// republishes (the ReadEvents Limit). It bounds the burst a single poll can hand
	// the run loop so a large backlog drains as bounded catch-up across ticks rather
	// than monopolizing the single writer with one unbounded publish storm (ADR-0075).
	inboundBatch int

	// osExportCfg is the OpenSearch event-exporter configuration (ADR-0114); it is
	// enabled only when a URL is set (WithOpenSearchExporter). exporter is the built
	// WAL-tailing sink, nil when disabled, and exporterPoll is its poll cadence. The
	// exporter runs on its own goroutine off the run loop — it only reads the durable
	// WAL files and the state store's applied-position watermark, never the processor
	// — so it never touches the single-writer invariant (I3).
	osExportCfg  opensearch.Config
	exporter     *opensearch.Exporter
	exporterPoll time.Duration
	// exporterTicks, when non-nil, replaces the exporter loop's real ticker so a test
	// drives each export pass explicitly rather than racing a wall-clock cadence.
	// exporterTicked, when non-nil, receives once after each triggered pass completes,
	// so a test awaits the pass it triggered without polling. Both are nil in
	// production (a real ticker drives the loop, nothing observes it). Set together by
	// withExporterTrigger — the same deterministic-test seam as the retention sweep.
	exporterTicks  <-chan time.Time
	exporterTicked chan struct{}

	// Retention (ADR-0115): hard-delete finished-instance history older than
	// retentionMaxAge, gated on the safe (exported, else durable) position so nothing
	// is deleted before it is archived. retentionMaxAge is the server-wide default
	// (WithRetention); a definition declaring atlas:historyTtl overrides it for its own
	// instances and enables retention on its own (ADR-0144), so a zero here is not
	// "retention off" but "off for everything that declares nothing".
	// The sweep is bounded (retentionBatch per tick) and resumable (retentionCursor);
	// all three are touched only on the run-loop goroutine (via do), so no lock.
	retentionMaxAge   time.Duration
	retentionInterval time.Duration
	retentionBatch    int
	retentionCursor   uint64

	// now reads wall-clock time (unix nanoseconds) for the retention sweep's
	// eligibility cutoff. It is injected so a test can drive the cutoff
	// deterministically instead of tuning durations against real time — the same
	// discipline the engine applies to event timestamps (invariant I4). It defaults
	// to the system clock; a test overrides it with withClock, sharing one clock
	// with the engine so a finished instance's CompletedAt and the sweep's "now"
	// come from a single controllable source.
	now func() int64
	// retentionTicks, when non-nil, replaces the retention sweep's real ticker so a
	// test triggers each sweep explicitly rather than racing a wall-clock cadence.
	// retentionSwept, when non-nil, receives once after each triggered sweep
	// completes, so a test awaits the sweep it triggered without polling. Both are
	// nil in production (a real ticker drives the sweep, nothing observes it). Set
	// together by withRetentionTrigger.
	retentionTicks <-chan time.Time
	retentionSwept chan struct{}

	// Recovery checkpoints (ADR-0131): on a fixed cadence, snapshot the applied state
	// so a restart replays only the WAL suffix past it instead of the whole log — the
	// bounded-recovery-time property. Off unless checkpointInterval > 0
	// (WithCheckpoints); checkpointKeep bounds how many published checkpoints survive,
	// since each one hard-links the SSTables it captured and so pins their disk.
	// checkpointRoot is derived from the data dir through checkpoint.Dir, the same
	// function startup recovery resolves, so the two can never disagree.
	//
	// The snapshot itself runs on the run loop (via do), which is what makes its
	// position exact: no batch can be half-applied while it is taken (invariant I3).
	// Pruning runs off the loop — it only removes directories this goroutine published.
	checkpointRoot     string
	checkpointInterval time.Duration
	checkpointKeep     int
	// checkpointTicks, when non-nil, replaces the checkpoint loop's real ticker so a
	// test takes each checkpoint explicitly rather than racing a wall-clock cadence.
	// checkpointDone, when non-nil, receives once after each triggered pass completes.
	// Both are nil in production. Set together by withCheckpointTrigger — the same
	// deterministic-test seam as the retention sweep and the exporter.
	checkpointTicks <-chan time.Time
	checkpointDone  chan struct{}

	// compactWAL enables deleting the WAL segments a checkpoint and every consumer
	// watermark make redundant (ADR-0131), on the same tick that takes the checkpoint —
	// a fresh checkpoint is what licenses new deletion, so there is nothing to schedule
	// separately. Opt-in (WithWALCompaction), like history retention (ADR-0115) and for
	// the same reason: it is the one irreversible operation here.
	compactWAL bool
	// checkpointRequests carries on-demand pass requests from a handler to the checkpoint
	// goroutine, each with its own reply channel (ADR-0131). Running them *on that
	// goroutine* is the point: an operator's pass and a scheduled one then serialize by
	// construction rather than by a lock, and both run the same code. It is nil when
	// checkpointing is off, which is what makes the endpoint's refusal honest.
	checkpointRequests chan chan checkpointPass
	// lastPass is the most recent pass's outcome, for the status endpoint. Written by the
	// checkpoint goroutine, read by handler goroutines, hence the mutex.
	lastPassMu sync.Mutex
	lastPass   *checkpointPass

	// backupsInFlight counts whole-instance snapshots currently streaming (ADR-0109).
	// A snapshot reads the WAL files off the run loop, so a compaction underneath one
	// could delete a segment the archive still needs; a pass that sees a non-zero count
	// skips and retries on the next tick.
	//
	// The ordering is what makes a zero reading safe rather than merely likely: a backup
	// increments *before* it picks the checkpoint it will carry, so a pass that reads
	// zero is one whose deletion the backup's later choice of checkpoint already accounts
	// for — its suffix starts at or above the cut. Atomic because the two run on
	// different goroutines.
	backupsInFlight atomic.Int64

	// metricsEnabled gates the Prometheus exposition at /metrics, and metrics is the
	// registry it serves (ADR-0142). On by default (opt-out, like the API docs): the
	// exposition carries only bounded-cardinality aggregates, so the cost of having it
	// is a port an operator may not want open rather than data leaking. Built once at
	// construction — a duplicate registration then fails the boot, not a scrape.
	metricsEnabled bool
	metrics        *metrics.Registry

	// traceRoutes wraps every /api/v1 route in a server span (ADR-0142). Opt-in and off
	// by default: with no collector configured there is nothing to send spans to, and an
	// operator who has not asked for tracing should not pay a wrapper per request.
	// Probes, the metrics scrape and the static UI are never traced — they run forever
	// on a timer and would bury the traces someone is actually looking for.
	traceRoutes bool

	// readyTimeout bounds how long GET /readyz waits for the run-loop goroutine to
	// answer before it reports the writer as unresponsive (ADR-0142). It exists so a
	// wedged writer fails the probe instead of hanging it; withReadyTimeout shortens it
	// for tests.
	readyTimeout time.Duration

	// docsEnabled gates the OpenAPI spec and the Scalar API explorer. On by
	// default (opt-out), consistent with the already-open web UI and MCP
	// endpoint; an operator who does not want the interactive surface disables
	// it with --docs=false / WithoutDocs (ADR-0043). Set once before Handler is
	// mounted; read-only thereafter.
	docsEnabled bool

	// logs is the recent-process-log tail exposed at GET /api/v1/logs, so an
	// operator can read server logs from the web UI without shell access. Nil when
	// the command did not wire a buffer (WithLogBuffer), in which case the endpoint
	// reports no lines. Set once at construction; read-only thereafter.
	logs *LogBuffer

	// mcpHandler is the Model Context Protocol transport, when the binary built one
	// (WithMCP). Handler mounts it at /mcp inside its own mux so it passes the
	// access boundary like every other route; it used to be mounted beside this
	// server, where withAuth never saw it and anything that could reach the port
	// drove the whole API (ADR-0196). Nil leaves /mcp
	// unmounted. Set once before Handler is mounted; read-only thereafter.
	mcpHandler http.Handler

	// The OAuth authorization server (ADR-0200): the clients an operator registered,
	// the grants people approved for them, and the short-lived codes in flight
	// between an approval and its token exchange. Each durable store is owned by the
	// run loop; each index is the concurrent half the handlers read. The codes are
	// in memory only — a code is worthless a minute after it is issued.
	oauthClientStore *oauthClientStore
	oauthClients     *oauthClientIndex
	oauthGrantStore  *oauthGrantStore
	oauthGrants      *oauthGrantIndex
	oauthCodes       *oauthCodeStore

	// dynamicRegistration opens RFC 7591 self-registration
	// (WithDynamicClientRegistration). Off by default: it is the one unauthenticated
	// endpoint that writes durable state, and oauthregister.go is where that is
	// argued. Read at mount time and by the metadata document, so the route and what
	// advertises it cannot disagree. Set once before Handler is mounted; read-only
	// thereafter.
	dynamicRegistration bool

	// externalURL is the origin this server is reachable under from outside, when an
	// operator stated one (WithExternalURL). It is what the RFC 9728 discovery
	// documents and the WWW-Authenticate challenge build their absolute URLs from;
	// empty means derive them from the request (ADR-0200).
	externalURL string

	// oidc is the identity provider people may sign in with, when an operator
	// configured one (WithOIDC, ADR-0210). Nil is the
	// default and means the local password is the only way in: the routes are not
	// mounted, and nothing is ever fetched from anybody. Set once before Handler is
	// mounted; read-only thereafter, and the provider guards its own caches.
	oidc *oidcProvider

	// oidcStates holds the federated logins in flight — the state, nonce and PKCE
	// verifier a callback needs to finish one. Handler-goroutine state like the
	// sessions and the authorization codes, so it guards itself and is never
	// persisted (oidclogin.go).
	oidcStates *oidcStateStore

	// publicCORSOrigins is the allow-list of web origins permitted to call the
	// unauthenticated /public/forms endpoints cross-origin, so a start form can be
	// embedded in an external site (ADR-0186). Empty is the closed default — no
	// cross-origin access, exactly as before the field existed; the sentinel "*"
	// allows any origin. It opens *only* the cookieless public surface, never
	// /api/v1. Set once before Handler is mounted; read-only thereafter.
	publicCORSOrigins []string
}

// Option configures a Server at construction. Options are applied in New before
// the run loop starts, so they set fields that are read-only afterwards.
type Option func(*Server)

// WithLogBuffer wires the server's recent-log tail, exposed at GET /api/v1/logs,
// so an operator can read server logs from the web UI. The command builds the
// buffer, tees the standard logger into it, and passes it here.
func WithLogBuffer(b *LogBuffer) Option { return func(s *Server) { s.logs = b } }

// WithoutDocs disables the OpenAPI document at /api/v1/openapi.json and the
// Scalar API explorer at /api/docs, which are otherwise served by default. Pass
// it when the interactive, mutating "Try it out" surface should not be exposed
// (ADR-0043).
func WithoutDocs() Option { return func(s *Server) { s.docsEnabled = false } }

// WithMCP mounts an MCP transport at /mcp, behind this server's own access
// boundary. The handler is taken as an http.Handler rather than a concrete type
// so the dependency runs one way: the mcp package adapts this API, and this
// package need not know it exists.
//
// Mounting it here is the whole point. The transport used to be registered on a
// mux beside this one, which meant withAuth never resolved a principal for it and
// --auth did not gate it — while the adapter attached a service credential of its
// own to the calls it made, so reaching the port was enough to drive the API.
// Passing the handler in makes that a decision this package owns rather than one
// the wiring in cmd can make differently (ADR-0196).
func WithMCP(h http.Handler) Option { return func(s *Server) { s.mcpHandler = h } }

// mcpTransport is the MCP handler with one thing added: every request entering it
// is stamped with mcpTransportHeader, which the adapter forwards on the API calls
// its tools make. That is what lets the boundary tell a tool call apart from a
// client driving /api/v1 with a token approved only for the transport — the two
// carry the same credential and are otherwise the same request.
//
// Stamping rather than trusting: whatever the caller sent under that name is
// replaced, so the marker is only ever a value this server put there. With
// authentication off there is no internal token and the header is removed instead,
// which keeps "no secret" from reading as "matched".
//
// The header goes on a clone, so the request the mux handed us is not mutated —
// nothing downstream of this handler expects its headers to have been rewritten.
func (s *Server) mcpTransport() http.Handler {
	h := s.mcpHandler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		if s.internalToken == "" {
			r.Header.Del(mcpTransportHeader)
		} else {
			r.Header.Set(mcpTransportHeader, s.internalToken)
		}
		h.ServeHTTP(w, r)
	})
}

// WithPublicFormsCORS allows the given web origins to call the unauthenticated
// /public/forms endpoints cross-origin, so a process's start form can be embedded
// in an external site (a custom order widget, say) rather than only iframed from
// Atlas's own page (ADR-0186). Off by default — with no origins the public
// endpoints send no CORS headers and a cross-origin fetch is blocked by the
// browser, exactly as before. The sentinel "*" allows any origin. It opens only the
// cookieless public surface; the authenticated /api/v1 surface is never
// CORS-enabled, and no Access-Control-Allow-Credentials is ever sent, so a
// permissive origin still reaches only what a visitor to the public link can.
func WithPublicFormsCORS(origins []string) Option {
	return func(s *Server) {
		trimmed := make([]string, 0, len(origins))
		for _, o := range origins {
			if o = strings.TrimSpace(o); o != "" {
				trimmed = append(trimmed, o)
			}
		}
		s.publicCORSOrigins = trimmed
	}
}

// WithSystemProcesses bootstrap-deploys Atlas's own embedded platform processes
// (user intake, access review, offboarding) into the protected system project at
// startup (ADR-0122). Opt-in — the installed binary (cmd/atlas) enables it, while
// the engine tests construct servers without it so a fresh instance still starts
// with no deployments.
func WithSystemProcesses() Option { return func(s *Server) { s.deploySysProcs = true } }

// WithUserProvisioning enables the in-process user-provisioning connector
// (create/set-password/disable Atlas logins) for the protected system project's
// processes (ADR-0123). Opt-in and off by default: it deliberately, and narrowly,
// reopens the ADR-0044/0049 boundary that no automated identity may manage users —
// so an instance keeps the human-in-the-loop ADR-0122 behavior until an operator
// turns this on. When off, a userConnector job has no worker and parks.
func WithUserProvisioning() Option { return func(s *Server) { s.userProvisioning = true } }

// WithInboundPollInterval sets the clio inbound bridge's poll cadence (ADR-0075).
// A non-positive interval disables the bridge (useful in tests that drive it
// directly). The default is 2s.
func WithInboundPollInterval(d time.Duration) Option {
	return func(s *Server) { s.inboundPoll = d }
}

// WithCollabKeepaliveInterval sets how often an idle collaboration SSE stream
// writes a keepalive comment, the mechanism that detects a half-open browser
// connection so its session participant is reaped (ADR-0140). A non-positive
// value restores the default (15s). Tests pass a short interval to exercise it.
func WithCollabKeepaliveInterval(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.collabKeepalive = d
		}
	}
}

// WithInboundBatchLimit caps how many clio events one poll of a subscription reads
// and republishes (ADR-0075). A non-positive value restores the default. It bounds
// the burst a single poll hands the run loop; a large backlog then drains as
// bounded catch-up across ticks instead of one unbounded publish storm.
func WithInboundBatchLimit(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.inboundBatch = n
		}
	}
}

// WithoutVault disables the engine-internal encrypted secret vault, which is
// otherwise on by default (ADR-0070). With it disabled the secret endpoints
// return 503 and connector credentials resolve only from the environment
// (ADR-0041 A2). Pass it when Atlas must not custody a key or ciphertext at all.
func WithoutVault() Option { return func(s *Server) { s.vaultEnabled = false } }

// WithScriptWorker registers the interpreter for one script language (identified
// by its reserved job-type index, e.g. compiler.PwshJobTypeIndex), so a deployed
// script task in that language actually runs instead of parking on its job
// (ADR-0047). It executes arbitrary interpreter code on the server host, so
// register only the languages whose trust boundary is acceptable (the eventual
// isolation boundary is an external worker in the customer's environment). exec is
// the interpreter seam — pass script.New(script.Python) etc., or a fake in tests.
func WithScriptWorker(jobType int32, exec script.Exec) Option {
	return func(s *Server) {
		if s.scriptWorkers == nil {
			s.scriptWorkers = map[int32]script.Exec{}
		}
		s.scriptWorkers[jobType] = exec
	}
}

// WithOpenSearchExporter enables the OpenSearch event exporter (ADR-0114): a
// WAL-tailing sink that mirrors the durable event log into an OpenSearch index so
// history stays searchable and can outlive engine-side retention. It is opt-in —
// a config with an empty URL leaves the exporter off. The endpoint, credentials,
// and index live in server config, never in a model.
func WithOpenSearchExporter(cfg opensearch.Config) Option {
	return func(s *Server) { s.osExportCfg = cfg }
}

// WithOpenSearchExportInterval sets how often the exporter polls the log for newly
// durable records (ADR-0114). A non-positive value restores the default (5s).
// Tests pass a short interval to exercise the loop.
func WithOpenSearchExportInterval(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.exporterPoll = d
		}
	}
}

// withExporterTrigger replaces the exporter loop's real ticker with an explicit tick
// channel and, optionally, a completion channel signaled after each triggered export
// pass. It is unexported — a test seam, not an operator knob — so a test drives export
// passes deterministically: send on ticks, receive on ticked, then assert, with no
// wall-clock cadence or polling (ADR-0114). Mirrors withRetentionTrigger.
func withExporterTrigger(ticks <-chan time.Time, ticked chan struct{}) Option {
	return func(s *Server) {
		s.exporterTicks = ticks
		s.exporterTicked = ticked
	}
}

const (
	// DefaultRetentionInterval is the default cadence of the history-retention sweep.
	// Exported so the CLI can show it as the default of --retention-interval rather
	// than restate a number that would drift from this one.
	DefaultRetentionInterval = time.Minute
	// DefaultRetentionBatch bounds how many finished instances one sweep tick evaluates,
	// so the scan never blocks the run loop (ADR-0115 / ADR-0085 no-full-scan rule).
	// Together the two bound the drain rate of a backlog: DefaultRetentionBatch per
	// DefaultRetentionInterval. Exported for the CLI, like DefaultRetentionInterval.
	DefaultRetentionBatch = 1000
)

// WithRetention enables history retention (ADR-0115): a finished instance whose
// terminal event is older than maxAge and whose events are already exported (its
// terminal position is at or below the safe position) is hard-deleted from the state
// store. A non-positive maxAge leaves the server with no default age — the opt-in
// default — and retention then applies only to definitions declaring their own
// atlas:historyTtl (ADR-0144).
func WithRetention(maxAge time.Duration) Option {
	return func(s *Server) {
		if maxAge > 0 {
			s.retentionMaxAge = maxAge
		}
	}
}

// WithRetentionInterval sets the retention sweep cadence (ADR-0115). A non-positive
// value restores the default. Tests pass a short interval to exercise the sweep.
func WithRetentionInterval(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.retentionInterval = d
		}
	}
}

// WithRetentionBatch caps how many finished instances one retention sweep tick
// evaluates and purges (ADR-0115), bounding the work a single tick does on the run
// loop; a larger backlog then drains as bounded catch-up across ticks. A non-positive
// value restores the default.
func WithRetentionBatch(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.retentionBatch = n
		}
	}
}

// withClock overrides the server clock the retention sweep reads for its eligibility
// cutoff (unix nanoseconds). It is unexported — a test seam, not an operator knob — so
// a test can share one deterministic clock with the engine and make a finished
// instance's age exact rather than tuned against real time (invariant I4).
func withClock(now func() int64) Option {
	return func(s *Server) {
		if now != nil {
			s.now = now
		}
	}
}

// withRetentionTrigger replaces the retention sweep's real ticker with an explicit
// tick channel and, optionally, a completion channel signaled after each triggered
// sweep. It is unexported — a test seam — so a test drives sweeps deterministically:
// send on ticks, receive on swept, then assert, with no wall-clock cadence or polling.
func withRetentionTrigger(ticks <-chan time.Time, swept chan struct{}) Option {
	return func(s *Server) {
		s.retentionTicks = ticks
		s.retentionSwept = swept
	}
}

const (
	// checkpointKeepDefault is how many published checkpoints survive a prune. More
	// than one so a checkpoint found to be corrupt at restore time still has an older
	// one behind it; few enough that the SSTables they pin stay bounded (ADR-0131).
	checkpointKeepDefault = 3
)

// WithCheckpoints enables periodic recovery checkpoints at the given cadence
// (ADR-0131). Each one snapshots the applied state, so a restart replays only the log
// past it rather than from genesis — recovery time becomes a function of the cadence
// instead of the log's whole length.
//
// It is purely additive: nothing is deleted, the WAL stays the source of truth, and a
// missing, failed, or corrupt checkpoint only makes the next recovery slower (invariant
// I2). A non-positive cadence leaves checkpointing off.
func WithCheckpoints(every time.Duration) Option {
	return func(s *Server) {
		if every > 0 {
			s.checkpointInterval = every
		}
	}
}

// WithCheckpointRetention sets how many published checkpoints are kept (ADR-0131). A
// checkpoint hard-links the SSTables it captured, so keeping every one would pin every
// file the store ever wrote; keeping a few leaves a fallback if the newest is corrupt.
// A non-positive value restores the default.
func WithCheckpointRetention(keep int) Option {
	return func(s *Server) {
		if keep > 0 {
			s.checkpointKeep = keep
		}
	}
}

// WithoutMetrics turns off the Prometheus exposition at /metrics (ADR-0142). It is
// served by default — a system meant to be operated should be observable without extra
// configuration, and the exposition carries only bounded-cardinality aggregates — so
// this is for an operator who does not want the surface open at all.
func WithoutMetrics() Option {
	return func(s *Server) { s.metricsEnabled = false }
}

// WithTracing wraps every /api/v1 route in an OpenTelemetry server span (ADR-0142). The
// command passes it when a collector endpoint is configured; without it the routes are
// registered bare, so tracing costs exactly nothing when nobody asked for it.
//
// The engine is deliberately not traced — see the tracing package for why, and for the
// test that keeps it that way.
func WithTracing() Option {
	return func(s *Server) { s.traceRoutes = true }
}

// WithWALCompaction deletes the WAL segments a recovery checkpoint and every consumer
// watermark make redundant (ADR-0131), bounding the log's disk instead of letting it
// grow with all history. It has no effect without WithCheckpoints: the cut is derived
// from a checkpoint, and compaction runs on the tick that takes one.
//
// It is opt-in, unlike checkpointing, for the same reason history retention is
// (ADR-0115): this is the one step here that destroys data. The cut itself is
// conservative — the newest **fully verified** checkpoint (manifest and state files) at
// or below the store, floored by every consumer watermark, with a corrupt, foreign, or
// ahead-of-store checkpoint licensing no deletion at all — but a conservative cut is
// still a deletion, and an operator should choose it.
func WithWALCompaction() Option {
	return func(s *Server) { s.compactWAL = true }
}

// withCheckpointTrigger replaces the checkpoint loop's real ticker with an explicit
// tick channel and, optionally, a completion channel signaled after each triggered
// pass. It is unexported — a test seam, not an operator knob — so a test takes
// checkpoints deterministically: send on ticks, receive on done, then assert, with no
// wall-clock cadence or polling. Mirrors withRetentionTrigger.
func withCheckpointTrigger(ticks <-chan time.Time, done chan struct{}) Option {
	return func(s *Server) {
		s.checkpointTicks = ticks
		s.checkpointDone = done
	}
}

// withReadyTimeout shortens the deadline GET /readyz gives the run loop. Like
// withCheckpointTrigger it is a test seam rather than an operator knob: a test that
// wedges the run loop should not have to wait out the production deadline to see the
// probe give up.
func withReadyTimeout(d time.Duration) Option {
	return func(s *Server) { s.readyTimeout = d }
}

// New builds a Server over an already-recovered processor and its store and
// starts the run-loop goroutine. dataDir is the base data directory; the durable
// deployment and draft sidecar stores live in its "deployments" and "drafts"
// subdirectories (ADR-0019). New reloads any deployments found there,
// re-registering them with the processor so recovered instances resolve their
// definition and the UI can render diagrams again. The caller retains ownership
// of proc and store (Close here stops only the loop, not the engine).
func New(proc *engine.Processor, store *state.Store, dataDir string, opts ...Option) (*Server, error) {
	ds, err := newDeployStore(filepath.Join(dataDir, "deployments"))
	if err != nil {
		return nil, err
	}
	// The engine-wide job-type table. Every compiled process is resolved through it
	// before it is deployed, so the job type a service task's job carries means the
	// same thing across definitions (ADR-0007/0157).
	jobTypes, err := jobtype.NewRegistry(filepath.Join(dataDir, "jobtypes"))
	if err != nil {
		return nil, err
	}
	// A store written before the reserved range reached these indices holds
	// model-authored types on them. The table cannot keep such a record — the index
	// belongs to a built-in now — but dropping it in silence is what would let jobs
	// already on disk be read as the built-in's. So it is said out loud, once, with
	// the name and what its index means now. The server still starts: the repair is
	// not decided yet, and taking an instance down over it would be the larger harm.
	for _, c := range jobTypes.Dropped() {
		logging.Warn(logging.JobTypeIndexCollision,
			"a stored job type sits on an index a built-in has since taken; its parked jobs would be read as that built-in's",
			slog.String("jobType", c.Name), slog.Int("index", int(c.Index)),
			slog.String("indexNowMeans", c.NowMeans))
	}
	drafts, err := newDraftStore(filepath.Join(dataDir, "drafts"))
	if err != nil {
		return nil, err
	}
	forms, err := newFormStore(filepath.Join(dataDir, "forms"))
	if err != nil {
		return nil, err
	}
	publicLinks, err := newPublicLinkStore(filepath.Join(dataDir, "public-links"))
	if err != nil {
		return nil, err
	}
	projects, err := newProjectStore(filepath.Join(dataDir, "projects"))
	if err != nil {
		return nil, err
	}
	processDocStore, err := processdoc.NewStore(filepath.Join(dataDir, "process-docs"))
	if err != nil {
		return nil, err
	}
	panoramaStore, err := panorama.NewStore(filepath.Join(dataDir, "panorama-models"))
	if err != nil {
		return nil, err
	}
	releases, err := newReleaseStore(filepath.Join(dataDir, "releases"))
	if err != nil {
		return nil, err
	}
	grantAudit, err := newGrantAuditStore(filepath.Join(dataDir, "grant-audit"))
	if err != nil {
		return nil, err
	}
	apiTokenStore, err := newAPITokenStore(filepath.Join(dataDir, "api-tokens"))
	if err != nil {
		return nil, err
	}
	deployTokenStore, err := newDeployTokenStore(filepath.Join(dataDir, "deploy-tokens"))
	if err != nil {
		return nil, err
	}
	oauthClientStore, err := newOAuthClientStore(filepath.Join(dataDir, "oauth-clients"))
	if err != nil {
		return nil, err
	}
	oauthGrantStore, err := newOAuthGrantStore(filepath.Join(dataDir, "oauth-grants"))
	if err != nil {
		return nil, err
	}
	targets, err := newTargetStore(filepath.Join(dataDir, "targets"))
	if err != nil {
		return nil, err
	}
	dmnrefs, err := newDmnRefStore(filepath.Join(dataDir, "dmnrefs"))
	if err != nil {
		return nil, err
	}
	users, err := newUserStore(filepath.Join(dataDir, "users"))
	if err != nil {
		return nil, err
	}
	groups, err := newGroupStore(filepath.Join(dataDir, "groups"))
	if err != nil {
		return nil, err
	}
	connectors, err := newConnectorStore(filepath.Join(dataDir, "connectors"))
	if err != nil {
		return nil, err
	}
	callOverrides, err := newCallOverrideStore(filepath.Join(dataDir, "call-overrides"))
	if err != nil {
		return nil, err
	}
	// Carry a pre-rename install across before opening the store, so templates
	// installed when the area was still called "Marketplace" are still there.
	if err := migrateRepositoryDir(dataDir); err != nil {
		return nil, err
	}
	repositoryStore, err := newRepositoryStore(filepath.Join(dataDir, "repository"))
	if err != nil {
		return nil, err
	}
	// The repository catalog is curated and compiled into the binary (ADR-0081);
	// an invalid bundled package is a build error, so it fails construction here.
	repositoryCatalog, err := loadBundledCatalog()
	if err != nil {
		return nil, err
	}
	inboundSubs, err := newInboundSubStore(filepath.Join(dataDir, "inbound-subscriptions"))
	if err != nil {
		return nil, err
	}
	settings, err := newSettingsStore(filepath.Join(dataDir, "settings"))
	if err != nil {
		return nil, err
	}
	// The node identity is minted here rather than on the first read of the
	// descriptor, for two reasons: a stable id must not be the side effect of
	// somebody's GET, and a data directory this server cannot write to is an
	// operator's problem to see at startup rather than weeks later on a request
	// (ADR-0189 §6).
	if _, err := ensureNodeIdentity(settings); err != nil {
		return nil, err
	}
	// DMN reference models are resolved either from a temis model service (when
	// configured) or the zero-config <data-dir>/dmn-models folder. Both satisfy the
	// Resolver interface, so the rest of the server is unaffected (ADR-0034/0014).
	resolver := dmnResolverFromEnv(filepath.Join(dataDir, "dmn-models"))
	// quit stops the run loop and every background sweeper alike, so the loop is
	// built from it rather than owning it.
	quit := make(chan struct{})
	s := &Server{
		proc:        proc,
		store:       store,
		dataDir:     dataDir,
		quit:        quit,
		runLoop:     runloop.New(quit),
		deployments: map[uint64]*deployment{},
		nextKey:     1,
		versions:    map[string]int32{},
		deploys:     ds,
		jobTypes:    jobTypes,
		workers:     newWorkerRegistry(nil),
		jobWaiters:  newJobWaiters(),
		drafts:      drafts,
		forms:       forms,
		publicLinks: publicLinks,
		// A public start link tolerates a modest burst then ~1 start/sec per IP;
		// generous for a human intake form, throttling for a script (ADR-0029).
		publicRate: newRateLimiter(20, 1),
		// Registration gets its own budget rather than sharing publicRate: a flood of
		// registrations must not throttle the token exchanges of the clients that
		// already registered, or abuse of the open endpoint becomes an outage for
		// everybody else. Burst enough for an operator wiring up several connectors at
		// once, then one every five seconds — slow enough that churning the bounded
		// registry from outside is not worth anyone's time (ADR-0197, ADR-0200).
		registerRate:      newRateLimiter(30, 0.2),
		logins:            newLoginGuard(),
		projects:          projects,
		releases:          releases,
		grantAudit:        grantAudit,
		deployTokenStore:  deployTokenStore,
		deployTokens:      newDeployTokenIndex(),
		apiTokenStore:     apiTokenStore,
		apiTokens:         newAPITokenIndex(),
		oauthClientStore:  oauthClientStore,
		oauthClients:      newOAuthClientIndex(),
		oauthGrantStore:   oauthGrantStore,
		oauthGrants:       newOAuthGrantIndex(),
		oauthCodes:        newOAuthCodeStore(),
		targets:           targets,
		appVersions:       map[string]int32{},
		dmnrefs:           dmnrefs,
		connectors:        connectors,
		callOverrides:     callOverrides,
		repository:        repositoryCatalog,
		repositoryStore:   repositoryStore,
		inboundSubs:       inboundSubs,
		settings:          settings,
		inboundPoll:       2 * time.Second,          // default cadence; WithInboundPollInterval overrides, 0 disables
		inboundBatch:      defaultInboundBatch,      // per-poll ReadEvents cap; WithInboundBatchLimit overrides
		exporterPoll:      5 * time.Second,          // OpenSearch export cadence; WithOpenSearchExportInterval overrides (ADR-0114)
		retentionInterval: DefaultRetentionInterval, // history-retention sweep cadence; WithRetentionInterval overrides (ADR-0115)
		retentionBatch:    DefaultRetentionBatch,    // finished instances evaluated per sweep tick
		checkpointKeep:    checkpointKeepDefault,    // published checkpoints kept; WithCheckpointRetention overrides (ADR-0131)
		vaultEnabled:      true,                     // opt-out: built unless WithoutVault is passed (ADR-0070)
		users:             users,
		groups:            groups,
		sessions:          newSessionStore(defaultSessionTTL),
		oidcStates:        newOIDCStateStore(),
		collab:            collab.NewRegistry(),
		collabKeepalive:   collab.KeepaliveInterval,
		dmnResolver:       resolver,
		dmnValidator:      dmn.NewValidator(resolver),
		dmnRegistry:       dmn.NewRegistry(),
		docsEnabled:       true, // opt-out: served unless WithoutDocs is passed (ADR-0043)
		metricsEnabled:    true, // opt-out: served unless WithoutMetrics is passed (ADR-0142)
		readyTimeout:      readyTimeoutDefault,
	}
	// The retention sweep reads its eligibility cutoff from the system clock by
	// default; the options below may replace it (withClock) for a deterministic
	// test. Set here rather than in the literal above to keep the literal's comment
	// alignment intact (invariant I4).
	s.now = func() int64 { return time.Now().UnixNano() }
	// The checkpoint root is derived, not configurable: recovery resolves the same
	// path through checkpoint.Dir, and a knob here could only make them disagree
	// (ADR-0131). Set outside the literal for the same alignment reason as above.
	s.checkpointRoot = checkpoint.Dir(dataDir)
	// The documentation area is a service rather than a set of Server methods
	// (ADR-0147). It is built here, after the Server exists, because two of its
	// collaborators are the server's: the shared public-route rate limiter, and the
	// deployment lookup, which reads the registry and so is only ever called from
	// inside the run loop the service was given.
	s.processDocs = processdoc.New(
		s.runLoop,
		processDocStore,
		func(clientIP string) bool { return s.publicRate.allow(clientIP) },
		func(processID string) (processdoc.Deployment, bool) {
			d := s.latestDeploymentByProcessID(processID)
			if d == nil {
				return processdoc.Deployment{}, false
			}
			return processdoc.Deployment{Key: d.Key, Version: d.Version}, true
		},
		token.New,
	)
	// Panorama reuses the process-application scope rather than inventing an ACL.
	// The resolver is called only from the service's run-loop turn, so reading the
	// project store here keeps both authorization and model persistence under the
	// same single writer (ADR-0189/0147).
	s.panorama = panorama.New(
		s.runLoop,
		panoramaStore,
		func(r *http.Request, applicationID string) (panorama.ApplicationAccess, error) {
			app, ok, err := s.projects.Get(applicationID)
			if err != nil || !ok {
				return panorama.ApplicationAccess{Exists: ok}, err
			}
			role := app.effectiveRole(httpapi.PrincipalFrom(r.Context()), s.authEnabled)
			return panorama.ApplicationAccess{
				Exists: true, CanView: scopeRank(role) >= scopeRank(ScopeRoleViewer),
				CanEdit: scopeRank(role) >= scopeRank(ScopeRoleEditor), Protected: app.Protected,
			}, nil
		},
		token.New,
		time.Now,
		s.collectBindingCatalog,
	)
	s.panoramaMesh = panorama.NewMesh(s.runLoop, s.collectLandscape, s.panorama.OverlaysOnLoop, meshMaxNodes)
	for _, opt := range opts {
		opt(s)
	}
	// The encrypted secret vault (ADR-0069) is on by default (ADR-0070) unless
	// WithoutVault disabled it. An operator key from the environment is preferred
	// and never persisted; absent one, a key is loaded from — or generated into —
	// <data-dir>/vault.key so the vault works with no provisioning. Built after the
	// options so WithoutVault takes effect before any key file is touched.
	if s.vaultEnabled {
		key, source, err := vault.ResolveKey(filepath.Join(dataDir, "vault.key"))
		if err != nil {
			return nil, err
		}
		if s.vault, err = vault.New(filepath.Join(dataDir, "vault"), key); err != nil {
			return nil, err
		}
		if source == "generated" {
			logging.Info(logging.VaultKeyGenerated,
				"generated a vault master key (mode 0600); set ATLAS_VAULT_KEY for a stronger "+
					"at-rest posture or pass --vault=false to disable (ADR-0070)",
				slog.String("path", filepath.Join(dataDir, "vault.key")))
		}
	}
	// When enforcement is on, make sure a fresh instance has an admin to log in
	// with (ADR-0044) and mint the internal service token the in-process MCP
	// adapter uses to authenticate its loopback calls (ADR-0049). Both run before
	// the loop serves traffic, so touching the user store directly here respects
	// the single-writer discipline.
	if s.authEnabled {
		if err := s.bootstrapAdmin(time.Now().Unix()); err != nil {
			return nil, err
		}
		// And bring accounts written before roles were enforced up to the model, so an
		// upgrade takes nothing away from an installation that is running work
		// (rolesupgrade.go). Same goroutine, same reason, and idempotent: each record
		// carries the marker that says it has been through this.
		if err := s.upgradeLegacyRoles(time.Now().Unix()); err != nil {
			return nil, err
		}
		token, err := randomHex(32)
		if err != nil {
			return nil, err
		}
		s.internalToken = token
	}
	// Ensure the protected system project exists (ADR-0122). Like bootstrapAdmin,
	// this runs on the constructing goroutine before the loop serves traffic, so it
	// touches the project store directly within the single-writer discipline. It is
	// idempotent, so it is safe on every start regardless of auth.
	if err := s.ensureSystemProject(time.Now().Unix()); err != nil {
		return nil, err
	}
	// The in-process DMN worker evaluates business rule tasks off no separate
	// goroutine (the single-binary server drives jobs synchronously on the run
	// loop). One handler serves every process: it resolves each job's decision,
	// inputs, and result variable from the compiled process the job belongs to
	// (ProcessLookup), so it registers once under the reserved DMN job type
	// (compiler.DMNJobTypeIndex). It registers via HandleCompleting because a
	// decision's completion both writes its result back as a process variable and
	// retains the evaluation (inputs, outputs, trace) for debugging (ADR-0066).
	// Wake long-polling workers when a job of their type becomes durable. The notifier
	// fires on the processor's goroutine right after fsync (ADR-0005), so what it calls
	// must be bounded and non-blocking — closing channels, never sending on them.
	proc.SetJobNotifier(func(jobType int32) { s.jobWaiters.notify(jobType) })
	s.jobRunner = job.NewRunner(store, proc)
	s.jobRunner.HandleCompleting(compiler.DMNJobTypeIndex, func(rd state.Reader) job.CompletingHandler {
		return dmn.Handler(rd, s.processLookup, s.dmnRegistry, nil)
	})
	// Script-language workers (PowerShell, Python, JavaScript) each register under
	// their reserved job type and resolve each job's script from the compiled
	// process via processLookup, exactly like the DMN worker (ADR-0047). They run
	// arbitrary interpreter code, so only the languages an operator registered with
	// WithScriptWorker subscribe; the rest park.
	for jobType, exec := range s.scriptWorkers {
		s.jobRunner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler { return script.Handler(rd, s.processLookup, exec) })
	}
	// Managed connector job workers (temis, clio, mail, sharepoint, remedy, jira): one
	// registry plus job worker(s) per kind, all driven from the managedConnectorKinds
	// registry so adding a kind needs no new block here. Each registry is created and
	// built before the loop serves traffic and rebuilt on every connector change; a
	// task whose connector is not configured parks until it is.
	if err := s.setupManagedConnectors(store); err != nil {
		return nil, err
	}
	// An HTTP-REST connector task calls a model-authored endpoint (ADR-0067). One
	// worker serves every process under the reserved REST job type; it resolves each
	// job's method/url/headers/query/result-variable from the compiled process, calls
	// the API off the run loop and after fsync, and writes the JSON response into the
	// task's result variable. The endpoint and headers live in the model; a request's
	// authentication secret is a *reference* the worker resolves at call time from the
	// environment (resolveConnectorSecret, ADR-0041), so a token never lives in a model.
	// For oauth2 (client-credentials) the worker exchanges the client secret for a
	// bearer token through the token provider, caching it until it nears expiry
	// (ADR-0152).
	s.jobRunner.HandleWithOutput(compiler.RestJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return rest.Handler(rd, s.processLookup, rest.NewHTTPClient(), s.resolveConnectorSecret, rest.NewTokenProvider())
	})
	// A SCIM 2.0 connector task provisions/reads an identity resource against a
	// model-authored service provider (ADR-0153). Like REST the base URL and resource
	// live in the model and the authentication secret is a *reference* the worker
	// resolves at call time (resolveConnectorSecret, ADR-0041); unlike REST it speaks
	// SCIM (application/scim+json, resource-path URLs, filtered search). One worker
	// serves every process under the reserved SCIM job type.
	s.jobRunner.HandleWithOutput(compiler.ScimJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return scim.Handler(rd, s.processLookup, scim.NewHTTPClient(), s.resolveConnectorSecret)
	})
	// A generic LDAP connector task performs a directory operation (search/add/modify/
	// delete/modify-password) against a model-authored server (ADR-0154). The server URL
	// and DNs live in the model; the bind password is a *reference* the worker resolves
	// at call time (resolveConnectorSecret, ADR-0041). One worker serves every process
	// under the reserved LDAP job type; each job dials, binds, operates, and closes.
	//
	// The dialer pools its connections (ADR-0154, amended): a run over a few hundred
	// accounts would otherwise pay a handshake and a bind per entry. The pool is keyed
	// by the whole credential, so a connection bound as one identity is never handed to
	// a job asking for another, and Close releases what it holds at shutdown.
	s.ldapPool = ldap.NewPool(ldap.NewDialer(), ldap.PoolOptions{})
	s.jobRunner.HandleWithOutput(compiler.LdapJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return ldap.Handler(rd, s.processLookup, s.ldapPool, s.resolveConnectorSecret)
	})
	// A SOAP / Web Services (WSDL) connector task invokes an operation against a
	// model-authored web-service endpoint (ADR-0165) — reading identities (import) or
	// provisioning accounts (outbound). Like REST the endpoint and body live in the model
	// and any authentication secret is a *reference* the worker resolves at call time
	// (resolveConnectorSecret, ADR-0041); the worker wraps the body in a SOAP envelope,
	// carries the SOAPAction, and turns a SOAP fault into the job-failure message. One
	// worker serves every process under the reserved SOAP job type.
	s.jobRunner.HandleWithOutput(compiler.SoapJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return soap.Handler(rd, s.processLookup, soap.NewHTTPClient(), s.resolveConnectorSecret)
	})
	// An Active Directory connector task performs an AD-specific provisioning operation
	// (create-user, set-password via unicodePwd, enable/disable via userAccountControl,
	// add/remove a group member) against a model-authored server (ADR-0166). AD speaks
	// LDAP; the server URL and DNs live in the model, the bind password is a *reference*
	// the worker resolves at call time (resolveConnectorSecret, ADR-0041). One worker
	// serves every process under the reserved AD job type; each job dials, binds,
	// operates, and closes.
	s.jobRunner.HandleWithOutput(compiler.AdJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		// No directory registry in-process: a task naming a Console-configured
		// directory is served by the worker that holds it (ADR-0164/0168), and this
		// path exists only for a task that carries its own url.
		return ad.Handler(rd, s.processLookup, ad.NewDialer(), s.resolveConnectorSecret, nil)
	})
	// A CSV-import service task parses an uploaded CSV (a `csvText` variable) against
	// a `columnConfig` layout into a `rows` collection, in-process, so a batch of
	// records is ingested and validated on the engine with the file arriving through a
	// user-task form rather than a side-channel endpoint (ADR-0087). One worker serves
	// every process under the reserved CSV-import job type.
	s.jobRunner.HandleWithOutput(compiler.CsvImportJobTypeIndex, func(rd state.Reader) job.OutputHandler { return csvimport.Handler(rd, s.processLookup) })
	// A web-scraping service task fetches a model-authored URL and extracts the
	// elements matching a CSS selector, in-process, off the run loop and after fsync,
	// writing the extracted values into the task's result variable as a JSON array.
	// The URL and selector live in the model, like REST (ADR-0118). One worker serves
	// every process under the reserved web-scrape job type.
	s.jobRunner.HandleWithOutput(compiler.WebScrapeJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return webscrape.Handler(rd, s.processLookup, webscrape.NewHTTPClient())
	})
	// A directory-file connector task reads or writes LDIF/DSML entries (ADR-0171).
	// Like CSV it is a pure transform — no network, no credential — so it runs here as
	// well as on a worker, and neither placement can block the other.
	s.jobRunner.HandleWithOutput(compiler.LdifJobTypeIndex, func(rd state.Reader) job.OutputHandler {
		return ldif.Handler(rd, s.processLookup)
	})
	// User-provisioning connector (ADR-0123), opt-in. The handler mutates the
	// run-loop-owned user store, so it is a closure over s and runs on the loop (the
	// server drives jobs synchronously); it is gated at runtime to the system project.
	if s.userProvisioning {
		s.jobRunner.Handle(compiler.UserConnectorJobTypeIndex, func(rd state.Reader) job.Handler { return s.userConnectorHandler(rd) })
	}
	// Kinds the operator moved to a worker lose their in-process handler here, after
	// every registration path has run (ADR-0168).
	if err := s.applyOffloadedKinds(); err != nil {
		return nil, err
	}
	if err := s.loadDeployments(); err != nil {
		return nil, err
	}
	// Rebuild the per-application release counter from the durable release records,
	// so the next publish after a restart continues the sequence (ADR-0128).
	if err := s.loadReleaseVersions(); err != nil {
		return nil, err
	}
	// Same discipline for the per-process documentation counter, so an export after
	// a restart continues the sequence instead of minting a second v1 (ADR-0143).
	if err := s.processDocs.LoadVersions(); err != nil {
		return nil, err
	}
	// Peer deploy tokens (ADR-0129) into the in-memory index the auth middleware
	// reads, so a peer's credential keeps working across a restart.
	if err := s.loadAPITokens(); err != nil {
		return nil, err
	}
	s.checkWorkerTokenEnv()
	if err := s.loadDeployTokens(); err != nil {
		return nil, err
	}
	// Registered OAuth clients and the grants people approved for them (ADR-0200).
	// Both into their in-memory indexes before traffic, for the reason the token
	// indexes exist: resolving a credential happens on a handler goroutine and must
	// touch neither the disk nor a run-loop-owned store.
	if err := s.loadOAuth(); err != nil {
		return nil, err
	}
	// Push per-server call-activity overrides into the processor. Runs after
	// loadDeployments so a pin's version resolves to a definition key, and before the
	// loop serves traffic so touching the processor directly is single-writer-safe
	// (ADR-0105).
	if err := s.loadCallOverrides(); err != nil {
		return nil, err
	}
	// Bootstrap-deploy Atlas's own platform processes into the protected system
	// project (ADR-0122), when enabled. Runs after loadDeployments so its
	// idempotency check sees the recovered deployments (a restart adds no versions),
	// and before the loop serves traffic so the deploy path is single-writer-safe —
	// the same discipline loadDeployments itself uses.
	if s.deploySysProcs {
		if err := s.ensureSystemProcesses(time.Now().Unix()); err != nil {
			return nil, err
		}
		// Mint the public start link for the self-service registration process so a
		// fresh instance's login screen offers a "Registrieren" link out of the box
		// (ADR-0126). Runs after ensureSystemProcesses so the default process is
		// deployed, and before the loop serves so the mint is single-writer-safe.
		if err := s.ensureRegistrationLink(time.Now().Unix()); err != nil {
			return nil, err
		}
	}
	// Build the OpenSearch exporter when configured (ADR-0114). It tails the durable
	// WAL under dataDir and is bounded by the state store's applied-position
	// watermark (LastAppliedPosition), so it only ever indexes records that are on
	// disk — durable-before-visible (I2) — while never touching the processor.
	if s.osExportCfg.Enabled() {
		exp, err := opensearch.New(
			filepath.Join(dataDir, "wal"),
			s.store.LastAppliedPosition,
			opensearch.NewHTTPClient(s.osExportCfg),
			s.osExportCfg.Index,
			opensearch.NewPositionStore(filepath.Join(dataDir, "exporter")),
		)
		if err != nil {
			return nil, err
		}
		s.exporter = exp
	}

	// The job-history exporter, when an operator named a connector for it. It is a
	// plain goroutine rather than one of the tracked three: nothing waits for it, and
	// a shutdown that blocked on draining telemetry would be the tail wagging the dog.
	//
	// The registry holds it too, because the settle path is where a run becomes a
	// history entry.
	s.workers.history = s.history
	if s.history != nil {
		go s.history.run(quit)
	}

	s.wg.Add(3)
	go s.loop()

	// Supervised workers, if the operator asked for any on the command line, or if the
	// default opt-out set applies. They stop with the server: the same `atlas worker`
	// an operator could run by hand, just launched here (ADR-0157 step 7).
	//
	// They are registered after the run loop is running because a worker's environment
	// is read from the connector store, and the store is the loop's to read.
	if len(s.SuperviseSpecs) > 0 {
		s.supervisor = newSupervisor(quit)
		for i, spec := range s.SuperviseSpecs {
			args := []string{"worker", "--server", s.superviseURL, "--id", spec.ID}
			for _, h := range s.superviseHandles[i] {
				args = append(args, "--handle", h)
			}
			// A supervised worker may also serve built-in connector kinds. It is a
			// child of this process, so it inherits the environment any of them read
			// their configuration from — and for a kind whose configuration lives in
			// the connector store instead, the engine adds it to that environment at
			// spawn (see superviseEnv). Together that is what makes the default set
			// work with nothing configured at all.
			if len(spec.Connectors) > 0 {
				args = append(args, "--connector", strings.Join(spec.Connectors, ","))
			}
			s.supervisor.add(spec, args, s.superviseEnv(spec))
		}
		s.supervisor.start()
	}
	go s.timerScheduler(time.Second)
	// The collaboration reaper evicts idle detached session participants (MCP
	// agents that stopped polling) and releases their locks (ADR-0140). It runs off
	// the run loop — the collab registry is its own mutex-guarded, engine-independent
	// state — so it never touches the processor or the invariants.
	go s.collabReaper(collab.ReapInterval)
	// The clio inbound bridge polls configured subscriptions and republishes new
	// clio events as Atlas messages (ADR-0075). It is a separate goroutine like the
	// timer scheduler — it does its network reads off the run loop and hands only the
	// resulting publish onto it (invariant I3). A non-positive interval disables it.
	if s.inboundPoll > 0 {
		s.wg.Add(1)
		go s.inboundBridge(s.inboundPoll)
	}
	// The OpenSearch exporter tails the durable log and bulk-indexes new records
	// (ADR-0114). Like the timer scheduler and inbound bridge it is a separate
	// goroutine doing its I/O off the run loop; it reads only the durable WAL files
	// and the concurrent-safe applied-position watermark, so it never funnels
	// through do() and never touches the processor (invariant I3).
	if s.exporter != nil {
		s.wg.Add(1)
		go s.exporterLoop(s.exporterPoll)
	}
	// History retention hard-deletes finished instances past the max age, gated on the
	// exported (else durable) position (ADR-0115). The sweeper always runs: retention is
	// configured server-wide *or* per definition (atlas:historyTtl, ADR-0144), and a
	// definition carrying one can be deployed at any moment — so the cheap standing tick
	// is what makes a mid-flight deploy take effect. A tick with nothing due and no
	// server-wide age costs one empty range scan of the expiry index (ADR-0146). Like the
	// other pollers it
	// computes wall-clock time off the run loop and hops onto it via do() to touch the
	// processor and its bounded, resumable cursor.
	s.wg.Add(1)
	go s.retentionSweeper(s.retentionInterval)
	// Recovery checkpoints bound restart time (ADR-0131). Off unless a cadence is
	// configured. Unlike the pollers above, this one's actual work happens *on* the run
	// loop — a snapshot is only exact at a batch boundary (invariant I3) — so the
	// goroutine exists to own the cadence and the off-loop pruning, not the snapshot.
	if s.metricsEnabled {
		if err := s.buildMetrics(); err != nil {
			return nil, err
		}
	}
	if s.checkpointInterval > 0 {
		s.checkpointRequests = make(chan chan checkpointPass)
		s.wg.Add(1)
		go s.checkpointLoop(s.checkpointInterval)
	}
	return s, nil
}

// exporterLoop polls the durable log on a fixed cadence and hands each newly
// durable batch of records to the OpenSearch exporter (ADR-0114). A tick error
// (OpenSearch unreachable, a transient index failure) is logged and retried on the
// next tick — the exporter leaves its high-water mark unadvanced on failure, so no
// record is skipped and delivery stays at-least-once.
//
// The trigger is a seam for deterministic tests: production runs a real ticker,
// while a test injects an explicit tick channel (withExporterTrigger) so an export
// pass runs exactly when the test says — no cadence to race, no polling. When
// exporterTicked is set the loop signals it after each triggered pass so the test can
// await completion. This mirrors the retention sweep's clock/ticker seam.
func (s *Server) exporterLoop(every time.Duration) {
	defer s.wg.Done()
	ticks := s.exporterTicks
	if ticks == nil {
		t := time.NewTicker(every)
		defer t.Stop()
		ticks = t.C
	}
	for {
		select {
		case <-s.quit:
			return
		case <-ticks:
			if n, err := s.exporter.Tick(context.Background()); err != nil {
				logging.Warn(logging.ExporterTickFailed, "opensearch export tick failed; will retry next tick",
					slog.String("error", err.Error()))
			} else if n > 0 {
				logging.Info(logging.ExporterIndexed, "indexed records into opensearch",
					slog.Int("records", n))
			}
			if s.exporterTicked != nil {
				select {
				case s.exporterTicked <- struct{}{}:
				case <-s.quit:
					return
				}
			}
		}
	}
}

// retentionSweeper runs the history-retention sweep on a fixed cadence (ADR-0115).
// Wall-clock "now" is read from s.now() off the run loop; the sweep itself hops onto
// the loop via do() so its scan, purge commands, and cursor are single-writer-safe.
//
// The trigger and clock are seams for deterministic tests (invariant I4): production
// runs a real ticker and the system clock, while a test injects an explicit tick
// channel and a fixed clock (withRetentionTrigger/withClock) so a sweep fires exactly
// when the test says and evaluates a time the test controls — no scheduling luck, no
// wall-clock-tuned durations. When retentionSwept is set the sweeper signals it after
// each triggered sweep so the test can await completion.
func (s *Server) retentionSweeper(every time.Duration) {
	defer s.wg.Done()
	ticks := s.retentionTicks
	if ticks == nil {
		t := time.NewTicker(every)
		defer t.Stop()
		ticks = t.C
	}
	for {
		select {
		case <-s.quit:
			return
		case <-ticks:
			now := s.now()
			s.do(func() { s.sweepRetention(now) })
			if s.retentionSwept != nil {
				select {
				case s.retentionSwept <- struct{}{}:
				case <-s.quit:
					return
				}
			}
		}
	}
}

// purgeTarget is one finished instance a sweep decided to hard-delete, carrying the
// history value the purge command needs (its definition key, and the purge due date that
// locates its schedule entry) so neither the command nor applyToState reads it again.
type purgeTarget struct {
	key uint64
	pi  model.ProcessInstanceValue
}

// sweepRetention hard-deletes the finished instances that are eligible now, from two
// sources: what a definition's own historyTtl scheduled (the due-date index, ADR-0146)
// and — only when the operator configured a server-wide max age — what that age has
// caught in the bounded key-order window (ADR-0115). Both apply the same export gate, so
// nothing is deleted before it is archived. It runs inside a do() turn, so the scans, the
// purge commands it enqueues, and the cursor advance are one atomic single-writer step.
// Errors are logged and retried next tick.
func (s *Server) sweepRetention(now int64) {
	// A transient read error just skips this tick (retried on the next), matching the
	// silent, best-effort style of the other run-loop pollers (timerScheduler).
	safePos, err := s.retentionSafePosition()
	if err != nil {
		return
	}
	targets := s.scheduledPurges(now, safePos)
	if s.retentionMaxAge > 0 {
		targets = s.agedPurges(targets, now, safePos)
	}
	if len(targets) == 0 {
		return
	}
	for i := range targets {
		s.proc.PurgeInstance(targets[i].key, &targets[i].pi)
	}
	// Applying the purge commands is the whole of the work: a purge unblocks no
	// in-process handler. An error is retried on the next tick.
	_ = s.proc.RunUntilIdle()
	// No single age to name: instances in one sweep may have come due under different
	// per-definition TTLs, or under the server-wide age (ADR-0144).
	logging.Info(logging.RetentionPurged, "purged finished instances past their history TTL",
		slog.Int("instances", len(targets)))
}

// scheduledPurges collects the instances whose declared history TTL has elapsed, by
// range-scanning the due-date index up to now for at most one batch (ADR-0146). This is
// what makes a tick cost what is *due* rather than what the history *holds*: an idle
// server pays one empty scan, and a due instance is never delayed by the millions of
// finished records that no policy can touch. An instance whose events are not yet
// exported keeps its index entry and is re-offered on the next tick — the export gate
// delays a purge, it never cancels one (ADR-0115).
func (s *Server) scheduledPurges(now int64, safePos uint64) []purgeTarget {
	var targets []purgeTarget
	if _, err := s.store.DueHistoryExpiries(now, s.retentionBatch, func(_ int64, piKey uint64) error {
		pi, ok, err := s.store.ProcessInstance(piKey)
		if err != nil {
			return err
		}
		// No record behind the entry: nothing to delete, and nothing to decide from.
		if !ok {
			return nil
		}
		if pi.CompletedPosition != 0 && pi.CompletedPosition <= safePos {
			targets = append(targets, purgeTarget{piKey, *pi})
		}
		return nil
	}); err != nil {
		return nil
	}
	return targets
}

// agedPurges appends what the server-wide max age has caught, from one bounded, resumable
// window of the history in key order (ADR-0115). That age has no schedule behind it — it
// is a flag that can change between restarts, and it is the only thing that reaches
// records finished before ADR-0146 indexed them — so this path still scans rather than
// asks. A definition's own TTL wins over the age for its instances (ADR-0144), and an
// instance already scheduled above is skipped so one sweep never purges it twice.
func (s *Server) agedPurges(targets []purgeTarget, now int64, safePos uint64) []purgeTarget {
	scheduled := make(map[uint64]struct{}, len(targets))
	for i := range targets {
		scheduled[targets[i].key] = struct{}{}
	}
	next, more, err := s.store.CompletedProcessInstancesFrom(s.retentionCursor, s.retentionBatch,
		func(key uint64, v *model.ProcessInstanceValue) error {
			if _, dup := scheduled[key]; dup {
				return nil
			}
			// The max age is the instance's own definition's when it declares one, else the
			// server-wide setting (ADR-0144); zero means nothing retains this instance.
			maxAge := s.retentionAgeFor(v.ProcessDefKey)
			if maxAge <= 0 {
				return nil
			}
			// Eligible only when old enough AND export-provable: a zero CompletedPosition
			// (a record written before this feature) is never provably exported, so it is
			// conservatively skipped rather than deleted (ADR-0115).
			if v.CompletedAt <= now-maxAge && v.CompletedPosition != 0 && v.CompletedPosition <= safePos {
				targets = append(targets, purgeTarget{key, *v})
			}
			return nil
		})
	if err != nil {
		return targets
	}
	// Advance the cursor; wrap to genesis at the end so the next pass re-evaluates
	// instances that have since aged past the cutoff or become exported.
	if more {
		s.retentionCursor = next
	} else {
		s.retentionCursor = 0
	}
	return targets
}

// retentionAgeFor returns the retention max age in nanoseconds that governs a finished
// instance of defKey: the definition's own atlas:historyTtl when it declares one
// (ADR-0144), otherwise the server-wide max age. Zero means no retention applies and
// the instance is kept indefinitely — the case for an undeployed definition (its
// compiled process is gone, so only a server-wide setting can still reach it). Reads
// the deployment registry, so it runs on the loop, inside the sweep's do() turn.
func (s *Server) retentionAgeFor(defKey uint64) int64 {
	if cp := s.processLookup(defKey); cp != nil {
		if ttl := cp.HistoryTtlNanos(); ttl > 0 {
			return ttl
		}
	}
	return s.retentionMaxAge.Nanoseconds()
}

// retentionSafePosition is the highest log position safe to hard-delete up to: the
// exporter's high-water mark when the exporter is enabled (delete only what OpenSearch
// already holds — the operator's export-before-delete requirement), otherwise the
// state store's durable applied position (retention still works standalone, with the
// WAL as the archive of record). See ADR-0115.
func (s *Server) retentionSafePosition() (uint64, error) {
	if s.exporter != nil {
		return s.exporter.HighWaterMark(), nil
	}
	return s.store.LastAppliedPosition()
}

// checkpointLoop publishes a recovery checkpoint on a fixed cadence and prunes the
// ones it makes redundant (ADR-0131). A failed pass is logged and retried on the next
// tick: a checkpoint is an optimization over the WAL, never a durability step, so
// losing one costs a slower recovery and nothing else (invariant I2).
//
// The trigger is the same seam the retention sweep and exporter use — production runs
// a real ticker, a test injects an explicit channel (withCheckpointTrigger) so a
// checkpoint is taken exactly when the test says. When checkpointDone is set the loop
// signals it after each triggered pass so the test can await completion.
func (s *Server) checkpointLoop(every time.Duration) {
	defer s.wg.Done()
	ticks := s.checkpointTicks
	if ticks == nil {
		t := time.NewTicker(every)
		defer t.Stop()
		ticks = t.C
	}
	// last is the position most recently reported as published. An idle server
	// re-publishes nothing (Publish is a no-op at an existing position), so tracking it
	// keeps the cadence from logging the same line forever. Only this goroutine reads
	// or writes it, so it stays a local.
	var last uint64
	for {
		select {
		case <-s.quit:
			return
		case reply := <-s.checkpointRequests:
			// An operator asked for a pass (ADR-0131 slice 8). It runs *here*, on the same
			// goroutine as the cadence, so an on-demand pass and a scheduled one can never
			// overlap — and it is the same pass, not a second code path.
			res := s.runCheckpointPass(&last)
			select {
			case reply <- res:
			case <-s.quit:
				return
			}
		case <-ticks:
			s.runCheckpointPass(&last)
			if s.checkpointDone != nil {
				select {
				case s.checkpointDone <- struct{}{}:
				case <-s.quit:
					return
				}
			}
		}
	}
}

// runCheckpointPass takes a checkpoint and, when compaction is enabled, compacts on the
// same pass: the checkpoint just taken is what licenses any new deletion, so running the
// two together keeps them in one order. It records the result for the status endpoint
// and returns it for the operator who asked.
//
// last carries the position most recently *logged* as published. An idle server
// re-publishes nothing (Publish is a no-op at an existing position), so tracking it keeps
// the cadence from logging the same line forever. Only the checkpoint goroutine touches
// it, so it needs no lock.
func (s *Server) runCheckpointPass(last *uint64) checkpointPass {
	res := checkpointPass{At: s.now()}
	pos, err := s.takeCheckpoint()
	res.Position = pos
	if err != nil {
		res.CheckpointError = err.Error()
	}
	if pos != 0 && pos != *last {
		*last = pos
		logging.Info(logging.CheckpointPublished, "published a recovery checkpoint; recovery replays only past it",
			slog.Uint64("position", pos))
	}
	if s.compactWAL {
		removed, note, cerr := s.compactLog()
		res.SegmentsRemoved = removed
		res.Note = note
		if cerr != nil {
			res.CompactionError = cerr.Error()
		}
	}
	s.lastPassMu.Lock()
	s.lastPass = &res
	s.lastPassMu.Unlock()
	return res
}

// takeCheckpoint publishes one checkpoint and prunes older ones, returning the position
// it captured (zero when nothing was published — an idle server or a failed pass).
//
// The snapshot hops onto the run loop via do(): between batches, the store's applied
// position and the state it holds agree exactly, which is precisely what makes a
// checkpoint's consistency boundary meaningful (invariant I3). Pruning then runs off
// the loop, keeping directory removal and an fsync out of the writer's way; it is safe
// there because only this goroutine ever publishes into the root.
func (s *Server) takeCheckpoint() (uint64, error) {
	var pos uint64
	var err error
	s.do(func() {
		var applied uint64
		if applied, err = s.store.LastAppliedPosition(); err != nil || applied == 0 {
			return // nothing durable yet: there is no state worth snapshotting
		}
		pos, err = s.proc.Checkpoint(s.checkpointRoot)
	})
	if err != nil {
		logging.Warn(logging.CheckpointFailed, "taking a recovery checkpoint failed; will retry next tick",
			slog.String("error", err.Error()))
		return 0, err
	}
	if pos == 0 {
		return 0, nil
	}
	if err := checkpoint.Prune(s.checkpointRoot, s.checkpointKeep); err != nil {
		// The checkpoint is published either way; a failed prune costs disk, not recovery,
		// so it is reported without discarding the position that was captured.
		logging.Warn(logging.CheckpointPruneFailed,
			"pruning old checkpoints failed; will retry next tick — this costs disk, not recovery",
			slog.String("error", err.Error()), slog.Uint64("position", pos))
		return pos, err
	}
	return pos, nil
}

// compactLog deletes the WAL segments the newest verified checkpoint and every consumer
// watermark make redundant (ADR-0131). Everything about it is fail-closed: a watermark
// that cannot be read, a snapshot in flight, or an error anywhere skips the pass
// entirely, because the cost of skipping is disk and the cost of proceeding is a segment
// recovery still needs.
//
// The deletion runs on the run loop (do): the WAL belongs to the single writer, which
// may be rolling a segment at the same moment (invariant I3). It is bounded work — a few
// unlinks and one directory fsync — and only for segments already proven redundant.
// It returns how many segments went, and a note naming why a pass did nothing when the
// reason is a deliberate hold rather than an error — so an operator who asked for a pass
// gets an answer, not a silent zero.
func (s *Server) compactLog() (int, string, error) {
	// A whole-instance snapshot streams the WAL files off the run loop; deleting
	// underneath one would ship an archive with a hole (ADR-0109). Skipping costs a tick.
	if s.backupsInFlight.Load() > 0 {
		return 0, "skipped: a whole-instance snapshot is streaming the WAL", nil
	}
	limits, err := s.consumerWatermarks()
	if err != nil {
		logging.Warn(logging.WALCompactionWatermarkFailed,
			"a consumer watermark is unavailable; skipping compaction this tick rather than "+
				"deleting a segment a consumer may still need",
			slog.String("error", err.Error()))
		return 0, "", err
	}
	var removed int
	s.do(func() { removed, err = s.proc.CompactLog(s.checkpointRoot, limits) })
	if err != nil {
		logging.Warn(logging.WALCompactionFailed, "wal compaction failed; will retry next tick",
			slog.String("error", err.Error()))
		return 0, "", err
	}
	if removed > 0 {
		logging.Info(logging.WALCompactionSegmentsDeleted,
			"deleted WAL segments already covered by a checkpoint and every consumer watermark",
			slog.Int("segments", removed))
	}
	return removed, "", nil
}

// consumerWatermarks is the highest position each consumer of the durable log has
// provably read, which compaction may not delete past. It returns an error rather than a
// short list when one cannot be read: a missing watermark must hold the log, never
// silently stop constraining it.
//
// Only the OpenSearch exporter (ADR-0114) actually tails the WAL. History retention
// (ADR-0115) reads the state store rather than the log, so its safe position never binds
// tighter than the exporter's — it *is* the exporter's high-water mark when export is on
// — but it is included when retention is enabled so the gate matches ADR-0131's rule
// literally rather than by an argument that could stop holding.
func (s *Server) consumerWatermarks() ([]uint64, error) {
	var limits []uint64
	if s.exporter != nil {
		limits = append(limits, s.exporter.HighWaterMark())
	}
	if s.retentionMaxAge > 0 {
		pos, err := s.retentionSafePosition()
		if err != nil {
			return nil, err
		}
		limits = append(limits, pos)
	}
	return limits, nil
}

// processLookup resolves a def key to its compiled process for the DMN worker. It
// is called only while driving jobs on the run-loop goroutine, so reading the
// deployment registry here needs no locking.
func (s *Server) processLookup(defKey uint64) *compiler.CompiledProcess {
	if d, ok := s.deployments[defKey]; ok {
		return d.cp
	}
	return nil
}

// WithOffloadedConnectorKinds names the managed connector kinds this server must
// NOT serve itself, so their jobs park for an external worker instead
// (ADR-0168/0164).
//
// This is the operative act of relocating a kind. The type-keyed pull refuses a job
// type an in-process handler is registered for — that refusal is what keeps one job
// from being worked twice — so a kind stays in the engine until its handler is
// turned off here. What is left is exactly what an unconfigured connector already
// does: the job parks on the activatable index until something takes it.
//
// An unknown name is refused at startup rather than ignored. An operator who
// misspells a kind would otherwise believe they had relocated it while it kept
// running in the engine, which is the one outcome this flag exists to prevent.
func WithOffloadedConnectorKinds(kinds []string) Option {
	return func(s *Server) { s.offloadedKinds = kinds }
}

// WithSupervisedWorkers asks the server to run these workers itself: one child
// process per entry, restarted while the server lives (ADR-0157 step 7).
//
// It is an Option, so it comes from the process's own command line and from
// nowhere else. Nothing a request carries can add, change or name a supervised
// worker's command — the API can restart one that is already configured, and can
// do nothing else to it.
func WithSupervisedWorkers(serverURL string, specs []SuperviseSpec, handles [][]string) Option {
	return func(s *Server) {
		s.superviseURL = serverURL
		s.SuperviseSpecs = specs
		s.superviseHandles = handles
	}
}

// WithWorkerHistory sends every settled job run to a clio connector, so a worker's
// history outlives this process and its retention becomes the operator's own policy
// in their own store (see workerhistory.go).
//
// Like the supervisor's configuration it is an Option, so the connector is named on
// this server's command line and nowhere else. The name is resolved at write time
// rather than at startup: an operator may create the connector after the server is
// running, and the history should start flowing when they do rather than at the next
// restart.
//
// scope is [HistoryScopeAll] or [HistoryScopeFailed]; anything else means all.
func WithWorkerHistory(connector, scope string) Option {
	return func(s *Server) {
		s.history = newHistoryExporter(connector, scope, func() (clio.Client, bool) {
			var (
				c  clio.Client
				ok bool
			)
			// On the loop: clientreg.Registry has no lock, and a connector rebuild
			// swaps its map wholesale. The network write happens outside, in run.
			s.do(func() { c, ok = s.clioRegistry.Client(connector) })
			return c, ok
		})
	}
}

// drive runs the engine and its in-process job handlers until nothing is left to
// do. It is where ADR-0157 step 6 actually lands: the run loop is held only for the
// processor's own steps — claiming jobs and applying their outcomes — while the
// handlers, which is where a connector's outbound call happens, run out here on the
// caller's goroutine.
//
// Before this, driving happened inside s.do, so every outbound call held the single
// writer for its duration. ADR-0155 measured that and ADR-0149 could only bound it
// with a timeout; now a slow host stalls the request that caused it, not the engine.
//
// The caller still waits for quiescence, which is deliberate: every request path and
// every test would otherwise change meaning. What changed is *who* waits — the
// caller's goroutine instead of the one goroutine everything else needs.
//
// Drivers are serialized. Two concurrent callers must not claim the same job and
// work it twice, and the second waiting for the first is also what keeps "my
// request's work is done when it returns" true.
//
// Handlers read through a [state.ReadView] taken while the loop is held, so each
// round sees one coherent state rather than whatever the writer has reached since —
// the guarantee they used to get for free by running on the writer itself.
func (s *Server) drive() error {
	s.driveMu.Lock()
	defer s.driveMu.Unlock()
	for {
		var (
			jobs []job.Job
			view *state.ReadView
			err  error
		)
		s.do(func() {
			if err = s.proc.RunUntilIdle(); err != nil {
				return
			}
			if jobs, err = s.jobRunner.Claim(); err != nil || len(jobs) == 0 {
				return
			}
			view = s.store.ReadView()
		})
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		outcomes := s.jobRunner.Work(jobs, view)
		_ = view.Close()
		if len(outcomes) == 0 {
			return nil // nothing this runner serves; the rest is an external worker's
		}
		s.do(func() { s.jobRunner.Submit(outcomes) })
	}
}

// loadDeployments rebuilds the in-memory deployment registry and re-registers
// each definition with the processor from the durable store, so a restart
// restores diagrams, names, versions, and the ability to advance recovered
// instances (ADR-0019). It runs before the loop serves traffic, so touching the
// registry and the processor directly here respects the single-writer invariant.
func (s *Server) loadDeployments() error {
	recs, err := s.deploys.LoadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		// Recompile exactly the process this record represents (a collaboration's
		// XML holds several), keyed as originally assigned (ADR-0019/0022) — and
		// without the deploy-time validation gate
		// (ADR-0177). This definition passed the gate
		// that existed when it was deployed and its instances have been running under
		// it since; a rule added to the compiler afterwards is a reason to tell the
		// operator, not to refuse to start. The model on disk did not change.
		cp, problems, err := compiler.ReloadNamed(rec.Key, rec.Version, bytes.NewReader([]byte(rec.XML)), rec.ProcessID)
		if err != nil {
			// A stored model that no longer compiles *at all* is still a hard,
			// actionable error rather than a silently dropped definition (ADR-0019):
			// there is no definition to bring back, so its instances could not advance
			// either way. Name the record, since acting on it means editing that file.
			return fmt.Errorf("api: reload deployment %d (%s v%d) from %s: %w", rec.Key, rec.ProcessID, rec.Version, s.deploys.fileFor(rec.Key), err)
		}
		if len(problems) > 0 {
			// Not silent: the model is drifting from what the compiler now asks for,
			// and the next deploy of it will be refused. Named per deployment so the
			// operator can go straight to the model that needs fixing.
			logging.Warn(logging.DeploymentReloadedWithProblems,
				"a deployed definition would no longer pass validation; it was restored and keeps running — fix the model and deploy it again",
				slog.Uint64("deploymentKey", rec.Key),
				slog.String("processId", rec.ProcessID),
				slog.Int64("version", int64(rec.Version)),
				slog.String("artifact", "bpmn"),
				slog.String("problems", compiler.SummarizeProblems(problems)))
		}
		cp.Version = rec.Version
		// Re-resolve the job types the same way the original deploy did. The registry
		// is durable and never recycles an index, so this lands on exactly the indices
		// the jobs already in state were written under (ADR-0007/0157).
		if err := cp.ResolveJobTypes(s.jobTypes.Intern); err != nil {
			return fmt.Errorf("api: resolve job types for deployment %d (%s v%d): %w", rec.Key, rec.ProcessID, rec.Version, err)
		}
		s.proc.Deploy(cp)
		// Re-register the process's DMN models so its business rule tasks evaluate
		// after a restart, exactly as they did when first deployed (ADR-0014). The
		// models are snapshotted in the deployment record (a legacy record carries a
		// single model), so no temis reference has to be re-resolved here.
		for _, dmnXML := range rec.dmnModels() {
			// The same split as the BPMN model above
			// (ADR-0177), for the same reason: refusing a
			// snapshotted DMN model here undeploys nothing, it only keeps the server from
			// starting. A decision that stopped compiling since the deploy fails when it
			// is evaluated — a job error on a worker, which the engine has an answer for
			// — while every other decision in the model keeps answering.
			dmnProblems, err := s.dmnRegistry.Reload(rec.Key, []byte(dmnXML))
			if err != nil {
				return fmt.Errorf("api: reload dmn model for def %d (%s) from %s: %w", rec.Key, rec.ProcessID, s.deploys.fileFor(rec.Key), err)
			}
			if dmnProblems != "" {
				logging.Warn(logging.DeploymentReloadedWithProblems,
					"a DMN model bundled with a deployed definition no longer compiles cleanly; the definition keeps running and the decisions that still compile keep answering — fix the model and deploy it again",
					slog.Uint64("deploymentKey", rec.Key),
					slog.String("processId", rec.ProcessID),
					slog.Int64("version", int64(rec.Version)),
					slog.String("artifact", "dmn"),
					slog.String("problems", dmnProblems))
			}
		}
		// Restore the deactivation flag (ADR-0119) before the loop serves traffic and
		// before timers tick, so a start timer restored from the log finds the definition
		// inactive and skips instantiation. loadDeployments does not re-arm timers (they
		// come back from the WAL), so this is the only place recovery re-applies the gate.
		if rec.Inactive {
			s.proc.SetProcessActive(rec.Key, false)
		}
		s.deployments[rec.Key] = &deployment{
			Key:        rec.Key,
			ProcessID:  rec.ProcessID,
			Name:       rec.Name,
			Version:    rec.Version,
			DeployedAt: rec.DeployedAt,
			ProjectID:  rec.ProjectID,
			DeployedBy: rec.DeployedBy,
			xml:        []byte(rec.XML),
			cp:         cp,
			inactive:   rec.Inactive,
		}
		s.order = append(s.order, rec.Key)
		if rec.Version > s.versions[rec.ProcessID] {
			s.versions[rec.ProcessID] = rec.Version
		}
		if rec.Key >= s.nextKey {
			s.nextKey = rec.Key + 1
		}
	}
	return nil
}

// timerScheduler fires due timers on the run-loop goroutine at a fixed cadence,
// so intermediate timer events wake up without any external command. The tick is
// coarse (whole seconds) — timers are "fire at or after due", not real-time.
func (s *Server) timerScheduler(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			// Fire due timers on the loop, then drive any jobs they unblocked (e.g. a
			// timer leading into a business rule task) OFF it: this tick runs every
			// second, and before ADR-0157 step 6 it was the path that could hold the
			// single writer for a connector call nobody had asked for.
			ticked := false
			s.do(func() { ticked = s.proc.TickTimers() == nil })
			if ticked {
				_ = s.drive()
			}
		}
	}
}

// collabReaper periodically evicts detached collaboration-session participants
// (AI agents that joined over MCP and stopped polling) that have gone silent past
// the TTL, releasing their locks so a crashed or forgotten agent never holds an
// element forever (ADR-0140). Browser SSE participants are reaped on disconnect
// instead and are exempt. It runs off the run loop — the collab registry is its
// own mutex-guarded, engine-independent state — so it never touches the processor.
func (s *Server) collabReaper(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			if n := s.collab.Reap(); n > 0 {
				logging.Info(logging.CollabParticipantsReaped,
					"reaped idle collaboration participants past their TTL (ADR-0140)",
					slog.Int("participants", n), slog.Duration("ttl", collab.ParticipantTTL))
			}
		}
	}
}

// loop runs the single-writer goroutine that owns the processor. Every processor
// and registry access funnels through it, so the single-writer invariant holds
// even though HTTP handlers are concurrent; see [runloop] for the boundary
// itself.
func (s *Server) loop() {
	defer s.wg.Done()
	s.runLoop.Run()
}

// do runs fn on the run-loop goroutine and blocks until it completes. If the
// server is closing, fn does not run and do returns immediately; callers must
// treat their result variables' zero values as "not produced".
//
// It is a shorthand for the server's own handlers; a per-area service holds the
// [runloop.Loop] itself (ADR-0147).
func (s *Server) do(fn func()) { s.runLoop.Do(fn) }

// Close stops the run-loop goroutine. It does not close the processor, log, or
// store — the caller owns those.
func (s *Server) Close() {
	close(s.quit)
	s.wg.Wait()
	// After the workers have stopped, nothing is borrowing from the pool any more, so
	// the sockets it is holding can go.
	if s.ldapPool != nil {
		_ = s.ldapPool.Close()
	}
}

// Handler returns the HTTP handler: the JSON API under /api/v1, the probes, the
// MCP transport when one was supplied (WithMCP), the public share links, and the
// embedded web UI at the root — every one of them behind the access boundary.
//
// With --docs (the default) it also serves the OpenAPI document at
// /api/v1/openapi.json and the Scalar API explorer at /api/docs.
func (s *Server) Handler() http.Handler {
	mux, policy := s.mountRoutes()
	return s.withAuth(policy, mux)
}

// mountRoutes registers every route this server serves and, in the same pass,
// declares the access class of each one (see access.go).
//
// The two are built together on purpose. mount is the only way a route reaches
// the mux, and it cannot be called without stating a class, so "public because
// nobody said otherwise" is not a state this function can produce. A route
// mounted straight onto mux, bypassing it, is not thereby public either — it is
// undeclared, and classify gates what it does not know.
func (s *Server) mountRoutes() (*http.ServeMux, *accessPolicy) {
	mux := http.NewServeMux()
	policy := newAccessPolicy(mux)
	// Two declarations per route, both required by the signature: who may reach it
	// without a principal (access.go) and which role a principal needs
	// (routeroles.go). Neither has a default here, so a route cannot be mounted
	// without both being decided at the mount site.
	mount := func(class accessClass, role, pattern string, h http.Handler) {
		mux.Handle(pattern, h)
		policy.declare(pattern, class, role)
	}
	mountFunc := func(class accessClass, role, pattern string, h http.HandlerFunc) {
		mount(class, role, pattern, h)
	}

	// The Prometheus exposition sits beside the probes — same process, same port
	// (ADR-0142) — but not beside them in reach. It was the last route whose
	// protection depended on a proxy rule rather than on this server, and a scraper
	// is a machine, so it presents a credential like every other machine does: an
	// API token scoped "metrics", which reaches this one route and nothing else
	// (ADR-0198).
	if s.metricsEnabled {
		mountFunc(accessAuthenticated, roleAny, "GET /metrics", s.handleMetrics)
	}
	// Liveness: is this process alive. Unconditional on purpose — the only remedy a
	// liveness probe has is a restart, so it must not fail for anything a restart would
	// not fix (a long recovery above all). Readiness is the separate question, and it is
	// answered separately, at /readyz (ADR-0142). Both are public because a probe that
	// needs a credential does not work in the incident it exists for.
	mountFunc(accessPublic, roleAny, "GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mountFunc(accessPublic, roleAny, "GET /readyz", s.handleReadyz)

	// What this server is, as an OAuth protected resource (RFC 9728, ADR-0200).
	// Public because it is the document a caller reads *after* being refused, to find
	// out what refused it — behind the credential it exists to help you obtain, it
	// could never be read by anyone who needed it. It names no secret: the origin, a
	// product name, and that a bearer goes in a header.
	mountFunc(accessPublic, roleAny, "GET "+protectedResourceMetadataPath, s.handleProtectedResourceMetadata)
	// The authorization server (ADR-0200). Its metadata is public for the same
	// reason: a client reads it before it holds anything. /oauth/authorize is public
	// because an unauthenticated *browser* has to be able to land there — a 401 is
	// not an answer a person can act on — and the page it serves shows nothing until
	// it asks, while /oauth/token authenticates the client from its own body.
	mountFunc(accessPublic, roleAny, "GET "+oauthMetadataPath, s.handleAuthorizationServerMetadata)
	mountFunc(accessPublic, roleAny, "GET "+oauthAuthorizePath, s.handleAuthorize)
	mountFunc(accessPublic, roleAny, "POST "+oauthTokenPath, s.handleToken)
	if s.dynamicRegistration {
		// RFC 7591 self-registration, when an operator opened it. Public because a
		// client that has nothing yet is the entire caller — and mounted only when it
		// is on, so a server that did not open it answers 404 rather than refusing
		// something it appears to offer (ADR-0200, oauthregister.go).
		mountFunc(accessPublic, roleAny, "POST "+oauthRegisterPath, s.handleRegisterDynamicClient)
	}
	if s.mcpHandler != nil {
		// The path-suffixed form, for the transport as a resource of its own. Served
		// only when that transport is, so the document never describes something this
		// binary does not answer on.
		mountFunc(accessPublic, roleAny, "GET "+protectedResourceMetadataPath+"/mcp", s.handleProtectedResourceMetadata)
	}
	if s.oidc != nil {
		// The federated login, when an operator configured a provider
		// (ADR-0210). Both halves are public because both are
		// a browser that is not signed in yet — that is the entire point of them — and
		// mounted only when there is somewhere to send it, so a server without a
		// provider answers 404 rather than redirecting nowhere.
		//
		// The callback is safe without a principal for the reasons oidclogin.go gives:
		// it is refused unless the state matches this browser, the state was one this
		// server minted and has not spent, and the token the code buys survives every
		// check in oidctoken.go.
		mountFunc(accessPublic, roleAny, "GET "+oidcStartPath, s.handleOIDCStart)
		mountFunc(accessPublic, roleAny, "GET "+oidcCallbackPath, s.handleOIDCCallback)
	}

	// Every /api/v1 route is registered from the single-source-of-truth route
	// table, the same list openapiDoc describes, so the served surface and its
	// OpenAPI spec cannot drift (ADR-0043). Each one is gated unless
	// publicAPIRoutes names it.
	for _, r := range s.apiRoutes() {
		route := r.method + " " + r.pattern
		class := apiRouteAccess(route)
		if s.traceRoutes {
			// The span is named for the *pattern*, which is fixed by this table, so the
			// set of span names is bounded by the code rather than by traffic (ADR-0142).
			mount(class, r.op.role, route, tracing.Handler(route, r.handler))
			continue
		}
		mountFunc(class, r.op.role, route, r.handler)
	}

	// The MCP transport, when the binary supplied one (ADR-0016). It is mounted
	// here, inside this handler, rather than beside it: an adapter that drives the
	// whole API is exactly the surface that must not sit outside the boundary, and
	// wiring is not a place where that decision should be makeable
	// (ADR-0196).
	if s.mcpHandler != nil {
		transport := s.mcpTransport()
		mount(accessAuthenticated, roleAny, "/mcp", transport)
		mount(accessAuthenticated, roleAny, "/mcp/", transport)
	}

	// Anything under /api/v1 that no route above claimed. Without this the UI's "/"
	// catch-all would serve an unrouted API path — and, being public, would classify
	// it public on the way in, which is the shape of the bug this whole boundary is
	// about. Registering it keeps such a path gated, and answers it in the API's own
	// error envelope rather than with the file server's 404.
	mountFunc(accessAuthenticated, roleAny, "/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.Error(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	})

	// The OpenAPI document and the Scalar API explorer are gated behind --docs:
	// the explorer's "Try it out" exercises the same mutating surface as the API,
	// so an operator opts in explicitly (ADR-0043). The vendored Scalar asset is
	// served by the file server below at /vendor/scalar/.
	//
	// They are also behind the login. Nothing on the login screen reads either, and
	// the explorer is a developer surface that drives the whole API — the same
	// argument --docs already makes, one step further, now that requiring a login is
	// the default rather than the exception (ADR-0195). Gating
	// them together matters: an explorer whose document is refused is a page that
	// renders and then cannot load what it is for.
	if s.docsEnabled {
		mountFunc(accessAuthenticated, roleAny, "GET /api/v1/openapi.json", s.handleOpenAPI)
		mountFunc(accessAuthenticated, roleAny, "GET /api/docs", s.handleDocs)
		mountFunc(accessAuthenticated, roleAny, "GET /api/docs/", s.handleDocs)
	}

	// Public, unauthenticated share links (ADR-0029/0143). These expose exactly one
	// thing each: the start form, or the documentation version, that one token
	// names. The token in the URL is the whole authorization, and the handlers rate
	// limit them.
	mountFunc(accessPublic, roleAny, "GET "+processdoc.PublicPath+"{token}", s.processDocs.HandlePublic)
	mountFunc(accessPublic, roleAny, "GET /public/forms/{token}", s.handlePublicFormPage)
	mountFunc(accessPublic, roleAny, "GET /public/forms/{token}/schema", s.handlePublicFormSchema)
	mountFunc(accessPublic, roleAny, "POST /public/forms/{token}/start", s.handlePublicFormStart)
	// CORS preflight for the two JSON endpoints above, so an allow-listed external
	// site can embed the start form as a custom widget (ADR-0186). A cross-origin
	// GET /schema is a "simple" request needing no preflight (its handler sets the
	// header); a POST /start of application/json is preflighted, so it needs this.
	// A preflight carries no credentials by definition, so it is public with them.
	mountFunc(accessPublic, roleAny, "OPTIONS /public/forms/{token}/schema", s.handlePublicFormPreflight)
	mountFunc(accessPublic, roleAny, "OPTIONS /public/forms/{token}/start", s.handlePublicFormPreflight)

	// The embedded UI is the catch-all; the more specific patterns above win under
	// net/http's precedence rules. Static assets, and the login screen has to load.
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// webFS is compiled in, so this only fails on a broken build.
		panic("api: embedded web assets missing: " + err.Error())
	}
	mount(accessPublic, roleAny, "/", revalidated(sub, http.FileServerFS(sub)))

	return mux, policy
}

// readStats reads the live instance counts. It must be called on the run-loop
// goroutine (inside do).
func (s *Server) readStats() (statsResp, error) {
	pi, err := s.store.ActiveProcessInstanceCount()
	if err != nil {
		return statsResp{}, err
	}
	ei, err := s.store.ActiveElementInstanceCount()
	if err != nil {
		return statsResp{}, err
	}
	inc, err := s.store.IncidentCount()
	if err != nil {
		return statsResp{}, err
	}
	return statsResp{ActiveProcessInstances: pi, ActiveElementInstances: ei, UnresolvedIncidents: inc}, nil
}

// webETags maps each embedded UI file to a strong ETag over its bytes, built once at
// startup. The files are compiled in, so the map is fixed for the life of the process
// and needs no locking.
func webETags(root fs.FS) map[string]string {
	tags := map[string]string{}
	_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a file that cannot be read simply gets no tag
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		tags["/"+p] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	return tags
}

// revalidated wraps the embedded-UI file server so every asset carries a validator.
//
// Without one the browser is left to guess how long a file stays fresh, and it guesses
// per file: an upgraded server could hand a returning browser a *mixed* UI — one module
// from the new build, another still from the old — and the app died on an import of
// something the stale half did not export yet. An embedded file has a zero modtime, so
// http.ServeContent omits Last-Modified and the responses had no validator at all.
//
// Each file gets a strong ETag over its own bytes and `Cache-Control: no-cache`, which
// means "reuse it, but ask first" rather than "do not store it": the browser keeps the
// copy and revalidates, and an unchanged file costs a 304 with no body. Because the
// header is set before the file server runs, http.ServeContent does the If-None-Match
// comparison itself. A path with no tag (nothing matches it in the FS) is passed through
// untouched — the file server will answer 404 for it.
func revalidated(root fs.FS, next http.Handler) http.Handler {
	tags := webETags(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// The SPA shell is served for a bare directory path, so a request for "/" (or
		// any directory) is answered with the index inside it.
		if strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		if tag, ok := tags[p]; ok {
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}
