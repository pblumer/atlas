package ad_test

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// The mock directory is what makes an AD process testable without a domain
// controller: the same resolved job, the same Run, a directory that lives in the
// worker's memory. These tests are therefore written against ad.Run rather than
// against the Conn methods — what a model authors is a job, and the job is what has
// to work.

const (
	mockTLSURL   = "ldaps://dc.example.com:636"
	mockPlainURL = "ldap://dc.example.com:389"
	arnoDN       = "cn=Arno,ou=users,dc=example,dc=com"
	usersDN      = "ou=users,dc=example,dc=com"
	baseDN       = "dc=example,dc=com"
)

// run performs a resolved job against the mock, the way the worker does.
func run(t *testing.T, d *ad.MockDirectory, j ad.Job) map[string]any {
	t.Helper()
	out, err := ad.Run(context.Background(), j, d, nil, nil)
	if err != nil {
		t.Fatalf("%s %s: %v", j.Operation, j.DN, err)
	}
	return out
}

// runErr performs a job expected to fail and returns the error.
func runErr(t *testing.T, d *ad.MockDirectory, j ad.Job) error {
	t.Helper()
	_, err := ad.Run(context.Background(), j, d, nil, nil)
	if err == nil {
		t.Fatalf("%s %s succeeded, want a failure", j.Operation, j.DN)
	}
	return err
}

// entry returns one live entry by DN, or fails the test.
func entry(t *testing.T, d *ad.MockDirectory, dn string) ad.Entry {
	t.Helper()
	for _, e := range d.Entries() {
		if strings.EqualFold(e.DN, dn) {
			return e
		}
	}
	t.Fatalf("no entry %q; have %v", dn, dns(d))
	return ad.Entry{}
}

func dns(d *ad.MockDirectory) []string {
	out := make([]string, 0, len(d.Entries()))
	for _, e := range d.Entries() {
		out = append(out, e.DN)
	}
	return out
}

// A create-user lands in the directory with the object classes the worker
// supplies, and a second create of the same DN is refused — Active Directory refuses
// it, and job delivery is at-least-once, so a mock that quietly accepted a replay
// would hide the one failure a create actually has.
func TestMockCreateThenRefusesTheReplay(t *testing.T) {
	d := ad.NewMockDirectory()
	create := ad.Job{
		URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}},
	}
	run(t, d, create)

	e := entry(t, d, arnoDN)
	if got := e.Attributes["sAMAccountName"]; len(got) != 1 || got[0] != "arno" {
		t.Errorf("sAMAccountName = %v, want the authored value", got)
	}
	if got := e.Attributes["objectClass"]; len(got) == 0 || got[len(got)-1] != "user" {
		t.Errorf("objectClass = %v, want the worker's user chain", got)
	}

	err := runErr(t, d, create)
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want the entry-already-exists Active Directory answers with", err)
	}
}

// Disable sets the ACCOUNTDISABLE bit and leaves every other flag alone; enable
// clears it again. The mock is where that read-modify-write can be seen at all —
// the fakes in the other tests answer a canned value.
func TestMockEnableDisableKeepsTheOtherFlags(t *testing.T) {
	// 66048 = 512 (normal account) | 65536 (password never expires).
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{
		"userAccountControl": {"66048"},
	}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "disable", DN: arnoDN})
	if got := entry(t, d, arnoDN).Attributes["userAccountControl"]; got[0] != "66050" {
		t.Errorf("userAccountControl = %v, want 66050 (66048 with ACCOUNTDISABLE set)", got)
	}
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "enable", DN: arnoDN})
	if got := entry(t, d, arnoDN).Attributes["userAccountControl"]; got[0] != "66048" {
		t.Errorf("userAccountControl = %v, want the flag cleared and the rest kept", got)
	}
}

// A password may only be set over an encrypted channel. That is the rule a first
// set-password most often breaks, so the mock enforces it rather than accepting what
// the real directory will refuse.
func TestMockSetPasswordNeedsAnEncryptedChannel(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{
		URL: mockPlainURL, Operation: "set-password", DN: arnoDN, NewPassword: "N3w!pass",
	})
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("error = %v, want it to name the missing encryption", err)
	}
	// StartTLS over the same plain URL is encryption enough, exactly as it is for AD.
	run(t, d, ad.Job{
		URL: mockPlainURL, StartTLS: true, Operation: "set-password", DN: arnoDN, NewPassword: "N3w!pass",
	})
	e := entry(t, d, arnoDN)
	if _, stored := e.Attributes["unicodePwd"]; stored {
		t.Error("the mock stored the password; it must record that one was set, not the value")
	}
	if _, ok := e.Attributes["pwdLastSet"]; !ok {
		t.Errorf("no pwdLastSet after a set-password; attributes = %v", e.Attributes)
	}
	for _, op := range d.Operations() {
		if strings.Contains(op.Detail, "N3w!pass") {
			t.Errorf("the journal carries the password: %q", op.Detail)
		}
	}
}

// A unicodePwd written by hand — through update-attributes rather than through
// set-password — must carry AD's encoding. Checking it is what lets the mock prove
// the worker's encoding is right without keeping the password.
func TestMockRejectsAPasswordThatIsNotADsEncoding(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN,
		Attributes: map[string][]string{"unicodePwd": {"geheim"}},
	})
	if !strings.Contains(err.Error(), "UTF-16LE") {
		t.Errorf("error = %v, want it to name the encoding AD requires", err)
	}
}

// Group membership is an incremental change: adding a member that is already there
// fails, and so does removing one that is not. Both are what a replayed job meets.
func TestMockGroupMembershipIsIncremental(t *testing.T) {
	const groupDN = "cn=Admins,ou=groups,dc=example,dc=com"
	d := ad.NewMockDirectory(ad.Entry{DN: groupDN}, ad.Entry{DN: arnoDN})
	add := ad.Job{URL: mockTLSURL, Operation: "add-group-member", DN: groupDN, MemberDN: arnoDN}
	run(t, d, add)
	if got := entry(t, d, groupDN).Attributes["member"]; len(got) != 1 || got[0] != arnoDN {
		t.Errorf("member = %v, want the one added member", got)
	}
	if err := runErr(t, d, add); !strings.Contains(err.Error(), "exists") {
		t.Errorf("error = %v, want the attribute-or-value-exists AD answers a replayed add with", err)
	}

	remove := ad.Job{URL: mockTLSURL, Operation: "remove-group-member", DN: groupDN, MemberDN: arnoDN}
	run(t, d, remove)
	if got := entry(t, d, groupDN).Attributes["member"]; len(got) != 0 {
		t.Errorf("member = %v, want the value gone", got)
	}
	if err := runErr(t, d, remove); !strings.Contains(err.Error(), "no such attribute") {
		t.Errorf("error = %v, want the no-such-attribute a second remove gets", err)
	}
}

// A mover is a DN change, and in a directory a move takes the whole subtree with it —
// the child's DN is its place in the tree. The relative name is written into the
// naming attribute, because deleteOldRDN is true.
func TestMockMoveTakesTheSubtreeAlong(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: usersDN, Attributes: map[string][]string{"ou": {"users"}}},
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}},
	)
	run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "move", DN: usersDN, NewDN: "ou=people,dc=example,dc=com",
	})
	moved := entry(t, d, "ou=people,dc=example,dc=com")
	if got := moved.Attributes["ou"]; len(got) != 1 || got[0] != "people" {
		t.Errorf("ou = %v, want the new relative name (deleteOldRDN is true)", got)
	}
	child := entry(t, d, "cn=Arno,ou=people,dc=example,dc=com")
	if got := child.Attributes["cn"]; got[0] != "Arno" {
		t.Errorf("the child lost its attributes on the move: %v", child.Attributes)
	}
	if len(d.Entries()) != 2 {
		t.Errorf("entries = %v, want the two moved ones and no copy left behind", dns(d))
	}
}

// A container with children cannot be deleted, which is the behaviour a leaver
// process wants: the entry below it would otherwise vanish unannounced.
func TestMockDeleteRefusesANonLeaf(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: usersDN}, ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: usersDN})
	if !strings.Contains(err.Error(), "not allowed on non-leaf") {
		t.Errorf("error = %v, want AD's not-allowed-on-non-leaf", err)
	}
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: arnoDN})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: usersDN})
	if len(d.Entries()) != 0 {
		t.Errorf("entries = %v, want an empty directory", dns(d))
	}
}

// Every operation on an entry that is not there fails the way AD fails it, so a
// process meets the same incident in a test as in production.
func TestMockRefusesWhatIsNotThere(t *testing.T) {
	d := ad.NewMockDirectory()
	for _, j := range []ad.Job{
		{URL: mockTLSURL, Operation: "disable", DN: arnoDN},
		{URL: mockTLSURL, Operation: "delete", DN: arnoDN},
		{URL: mockTLSURL, Operation: "move", DN: arnoDN, NewDN: "cn=Arno,dc=example,dc=com"},
		{URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN, Attributes: map[string][]string{"title": {"x"}}},
		{URL: mockTLSURL, Operation: "set-password", DN: arnoDN, NewPassword: "x"},
	} {
		if err := runErr(t, d, j); !strings.Contains(err.Error(), "no such object") {
			t.Errorf("%s: error = %v, want no such object", j.Operation, err)
		}
	}
}

// A move onto a DN that is taken is refused rather than silently overwriting the
// entry that is already there.
func TestMockMoveOntoAnExistingDN(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN}, ad.Entry{DN: "cn=Ada,ou=users,dc=example,dc=com"})
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "move", DN: arnoDN, NewDN: "cn=Ada,ou=users,dc=example,dc=com",
	})
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want already exists", err)
	}
}

// A dial the real directory would refuse is refused here too: a URL that is not an
// LDAP URL, and a simple bind that names a DN with no password behind it — which is
// what an unset ATLAS_CONNECTOR_<REF>_TOKEN looks like on the wire.
func TestMockRefusesADialADirectoryWouldRefuse(t *testing.T) {
	d := ad.NewMockDirectory()
	err := runErr(t, d, ad.Job{URL: "dc.example.com", Operation: "delete", DN: arnoDN})
	if !strings.Contains(err.Error(), "LDAP URL") {
		t.Errorf("error = %v, want it to name the missing scheme", err)
	}
	err = runErr(t, d, ad.Job{URL: mockTLSURL, BindDN: "cn=svc,dc=example,dc=com", Operation: "delete", DN: arnoDN})
	if !strings.Contains(err.Error(), "bind") {
		t.Errorf("error = %v, want the refused bind", err)
	}
}

// A sync returns what changed since the cookie it was given, and writes the next
// cookie back into the variable it read — the whole of how a reconciliation loop
// carries its own position forward.
func TestMockSyncReturnsOnlyWhatChanged(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: "cn=Ada,ou=users,dc=example,dc=com",
		Attributes: map[string][]string{"sAMAccountName": {"ada"}}})

	sync := ad.Job{
		URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		ResultVariable: "changes", CookieVariable: "cookie",
	}
	out := run(t, d, sync)
	if n := len(syncEntries(t, out)); n != 2 {
		t.Fatalf("first pass returned %d entries, want both", n)
	}
	cookie, _ := out["cookie"].(string)
	if cookie == "" {
		t.Fatal("no cookie written back; a loop could not carry itself forward")
	}
	if _, err := base64.StdEncoding.DecodeString(cookie); err != nil {
		t.Fatalf("the cookie is not the base64 a process variable can hold: %v", err)
	}

	// Nothing changed since: an empty delta, and a fresh cookie.
	sync.Cookie = cookie
	out = run(t, d, sync)
	if n := len(syncEntries(t, out)); n != 0 {
		t.Errorf("a pass with nothing to report returned %d entries", n)
	}

	// One change, one entry.
	sync.Cookie, _ = out["cookie"].(string)
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN,
		Attributes: map[string][]string{"title": {"Chef"}}})
	out = run(t, d, sync)
	got := syncEntries(t, out)
	if len(got) != 1 {
		t.Fatalf("delta = %v, want the one changed entry", got)
	}
	if e, _ := got[0].(map[string]any); !strings.EqualFold(e["dn"].(string), arnoDN) {
		t.Errorf("changed entry = %v, want %s", got[0], arnoDN)
	}
}

// maxEntries caps a pass, and the cap is not a loss: the server says more is waiting
// and the next pass resumes exactly where this one stopped.
func TestMockSyncCapIsResumable(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: arnoDN}, ad.Entry{DN: "cn=Ada,ou=users,dc=example,dc=com"},
	)
	sync := ad.Job{
		URL: mockTLSURL, Operation: "sync", BaseDN: baseDN, MaxEntries: 1,
		ResultVariable: "changes", CookieVariable: "cookie",
	}
	out := run(t, d, sync)
	if n := len(syncEntries(t, out)); n != 1 {
		t.Fatalf("first pass returned %d entries, want the cap of 1", n)
	}
	if res, _ := out["changes"].(map[string]any); res["more"] != true {
		t.Errorf("more = %v, want the signal that a further pass is worth making", res["more"])
	}
	sync.Cookie, _ = out["cookie"].(string)
	out = run(t, d, sync)
	if n := len(syncEntries(t, out)); n != 1 {
		t.Fatalf("second pass returned %d entries, want the remaining one", n)
	}
	if res, _ := out["changes"].(map[string]any); res["more"] != false {
		t.Errorf("more = %v, want it cleared once the directory is caught up", res["more"])
	}
}

// A deletion is reported as a change carrying isDeleted, because AD reports it that
// way and it is the only signal a leaver process has.
func TestMockSyncReportsADeletion(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	sync := ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		ResultVariable: "changes", CookieVariable: "cookie"}
	out := run(t, d, sync)
	sync.Cookie, _ = out["cookie"].(string)

	run(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: arnoDN})
	got := syncEntries(t, run(t, d, sync))
	if len(got) != 1 {
		t.Fatalf("delta = %v, want the deletion", got)
	}
	e := got[0].(map[string]any)
	attrs := e["attributes"].(map[string]any)
	vals, _ := attrs["isDeleted"].([]string)
	if len(vals) != 1 || vals[0] != "TRUE" {
		t.Errorf("attributes = %v, want isDeleted=TRUE", attrs)
	}
}

// A cookie no pass of this directory wrote fails the job rather than silently
// starting over — the same refusal the real worker gives a corrupted one, and for
// the same reason: a full directory presented as a change set is worse than an error.
func TestMockSyncRefusesAForeignCookie(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		ResultVariable: "changes", CookieVariable: "cookie",
		Cookie: base64.StdEncoding.EncodeToString([]byte("from-another-directory")),
	})
	if !strings.Contains(err.Error(), "cookie") {
		t.Errorf("error = %v, want it to name the cookie", err)
	}
}

// DirSync is answered only at a naming context root, so a base that is an OU fails
// here as it does against a domain controller — and says why.
func TestMockSyncNeedsANamingContextRoot(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: usersDN,
		ResultVariable: "changes", CookieVariable: "cookie"})
	if !strings.Contains(err.Error(), "naming context") {
		t.Errorf("error = %v, want it to name what a DirSync base must be", err)
	}
}

// The filter selects, so a GALSync-shaped model that asks only for contacts is not
// handed the whole domain.
func TestMockSyncAppliesTheFilter(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-contact", DN: "cn=Extern,ou=users,dc=example,dc=com",
		Attributes: map[string][]string{"mail": {"e@partner.example"}}})

	sync := ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		Filter: "(objectClass=contact)", ResultVariable: "changes", CookieVariable: "cookie"}
	got := syncEntries(t, run(t, d, sync))
	if len(got) != 1 {
		t.Fatalf("delta = %v, want only the contact", got)
	}

	sync.Filter = "(&(objectClass=user)(sAMAccountName=arno))"
	if got := syncEntries(t, run(t, d, sync)); len(got) != 1 {
		t.Errorf("delta = %v, want the one user the conjunction names", got)
	}
	sync.Filter = "(!(objectClass=contact))"
	if got := syncEntries(t, run(t, d, sync)); len(got) != 1 {
		t.Errorf("delta = %v, want everything but the contact", got)
	}
	sync.Filter = "(mail=*)"
	if got := syncEntries(t, run(t, d, sync)); len(got) != 1 {
		t.Errorf("delta = %v, want the entry that has a mail at all", got)
	}
	sync.Filter = "(sAMAccountName=ar*)"
	if got := syncEntries(t, run(t, d, sync)); len(got) != 1 {
		t.Errorf("delta = %v, want the prefix match", got)
	}
}

// A filter the mock cannot apply is refused rather than ignored: a filter silently
// dropped would hand the process more than the real directory ever would.
func TestMockSyncRefusesAFilterItCannotApply(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	for _, f := range []string{"(whenChanged>=20260101000000.0Z)", "cn=Arno", "(&(cn=a)"} {
		err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
			Filter: f, ResultVariable: "changes", CookieVariable: "cookie"})
		if !strings.Contains(err.Error(), "filter") {
			t.Errorf("%s: error = %v, want it to name the filter", f, err)
		}
	}
}

// What the mock was asked is readable afterwards — that is the whole point of a
// mockup run — and an observer sees each operation as it happens, which is how the
// worker puts them in its log.
func TestMockRecordsWhatItWasAsked(t *testing.T) {
	d := ad.NewMockDirectory()
	var mu sync.Mutex
	var seen []ad.MockOperation
	d.Observe(func(op ad.MockOperation) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, op)
	})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN,
		Attributes: map[string][]string{"sAMAccountName": {"arno"}}})

	ops := d.Operations()
	if len(ops) < 2 || ops[0].Op != "bind" {
		t.Fatalf("operations = %v, want the bind and the add", ops)
	}
	last := ops[len(ops)-1]
	if last.Op != "add" || !strings.EqualFold(last.DN, arnoDN) {
		t.Errorf("last operation = %+v, want the add of %s", last, arnoDN)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != len(ops) {
		t.Errorf("the observer saw %d operations, the journal holds %d", len(seen), len(ops))
	}
}

// The journal is bounded: a mock worker left running for a week must not grow into
// the memory of the process it is meant to be a stand-in for.
func TestMockJournalIsBounded(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	for i := 0; i < 400; i++ {
		run(t, d, ad.Job{URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN,
			Attributes: map[string][]string{"title": {"Chef"}}})
	}
	ops := d.Operations()
	if len(ops) > 256 {
		t.Errorf("journal holds %d operations, want it bounded", len(ops))
	}
	if ops[len(ops)-1].Op != "modify" {
		t.Errorf("last operation = %+v, want the newest kept", ops[len(ops)-1])
	}
}

// One directory serves every job a worker leases, and a worker runs them
// concurrently — so the mock holds its own lock rather than assuming one caller.
func TestMockIsSafeForConcurrentJobs(t *testing.T) {
	d := ad.NewMockDirectory()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dn := "cn=user" + string(rune('a'+i)) + ",ou=users,dc=example,dc=com"
			_, _ = ad.Run(context.Background(), ad.Job{
				URL: mockTLSURL, Operation: "create-user", DN: dn,
				Attributes: map[string][]string{"sAMAccountName": {"u"}},
			}, d, nil, nil)
		}(i)
	}
	wg.Wait()
	if len(d.Entries()) != 8 {
		t.Errorf("entries = %v, want all eight creates", dns(d))
	}
}

// A DN differing only in case or in the spacing around its commas is the same entry,
// because it is the same entry in a directory.
func TestMockDNsAreDirectoryDNs(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "update-attributes",
		DN:         "CN=arno, OU=users, DC=example, DC=com",
		Attributes: map[string][]string{"title": {"Chef"}}})
	if got := entry(t, d, arnoDN).Attributes["title"]; len(got) != 1 || got[0] != "Chef" {
		t.Errorf("title = %v, want the update to have found the same entry", got)
	}
}

// syncEntries digs the entry list out of what a sync job completed with.
func syncEntries(t *testing.T, out map[string]any) []any {
	t.Helper()
	res, ok := out["changes"].(map[string]any)
	if !ok {
		t.Fatalf("result variable = %#v, want the sync result", out["changes"])
	}
	entries, ok := res["entries"].([]any)
	if !ok {
		t.Fatalf("entries = %#v, want a list", res["entries"])
	}
	return entries
}

// The search a joiner process does before it puts anybody in a group: find the group,
// take its DN, add the member. Against the mock the whole sequence runs with no domain
// controller anywhere near it.
func TestMockSearchFindsTheGroupAMembershipChangeNeeds(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: "cn=Vertrieb,ou=groups,dc=example,dc=com", Attributes: map[string][]string{
			"objectClass": {"top", "group"}, "cn": {"Vertrieb"},
		}},
		ad.Entry{DN: "cn=Einkauf,ou=groups,dc=example,dc=com", Attributes: map[string][]string{
			"objectClass": {"top", "group"}, "cn": {"Einkauf"},
		}},
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"objectClass": {"top", "user"}}},
	)
	out := run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Scope: "sub",
		Filter: "(&(objectClass=group)(cn=Vertrieb))", ResultVariable: "gruppe",
	})
	res, ok := out["gruppe"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want the search's own object", out)
	}
	if res["found"] != true || res["count"] != 1 {
		t.Errorf("found/count = %v / %v, want the one group", res["found"], res["count"])
	}
	if res["dn"] != "cn=Vertrieb,ou=groups,dc=example,dc=com" {
		t.Errorf("dn = %v, want the group's own distinguished name", res["dn"])
	}
	// And the DN it handed back is the one the next task can act on.
	run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "add-group-member",
		DN: res["dn"].(string), MemberDN: arnoDN,
	})
	if got := entry(t, d, "cn=Vertrieb,ou=groups,dc=example,dc=com").Attributes["member"]; len(got) != 1 || got[0] != arnoDN {
		t.Errorf("member = %v, want the account the search's DN let us add", got)
	}
}

// A group that is not there is an empty answer, not a failure — that is the question
// the operation exists to answer.
func TestMockSearchFindingNothingIsNotAFailure(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}})
	out := run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN,
		Filter: "(cn=Gibtsnicht)", ResultVariable: "treffer",
	})
	res := out["treffer"].(map[string]any)
	if res["found"] != false || res["count"] != 0 || res["dn"] != "" {
		t.Errorf("result = %v, want an empty answer", res)
	}
}

// Scope is applied, not ignored: a mock that answered "one" with the whole subtree
// would hand a process entries the real directory would not.
func TestMockSearchAppliesTheScope(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: usersDN, Attributes: map[string][]string{"ou": {"users"}}},
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}},
		ad.Entry{DN: "cn=Clara," + usersDN, Attributes: map[string][]string{"cn": {"Clara"}}},
		ad.Entry{DN: "cn=Berta,ou=extern," + usersDN, Attributes: map[string][]string{"cn": {"Berta"}}},
	)
	counts := map[string]int{}
	for _, scope := range []string{"base", "one", "sub"} {
		out := run(t, d, ad.Job{
			URL: mockTLSURL, Operation: "search", BaseDN: usersDN, Scope: scope,
			ResultVariable: "r",
		})
		counts[scope] = out["r"].(map[string]any)["count"].(int)
	}
	// base is the OU itself; one is its two immediate children and *not* the OU, the
	// way LDAP's single-level scope reads; sub is the OU and everything below it.
	if counts["base"] != 1 || counts["one"] != 2 || counts["sub"] != 4 {
		t.Errorf("base/one/sub = %d / %d / %d, want 1 / 2 / 4", counts["base"], counts["one"], counts["sub"])
	}
}

// A deleted entry is gone from a search even though a delta still reports it: AD
// answers a search from the live directory, and an existence check that found a
// tombstone would say an account is there that nobody can use.
func TestMockSearchDoesNotFindATombstone(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "delete", DN: arnoDN})
	out := run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Filter: "(cn=Arno)",
		ResultVariable: "r",
	})
	if res := out["r"].(map[string]any); res["found"] != false {
		t.Errorf("result = %v, want the deleted account not to be found", res)
	}
}

// Exceeding the cap fails rather than truncating: a short result set is a wrong
// answer, and a process branching on the count would branch on it confidently.
func TestMockSearchRefusesToTruncate(t *testing.T) {
	d := ad.NewMockDirectory(
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"objectClass": {"user"}}},
		ad.Entry{DN: "cn=Berta," + usersDN, Attributes: map[string][]string{"objectClass": {"user"}}},
	)
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Filter: "(objectClass=user)",
		MaxEntries: 1, ResultVariable: "r",
	})
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error = %v, want it to name the cap it exceeded", err)
	}
}

// A filter the mock cannot apply is refused rather than dropped, for the same reason
// a dropped filter is refused everywhere else here: silently answering a wider
// question hands a process more than the real directory would.
func TestMockSearchRefusesAFilterItCannotApply(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN,
		Filter: "(badPwdCount>=3)", ResultVariable: "r",
	})
	if !strings.Contains(err.Error(), "filter") {
		t.Errorf("error = %v, want it to name the filter it could not apply", err)
	}
}

// The journal carries the search, so a mockup run shows what was asked as well as
// what was written.
func TestMockSearchIsJournaled(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{"cn": {"Arno"}}})
	run(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Filter: "(cn=Arno)",
		ResultVariable: "r",
	})
	var found bool
	for _, op := range d.Operations() {
		if op.Op == "search" && op.DN == baseDN && strings.Contains(op.Detail, "(cn=Arno)") {
			found = true
		}
	}
	if !found {
		t.Errorf("no search in the journal: %+v", d.Operations())
	}
}

// An empty base is refused before it reaches the directory: it is what a FEEL baseDN
// over an unset variable resolves to, and a server would answer it either with a
// refusal or with the whole tree.
func TestMockSearchRefusesAnEmptyBase(t *testing.T) {
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN})
	err := runErr(t, d, ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: "", ResultVariable: "r",
	})
	if !strings.Contains(err.Error(), "baseDN") {
		t.Errorf("error = %v, want it to name the missing baseDN", err)
	}
}
