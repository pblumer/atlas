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
}

// Conn is a bound LDAP connection the worker operates over and then closes. It is an
// interface so the worker is testable without a live directory.
type Conn interface {
	Search(req SearchRequest) ([]Entry, error)
	Add(dn string, attrs map[string][]string) error
	Modify(dn string, replace map[string][]string) error
	Delete(dn string) error
	SetPassword(dn, newPassword string) error
	Close() error
}

// Dialer opens and binds an LDAP connection. url is ldap://host:389 or
// ldaps://host:636; startTLS upgrades a plain connection with STARTTLS; bindDN and
// bindPassword authenticate the bind (an empty bindDN leaves the connection anonymous).
// It is an interface so the worker is testable without a live server.
type Dialer interface {
	Dial(url, bindDN, bindPassword string, startTLS bool) (Conn, error)
}

// GoDialer dials a real LDAP server through github.com/go-ldap/ldap. The dial and
// every subsequent operation are bounded by the shared connector call budget
// (nettimeout.Default), since the worker runs on the run-loop goroutine
// (ADR-0149/0153).
type GoDialer struct{}

// NewDialer returns the production LDAP dialer.
func NewDialer() GoDialer { return GoDialer{} }

func (GoDialer) Dial(rawURL, bindDN, bindPassword string, startTLS bool) (Conn, error) {
	conn, err := goldap.DialURL(rawURL, goldap.DialWithDialer(&net.Dialer{Timeout: nettimeout.Default}))
	if err != nil {
		return nil, fmt.Errorf("ldap: dial %s: %w", rawURL, err)
	}
	conn.SetTimeout(nettimeout.Default)
	if startTLS {
		if err := conn.StartTLS(&tls.Config{ServerName: hostOf(rawURL)}); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap: starttls %s: %w", rawURL, err)
		}
	}
	// An empty bind DN leaves the connection anonymous (no simple bind); a named DN
	// authenticates with the resolved password.
	if bindDN != "" {
		if err := conn.Bind(bindDN, bindPassword); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap: bind %s: %w", bindDN, err)
		}
	}
	return &goConn{conn: conn}, nil
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
	res, err := c.conn.Search(buildSearchRequest(req))
	if err != nil {
		return nil, fmt.Errorf("ldap: search %s: %w", req.BaseDN, err)
	}
	return entriesFrom(res), nil
}

// buildSearchRequest translates a connector search into a go-ldap request, defaulting
// an empty filter to "(objectClass=*)" and an unknown scope to the whole subtree.
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

// entriesFrom flattens a go-ldap search result into connector entries (DN plus
// multi-valued attributes keyed by name).
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

func (c *goConn) Modify(dn string, replace map[string][]string) error {
	if err := c.conn.Modify(buildModifyRequest(dn, replace)); err != nil {
		return fmt.Errorf("ldap: modify %s: %w", dn, err)
	}
	return nil
}

// buildModifyRequest translates a DN and replacement attribute map into a go-ldap
// modify request that replaces each named attribute's values.
func buildModifyRequest(dn string, replace map[string][]string) *goldap.ModifyRequest {
	req := goldap.NewModifyRequest(dn, nil)
	for name, vals := range replace {
		req.Replace(name, vals)
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
