package ad_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// The mock directory is a [ad.Conn] as well as a [ad.Dialer], and a caller embedding
// the package can drive it directly — the connector's own operations are only some of
// what LDAP's modify can say. These tests take that route for the change shapes a job
// cannot author, and for the answers a connection gives about entries.

// conn dials the mock the way the connector does.
func conn(t *testing.T, d *ad.MockDirectory) ad.Conn {
	t.Helper()
	c, err := d.Dial(mockTLSURL, "", "", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A delete with no values removes the whole attribute, and a replace with none does
// the same — LDAP says both, even though the AD connector's own operations author
// neither.
func TestMockModifyRemovesAnAttributeWholesale(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{
		"title": {"Chef"}, "department": {"IT"},
	}})
	c := conn(t, d)
	if err := c.Modify(arnoDN, []ad.Mod{{Op: 1, Attr: "title"}}); err != nil { // delete
		t.Fatalf("delete the attribute: %v", err)
	}
	if err := c.Modify(arnoDN, []ad.Mod{{Op: 2, Attr: "department"}}); err != nil { // replace with nothing
		t.Fatalf("replace with no values: %v", err)
	}
	if attrs := entry(t, d, arnoDN).Attributes; len(attrs) != 0 {
		t.Errorf("attributes = %v, want both gone", attrs)
	}
}

// An attribute an entry does not have reads as nothing rather than as an error: it is
// the absence the enable/disable read-modify-write starts from when a seeded account
// carries no userAccountControl.
func TestMockReadAttrOfAnAbsentAttribute(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	vals, err := conn(t, d).ReadAttr(arnoDN, "userAccountControl")
	if err != nil || len(vals) != 0 {
		t.Errorf("ReadAttr = %v, %v; want no values and no error", vals, err)
	}
	if _, err := conn(t, d).ReadAttr("cn=Nobody,dc=example,dc=com", "cn"); err == nil {
		t.Error("a read of an entry that is not there succeeded")
	}
	// Enable on such an account starts from the normal-account baseline.
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "enable", DN: arnoDN})
	if got := entry(t, d, arnoDN).Attributes["userAccountControl"]; got[0] != "512" {
		t.Errorf("userAccountControl = %v, want the normal-account baseline", got)
	}
}

// A rename in place keeps the entry where it is and gives it a new relative name,
// which is what a person who changed their surname gets.
func TestMockRenameInPlace(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{"CN": {"Arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "move", DN: arnoDN,
		NewDN: "cn=Arno Meier,ou=users,dc=example,dc=com"})
	e := entry(t, d, "cn=Arno Meier,ou=users,dc=example,dc=com")
	// The naming attribute keeps the case it was stored under; only its value moves.
	if got := e.Attributes["CN"]; len(got) != 1 || got[0] != "Arno Meier" {
		t.Errorf("cn = %v, want the new relative name in the attribute it was already in", e.Attributes)
	}
}

// A move of an entry that carries no naming attribute at all gains one, because the
// relative name is written whether or not it was there before.
func TestMockMoveWritesTheNamingAttribute(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "move", DN: arnoDN,
		NewDN: "cn=Arno,ou=extern,dc=example,dc=com"})
	if got := entry(t, d, "cn=Arno,ou=extern,dc=example,dc=com").Attributes["cn"]; len(got) != 1 {
		t.Errorf("cn = %v, want the relative name written", got)
	}
}

// A create may carry the password with it, and the same two rules apply: an encrypted
// channel, and AD's encoding. The value is checked and dropped either way.
func TestMockCreateCarryingAPassword(t *testing.T) {
	d := ad.NewMockDirectory()
	c := conn(t, d)
	pwd := encodedPassword("N3w!pass")
	if err := c.Add(arnoDN, map[string][]string{"cn": {"Arno"}, "unicodePwd": {pwd}}); err != nil {
		t.Fatalf("Add with a password: %v", err)
	}
	e := entry(t, d, arnoDN)
	if _, stored := e.Attributes["unicodePwd"]; stored {
		t.Error("the mock stored the password an add carried")
	}
	if _, ok := e.Attributes["pwdLastSet"]; !ok {
		t.Errorf("attributes = %v, want the record that a password was set", e.Attributes)
	}

	plain, err := d.Dial(mockPlainURL, "", "", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = plain.Close() }()
	if err := plain.Add("cn=Ada,ou=users,dc=example,dc=com", map[string][]string{"unicodePwd": {pwd}}); err == nil {
		t.Error("a create carrying a password over an unencrypted channel was accepted")
	}
	// A value that is not AD's encoding is refused even over TLS — an odd byte count
	// cannot be UTF-16 at all.
	if err := c.Add("cn=Eva,ou=users,dc=example,dc=com", map[string][]string{"unicodePwd": {"abc"}}); err == nil {
		t.Error("a unicodePwd that is not UTF-16LE was accepted")
	}
}

// The journal keeps a line readable: a modify writing a long value is clipped rather
// than putting a kilobyte into the worker's log.
func TestMockJournalClipsALongValue(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN,
		Attributes: map[string][]string{"description": {strings.Repeat("lang ", 60)}}})
	ops := d.Operations()
	if last := ops[len(ops)-1]; len(last.Detail) > 160 || !strings.HasSuffix(last.Detail, "…") {
		t.Errorf("detail = %q, want it clipped", last.Detail)
	}
}

// A sync is journaled too, and an observer sees it: a reconciliation that returns
// nothing is a fact worth having in the log.
func TestMockSyncIsObserved(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	var seen []string
	d.Observe(func(op ad.MockOperation) { seen = append(seen, op.Op) })
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		ResultVariable: "changes", CookieVariable: "cookie"})
	if len(seen) != 2 || seen[1] != "dirsync" {
		t.Errorf("observed = %v, want the bind and the dirsync", seen)
	}
}

// A base that is not a DN at all fails the same way an OU does, and a cookie that
// carries this directory's own marker but no position is malformed rather than a
// starting point.
func TestMockSyncBaseAndCookieEdges(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	if err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: "",
		ResultVariable: "changes", CookieVariable: "cookie"}); !strings.Contains(err.Error(), "naming context") {
		t.Errorf("error = %v, want the naming-context refusal", err)
	}
	err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		ResultVariable: "changes", CookieVariable: "cookie",
		Cookie: base64.StdEncoding.EncodeToString([]byte("atlas-ad-mock:nicht-zahl")),
	})
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error = %v, want the malformed cookie", err)
	}
}

// The filter's remaining shapes: a disjunction, wildcards in the middle and at the
// front, and the malformed filters that are refused rather than approximated.
func TestMockFilterShapes(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{
			"objectClass": {"user"}, "cn": {"Arno"}, "mail": {"arno@example.com"},
		}},
		ad.Entry{DN: "cn=Extern,ou=users,dc=example,dc=com", Attributes: map[string][]string{
			"objectClass": {"contact"}, "cn": {"Extern"},
		}},
	)
	for _, tc := range []struct {
		filter string
		want   int
	}{
		{"(|(objectClass=user)(objectClass=contact))", 2},
		{"(|(objectClass=computer)(cn=Arno))", 1},
		{"(cn=A*o)", 1},
		{"(cn=*rno)", 1},
		{"(cn=*z*)", 0},
		{"(mail=*)", 1},
		{"(objectClass=*)", 2},
		{"(cn=A*n*o)", 1},
	} {
		out := run(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
			Filter: tc.filter, ResultVariable: "changes", CookieVariable: "cookie"})
		if got := len(syncEntries(t, out)); got != tc.want {
			t.Errorf("%s matched %d entries, want %d", tc.filter, got, tc.want)
		}
	}
	for _, bad := range []string{"(&)", "(!(cn=a)(cn=b))", "(cn=a)(cn=b)", "(=Arno)", "(cn=)", "(cn~=Arno)"} {
		if err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
			Filter: bad, ResultVariable: "changes", CookieVariable: "cookie"}); !strings.Contains(err.Error(), "filter") {
			t.Errorf("%s: error = %v, want it refused as a filter", bad, err)
		}
	}
}

// encodedPassword is what the connector puts on the wire for a set-password: the
// password in double quotes, UTF-16LE.
func encodedPassword(pw string) string {
	quoted := `"` + pw + `"`
	b := make([]byte, 0, len(quoted)*2)
	for _, r := range quoted {
		b = append(b, byte(r), byte(r>>8))
	}
	return string(b)
}

// The mock is a Dialer, so it is what a caller embedding the worker hands to Run —
// this is that call, with the context the worker passes.
func TestMockIsADialerRunCanUse(t *testing.T) {
	var d ad.Dialer = ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	if _, err := ad.Run(context.Background(), ad.Job{
		URL: mockTLSURL, Operation: "delete", DN: arnoDN,
	}, d, nil, nil); err != nil {
		t.Fatalf("Run against the mock: %v", err)
	}
}

// A create whose entry object resolved to nothing is refused, not performed and not
// crashed on. This is a regression test for a panic, and one that reached a *real*
// directory as readily as a mock: dispatch wrote the default objectClass into the job's
// attribute map, and an entryVariable that resolved to nothing left that map nil, so a
// joiner whose FEEL variable was empty or misspelled took the AD worker down with
// "assignment to entry in nil map" instead of failing its job
// (ADR-draft-atlas-manages-the-ad-mock-seed).
func TestACreateWithNoAttributesIsRefusedRatherThanCrashing(t *testing.T) {
	for _, op := range []string{"create-user", "create-group", "create-contact"} {
		t.Run(op, func(t *testing.T) {
			mock := ad.NewMockDirectory()
			_, err := ad.Run(context.Background(), ad.Job{
				URL: "ldaps://dc", Operation: op, DN: "cn=Neu,dc=example,dc=com",
			}, mock, func(string) string { return "pw" }, nil)
			if err == nil {
				t.Fatal("a create with no attributes was performed")
			}
			if !strings.Contains(err.Error(), "no attributes") {
				t.Errorf("error = %v, want it to name the empty entry object", err)
			}
			// And nothing was written on the way out: a refused create leaves the
			// directory as it was, rather than a nameless entry carrying an
			// objectClass and nothing else.
			if got := mock.Entries(); len(got) != 0 {
				t.Errorf("entries = %v, want the directory untouched", got)
			}
		})
	}
}

// Two forests are two directories. The mock used to serve every URL from one set of
// entries, which made it lie in exactly the topology that most needs a mockup: a
// process addressing two directories found that creating an account in the *second*
// failed with "entry already exists", something no real pair of domain controllers
// would ever do (ADR-draft-ad-as-a-console-connector, amended).
func TestTwoDirectoriesAreTwoForests(t *testing.T) {
	const (
		prod = "ldaps://dc-prod.example.com:636"
		test = "ldaps://dc-test.example.com:636"
	)
	mock := ad.NewMockDirectory()
	secret := func(string) string { return "pw" }
	create := func(url string) error {
		_, err := ad.Run(context.Background(), ad.Job{
			URL: url, BindDN: "cn=svc,dc=x", BindSecret: "S", Operation: "create-user",
			DN: "cn=Ada,dc=example,dc=com", Attributes: map[string][]string{"sAMAccountName": {"ada"}},
		}, mock, secret, nil)
		return err
	}

	if err := create(prod); err != nil {
		t.Fatalf("create in the production forest: %v", err)
	}
	// The same DN in a *different* directory is a different account, and must succeed.
	if err := create(test); err != nil {
		t.Fatalf("create of the same DN in a different forest: %v — the forests are not separate", err)
	}
	// A second create in the *same* forest is still refused, exactly as AD refuses it.
	if err := create(prod); err == nil {
		t.Error("the same DN was created twice in one forest")
	}

	for _, url := range []string{prod, test} {
		if got := mock.EntriesAt(url); len(got) != 1 {
			t.Errorf("%s holds %v, want exactly its own account", url, got)
		}
	}
	if got := mock.URLs(); len(got) != 2 {
		t.Errorf("URLs = %v, want both directories", got)
	}
}

// A write in one forest does not show up in another. Creating separate maps is easy to
// get right; keeping the *operations* pointed at the right one is where a refactor
// would quietly reconverge them.
func TestAWriteInOneForestDoesNotReachAnother(t *testing.T) {
	const (
		a = "ldaps://dc-a.example.com:636"
		b = "ldaps://dc-b.example.com:636"
	)
	mock := ad.NewMockDirectory(ad.Entry{
		DN: "cn=Ada,dc=example,dc=com", Attributes: map[string][]string{"userAccountControl": {"512"}},
	})
	secret := func(string) string { return "pw" }
	run := func(url, op string) error {
		_, err := ad.Run(context.Background(), ad.Job{
			URL: url, BindDN: "cn=svc,dc=x", BindSecret: "S",
			Operation: op, DN: "cn=Ada,dc=example,dc=com",
		}, mock, secret, nil)
		return err
	}

	// The seed reaches both, because it says what a process expects to find wherever it
	// looks — but each gets its own copy.
	if err := run(a, "disable"); err != nil {
		t.Fatalf("disable in forest a: %v", err)
	}
	if err := run(b, "enable"); err != nil {
		t.Fatalf("enable in forest b: %v", err)
	}
	got := map[string]string{}
	for _, url := range []string{a, b} {
		entries := mock.EntriesAt(url)
		if len(entries) != 1 {
			t.Fatalf("%s holds %v, want the seeded account", url, entries)
		}
		got[url] = entries[0].Attributes["userAccountControl"][0]
	}
	if got[a] != "514" {
		t.Errorf("forest a userAccountControl = %q, want the disabled account", got[a])
	}
	if got[b] != "512" {
		t.Errorf("forest b userAccountControl = %q, want the enabled account — the disable leaked across forests", got[b])
	}
}

// A DirSync delta is one directory's own history. Sharing a change counter would make a
// reconciliation loop over one forest report writes that happened in another — the
// worst kind of wrong answer, because the cookie makes it look authoritative.
func TestADirSyncDeltaIsOneForestsOwnHistory(t *testing.T) {
	const (
		a = "ldaps://dc-a.example.com:636"
		b = "ldaps://dc-b.example.com:636"
	)
	mock := ad.NewMockDirectory()
	secret := func(string) string { return "pw" }
	create := func(url, cn string) {
		t.Helper()
		if _, err := ad.Run(context.Background(), ad.Job{
			URL: url, BindDN: "cn=svc,dc=x", BindSecret: "S", Operation: "create-user",
			DN: "cn=" + cn + ",dc=example,dc=com", Attributes: map[string][]string{"cn": {cn}},
		}, mock, secret, nil); err != nil {
			t.Fatalf("create %s in %s: %v", cn, url, err)
		}
	}
	create(a, "Ada")
	create(b, "Grace")

	out, err := ad.Run(context.Background(), ad.Job{
		URL: a, BindDN: "cn=svc,dc=x", BindSecret: "S", Operation: "sync",
		BaseDN: "dc=example,dc=com", CookieVariable: "c", ResultVariable: "changes",
	}, mock, secret, nil)
	if err != nil {
		t.Fatalf("sync forest a: %v", err)
	}
	// The delta is {entries, more}: a pass is bounded, so the caller is told whether to
	// come back rather than left to guess from a length.
	delta, ok := out["changes"].(map[string]any)
	if !ok {
		t.Fatalf("changes = %#v, want the delta object", out["changes"])
	}
	entries, ok := delta["entries"].([]any)
	if !ok {
		t.Fatalf("entries = %#v, want a list", delta["entries"])
	}
	if len(entries) != 1 {
		t.Fatalf("forest a reported %d changes, want only its own", len(entries))
	}
	if got := fmt.Sprint(entries[0]); !strings.Contains(got, "Ada") || strings.Contains(got, "Grace") {
		t.Errorf("forest a reported %v, want its own account and not the other forest's", got)
	}
}

// The three ways to ask a multi-directory mock what happened. They exist because
// flattening stopped being an answer once one mock could hold several forests: "is this
// account there?" is a question about a *directory*, and "did the seed load?" is a
// question about neither — it is about the template every directory starts from.
func TestAskingAMockWhichDirectoryHoldsWhat(t *testing.T) {
	const url = "ldaps://dc.example.com:636"
	seed := ad.Entry{DN: "cn=Ada,dc=example,dc=com", Attributes: map[string][]string{"cn": {"Ada"}}}
	mock := ad.NewMockDirectory(seed)

	// Before anything dials, the mock holds a template and no directories at all. The
	// seed still reads back — a worker announces it at startup, before any job runs.
	if got := mock.Seed(); len(got) != 1 || got[0].DN != seed.DN {
		t.Errorf("Seed() = %v, want the template it was built with", got)
	}
	if got := mock.URLs(); len(got) != 0 {
		t.Errorf("URLs() = %v, want none before anything dialled", got)
	}
	if got := mock.EntriesAt(url); got != nil {
		t.Errorf("EntriesAt(%q) = %v, want nothing for a directory nobody has reached", url, got)
	}

	if _, err := ad.Run(context.Background(), ad.Job{
		URL: url, BindDN: "cn=svc,dc=x", BindSecret: "S",
		Operation: "disable", DN: "cn=Ada,dc=example,dc=com",
	}, mock, func(string) string { return "pw" }, nil); err != nil {
		t.Fatalf("disable against the seeded directory: %v", err)
	}

	if got := mock.URLs(); len(got) != 1 || got[0] != url {
		t.Errorf("URLs() = %v, want the one directory that was dialled", got)
	}
	if got := mock.EntriesAt(url); len(got) != 1 {
		t.Errorf("EntriesAt(%q) = %v, want its seeded account", url, got)
	}
	// A URL differing only in case is the same directory: scheme and host are
	// case-insensitive, and two forests for one domain controller would be the bug this
	// keying exists to avoid, wearing different clothes.
	if got := mock.EntriesAt("LDAPS://DC.EXAMPLE.COM:636"); len(got) != 1 {
		t.Errorf("a differently-cased URL found %v, want the same directory", got)
	}
	// Mutating the returned seed must not reach the mock: it is a template, and a
	// caller holding a slice into it could reseed every directory dialled afterwards.
	mock.Seed()[0].DN = "cn=Someone Else,dc=example,dc=com"
	if got := mock.Seed(); got[0].DN != seed.DN {
		t.Errorf("Seed() = %v after a caller edited what it returned, want the template intact", got)
	}
}
