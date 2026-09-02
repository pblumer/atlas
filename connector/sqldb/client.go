package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/connector/nettimeout"
)

// DefaultMaxRows caps a query that authored no cap of its own. A SELECT with a
// forgotten WHERE against a person table is the ordinary way to exhaust a worker's
// memory, and a thousand rows is far past what a process variable should hold.
const DefaultMaxRows = 1000

// The pool policy every database this connector opens is held to. database/sql's own
// defaults are the two a database administrator would not have chosen: an unlimited
// number of open connections, and connections kept until the process exits.
//
//   - [MaxOpenConns] bounds what one Worker Instance can take from a database. It sits
//     far above the jobs a worker actually runs at once (one per poll by default, and
//     a handful with --max-jobs raised) and far below what a SQL Server will accept, so
//     it is headroom rather than a throttle: raising a worker's job count needs no
//     matching change here, and a worker retrying against a database that has started
//     refusing still cannot take its connection limit. Past the cap a job waits for a
//     connection and gives up on the same [nettimeout.Default] budget as the call
//     itself, which is back-pressure rather than a queue with no end. More throughput
//     is more Worker Instances (ADR-0203), and that raises the load on the database in
//     steps an operator can count.
//   - [ConnMaxLifetime] and [ConnMaxIdleTime] recycle connections. A pooled connection
//     that a firewall, a failover or Azure SQL's idle timeout closed underneath the
//     driver is indistinguishable from a working one until it is used, so the first
//     job after a quiet night is the one that meets it. Recycling bounds how long that
//     window can be instead of leaving it open for the life of the worker.
const (
	MaxOpenConns    = 16
	MaxIdleConns    = 4
	ConnMaxLifetime = 30 * time.Minute
	ConnMaxIdleTime = 5 * time.Minute
)

// Open opens the pooled handle for one configured database and applies the pool
// policy above. It is what a worker calls; the driver is registered by the worker's
// blank import, and database/sql resolves it by name here, so this package still
// imports no vendor code and links no driver into anything that only resolves jobs.
//
// sql.Open connects lazily, so a database that is merely down does not fail here — a
// worker has to survive a database restart. What it does catch is a driver nobody
// registered, and a connection string the driver parses eagerly enough to reject.
func Open(p Product, dsn string) (*Client, error) {
	db, err := sql.Open(p.Driver, dsn)
	if err != nil {
		return nil, err
	}
	tunePool(db)
	return NewClient(db, p), nil
}

// tunePool applies the policy above. It is one function so the mock database is held
// to the same limits as a real one rather than being the one path that is not.
func tunePool(db *sql.DB) {
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetConnMaxLifetime(ConnMaxLifetime)
	db.SetConnMaxIdleTime(ConnMaxIdleTime)
}

// Registry resolves a connector name to the database behind it. It is the worker's
// own map — the engine never holds one for this kind (ADR-0173).
type Registry = clientreg.Registry[*Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[*Client]() }

// Client is one configured database: a pooled handle and the product whose rules
// apply to it. database/sql's handle *is* the pool, so unlike the LDAP and AD
// connectors this kind reuses connections rather than dialing per job.
type Client struct {
	db      *sql.DB
	product Product
}

// NewClient wraps an open handle as it is. The caller owns opening it — and, with it,
// the pool limits: a handle that did not come from [Open] keeps whatever tuning it
// arrived with. That is what lets the tests drive this against a fake driver and keeps
// every vendor import out of this package.
func NewClient(db *sql.DB, p Product) *Client { return &Client{db: db, product: p} }

// Product returns the client's product.
func (c *Client) Product() Product { return c.product }

// Close releases the pool.
func (c *Client) Close() error { return c.db.Close() }

// Ping opens a connection and checks that the database answers on it, so a
// configuration mistake — a host that does not resolve, a login the server rejects, a
// database name nobody has — is reported to whoever typed it instead of waiting for
// the first process to meet it (ADR-0150's argument, applied to a database).
//
// It proves reachability and authentication, and nothing beyond that: a login with no
// rights to the table a statement names still pings.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, nettimeout.Default)
	defer cancel()
	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqldb: connect: %w", err)
	}
	return nil
}

// Query runs a row-returning statement and converts the result to JSON-shaped values.
//
// maxRows is a limit, not a page size: the statement is asked for one row more than
// the cap, and finding it fails the job. Truncating would be worse than failing —
// a short result set is a wrong business answer, and a process that branches on
// count(rows) would branch on it confidently.
func (c *Client) Query(ctx context.Context, stmt string, args []any, maxRows int) ([]map[string]any, error) {
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	ctx, cancel := context.WithTimeout(ctx, nettimeout.Default)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("sqldb: query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqldb: columns: %w", err)
	}
	out := make([]map[string]any, 0, 16)
	for rows.Next() {
		if len(out) == maxRows {
			return nil, fmt.Errorf("sqldb: the query returned more than the %d-row cap; narrow the statement or raise maxRows (truncating would be a wrong answer, not a partial one)", maxRows)
		}
		row, err := scanRow(rows, cols)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqldb: reading rows: %w", err)
	}
	return out, nil
}

// Exec runs a statement that returns no rows and reports how many it affected. A
// driver that cannot report the count yields 0 rather than an error: the statement
// did run, and failing the job over the bookkeeping would be a lie about the effect.
func (c *Client) Exec(ctx context.Context, stmt string, args []any) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, nettimeout.Default)
	defer cancel()

	res, err := c.db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, fmt.Errorf("sqldb: execute: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// rowScanner is the part of *sql.Rows scanRow needs, so the conversion is testable
// without a live cursor.
type rowScanner interface{ Scan(dest ...any) error }

// scanRow reads one row into a column-keyed map, normalizing the driver's Go types
// into ones that survive a round trip through a process variable's JSON.
func scanRow(rows rowScanner, cols []string) (map[string]any, error) {
	cells := make([]any, len(cols))
	into := make([]any, len(cols))
	for i := range cells {
		into[i] = &cells[i]
	}
	if err := rows.Scan(into...); err != nil {
		return nil, fmt.Errorf("sqldb: scan: %w", err)
	}
	row := make(map[string]any, len(cols))
	for i, name := range cols {
		row[name] = normalize(cells[i])
	}
	return row, nil
}

// normalize converts a driver's value into something a process variable can hold.
//
// The two that matter are []byte — which most drivers hand back for text and which
// would otherwise be JSON-encoded as base64 — and time.Time, which becomes RFC 3339
// so a date read from a database compares as the same string a FEEL expression would
// write. Everything else is already a JSON-native scalar.
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return v
	}
}
