package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

// Version is the atlasd build version. It is overridable at link time
// (-ldflags "-X github.com/pblumer/atlas/server.Version=...").
var Version = "0.0.0-dev"

//go:embed web
var webFS embed.FS

// Server wires the atlasd HTTP surfaces described by a Config. It does not own a
// running engine yet; the API handlers are honest placeholders until the public
// query API (Milestone 4) lands. Construct one with New.
type Server struct {
	cfg Config
	log *slog.Logger
	mux *http.ServeMux
}

// New builds a Server from cfg. If log is nil, the default slog logger is used.
// Handlers are registered eagerly so which surfaces are enabled is decided once,
// here, from the Config.
func New(cfg Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{cfg: cfg, log: log, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the composed HTTP handler. Exposed for tests and for callers
// that want to embed atlasd's surfaces in their own server.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// Core, always on: liveness and the public API surface. Only the web and
	// MCP surfaces are opt-out (ADR-0012); the API is the shared substrate.
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/info", s.handleInfo)

	// Runtime query API — placeholders until the engine query surface exists
	// (ADR-0011 phasing; Milestone 4). They return 501 so the shape is visible
	// and callers get an honest signal rather than fabricated data.
	for _, p := range []string{"instances", "incidents", "jobs"} {
		s.mux.HandleFunc("GET /api/v1/"+p, s.notImplemented(p+" query API is pending the engine query surface (Milestone 4)"))
	}

	if s.cfg.MCP {
		s.mux.HandleFunc("/mcp", s.notImplemented("MCP endpoint is a placeholder; disable with ATLAS_MCP=off"))
	}

	if s.cfg.Web {
		sub, err := fs.Sub(webFS, "web")
		if err != nil {
			// The embed path is a compile-time constant; a failure here is a
			// programmer error, not a runtime condition.
			panic(err)
		}
		// Method-agnostic catch-all: a method-specific "GET /" would conflict
		// with the more path-specific "/mcp" under Go's routing rules. The file
		// server answers non-GET/HEAD with 405 on its own.
		s.mux.Handle("/", http.FileServerFS(sub))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "atlas",
		"version": Version,
		"surfaces": map[string]bool{
			"web": s.cfg.Web,
			"mcp": s.cfg.MCP,
		},
	})
}

func (s *Server) notImplemented(detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"status": "not_implemented",
			"detail": detail,
		})
	}
}

// Run starts the HTTP server and blocks until ctx is cancelled, then shuts down
// gracefully. It returns the first non-nil error from serving or shutdown.
func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("atlasd listening",
			"addr", s.cfg.Addr, "web", s.cfg.Web, "mcp", s.cfg.MCP, "version", Version)
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.log.Info("atlasd shutting down")
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
