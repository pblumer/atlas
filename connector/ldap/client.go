// Package ldap integrates a generic LDAP directory as a service-task connector: a
// BPMN LDAP connector task performs a directory operation — search an entry, add /
// modify / delete an entry, or set an entry's password — against a model-authored
// server through the job path (ADR-0154), the same seam the rest and scim packages
// use for HTTP (ADR-0067/0151). It inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - An LDAP connector task creates a job carrying the reserved [compiler.LdapJobType].
//     The processor never performs the directory call itself, so it stays
//     allocation-free (invariant I1) and free of any LDAP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, dials and binds off
//     the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), performs the operation, writes a search's entries into the
//     task's result variable, and completes the job, which drives the token onward.
//
// The server URL, bind DN, and target/base DN live in the model as literal-or-FEEL
// values (the fx toggle, ADR-0067); the bind password is never authored there — it
// names a server-side secret the worker resolves at runtime (ADR-0041). Every call is
// bounded by the shared connector budget (nettimeout.Default, ADR-0149), since the
// worker runs on the run-loop goroutine.
//
// Delivery is at-least-once: a crash between "the directory accepted the change" and
// "job completed" replays the operation, so add/modify/delete must be authored to
// tolerate a replay (add of an existing entry, delete of a gone entry) — the worker
// surfaces the directory's error, and the retry/incident path (ADR-0061) handles it.
package ldap

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// Entry is one directory entry a search returns: its DN and its multi-valued
// attributes keyed by attribute name.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// SearchRequest addresses an LDAP search: the base DN, a scope ("base"/"one"/"sub"),
// a filter (empty → "(objectClass=*)"), and the attributes to return (nil → the
// server's default set).
type SearchRequest struct {
	BaseDN     string
	Scope      string
	Filter     string
	Attributes []string
	// PageSize drives the simple paged-results control (RFC 2696): the search is
	// fetched in pages of this size, so a directory's administrative size limit does
	// not refuse a legitimate search. 0 asks for one unpaged search.
	PageSize int32
	// MaxEntries caps how many entries may be returned. Exceeding it is an error
	// rather than a truncation: a short result set is a wrong answer, not a partial
	// one, and a process branching on the count would branch on it confidently
	// (the same rule the SQL connectors apply to rows, ADR-0173). 0 is unbounded.
	MaxEntries int32
}

// modOp identifies an LDAP modify change operation, mirroring go-ldap's constants —
// and the AD connector's Mod, so the two directory connectors express a change the
// same way.
type modOp = uint

const (
	// ModAdd adds values to an attribute, ModDelete removes them, and ModReplace
	// replaces the attribute wholesale.
	ModAdd     modOp = goldap.AddAttribute
	ModDelete  modOp = goldap.DeleteAttribute
	ModReplace modOp = goldap.ReplaceAttribute
)

// Mod is one attribute change in a modify: an operation, the attribute, and the
// values it applies.
type Mod struct {
	Op   modOp
	Attr string
	Vals []string
}

// Conn is a bound LDAP connection the worker operates over and then closes. It is an
// interface so the worker is testable without a live directory.
type Conn interface {
	Search(req SearchRequest) ([]Entry, error)
	Add(dn string, attrs map[string][]string) error
	Modify(dn string, mods []Mod) error
	Delete(dn string) error
	SetPassword(dn, newPassword string) error
	Close() error
}

// DialOptions is everything a connection needs. It is a struct rather than a
// parameter list because the list had already reached four and TLS added two more;
// past that, a call site is a row of positional booleans nobody can read.
type DialOptions struct {
	// URL is ldap://host:389 or ldaps://host:636; StartTLS upgrades a plain
	// connection.
	URL      string
	StartTLS bool
	// BindDN and BindPassword authenticate a simple bind. An empty BindDN leaves the
	// connection anonymous — unless ClientCert is set, in which case the certificate
	// is the identity and the bind is SASL EXTERNAL.
	BindDN       string
	BindPassword string
	// ClientCert is a PEM bundle (certificate plus private key) presented to the
	// server. It arrives resolved from a secret reference; the model never carries it
	// (ADR-0041).
	ClientCert string
	// CACert is an optional PEM bundle of roots used to verify the server, for a
	// directory with a private CA. Empty uses the host's trust store.
	CACert string
}

// Dialer opens and binds an LDAP connection. It is an interface so the worker is
// testable without a live server, and so a pooling implementation can stand in front
// of the real one.
type Dialer interface {
	Dial(opts DialOptions) (Conn, error)
}

// GoDialer dials a real LDAP server through github.com/go-ldap/ldap. The dial and
// every subsequent operation are bounded by the shared connector call budget
// (nettimeout.Default), since the worker runs on the run-loop goroutine
// (ADR-0149/0153).
type GoDialer struct{}

// NewDialer returns the production LDAP dialer.
func NewDialer() GoDialer { return GoDialer{} }

func (GoDialer) Dial(opts DialOptions) (Conn, error) {
	tlsCfg, err := tlsConfigFor(opts)
	if err != nil {
		return nil, err
	}
	dialOpts := []goldap.DialOpt{goldap.DialWithDialer(&net.Dialer{Timeout: nettimeout.Default})}
	if tlsCfg != nil {
		dialOpts = append(dialOpts, goldap.DialWithTLSConfig(tlsCfg))
	}
	conn, err := goldap.DialURL(opts.URL, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("ldap: dial %s: %w", opts.URL, err)
	}
	conn.SetTimeout(nettimeout.Default)
	if opts.StartTLS {
		cfg := tlsCfg
		if cfg == nil {
			cfg = &tls.Config{ServerName: hostOf(opts.URL)}
		}
		if err := conn.StartTLS(cfg); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap: starttls %s: %w", opts.URL, err)
		}
	}
	if err := bind(conn, opts); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &goConn{conn: conn}, nil
}

// bind authenticates the connection.
//
// A named bind DN is a simple bind, as before. With no bind DN but a client
// certificate the identity *is* the certificate, and the server is asked to use it
// via SASL EXTERNAL — presenting a certificate and then staying anonymous would
// authenticate nothing, which is the mistake this rule exists to prevent. Neither
// leaves the connection anonymous.
func bind(conn *goldap.Conn, opts DialOptions) error {
	switch {
	case opts.BindDN != "":
		if err := conn.Bind(opts.BindDN, opts.BindPassword); err != nil {
			return fmt.Errorf("ldap: bind %s: %w", opts.BindDN, err)
		}
	case opts.ClientCert != "":
		if err := conn.ExternalBind(); err != nil {
			return fmt.Errorf("ldap: external bind with the client certificate: %w", err)
		}
	}
	return nil
}

// tlsConfigFor builds the TLS configuration a connection needs, or nil when it needs
// none beyond the defaults.
func tlsConfigFor(opts DialOptions) (*tls.Config, error) {
	if opts.ClientCert == "" && opts.CACert == "" {
		return nil, nil
	}
	cfg := &tls.Config{ServerName: hostOf(opts.URL), MinVersion: tls.VersionTLS12}
	if opts.ClientCert != "" {
		// One PEM bundle holds both halves; splitting them into two secrets would be
		// two things to rotate together and one more way to get it half-done.
		cert, err := tls.X509KeyPair([]byte(opts.ClientCert), []byte(opts.ClientCert))
		if err != nil {
			return nil, fmt.Errorf("ldap: the client certificate secret is not a PEM certificate and key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if opts.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(opts.CACert)) {
			return nil, fmt.Errorf("ldap: the CA secret holds no PEM certificate")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// hostOf extracts the host (no port) from an LDAP URL, for the STARTTLS server name.
// An unparseable URL yields "", which lets crypto/tls fall back to its own handling.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// goConn adapts a *goldap.Conn to the Conn interface.
type goConn struct {
	conn *goldap.Conn
}

// scopeCode maps an authored scope name to the go-ldap scope constant. The compiler
// validates the name, so an unknown value here defaults to the whole subtree.
func scopeCode(scope string) int {
	switch scope {
	case "base":
		return goldap.ScopeBaseObject
	case "one":
		return goldap.ScopeSingleLevel
	default:
		return goldap.ScopeWholeSubtree
	}
}

func (c *goConn) Search(req SearchRequest) ([]Entry, error) {
	sr := buildSearchRequest(req)
	var (
		res *goldap.SearchResult
		err error
	)
	if req.PageSize > 0 {
		res, err = c.conn.SearchWithPaging(sr, uint32(req.PageSize))
	} else {
		res, err = c.conn.Search(sr)
	}
	if err != nil {
		return nil, fmt.Errorf("ldap: search %s: %w", req.BaseDN, err)
	}
	if err := capExceeded(len(res.Entries), req.MaxEntries, req.BaseDN); err != nil {
		return nil, err
	}
	return entriesFrom(res), nil
}

// capExceeded reports a search that returned more entries than the model allows.
// Exceeding the cap is an error rather than a truncation: a short result set is a
// wrong answer, not a partial one, and a process branching on the count would branch
// on it confidently. A cap of 0 is unbounded.
func capExceeded(got int, max int32, baseDN string) error {
	if max <= 0 || got <= int(max) {
		return nil
	}
	return fmt.Errorf("ldap: the search under %s returned more than the %d-entry cap; narrow the filter or raise maxEntries (truncating would be a wrong answer, not a partial one)", baseDN, max)
}

// buildSearchRequest translates a connector search into a go-ldap request, defaulting
// an empty filter to "(objectClass=*)" and an unknown scope to the whole subtree.
//
// SizeLimit stays 0 — the cap is enforced on the result rather than asked of the
// server, because a server-side size limit *truncates* where MaxEntries must fail.
func buildSearchRequest(req SearchRequest) *goldap.SearchRequest {
	filter := req.Filter
	if filter == "" {
		filter = "(objectClass=*)"
	}
	return goldap.NewSearchRequest(
		req.BaseDN, scopeCode(req.Scope), goldap.NeverDerefAliases,
		0, 0, false, filter, req.Attributes, nil,
	)
}

func entriesFrom(res *goldap.SearchResult) []Entry {
	out := make([]Entry, 0, len(res.Entries))
	for _, e := range res.Entries {
		attrs := make(map[string][]string, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs[a.Name] = a.Values
		}
		out = append(out, Entry{DN: e.DN, Attributes: attrs})
	}
	return out
}

func (c *goConn) Add(dn string, attrs map[string][]string) error {
	if err := c.conn.Add(buildAddRequest(dn, attrs)); err != nil {
		return fmt.Errorf("ldap: add %s: %w", dn, err)
	}
	return nil
}

// buildAddRequest translates a DN and attribute map into a go-ldap add request.
func buildAddRequest(dn string, attrs map[string][]string) *goldap.AddRequest {
	req := goldap.NewAddRequest(dn, nil)
	for name, vals := range attrs {
		req.Attribute(name, vals)
	}
	return req
}

func (c *goConn) Modify(dn string, mods []Mod) error {
	if err := c.conn.Modify(buildModifyRequest(dn, mods)); err != nil {
		return fmt.Errorf("ldap: modify %s: %w", dn, err)
	}
	return nil
}

// buildModifyRequest translates the change operations into a go-ldap modify request.
func buildModifyRequest(dn string, mods []Mod) *goldap.ModifyRequest {
	req := goldap.NewModifyRequest(dn, nil)
	for _, m := range mods {
		switch m.Op {
		case ModAdd:
			req.Add(m.Attr, m.Vals)
		case ModDelete:
			req.Delete(m.Attr, m.Vals)
		default:
			req.Replace(m.Attr, m.Vals)
		}
	}
	return req
}

func (c *goConn) Delete(dn string) error {
	if err := c.conn.Del(goldap.NewDelRequest(dn, nil)); err != nil {
		return fmt.Errorf("ldap: delete %s: %w", dn, err)
	}
	return nil
}

func (c *goConn) SetPassword(dn, newPassword string) error {
	// RFC 3062 Password Modify extended operation: set the named entry's password.
	req := goldap.NewPasswordModifyRequest(dn, "", newPassword)
	if _, err := c.conn.PasswordModify(req); err != nil {
		return fmt.Errorf("ldap: modify-password %s: %w", dn, err)
	}
	return nil
}

func (c *goConn) Close() error { return c.conn.Close() }
