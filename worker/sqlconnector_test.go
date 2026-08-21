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
// here, so it must never be reachable through argv (ADR-0170).
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

// A worker with no in-process fallback must refuse a broken configuration while the
// operator is still watching, not a retry budget later.
func TestBuiltinConnectorsRefusesAMisconfiguredSQLWorker(t *testing.T) {
	if _, err := BuiltinConnectors(envMap(nil), "postgres"); err == nil {
		t.Error("no ATLAS_POSTGRES_CONNECTORS must fail at startup")
	} else if !strings.Contains(err.Error(), "ATLAS_POSTGRES_CONNECTORS") {
		t.Errorf("the error should name the variable to set, got: %v", err)
	}

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

// An unknown kind yields no handler, so the caller's count comparison catches it.
func TestBuiltinConnectorsIgnoresAnUnknownKind(t *testing.T) {
	got, err := BuiltinConnectors(envMap(nil), "db2")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(got.Handlers) != 0 {
		t.Errorf("handlers = %v, want none for an unimplemented kind", got.Handlers)
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
