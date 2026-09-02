package worker

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
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

// Mock mode, for every product. The worker serves SQL tasks from its own memory: two
// variables replace the connection strings — the names it answers to, and the file of
// seeded answers — so a model that reads a database runs end to end without one.
//
// Each of these runs for all three products. Mock mode is one code path (ADR-0221) and
// the only thing that differs is the variable prefix, which is exactly the kind of
// difference a single-product test cannot see: a product whose variables were spelled
// differently would have a mockup mode that quietly does nothing.

// sqlProducts are the three, for the tests that must hold for all of them.
func sqlProducts(t *testing.T) []sqldb.Product {
	t.Helper()
	var out []sqldb.Product
	for _, name := range sqldb.ProductNames() {
		p, ok := sqldb.ProductByName(name)
		if !ok {
			t.Fatalf("ProductByName(%q) missing", name)
		}
		out = append(out, p)
	}
	if len(out) < 3 {
		t.Fatalf("got %d SQL products, want at least the three; a table this test read as empty would pass every case vacuously", len(out))
	}
	return out
}

// A mock worker needs no connection string at all: the names it serves and a seed
// file are the whole configuration.
func TestASQLWorkerInMockModeNeedsNoConnectionString(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			stmt := "SELECT id, mail FROM personen WHERE id = " + p.Placeholder
			seed := filepath.Join(t.TempDir(), "seed.json")
			if err := os.WriteFile(seed, []byte(`{"answers":[
	  {"statement":"`+stmt+`","params":[42],"columns":["id","mail"],"rows":[[42,"arno@example.com"]]}
	]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			built, err := BuiltinConnectors(envMap(map[string]string{
				p.ConnectorsEnv(): "hr-db",
				p.MockEnv():       "1",
				p.MockSeedEnv():   seed,
			}), p.Name)
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			h, ok := built.Handlers[p.JobType]
			if !ok {
				t.Fatalf("mock mode registered no %s handler", p.Name)
			}
			out, err := h.Run(context.Background(), Job{Connector: &ConnectorPayload{Kind: p.Name, Fields: map[string]any{
				"connector": "hr-db", "product": p.Name, "operation": "query-one",
				"statement": stmt,
				"params":    []any{42}, "resultVariable": "person",
			}}})
			if err != nil {
				t.Fatalf("a mock job failed: %v", err)
			}
			person, ok := out["person"].(map[string]any)
			if !ok {
				t.Fatalf("person = %#v, want the seeded row", out["person"])
			}
			if person["mail"] != "arno@example.com" {
				t.Errorf("mail = %#v, want the seeded address", person["mail"])
			}
		})
	}
}

// A seed named with mock mode off would be read into a database no job reaches. It is
// almost certainly a half-finished mockup setup, and silence would leave the operator
// believing they had one — the same refusal the AD mock makes.
func TestASQLSeedWithoutMockModeIsRefused(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			_, err := BuiltinConnectors(envMap(map[string]string{
				p.ConnectorsEnv(): "hr-db",
				p.DSNEnv("hr-db"): "sqlserver://x",
				p.MockSeedEnv():   "/tmp/nope.json",
			}), p.Name)
			if err == nil || !strings.Contains(err.Error(), p.MockEnv()) {
				t.Fatalf("error = %v, want one naming %s", err, p.MockEnv())
			}
		})
	}
}

// An unreadable seed starts an unseeded mock rather than taking the worker down. The
// supervisor restarts a child that exits, so refusing here is a restart loop over an
// optional file — the outage the AD mock's seed handling already learned. Every
// statement then fails naming itself and the product's own seed variable, which points
// at the missing seed rather than hiding it.
func TestAnUnreadableSQLSeedStartsAnUnseededMock(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(map[string]string{
				p.ConnectorsEnv(): "hr-db",
				p.MockEnv():       "yes",
				p.MockSeedEnv():   filepath.Join(t.TempDir(), "absent.json"),
			}), p.Name)
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			h := built.Handlers[p.JobType]
			if h == nil {
				t.Fatal("no handler was registered")
			}
			_, err = h.Run(context.Background(), Job{Connector: &ConnectorPayload{Kind: p.Name, Fields: map[string]any{
				"connector": "hr-db", "product": p.Name, "operation": "query",
				"statement": "SELECT 1", "resultVariable": "rows",
			}}})
			if err == nil || !strings.Contains(err.Error(), "nothing is seeded") {
				t.Fatalf("error = %v, want the unseeded-statement refusal", err)
			}
			// The refusal has to name the variable this product's operator actually
			// sets, not a pattern they have to fold themselves.
			if !strings.Contains(err.Error(), p.MockSeedEnv()) {
				t.Errorf("error = %v, want it to name %s", err, p.MockSeedEnv())
			}
		})
	}
}

// A malformed seed is an operator's typo in a file they wrote, so it is reported with
// the file named — not degraded into a mock that silently answers nothing.
func TestAMalformedSQLSeedIsReported(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			seed := filepath.Join(t.TempDir(), "seed.json")
			if err := os.WriteFile(seed, []byte(`{"answers":[{"columns":["id"]}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := BuiltinConnectors(envMap(map[string]string{
				p.ConnectorsEnv(): "hr-db",
				p.MockEnv():       "1",
				p.MockSeedEnv():   seed,
			}), p.Name)
			if err == nil || !strings.Contains(err.Error(), "needs a statement") {
				t.Fatalf("error = %v, want the seed's own complaint", err)
			}
		})
	}
}

// Mock mode still answers only the names it was told to serve: a model naming a
// database this worker does not hold gets the unresolved-connector error it would get
// against a real one, rather than a mock inventing a database.
func TestMockModeAnswersOnlyItsConfiguredNames(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(map[string]string{
				p.ConnectorsEnv(): "hr-db",
				p.MockEnv():       "1",
			}), p.Name)
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			_, err = built.Handlers[p.JobType].Run(context.Background(), Job{Connector: &ConnectorPayload{Kind: p.Name, Fields: map[string]any{
				"connector": "other-db", "product": p.Name, "operation": "query",
				"statement": "SELECT 1", "resultVariable": "rows",
			}}})
			if err == nil || !strings.Contains(err.Error(), "other-db") {
				t.Fatalf("error = %v, want one naming the unconfigured database", err)
			}
		})
	}
}

// Mock mode with nothing named parks like every other unconfigured kind: the model
// names a database, so a mock with no names has nothing a task could address.
func TestMockModeWithNoNamesParks(t *testing.T) {
	for _, p := range sqlProducts(t) {
		t.Run(p.Name, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(map[string]string{p.MockEnv(): "1"}), p.Name)
			if err != nil {
				t.Fatalf("BuiltinConnectors: %v", err)
			}
			if len(built.Unconfigured) != 1 || built.Unconfigured[0] != p.Name {
				t.Errorf("Unconfigured = %v, want [%s]", built.Unconfigured, p.Name)
			}
		})
	}
}

// probeDriver is a database/sql driver that only has to connect, which is all a
// connection check asks of one. It stands in for the vendor drivers this package
// blank-imports, so the check itself is testable without a database.
type probeDriver struct{ err error }

func (d probeDriver) Open(string) (driver.Conn, error) {
	if d.err != nil {
		return nil, d.err
	}
	return probeConn{}, nil
}

type probeConn struct{}

func (probeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (probeConn) Close() error                        { return nil }
func (probeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }

func init() {
	sql.Register("atlasprobe", probeDriver{})
	sql.Register("atlasprobedead", probeDriver{err: errors.New("dial tcp 10.0.0.9:1433: connect: connection refused")})
}

// A database that answers is what the Console's check reports as working. Nothing
// else is claimed: the ping proves the address resolves, the port answers and the
// login is accepted, and stops there.
func TestProbeSQLReachesADatabase(t *testing.T) {
	p := sqldb.Product{Name: "probe", Driver: "atlasprobe"}
	if err := ProbeSQL(context.Background(), p, "server=db;database=hr"); err != nil {
		t.Fatalf("ProbeSQL against a database that answers: %v", err)
	}
}

// And one that does not answer comes back with the driver's own reason, because that
// sentence is what the operator reads in the form — "failed" would send them looking
// for which of the six things in a connection string is wrong.
func TestProbeSQLReportsWhyItCannotConnect(t *testing.T) {
	p := sqldb.Product{Name: "probe", Driver: "atlasprobedead"}
	err := ProbeSQL(context.Background(), p, "server=db;database=hr")
	if err == nil {
		t.Fatal("ProbeSQL succeeded against a database that refuses connections")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want the driver's own reason", err)
	}
}

// A driver nobody registered is reported before anything is dialled — the case a
// server built without the SQL drivers is in, which the check words as "this server
// cannot check a database connection" rather than as a broken connector.
func TestProbeSQLWithoutADriver(t *testing.T) {
	err := ProbeSQL(context.Background(), sqldb.Product{Name: "probe", Driver: "no-such-driver"}, "whatever")
	if err == nil {
		t.Fatal("ProbeSQL succeeded with no driver registered")
	}
}
