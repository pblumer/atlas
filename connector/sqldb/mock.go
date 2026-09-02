package sqldb

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Mock mode: an in-memory stand-in for a database.
//
// A process that reads or writes a database cannot be tried out without one, and the
// database an identity process needs is the one nobody wants a test writing into.
// [MockDatabase] is the other half of the worker (ADR-0168): the same resolved job,
// the same [Run], the same [Client] over the same database/sql machinery — against
// answers that live in the worker's memory and are thrown away when it stops.
// `atlas worker --connector <product>` with that product's ATLAS_<PRODUCT>_MOCK set
// serves SQL tasks this way, so a model runs end to end against a database that does
// not exist (ADR-0221). All three products reach it through the same code: what
// differs is only the variable prefix, which the product spells
// ([Product.MockEnv], [Product.MockSeedEnv]).
//
// The switch is the *worker's*, not the model's, for the reason the AD mock's is: a
// mockup flag on the task would be a model that behaves differently in test and in
// production, and would eventually be deployed with the flag still set. Here the model
// is byte-identical either way and what differs is which worker leases its jobs.
//
// # It answers, it does not execute
//
// This is deliberately not a SQL engine. It holds *seeded answers* — a statement, the
// parameters it is for, and the rows or affected count it produces — and matches an
// incoming statement against them. Writing a SQL engine to stand in for SQL Server is
// how a mock ends up quietly disagreeing with the thing it stands in for, in the
// dialect corners (collation, implicit conversion, TOP versus LIMIT) that are exactly
// where a real statement goes wrong.
//
// So the one rule that makes it trustworthy is the refusal: a statement nobody seeded
// **fails, naming itself and its bound parameters**, and never answers with no rows.
// An empty result is a business answer — a lookup that found nobody, a leaver with no
// account — and a mock inventing one teaches a process a fact, in the direction that
// is hardest to notice.
//
// What follows from "it answers, it does not execute" is worth stating plainly: an
// INSERT does not change what a later SELECT returns. Both are seeded, and a process
// that writes and then reads back sees whatever the seed says it sees. That is the
// cost of not having an engine, and it is why this is a mockup aid rather than a test
// database.
//
// # What is faithful
//
// Where it *can* be faithful cheaply, it is, because a mock that accepts what the real
// database refuses teaches a model to be wrong and the lesson arrives in production:
//
//   - Parameters are bound, never interpolated — the statement reaching the mock is the
//     statement the author wrote, and the values arrive beside it, so a seed can match
//     on them and the journal records what was actually passed.
//   - A seeded [MockAnswer.Error] fails the statement, because the failures a database
//     has are part of what a process must be tried against: a unique-key violation on
//     a replayed create is not an edge case in an identity process.
//   - Values keep their types through the file: a seeded 1000000 arrives as an integer,
//     not as the 1e+06 a float64 round trip produces and nothing matches.
//   - There are no transactions, matching ADR-0173's connector exactly — one statement
//     per task, autocommit, nothing spanning two.
//
// Nothing here is durable: it is memory, and a restart is an unseeded database.

// maxMockStatements bounds the journal. A mock worker left running is a long-lived
// process, and a stand-in for a database must not grow into the memory of the thing it
// stands in for. The newest are kept, like the AD mock's operation journal.
const maxMockStatements = 200

// MockAnswer is one thing the mock database knows how to answer.
//
// Statement is matched ignoring case and runs of whitespace (see [normalizeStatement]).
// Params or Named, when set, narrow the answer to one binding; an answer that sets
// neither answers every binding of its statement, which makes it the fallback. Rows
// with Columns answer a query, Affected answers an execute, and Error fails either.
type MockAnswer struct {
	Statement string         `json:"statement"`
	Params    []any          `json:"params,omitempty"`
	Named     map[string]any `json:"named,omitempty"`
	Columns   []string       `json:"columns,omitempty"`
	Rows      [][]any        `json:"rows,omitempty"`
	Affected  int64          `json:"affected,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// mockSeedFile is the shape of the seed an operator writes.
type mockSeedFile struct {
	Answers []MockAnswer `json:"answers"`
}

// MockStatement is one statement a process ran against the mock, with the values it
// bound. It is what a mockup run leaves behind — the answer to "what would this have
// done to the database", which is the question the run exists to ask.
type MockStatement struct {
	Seq       uint64         `json:"seq"`
	Statement string         `json:"statement"`
	Params    []any          `json:"params,omitempty"`
	Named     map[string]any `json:"named,omitempty"`
	// Failed says the mock refused this statement — either because a seed made it
	// fail, or because nothing answered it.
	Failed bool   `json:"failed,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// MockDatabase is an in-memory database for mock mode. It implements
// [driver.Connector], so it drops into database/sql exactly where a real driver sits,
// and it is safe for concurrent use: one database serves every job a worker leases.
//
// It knows its product for one reason: the refusal below has to name the seed file an
// operator must add the answer to, and that variable is spelled per product
// ([Product.MockSeedEnv]). A message quoting a pattern would leave the reader to apply
// the substitution themselves, which is the same fold connector/envname exists to stop
// three packages getting wrong in three ways.
type MockDatabase struct {
	product Product
	mu      sync.Mutex
	answers []MockAnswer
	ran     []MockStatement
	seq     uint64
}

// NewMockDatabase builds a mock database of product p from seeded answers.
func NewMockDatabase(p Product, answers ...MockAnswer) *MockDatabase {
	return &MockDatabase{product: p, answers: append([]MockAnswer(nil), answers...)}
}

// Product returns the product this mock stands in for.
func (m *MockDatabase) Product() Product { return m.product }

// OpenMock wraps a mock database as a [Client] of its own product, with the same pool
// policy a real one gets — so the mock exercises the limits rather than being the one
// path that does not.
func OpenMock(m *MockDatabase) *Client {
	db := sql.OpenDB(m)
	tunePool(db)
	return NewClient(db, m.product)
}

// Statements returns the journal, oldest first.
func (m *MockDatabase) Statements() []MockStatement {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MockStatement(nil), m.ran...)
}

// Connect and Driver make a *MockDatabase a driver.Connector, which is what lets
// sql.OpenDB build a pool over it without registering a driver name globally. A name
// would be a process-wide registration for something that is per worker.
func (m *MockDatabase) Connect(context.Context) (driver.Conn, error) { return &mockConn{m: m}, nil }
func (m *MockDatabase) Driver() driver.Driver                        { return mockDriver{} }

// mockDriver exists only to satisfy driver.Connector. Nothing opens through it: a mock
// database is never addressed by a DSN, because there is no server to address.
type mockDriver struct{}

func (mockDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("sqldb: a mock database has no connection string to open")
}

// answer finds the seeded answer for a statement and its binding, records the call in
// the journal, and reports what to do.
func (m *MockDatabase) answer(stmt string, args []driver.NamedValue) (MockAnswer, error) {
	params, named := splitArgs(args)
	want := normalizeStatement(stmt)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Two passes, most specific first: an answer that names the binding it is for wins
	// over the statement's catch-all, whatever order the seed file lists them in.
	var fallback *MockAnswer
	var found *MockAnswer
	for i := range m.answers {
		a := &m.answers[i]
		if normalizeStatement(a.Statement) != want {
			continue
		}
		if a.Params == nil && a.Named == nil {
			if fallback == nil {
				fallback = a
			}
			continue
		}
		if bindingMatches(*a, params, named) {
			found = a
			break
		}
	}
	if found == nil {
		found = fallback
	}
	if found == nil {
		err := fmt.Errorf("sqldb: mock: nothing is seeded for the statement %q%s; add an answer for it to the mock seed (%s), "+
			"because answering an unseeded statement with no rows would hand the process a fact it made up",
			strings.TrimSpace(stmt), boundDetail(params, named), m.product.MockSeedEnv())
		m.journal(stmt, params, named, true, err.Error())
		return MockAnswer{}, err
	}
	if found.Error != "" {
		m.journal(stmt, params, named, true, found.Error)
		return MockAnswer{}, fmt.Errorf("sqldb: mock: %s", found.Error)
	}
	m.journal(stmt, params, named, false, "")
	return *found, nil
}

// journal records one statement, keeping the newest within the cap. The caller holds
// the lock.
func (m *MockDatabase) journal(stmt string, params []any, named map[string]any, failed bool, detail string) {
	m.seq++
	m.ran = append(m.ran, MockStatement{
		Seq: m.seq, Statement: strings.TrimSpace(stmt), Params: params, Named: named,
		Failed: failed, Detail: detail,
	})
	if len(m.ran) > maxMockStatements {
		m.ran = append(m.ran[:0], m.ran[len(m.ran)-maxMockStatements:]...)
	}
}

// splitArgs separates a driver's arguments into the positional and named halves the
// seed file speaks in.
func splitArgs(args []driver.NamedValue) ([]any, map[string]any) {
	var params []any
	var named map[string]any
	for _, a := range args {
		v := normalize(a.Value)
		if a.Name != "" {
			if named == nil {
				named = map[string]any{}
			}
			named[a.Name] = v
			continue
		}
		params = append(params, v)
	}
	return params, named
}

// bindingMatches reports whether an answer's declared binding is the one that arrived.
func bindingMatches(a MockAnswer, params []any, named map[string]any) bool {
	if a.Named != nil {
		if len(a.Named) != len(named) {
			return false
		}
		for k, want := range a.Named {
			got, ok := named[k]
			if !ok || !sameValue(want, got) {
				return false
			}
		}
		return true
	}
	if len(a.Params) != len(params) {
		return false
	}
	for i, want := range a.Params {
		if !sameValue(want, params[i]) {
			return false
		}
	}
	return true
}

// sameValue compares a seeded value with a bound one. A seed is written in JSON and a
// binding arrives from a driver, so 42, int64(42) and "42" have to be able to mean the
// same parameter — comparing their rendered forms is what makes a seed an operator
// wrote match a value a process bound, without a table of type coercions nobody would
// get right for every driver.
func sameValue(seed, bound any) bool {
	if seed == nil || bound == nil {
		return seed == nil && bound == nil
	}
	return renderValue(seed) == renderValue(bound)
}

// renderValue is the canonical text of a bound or seeded value.
func renderValue(v any) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	case float64:
		// An integral float renders as an integer, so a seeded 42 read from JSON and an
		// int64(42) a driver bound are one parameter.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
	}
	return fmt.Sprintf("%v", v)
}

// boundDetail renders the parameters into the unseeded-statement error, so the
// operator can copy the binding into the seed instead of guessing at it.
func boundDetail(params []any, named map[string]any) string {
	switch {
	case len(named) > 0:
		keys := make([]string, 0, len(named))
		for k, v := range named {
			keys = append(keys, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(keys)
		return " with parameters " + strings.Join(keys, ", ")
	case len(params) > 0:
		vals := make([]string, 0, len(params))
		for _, v := range params {
			vals = append(vals, fmt.Sprintf("%v", v))
		}
		return " with parameters " + strings.Join(vals, ", ")
	}
	return ""
}

// normalizeStatement is the identity a statement is matched by: its text, with runs of
// whitespace collapsed and case folded.
//
// Both are forgiven because neither is part of what the statement says. The Modeler's
// statement field wraps across lines, and SQL Server is case-insensitive for keywords
// and, by its default collation, for identifiers too — so a seed that differs in
// either from the model is the same statement, and refusing it would teach the
// operator to diff whitespace instead of to write a seed. Nothing else is forgiven:
// a different column list is a different statement.
func normalizeStatement(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// ParseMockSeed reads a mock seed file. Its errors speak the file's vocabulary rather
// than a Go type's, because the person who wrote it is an operator with a text editor.
func ParseMockSeed(data []byte) ([]MockAnswer, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // a seeded id must not come back as 1e+06
	var file mockSeedFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("sqldb: the mock seed is not valid JSON: %w", err)
	}
	for i := range file.Answers {
		a := &file.Answers[i]
		if strings.TrimSpace(a.Statement) == "" {
			return nil, fmt.Errorf("sqldb: mock seed answer %d needs a statement", i+1)
		}
		for j := range a.Params {
			a.Params[j] = jsonScalar(a.Params[j])
		}
		for k, v := range a.Named {
			a.Named[k] = jsonScalar(v)
		}
		if len(a.Rows) > 0 && len(a.Columns) == 0 {
			return nil, fmt.Errorf("sqldb: mock seed answer %d (%q) has rows but names no columns; a row is read by column name",
				i+1, strings.TrimSpace(a.Statement))
		}
		for r := range a.Rows {
			if len(a.Rows[r]) != len(a.Columns) {
				return nil, fmt.Errorf("sqldb: mock seed answer %d (%q) row %d has %d values for %d columns",
					i+1, strings.TrimSpace(a.Statement), r+1, len(a.Rows[r]), len(a.Columns))
			}
			for c := range a.Rows[r] {
				a.Rows[r][c] = jsonScalar(a.Rows[r][c])
			}
		}
	}
	return file.Answers, nil
}

// mockConn is one connection to a mock database. Every one of them reaches the same
// answers and the same journal, which is what a pool over a single server looks like.
type mockConn struct{ m *MockDatabase }

func (c *mockConn) Prepare(string) (driver.Stmt, error) {
	// QueryContext and ExecContext below are implemented directly, so database/sql
	// never falls back to a prepared statement.
	return nil, fmt.Errorf("sqldb: a mock database prepares nothing")
}
func (c *mockConn) Close() error { return nil }

// Begin refuses, matching the connector: a SQL task is one autocommit statement and no
// transaction spans two tasks (ADR-0173), so a mock offering one would teach a model
// something the real connector cannot do.
func (c *mockConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("sqldb: a SQL task is one autocommit statement; no transaction spans two tasks (ADR-0173)")
}

// Ping answers, so the Console's connection check says the same thing about a mock
// worker that it says about a real one.
func (c *mockConn) Ping(context.Context) error { return nil }

// CheckNamedValue accepts every value unchanged: the mock compares against the seed
// itself, so database/sql's default coercion would only obscure what was bound.
func (c *mockConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *mockConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	a, err := c.m.answer(q, args)
	if err != nil {
		return nil, err
	}
	return &mockRows{cols: a.Columns, rows: a.Rows}, nil
}

func (c *mockConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	a, err := c.m.answer(q, args)
	if err != nil {
		return nil, err
	}
	return mockResult(a.Affected), nil
}

type mockResult int64

func (mockResult) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("sqldb: a mock database has no identity column")
}
func (r mockResult) RowsAffected() (int64, error) { return int64(r), nil }

type mockRows struct {
	cols []string
	rows [][]any
	i    int
}

func (r *mockRows) Columns() []string { return r.cols }
func (r *mockRows) Close() error      { return nil }
func (r *mockRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	for i, v := range r.rows[r.i] {
		if i >= len(dest) {
			break
		}
		dest[i] = mockDriverValue(v)
	}
	r.i++
	return nil
}

// mockDriverValue narrows a seeded value to something driver.Value admits. A driver
// hands back a small set of Go types and [normalize] is written for that set, so a
// seeded value has to arrive in it or the conversion under test is not the one that
// runs in production.
func mockDriverValue(v any) driver.Value {
	switch t := v.(type) {
	case nil, bool, int64, float64, string, []byte:
		return t
	case int:
		return int64(t)
	case json.Number:
		return jsonScalar(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}
