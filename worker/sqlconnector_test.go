package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sqldb"
)

// envMap makes a lookup out of a map, so a test states only the variables it sets.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// A SQL worker is configured entirely from its environment: the DSN is the credential
// here, so it must never be reachable through argv (ADR-0173).
func TestBuiltinConnectorsRegistersEachSQLProduct(t *testing.T) {
	for _, tc := range []struct{ kind, prefix, dsn, jobType string }{
		{"mssql", "ATLAS_MSSQL_", "sqlserver://u:p@localhost:1433?database=hr", compiler.MsSqlJobType},
		{"mariadb", "ATLAS_MARIADB_", "u:p@tcp(localhost:3306)/hr", compiler.MariaDBJobType},
		{"postgres", "ATLAS_POSTGRES_", "postgres://u:p@localhost:5432/hr", compiler.PostgresJobType},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := BuiltinConnectors(envMap(map[string]string{
				tc.prefix + "CONNECTORS": "hr-db",
				tc.prefix + "HR_DB_DSN":  tc.dsn,
			}), tc.kind)
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			if _, ok := got.Handlers[tc.jobType]; !ok {
				t.Errorf("no handler for %s; have %v", tc.jobType, got.Handlers)
			}
			// The names are what the Workers view subtracts from what models
			// reference, to show which connectors nothing can serve.
			if len(got.Names) != 1 || got.Names[0] != "hr-db" {
				t.Errorf("names = %v, want [hr-db]", got.Names)
			}
		})
	}
}

// A SQL worker holding no database yet is not misconfigured — it is unconfigured,
// which is the state every server starts in. It must park rather than fail, because
// a supervised worker that *fails* is restarted with a growing backoff that never
// converges, and a kind Atlas supervises by default would spend the rest of the
// server's life restarting into the same emptiness. Configure a connector in the
// Console and refreshSupervisedWorkers brings this worker back holding it.
//
// The distinction this draws is the whole point: nothing configured parks, anything
// configured-but-broken still fails at startup (the test below).
func TestASQLWorkerWithNoConfiguredConnectorParksInsteadOfFailing(t *testing.T) {
	for _, kind := range []string{"postgres", "mariadb", "mssql"} {
		t.Run(kind, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(nil), kind)
			if err != nil {
				t.Fatalf("an unconfigured %s worker must not fail at startup: %v", kind, err)
			}
			if len(built.Handlers) != 0 {
				t.Errorf("handlers = %v, want none — it has no database to run them against", built.Handlers)
			}
			if len(built.Unconfigured) != 1 || built.Unconfigured[0] != kind {
				t.Errorf("unconfigured = %v, want [%s] so the startup line can say it", built.Unconfigured, kind)
			}
		})
	}

	// The kinds it *can* serve must survive: a worker serving both must not lose its
	// CSV handler because no database is configured yet.
	built, err := BuiltinConnectors(envMap(nil), "csv", "postgres")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers[compiler.CsvImportJobType]; !ok {
		t.Error("the kinds it can serve were dropped along with postgres")
	}
}

// A worker with no in-process fallback must refuse a broken configuration while the
// operator is still watching, not a retry budget later. A *named* connector with no
// DSN is that: somebody meant to configure it and did it wrong, which is not the
// same as not having configured one at all.
func TestBuiltinConnectorsRefusesAMisconfiguredSQLWorker(t *testing.T) {
	_, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_POSTGRES_CONNECTORS": "hr-db",
	}), "postgres")
	if err == nil {
		t.Fatal("a connector with no DSN must fail at startup")
	}
	if !strings.Contains(err.Error(), "ATLAS_POSTGRES_HR_DB_DSN") {
		t.Errorf("the error should quote the exact variable, got: %v", err)
	}
}

// Where the driver parses the DSN at open time, a malformed one is refused at
// startup. This is best-effort by driver — MySQL and SQL Server parse here, pgx
// defers to the first connection — so it is asserted against a driver that does.
func TestBuiltinConnectorsRefusesAnUnparsableDSN(t *testing.T) {
	_, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_MARIADB_CONNECTORS": "hr-db",
		"ATLAS_MARIADB_HR_DB_DSN":  "not-a-dsn",
	}), "mariadb")
	if err == nil {
		t.Fatal("an unparsable DSN must fail at startup")
	}
	if !strings.Contains(err.Error(), "hr-db") {
		t.Errorf("the error should name the connector, got: %v", err)
	}
}

// A database that is merely unreachable must not stop a worker from starting: a
// worker has to survive a database restart, and sql.Open connects lazily.
func TestBuiltinConnectorsStartsWithAnUnreachableDatabase(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_MARIADB_CONNECTORS": "hr-db",
		"ATLAS_MARIADB_HR_DB_DSN":  "u:p@tcp(127.0.0.1:1)/hr",
	}), "mariadb")
	if err != nil {
		t.Fatalf("an unreachable database must not fail startup: %v", err)
	}
	if _, ok := got.Handlers[compiler.MariaDBJobType]; !ok {
		t.Error("the handler should still be registered")
	}
}

// A kind this worker does not implement is refused at startup, naming what it does
// implement. DB2 is the case that matters here: it is the one MIM database agent with
// no pure-Go driver (ADR-0010), so an operator who reaches for it should be told so
// while they are still watching.
func TestBuiltinConnectorsRefusesAnUnimplementedKind(t *testing.T) {
	_, err := BuiltinConnectors(envMap(nil), "db2")
	if err == nil {
		t.Fatal("an unimplemented kind must be refused at startup")
	}
	if !strings.Contains(err.Error(), "db2") || !strings.Contains(err.Error(), "mssql") {
		t.Errorf("the error should name the kind and what is available, got: %v", err)
	}
}

// A job that arrives without a resolved detail is a server that does not resolve SQL
// tasks — reported as that, rather than as an empty statement.
func TestRunSQLJobWithoutADetail(t *testing.T) {
	_, err := RunSQLJob(context.Background(), Job{}, sqldb.NewRegistry())
	if err == nil {
		t.Fatal("a job with no connector detail must fail")
	}
}

// The resolved detail round-trips through the job payload into a sqldb.Job.
func TestRunSQLJobReadsTheResolvedDetail(t *testing.T) {
	_, err := RunSQLJob(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "postgres",
		Fields: map[string]any{
			"connector": "hr-db", "product": "postgres", "operation": "query",
			"statement": "SELECT 1", "resultVariable": "r",
		},
	}}, sqldb.NewRegistry())
	// The registry is empty, so this reaches the connector lookup — which is the
	// evidence the payload decoded: an undecodable one fails earlier and differently.
	if err == nil || !strings.Contains(err.Error(), "hr-db") {
		t.Errorf("err = %v, want an unresolved-connector error naming hr-db", err)
	}
}
