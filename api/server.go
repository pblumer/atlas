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
// remains: there is no job-worker HTTP surface yet (that follows the gRPC job
// protocol, ADR-0007), so an instance parks at its service task — which is
// exactly the "waiting token" the live viewer will show.
package api

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

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
	deployments map[uint64]*deployment
	order       []uint64 // deployment keys in registration order, for stable listing
	nextKey     uint64
	versions    map[string]int32 // bpmnProcessId → highest version deployed
	deploys     *deployStore     // durable sidecar for deployments (ADR-0019)
	drafts      *draftStore      // durable sidecar for saved-but-not-deployed diagrams
	projects    *projectStore    // durable sidecar for projects grouping artifacts (ADR-0034)
	dmnrefs     *dmnRefStore     // durable sidecar for DMN reference artifacts (ADR-0034)

	// dmnResolver turns a DMN reference handle into model XML; dmnValidator wraps
	// it with a temis compile for the deploy-time validation gate (ADR-0034).
	dmnResolver  dmn.Resolver
	dmnValidator *dmn.Validator

	// dmnRegistry holds the compiled DMN model for each deployed process (keyed by
	// def key); jobRunner drives the in-process DMN worker that evaluates business
	// rule tasks (ADR-0014). Both are touched only on the run-loop goroutine.
	dmnRegistry *dmn.Registry
	jobRunner   *job.Runner
}

// New builds a Server over an already-recovered processor and its store and
// starts the run-loop goroutine. dataDir is the base data directory; the durable
// deployment and draft sidecar stores live in its "deployments" and "drafts"
// subdirectories (ADR-0019). New reloads any deployments found there,
// re-registering them with the processor so recovered instances resolve their
// definition and the UI can render diagrams again. The caller retains ownership
// of proc and store (Close here stops only the loop, not the engine).
func New(proc *engine.Processor, store *state.Store, dataDir string) (*Server, error) {
	ds, err := newDeployStore(filepath.Join(dataDir, "deployments"))
	if err != nil {
		return nil, err
	}
	drafts, err := newDraftStore(filepath.Join(dataDir, "drafts"))
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
	// DMN reference models are resolved from <data-dir>/dmn-models, a folder of
	// temis-exported models. The Resolver interface lets a temis git/service
	// source replace this later without touching callers (ADR-0034).
	resolver := dmn.DirResolver{Dir: filepath.Join(dataDir, "dmn-models")}
	s := &Server{
		proc:         proc,
		store:        store,
		tasks:        make(chan func()),
		quit:         make(chan struct{}),
		deployments:  map[uint64]*deployment{},
		nextKey:      1,
		versions:     map[string]int32{},
		deploys:      ds,
		drafts:       drafts,
		projects:     projects,
		dmnrefs:      dmnrefs,
		dmnResolver:  resolver,
		dmnValidator: dmn.NewValidator(resolver),
		dmnRegistry:  dmn.NewRegistry(),
	}
	// The in-process DMN worker evaluates business rule tasks off no separate
	// goroutine (the single-binary server drives jobs synchronously on the run
	// loop). One handler serves every process: it resolves each job's decision and
	// static inputs from the compiled process the job belongs to (ProcessLookup),
	// so it registers once under the reserved DMN job type (compiler.DMNJobTypeIndex).
	s.jobRunner = job.NewRunner(store, proc)
	s.jobRunner.Handle(compiler.DMNJobTypeIndex, dmn.Handler(store, s.processLookup, s.dmnRegistry, nil))
	if err := s.loadDeployments(); err != nil {
		return nil, err
	}
	s.wg.Add(2)
	go s.loop()
	go s.timerScheduler(time.Second)
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
		// Re-register the process's DMN model so its business rule tasks evaluate
		// after a restart, exactly as they did when first deployed (ADR-0014). The
		// model is snapshotted in the deployment record, so no temis reference has
		// to be re-resolved here.
		if rec.DMNXML != "" {
			if err := s.dmnRegistry.Deploy(rec.Key, []byte(rec.DMNXML)); err != nil {
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

// Handler returns the HTTP handler: JSON API under /api/v1, /healthz, and the
// embedded web UI at the root.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /api/v1/info", s.handleInfo)
	mux.HandleFunc("POST /api/v1/feel/validate", s.handleValidateFeel)
	mux.HandleFunc("POST /api/v1/feel/evaluate", s.handleEvaluateFeel)
	mux.HandleFunc("POST /api/v1/deployments", s.handleDeploy)
	mux.HandleFunc("GET /api/v1/processes", s.handleListProcesses)
	mux.HandleFunc("POST /api/v1/drafts", s.handleSaveDraft)
	mux.HandleFunc("GET /api/v1/drafts", s.handleListDrafts)
	mux.HandleFunc("GET /api/v1/drafts/{id}/xml", s.handleDraftXML)
	mux.HandleFunc("PATCH /api/v1/drafts/{id}", s.handleMoveDraft)
	mux.HandleFunc("DELETE /api/v1/drafts/{id}", s.handleDeleteDraft)
	mux.HandleFunc("POST /api/v1/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", s.handleRenameProject)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.handleDeleteProject)
	mux.HandleFunc("POST /api/v1/dmnrefs", s.handleCreateDmnRef)
	mux.HandleFunc("GET /api/v1/dmnrefs", s.handleListDmnRefs)
	mux.HandleFunc("PATCH /api/v1/dmnrefs/{id}", s.handleMoveDmnRef)
	mux.HandleFunc("DELETE /api/v1/dmnrefs/{id}", s.handleDeleteDmnRef)
	mux.HandleFunc("POST /api/v1/dmnrefs/{id}/validate", s.handleValidateDmnRef)
	mux.HandleFunc("POST /api/v1/projects/{id}/validate", s.handleValidateProject)
	mux.HandleFunc("POST /api/v1/projects/{id}/deploy", s.handleDeployProject)
	mux.HandleFunc("GET /api/v1/processes/{key}/xml", s.handleProcessXML)
	mux.HandleFunc("DELETE /api/v1/processes/{key}", s.handleDeleteProcess)
	mux.HandleFunc("GET /api/v1/processes/{key}/runtime", s.handleProcessRuntime)
	mux.HandleFunc("GET /api/v1/collaborations/{key}/runtime", s.handleCollaborationRuntime)
	mux.HandleFunc("POST /api/v1/processes/{key}/instances", s.handleCreateInstance)
	mux.HandleFunc("GET /api/v1/instances", s.handleListInstances)
	mux.HandleFunc("DELETE /api/v1/instances/{key}", s.handleCancelInstance)
	mux.HandleFunc("POST /api/v1/messages", s.handlePublishMessage)
	mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/tasks/{key}/complete", s.handleCompleteTask)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)

	// The embedded UI is the catch-all; the more specific API patterns above win
	// under net/http's precedence rules.
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// webFS is compiled in, so this only fails on a broken build.
		panic("api: embedded web assets missing: " + err.Error())
	}
	mux.Handle("/", http.FileServerFS(sub))

	return mux
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
