package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	// The SQL drivers a worker can open, registered with database/sql by import.
	// They live here rather than in connector/sqldb so that package stays free of
	// vendor code and testable against a fake driver — and so the engine, which
	// never opens a database, does not link a driver by depending on the connector.
	//
	// All three are pure Go: ADR-0010 forbids CGO, which is also why IBM DB2 has no
	// connector (its driver is a CGO wrapper with no pure-Go alternative).
	_ "github.com/go-sql-driver/mysql"  // mysql — MariaDB
	_ "github.com/jackc/pgx/v5/stdlib"  // pgx — PostgreSQL
	_ "github.com/microsoft/go-mssqldb" // sqlserver — Microsoft SQL Server

	"github.com/pblumer/atlas/connector/sqldb"
)

// sqlRegistryFromEnv builds the databases of one product this worker holds.
// ATLAS_<PRODUCT>_CONNECTORS lists the names; each name contributes
// ATLAS_<PRODUCT>_<NAME>_DSN. The names are the ones models reference, so the list is
// also the answer to "what can this worker actually reach".
//
// The DSN comes from the environment and not from a flag because argv is readable by
// anyone who can list processes — and unlike every other connector's endpoint, a DSN
// *is* the credential (ADR-0173).
func sqlRegistryFromEnv(env func(string) string, p sqldb.Product) (*sqldb.Registry, []string, error) {
	prefix := "ATLAS_" + envFold(p.Name) + "_"
	names := splitAndTrim(env(prefix + "CONNECTORS"))
	if len(names) == 0 {
		// Unconfigured, not misconfigured — a nil registry and no error, which the
		// caller reports as a kind this worker does not serve. The two must not be
		// conflated: this kind is supervised by default, so every server that has not
		// configured a database yet starts one of these, and failing here would be a
		// backoff loop that never converges (exitNothingToServe exists for exactly
		// that). A *named* connector missing its DSN, below, is still an error.
		return nil, nil, nil
	}
	reg := sqldb.NewRegistry()
	for _, name := range names {
		key := prefix + envFold(name) + "_"
		dsn := env(key + "DSN")
		if dsn == "" {
			return nil, nil, fmt.Errorf("worker: %s connector %q has no connection string: set %sDSN", p.Name, name, key)
		}
		// sql.Open connects lazily, so a database that is merely down does not stop a
		// worker from starting — a worker must survive a database restart.
		//
		// What it catches is driver-dependent and therefore best-effort: the SQL
		// Server and MySQL drivers parse the DSN here and reject a malformed one,
		// while pgx defers parsing to the first connection. So the guarantee this
		// startup check actually makes is the narrower one that matters in practice —
		// every configured name *has* a DSN — and a malformed one surfaces on the
		// first job rather than being silently ignored.
		db, err := sql.Open(p.Driver, dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("worker: %s connector %q has an unusable connection string: %w", p.Name, name, err)
		}
		reg.Register(name, sqldb.NewClient(db, p))
	}
	return reg, names, nil
}

// RunSQLJob executes a resolved SQL job through a registry the caller owns. It is
// exported for the same reason RunMailJob is: the environment is only the default
// place a worker's connection strings come from, and a caller embedding this package
// can build a registry from a vault or an instance profile and get the identical
// execution.
func RunSQLJob(ctx context.Context, j Job, reg *sqldb.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("sqldb: the job carried no resolved connector detail; is this server running a version that resolves SQL tasks?")
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
