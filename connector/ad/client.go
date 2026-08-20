// Package ad integrates Active Directory as a service-task connector: a BPMN AD
// connector task performs an AD-specific provisioning operation — create a user, set
// a password, enable or disable an account, or add/remove a group member — against a
// model-authored server through the job path (ADR-0166), the same seam the ldap
// package uses (ADR-0154). AD speaks LDAP, so this connector dials and binds exactly
// like the generic LDAP connector; it exists because AD expresses those operations
// through mechanisms the generic connector cannot: a password is the binary
// `unicodePwd` attribute (UTF-16LE, quote-wrapped, LDAPS only), an account's
// enabled/disabled state is a bit in `userAccountControl` (a read-modify-write), and
// group membership is an *incremental* add/delete of a `member` value rather than a
// whole-attribute replace.
//
// It inherits the job protocol's durability and non-blocking properties (ADR-0007):
// the processor creates a job carrying [compiler.AdJobType] and never talks to AD
// itself; the in-process [Handler] pulls the job, dials/binds/operates/closes off the
// run loop and after fsync, and completes it. The server URL and DNs live in the model
// as literal-or-FEEL values; the bind password is a server-side secret reference
// (ADR-0041). Every call is bounded by the shared connector budget (nettimeout.Default,
// ADR-0149). Delivery is at-least-once, so an operation must tolerate a replay.
package ad

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"unicode/utf16"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// modOp identifies an LDAP modify change operation. The values mirror go-ldap's
// AddAttribute / DeleteAttribute / ReplaceAttribute so the worker can build
// incremental changes (add/remove one group member) as well as replacements.
type modOp = uint

const (
	modAdd     modOp = goldap.AddAttribute
	modDelete  modOp = goldap.DeleteAttribute
	modReplace modOp = goldap.ReplaceAttribute
)

// Mod is one attribute change in a modify: an operation, the attribute, and the values
// the operation applies.
type Mod struct {
	Op   modOp
	Attr string
	Vals []string
}

// Conn is a bound AD connection the worker operates over and then closes. It is an
// interface so the worker is testable without a live directory.
type Conn interface {
	// Add creates an entry with the given attributes.
	Add(dn string, attrs map[string][]string) error
	// Modify applies the change operations to an existing entry.
	Modify(dn string, mods []Mod) error
	// ReadAttr returns one attribute's values for an entry (a base-object read), used
	// for the userAccountControl read-modify-write. Missing → an empty slice, no error.
	ReadAttr(dn, attr string) ([]string, error)
	Close() error
}

// Dialer opens and binds an AD connection. It is an interface so the worker is
// testable without a live server.
type Dialer interface {
	Dial(url, bindDN, bindPassword string, startTLS bool) (Conn, error)
}

// GoDialer dials a real AD server through github.com/go-ldap/ldap, bounded by the
// shared connector call budget (nettimeout.Default), like the LDAP connector.
type GoDialer struct{}

// NewDialer returns the production AD dialer.
func NewDialer() GoDialer { return GoDialer{} }

func (GoDialer) Dial(rawURL, bindDN, bindPassword string, startTLS bool) (Conn, error) {
	conn, err := goldap.DialURL(rawURL, goldap.DialWithDialer(&net.Dialer{Timeout: nettimeout.Default}))
	if err != nil {
		return nil, fmt.Errorf("ad: dial %s: %w", rawURL, err)
	}
	conn.SetTimeout(nettimeout.Default)
	if startTLS {
		if err := conn.StartTLS(&tls.Config{ServerName: hostOf(rawURL)}); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ad: starttls %s: %w", rawURL, err)
		}
	}
	if bindDN != "" {
		if err := conn.Bind(bindDN, bindPassword); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ad: bind %s: %w", bindDN, err)
		}
	}
	return &goConn{conn: conn}, nil
}

// hostOf extracts the host (no port) from an LDAP URL, for the STARTTLS server name.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// encodePassword renders a password as AD's unicodePwd attribute value: the password
// wrapped in double quotes and encoded UTF-16LE, with no BOM. AD only accepts this
// over an encrypted channel (LDAPS or STARTTLS), which the model is expected to use.
func encodePassword(pw string) string {
	u16 := utf16.Encode([]rune(`"` + pw + `"`))
	b := make([]byte, 0, len(u16)*2)
	for _, r := range u16 {
		b = append(b, byte(r), byte(r>>8)) // little-endian
	}
	return string(b)
}

// goConn adapts a *goldap.Conn to the Conn interface.
type goConn struct {
	conn *goldap.Conn
}

func (c *goConn) Add(dn string, attrs map[string][]string) error {
	req := goldap.NewAddRequest(dn, nil)
	for name, vals := range attrs {
		req.Attribute(name, vals)
	}
	if err := c.conn.Add(req); err != nil {
		return fmt.Errorf("ad: add %s: %w", dn, err)
	}
	return nil
}

func (c *goConn) Modify(dn string, mods []Mod) error {
	if err := c.conn.Modify(buildModify(dn, mods)); err != nil {
		return fmt.Errorf("ad: modify %s: %w", dn, err)
	}
	return nil
}

// buildModify translates the change operations into a go-ldap modify request.
func buildModify(dn string, mods []Mod) *goldap.ModifyRequest {
	req := goldap.NewModifyRequest(dn, nil)
	for _, m := range mods {
		switch m.Op {
		case modAdd:
			req.Add(m.Attr, m.Vals)
		case modDelete:
			req.Delete(m.Attr, m.Vals)
		default:
			req.Replace(m.Attr, m.Vals)
		}
	}
	return req
}

func (c *goConn) ReadAttr(dn, attr string) ([]string, error) {
	res, err := c.conn.Search(goldap.NewSearchRequest(
		dn, goldap.ScopeBaseObject, goldap.NeverDerefAliases,
		1, 0, false, "(objectClass=*)", []string{attr}, nil,
	))
	if err != nil {
		return nil, fmt.Errorf("ad: read %s of %s: %w", attr, dn, err)
	}
	if len(res.Entries) == 0 {
		return nil, nil
	}
	return res.Entries[0].GetAttributeValues(attr), nil
}

func (c *goConn) Close() error { return c.conn.Close() }
