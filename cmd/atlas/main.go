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
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/opensearch"
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
	case "version", "-v", "--version":
		printVersion(os.Stdout)
	case "reset-password":
		if err := runResetPassword(args); err != nil {
			log.Fatalf("atlas reset-password: %v", err)
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
  atlas serve          [flags]      Run the engine, HTTP API, and web UI (default)
  atlas mcp            [flags]      Run the Model Context Protocol adapter on stdio
  atlas reset-password [flags] USER Reset a local user's password from the shell
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
	retentionAge := fs.Duration("retention-max-age", envDuration("ATLAS_RETENTION_MAX_AGE"), "hard-delete finished process instances older than this once their events are exported (ADR-0115), e.g. 720h; 0 disables retention")
	// Recovery checkpoints (ADR-0131): on by default, because bounded restart time is
	// the point of them. They only ever add a shortcut — the WAL stays the source of
	// truth and a missing or unusable checkpoint just means a full replay — so unlike
	// the exporter and retention there is nothing here to opt into.
	checkpointInterval := fs.Duration("checkpoint-interval", 5*time.Minute, "how often to snapshot applied state so a restart replays only the log past it (ADR-0131); 0 disables checkpointing")
	checkpointKeep := fs.Int("checkpoint-keep", 3, "how many recovery checkpoints to keep; each pins the state files it captured, so this bounds their disk (ADR-0131)")
	// WAL compaction (ADR-0131): opt-in, off by default. Unlike checkpointing it deletes
	// data, so — like history retention (ADR-0115) — an operator turns it on deliberately.
	compactWAL := fs.Bool("compact-wal", false, "delete WAL segments already covered by a recovery checkpoint and every consumer watermark (ADR-0131), bounding the log's disk; off by default because it is irreversible. Requires checkpointing")
	// Prometheus metrics (ADR-0142): on by default. The exposition carries only
	// bounded-cardinality aggregates, so the cost of having it is a path an operator may
	// not want reachable rather than data leaking.
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
	return serve(*addr, *dataDir, *shutdownTimeout, *docs, *auth, *vault, *userProvisioning, enabled, *scriptTimeout, osCfg, *retentionAge, *checkpointInterval, *checkpointKeep, *compactWAL, *metricsOn)
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
func envDuration(key string) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0
}

func serve(addr, dataDir string, shutdownTimeout time.Duration, docs, auth, vault, userProvisioning bool, scriptLangs map[string]bool, scriptTimeout time.Duration, osExport opensearch.Config, retentionMaxAge time.Duration, checkpointInterval time.Duration, checkpointKeep int, compactWAL, metricsOn bool) error {
	// Tee the process log into a bounded in-memory buffer, exposed at
	// GET /api/v1/logs, so an operator can read recent server logs from the web UI
	// without shell access. Set before the first log line so startup is captured.
	logs := api.NewLogBuffer(2000)
	log.SetOutput(io.MultiWriter(os.Stderr, logs))

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
		log.Printf("applied a staged full-snapshot restore; state will be rebuilt from the restored WAL")
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
	if !docs {
		apiOpts = append(apiOpts, api.WithoutDocs())
	}
	if !metricsOn {
		apiOpts = append(apiOpts, api.WithoutMetrics())
	}
	// Mirror the durable event log into OpenSearch when configured (ADR-0114).
	if osExport.Enabled() {
		apiOpts = append(apiOpts, api.WithOpenSearchExporter(osExport))
		log.Printf("opensearch exporter enabled: indexing events into %s at %s", osExport.Index, osExport.URL)
	}
	// Hard-delete finished-instance history past the max age, gated on export (ADR-0115).
	if retentionMaxAge > 0 {
		apiOpts = append(apiOpts, api.WithRetention(retentionMaxAge))
		gate := "durable position"
		if osExport.Enabled() {
			gate = "OpenSearch export"
		}
		log.Printf("history retention enabled: purging finished instances older than %s, gated on %s", retentionMaxAge, gate)
	}
	// Periodic recovery checkpoints keep restart time a function of the cadence rather
	// than of the whole log's length (ADR-0131). Nothing is deleted by them.
	if checkpointInterval > 0 {
		apiOpts = append(apiOpts, api.WithCheckpoints(checkpointInterval), api.WithCheckpointRetention(checkpointKeep))
		log.Printf("recovery checkpoints enabled: snapshotting applied state every %s, keeping %d", checkpointInterval, checkpointKeep)
		// Compaction rides the checkpoint tick, so it needs one to ride (ADR-0131).
		if compactWAL {
			apiOpts = append(apiOpts, api.WithWALCompaction())
			log.Printf("wal compaction enabled: deleting segments already covered by a checkpoint and every consumer watermark")
		}
	} else if compactWAL {
		log.Printf("WARNING: --compact-wal has no effect without checkpointing; set --checkpoint-interval to a positive duration to enable it (ADR-0131)")
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
		if metricsOn {
			log.Printf("Prometheus metrics at %s/metrics (unauthenticated; proxy it if exposed beyond the host)", base)
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

// runResetPassword sets a local user's password directly against the on-disk user
// store, without a running server or a login. It is the operator recovery path
// for a self-hosted, admin-managed instance whose admin is locked out — there is
// no self-service reset and the MCP adapter is not an admin (ADR-0044/0049), so
// recovery has to be reachable from a shell (e.g. `docker exec … reset-password`).
//
// By default it generates a strong password and prints it once; --password-stdin
// reads one from stdin instead, keeping the secret out of the process arguments
// (where `ps` or shell history would expose it).
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
	log.Printf("atlas: %s user %q (id %s)", verb, res.Username, res.UserID)
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
