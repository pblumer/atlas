package ad

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// Mock mode: an in-memory stand-in for a domain controller.
//
// A process that provisions Active Directory cannot be tried out without a
// directory to provision, and the directory an identity process needs is the one
// nobody wants a test writing into. [MockDirectory] is the other half of the worker
// (ADR-0168): the same resolved job, the same [Run], the same [Conn] the go-ldap
// adapter implements — against entries that live in the worker's memory and are
// thrown away when it stops. `atlas worker --connector ad` with ATLAS_AD_MOCK set
// serves AD tasks this way, so a joiner/mover/leaver model runs end to end against a
// directory that does not exist.
//
// **It is faithful where being faithful is the point.** A mock that accepts what the
// real directory refuses teaches a model to be wrong, and the lesson only arrives in
// production. So a replayed create fails with "entry already exists" (delivery is
// at-least-once, and that is the failure a create actually has), a password may only
// be written over an encrypted channel and must carry AD's own UTF-16LE encoding, a
// group member cannot be added twice, a container with children cannot be deleted,
// and DirSync is answered only at a naming context root — the one error a first sync
// almost always earns.
//
// **The password is the exception, deliberately.** A set-password is checked and then
// *not kept*: the entry records that one was set (`pwdLastSet`), never the value, and
// the operation journal carries no password either. Validating the encoding is what a
// mock is for; storing a credential is not.
//
// **What it is not.** There is no schema, no ACL, no password policy, no replication
// and no naming context beyond the DirSync check above — an add whose parent does not
// exist is accepted, because a mock that demanded a seeded OU chain would cost every
// test a fixture and prove nothing. Only the equality, presence and trailing-wildcard
// parts of an LDAP filter are applied; anything else is refused rather than ignored,
// because a filter silently dropped hands a process more than the real directory
// would. Nothing here is durable: it is memory, and a restart is an empty forest.

// maxMockOperations bounds the operation journal. A mock worker left running is a
// long-lived process, and a stand-in for a directory must not grow into the memory of
// the thing it stands in for. The newest are kept, like the preview outbox's.
const maxMockOperations = 200

// mockCookiePrefix marks a DirSync cookie as this directory's own. AD's cookie is
// opaque and belongs to the server, so a mock cookie is opaque too — and one this
// directory did not write is refused rather than treated as "start over", which would
// hand a process a full directory it believes to be a change set.
const mockCookiePrefix = "atlas-ad-mock:"

// MockOperation is one thing the mock directory was asked to do. It is what a mockup
// run leaves behind: the worker logs each one, and [MockDirectory.Operations] holds
// the newest for a test to read.
//
// Detail is a short human-readable summary — attribute names and values — with any
// password redacted.
type MockOperation struct {
	Seq    uint64
	Op     string // bind, add, modify, modifydn, delete, dirsync
	DN     string
	Detail string
}

// MockDirectory is an in-memory Active Directory for mock mode. It implements
// [Dialer], so it drops into the worker exactly where [GoDialer] sits, and it is safe
// for concurrent use: one directory serves every job a worker leases.
type MockDirectory struct {
	mu      sync.Mutex
	entries map[string]*mockEntry
	change  uint64 // the DirSync change counter: every write stamps the entry with it
	opSeq   uint64
	ops     []MockOperation
	observe func(MockOperation)
}

// mockEntry is one directory entry. A deleted entry is kept as a tombstone carrying
// isDeleted, because AD reports a deletion as a change rather than as an absence and
// that is the only signal a leaver process has.
type mockEntry struct {
	dn      string
	attrs   map[string][]string
	changed uint64
	deleted bool
}

// NewMockDirectory returns an empty mock directory, or one holding the seed entries —
// the accounts a process expects to find already there. A worker seeds it from an
// LDIF or DSML file.
func NewMockDirectory(seed ...Entry) *MockDirectory {
	d := &MockDirectory{entries: make(map[string]*mockEntry, len(seed))}
	for _, e := range seed {
		d.change++
		d.entries[normalizeDN(e.DN)] = &mockEntry{dn: e.DN, attrs: copyAttrs(e.Attributes), changed: d.change}
	}
	return d
}

// Observe installs a callback run once per operation, after the directory's lock is
// released — so an observer that logs (which is what the worker's does) cannot
// deadlock the directory it is watching. Only one is held; a second replaces it.
func (d *MockDirectory) Observe(fn func(MockOperation)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.observe = fn
}

// Entries returns the live entries, DN-sorted, as a snapshot. Tombstones are not
// among them: they are gone from the directory and present only in a delta.
func (d *MockDirectory) Entries() []Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Entry, 0, len(d.entries))
	for _, e := range d.entries {
		if e.deleted {
			continue
		}
		out = append(out, Entry{DN: e.dn, Attributes: copyAttrs(e.attrs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DN < out[j].DN })
	return out
}

// Operations returns the newest operations the directory was asked to perform,
// oldest first.
func (d *MockDirectory) Operations() []MockOperation {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]MockOperation(nil), d.ops...)
}

// Dial accepts a connection the way a domain controller would: the URL has to be an
// LDAP URL, and a simple bind that names a DN and sends no password is refused — that
// is what an unset ATLAS_CONNECTOR_<REF>_TOKEN looks like on the wire, and finding it
// here is cheaper than finding it against the real directory.
func (d *MockDirectory) Dial(rawURL, bindDN, bindPassword string, startTLS bool) (Conn, error) {
	scheme, ok := ldapScheme(rawURL)
	if !ok {
		return nil, fmt.Errorf("ad: mock: dial %s: not an LDAP URL (want ldap:// or ldaps://)", rawURL)
	}
	if bindDN != "" && bindPassword == "" {
		return nil, fmt.Errorf("ad: mock: bind %s: a simple bind naming a DN with no password is refused, as Active Directory refuses it (is the bind secret set where this job runs?)", bindDN)
	}
	who := bindDN
	if who == "" {
		who = "(anonymous)"
	}
	encrypted := scheme == "ldaps" || startTLS
	_ = d.mutate(func() (string, string, string, error) {
		return "bind", who, fmt.Sprintf("%s encrypted=%t", rawURL, encrypted), nil
	})
	return &mockConn{dir: d, encrypted: encrypted}, nil
}

// mockConn is one connection to the mock directory. It carries whether the channel is
// encrypted, because that is a property of the connection and it decides whether a
// password may be written over it.
type mockConn struct {
	dir       *MockDirectory
	encrypted bool
}

func (c *mockConn) Add(dn string, attrs map[string][]string) error {
	return c.dir.add(dn, attrs, c.encrypted)
}
func (c *mockConn) Modify(dn string, mods []Mod) error { return c.dir.modify(dn, mods, c.encrypted) }
func (c *mockConn) ReadAttr(dn, attr string) ([]string, error) {
	return c.dir.readAttr(dn, attr)
}
func (c *mockConn) ModifyDN(dn, newRDN, newSuperior string) error {
	return c.dir.modifyDN(dn, newRDN, newSuperior)
}
func (c *mockConn) Delete(dn string) error { return c.dir.del(dn) }
func (c *mockConn) DirSync(req DirSyncRequest) (DirSyncResult, error) {
	return c.dir.dirSync(req)
}

// Close is a no-op: the directory outlives the connection, which is the whole point —
// what one job created, the next job finds.
func (c *mockConn) Close() error { return nil }

// mutate runs one operation under the directory's lock and journals what it reports.
// The observer runs afterwards, with the lock released.
func (d *MockDirectory) mutate(fn func() (op, dn, detail string, err error)) error {
	d.mu.Lock()
	op, dn, detail, err := fn()
	if err != nil {
		d.mu.Unlock()
		return err
	}
	rec := d.record(op, dn, detail)
	obs := d.observe
	d.mu.Unlock()
	if obs != nil {
		obs(rec)
	}
	return nil
}

// record appends one operation to the bounded journal. The caller holds the lock.
func (d *MockDirectory) record(op, dn, detail string) MockOperation {
	d.opSeq++
	rec := MockOperation{Seq: d.opSeq, Op: op, DN: dn, Detail: detail}
	d.ops = append(d.ops, rec)
	if len(d.ops) > maxMockOperations {
		d.ops = append(d.ops[:0], d.ops[len(d.ops)-maxMockOperations:]...)
	}
	return rec
}

// add creates an entry. A DN that is already there is refused the way AD refuses it,
// so a replayed job fails rather than quietly creating a second account.
func (d *MockDirectory) add(dn string, attrs map[string][]string, encrypted bool) error {
	return d.mutate(func() (string, string, string, error) {
		key := normalizeDN(dn)
		if e, ok := d.entries[key]; ok && !e.deleted {
			return "", "", "", fmt.Errorf("ad: mock: add %s: entry already exists", dn)
		}
		stored := copyAttrs(attrs)
		hadPassword, err := takePassword(stored, encrypted)
		if err != nil {
			return "", "", "", err
		}
		d.change++
		if hadPassword {
			stored["pwdLastSet"] = []string{strconv.FormatUint(d.change, 10)}
		}
		d.entries[key] = &mockEntry{dn: dn, attrs: stored, changed: d.change}
		return "add", dn, attrsDetail(stored), nil
	})
}

// modify applies the change operations to an existing entry. It is atomic: every
// change is applied to a copy and the copy replaces the entry only if all of them
// hold, so a modify that fails halfway leaves nothing behind.
func (d *MockDirectory) modify(dn string, mods []Mod, encrypted bool) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(dn)
		if err != nil {
			return "", "", "", err
		}
		working := copyAttrs(e.attrs)
		passwordSet := false
		for _, m := range mods {
			if isPasswordAttr(m.Attr) {
				if err := checkPassword(m.Vals, encrypted); err != nil {
					return "", "", "", err
				}
				passwordSet = true
				continue // checked, never kept
			}
			if err := applyMod(working, m, dn); err != nil {
				return "", "", "", err
			}
		}
		d.change++
		if passwordSet {
			working["pwdLastSet"] = []string{strconv.FormatUint(d.change, 10)}
		}
		e.attrs = working
		e.changed = d.change
		return "modify", dn, modsDetail(mods), nil
	})
}

// applyMod applies one change to the working attributes. The three operations are
// AD's own: an incremental add, an incremental delete, and a whole-attribute replace.
func applyMod(attrs map[string][]string, m Mod, dn string) error {
	key, vals, present := findAttr(attrs, m.Attr)
	switch m.Op {
	case modAdd:
		if !present {
			attrs[m.Attr] = append([]string(nil), m.Vals...)
			return nil
		}
		for _, v := range m.Vals {
			if containsFold(vals, v) {
				return fmt.Errorf("ad: mock: modify %s: attribute or value exists: %s already has %q", dn, m.Attr, v)
			}
			vals = append(vals, v)
		}
		attrs[key] = vals
	case modDelete:
		if !present {
			return fmt.Errorf("ad: mock: modify %s: no such attribute: %s", dn, m.Attr)
		}
		if len(m.Vals) == 0 {
			delete(attrs, key)
			return nil
		}
		for _, v := range m.Vals {
			idx := indexFold(vals, v)
			if idx < 0 {
				return fmt.Errorf("ad: mock: modify %s: no such attribute: %s does not have %q", dn, m.Attr, v)
			}
			vals = append(vals[:idx], vals[idx+1:]...)
		}
		attrs[key] = vals
	default: // modReplace
		if present {
			delete(attrs, key)
		}
		if len(m.Vals) > 0 {
			attrs[m.Attr] = append([]string(nil), m.Vals...)
		}
	}
	return nil
}

// readAttr returns one attribute's values, the base-object read behind the
// enable/disable read-modify-write. It is not journaled: it is the internal half of
// an operation the journal already carries.
func (d *MockDirectory) readAttr(dn, attr string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, err := d.liveEntry(dn)
	if err != nil {
		return nil, err
	}
	_, vals, ok := findAttr(e.attrs, attr)
	if !ok {
		return nil, nil
	}
	return append([]string(nil), vals...), nil
}

// modifyDN moves and/or renames an entry, and takes its subtree with it: a child's
// DN *is* its place in the tree, so a container that moves moves everything below it.
// The new relative name is written into the naming attribute, because deleteOldRDN is
// true.
func (d *MockDirectory) modifyDN(dn, newRDN, newSuperior string) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(dn)
		if err != nil {
			return "", "", "", err
		}
		superior := strings.TrimSpace(newSuperior)
		if superior == "" {
			_, superior = splitDN(e.dn)
		}
		newDN := newRDN
		if superior != "" {
			newDN = newRDN + "," + superior
		}
		oldKey, newKey := normalizeDN(dn), normalizeDN(newDN)
		if newKey != oldKey {
			if x, ok := d.entries[newKey]; ok && !x.deleted {
				return "", "", "", fmt.Errorf("ad: mock: modifydn %s: entry already exists: %s", dn, newDN)
			}
		}
		depth := len(rdnComponents(e.dn))
		suffix := "," + oldKey
		moved := make(map[string]*mockEntry)
		for k, ent := range d.entries {
			if ent.deleted || (k != oldKey && !strings.HasSuffix(k, suffix)) {
				continue
			}
			comps := rdnComponents(ent.dn)
			target := newDN
			if kept := comps[:max(0, len(comps)-depth)]; len(kept) > 0 {
				target = strings.Join(kept, ",") + "," + newDN
			}
			delete(d.entries, k)
			d.change++
			ent.dn, ent.changed = target, d.change
			moved[normalizeDN(target)] = ent
		}
		for k, ent := range moved {
			d.entries[k] = ent
		}
		if attr, value, ok := strings.Cut(newRDN, "="); ok {
			e.attrs[namingAttrKey(e.attrs, attr)] = []string{strings.TrimSpace(value)}
		}
		return "modifydn", dn, "→ " + newDN, nil
	})
}

// del removes an entry, refusing one that still has children — which is what a
// leaver process wants: the entry below would otherwise vanish unannounced. What is
// left is a tombstone, so the next delta reports the deletion.
func (d *MockDirectory) del(dn string) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(dn)
		if err != nil {
			return "", "", "", err
		}
		key := normalizeDN(dn)
		for k, child := range d.entries {
			if !child.deleted && k != key && strings.HasSuffix(k, ","+key) {
				return "", "", "", fmt.Errorf("ad: mock: delete %s: not allowed on non-leaf: %s is below it", dn, child.dn)
			}
		}
		d.change++
		e.deleted, e.changed = true, d.change
		e.attrs = map[string][]string{"isDeleted": {"TRUE"}}
		return "delete", dn, "", nil
	})
}

// dirSync performs one DirSync pass: everything under the naming context that
// changed since the cookie, capped, with the cookie the next pass must present.
func (d *MockDirectory) dirSync(req DirSyncRequest) (DirSyncResult, error) {
	d.mu.Lock()
	res, detail, err := d.dirSyncLocked(req)
	if err != nil {
		d.mu.Unlock()
		return DirSyncResult{}, err
	}
	rec := d.record("dirsync", req.BaseDN, detail)
	obs := d.observe
	d.mu.Unlock()
	if obs != nil {
		obs(rec)
	}
	return res, nil
}

// dirSyncLocked is the pass itself. The caller holds the lock.
func (d *MockDirectory) dirSyncLocked(req DirSyncRequest) (DirSyncResult, string, error) {
	base := strings.TrimSpace(req.BaseDN)
	if !isNamingContext(base) {
		return DirSyncResult{}, "", fmt.Errorf("ad: mock: dirsync %q: the base is not a naming context root, and DirSync is answered only at one (e.g. dc=example,dc=com)", req.BaseDN)
	}
	since, err := parseMockCookie(req.Cookie)
	if err != nil {
		return DirSyncResult{}, "", err
	}
	filter, err := parseFilter(req.Filter)
	if err != nil {
		return DirSyncResult{}, "", err
	}
	baseKey := normalizeDN(base)
	changed := make([]*mockEntry, 0, len(d.entries))
	for k, e := range d.entries {
		if e.changed <= since || (k != baseKey && !strings.HasSuffix(k, ","+baseKey)) {
			continue
		}
		// A tombstone is returned whatever the filter says: AD strips a deleted
		// object down to its name, so no filter over its attributes could match, and
		// dropping it would remove the only signal a leaver has.
		if !e.deleted && !filter.match(e.attrs) {
			continue
		}
		changed = append(changed, e)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].changed < changed[j].changed })
	cookieAt, more := d.change, false
	if n := int(req.MaxEntries); n > 0 && len(changed) > n {
		changed = changed[:n]
		cookieAt, more = changed[n-1].changed, true
	}
	out := DirSyncResult{
		Entries: make([]Entry, 0, len(changed)),
		Cookie:  []byte(mockCookiePrefix + strconv.FormatUint(cookieAt, 10)),
		More:    more,
	}
	for _, e := range changed {
		out.Entries = append(out.Entries, Entry{DN: e.dn, Attributes: copyAttrs(e.attrs)})
	}
	return out, fmt.Sprintf("%d change(s) since %d, more=%t", len(out.Entries), since, more), nil
}

// parseMockCookie reads the position out of a cookie a previous pass wrote. Empty is
// a full pass — a reconciliation starts by reading everything.
func parseMockCookie(cookie []byte) (uint64, error) {
	if len(cookie) == 0 {
		return 0, nil
	}
	raw, ok := strings.CutPrefix(string(cookie), mockCookiePrefix)
	if !ok {
		return 0, fmt.Errorf("ad: mock: the sync cookie was not written by this mock directory; a pass cannot resume from it")
	}
	at, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ad: mock: the sync cookie is malformed: %w", err)
	}
	return at, nil
}

// liveEntry returns the entry at a DN, or the error AD gives for one that is not
// there. The caller holds the lock.
func (d *MockDirectory) liveEntry(dn string) (*mockEntry, error) {
	e, ok := d.entries[normalizeDN(dn)]
	if !ok || e.deleted {
		return nil, fmt.Errorf("ad: mock: no such object: %s", dn)
	}
	return e, nil
}

// ldapScheme reports the URL's scheme when it is one an LDAP client can dial.
func ldapScheme(rawURL string) (string, bool) {
	scheme, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(rawURL)), "://")
	if !ok || (scheme != "ldap" && scheme != "ldaps") {
		return "", false
	}
	return scheme, true
}

// isNamingContext reports whether a DN is a domain naming context root — every
// component a dc=. AD answers DirSync only at one, and saying so is more useful than
// the error a domain controller gives.
func isNamingContext(dn string) bool {
	comps := rdnComponents(dn)
	if len(comps) == 0 {
		return false
	}
	for _, c := range comps {
		if !strings.HasPrefix(strings.ToLower(c), "dc=") {
			return false
		}
	}
	return true
}

// normalizeDN is the form two DNs are compared in: case-folded, with the whitespace
// around each relative name trimmed. A directory treats "CN=Arno, OU=users" and
// "cn=arno,ou=users" as one entry, and so does this.
func normalizeDN(dn string) string {
	comps := rdnComponents(dn)
	for i, c := range comps {
		comps[i] = strings.ToLower(c)
	}
	return strings.Join(comps, ",")
}

// rdnComponents splits a DN into its relative names, respecting the backslash escapes
// splitDN respects — so a comma inside a value does not become a component boundary.
func rdnComponents(dn string) []string {
	var out []string
	rest := strings.TrimSpace(dn)
	for rest != "" {
		var rdn string
		rdn, rest = splitDN(rest)
		if rdn != "" {
			out = append(out, rdn)
		}
		rest = strings.TrimSpace(rest)
	}
	return out
}

// namingAttrKey is the key a naming attribute is stored under: the one already there
// in whatever case it was written, or the new name.
func namingAttrKey(attrs map[string][]string, attr string) string {
	if key, _, ok := findAttr(attrs, attr); ok {
		return key
	}
	return strings.TrimSpace(attr)
}

// findAttr looks an attribute up case-insensitively, the way a directory does, and
// returns the key it is actually stored under.
func findAttr(attrs map[string][]string, name string) (string, []string, bool) {
	for k, v := range attrs {
		if strings.EqualFold(k, name) {
			return k, v, true
		}
	}
	return "", nil, false
}

// copyAttrs deep-copies an attribute map, so a snapshot cannot be written through.
func copyAttrs(attrs map[string][]string) map[string][]string {
	out := make(map[string][]string, len(attrs))
	for k, v := range attrs {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func containsFold(vals []string, v string) bool { return indexFold(vals, v) >= 0 }

func indexFold(vals []string, v string) int {
	for i, have := range vals {
		if strings.EqualFold(have, v) {
			return i
		}
	}
	return -1
}

// isPasswordAttr reports whether an attribute is AD's password attribute.
func isPasswordAttr(name string) bool { return strings.EqualFold(name, attrUnicodePwd) }

// takePassword checks and removes a unicodePwd an add carried, reporting whether
// there was one. The value is never stored — see the package's mock-mode note.
func takePassword(attrs map[string][]string, encrypted bool) (bool, error) {
	key, vals, ok := findAttr(attrs, attrUnicodePwd)
	if !ok {
		return false, nil
	}
	if err := checkPassword(vals, encrypted); err != nil {
		return false, err
	}
	delete(attrs, key)
	return true, nil
}

// checkPassword holds a password write to the two rules Active Directory holds it to:
// an encrypted channel, and the unicodePwd encoding. Both are what a first
// set-password gets wrong, and both are cheaper to learn here.
func checkPassword(vals []string, encrypted bool) error {
	if !encrypted {
		return fmt.Errorf("ad: mock: unicodePwd may only be written over an encrypted channel; use ldaps:// or startTLS, which is what Active Directory requires")
	}
	for _, v := range vals {
		if !isPasswordEncoding(v) {
			return fmt.Errorf("ad: mock: unicodePwd must be the password wrapped in double quotes and encoded UTF-16LE, the form Active Directory accepts")
		}
	}
	return nil
}

// isPasswordEncoding reports whether a value is what [encodePassword] produces: the
// password in double quotes, UTF-16LE, no BOM.
func isPasswordEncoding(v string) bool {
	b := []byte(v)
	if len(b) < 4 || len(b)%2 != 0 {
		return false
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	runes := utf16.Decode(units)
	return len(runes) >= 2 && runes[0] == '"' && runes[len(runes)-1] == '"'
}

// attrsDetail summarizes an entry for the journal: its attribute names, sorted.
func attrsDetail(attrs map[string][]string) string {
	return strings.Join(sortedKeys(attrs), ", ")
}

// modsDetail summarizes a modify for the journal. A password is named but never
// shown: what is worth reading is that one was set.
func modsDetail(mods []Mod) string {
	parts := make([]string, 0, len(mods))
	for _, m := range mods {
		verb := "replace"
		switch m.Op {
		case modAdd:
			verb = "add"
		case modDelete:
			verb = "delete"
		}
		if isPasswordAttr(m.Attr) {
			parts = append(parts, verb+" "+m.Attr+"=(redacted)")
			continue
		}
		parts = append(parts, verb+" "+m.Attr+"="+clipDetail(strings.Join(m.Vals, "|")))
	}
	return strings.Join(parts, ", ")
}

// clipDetail keeps one journal line readable.
func clipDetail(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
