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
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/clio"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/mail"
	"github.com/pblumer/atlas/rest"
	"github.com/pblumer/atlas/script"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/temis"
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
func connectorEnvKey(name string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
		} else {
			pendingSep = true
		}
	}
	return b.String()
}

//go:embed web
var webFS embed.FS

// Version is the Atlas product version reported to the UI. It is UI/display
// metadata only and unrelated to a deployment's process version.
const Version = "0.1.0-dev"

// deployment is the server-side record of a deployed definition. The compiled
// process itself lives in the processor; here we keep the metadata the UI needs
// plus the original XML so the viewer can render it. DeployedAt is server-side
// display metadata (wall-clock at deploy time), not engine state.
type deployment struct {
	Key        uint64
	ProcessID  string
	Name       string // human-readable <process name="…">, for display
	Version    int32
	DeployedAt int64 // unix seconds, for the UI's "last changed" column
	xml        []byte
	cp         *compiler.CompiledProcess // for the live overlay's element-id mapping
}

// Server hosts the engine behind an HTTP surface. Construct it with New, mount
// Handler on an http.Server, and call Close to stop the run loop.
type Server struct {
	proc  *engine.Processor
	store *state.Store

	// tasks carries closures to the single run-loop goroutine that owns the
	// processor; quit stops that goroutine.
	tasks chan func()
	quit  chan struct{}
	wg    sync.WaitGroup

	// The fields below are touched only on the run-loop goroutine (via do), so
	// they need no locking — the same single-owner discipline as process state.
	deployments      map[uint64]*deployment
	order            []uint64 // deployment keys in registration order, for stable listing
	nextKey          uint64
	versions         map[string]int32     // bpmnProcessId → highest version deployed
	deploys          *deployStore         // durable sidecar for deployments (ADR-0019)
	drafts           *draftStore          // durable sidecar for saved-but-not-deployed diagrams
	forms            *formStore           // durable sidecar for form definitions (ADR-0028)
	publicLinks      *publicLinkStore     // durable sidecar for public start links (ADR-0029)
	publicRate       *rateLimiter         // throttles the unauthenticated public endpoints
	projects         *projectStore        // durable sidecar for projects grouping artifacts (ADR-0034)
	dmnrefs          *dmnRefStore         // durable sidecar for DMN reference artifacts (ADR-0034)
	connectors       *connectorStore      // durable sidecar for managed connector instances (ADR-0041)
	marketplace      []marketplacePackage // curated, bundled marketplace catalog, immutable after New (ADR-0081)
	marketplaceStore *marketplaceStore    // durable sidecar for installed marketplace templates (ADR-0081)
	vault            *secretVault         // engine-internal encrypted secret store, nil when disabled (ADR-0069/0070)
	vaultEnabled     bool                 // whether to build the vault; on by default, off via WithoutVault (ADR-0070)
	users            *userStore           // durable sidecar for user accounts (ADR-0044)

	// sessions holds live login sessions in memory. Unlike the sidecar stores it
	// is touched from concurrent handler goroutines, so it guards itself with a
	// mutex; it is not engine state and does not persist (ADR-0044).
	sessions *sessionStore

	// collab holds live collaborative-editing sessions on drafts in memory
	// (ADR-0103). Like sessions it is reached from concurrent handler goroutines
	// (SSE streams and POSTs), guards itself with a mutex, and never persists or
	// touches the engine — it is design-time coordination around a draft, not
	// engine state.
	collab *collabRegistry

	// authEnabled gates authentication enforcement. Off by default: the server
	// stays fully open (single-user) until an operator opts in with WithAuth,
	// mirroring how docsEnabled gates the API explorer (ADR-0044/0043). Set once
	// before Handler is mounted; read-only thereafter.
	authEnabled bool

	// internalToken is a random secret minted at startup when auth is enabled. A
	// trusted in-process component (the MCP adapter) presents it as a bearer token
	// so its loopback API calls authenticate without a login (ADR-0049). It
	// resolves to a non-admin service principal. Empty when auth is off.
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

	// mailRegistry resolves a connector name to a mail-provider client for outbound
	// mail connector tasks (ADR-0079), built from the managed connector store at
	// startup and rebuilt on every connector change, with each provider's credential
	// resolved from the vault (ADR-0041). Read only while driving jobs on the run
	// loop, so it needs no lock.
	mailRegistry *mail.Registry

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

// WithInboundPollInterval sets the clio inbound bridge's poll cadence (ADR-0075).
// A non-positive interval disables the bridge (useful in tests that drive it
// directly). The default is 2s.
func WithInboundPollInterval(d time.Duration) Option {
	return func(s *Server) { s.inboundPoll = d }
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
	dmnrefs, err := newDmnRefStore(filepath.Join(dataDir, "dmnrefs"))
	if err != nil {
		return nil, err
	}
	users, err := newUserStore(filepath.Join(dataDir, "users"))
	if err != nil {
		return nil, err
	}
	connectors, err := newConnectorStore(filepath.Join(dataDir, "connectors"))
	if err != nil {
		return nil, err
	}
	marketplaceStore, err := newMarketplaceStore(filepath.Join(dataDir, "marketplace"))
	if err != nil {
		return nil, err
	}
	// The marketplace catalog is curated and compiled into the binary (ADR-0081);
	// an invalid bundled package is a build error, so it fails construction here.
	marketplaceCatalog, err := loadBundledCatalog()
	if err != nil {
		return nil, err
	}
	inboundSubs, err := newInboundSubStore(filepath.Join(dataDir, "inbound-subscriptions"))
	if err != nil {
		return nil, err
	}
	// DMN reference models are resolved either from a temis model service (when
	// configured) or the zero-config <data-dir>/dmn-models folder. Both satisfy the
	// Resolver interface, so the rest of the server is unaffected (ADR-0034/0014).
	resolver := dmnResolverFromEnv(filepath.Join(dataDir, "dmn-models"))
	s := &Server{
		proc:        proc,
		store:       store,
		tasks:       make(chan func()),
		quit:        make(chan struct{}),
		deployments: map[uint64]*deployment{},
		nextKey:     1,
		versions:    map[string]int32{},
		deploys:     ds,
		drafts:      drafts,
		forms:       forms,
		publicLinks: publicLinks,
		// A public start link tolerates a modest burst then ~1 start/sec per IP;
		// generous for a human intake form, throttling for a script (ADR-0029).
		publicRate:       newRateLimiter(20, 1),
		projects:         projects,
		dmnrefs:          dmnrefs,
		connectors:       connectors,
		marketplace:      marketplaceCatalog,
		marketplaceStore: marketplaceStore,
		inboundSubs:      inboundSubs,
		inboundPoll:      2 * time.Second,     // default cadence; WithInboundPollInterval overrides, 0 disables
		inboundBatch:     defaultInboundBatch, // per-poll ReadEvents cap; WithInboundBatchLimit overrides
		vaultEnabled:     true,                // opt-out: built unless WithoutVault is passed (ADR-0070)
		users:            users,
		sessions:         newSessionStore(defaultSessionTTL),
		collab:           newCollabRegistry(),
		dmnResolver:      resolver,
		dmnValidator:     dmn.NewValidator(resolver),
		dmnRegistry:      dmn.NewRegistry(),
		docsEnabled:      true, // opt-out: served unless WithoutDocs is passed (ADR-0043)
	}
	for _, opt := range opts {
		opt(s)
	}
	// The encrypted secret vault (ADR-0069) is on by default (ADR-0070) unless
	// WithoutVault disabled it. An operator key from the environment is preferred
	// and never persisted; absent one, a key is loaded from — or generated into —
	// <data-dir>/vault.key so the vault works with no provisioning. Built after the
	// options so WithoutVault takes effect before any key file is touched.
	if s.vaultEnabled {
		key, source, err := resolveVaultKey(filepath.Join(dataDir, "vault.key"))
		if err != nil {
			return nil, err
		}
		if s.vault, err = newSecretVault(filepath.Join(dataDir, "vault"), key); err != nil {
			return nil, err
		}
		if source == "generated" {
			log.Printf("vault: generated a master key at %s (mode 0600); set ATLAS_VAULT_KEY "+
				"for a stronger at-rest posture or pass --vault=false to disable (ADR-0070)",
				filepath.Join(dataDir, "vault.key"))
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
		token, err := randomHex(32)
		if err != nil {
			return nil, err
		}
		s.internalToken = token
	}
	// The in-process DMN worker evaluates business rule tasks off no separate
	// goroutine (the single-binary server drives jobs synchronously on the run
	// loop). One handler serves every process: it resolves each job's decision,
	// inputs, and result variable from the compiled process the job belongs to
	// (ProcessLookup), so it registers once under the reserved DMN job type
	// (compiler.DMNJobTypeIndex). It registers via HandleCompleting because a
	// decision's completion both writes its result back as a process variable and
	// retains the evaluation (inputs, outputs, trace) for debugging (ADR-0066).
	s.jobRunner = job.NewRunner(store, proc)
	s.jobRunner.HandleCompleting(compiler.DMNJobTypeIndex, dmn.Handler(store, s.processLookup, s.dmnRegistry, nil))
	// Script-language workers (PowerShell, Python, JavaScript) each register under
	// their reserved job type and resolve each job's script from the compiled
	// process via processLookup, exactly like the DMN worker (ADR-0047). They run
	// arbitrary interpreter code, so only the languages an operator registered with
	// WithScriptWorker subscribe; the rest park.
	for jobType, exec := range s.scriptWorkers {
		s.jobRunner.HandleWithOutput(jobType, script.Handler(store, s.processLookup, exec))
	}
	// A *central* business rule task delegates its decision to a remote temis
	// service instead of the embedded library (ADR-0050). One connector worker
	// serves every process under the reserved temis-connector job type; it resolves
	// each job's connector name from the compiled process and calls the endpoint
	// configured for that connector. Connectors come from the environment plus
	// operator-managed instances in the Console (ADR-0041); the registry is built
	// here (before the loop serves traffic) and rebuilt on every connector change.
	// A model whose connector is not configured simply parks until it is.
	s.temisRegistry = temis.NewRegistry()
	clients, err := s.buildTemisClients()
	if err != nil {
		return nil, err
	}
	s.temisRegistry.Replace(clients)
	s.jobRunner.HandleCompleting(compiler.TemisDecisionJobTypeIndex, temis.Handler(store, s.processLookup, s.temisRegistry, nil))
	// A clio connector task appends, reads, or queries a server-registered clio
	// event store (ADR-0036). One worker per operation serves every process under a
	// reserved clio job type; each resolves its job's connector name from the
	// compiled process and calls the endpoint configured for that connector. Unlike
	// REST's model-authored endpoint, a clio connector's endpoint and token live in
	// the managed connector store; the token is resolved from the vault at build time
	// (ADR-0041). The registry is built here (before the loop serves traffic) and
	// rebuilt on every connector change; a task whose connector is not configured
	// parks until it is.
	s.clioRegistry = clio.NewRegistry()
	clioClients, err := s.buildClioClients()
	if err != nil {
		return nil, err
	}
	s.clioRegistry.Replace(clioClients)
	s.jobRunner.Handle(compiler.ClioWriteJobTypeIndex, clio.Handler(store, s.processLookup, s.clioRegistry))
	s.jobRunner.HandleWithOutput(compiler.ClioQueryJobTypeIndex, clio.QueryHandler(store, s.processLookup, s.clioRegistry))
	s.jobRunner.HandleWithOutput(compiler.ClioReadJobTypeIndex, clio.ReadHandler(store, s.processLookup, s.clioRegistry))
	// An outbound mail connector task sends a model-authored message through a
	// server-registered mail provider (ADR-0079). One worker serves every process
	// under the reserved mail job type; it resolves each job's connector name and
	// recipients/subject/body from the compiled process, sends the message off the run
	// loop and after fsync, and completes the job. The provider host and credentials
	// live in the managed connector store like clio's; the credential is resolved from
	// the vault at build time (ADR-0041), so a secret never lives in a model. The
	// registry is built here and rebuilt on every connector change; a task whose
	// connector is not configured parks until it is.
	s.mailRegistry = mail.NewRegistry()
	mailClients, err := s.buildMailClients()
	if err != nil {
		return nil, err
	}
	s.mailRegistry.Replace(mailClients)
	s.jobRunner.Handle(compiler.MailJobTypeIndex, mail.Handler(store, s.processLookup, s.mailRegistry))
	// An HTTP-REST connector task calls a model-authored endpoint (ADR-0067). One
	// worker serves every process under the reserved REST job type; it resolves each
	// job's method/url/headers/query/result-variable from the compiled process, calls
	// the API off the run loop and after fsync, and writes the JSON response into the
	// task's result variable. The endpoint and headers live in the model; a request's
	// authentication secret is a *reference* the worker resolves at call time from the
	// environment (resolveConnectorSecret, ADR-0041), so a token never lives in a model.
	s.jobRunner.HandleWithOutput(compiler.RestJobTypeIndex, rest.Handler(store, s.processLookup, rest.NewHTTPClient(), s.resolveConnectorSecret))
	// A CSV-import service task parses an uploaded CSV (a `csvText` variable) against
	// a `columnConfig` layout into a `rows` collection, in-process, so a batch of
	// records is ingested and validated on the engine with the file arriving through a
	// user-task form rather than a side-channel endpoint (ADR-0087). One worker serves
	// every process under the reserved CSV-import job type.
	s.jobRunner.HandleWithOutput(compiler.CsvImportJobTypeIndex, csvImportHandler(store))
	if err := s.loadDeployments(); err != nil {
		return nil, err
	}
	s.wg.Add(3)
	go s.loop()
	go s.timerScheduler(time.Second)
	// The collaboration reaper evicts idle detached session participants (MCP
	// agents that stopped polling) and releases their locks (ADR-0103). It runs off
	// the run loop — the collab registry is its own mutex-guarded, engine-independent
	// state — so it never touches the processor or the invariants.
	go s.collabReaper(collabReapInterval)
	// The clio inbound bridge polls configured subscriptions and republishes new
	// clio events as Atlas messages (ADR-0075). It is a separate goroutine like the
	// timer scheduler — it does its network reads off the run loop and hands only the
	// resulting publish onto it (invariant I3). A non-positive interval disables it.
	if s.inboundPoll > 0 {
		s.wg.Add(1)
		go s.inboundBridge(s.inboundPoll)
	}
	return s, nil
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

// loadDeployments rebuilds the in-memory deployment registry and re-registers
// each definition with the processor from the durable store, so a restart
// restores diagrams, names, versions, and the ability to advance recovered
// instances (ADR-0019). It runs before the loop serves traffic, so touching the
// registry and the processor directly here respects the single-writer invariant.
func (s *Server) loadDeployments() error {
	recs, err := s.deploys.loadAll()
	if err != nil {
		return err
	}
	for _, rec := range recs {
		// Recompile exactly the process this record represents (a collaboration's
		// XML holds several), keyed as originally assigned (ADR-0019/0022).
		cp, err := compiler.ParseNamed(rec.Key, rec.Version, bytes.NewReader([]byte(rec.XML)), rec.ProcessID)
		if err != nil {
			// A stored model that no longer compiles is a hard, actionable error
			// rather than a silently dropped definition (ADR-0019).
			return fmt.Errorf("api: reload deployment %d (%s v%d): %w", rec.Key, rec.ProcessID, rec.Version, err)
		}
		cp.Version = rec.Version
		s.proc.Deploy(cp)
		// Re-register the process's DMN models so its business rule tasks evaluate
		// after a restart, exactly as they did when first deployed (ADR-0014). The
		// models are snapshotted in the deployment record (a legacy record carries a
		// single model), so no temis reference has to be re-resolved here.
		for _, dmnXML := range rec.dmnModels() {
			if err := s.dmnRegistry.Deploy(rec.Key, []byte(dmnXML)); err != nil {
				return fmt.Errorf("api: reload dmn model for def %d (%s): %w", rec.Key, rec.ProcessID, err)
			}
		}
		s.deployments[rec.Key] = &deployment{
			Key:        rec.Key,
			ProcessID:  rec.ProcessID,
			Name:       rec.Name,
			Version:    rec.Version,
			DeployedAt: rec.DeployedAt,
			xml:        []byte(rec.XML),
			cp:         cp,
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
			// Fire due timers, then drive any jobs they unblocked (e.g. a timer
			// leading into a business rule task) to completion.
			s.do(func() {
				if err := s.proc.TickTimers(); err != nil {
					return
				}
				_ = s.jobRunner.Drive()
			})
		}
	}
}

// collabReaper periodically evicts detached collaboration-session participants
// (AI agents that joined over MCP and stopped polling) that have gone silent past
// the TTL, releasing their locks so a crashed or forgotten agent never holds an
// element forever (ADR-0103). Browser SSE participants are reaped on disconnect
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
			if n := s.collab.reap(); n > 0 {
				log.Printf("collab: reaped %d idle session participant(s) past the %s TTL (ADR-0103)", n, collabParticipantTTL)
			}
		}
	}
}

// loop is the single owner of the processor. Every processor and registry access
// funnels through here, so the single-writer invariant holds even though HTTP
// handlers are concurrent.
func (s *Server) loop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.quit:
			return
		case fn := <-s.tasks:
			fn()
		}
	}
}

// do runs fn on the run-loop goroutine and blocks until it completes. If the
// server is closing, fn does not run and do returns immediately; callers must
// treat their result variables' zero values as "not produced".
func (s *Server) do(fn func()) {
	done := make(chan struct{})
	select {
	case s.tasks <- func() { defer close(done); fn() }:
		<-done
	case <-s.quit:
	}
}

// Close stops the run-loop goroutine. It does not close the processor, log, or
// store — the caller owns those.
func (s *Server) Close() {
	close(s.quit)
	s.wg.Wait()
}

// Handler returns the HTTP handler: JSON API under /api/v1, /healthz, the
// embedded web UI at the root, and — when docs are enabled (WithDocs) — the
// OpenAPI document at /api/v1/openapi.json and the Scalar API explorer at
// /api/docs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Every /api/v1 route is registered from the single-source-of-truth route
	// table, the same list openapiDoc describes, so the served surface and its
	// OpenAPI spec cannot drift (ADR-0043).
	for _, r := range s.apiRoutes() {
		mux.HandleFunc(r.method+" "+r.pattern, r.handler)
	}

	// The OpenAPI document and the Scalar API explorer are gated behind --docs:
	// the explorer's "Try it out" exercises the same unauthenticated, mutating
	// surface as the API, so an operator opts in explicitly (ADR-0043). The
	// vendored Scalar asset is served by the file server below at /vendor/scalar/.
	if s.docsEnabled {
		mux.HandleFunc("GET /api/v1/openapi.json", s.handleOpenAPI)
		mux.HandleFunc("GET /api/docs", s.handleDocs)
		mux.HandleFunc("GET /api/docs/", s.handleDocs)
	}

	// Public, unauthenticated start links (ADR-0029). These live under /public/,
	// outside the /api/v1 surface auth gates, and expose exactly one thing: the
	// start form for one token. They are rate-limited in the handlers.
	mux.HandleFunc("GET /public/forms/{token}", s.handlePublicFormPage)
	mux.HandleFunc("GET /public/forms/{token}/schema", s.handlePublicFormSchema)
	mux.HandleFunc("POST /public/forms/{token}/start", s.handlePublicFormStart)

	// The embedded UI is the catch-all; the more specific API patterns above win
	// under net/http's precedence rules.
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// webFS is compiled in, so this only fails on a broken build.
		panic("api: embedded web assets missing: " + err.Error())
	}
	mux.Handle("/", http.FileServerFS(sub))

	// Resolve a Principal for every request and, when enforcement is on, gate the
	// mutating /api/v1 surface behind a valid session (ADR-0044). With auth off
	// (the default) this is a transparent pass-through.
	return s.withAuth(mux)
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
	return statsResp{ActiveProcessInstances: pi, ActiveElementInstances: ei}, nil
}
