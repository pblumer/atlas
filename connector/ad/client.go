// Package ad integrates Active Directory as a service-task Worker Type: a BPMN AD
// task performs an AD-specific provisioning operation — create a user, set
// a password, enable or disable an account, or add/remove a group member — against a
// model-authored server through the job path (ADR-0166), the same seam the ldap
// package uses (ADR-0154). AD speaks LDAP, so this worker dials and binds exactly
// like the generic LDAP worker; it exists because AD expresses those operations
// through mechanisms the generic worker cannot: a password is the binary
// `unicodePwd` attribute (UTF-16LE, quote-wrapped, LDAPS only), an account's
// enabled/disabled state is a bit in `userAccountControl` (a read-modify-write), and
// group membership is an *incremental* add/delete of a `member` value rather than a
// whole-attribute replace. It also reads: a DirSync delta, which is AD's own
// mechanism, and an ordinary search, which is not — a provisioning process has to ask
// whether a group exists before it can put anybody in it, and making it bind a second
// worker to the same directory to ask was a seam in the middle of one lifecycle.
//
// It inherits the job protocol's durability and non-blocking properties (ADR-0007):
// the processor creates a job carrying [compiler.AdJobType] and never talks to AD
// itself; the in-process [Handler] pulls the job, dials/binds/operates/closes off the
// run loop and after fsync, and completes it. The server URL and DNs live in the model
// as literal-or-FEEL values; the bind password is a server-side secret reference
// (ADR-0041). Every call is bounded by the shared worker budget (nettimeout.Default,
// ADR-0149). Delivery is at-least-once, so an operation must tolerate a replay.
package ad

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"
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

// Entry is one directory entry a delta returns: its DN and its multi-valued
// attributes keyed by attribute name.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// DirSyncRequest asks Active Directory for everything that changed under a naming
// context since a cookie (MS-ADTS LDAP_SERVER_DIRSYNC_OID).
//
// Cookie is opaque and belongs to the server: empty asks for a full pass, and the
// cookie a pass returns is the only correct input to the next one. Flags carries the
// DirSync flags; MaxEntries caps one pass, which costs nothing because a pass is
// resumable by construction.
type DirSyncRequest struct {
	BaseDN     string
	Filter     string
	Attributes []string
	Cookie     []byte
	Flags      int64
	MaxEntries int32
}

// DirSyncResult is one pass: the entries that changed, the cookie the next pass must
// present, and whether the server has more waiting right now.
type DirSyncResult struct {
	Entries []Entry
	Cookie  []byte
	More    bool
}

// SearchRequest asks the directory what is under a base right now: the base DN, a
// scope ("base"/"one"/"sub"), a filter (empty → "(objectClass=*)"), and the attributes
// to return (nil → the server's default set).
//
// It is the generic LDAP worker's search request, deliberately — the two workers
// read a directory the same way, and only what AD *writes* differs.
type SearchRequest struct {
	BaseDN     string
	Scope      string
	Filter     string
	Attributes []string
	// PageSize drives the simple paged-results control (RFC 2696), so a domain
	// controller's administrative size limit (1000 by default) does not refuse a
	// legitimate search. 0 asks for one unpaged search.
	PageSize int32
	// MaxEntries caps how many entries may be returned. Exceeding it is an error
	// rather than a truncation: a short result set is a wrong answer, not a partial
	// one, and a process branching on "did I find the group?" would branch on it
	// confidently. 0 is unbounded.
	MaxEntries int32
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
	// ModifyDN moves and/or renames an entry. In a directory those are one operation:
	// an entry's place in the tree *is* its name, so a mover is a DN change.
	ModifyDN(dn, newRDN, newSuperior string) error
	// Delete removes an entry. AD refuses to delete a container that still has
	// children, which is the behaviour a leaver process wants.
	Delete(dn string) error
	// DirSync reads what changed under a naming context since a cookie. It is here
	// because DirSync is Active Directory's own mechanism, not LDAP's.
	DirSync(req DirSyncRequest) (DirSyncResult, error)
	// Search reads what is under a base right now. It is an ordinary LDAP search and
	// the generic worker can do it too; it is here because a provisioning process
	// has to ask whether an entry exists before it can act on one — and having to bind
	// a second worker to the same directory to ask is the seam this worker
	// exists to remove (ADR-0166, amended a fifth time).
	Search(req SearchRequest) ([]Entry, error)
	Close() error
}

// Dialer opens and binds an AD connection. It is an interface so the worker is
// testable without a live server.
type Dialer interface {
	Dial(url, bindDN, bindPassword string, startTLS bool) (Conn, error)
}

// GoDialer dials a real AD server through github.com/go-ldap/ldap, bounded by the
// shared worker call budget (nettimeout.Default), like the LDAP worker.
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

func (c *goConn) ModifyDN(dn, newRDN, newSuperior string) error {
	// deleteOldRDN is true: a rename that kept the old relative name as a spare
	// attribute value is a directory slowly filling with an entry's former names.
	req := goldap.NewModifyDNRequest(dn, newRDN, true, newSuperior)
	if err := c.conn.ModifyDN(req); err != nil {
		return fmt.Errorf("ad: modifydn %s: %w", dn, err)
	}
	return nil
}

func (c *goConn) Delete(dn string) error {
	if err := c.conn.Del(goldap.NewDelRequest(dn, nil)); err != nil {
		return fmt.Errorf("ad: delete %s: %w", dn, err)
	}
	return nil
}

// dirSyncOID is Active Directory's DirSync control.
const dirSyncOID = goldap.ControlTypeDirSync

func (c *goConn) DirSync(req DirSyncRequest) (DirSyncResult, error) {
	filter := req.Filter
	if filter == "" {
		filter = "(objectClass=*)"
	}
	// DirSync is always a subtree read from a naming context root; AD refuses any
	// other scope, so the model does not get to author one.
	sr := goldap.NewSearchRequest(
		req.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		0, 0, false, filter, req.Attributes,
		[]goldap.Control{goldap.NewRequestControlDirSync(req.Flags, 0, req.Cookie)},
	)
	res, err := c.conn.Search(sr)
	if err != nil {
		return DirSyncResult{}, fmt.Errorf("ad: dirsync %s: %w", req.BaseDN, err)
	}
	if req.MaxEntries > 0 && len(res.Entries) > int(req.MaxEntries) {
		return DirSyncResult{}, fmt.Errorf("ad: the sync pass under %s returned more than the %d-entry cap; raise maxEntries or narrow the filter", req.BaseDN, req.MaxEntries)
	}
	out := DirSyncResult{Entries: entriesFrom(res)}
	ctrl := goldap.FindControl(res.Controls, dirSyncOID)
	if ctrl == nil {
		// No control back means the server did not honour DirSync — most often the
		// bind account lacks the replication right, or the base is not a naming
		// context root. Returning the entries as if they were a delta would hand the
		// process a full directory it believes to be a change set.
		return DirSyncResult{}, fmt.Errorf("ad: the server returned no DirSync control for %s; check that the base is a naming context root and that the bind account may replicate directory changes (or set objectSecurity)", req.BaseDN)
	}
	ds := ctrl.(*goldap.ControlDirSync)
	// In a DirSync *response* the control's fields do not mean what they are named:
	// go-ldap reuses the request struct, and the first field is MS-ADTS's MoreResults
	// rather than the request flags.
	out.More = ds.Flags != 0
	out.Cookie = ds.Cookie
	return out, nil
}

func (c *goConn) Search(req SearchRequest) ([]Entry, error) {
	filter := req.Filter
	if filter == "" {
		filter = "(objectClass=*)"
	}
	// SizeLimit stays 0: the cap is enforced on the result rather than asked of the
	// server, because a server-side size limit *truncates* where MaxEntries must fail.
	sr := goldap.NewSearchRequest(
		req.BaseDN, searchScope(req.Scope), goldap.NeverDerefAliases,
		0, 0, false, filter, req.Attributes, nil,
	)
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
		return nil, fmt.Errorf("ad: search %s: %w", req.BaseDN, err)
	}
	if max := req.MaxEntries; max > 0 && len(res.Entries) > int(max) {
		return nil, fmt.Errorf("ad: the search under %s returned more than the %d-entry cap; narrow the filter or raise maxEntries (truncating would be a wrong answer, not a partial one)", req.BaseDN, max)
	}
	return entriesFrom(res), nil
}

// searchScope maps an authored scope name onto go-ldap's constant. The compiler
// refuses anything else, so an unknown one here can only be a job written before a
// scope was authored at all: the whole subtree, which is what a search means when
// nobody said otherwise.
func searchScope(scope string) int {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "base":
		return goldap.ScopeBaseObject
	case "one":
		return goldap.ScopeSingleLevel
	default:
		return goldap.ScopeWholeSubtree
	}
}

// entriesFrom converts a search result into the worker's entry shape.
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

func (c *goConn) Close() error { return c.conn.Close() }

// splitDN separates a distinguished name into its relative name and its parent,
// respecting backslash escapes so a comma *inside* a value — "cn=Meier\, Arno,ou=…" —
// does not split the name in the wrong place.
//
// It exists because the model authors a move as one target DN, which is how a person
// thinks about it ("this entry now lives here"), while LDAP's ModifyDN wants the two
// halves separately.
func splitDN(dn string) (rdn, superior string) {
	escaped := false
	for i, r := range dn {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == ',':
			return strings.TrimSpace(dn[:i]), strings.TrimSpace(dn[i+1:])
		}
	}
	return strings.TrimSpace(dn), ""
}
