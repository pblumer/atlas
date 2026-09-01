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
// group member cannot be added twice, a container with children cannot be deleted, a
// search applies the scope and filter it was given rather than handing back the whole
// forest, and DirSync is answered only at a naming context root — the one error a
// first sync almost always earns.
//
// **The password is the exception, deliberately.** A set-password is checked and then
// *not kept*: the entry records that one was set (`pwdLastSet`), never the value, and
// the operation journal carries no password either. Validating the encoding is what a
// mock is for; storing a credential is not.
//
// **What it is not.** There is no schema, no ACL, no password policy, no replication
// and no naming context beyond the DirSync check above — an add whose parent does not
// exist is accepted, and a search under a base nobody created finds nothing rather
// than refusing, because a mock that demanded a seeded OU chain would cost every test
// a fixture and prove nothing. Of an LDAP filter it applies equality, presence,
// wildcards and the &/|/! that compose them; anything else — ordering, approximate,
// extensible matches — is refused rather than ignored, because a filter silently
// dropped hands a process more than the real directory would. Nothing here is
// durable: it is memory, and a restart is an empty forest.

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
	Seq    uint64 `json:"seq"`
	Op     string `json:"op"` // bind, add, modify, modifydn, delete, dirsync, search
	DN     string `json:"dn"`
	Detail string `json:"detail,omitempty"`
}

// MockDirectory is an in-memory Active Directory for mock mode. It implements
// [Dialer], so it drops into the worker exactly where [GoDialer] sits, and it is safe
// for concurrent use: one directory serves every job a worker leases.
type MockDirectory struct {
	mu sync.Mutex
	// seed is the starting state *every* forest gets a copy of on first contact — the
	// accounts a process expects to find, whichever directory it addresses.
	seed []Entry
	// forests is one in-memory directory per LDAP URL dialled.
	//
	// One mock used to serve every URL, which made it lie in exactly the topology that
	// most needs a mockup: a process addressing two forests found that creating the same
	// DN in the *second* one failed with "entry already exists", something no real pair
	// of domain controllers would ever do
	// (ADR-0206, amended). Keying on the URL is what makes a
	// mock run over several directories mean what the same run would mean in production.
	forests map[string]*mockForest
	opSeq   uint64
	ops     []MockOperation
	observe func(MockOperation)
}

// mockForest is one simulated directory: the entries at one LDAP URL and that
// directory's own DirSync change counter. Two forests share nothing — which is the
// whole point — except the journal and the lock, both of which belong to the worker
// rather than to any one directory.
type mockForest struct {
	url     string
	entries map[string]*mockEntry
	change  uint64 // the DirSync change counter: every write stamps the entry with it
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
	return &MockDirectory{
		seed:    append([]Entry(nil), seed...),
		forests: map[string]*mockForest{},
	}
}

// forestFor returns the directory at this URL, creating it from the seed on first
// contact. The caller holds the lock.
//
// The seed is a *template*, not shared state: each forest gets its own copy, so two
// directories start out looking alike and then diverge exactly as two real ones would
// once a process starts writing to them.
func (d *MockDirectory) forestFor(rawURL string) *mockForest {
	key := strings.ToLower(strings.TrimSpace(rawURL))
	if f, ok := d.forests[key]; ok {
		return f
	}
	f := &mockForest{url: rawURL, entries: make(map[string]*mockEntry, len(d.seed))}
	for _, e := range d.seed {
		f.change++
		f.entries[normalizeDN(e.DN)] = &mockEntry{dn: e.DN, attrs: copyAttrs(e.Attributes), changed: f.change}
	}
	d.forests[key] = f
	return f
}

// urls returns every directory that has been dialled, sorted. The caller holds the lock.
func (d *MockDirectory) urls() []string {
	out := make([]string, 0, len(d.forests))
	for _, f := range d.forests {
		out = append(out, f.url)
	}
	sort.Strings(out)
	return out
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
	var out []Entry
	for _, url := range d.urls() {
		out = append(out, d.forests[strings.ToLower(strings.TrimSpace(url))].live()...)
	}
	return out
}

// EntriesAt returns the live entries of one directory. It is what a test asserting on
// a multi-directory run needs, and what Entries cannot answer: flattened across
// forests, "is this account there?" stops being a question about a directory.
func (d *MockDirectory) EntriesAt(rawURL string) []Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.forests[strings.ToLower(strings.TrimSpace(rawURL))]
	if !ok {
		return nil
	}
	return f.live()
}

// Seed returns the entries every directory starts from — the template, not the state
// of any one of them.
//
// It exists because forests are created on first contact, so before a single job runs
// there is nothing to count: a worker announcing "seeded=0" at startup because nobody
// had dialled yet would take away the one confirmation an operator has that their
// starting entries loaded at all.
func (d *MockDirectory) Seed() []Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Entry(nil), d.seed...)
}

// URLs returns every directory this mock has been asked to reach, sorted — the answer
// to "which forests did that run actually touch?".
func (d *MockDirectory) URLs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.urls()
}

// live is one forest's entries, DN-sorted, as a snapshot. Tombstones are not among
// them: they are gone from the directory and present only in a delta.
func (f *mockForest) live() []Entry {
	out := make([]Entry, 0, len(f.entries))
	for _, e := range f.entries {
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
	// Which directory this connection is to, resolved under the lock the journal write
	// already takes. A URL dialled for the first time gets its own forest, seeded from
	// the template — so "the accounts a process expects to find" apply to whichever
	// directory that process addresses, and two of them stay separate.
	var f *mockForest
	_ = d.mutate(func() (string, string, string, error) {
		f = d.forestFor(rawURL)
		return "bind", who, fmt.Sprintf("%s encrypted=%t", rawURL, encrypted), nil
	})
	return &mockConn{dir: d, forest: f, encrypted: encrypted}, nil
}

// mockConn is one connection to the mock directory. It carries whether the channel is
// encrypted, because that is a property of the connection and it decides whether a
// password may be written over it.
type mockConn struct {
	dir       *MockDirectory
	forest    *mockForest
	encrypted bool
}

func (c *mockConn) Add(dn string, attrs map[string][]string) error {
	return c.dir.add(c.forest, dn, attrs, c.encrypted)
}
func (c *mockConn) Modify(dn string, mods []Mod) error {
	return c.dir.modify(c.forest, dn, mods, c.encrypted)
}
func (c *mockConn) ReadAttr(dn, attr string) ([]string, error) {
	return c.dir.readAttr(c.forest, dn, attr)
}
func (c *mockConn) ModifyDN(dn, newRDN, newSuperior string) error {
	return c.dir.modifyDN(c.forest, dn, newRDN, newSuperior)
}
func (c *mockConn) Delete(dn string) error { return c.dir.del(c.forest, dn) }
func (c *mockConn) DirSync(req DirSyncRequest) (DirSyncResult, error) {
	return c.dir.dirSync(c.forest, req)
}
func (c *mockConn) Search(req SearchRequest) ([]Entry, error) {
	return c.dir.search(c.forest, req)
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
func (d *MockDirectory) add(f *mockForest, dn string, attrs map[string][]string, encrypted bool) error {
	return d.mutate(func() (string, string, string, error) {
		key := normalizeDN(dn)
		if e, ok := f.entries[key]; ok && !e.deleted {
			return "", "", "", fmt.Errorf("ad: mock: add %s: entry already exists", dn)
		}
		stored := copyAttrs(attrs)
		hadPassword, err := takePassword(stored, encrypted)
		if err != nil {
			return "", "", "", err
		}
		f.change++
		if hadPassword {
			stored["pwdLastSet"] = []string{strconv.FormatUint(f.change, 10)}
		}
		f.entries[key] = &mockEntry{dn: dn, attrs: stored, changed: f.change}
		return "add", dn, attrsDetail(stored), nil
	})
}

// modify applies the change operations to an existing entry. It is atomic: every
// change is applied to a copy and the copy replaces the entry only if all of them
// hold, so a modify that fails halfway leaves nothing behind.
func (d *MockDirectory) modify(f *mockForest, dn string, mods []Mod, encrypted bool) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(f, dn)
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
		f.change++
		if passwordSet {
			working["pwdLastSet"] = []string{strconv.FormatUint(f.change, 10)}
		}
		e.attrs = working
		e.changed = f.change
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
func (d *MockDirectory) readAttr(f *mockForest, dn, attr string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, err := d.liveEntry(f, dn)
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
func (d *MockDirectory) modifyDN(f *mockForest, dn, newRDN, newSuperior string) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(f, dn)
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
			if x, ok := f.entries[newKey]; ok && !x.deleted {
				return "", "", "", fmt.Errorf("ad: mock: modifydn %s: entry already exists: %s", dn, newDN)
			}
		}
		depth := len(rdnComponents(e.dn))
		suffix := "," + oldKey
		moved := make(map[string]*mockEntry)
		for k, ent := range f.entries {
			if ent.deleted || (k != oldKey && !strings.HasSuffix(k, suffix)) {
				continue
			}
			comps := rdnComponents(ent.dn)
			target := newDN
			if kept := comps[:max(0, len(comps)-depth)]; len(kept) > 0 {
				target = strings.Join(kept, ",") + "," + newDN
			}
			delete(f.entries, k)
			f.change++
			ent.dn, ent.changed = target, f.change
			moved[normalizeDN(target)] = ent
		}
		for k, ent := range moved {
			f.entries[k] = ent
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
func (d *MockDirectory) del(f *mockForest, dn string) error {
	return d.mutate(func() (string, string, string, error) {
		e, err := d.liveEntry(f, dn)
		if err != nil {
			return "", "", "", err
		}
		key := normalizeDN(dn)
		for k, child := range f.entries {
			if !child.deleted && k != key && strings.HasSuffix(k, ","+key) {
				return "", "", "", fmt.Errorf("ad: mock: delete %s: not allowed on non-leaf: %s is below it", dn, child.dn)
			}
		}
		f.change++
		e.deleted, e.changed = true, f.change
		e.attrs = map[string][]string{"isDeleted": {"TRUE"}}
		return "delete", dn, "", nil
	})
}

// search answers what is under a base right now: the live entries the scope selects
// and the filter matches, DN-sorted, capped.
//
// Two ways it is faithful, and one way it deliberately is not. It applies the filter
// and the scope, because a mock that returned more than the real directory would teach
// a process to be wrong; and it refuses to truncate at the cap, because a short result
// set is a wrong answer rather than a partial one. But it does **not** require the base
// itself to exist: this directory accepts an add whose parent was never created (see
// the note on the type), so demanding a seeded OU chain here would refuse the very
// mockup runs the rest of the mock is built to allow. A base with nothing under it
// finds nothing, which is also the answer an existence check is asking for.
func (d *MockDirectory) search(f *mockForest, req SearchRequest) ([]Entry, error) {
	d.mu.Lock()
	out, err := d.searchLocked(f, req)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	rec := d.record("search", req.BaseDN, fmt.Sprintf("%s %s → %d entr%s", scopeName(req.Scope), filterDetail(req.Filter), len(out), plural(len(out))))
	obs := d.observe
	d.mu.Unlock()
	if obs != nil {
		obs(rec)
	}
	return out, nil
}

// searchLocked is the search itself. The caller holds the lock.
func (d *MockDirectory) searchLocked(f *mockForest, req SearchRequest) ([]Entry, error) {
	filter, err := parseFilter(req.Filter)
	if err != nil {
		return nil, err
	}
	baseKey := normalizeDN(req.BaseDN)
	baseDepth := len(rdnComponents(req.BaseDN))
	out := make([]Entry, 0, len(f.entries))
	for k, e := range f.entries {
		// A tombstone is not in the directory any more. It is a delta's business, not
		// a search's: an existence check must not find an entry that was deleted.
		if e.deleted || !inScope(req.Scope, k, baseKey, len(rdnComponents(e.dn)), baseDepth) {
			continue
		}
		if !filter.match(e.attrs) {
			continue
		}
		out = append(out, Entry{DN: e.dn, Attributes: copyAttrs(e.attrs)})
	}
	if max := int(req.MaxEntries); max > 0 && len(out) > max {
		return nil, fmt.Errorf("ad: mock: the search under %s returned more than the %d-entry cap; narrow the filter or raise maxEntries (truncating would be a wrong answer, not a partial one)", req.BaseDN, max)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DN < out[j].DN })
	return out, nil
}

// inScope reports whether an entry is within the search scope: base is the entry at
// the base itself, one its immediate children, and sub — the default — the base and
// everything below it.
func inScope(scope, key, baseKey string, depth, baseDepth int) bool {
	below := strings.HasSuffix(key, ","+baseKey)
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "base":
		return key == baseKey
	case "one":
		return below && depth == baseDepth+1
	default:
		return key == baseKey || below
	}
}

// scopeName renders a scope for the journal, naming the default rather than leaving
// the line silent about which one ran.
func scopeName(scope string) string {
	if s := strings.ToLower(strings.TrimSpace(scope)); s != "" {
		return s
	}
	return "sub"
}

// filterDetail renders a filter for the journal, saying so when there is none.
func filterDetail(filter string) string {
	if f := strings.TrimSpace(filter); f != "" {
		return f
	}
	return "(objectClass=*)"
}

// plural is the "y"/"ies" of the journal's entry count.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// dirSync performs one DirSync pass: everything under the naming context that
// changed since the cookie, capped, with the cookie the next pass must present.
func (d *MockDirectory) dirSync(f *mockForest, req DirSyncRequest) (DirSyncResult, error) {
	d.mu.Lock()
	res, detail, err := d.dirSyncLocked(f, req)
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
func (d *MockDirectory) dirSyncLocked(f *mockForest, req DirSyncRequest) (DirSyncResult, string, error) {
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
	changed := make([]*mockEntry, 0, len(f.entries))
	for k, e := range f.entries {
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
	cookieAt, more := f.change, false
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
func (d *MockDirectory) liveEntry(f *mockForest, dn string) (*mockEntry, error) {
	e, ok := f.entries[normalizeDN(dn)]
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
