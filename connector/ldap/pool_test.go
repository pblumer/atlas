package ldap

import (
	"errors"
	"testing"
	"time"
)

// countingDialer records how many real dials were made, which is the whole point of
// the pool.
type countingDialer struct {
	dials int
	opts  []DialOptions
	err   error
}

func (d *countingDialer) Dial(o DialOptions) (Conn, error) {
	d.dials++
	d.opts = append(d.opts, o)
	if d.err != nil {
		return nil, d.err
	}
	return &poolFakeConn{}, nil
}

type poolFakeConn struct {
	closed  bool
	opError error
}

func (c *poolFakeConn) Search(SearchRequest) ([]Entry, error) { return nil, c.opError }
func (c *poolFakeConn) Add(string, map[string][]string) error { return c.opError }
func (c *poolFakeConn) Modify(string, []Mod) error            { return c.opError }
func (c *poolFakeConn) Delete(string) error                   { return c.opError }
func (c *poolFakeConn) SetPassword(string, string) error      { return c.opError }
func (c *poolFakeConn) Close() error                          { c.closed = true; return nil }

var poolOpts = DialOptions{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "pw"}

// A released connection is reused rather than re-dialed, which is the saving: a
// joiner run over hundreds of accounts otherwise pays a handshake and a bind per
// entry.
func TestPoolReusesAReleasedConnection(t *testing.T) {
	d := &countingDialer{}
	p := NewPool(d, PoolOptions{})
	t.Cleanup(func() { _ = p.Close() })

	c1, err := p.Dial(poolOpts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = c1.Close()
	if p.IdleCount() != 1 {
		t.Fatalf("idle = %d, want 1 after release", p.IdleCount())
	}

	c2, err := p.Dial(poolOpts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = c2.Close()
	if d.dials != 1 {
		t.Errorf("dials = %d, want 1 (the second was reused)", d.dials)
	}
}

// The pool key covers everything that decides who the connection is authenticated
// as. A connection bound as one identity must never be handed to a job asking for
// another — this is the property the whole design rests on.
func TestPoolNeverCrossesIdentities(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts DialOptions
	}{
		{"another server", DialOptions{URL: "ldap://other", BindDN: "cn=svc", BindPassword: "pw"}},
		{"another bind DN", DialOptions{URL: "ldap://dc", BindDN: "cn=other", BindPassword: "pw"}},
		{"a rotated password", DialOptions{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "new"}},
		{"STARTTLS", DialOptions{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "pw", StartTLS: true}},
		{"a client certificate", DialOptions{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "pw", ClientCert: "PEM"}},
		{"another CA", DialOptions{URL: "ldap://dc", BindDN: "cn=svc", BindPassword: "pw", CACert: "PEM"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &countingDialer{}
			p := NewPool(d, PoolOptions{})
			t.Cleanup(func() { _ = p.Close() })

			c, _ := p.Dial(poolOpts)
			_ = c.Close() // now idle under the first identity

			c2, err := p.Dial(tc.opts)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			_ = c2.Close()
			if d.dials != 2 {
				t.Errorf("dials = %d, want 2 — the idle connection belongs to another identity", d.dials)
			}
		})
	}
}

// A bind DN ending where a password begins must not collide with the other split of
// the same bytes.
func TestFingerprintIsUnambiguous(t *testing.T) {
	a := fingerprint(DialOptions{BindDN: "cn=ab", BindPassword: "c"})
	b := fingerprint(DialOptions{BindDN: "cn=a", BindPassword: "bc"})
	if a == b {
		t.Error("two different credentials share a fingerprint")
	}
}

// A connection whose operation failed is retired rather than pooled: LDAP errors do
// not reliably distinguish a bad filter from a dead socket, and betting a later job
// on the difference is not worth the saved handshake.
func TestPoolRetiresAConnectionThatErrored(t *testing.T) {
	d := &countingDialer{}
	p := NewPool(d, PoolOptions{})
	t.Cleanup(func() { _ = p.Close() })

	c, _ := p.Dial(poolOpts)
	c.(*pooledConn).Conn.(*poolFakeConn).opError = errors.New("connection reset")
	if err := c.Delete("uid=a"); err == nil {
		t.Fatal("want the operation error")
	}
	_ = c.Close()

	if p.IdleCount() != 0 {
		t.Errorf("idle = %d, want 0 — a failed connection must not be pooled", p.IdleCount())
	}
}

// Every operation marks the connection, not just the one the test happened to call.
func TestPoolRetiresOnEveryOperation(t *testing.T) {
	boom := errors.New("reset")
	for name, op := range map[string]func(Conn) error{
		"search":   func(c Conn) error { _, err := c.Search(SearchRequest{}); return err },
		"add":      func(c Conn) error { return c.Add("dn", nil) },
		"modify":   func(c Conn) error { return c.Modify("dn", nil) },
		"delete":   func(c Conn) error { return c.Delete("dn") },
		"password": func(c Conn) error { return c.SetPassword("dn", "pw") },
	} {
		t.Run(name, func(t *testing.T) {
			p := NewPool(&countingDialer{}, PoolOptions{})
			t.Cleanup(func() { _ = p.Close() })
			c, _ := p.Dial(poolOpts)
			c.(*pooledConn).Conn.(*poolFakeConn).opError = boom
			_ = op(c)
			_ = c.Close()
			if p.IdleCount() != 0 {
				t.Errorf("%s: idle = %d, want 0", name, p.IdleCount())
			}
		})
	}
}

// An idle connection past its window is closed rather than handed out: a directory
// closes idle connections on its own schedule and never says so.
func TestPoolExpiresIdleConnections(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	d := &countingDialer{}
	p := NewPool(d, PoolOptions{IdleTTL: time.Minute, Now: func() time.Time { return now }})
	t.Cleanup(func() { _ = p.Close() })

	c, _ := p.Dial(poolOpts)
	inner := c.(*pooledConn).Conn.(*poolFakeConn)
	_ = c.Close()

	now = now.Add(2 * time.Minute)
	c2, err := p.Dial(poolOpts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = c2.Close()
	if d.dials != 2 {
		t.Errorf("dials = %d, want 2 — the idle connection had expired", d.dials)
	}
	if !inner.closed {
		t.Error("the expired connection was not closed")
	}
}

// The pool holds a bounded number per target; the rest are closed on release.
func TestPoolBoundsIdleConnections(t *testing.T) {
	p := NewPool(&countingDialer{}, PoolOptions{MaxIdlePerTarget: 2})
	t.Cleanup(func() { _ = p.Close() })

	var conns []Conn
	for i := 0; i < 4; i++ {
		c, err := p.Dial(poolOpts)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if p.IdleCount() != 2 {
		t.Errorf("idle = %d, want the 2-connection bound", p.IdleCount())
	}
}

// Close releases what the pool holds and stops it keeping any more, so a shutdown
// does not leave sockets open.
func TestPoolClose(t *testing.T) {
	p := NewPool(&countingDialer{}, PoolOptions{})
	c, _ := p.Dial(poolOpts)
	inner := c.(*pooledConn).Conn.(*poolFakeConn)
	_ = c.Close()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !inner.closed {
		t.Error("Close left an idle connection open")
	}
	if p.IdleCount() != 0 {
		t.Errorf("idle = %d after Close", p.IdleCount())
	}

	// A connection borrowed before the close, and one taken after it, are both closed
	// on release rather than pooled.
	c2, err := p.Dial(poolOpts)
	if err != nil {
		t.Fatalf("Dial after Close: %v", err)
	}
	inner2 := c2.(*pooledConn).Conn.(*poolFakeConn)
	_ = c2.Close()
	if !inner2.closed || p.IdleCount() != 0 {
		t.Error("a closed pool must not start holding connections again")
	}
}

// A dial that fails is passed through, and nothing is pooled for it.
func TestPoolDialError(t *testing.T) {
	p := NewPool(&countingDialer{err: errors.New("no route")}, PoolOptions{})
	t.Cleanup(func() { _ = p.Close() })
	if _, err := p.Dial(poolOpts); err == nil {
		t.Fatal("a dial failure must surface")
	}
	if p.IdleCount() != 0 {
		t.Errorf("idle = %d after a failed dial", p.IdleCount())
	}
}

// A successful operation leaves the connection poolable, which is the happy path the
// retire rule must not swallow.
func TestPoolKeepsAHealthyConnection(t *testing.T) {
	p := NewPool(&countingDialer{}, PoolOptions{})
	t.Cleanup(func() { _ = p.Close() })
	c, _ := p.Dial(poolOpts)
	if err := c.Delete("uid=a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Search(SearchRequest{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	_ = c.Close()
	if p.IdleCount() != 1 {
		t.Errorf("idle = %d, want the healthy connection kept", p.IdleCount())
	}
}

// The defaults apply when the options say nothing.
func TestPoolDefaults(t *testing.T) {
	p := NewPool(&countingDialer{}, PoolOptions{})
	t.Cleanup(func() { _ = p.Close() })
	if p.maxIdle != DefaultMaxIdlePerTarget || p.idleTTL != DefaultIdleTTL || p.now == nil {
		t.Errorf("defaults = %d / %v / %v", p.maxIdle, p.idleTTL, p.now != nil)
	}
}
