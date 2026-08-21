package sqldb

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustProduct(t *testing.T, name string) Product {
	t.Helper()
	p, ok := ProductByName(name)
	if !ok {
		t.Fatalf("no product %q", name)
	}
	return p
}

// A query's rows come back as column-keyed maps, with the driver's []byte and
// time.Time normalized into values a process variable can actually hold.
func TestQueryNormalizesDriverValues(t *testing.T) {
	when := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id", "mail", "aktiv", "eintritt", "notiz"},
		rows: [][]driver.Value{
			{int64(7), []byte("arno@example.com"), true, when, nil},
		},
	})
	rows, err := c.Query(context.Background(), "SELECT ...", nil, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got["id"] != int64(7) {
		t.Errorf("id = %#v, want int64(7)", got["id"])
	}
	// []byte would otherwise be base64 in the variable's JSON.
	if got["mail"] != "arno@example.com" {
		t.Errorf("mail = %#v, want a string", got["mail"])
	}
	if got["aktiv"] != true {
		t.Errorf("aktiv = %#v", got["aktiv"])
	}
	if got["eintritt"] != when.Format(time.RFC3339Nano) {
		t.Errorf("eintritt = %#v, want an RFC 3339 string", got["eintritt"])
	}
	if v, ok := got["notiz"]; !ok || v != nil {
		t.Errorf("notiz = %#v, want a present nil", v)
	}
}

// The cap fails rather than truncates: a short result set is a wrong answer, and a
// process branching on the row count would branch on it confidently.
func TestQueryRowCapFailsRatherThanTruncates(t *testing.T) {
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id"},
		rows: [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}},
	})
	_, err := c.Query(context.Background(), "SELECT id FROM t", nil, 2)
	if err == nil {
		t.Fatal("a query over the cap must fail")
	}
	if !strings.Contains(err.Error(), "2-row cap") {
		t.Errorf("the error should name the cap, got: %v", err)
	}
}

// Exactly the cap is not over it.
func TestQueryAtTheCapSucceeds(t *testing.T) {
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id"},
		rows: [][]driver.Value{{int64(1)}, {int64(2)}},
	})
	rows, err := c.Query(context.Background(), "SELECT id FROM t", nil, 2)
	if err != nil {
		t.Fatalf("Query at the cap: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}

// An unset cap uses the default rather than meaning "unlimited".
func TestQueryDefaultCap(t *testing.T) {
	rows := make([][]driver.Value, DefaultMaxRows+1)
	for i := range rows {
		rows[i] = []driver.Value{int64(i)}
	}
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{cols: []string{"id"}, rows: rows})
	if _, err := c.Query(context.Background(), "SELECT id FROM t", nil, 0); err == nil {
		t.Fatalf("an unset cap must still bound the result at %d", DefaultMaxRows)
	}
}

func TestQueryAndExecErrors(t *testing.T) {
	boom := errors.New("connection reset")
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{err: boom})
	if _, err := c.Query(context.Background(), "SELECT 1", nil, 0); err == nil {
		t.Error("a driver error must fail the query")
	}
	if _, err := c.Exec(context.Background(), "DELETE FROM t", nil); err == nil {
		t.Error("a driver error must fail the exec")
	}
}

func TestExecReportsAffectedRows(t *testing.T) {
	c := openFake(t, mustProduct(t, "mariadb"), &fakeResult{affected: 3})
	n, err := c.Exec(context.Background(), "DELETE FROM sperren WHERE id = ?", []any{int64(9)})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if n != 3 {
		t.Errorf("affected = %d, want 3", n)
	}
}

// Parameters reach the driver as bound arguments, never spliced into the statement.
func TestParametersAreBoundNotInterpolated(t *testing.T) {
	r := &fakeResult{cols: []string{"id"}, rows: [][]driver.Value{{int64(1)}}}
	c := openFake(t, mustProduct(t, "postgres"), r)
	const stmt = "SELECT id FROM personen WHERE name = $1"
	if _, err := c.Query(context.Background(), stmt, []any{"Robert'); DROP TABLE personen;--"}, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
	fakeMu.Lock()
	gotStmt, gotArgs := r.gotStmt, append([]driver.NamedValue(nil), r.gotArgs...)
	fakeMu.Unlock()
	if gotStmt != stmt {
		t.Errorf("the driver saw a modified statement: %q", gotStmt)
	}
	if len(gotArgs) != 1 || gotArgs[0].Value != "Robert'); DROP TABLE personen;--" {
		t.Errorf("args = %#v, want the value bound verbatim", gotArgs)
	}
}

// scanRow reports a scan failure rather than yielding a half-filled row.
func TestScanRowError(t *testing.T) {
	_, err := scanRow(failingScanner{}, []string{"a"})
	if err == nil {
		t.Fatal("a scan error must surface")
	}
}

type failingScanner struct{}

func (failingScanner) Scan(...any) error { return fmt.Errorf("bad column") }

// A driver that cannot report a count is not a failed statement: the rows were
// affected, only the bookkeeping is missing.
func TestExecWithoutRowCountReportsZero(t *testing.T) {
	if n, err := rowsAffectedOf(noCountResult{}); err != nil || n != 0 {
		t.Errorf("rowsAffectedOf = %d, %v; want 0, nil", n, err)
	}
}

// rowsAffectedOf mirrors Exec's handling of a driver that cannot count, so the
// branch is assertable without a second fake driver.
func rowsAffectedOf(res driver.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func TestProductTable(t *testing.T) {
	for _, name := range []string{"mssql", "mariadb", "postgres"} {
		p, ok := ProductByName(name)
		if !ok {
			t.Fatalf("ProductByName(%q) missing", name)
		}
		if p.Driver == "" || p.JobType == "" || p.Placeholder == "" {
			t.Errorf("product %q is incompletely defined: %+v", name, p)
		}
		q, ok := ProductByJobType(p.JobType)
		if !ok || q.Name != p.Name {
			t.Errorf("ProductByJobType(%q) = %+v, %v", p.JobType, q, ok)
		}
	}
	if _, ok := ProductByName("Postgres "); !ok {
		t.Error("a product name should be matched case- and space-insensitively")
	}
	if _, ok := ProductByName("db2"); ok {
		t.Error("db2 has no pure-Go driver and must not be offered (ADR-0010/0170)")
	}
	if _, ok := ProductByJobType("io.atlas.nope"); ok {
		t.Error("an unknown job type must not resolve to a product")
	}
	if got := ProductNames(); len(got) != 3 || got[0] != "mariadb" {
		t.Errorf("ProductNames() = %v, want three sorted names", got)
	}
	if err := UnknownProduct("db2"); !strings.Contains(err.Error(), "mssql") {
		t.Errorf("UnknownProduct should list what is available, got: %v", err)
	}
}

// A cursor that fails mid-read is a failed query, not a short one.
func TestQueryCursorError(t *testing.T) {
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id"}, nextErr: errors.New("connection lost"),
	})
	if _, err := c.Query(context.Background(), "SELECT id FROM t", nil, 0); err == nil {
		t.Fatal("a cursor error must fail the query")
	}
}

// A driver that ran the statement but cannot count the rows it touched reports zero
// rather than failing: the effect happened, only the bookkeeping is missing.
func TestExecNoRowCountIsNotAFailure(t *testing.T) {
	c := openFake(t, mustProduct(t, "mariadb"), &fakeResult{noCount: true})
	n, err := c.Exec(context.Background(), "DELETE FROM t", nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if n != 0 {
		t.Errorf("affected = %d, want 0", n)
	}
}

// normalize leaves JSON-native scalars alone.
func TestNormalizePassesScalarsThrough(t *testing.T) {
	for _, v := range []any{int64(1), 1.5, true, "x"} {
		if got := normalize(v); got != v {
			t.Errorf("normalize(%#v) = %#v", v, got)
		}
	}
	if got := normalize(nil); got != nil {
		t.Errorf("normalize(nil) = %#v", got)
	}
}
