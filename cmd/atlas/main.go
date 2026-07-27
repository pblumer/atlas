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
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/script"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
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
			log.Fatalf("atlas: %v", err)
		}
	case "mcp":
		if err := runMCP(args); err != nil {
			log.Fatalf("atlas mcp: %v", err)
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
  atlas serve [flags]   Run the engine, HTTP API, and web UI (default)
  atlas mcp   [flags]   Run the Model Context Protocol adapter on stdio

Run "atlas <command> -h" for the flags of a command.
`)
}

// runServe boots the engine behind the HTTP API and web UI.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	dataDir := fs.String("data-dir", "atlas-data", "directory for the write-ahead log and state store")
	shutdownTimeout := fs.Duration("shutdown-timeout", 10*time.Second, "grace period for in-flight requests on shutdown")
	docs := fs.Bool("docs", true, "serve the OpenAPI spec (/api/v1/openapi.json) and the Scalar API explorer (/api/docs); pass --docs=false to disable")
	auth := fs.Bool("auth", false, "require login for the API and UI; seeds an admin from ATLAS_ADMIN_USERNAME/ATLAS_ADMIN_PASSWORD on first run")
	powershell := fs.Bool("powershell", true, "run PowerShell script tasks by shelling out to pwsh; on by default, --powershell=false to disable (executes arbitrary interpreter code)")
	python := fs.Bool("python", true, "run Python script tasks by shelling out to python3; on by default, --python=false to disable (executes arbitrary interpreter code)")
	javascript := fs.Bool("javascript", true, "run JavaScript script tasks by shelling out to node; on by default, --javascript=false to disable (executes arbitrary interpreter code)")
	scriptTimeout := fs.Duration("script-timeout", 30*time.Second, "wall-clock limit for a single script task in any language; an overrunning script is killed and its job left pending")
	if err := fs.Parse(args); err != nil {
		return err
	}
	enabled := map[string]bool{"powershell": *powershell, "python": *python, "javascript": *javascript}
	return serve(*addr, *dataDir, *shutdownTimeout, *docs, *auth, enabled, *scriptTimeout)
}

func serve(addr, dataDir string, shutdownTimeout time.Duration, docs, auth bool, scriptLangs map[string]bool, scriptTimeout time.Duration) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	// Open durable stores. The wal is the source of truth; the store is its
	// materialization, caught up on Recover below.
	log.Printf("opening data directory %s", dataDir)
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

	// One partition for now (single-node). Recover replays the log into the
	// store before we accept traffic.
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		return err
	}

	var apiOpts []api.Option
	if !docs {
		apiOpts = append(apiOpts, api.WithoutDocs())
	}
	if auth {
		apiOpts = append(apiOpts, api.WithAuth())
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
			log.Printf("WARNING: %s script worker enabled but %q was not found on PATH (%v); %s script tasks will park until it is installed", lang.Name, lang.Bin, err, lang.Name)
		} else {
			log.Printf("%s script worker enabled (%s found on PATH)", lang.Name, lang.Bin)
		}
		apiOpts = append(apiOpts, api.WithScriptWorker(lang.JobType, ex))
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
		log.Printf("listening on %s (UI at %s/, MCP at %s/mcp)", addr, base, base)
		if docs {
			log.Printf("API explorer at %s/api/docs (OpenAPI at %s/api/v1/openapi.json)", base, base)
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
		log.Printf("shutting down")
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
	log.SetOutput(os.Stderr)
	log.Printf("atlas mcp: proxying to %s (stdio)", *server)

	s := mcp.NewServer(mcp.NewClient(*server))
	return s.Serve(os.Stdin, os.Stdout)
}
