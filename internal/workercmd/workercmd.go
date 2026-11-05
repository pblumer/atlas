// Package workercmd is the out-of-process worker's command line: the flags, the
// environment it reads, and the run loop around workersdk. Both `atlas worker` and
// the separate `atlas-workers` executable run it (ADR-0535, migration step 2), so a
// worker the server supervises behaves the same whichever binary it is.
package workercmd

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/internal/sysproxy"
	"github.com/pblumer/atlas/internal/trustroots"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/worker"
	"github.com/pblumer/atlas/workersdk"
)

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

// ErrNothingToServe ends a worker that holds no handler at all. It leaves
// [exitNothingToServe] rather than a generic failure so a supervisor can tell "this
// worker has nothing configured yet" from "this worker crashed", and park it instead
// of restarting it into an empty configuration on a backoff loop.
var ErrNothingToServe = errors.New("worker: nothing to serve")

// ExitNothingToServe is the status a process ending in [ErrNothingToServe] leaves
// (EX_CONFIG). The supervisor reads it to park a worker rather than restart it; api
// defines its constant as this one, so the two ends cannot disagree.
const ExitNothingToServe = 78

// Run runs this process as an out-of-process worker for a running Atlas
// (ADR-0157 step 5): it leases jobs of the types it was given, runs a command for
// each, and reports the outcome. The job arrives on the command's stdin as JSON and
// whatever JSON object the command prints becomes the variables the job completes
// with; a non-zero exit fails the job, carrying stderr as the message.
//
// This is what takes a service task's work off the engine's single writer: the
// latency of the work is paid by this process, not by every other request.
func Run(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "base URL of the atlas server to work for")
	id := fs.String("id", DefaultWorkerID(), "worker id, shown in the Workers view; give each deployment its own")
	token := fs.String("token", os.Getenv("ATLAS_TOKEN"), "bearer token, when the server requires authentication (or ATLAS_TOKEN)")
	tlsCA := fs.String("tls-ca", os.Getenv("ATLAS_TLS_CA"), "PEM bundle of certificate authorities to trust *in addition to* the host's, when --server is https and its certificate comes from an internal CA (ADR-0191). Without it the host trust store is the only answer. It is never a way to skip verification: there is none (or ATLAS_TLS_CA)")
	lease := fs.Duration("lease", worker.DefaultLease, "how long the engine holds a job for this worker; must comfortably exceed how long the work takes")
	wait := fs.Duration("wait", worker.DefaultWait, "how long a poll waits for work before asking again; the server caps it")
	maxJobs := fs.Int("max-jobs", worker.DefaultMaxJobs, "how many jobs of one type this worker runs at once; a poll leases only as many as there are free places for, so a leased job starts the moment it arrives. Keep it to what the handler and its target can take in parallel")
	once := fs.Bool("once", false, "poll each type once and exit, instead of working until interrupted")
	handles := handleFlag{}
	fs.Var(handles, "handle", "a job type and the command that works it, as type=command; repeat for each type")
	connectors := fs.String("connector", "", "comma-separated built-in Worker Types this worker serves (currently: ad, carddav, csv, entra, exchange, googlecontacts, jira, ldif, mail, mariadb, mssql, postgres, remedy, rest, script, teamsphone, webscrape, wikiwikibangbang). The server must be offloading them (it offloads ad, csv, jira, mail, remedy, script and webscrape by default; --in-process-connectors turns that off), or it still works them itself (ADR-0168). A kind with credentials reads them from the environment, never from a flag: mail takes ATLAS_MAIL_CONNECTORS plus, per name, ATLAS_MAIL_<NAME>_PROVIDER with _ENDPOINT, _SENDER and _SECRET — or, in the SMTP-only form, ATLAS_MAIL_<NAME>_ENDPOINT with the optional _USERNAME, _PASSWORD and _FROM. Each SQL kind takes ATLAS_<KIND>_CONNECTORS plus ATLAS_<KIND>_<NAME>_DSN — or, with ATLAS_<KIND>_MOCK=1, no DSN at all: the worker then answers that product's statements from seeded answers in its own memory, so a model that reads or writes a database runs end to end without one, and ATLAS_<KIND>_MOCK_SEED names the JSON file of answers it starts with (a statement nobody seeded fails naming itself rather than answering no rows). entra takes ATLAS_ENTRA_CONNECTORS plus ATLAS_ENTRA_<NAME>_TENANT_ID, _CLIENT_ID and exactly one of _CLIENT_SECRET, _CLIENT_CERTIFICATE (the app's certificate with its private key as PEM) or, on Windows, _CLIENT_CERTIFICATE_THUMBPRINT (a certificate in this host's store, whose key signs there), teamsphone takes ATLAS_TEAMSPHONE_CONNECTORS plus ATLAS_TEAMSPHONE_<NAME>_TENANT_ID, _CLIENT_ID and exactly one of _CERTIFICATE (PEM) or _CERTIFICATE_THUMBPRINT (a certificate in this host's store), runs pwsh — or ATLAS_TEAMSPHONE_PWSH — with the MicrosoftTeams module for each job within ATLAS_TEAMSPHONE_TIMEOUT (default 4m), and with ATLAS_TEAMSPHONE_MOCK=1 serves its tenants from memory instead, exchange takes ATLAS_EXCHANGE_CONNECTORS plus ATLAS_EXCHANGE_<NAME>_CREDENTIALS (the JSON bundle naming an Exchange Online organization or, with \"target\": \"onprem\", an Exchange Server) and optionally _URL (an Exchange Server's Remote PowerShell URI), and ATLAS_EXCHANGE_POWERSHELL for the interpreter an on-premises call starts, googlecontacts takes ATLAS_GOOGLECONTACTS_CONNECTORS plus ATLAS_GOOGLECONTACTS_<NAME>_CREDENTIALS (the JSON bundle of a service account acting as a Workspace user, or of a refresh token) and optionally _URL (a People API base behind a proxy), carddav takes ATLAS_CARDDAV_CONNECTORS plus ATLAS_CARDDAV_<NAME>_URL (the address book's URL, or the server's, from which the address book is found) and ATLAS_CARDDAV_<NAME>_CREDENTIALS (the JSON bundle {username, password, groups}), wikiwikibangbang takes ATLAS_WIKIWIKIBANGBANG_CONNECTORS plus ATLAS_WIKIWIKIBANGBANG_<NAME>_URL (the wiki's base URL) and ATLAS_WIKIWIKIBANGBANG_<NAME>_CREDENTIALS (the JSON bundle {token} of a technical account that holds no system role in the wiki), remedy takes ATLAS_REMEDY_CONNECTORS plus ATLAS_REMEDY_<NAME>_ENDPOINT, _USERNAME and _PASSWORD, and jira takes ATLAS_JIRA_CONNECTORS plus ATLAS_JIRA_<NAME>_URL and exactly one credential shape — _EMAIL with _API_TOKEN for Jira Cloud, or _TOKEN alone for a Data Center personal access token, because that shape also decides how an assignee is addressed and which search endpoint is used; ad and ldif need no startup configuration, ad resolving each task's bind-password reference from ATLAS_CONNECTOR_<REF>_TOKEN. Set ATLAS_AD_MOCK=1 to serve Active Directory tasks against a mock directory in this worker's memory instead of a real one — the models stay unchanged, nothing reaches a domain controller, and ATLAS_AD_MOCK_SEED names an LDIF or DSML file of entries it starts with. Point ATLAS_AD_MOCK_VIEW_URL at an atlas's /api/v1/ad/mock-directory and the worker reports the forest it holds, so it shows up under Operations > Mock directory instead of only in this worker's log. A worker this server supervises is switched from Console > Workers instead, which needs no restart; these variables are for a worker you run yourself, and for what a server does before anyone has used that switch. A worker atlas supervises is handed all of that at spawn from the worker store, so it needs none of it set by hand")
	scriptLanguages := fs.String("script-languages", "", "comma-separated script languages this worker serves (powershell, python, javascript); empty serves all for compatibility. atlas serve sets this automatically from its per-language flags")
	scriptSandboxRaw := fs.String("script-sandbox", envOr(script.SandboxEnv, string(script.SandboxOff)), "operating-system isolation for script interpreters: off or strict (Linux Landlock/seccomp; or ATLAS_SCRIPT_SANDBOX)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kinds := splitList(*connectors)
	scriptSandbox, err := script.ParseSandboxMode(*scriptSandboxRaw)
	if err != nil {
		return err
	}
	if slices.Contains(kinds, "script") {
		if err := script.CheckSandbox(scriptSandbox); err != nil {
			return err
		}
		// An external worker gets the same fail-closed startup the server has: the
		// languages it will serve are proved to start under the profile now, not
		// discovered to be unrunnable by the first job that leases.
		if err := script.CheckSandboxLanguages(scriptSandbox, splitList(*scriptLanguages)); err != nil {
			return err
		}
	}
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
		if name == script.LanguagesEnv && strings.TrimSpace(*scriptLanguages) != "" {
			return *scriptLanguages
		}
		if name == script.SandboxEnv {
			return string(scriptSandbox)
		}
		return os.Getenv(name)
	}
	builtin, err := worker.BuiltinConnectors(env, kinds...)
	if err != nil {
		return err
	}

	if err := logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel); err != nil {
		return err
	}
	sysproxy.InstallAndLog(os.Getenv)
	if len(handles) == 0 && len(builtin.Handlers) == 0 && len(builtin.Queries) == 0 {
		// Nothing to serve. For a worker an operator ran by hand this is a typo, and
		// saying so and stopping is right. For a supervised one it is an ordinary
		// state — a mail worker on a server with no mail worker yet — and the
		// supervisor must park it rather than restart it into the same emptiness
		// forever, so the two are told apart by the exit status rather than by the
		// message. Configure the kind and the supervisor brings this worker back
		// holding it.
		logging.Warn(logging.WorkerStarting, "nothing to serve: give at least one --handle type=command, or configure a Worker Type this worker was given",
			slog.String("connectors", strings.Join(kinds, ",")))
		return ErrNothingToServe
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
	serverRoots, err := trustroots.Load(*tlsCA)
	if err != nil {
		return err
	}
	opts := worker.Options{
		Server: strings.TrimRight(*server, "/"), ID: *id, Token: *token,
		// Only a server that supervises this worker sets it, and only for the process
		// serving a Worker whose credential it brokers per job.
		BrokerKey: os.Getenv("ATLAS_BROKER_KEY"),
		// The Worker Type this process runs, for the handshake (ADR-0535 §3). A
		// supervisor launching an installed artifact sets these from its manifest and
		// the digest it verified; a worker run by hand reports none unless its operator
		// sets them.
		Identity: workerIdentityFromEnv(os.Getenv),
		Handlers: execs, Lease: *lease, Wait: *wait, MaxJobs: *maxJobs,
		Connectors: builtin.Names,
		Endpoints:  builtin.Endpoints,
		// The form lookups the configured kinds answer, for the same Workers
		// (ADR-0473).
		Queries: builtin.Queries,
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

	logging.Info(logging.WorkerStarting, "working jobs for a running atlas",
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

// The environment a Worker Instance reads its Worker Type identity from.
const (
	workerTypeIDEnv      = "ATLAS_WORKER_TYPE_ID"
	workerTypeVersionEnv = "ATLAS_WORKER_TYPE_VERSION"
	workerTypeDigestEnv  = "ATLAS_WORKER_TYPE_DIGEST"
)

// workerIdentityFromEnv is the Worker Type identity the environment names, or nil when
// it names no id. Its shape is the server's to judge: a malformed one is refused on the
// first poll, with the reason, which is where an operator looks.
func workerIdentityFromEnv(getenv func(string) string) *workersdk.Identity {
	id := strings.TrimSpace(getenv(workerTypeIDEnv))
	if id == "" {
		return nil
	}
	return &workersdk.Identity{
		ID:      id,
		Version: strings.TrimSpace(getenv(workerTypeVersionEnv)),
		Digest:  strings.TrimSpace(getenv(workerTypeDigestEnv)),
	}
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

// DefaultWorkerID names the worker after the host it runs on, which is the useful
// default when several replicas each want their own row in the Workers view.
func DefaultWorkerID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "atlas-worker"
}

// splitList splits a comma-separated flag value into trimmed, non-empty items.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// envOr is the environment variable key, or def when it is unset or empty.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
