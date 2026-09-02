package api

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/logging"
)

// A SQL database an operator configures in the Console, and how its connection string
// reaches the worker that uses it.
//
// [ADR-0173] decided the opposite of this: a SQL connector is worker-only, and its DSN
// lives in the worker's environment because the engine must never hold a database
// credential. That decision stands for every worker somebody else runs — an external
// worker still reads its own environment and is handed nothing from here. What this
// adds is the *supervised* case, where the engine is also the operator: the same
// argument mail and Entra already won (see superviseEnv), applied to the kind whose
// credential is the most valuable one Atlas touches. ADR-0188
// states the cost that buys.
//
// The credential is the WHOLE connection string, not a password spliced into an
// address. That is what keeps ADR-0173's actual objection intact: splitting a DSN
// would mean re-assembling it per vendor — MySQL's is not even a URL — and inventing
// a credential-handling path for the convenience of putting an address in a record.
// Here the record holds a reference and nothing else (I6), exactly as it does for
// every other managed kind.

// Connector-store kinds for the three SQL products. They are the same operator-facing
// names sqldb.Product uses and `atlas worker --connector` takes, so a record, a flag
// and an environment variable never disagree about what a product is called.
const (
	connectorKindMSSQL    = "mssql"
	connectorKindMariaDB  = "mariadb"
	connectorKindPostgres = "postgres"
)

// sqlConnectorKinds are the three, in the order the Console offers them.
func sqlConnectorKinds() []string {
	return []string{connectorKindPostgres, connectorKindMariaDB, connectorKindMSSQL}
}

// isSQLConnectorKind reports whether a kind name is one of the SQL products, so the
// create handler can refuse a connectionString sent to a kind that has no use for one
// rather than silently dropping it.
func isSQLConnectorKind(kind string) bool {
	_, ok := sqldb.ProductByName(kind)
	return ok
}

// validateSQLConnector checks a database a Console operator is adding. The connection
// string lives in the vault under the credentialsRef — never in the record, and never
// in a model. The create handler accepts the string itself and seals it, so what
// reaches here is already a reference either way; the mail-only fields do not apply,
// and the endpoint is a derived, redacted label rather than something an operator
// authors (see redactedSQLTarget).
func validateSQLConnector(p *createConnectorParams) string {
	p.Provider, p.Sender = "", ""
	if p.CredentialsRef == "" {
		return "a SQL connector requires a connectionString to seal, or a credentialsRef naming one already in the vault"
	}
	return ""
}

// sqlDSNRef is the vault key a connector's connection string is sealed under when the
// operator pastes one into the Console instead of naming a key themselves. It is
// derived from the record id rather than the name so that renaming a connector cannot
// orphan its secret, and so two connectors can never collide on one key.
func sqlDSNRef(id string) string { return "sql/" + id + "/dsn" }

// redactedSQLTarget derives the address half of a connection string, for the Console
// to show which database a connector points at. A connector whose whole configuration
// is one secret would otherwise be an opaque name, and "is this the test database or
// the production one" is exactly the question an operator needs answered before they
// point a process at it.
//
// Deriving this is allowed where *assembling* a DSN is not, and the difference is the
// failure mode. An assembler that gets a vendor's grammar wrong breaks the connection;
// this only ever produces a label, so it is written to give up rather than guess: a
// value that is not a URL with a host (MySQL's keyword form, anything unparsable)
// yields no label at all. The password is never read — only the username — and the
// query string is dropped wholesale, because some vendors carry a password in it.
func redactedSQLTarget(dsn string) string {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	target := u.Host + u.Path
	if user := u.User.Username(); user != "" {
		target = user + "@" + target
	}
	return target
}

// sqlWorkerEnvByName is sqlWorkerEnv keyed by the operator-facing product name, for
// the provisioned-kinds table. An unknown name renders nothing rather than panicking:
// the table and the product table are checked against each other by
// TestEverySQLKindIsAProduct, so a mismatch is a failing test, not a nil map read at
// spawn time.
func (s *Server) sqlWorkerEnvByName(kind string) []string {
	p, ok := sqldb.ProductByName(kind)
	if !ok {
		return nil
	}
	return s.sqlWorkerEnv(p)
}

// sqlWorkerEnv renders the connection strings a supervised worker of one SQL product
// needs: ATLAS_<PRODUCT>_CONNECTORS naming the databases, and one
// ATLAS_<PRODUCT>_<NAME>_DSN per database, resolved out of this server's vault.
//
// It is the Entra story with a connection string in place of an OAuth bundle, and it
// obeys the same two rules. The variables are the ones an operator sets by hand for an
// external worker, because a private channel between parent and child is how the
// supervised path would quietly become the only tested one (ADR-0157). And a database
// whose DSN the operator set directly on the host wins: overriding it would let a
// stale vault entry silently beat an explicit choice.
//
// It reads the connector store and the vault, so it runs on the run-loop goroutine
// (their owner), like mailWorkerEnv and entraWorkerEnv do.
func (s *Server) sqlWorkerEnv(p sqldb.Product) []string {
	prefix := "ATLAS_" + connectorEnvKey(p.Name) + "_"
	connectorsVar := prefix + "CONNECTORS"

	var env []string
	var names []string
	var fromStore bool // a store database contributed a name; only then must CONNECTORS be rendered
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	s.do(func() {
		// Databases an operator set on the host: inherited by the child already, so
		// nothing is rendered for them — they are collected only so a store-based
		// render below does not drop them from the CONNECTORS list.
		for _, name := range splitConnectorList(os.Getenv(connectorsVar)) {
			addName(name)
		}
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the connector store for a supervised SQL worker",
				slog.String("product", p.Name), slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != p.Name || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			// Two names that fold to one variable would silently give one the other's
			// credential — the mail/Entra collision, refused here for the same reason.
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two SQL connectors share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			taken[envKey] = c.Name
			key := prefix + envKey + "_"
			if strings.TrimSpace(os.Getenv(key+"DSN")) != "" {
				// The operator set this one on the host; leave it alone and let the
				// child inherit it, but keep the name in CONNECTORS.
				addName(c.Name)
				fromStore = true
				continue
			}
			dsn := strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef))
			if dsn == "" {
				// A record whose secret is not set yet (or was deleted) is left out
				// rather than handed over empty: the worker then simply does not build
				// that database, and the Console shows the connector as
				// configured-not-working instead of a job failing mid-run.
				//
				// Its name must be left out of CONNECTORS too, and that is not
				// cosmetic: a name the worker is told to serve with no DSN behind it is
				// precisely the misconfiguration it refuses to start on. Adding the
				// name here would turn "this connector has no secret yet" — the state
				// every connector passes through while it is being set up — into a
				// worker that will not boot.
				continue
			}
			addName(c.Name)
			env = append(env, key+"DSN="+dsn)
			fromStore = true
		}
	})
	if !fromStore {
		return env
	}
	return append(env, connectorsVar+"="+strings.Join(names, ","))
}

// sqlConnectorProblem reports why a configured SQL connector is not usable, so the
// Console can say "stored but not working" instead of leaving it to a task that parks
// (ADR-0158). The engine builds no client for these kinds, so unlike the managed kinds
// there is no registry to miss from — what can be checked is the record itself and
// whether its secret resolves.
//
// Threading, and note it runs the opposite way round to sqlWorkerEnv above: this is
// called from connectorProblem, which is already ON the run loop, so it reads the
// store and the vault directly and must never wrap them in s.do — that would schedule
// onto the goroutine it is running on. sqlWorkerEnv is called off the loop and so does
// the opposite. Making these two agree is the wrong instinct; their callers differ.
func (s *Server) sqlConnectorProblem(kind, name string) (string, bool) {
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return "", false
	}
	for _, c := range recs {
		if c.Name != name || c.Kind != kind {
			continue
		}
		if !c.Enabled {
			return "the connector is disabled", true
		}
		if strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef)) == "" {
			return "no connection string is stored for this connector", true
		}
		return "", false
	}
	return "", false
}

// SQLProbe opens one database and reports whether it answers. It is the seam that
// lets the Console check a database without the engine linking a database driver.
//
// [ADR-0173] keeps the drivers in `worker`, so `api` — which resolves SQL tasks and
// never executes them — links none of them. A check therefore cannot call sql.Open
// here; it is handed in by whoever assembles the binary, which for the single binary
// (ADR-0011) is the same process that runs workers. An embedder who wires nothing gets
// a check that says it cannot run, which is honest, rather than a connector reported
// broken because a driver was absent.
//
// [ADR-0173]: https://github.com/pblumer/atlas/blob/main/docs/adr/0173-generic-sql-connector.md
type SQLProbe func(ctx context.Context, product sqldb.Product, dsn string) error

// WithSQLProbe gives this server a way to check a SQL connector's connection string
// (ADR-draft-checking-a-database-connector). Pass worker.ProbeSQL, which opens the
// product's driver and pings it.
//
// It is what makes the Console's check work for the SQL kinds. Without it the check
// answers "this server cannot check a database connection", because that is the truth:
// nothing in this package can dial one.
func WithSQLProbe(p SQLProbe) Option { return func(s *Server) { s.sqlProbe = p } }

// checkSQLConnector answers the Console's check for one of the SQL kinds.
//
// What is being dialled is the operator's own database with the operator's own
// credential, at the moment they ask — not process work. The engine already holds this
// connection string for a Console-managed connector (ADR-0188 decided that, so the
// supervised worker can be handed it), so what is new here is only the dial, on a
// click, from the process the operator is already talking to. ADR-0173's promise —
// that no *model* and no external worker's credential passes through the engine — is
// untouched.
//
// It returns the verdict the handler renders: whether it worked, and a sentence saying
// what that did and did not prove.
func (s *Server) checkSQLConnector(ctx context.Context, product sqldb.Product, dsn string) (bool, string) {
	if s.sqlProbe == nil {
		return false, "This server cannot check a database connection: it was built without a database driver. " +
			"The connector still works on a worker that has one."
	}
	if err := s.sqlProbe(ctx, product, dsn); err != nil {
		return false, err.Error()
	}
	// Say what was proved. A login that connects may still have no rights to the table
	// a statement names, and an operator who reads "OK" as "the task will work" finds
	// that out from an incident.
	target := redactedSQLTarget(dsn)
	if target == "" {
		target = "the database"
	}
	return true, "Connected to " + target + " and authenticated. No statement was run, so this does not " +
		"prove the login may read or write the tables a task names."
}
