package sqldb

import (
	"context"
	"strings"
	"testing"
)

// mockClient opens a client of the mssql product against a mock database, which is
// the combination mock mode actually builds.
func mockClient(t *testing.T, answers ...MockAnswer) (*Client, *MockDatabase) {
	t.Helper()
	return mockClientOf(t, "mssql", answers...)
}

// mockClientOf is the same for a named product. Mock mode is one code path for all
// three (ADR-0221), so what differs between them is worth stating per product rather
// than assuming: the driver behind the pool, and the seed variable the refusal names.
func mockClientOf(t *testing.T, product string, answers ...MockAnswer) (*Client, *MockDatabase) {
	t.Helper()
	m := NewMockDatabase(mustProduct(t, product), answers...)
	c := OpenMock(m)
	t.Cleanup(func() { _ = c.Close() })
	return c, m
}

// A seeded query answers with its rows, through the same database/sql machinery and
// the same value normalization a real driver's result goes through.
func TestMockAnswersASeededQuery(t *testing.T) {
	c, _ := mockClient(t, MockAnswer{
		Statement: "SELECT id, mail FROM personen WHERE abteilung = @p1",
		Columns:   []string{"id", "mail"},
		Rows:      [][]any{{7, "arno@example.com"}, {9, "bea@example.com"}},
	})
	rows, err := c.Query(context.Background(), "SELECT id, mail FROM personen WHERE abteilung = @p1", []any{"IT"}, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(rows), rows)
	}
	// A seeded 7 must arrive as an integer, not as the 7e+00 a JSON round trip
	// through float64 would produce — an id that does not compare is the whole
	// class of bug the real connector's jsonScalar exists to prevent.
	if got := rows[0]["id"]; got != int64(7) {
		t.Errorf("id = %#v, want int64(7)", got)
	}
	if got := rows[0]["mail"]; got != "arno@example.com" {
		t.Errorf("mail = %#v", got)
	}
}

// Whitespace and case are not part of a statement's identity: the modeler wraps a
// statement across lines and SQL Server does not distinguish SELECT from select, so a
// seed that differs in either still answers. Nothing else is forgiven.
func TestMockMatchesAcrossWhitespaceAndCase(t *testing.T) {
	c, _ := mockClient(t, MockAnswer{
		Statement: "select COUNT(*) as n from personen",
		Columns:   []string{"n"},
		Rows:      [][]any{{3}},
	})
	rows, err := c.Query(context.Background(), "SELECT count(*) AS n\n  FROM personen", nil, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0]["n"] != int64(3) {
		t.Fatalf("got %v, want one row with n=3", rows)
	}
}

// An answer that names the parameters it is for answers only that binding, and one
// that names none answers the rest. That is what lets a seed teach "person 42 exists
// and person 7 does not" without a SQL engine behind it.
func TestMockPicksTheAnswerForTheBoundParameters(t *testing.T) {
	const stmt = "SELECT mail FROM personen WHERE id = @p1"
	c, _ := mockClient(t,
		MockAnswer{Statement: stmt, Params: []any{42}, Columns: []string{"mail"}, Rows: [][]any{{"arno@example.com"}}},
		MockAnswer{Statement: stmt, Columns: []string{"mail"}},
	)
	rows, err := c.Query(context.Background(), stmt, []any{int64(42)}, 0)
	if err != nil {
		t.Fatalf("Query(42): %v", err)
	}
	if len(rows) != 1 || rows[0]["mail"] != "arno@example.com" {
		t.Fatalf("Query(42) = %v, want arno", rows)
	}
	rows, err = c.Query(context.Background(), stmt, []any{int64(7)}, 0)
	if err != nil {
		t.Fatalf("Query(7): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("Query(7) = %v, want the catch-all answer's no rows", rows)
	}
}

// SQL Server's named binding is the one thing this product has that the other two do
// not, so the mock has to match on it too.
func TestMockMatchesNamedParameters(t *testing.T) {
	const stmt = "UPDATE personen SET aktiv = 0 WHERE id = @id"
	c, _ := mockClient(t,
		MockAnswer{Statement: stmt, Named: map[string]any{"id": 42}, Affected: 1},
		MockAnswer{Statement: stmt, Affected: 0},
	)
	j := Job{Connector: "hr", Product: "mssql", Operation: "execute", Statement: stmt,
		Named: map[string]any{"id": int64(42)}, ResultVariable: "n"}
	reg := NewRegistry()
	reg.Register("hr", c)
	out, err := Run(context.Background(), j, reg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["n"] != int64(1) {
		t.Errorf("affected = %#v, want 1 — the named binding did not select the seeded answer", out["n"])
	}
}

// The refusal that makes the mock worth trusting. A statement nobody seeded is not
// answered with no rows: an empty result is a business answer, and a process that
// branches on it would branch on something the mock invented.
func TestMockRefusesAnUnseededStatement(t *testing.T) {
	// Every product refuses the same way and names its *own* seed variable. A message
	// quoting a pattern like ATLAS_<PRODUCT>_MOCK_SEED leaves the reader to apply the
	// substitution in their head, which is exactly where "I set the variable and it
	// still says it is missing" comes from.
	for _, product := range ProductNames() {
		t.Run(product, func(t *testing.T) {
			c, _ := mockClientOf(t, product, MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}})
			_, err := c.Query(context.Background(), "SELECT mail FROM personen WHERE id = @p1", []any{int64(42)}, 0)
			if err == nil {
				t.Fatal("an unseeded statement returned rows; a mock that guesses teaches a model to be wrong")
			}
			// The message has to carry what to seed, or the operator is left diffing.
			seedVar := mustProduct(t, product).MockSeedEnv()
			for _, want := range []string{"SELECT mail FROM personen WHERE id = @p1", "42", seedVar} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// A seed can make a statement fail, because the failures a database has are part of
// what a process must be tried against — a constraint violation is not an edge case
// in an identity process, it is Tuesday.
func TestMockCanSeedAFailure(t *testing.T) {
	c, _ := mockClient(t, MockAnswer{
		Statement: "INSERT INTO personen (mail) VALUES (@p1)",
		Error:     "Violation of UNIQUE KEY constraint 'UQ_personen_mail'",
	})
	_, err := c.Exec(context.Background(), "INSERT INTO personen (mail) VALUES (@p1)", []any{"arno@example.com"})
	if err == nil || !strings.Contains(err.Error(), "UQ_personen_mail") {
		t.Fatalf("Exec error = %v, want the seeded constraint violation", err)
	}
}

// What the mock leaves behind: every statement a process actually ran, with the values
// it bound. It is the answer to "what would this process have done to the database",
// which is the question a mockup run exists to answer.
func TestMockJournalsWhatAProcessRan(t *testing.T) {
	c, m := mockClient(t,
		MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}},
		MockAnswer{Statement: "UPDATE personen SET aktiv = 0 WHERE id = @p1", Affected: 1},
	)
	if _, err := c.Query(context.Background(), "SELECT 1", nil, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if _, err := c.Exec(context.Background(), "UPDATE personen SET aktiv = 0 WHERE id = @p1", []any{int64(42)}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	ran := m.Statements()
	if len(ran) != 2 {
		t.Fatalf("journal has %d entries, want 2: %v", len(ran), ran)
	}
	if ran[1].Statement != "UPDATE personen SET aktiv = 0 WHERE id = @p1" {
		t.Errorf("journal[1].Statement = %q", ran[1].Statement)
	}
	if len(ran[1].Params) != 1 || ran[1].Params[0] != int64(42) {
		t.Errorf("journal[1].Params = %#v, want [42]", ran[1].Params)
	}
	if ran[0].Seq >= ran[1].Seq {
		t.Errorf("journal is not in order: %d then %d", ran[0].Seq, ran[1].Seq)
	}
}

// A long-running mock worker must not grow into the memory of the database it stands
// in for, so the journal keeps the newest and drops the rest.
func TestMockJournalIsBounded(t *testing.T) {
	c, m := mockClient(t, MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}})
	for i := 0; i < maxMockStatements+5; i++ {
		if _, err := c.Query(context.Background(), "SELECT 1", nil, 0); err != nil {
			t.Fatalf("Query %d: %v", i, err)
		}
	}
	ran := m.Statements()
	if len(ran) != maxMockStatements {
		t.Fatalf("journal has %d entries, want the %d cap", len(ran), maxMockStatements)
	}
	if ran[len(ran)-1].Seq != uint64(maxMockStatements+5) {
		t.Errorf("the newest entry is %d; the journal dropped the wrong end", ran[len(ran)-1].Seq)
	}
}

// The seed file is what an operator writes, so its errors have to name the file's own
// vocabulary rather than a Go type's.
func TestParseMockSeed(t *testing.T) {
	answers, err := ParseMockSeed([]byte(`{"answers":[
	  {"statement":"SELECT id FROM personen","columns":["id"],"rows":[[1000000]]},
	  {"statement":"DELETE FROM personen WHERE id = @p1","params":[7],"affected":1}
	]}`))
	if err != nil {
		t.Fatalf("ParseMockSeed: %v", err)
	}
	if len(answers) != 2 {
		t.Fatalf("got %d answers, want 2", len(answers))
	}
	// A seeded id must survive the file as an integer: 1000000 read as a float64 and
	// written back is 1e+06, which matches nothing.
	if got := answers[0].Rows[0][0]; got != int64(1000000) {
		t.Errorf("seeded row value = %#v, want int64(1000000)", got)
	}
	if got := answers[1].Params[0]; got != int64(7) {
		t.Errorf("seeded parameter = %#v, want int64(7)", got)
	}
}

func TestParseMockSeedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, seed, want string
	}{
		{"not json", `{`, "is not valid JSON"},
		{"no statement", `{"answers":[{"columns":["id"]}]}`, "needs a statement"},
		{"row is not as wide as the columns", `{"answers":[{"statement":"SELECT id, mail FROM p","columns":["id","mail"],"rows":[[1]]}]}`, "columns"},
		{"rows without columns", `{"answers":[{"statement":"SELECT id FROM p","rows":[[1]]}]}`, "columns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMockSeed([]byte(tc.seed))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// A mock database answers a ping, so the Console's connection check says the same
// thing about a mock worker that it says about a real one: this is reachable.
func TestMockAnswersAPing(t *testing.T) {
	c, _ := mockClient(t)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// A mock is one statement at a time, like the connector itself: there is no
// transaction to span two tasks (ADR-0173), so pretending to start one would be the
// mock teaching a model something the real connector will not do.
func TestMockHasNoTransactions(t *testing.T) {
	c, _ := mockClient(t)
	if _, err := c.db.BeginTx(context.Background(), nil); err == nil {
		t.Fatal("the mock started a transaction the real connector has no way to offer")
	}
}

// The unseeded-statement error names named parameters too, and in a stable order — an
// operator reads it to write the seed, and a map's iteration order would make the same
// failure read differently every time.
func TestMockUnseededErrorNamesNamedParameters(t *testing.T) {
	c, _ := mockClient(t)
	j := Job{Connector: "hr", Product: "mssql", Operation: "execute", Statement: "UPDATE p SET a = @a WHERE id = @id",
		Named: map[string]any{"id": int64(42), "a": false}}
	reg := NewRegistry()
	reg.Register("hr", c)
	_, err := Run(context.Background(), j, reg)
	if err == nil {
		t.Fatal("an unseeded statement succeeded")
	}
	if !strings.Contains(err.Error(), "a=false, id=42") {
		t.Errorf("error %q does not carry the bound names in order", err)
	}
}

// A binding that differs in length from the seeded one is a different binding, not a
// near miss: a statement called with two parameters is not the one seeded for three.
func TestMockBindingLengthMustMatch(t *testing.T) {
	const stmt = "SELECT mail FROM personen WHERE id = @p1 AND aktiv = @p2"
	c, _ := mockClient(t,
		MockAnswer{Statement: stmt, Params: []any{42, true}, Columns: []string{"mail"}, Rows: [][]any{{"arno@example.com"}}},
		MockAnswer{Statement: stmt, Named: map[string]any{"id": 42}, Columns: []string{"mail"}, Rows: [][]any{{"named@example.com"}}},
	)
	if _, err := c.Query(context.Background(), stmt, []any{int64(42)}, 0); err == nil {
		t.Error("a one-parameter call matched a two-parameter answer")
	}
	if _, err := c.Query(context.Background(), stmt, []any{int64(42), false}, 0); err == nil {
		t.Error("a call binding false matched an answer seeded for true")
	}
}

// A seed and a binding meet across the types JSON and a driver each have: a seeded
// 42 is an int64(42) a driver bound, and a seeded "x" is the []byte a driver hands
// back for text. Without that a seed would have to be written per driver.
func TestMockSeedAndBindingMeetAcrossTypes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seed, bound any
		want        bool
	}{
		{"integral float and int64", float64(42), int64(42), true},
		{"fractional float", 1.5, 1.5, true},
		{"text as bytes", "arno", []byte("arno"), true},
		{"bool", true, true, true},
		{"both null", nil, nil, true},
		{"null against a value", nil, int64(1), false},
		{"value against null", "x", nil, false},
		{"different numbers", int64(42), int64(43), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameValue(tc.seed, tc.bound); got != tc.want {
				t.Errorf("sameValue(%#v, %#v) = %v, want %v", tc.seed, tc.bound, got, tc.want)
			}
		})
	}
}

// A seeded value the driver protocol has no place for is rendered as text rather than
// refused, because the alternative is a seed file that fails on a value a database
// would simply have returned as a string.
func TestMockSeededValuesNarrowToDriverTypes(t *testing.T) {
	for _, tc := range []struct{ in, want any }{
		{nil, nil},
		{7, int64(7)},
		{int64(7), int64(7)},
		{1.5, 1.5},
		{"x", "x"},
		{true, true},
		{[]any{1, 2}, "[1 2]"},
	} {
		if got := mockDriverValue(tc.in); got != tc.want {
			t.Errorf("mockDriverValue(%#v) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// A mock database is never addressed by a connection string, because there is no
// server to address. The database/sql seam still asks for a driver, so it says so
// rather than returning a connection to nothing.
func TestMockHasNoConnectionString(t *testing.T) {
	m := NewMockDatabase(mustProduct(t, "mssql"))
	d := m.Driver()
	if _, err := d.Open("sqlserver://somewhere"); err == nil {
		t.Error("the mock driver opened a connection string")
	}
	conn, err := m.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := conn.Prepare("SELECT 1"); err == nil {
		t.Error("the mock prepared a statement; database/sql must reach QueryContext instead")
	}
	if err := conn.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// An execute reports the affected rows and nothing else. A mock has no identity
// column, so the id a real INSERT would produce is refused rather than invented —
// the connector never reads it, and a made-up key is the kind of value that escapes
// into a process variable and is believed.
func TestMockReportsNoInsertedIdentity(t *testing.T) {
	c, _ := mockClient(t, MockAnswer{Statement: "INSERT INTO personen (mail) VALUES (@p1)", Affected: 1})
	n, err := c.Exec(context.Background(), "INSERT INTO personen (mail) VALUES (@p1)", []any{"arno@example.com"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if n != 1 {
		t.Errorf("affected = %d, want 1", n)
	}
	if _, err := mockResult(1).LastInsertId(); err == nil {
		t.Error("the mock reported an inserted identity it cannot know")
	}
}

// A ping that cannot reach the database is reported as a connection failure, which is
// what the Console's check turns into a sentence for whoever typed the address.
func TestPingReportsAnUnreachableDatabase(t *testing.T) {
	c := openFake(t, fakeProduct, &fakeResult{})
	// The fake's connections are handed out without error, so drop the pool to make
	// PingContext meet a closed one — the shape of an unreachable database.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "sqldb: connect") {
		t.Fatalf("Ping = %v, want a connect failure", err)
	}
}
