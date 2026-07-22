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
// read stats, health, and a static UI shell. Two honest limitations for now:
// deployments are held in memory and are lost on restart (durable deployment
// waits on the Milestone 4 public API), and there is no job-worker HTTP surface
// yet (that follows the gRPC job protocol, ADR-0007), so an instance parks at its
// service task — which is exactly the "waiting token" the live viewer will show.
package api

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
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
}

// New builds a Server over an already-recovered processor and its store and
// starts the run-loop goroutine. The caller retains ownership of proc and store
// (Close here stops only the loop, not the engine).
func New(proc *engine.Processor, store *state.Store) *Server {
	s := &Server{
		proc:        proc,
		store:       store,
		tasks:       make(chan func()),
		quit:        make(chan struct{}),
		deployments: map[uint64]*deployment{},
		nextKey:     1,
		versions:    map[string]int32{},
	}
	s.wg.Add(1)
	go s.loop()
	return s
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

// StartHistorySweep launches a background ticker that periodically asks the
// engine to purge expired finished instances (ADR-0018). The retention window
// itself is configured on the processor (SetHistoryRetention); this only drives
// the sweep on a cadence so history is bounded even without new activity. A
// non-positive interval is a no-op. The ticker stops with the server (Close).
func (s *Server) StartHistorySweep(interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.quit:
				return
			case <-t.C:
				s.do(func() {
					s.proc.PurgeExpiredHistory()
					if err := s.proc.RunUntilIdle(); err != nil {
						// A purge failure is not fatal to serving; the next tick
						// retries. Surface it for operators without crashing.
						log.Printf("history sweep: %v", err)
					}
				})
			}
		}
	}()
}

// Close stops the run-loop goroutine (and the history sweeper, if started). It
// does not close the processor, log, or store — the caller owns those.
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
	mux.HandleFunc("POST /api/v1/deployments", s.handleDeploy)
	mux.HandleFunc("GET /api/v1/processes", s.handleListProcesses)
	mux.HandleFunc("GET /api/v1/processes/{key}/xml", s.handleProcessXML)
	mux.HandleFunc("DELETE /api/v1/processes/{key}", s.handleDeleteProcess)
	mux.HandleFunc("GET /api/v1/processes/{key}/runtime", s.handleProcessRuntime)
	mux.HandleFunc("POST /api/v1/processes/{key}/instances", s.handleCreateInstance)
	mux.HandleFunc("GET /api/v1/instances", s.handleListInstances)
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
