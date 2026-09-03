package ldap

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"sync"
	"time"
)

// Default pool bounds. The idle window is deliberately short: a directory closes an
// idle connection on its own schedule and never tells the client, so a connection
// held much longer than this is one whose next use fails. Thirty seconds is long
// enough to carry a burst of jobs — a bulk reconciliation, a multi-instance loop —
// and short enough that the failure mode stays rare.
const (
	DefaultMaxIdlePerTarget = 4
	DefaultIdleTTL          = 30 * time.Second
)

// Pool is a [Dialer] that reuses bound connections instead of dialing, binding and
// tearing down one per job.
//
// # Why
//
// ADR-0154 shipped a connection per operation, which is the right default and the
// wrong steady state: a joiner run over a few hundred accounts pays a TCP handshake,
// a TLS handshake and a bind for every single entry it touches, against a server
// that would happily have kept the first connection.
//
// # What makes it safe
//
// The pool key is a fingerprint of *everything that decides who the connection is
// authenticated as* — the URL, STARTTLS, the bind DN, the bind password, and the
// client certificate. A connection bound as one identity can therefore never be
// handed to a job asking for another, and a rotated password does not reuse a
// connection bound with the old one: the fingerprint changes with it, so the old
// entries simply age out unused.
//
// A connection whose operation returned an error is never pooled. LDAP errors do not
// distinguish "your filter was wrong" from "this socket is gone" reliably enough to
// bet a later job on, so the pool does not try: any error retires the connection.
//
// # What it does not do
//
// It does not retry. A pooled connection the server closed while it sat idle fails
// its next operation, and that job takes the engine's ordinary retry-then-incident
// path (ADR-0061). Retrying inside the worker would mean re-sending a write whose
// outcome is unknown, which is a worse failure than the one it would paper over.
type Pool struct {
	dialer  Dialer
	maxIdle int
	idleTTL time.Duration
	now     func() time.Time

	mu     sync.Mutex
	idle   map[string][]idleConn
	closed bool
}

type idleConn struct {
	conn Conn
	at   time.Time
}

// PoolOptions configures a [Pool]. A zero value takes the defaults.
type PoolOptions struct {
	MaxIdlePerTarget int
	IdleTTL          time.Duration
	// Now is injected by tests so idle expiry is assertable without sleeping.
	Now func() time.Time
}

// NewPool wraps a dialer with connection reuse.
func NewPool(d Dialer, opts PoolOptions) *Pool {
	if opts.MaxIdlePerTarget <= 0 {
		opts.MaxIdlePerTarget = DefaultMaxIdlePerTarget
	}
	if opts.IdleTTL <= 0 {
		opts.IdleTTL = DefaultIdleTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Pool{dialer: d, maxIdle: opts.MaxIdlePerTarget, idleTTL: opts.IdleTTL, now: opts.Now, idle: map[string][]idleConn{}}
}

// Dial returns a connection for opts, reusing an idle one when the pool holds it.
func (p *Pool) Dial(opts DialOptions) (Conn, error) {
	key := fingerprint(opts)
	if c := p.take(key); c != nil {
		return &pooledConn{Conn: c, pool: p, key: key}, nil
	}
	c, err := p.dialer.Dial(opts)
	if err != nil {
		return nil, err
	}
	return &pooledConn{Conn: c, pool: p, key: key}, nil
}

// take removes and returns a live idle connection for key, closing any that have sat
// past the idle window.
func (p *Pool) take(key string) Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	cutoff := p.now().Add(-p.idleTTL)
	for len(p.idle[key]) > 0 {
		// Newest first: the most recently returned connection is the least likely to
		// have been closed by the server, and taking the oldest would maximise the
		// chance of handing out a dead one.
		last := len(p.idle[key]) - 1
		c := p.idle[key][last]
		p.idle[key] = p.idle[key][:last]
		if c.at.After(cutoff) {
			return c.conn
		}
		_ = c.conn.Close()
	}
	delete(p.idle, key)
	return nil
}

// put returns a connection to the pool, or closes it when the pool is full, closed,
// or the connection is not worth keeping.
func (p *Pool) put(key string, c Conn) {
	p.mu.Lock()
	if p.closed || len(p.idle[key]) >= p.maxIdle {
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	p.idle[key] = append(p.idle[key], idleConn{conn: c, at: p.now()})
	p.mu.Unlock()
}

// Close closes every idle connection and stops the pool reusing any more. Connections
// currently borrowed are closed when their holder releases them.
func (p *Pool) Close() error {
	p.mu.Lock()
	held := p.idle
	p.idle = map[string][]idleConn{}
	p.closed = true
	p.mu.Unlock()
	for _, cs := range held {
		for _, c := range cs {
			_ = c.conn.Close()
		}
	}
	return nil
}

// IdleCount reports how many connections the pool is holding, for tests and for a
// future Workers-view number.
func (p *Pool) IdleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, cs := range p.idle {
		n += len(cs)
	}
	return n
}

// fingerprint identifies a pool target by everything that decides who the connection
// is authenticated as. The credential is hashed rather than kept, so the key can be
// held in a map without holding a password beside it.
func fingerprint(o DialOptions) string {
	h := sha256.New()
	for _, part := range []string{o.URL, strconv.FormatBool(o.StartTLS), o.BindDN, o.BindPassword, o.ClientCert, o.CACert} {
		// The length prefix keeps two different splits of the same bytes — a bind DN
		// ending where a password begins — from colliding onto one key.
		_, _ = io.WriteString(h, strconv.Itoa(len(part))+":"+part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pooledConn is a borrowed connection: it delegates every operation, remembers
// whether one failed, and on Close either returns itself to the pool or closes for
// real.
type pooledConn struct {
	Conn
	pool     *Pool
	key      string
	poisoned bool
}

func (c *pooledConn) Search(req SearchRequest) ([]Entry, error) {
	e, err := c.Conn.Search(req)
	c.note(err)
	return e, err
}

func (c *pooledConn) Add(dn string, attrs map[string][]string) error {
	return c.note(c.Conn.Add(dn, attrs))
}

func (c *pooledConn) Modify(dn string, mods []Mod) error {
	return c.note(c.Conn.Modify(dn, mods))
}

func (c *pooledConn) Delete(dn string) error { return c.note(c.Conn.Delete(dn)) }

func (c *pooledConn) SetPassword(dn, newPassword string) error {
	return c.note(c.Conn.SetPassword(dn, newPassword))
}

// note records whether an operation failed and passes the error through.
func (c *pooledConn) note(err error) error {
	if err != nil {
		c.poisoned = true
	}
	return err
}

// Close releases the connection: back to the pool if nothing went wrong on it, and
// closed for good otherwise.
func (c *pooledConn) Close() error {
	if c.poisoned {
		return c.Conn.Close()
	}
	c.pool.put(c.key, c.Conn)
	return nil
}
