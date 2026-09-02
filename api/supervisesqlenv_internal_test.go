package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/worker"
)

// What a supervised SQL worker is handed at spawn, and what it must never be handed.
//
// These kinds are worker-only for a stronger reason than Entra: a DSN *is* a
// credential, so there is no public half to inherit and nothing to bridge — the whole
// connection string is rendered out of the vault or not at all
// (ADR-0188).

// A database an operator added in the Console: the worker is handed its connection
// string under the name it reads, plus the CONNECTORS list naming it.
//
// All three products, and each one round-tripped through the worker that has to read
// what was rendered. The two halves live in different packages and agree only because
// both ask sqldb.Product for the variable names; a product that grew its own spelling
// on either side would hand a supervised worker a credential under a name nothing
// reads, and the worker would report the database as unconfigured with the DSN sitting
// right there in its environment.
func TestASupervisedSQLWorkerGetsItsConnectionStringFromTheVault(t *testing.T) {
	// One DSN per product, in that product's own grammar: two of the three drivers
	// parse the string when the worker opens it, so a postgres:// URL handed to
	// MariaDB would fail this for a reason that has nothing to do with what is being
	// tested.
	dsns := map[string]string{
		connectorKindPostgres: "postgres://u:p@db.example:5432/hr",
		connectorKindMariaDB:  "u:p@tcp(db.example:3306)/hr",
		connectorKindMSSQL:    "sqlserver://u:p@db.example:1433?database=hr",
	}
	for _, kind := range sqlConnectorKinds() {
		t.Run(kind, func(t *testing.T) {
			p, ok := sqldb.ProductByName(kind)
			if !ok {
				t.Fatalf("connector kind %q is not a sqldb product", kind)
			}
			dsn := dsns[kind]
			if dsn == "" {
				t.Fatalf("this test has no connection string for %q; a new product needs one in its own grammar", kind)
			}
			srv, _ := newValidateServer(t)
			if _, err := srv.vault.Set("sql/1/dsn", dsn); err != nil {
				t.Fatalf("vault.Set: %v", err)
			}
			if err := srv.connectors.Save(connector{
				ID: "1", Name: "hr-db", Kind: kind,
				CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1,
			}); err != nil {
				t.Fatalf("connectors.Save: %v", err)
			}

			env := envOf(t, srv.sqlWorkerEnvByName(kind))
			if got := env[p.DSNEnv("hr-db")]; got != dsn {
				t.Errorf("%s = %q, want the DSN out of the vault", p.DSNEnv("hr-db"), got)
			}
			if got := env[p.ConnectorsEnv()]; got != "hr-db" {
				t.Errorf("%s = %q, want hr-db", p.ConnectorsEnv(), got)
			}

			// The worker's half of the same contract: what was rendered has to build a
			// worker that actually holds the database.
			built, err := worker.BuiltinConnectors(envMapFrom(env), kind)
			if err != nil {
				t.Fatalf("the rendered environment must be one the worker accepts: %v", err)
			}
			if !slices.Contains(built.Names, "hr-db") {
				t.Errorf("names = %v, want the configured database served", built.Names)
			}
			if _, ok := built.Handlers[p.JobType]; !ok {
				t.Errorf("no handler for %s; the supervised worker would serve nothing", p.JobType)
			}
		})
	}
}

// A record whose secret is not set yet is left out — and, critically, its name is left
// out of CONNECTORS as well. A name in CONNECTORS with no DSN behind it is exactly the
// misconfiguration the worker refuses to start on.
//
// The scenario is deliberately two connectors, not one. With a single unconfigured
// record the CONNECTORS line is never rendered at all, so the defect hides. It appears
// the moment a *working* database exists and the operator starts adding a second one:
// the record is created before its secret is, and for that window the rendered
// CONNECTORS would name a database with no DSN behind it — which stops the worker that
// was happily serving the first one.
func TestASQLConnectorWithNoSecretYetIsLeftOutOfCONNECTORS(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "postgres://u:p@db.example:5432/hr"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindPostgres,
		CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	// Half-created: the record exists, the secret does not yet.
	if err := srv.connectors.Save(connector{
		ID: "2", Name: "crm-db", Kind: connectorKindPostgres,
		CredentialsRef: "sql/2/dsn", Enabled: true, CreatedAt: 2,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}

	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindPostgres))
	if _, ok := env["ATLAS_POSTGRES_CRM_DB_DSN"]; ok {
		t.Error("a connector with no stored secret must not be handed over with an empty DSN")
	}
	if got := env["ATLAS_POSTGRES_CONNECTORS"]; strings.Contains(got, "crm-db") {
		t.Errorf("ATLAS_POSTGRES_CONNECTORS = %q; a name with no DSN behind it is what the worker refuses to start on", got)
	}
	if got := env["ATLAS_POSTGRES_CONNECTORS"]; got != "hr-db" {
		t.Errorf("ATLAS_POSTGRES_CONNECTORS = %q, want just the database that is actually configured", got)
	}

	// And the worker agrees: the environment this produced must start one that serves
	// the working database, rather than refusing to boot over the half-created one.
	built, err := worker.BuiltinConnectors(envMapFrom(env), connectorKindPostgres)
	if err != nil {
		t.Fatalf("the rendered environment must not be one the worker refuses: %v", err)
	}
	if !slices.Contains(built.Names, "hr-db") {
		t.Errorf("names = %v, want the configured database served", built.Names)
	}
}

// A DSN the operator set on the host is not overridden: the child inherits it, and a
// stale vault entry must not silently win over an explicit choice.
func TestAnOperatorSetSQLDSNIsNotOverriddenByTheVault(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "postgres://from-vault"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindPostgres,
		CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	t.Setenv("ATLAS_POSTGRES_HR_DB_DSN", "postgres://from-operator")

	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindPostgres))
	if _, ok := env["ATLAS_POSTGRES_HR_DB_DSN"]; ok {
		t.Error("the vault must not render a DSN the operator set on the host")
	}
	// The name still belongs in CONNECTORS: the child has the DSN by inheritance.
	if got := env["ATLAS_POSTGRES_CONNECTORS"]; got != "hr-db" {
		t.Errorf("ATLAS_POSTGRES_CONNECTORS = %q, want hr-db", got)
	}
}

// A disabled connector is not handed over at all.
func TestADisabledSQLConnectorIsNotHandedToTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "postgres://u:p@db.example/hr"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindPostgres,
		CredentialsRef: "sql/1/dsn", Enabled: false, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	if env := srv.sqlWorkerEnvByName(connectorKindPostgres); len(env) != 0 {
		t.Errorf("environment = %v, want nothing for a disabled connector", env)
	}
}

// One product's databases never reach another product's worker: a MariaDB record must
// not render into ATLAS_POSTGRES_*, which would hand a `?`-placeholder database to a
// worker whose statements use `$1`.
func TestASQLConnectorIsOnlyHandedToItsOwnProductsWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "u:p@tcp(db.example:3306)/hr"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindMariaDB,
		CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	if env := srv.sqlWorkerEnvByName(connectorKindPostgres); len(env) != 0 {
		t.Errorf("the postgres worker was handed %v, which is a MariaDB database", env)
	}
	if env := envOf(t, srv.sqlWorkerEnvByName(connectorKindMariaDB)); env["ATLAS_MARIADB_HR_DB_DSN"] == "" {
		t.Error("the mariadb worker was not handed its own database")
	}
}

// The Console needs to say which database a connector points at, and must never say
// the password. Deriving a label is allowed where assembling a DSN is not, precisely
// because this may give up: anything that is not a URL with a host yields no label.
func TestRedactedSQLTargetNeverCarriesThePassword(t *testing.T) {
	for _, tc := range []struct{ name, dsn, want string }{
		{"postgres url", "postgres://alice:hunter2@db.example:5432/hr", "alice@db.example:5432/hr"},
		{"no user", "postgres://db.example:5432/hr", "db.example:5432/hr"},
		{"query dropped", "sqlserver://sa:hunter2@db.example:1433?database=hr&password=hunter2", "sa@db.example:1433"},
		{"mysql keyword form gives up", "u:hunter2@tcp(db.example:3306)/hr", ""},
		{"not a dsn at all", "hunter2", ""},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := redactedSQLTarget(tc.dsn)
			if got != tc.want {
				t.Errorf("redactedSQLTarget(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
			if strings.Contains(got, "hunter2") {
				t.Errorf("the label leaked the password: %q", got)
			}
		})
	}
}

// Every SQL kind in the connector store must be a real sqldb product, and every kind
// the supervised-by-default list names must be one the engine can actually provision.
// The two tables are edited in different files, and a name that appears in one and not
// the other is a worker started for a kind nothing configures, or a connector nobody
// serves.
func TestEverySQLKindIsAProduct(t *testing.T) {
	for _, kind := range sqlConnectorKinds() {
		if _, ok := sqldb.ProductByName(kind); !ok {
			t.Errorf("connector kind %q is not a sqldb product", kind)
		}
		if _, ok := lookupManagedConnectorKind(kind); !ok {
			t.Errorf("connector kind %q is not registered in managedConnectorKinds", kind)
		}
		if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), kind) {
			t.Errorf("connector kind %q is not supervised by default; nothing would serve it", kind)
		}
		if !slices.Contains(worker.KnownConnectorKinds(), kind) {
			t.Errorf("connector kind %q is not a kind `atlas worker --connector` implements", kind)
		}
	}
	// Every default-supervised kind must be provisioned from the store, or the worker
	// Atlas starts holds nothing to serve with.
	srv, _ := newValidateServer(t)
	provisioned := srv.provisionedConnectorKinds()
	for _, kind := range DefaultSupervisedWorkerOnlyKinds() {
		if provisioned[kind] == nil {
			t.Errorf("kind %q is supervised by default but nothing renders its configuration", kind)
		}
	}
}

// envMapFrom turns a rendered environment into the lookup worker.BuiltinConnectors
// takes, so a test can assert that what the engine renders is something the worker
// actually accepts — the two halves are in different packages and drift silently.
func envMapFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// Creating a SQL connector from the Console: the operator pastes a connection string,
// and what lands in the record is a vault reference and a redacted label — never the
// string. This is the property the whole design rests on, so it is asserted against
// the stored record and the response body, not against the handler's intent.
func TestCreatingASQLConnectorSealsTheConnectionString(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	const dsn = "postgres://postgres.abc:hunter2@aws-0-eu-west-1.pooler.supabase.com:5432/postgres?sslmode=require"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors",
		strings.NewReader(`{"name":"supabase","kind":"postgres","connectionString":"`+dsn+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("the response echoed the password back: %s", rec.Body.String())
	}

	recs, err := srv.connectors.LoadAll()
	if err != nil || len(recs) != 1 {
		t.Fatalf("LoadAll = %v, %v", recs, err)
	}
	got := recs[0]
	if got.CredentialsRef != sqlDSNRef(got.ID) {
		t.Errorf("credentialsRef = %q, want the derived vault key %q", got.CredentialsRef, sqlDSNRef(got.ID))
	}
	if got.Endpoint != "postgres.abc@aws-0-eu-west-1.pooler.supabase.com:5432/postgres" {
		t.Errorf("endpoint = %q, want the redacted target", got.Endpoint)
	}
	// The record is a JSON file on disk; the credential must not be anywhere in it.
	if blob, _ := json.Marshal(got); strings.Contains(string(blob), "hunter2") {
		t.Errorf("the stored record carries the credential: %s", blob)
	}
	// And the sealed value is what the worker is handed.
	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindPostgres))
	if env["ATLAS_POSTGRES_SUPABASE_DSN"] != dsn {
		t.Errorf("ATLAS_POSTGRES_SUPABASE_DSN = %q, want the sealed connection string", env["ATLAS_POSTGRES_SUPABASE_DSN"])
	}
}

// A connectionString sent to a kind that has no use for one is refused rather than
// silently dropped: an operator who pastes a DSN into a mail connector has made a
// mistake worth hearing about.
func TestAConnectionStringIsRefusedForANonSQLKind(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors",
		strings.NewReader(`{"name":"m","kind":"mail","endpoint":"smtp.x:587","connectionString":"postgres://u:p@h/db"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create = %d, want 400 — connectionString applies only to a SQL connector", rec.Code)
	}
}

// A SQL connector created with neither a connection string nor a reference is refused:
// there is nothing else on the record that could make it work, so accepting it would
// store a connector guaranteed to park.
func TestASQLConnectorNeedsACredential(t *testing.T) {
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors",
		strings.NewReader(`{"name":"empty","kind":"postgres"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create = %d, want 400 — a SQL connector with no credential cannot work", rec.Code)
	}
}

// Why a configured SQL connector is not usable, said by the Console instead of
// discovered by a task that parks (ADR-0158). These kinds build no in-engine client,
// so what can be checked is the record and whether its secret resolves.
func TestSQLConnectorProblemReportsWhyItCannotWork(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/ok/dsn", "postgres://u:p@db/hr"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	for _, rec := range []connector{
		{ID: "ok", Name: "works", Kind: connectorKindPostgres, CredentialsRef: "sql/ok/dsn", Enabled: true, CreatedAt: 1},
		{ID: "off", Name: "disabled", Kind: connectorKindPostgres, CredentialsRef: "sql/ok/dsn", Enabled: false, CreatedAt: 2},
		{ID: "bare", Name: "secretless", Kind: connectorKindPostgres, CredentialsRef: "sql/missing/dsn", Enabled: true, CreatedAt: 3},
	} {
		if err := srv.connectors.Save(rec); err != nil {
			t.Fatalf("connectors.Save: %v", err)
		}
	}
	for _, tc := range []struct {
		name, connector, want string
		problem               bool
	}{
		{"healthy", "works", "", false},
		{"disabled", "disabled", "the connector is disabled", true},
		{"no secret", "secretless", "no connection string is stored for this connector", true},
		{"unknown name", "nothing", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, isProblem := srv.sqlConnectorProblem(connectorKindPostgres, tc.connector)
			if got != tc.want || isProblem != tc.problem {
				t.Errorf("sqlConnectorProblem(%q) = %q, %v; want %q, %v", tc.connector, got, isProblem, tc.want, tc.problem)
			}
		})
	}
	// A record of another product is not this product's problem to report.
	if _, isProblem := srv.sqlConnectorProblem(connectorKindMariaDB, "works"); isProblem {
		t.Error("a postgres record was reported as a mariadb connector's problem")
	}
}

// Two names that fold to one environment variable would silently give one the other's
// connection string. One wins and the other is left out — and the loser's *name* is
// left out with it, so the worker is not told to serve a database it was handed no DSN
// for.
//
// Which one wins is decided by sorting the records by name, so it is a property of the
// data and not of the store's iteration order: "hr db" sorts before "hr-db" (space
// before hyphen), so it is the one that gets the variable. That is worth pinning down,
// because a collision resolved differently on two servers would hand the same model's
// connector two different databases.
func TestTwoSQLConnectorsFoldingToOneVariableDoNotOverwriteEachOther(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "postgres://first"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if _, err := srv.vault.Set("sql/2/dsn", "postgres://second"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	for _, rec := range []connector{
		{ID: "1", Name: "hr-db", Kind: connectorKindPostgres, CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "hr db", Kind: connectorKindPostgres, CredentialsRef: "sql/2/dsn", Enabled: true, CreatedAt: 2},
	} {
		if err := srv.connectors.Save(rec); err != nil {
			t.Fatalf("connectors.Save: %v", err)
		}
	}
	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindPostgres))
	if got := env["ATLAS_POSTGRES_HR_DB_DSN"]; got != "postgres://second" {
		t.Errorf("ATLAS_POSTGRES_HR_DB_DSN = %q, want the sorted-first record's DSN and nothing else", got)
	}
	if got := env["ATLAS_POSTGRES_CONNECTORS"]; got != "hr db" {
		t.Errorf("ATLAS_POSTGRES_CONNECTORS = %q, want only the name that got a DSN", got)
	}
}

// A kind that is not a SQL product renders nothing rather than panicking on a nil
// product: the provisioned-kinds table and the product table live in different files,
// and TestEverySQLKindIsAProduct is what keeps them honest.
func TestSQLWorkerEnvForAKindThatIsNotAProductRendersNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	if env := srv.sqlWorkerEnvByName("oracle"); env != nil {
		t.Errorf("sqlWorkerEnvByName(\"oracle\") = %v, want nil", env)
	}
}

// A name with nothing alphanumeric in it folds to an empty variable name, which would
// render ATLAS_POSTGRES__DSN — a variable the worker does not read, under a name no
// model can meaningfully reference. It is skipped, and the connector simply does not
// reach the worker.
func TestASQLConnectorWhoseNameFoldsToNothingIsSkipped(t *testing.T) {
	srv, _ := newValidateServer(t)
	if _, err := srv.vault.Set("sql/1/dsn", "postgres://u:p@db/hr"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "---", Kind: connectorKindPostgres,
		CredentialsRef: "sql/1/dsn", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindPostgres))
	if _, ok := env["ATLAS_POSTGRES__DSN"]; ok {
		t.Error("a name that folds to nothing produced a nameless variable")
	}
	if len(env) != 0 {
		t.Errorf("environment = %v, want nothing", env)
	}
}

// The Console switch has to reach the worker, or it is a checkbox that does nothing.
// Turning the mockup on renders the variable for every SQL product, plus the file
// Atlas wrote the seed to — a path Atlas owns, because the Console is org-wide and a
// path typed there belongs to whichever host happens to run the worker.
func TestTheMockupSwitchReachesTheSupervisedSQLWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() {
		if err := srv.settings.saveSQLMock(sqlMockSetting{Enabled: true, Seed: sqlMockSeedJSON, SeedName: "hr.json"}); err != nil {
			t.Fatalf("saveSQLMock: %v", err)
		}
	})

	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindMSSQL))
	if got := env["ATLAS_MSSQL_MOCK"]; got != "1" {
		t.Errorf("ATLAS_MSSQL_MOCK = %q, want 1", got)
	}
	seed := env["ATLAS_MSSQL_MOCK_SEED"]
	if seed == "" {
		t.Fatalf("env = %v, want a seed file for the worker to read", env)
	}
	data, err := os.ReadFile(seed)
	if err != nil {
		t.Fatalf("the worker is pointed at %q, which it cannot read: %v", seed, err)
	}
	if string(data) != sqlMockSeedJSON {
		t.Errorf("the seed file holds %q, want what the operator saved", data)
	}
	// Every product, not one: mocking SQL Server while really writing to PostgreSQL is
	// the half-state a single switch exists to prevent.
	for _, kind := range []string{connectorKindMariaDB, connectorKindPostgres} {
		e := envOf(t, srv.sqlWorkerEnvByName(kind))
		if got := e["ATLAS_"+strings.ToUpper(kind)+"_MOCK"]; got != "1" {
			t.Errorf("%s: mock variable = %q, want the mockup on for it too", kind, got)
		}
	}
}

// A stored "off" says off, rather than saying nothing. A switch that reads off while
// the worker still simulates would be lying to the person who flipped it.
func TestAStoredOffTellsTheSQLWorkerToUseARealDatabase(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() { _ = srv.settings.saveSQLMock(sqlMockSetting{Enabled: false, Seed: sqlMockSeedJSON}) })

	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindMSSQL))
	if got := env["ATLAS_MSSQL_MOCK"]; got != "0" {
		t.Errorf("ATLAS_MSSQL_MOCK = %q, want 0", got)
	}
	// And no seed: a worker reaching a real database has nothing to read one for, and
	// the variable set alongside MOCK=0 is what the worker refuses to start on.
	if got := env["ATLAS_MSSQL_MOCK_SEED"]; got != "" {
		t.Errorf("ATLAS_MSSQL_MOCK_SEED = %q, want nothing handed to a worker that is not mocking", got)
	}
}

// No stored record renders nothing at all, so a server started with ATLAS_MSSQL_MOCK
// by hand keeps deciding for itself. That is what keeps an existing install working
// exactly as it did until somebody touches the switch.
func TestNoStoredSQLMockRecordLeavesTheHostEnvironmentAlone(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, v := range srv.sqlWorkerEnvByName(connectorKindMSSQL) {
		if strings.HasPrefix(v, "ATLAS_MSSQL_MOCK") {
			t.Errorf("env carries %q with nothing stored; the host's own setting must still decide", v)
		}
	}
}

// The blocker that would have made the switch useless. A worker record with no
// connection string is left out of CONNECTORS normally — a name the worker is told to
// serve with no DSN behind it is exactly the misconfiguration it refuses to start on
// (the test above). In mockup mode there is no DSN to have, so leaving the name out
// would mean the mockup serves nothing a task can address.
func TestInMockupModeASQLWorkerWithNoDSNIsStillServed(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindMSSQL, Enabled: true, CreatedAt: 1,
		// No CredentialsRef: nothing to resolve, which is the whole point.
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	srv.do(func() { _ = srv.settings.saveSQLMock(sqlMockSetting{Enabled: true, Seed: sqlMockSeedJSON}) })

	env := envOf(t, srv.sqlWorkerEnvByName(connectorKindMSSQL))
	if got := env["ATLAS_MSSQL_CONNECTORS"]; got != "hr-db" {
		t.Errorf("ATLAS_MSSQL_CONNECTORS = %q, want hr-db — the mockup has no name to answer to otherwise", got)
	}
	if got := env["ATLAS_MSSQL_HR_DB_DSN"]; got != "" {
		t.Errorf("a DSN was rendered in mockup mode: %q", got)
	}
}

// And with the mockup off it stays out, unchanged.
func TestWithTheMockupOffASQLWorkerWithNoDSNIsStillLeftOut(t *testing.T) {
	srv, _ := newValidateServer(t)
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "hr-db", Kind: connectorKindMSSQL, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("connectors.Save: %v", err)
	}
	srv.do(func() { _ = srv.settings.saveSQLMock(sqlMockSetting{Enabled: false}) })

	if got := envOf(t, srv.sqlWorkerEnvByName(connectorKindMSSQL))["ATLAS_MSSQL_CONNECTORS"]; got != "" {
		t.Errorf("ATLAS_MSSQL_CONNECTORS = %q, want the secretless worker left out", got)
	}
}
