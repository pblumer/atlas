package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"testing"
)

// A fake database/sql driver, so every test in this package drives the real
// database/sql machinery — Query, Scan, NamedValue binding, RowsAffected — instead of
// a hand-rolled stand-in for it. The conversion code here is exactly the code that
// gets a driver's values, so it is worth exercising through the same path a real
// driver would take.

// fakeResult is what a registered fake answers a statement with.
type fakeResult struct {
	cols     []string
	rows     [][]driver.Value
	affected int64
	err      error
	// nextErr fails the cursor mid-read, which is what rows.Err() reports.
	nextErr error
	// noCount makes the driver unable to report an affected-row count.
	noCount bool
	// gotArgs records the arguments the last call bound, so a test can assert that
	// parameters were bound rather than interpolated.
	gotArgs []driver.NamedValue
	gotStmt string
}

var (
	fakeMu sync.Mutex
	fakes  = map[string]*fakeResult{}
)

func init() { sql.Register("atlasfake", fakeDriver{}) }

// registerFake makes a DSN answer with r, and returns the DSN plus a cleanup.
func registerFake(t *testing.T, r *fakeResult) string {
	t.Helper()
	dsn := fmt.Sprintf("fake-%s", t.Name())
	fakeMu.Lock()
	fakes[dsn] = r
	fakeMu.Unlock()
	t.Cleanup(func() {
		fakeMu.Lock()
		delete(fakes, dsn)
		fakeMu.Unlock()
	})
	return dsn
}

// openFake opens a client of product p backed by a fake answering with r.
func openFake(t *testing.T, p Product, r *fakeResult) *Client {
	t.Helper()
	db, err := sql.Open("atlasfake", registerFake(t, r))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewClient(db, p)
}

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	r, ok := fakes[dsn]
	if !ok {
		return nil, fmt.Errorf("no fake registered for %q", dsn)
	}
	return &fakeConn{r: r}, nil
}

type fakeConn struct{ r *fakeResult }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, fmt.Errorf("no transactions") }

// QueryContext and ExecContext are implemented directly on the connection so the
// driver receives []driver.NamedValue and named binding is exercisable.
func (c *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	fakeMu.Lock()
	c.r.gotStmt, c.r.gotArgs = q, args
	fakeMu.Unlock()
	if c.r.err != nil {
		return nil, c.r.err
	}
	return &fakeRows{cols: c.r.cols, rows: c.r.rows, nextErr: c.r.nextErr}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	fakeMu.Lock()
	c.r.gotStmt, c.r.gotArgs = q, args
	fakeMu.Unlock()
	if c.r.err != nil {
		return nil, c.r.err
	}
	if c.r.noCount {
		return noCountResult{}, nil
	}
	return fakeAffected(c.r.affected), nil
}

// CheckNamedValue accepts every value unchanged, so the conversion under test is the
// one being asserted rather than database/sql's default coercion.
func (c *fakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }

type fakeAffected int64

func (f fakeAffected) LastInsertId() (int64, error) { return 0, nil }
func (f fakeAffected) RowsAffected() (int64, error) { return int64(f), nil }

// noCountResult is a driver.Result that cannot report a count, which is the case
// Exec deliberately reports as zero rather than as an error.
type noCountResult struct{}

func (noCountResult) LastInsertId() (int64, error) { return 0, fmt.Errorf("unsupported") }
func (noCountResult) RowsAffected() (int64, error) { return 0, fmt.Errorf("unsupported") }

type fakeRows struct {
	cols    []string
	rows    [][]driver.Value
	nextErr error
	i       int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.nextErr != nil {
		return r.nextErr
	}
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}
