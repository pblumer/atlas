package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	// The SQL drivers a worker can open, registered with database/sql by import.
	// They live here rather than in connector/sqldb so that package stays free of
	// vendor code and testable against a fake driver — and so the engine, which
	// never opens a database, does not link a driver by depending on the worker.
	//
	// All three are pure Go: ADR-0010 forbids CGO, which is also why IBM DB2 has no
	// worker (its driver is a CGO wrapper with no pure-Go alternative).
	_ "github.com/go-sql-driver/mysql"  // mysql — MariaDB
	_ "github.com/jackc/pgx/v5/stdlib"  // pgx — PostgreSQL
	_ "github.com/microsoft/go-mssqldb" // sqlserver — Microsoft SQL Server

	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/logging"
)

// sqlRegistryFromEnv builds the databases of one product this worker holds.
// ATLAS_<PRODUCT>_CONNECTORS lists the names; each name contributes
// ATLAS_<PRODUCT>_<NAME>_DSN. The names are the ones models reference, so the list is
// also the answer to "what can this worker actually reach".
//
// The DSN comes from the environment and not from a flag because argv is readable by
// anyone who can list processes — and unlike every other worker's endpoint, a DSN
// *is* the credential (ADR-0173).
func sqlRegistryFromEnv(env func(string) string, p sqldb.Product) (*sqldb.Registry, []string, *sqldb.MockDatabase, error) {
	mock, err := sqlMockFromEnv(env, p)
	if err != nil {
		return nil, nil, nil, err
	}
	names := splitAndTrim(env(p.ConnectorsEnv()))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. The two must not be
		// conflated: this kind is supervised by default, so every server that has not
		// configured a database yet starts one of these, and failing here would be a
		// backoff loop that never converges (exitNothingToServe exists for exactly
		// that). A *named* worker missing its DSN, below, is still an error.
		return nil, nil, nil, nil
	}
	reg := sqldb.NewRegistry()
	for _, name := range names {
		dsnVar := p.DSNEnv(name)
		if mock != nil {
			// Mock mode: every configured name is the one in-memory database, so the
			// journal is one list of what the process ran rather than one per name.
			// A DSN set alongside it is deliberately ignored and not an error — the
			// point of the switch is to run the same configuration against memory.
			reg.Register(name, sqldb.OpenMock(mock))
			continue
		}
		dsn := env(dsnVar)
		if dsn == "" {
			return nil, nil, nil, fmt.Errorf("worker: %s worker %q has no connection string: set %s", p.Name, name, dsnVar)
		}
		// sqldb.Open connects lazily and caps the pool. A database that is merely
		// down therefore does not stop a worker from starting — a worker must survive
		// a database restart.
		//
		// What it catches is driver-dependent and therefore best-effort: the SQL
		// Server and MySQL drivers parse the DSN here and reject a malformed one,
		// while pgx defers parsing to the first connection. So the guarantee this
		// startup check actually makes is the narrower one that matters in practice —
		// every configured name *has* a DSN — and a malformed one surfaces on the
		// first job rather than being silently ignored.
		client, err := sqldb.Open(p, dsn)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("worker: %s worker %q has an unusable connection string: %w", p.Name, name, err)
		}
		reg.Register(name, client)
	}
	return reg, names, mock, nil
}

// ProbeSQL opens one database and checks that it answers. It is what the Console's
// worker check calls, handed to the server as api.WithSQLProbe by whoever assembles
// the binary.
//
// It lives here and not in the api package for the reason the blank imports above do:
// this is where the drivers are, and the engine deliberately links none of them
// (ADR-0173). The pool it opens is closed again immediately — a check is one
// connection, not a registration.
func ProbeSQL(ctx context.Context, p sqldb.Product, dsn string) error {
	client, err := sqldb.Open(p, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return client.Ping(ctx)
}

// RunSQLJob executes a resolved SQL job through a registry the caller owns. It is
// exported for the same reason RunMailJob is: the environment is only the default
// place a worker's connection strings come from, and a caller embedding this package
// can build a registry from a vault or an instance profile and get the identical
// execution.
func RunSQLJob(ctx context.Context, j Job, reg *sqldb.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("sqldb: the job carried no resolved worker detail; is this server running a version that resolves SQL tasks?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task sqldb.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("sqldb: cannot read the resolved detail: %w", err)
	}
	return sqldb.Run(ctx, task, reg)
}

// Mock mode, for the SQL products. It is the AD mockup switch (ADR-0181) applied to a
// database: the worker answers SQL tasks from seeded answers in its own memory, so a
// model that reads or writes a database runs end to end without one — and without the
// database an identity process needs being the one nobody wants a test writing into.
//
// The switch is the worker's, never the model's. A mockup flag on the task would be a
// model that behaves differently in test and production and would eventually be
// deployed with the flag still set; here the model is byte-identical either way and
// what differs is which worker leases its jobs.
// The two variables are the product's own ([sqldb.Product.MockEnv],
// [sqldb.Product.MockSeedEnv]): ATLAS_<PRODUCT>_MOCK turns mock mode on, and
// ATLAS_<PRODUCT>_MOCK_SEED names the JSON file of seeded answers it starts with.
// Without a seed a mock knows nothing, and every statement fails naming itself — and
// naming that variable, which is why the spelling belongs to the product rather than
// to any one of the packages that quote it.

// sqlMockFromEnv decides whether this worker's tasks of product p reach a database or
// a stand-in in its own memory. A nil database means the real thing.
func sqlMockFromEnv(env func(string) string, p sqldb.Product) (*sqldb.MockDatabase, error) {
	mockVar, seedVar := p.MockEnv(), p.MockSeedEnv()
	on, err := envBool(env, mockVar)
	if err != nil {
		return nil, err
	}
	seed := strings.TrimSpace(env(seedVar))
	if !on {
		if seed != "" {
			// Read into a database nothing would ever reach. Almost certainly a
			// half-finished mockup setup, and silence would leave the operator
			// believing they had one.
			return nil, fmt.Errorf("worker: %s names %s but %s is not set, so the seed would be read into a database no job reaches", seedVar, seed, mockVar)
		}
		return nil, nil
	}
	answers, err := sqlMockSeed(seedVar, seed)
	if err != nil {
		return nil, err
	}
	logging.Warn(logging.SQLMockEnabled,
		"the "+p.Name+" worker is in mock mode: statements are answered from this worker's memory and reach no database",
		slog.String("product", p.Name), slog.Int("seeded", len(answers)), slog.String("seed", seed))
	return sqldb.NewMockDatabase(p, answers...), nil
}

// sqlMockSeed reads the seed file, if one was named.
//
// The two failures are told apart on purpose. A file that cannot be *read* starts an
// unseeded mock and warns: the supervisor restarts a child that exits, so refusing
// over an optional path is the restart loop ADR-0202 already paid for once, and every
// statement then fails naming itself — which points at the missing seed rather than
// hiding it. A file that *is* there and is malformed is refused, because that is a
// typo in something the operator wrote and is fixed by being told, not by a mock that
// silently answers nothing.
func sqlMockSeed(seedVar, path string) ([]sqldb.MockAnswer, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		logging.Warn(logging.SQLMockSeedUnusable,
			"the SQL mockup seed could not be read; the mock database starts with no answers, so every statement will fail naming itself",
			slog.String("seed", path), slog.String("error", err.Error()))
		return nil, nil
	}
	answers, err := sqldb.ParseMockSeed(data)
	if err != nil {
		return nil, fmt.Errorf("worker: %s %s: %w", seedVar, path, err)
	}
	return answers, nil
}
