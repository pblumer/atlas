package ad_test

import (
	"context"
	"path/filepath"
	"testing"
	"unicode/utf16"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// fakeConn records the operation the worker performed and returns canned results.
type fakeConn struct {
	err   error    // returned by every operation
	uac   []string // returned by ReadAttr (the current userAccountControl)
	addDN string
	addAt map[string][]string
	modDN string
	mods  []ad.Mod
	// mdnDN/mdnRDN/mdnSuperior record a ModifyDN; delDN records a Delete.
	mdnDN, mdnRDN, mdnSuperior string
	delDN                      string
	closed                     bool
}

func (c *fakeConn) Add(dn string, attrs map[string][]string) error {
	c.addDN, c.addAt = dn, attrs
	return c.err
}
func (c *fakeConn) Modify(dn string, mods []ad.Mod) error {
	c.modDN, c.mods = dn, mods
	return c.err
}
func (c *fakeConn) ReadAttr(dn, attr string) ([]string, error) { return c.uac, c.err }
func (c *fakeConn) ModifyDN(dn, newRDN, newSuperior string) error {
	c.mdnDN, c.mdnRDN, c.mdnSuperior = dn, newRDN, newSuperior
	return c.err
}
func (c *fakeConn) Delete(dn string) error { c.delDN = dn; return c.err }
func (c *fakeConn) Close() error           { c.closed = true; return nil }

type fakeDialer struct {
	conn                      *fakeConn
	err                       error
	dials                     int
	url, bindDN, bindPassword string
	startTLS                  bool
}

func (d *fakeDialer) Dial(url, bindDN, bindPassword string, startTLS bool) (ad.Conn, error) {
	d.dials++
	d.url, d.bindDN, d.bindPassword, d.startTLS = url, bindDN, bindPassword, startTLS
	if d.err != nil {
		return nil, d.err
	}
	return d.conn, nil
}

func noSecret(string) string { return "" }

const adDefKey = 97
const adURL = "ldaps://dc.example.com:636"

func lit(s string) compiler.RestExpr { return compiler.RestExpr{Literal: s} }

func adProcess(t *testing.T, cfg compiler.AdConfig, park bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	b := compiler.NewBuilder(adDefKey, "directory", 1)
	start := b.AddStartEvent()
	call := b.AddAdConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, call)
	if park {
		wait := b.AddServiceTask("wait", 3)
		b.Connect(call, wait)
		b.Connect(wait, end)
	} else {
		b.Connect(call, end)
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

func openStore(t *testing.T) (*wal.Log, *state.Store) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })
	return log, store
}

func mustActiveProcs(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	return n
}

func active(t *testing.T, s *state.Store) (pi, ei int) {
	t.Helper()
	var err error
	if pi, err = s.ActiveProcessInstanceCount(); err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	if ei, err = s.ActiveElementInstanceCount(); err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, dialer ad.Dialer, secret ad.SecretResolver, store *state.Store, log *wal.Log, vars ...model.VariableValue) {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return ad.Handler(rd, func(uint64) *compiler.CompiledProcess { return cp }, dialer, secret)
	})
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
}

// TestAdCreateUser is the vertical slice: create-user binds with the resolved password
// and adds the entry with the attribute object from the named variable.
func TestAdCreateUser(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), BindDN: lit("cn=svc,dc=x"), BindSecret: "AD_BIND",
		Op: "create-user", DN: lit("cn=Arno,ou=users,dc=x"), EntryVar: "entry",
	}, false)
	if jobType != compiler.AdJobTypeIndex {
		t.Fatalf("AD job type index = %d, want the reserved %d", jobType, compiler.AdJobTypeIndex)
	}
	conn := &fakeConn{}
	dialer := &fakeDialer{conn: conn}
	secret := func(ref string) string {
		if ref == "AD_BIND" {
			return "s3cr3t"
		}
		return ""
	}
	drive(t, cp, jobType, dialer, secret, store, log,
		model.VariableValue{Name: "entry", Kind: model.VarJSON, Text: `{"sAMAccountName":"arno","objectClass":["top","person","user"]}`})

	if dialer.url != adURL || dialer.bindDN != "cn=svc,dc=x" || dialer.bindPassword != "s3cr3t" {
		t.Errorf("dial = %+v, want the model url/bindDN with the resolved password", dialer)
	}
	if conn.addDN != "cn=Arno,ou=users,dc=x" || conn.addAt["sAMAccountName"][0] != "arno" {
		t.Errorf("add = %q %v, want the user entry", conn.addDN, conn.addAt)
	}
	if len(conn.addAt["objectClass"]) != 3 {
		t.Errorf("objectClass = %v, want 3 values", conn.addAt["objectClass"])
	}
	if !conn.closed {
		t.Error("connection not closed")
	}
}

// TestAdSetPassword proves set-password replaces unicodePwd with the UTF-16LE
// quote-wrapped encoding of the new password.
func TestAdSetPassword(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "set-password", DN: lit("cn=Arno,dc=x"), NewPassword: lit("N3w!pass"),
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)

	if conn.modDN != "cn=Arno,dc=x" || len(conn.mods) != 1 {
		t.Fatalf("modify = %q %v, want one change on the user", conn.modDN, conn.mods)
	}
	m := conn.mods[0]
	if m.Op != uint(goldap.ReplaceAttribute) || m.Attr != "unicodePwd" || len(m.Vals) != 1 {
		t.Fatalf("change = %+v, want a replace of unicodePwd", m)
	}
	if got := decodeUTF16LE(m.Vals[0]); got != `"N3w!pass"` {
		t.Errorf("unicodePwd decodes to %q, want the quote-wrapped password", got)
	}
}

// TestAdEnableDisable proves enable/disable read userAccountControl and write back the
// flipped ACCOUNTDISABLE bit, preserving other flags.
func TestAdEnableDisable(t *testing.T) {
	t.Run("enable clears the bit", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "enable", DN: lit("cn=Arno,dc=x")}, false)
		conn := &fakeConn{uac: []string{"66050"}} // NORMAL_ACCOUNT|DONT_EXPIRE|DISABLE
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.mods[0].Attr != "userAccountControl" || conn.mods[0].Vals[0] != "66048" {
			t.Errorf("uac = %v, want 66048 (disable bit cleared, others kept)", conn.mods[0].Vals)
		}
	})
	t.Run("disable sets the bit with a default baseline", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=Arno,dc=x")}, false)
		conn := &fakeConn{uac: nil} // no current value → normal-account baseline 512
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.mods[0].Vals[0] != "514" {
			t.Errorf("uac = %v, want 514 (512 + ACCOUNTDISABLE)", conn.mods[0].Vals)
		}
	})
}

// TestAdGroupMembership proves add/remove-group-member incrementally add or delete the
// member value on the group DN.
func TestAdGroupMembership(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "add-group-member", DN: lit("cn=Admins,dc=x"), MemberDN: lit("cn=Arno,dc=x")}, false)
		conn := &fakeConn{}
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.modDN != "cn=Admins,dc=x" || conn.mods[0].Op != uint(goldap.AddAttribute) || conn.mods[0].Vals[0] != "cn=Arno,dc=x" {
			t.Errorf("modify = %q %+v, want add member on the group", conn.modDN, conn.mods)
		}
	})
	t.Run("remove", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "remove-group-member", DN: lit("cn=Admins,dc=x"), MemberDN: lit("cn=Arno,dc=x")}, false)
		conn := &fakeConn{}
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.mods[0].Op != uint(goldap.DeleteAttribute) {
			t.Errorf("op = %d, want delete", conn.mods[0].Op)
		}
	})
}

// TestAdFeelFields evaluates FEEL url/dn/memberDN over the instance's variables.
func TestAdFeelFields(t *testing.T) {
	log, store := openStore(t)
	feel := func(src string) compiler.RestExpr {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return compiler.RestExpr{Expr: e}
	}
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL:      feel(`"ldaps://" + host`),
		Op:       "add-group-member",
		DN:       feel(`"cn=" + group + ",ou=groups,dc=x"`),
		MemberDN: feel(`userDN`),
	}, false)
	conn := &fakeConn{}
	dialer := &fakeDialer{conn: conn}
	drive(t, cp, jobType, dialer, noSecret, store, log,
		model.VariableValue{Name: "host", Kind: model.VarString, Text: "dc.example.com"},
		model.VariableValue{Name: "group", Kind: model.VarString, Text: "Admins"},
		model.VariableValue{Name: "userDN", Kind: model.VarString, Text: "cn=Arno,dc=x"},
	)
	if dialer.url != "ldaps://dc.example.com" || conn.modDN != "cn=Admins,ou=groups,dc=x" || conn.mods[0].Vals[0] != "cn=Arno,dc=x" {
		t.Errorf("resolved url=%q dn=%q member=%v, want the FEEL-computed values", dialer.url, conn.modDN, conn.mods[0].Vals)
	}
}

// TestAdBindSecretMissing proves a bind DN whose secret is unresolvable fails the job
// before any dial.
func TestAdBindSecretMissing(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), BindDN: lit("cn=svc"), BindSecret: "MISSING", Op: "disable", DN: lit("cn=x")}, false)
	dialer := &fakeDialer{conn: &fakeConn{}}
	drive(t, cp, jobType, dialer, noSecret, store, log)
	if dialer.dials != 0 {
		t.Errorf("dials = %d, want 0 (no dial when bind secret missing)", dialer.dials)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdDialError proves a dial/bind failure fails the job.
func TestAdDialError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=x")}, false)
	drive(t, cp, jobType, &fakeDialer{err: context.DeadlineExceeded}, noSecret, store, log)
	if mustActiveProcs(t, store) != 1 {
		t.Fatal("after dial error: want the job never completed")
	}
}

// TestAdOperationError proves an operation error fails the job and still closes the
// connection.
func TestAdOperationError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=x")}, false)
	conn := &fakeConn{err: context.DeadlineExceeded}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if !conn.closed {
		t.Error("connection not closed after an operation error")
	}
	if mustActiveProcs(t, store) != 1 {
		t.Fatal("after op error: want the job never completed")
	}
}

// TestAdCreateUserMissingEntryVar fails the job when the entry variable is absent.
func TestAdCreateUserMissingEntryVar(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "create-user", DN: lit("cn=x"), EntryVar: "entry"}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log) // no "entry" var
	if conn.addDN != "" {
		t.Errorf("add ran (%q) despite a missing entry variable", conn.addDN)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdEmptyURL proves a url that resolves to empty (a FEEL reference to an absent
// variable) fails the job before any dial.
func TestAdEmptyURL(t *testing.T) {
	log, store := openStore(t)
	feel, err := expr.CompileAuto(`missingHost`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cp, jobType := adProcess(t, compiler.AdConfig{URL: compiler.RestExpr{Expr: feel}, Op: "disable", DN: lit("cn=x")}, false)
	dialer := &fakeDialer{conn: &fakeConn{}}
	drive(t, cp, jobType, dialer, noSecret, store, log)
	if dialer.dials != 0 {
		t.Errorf("dials = %d, want 0 (empty url refused)", dialer.dials)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdCreateUserEntryNotObject proves a non-object entry variable fails the job.
func TestAdCreateUserEntryNotObject(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "create-user", DN: lit("cn=x"), EntryVar: "entry"}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		model.VariableValue{Name: "entry", Kind: model.VarString, Text: "not-an-object"})
	if conn.addDN != "" {
		t.Errorf("add ran (%q) despite a non-object entry variable", conn.addDN)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdHandlerElementInstanceGone covers the vanished-element guard.
func TestAdHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := ad.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &fakeDialer{conn: &fakeConn{}}, noSecret)
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

// TestAdRecoversAcrossRestart runs to the waiting AD job, simulates a crash, and
// recovers, proving the connector job survives replay.
func TestAdRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=x")}, false)
	clock := &fixedClock{}
	lookup := func(uint64) *compiler.CompiledProcess { return cp }

	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p1 := engine.New(1, log1, store1, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if mustActiveProcs(t, store1) != 1 {
		t.Fatal("before crash: want the job waiting")
	}
	store1.Close()
	log1.Close()

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	t.Cleanup(func() { store2.Close(); log2.Close() })
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	conn := &fakeConn{uac: []string{"512"}}
	runner := job.NewRunner(store2, p2)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return ad.Handler(rd, lookup, &fakeDialer{conn: conn}, noSecret)
	})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if conn.modDN != "cn=x" {
		t.Fatalf("after recovery modify DN = %q, want cn=x", conn.modDN)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// decodeUTF16LE decodes UTF-16LE bytes (as carried in a Go string) back to text.
func decodeUTF16LE(s string) string {
	b := []byte(s)
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}

// TestAdUpdateAttributes proves an update replaces exactly the attributes the model
// named, and leaves the rest of the entry alone.
func TestAdUpdateAttributes(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "update-attributes", DN: lit("cn=Arno,ou=users,dc=x"), EntryVar: "aenderungen",
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		model.VariableValue{Name: "aenderungen", Kind: model.VarJSON,
			Text: `{"department":"IT","title":"Ingenieur"}`})

	if conn.modDN != "cn=Arno,ou=users,dc=x" {
		t.Errorf("modify dn = %q", conn.modDN)
	}
	if len(conn.mods) != 2 {
		t.Fatalf("mods = %+v, want one per named attribute", conn.mods)
	}
	// Sorted, so a replayed job produces an identical request rather than one that
	// merely means the same thing.
	if conn.mods[0].Attr != "department" || conn.mods[1].Attr != "title" {
		t.Errorf("mods = %+v, want them in a stable order", conn.mods)
	}
	for _, m := range conn.mods {
		if m.Op != uint(goldap.ReplaceAttribute) {
			t.Errorf("mod %q op = %v, want replace", m.Attr, m.Op)
		}
	}
}

// An update naming nothing is a modelling mistake, not a no-op request to AD.
func TestAdUpdateAttributesEmpty(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "update-attributes", DN: lit("cn=Arno,dc=x"), EntryVar: "leer",
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		model.VariableValue{Name: "leer", Kind: model.VarJSON, Text: `{}`})
	if len(conn.mods) != 0 {
		t.Errorf("mods = %+v, want no request sent", conn.mods)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdMove proves a mover: the model authors one target DN — how a person thinks
// about it — and the connector splits it into the relative name and the new parent
// that LDAP's ModifyDN wants.
func TestAdMove(t *testing.T) {
	for _, tc := range []struct{ name, newDN, wantRDN, wantSuperior string }{
		{"move and rename", "cn=Arno Meier,ou=extern,dc=x", "cn=Arno Meier", "ou=extern,dc=x"},
		{"rename only", "cn=A. Meier,ou=users,dc=x", "cn=A. Meier", "ou=users,dc=x"},
		// A comma inside a value must not split the name in the wrong place.
		{"escaped comma", `cn=Meier\, Arno,ou=users,dc=x`, `cn=Meier\, Arno`, "ou=users,dc=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, store := openStore(t)
			cp, jobType := adProcess(t, compiler.AdConfig{
				URL: lit(adURL), Op: "move", DN: lit("cn=Arno,ou=users,dc=x"), NewDN: lit(tc.newDN),
			}, false)
			conn := &fakeConn{}
			drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
			if conn.mdnDN != "cn=Arno,ou=users,dc=x" {
				t.Errorf("modifydn dn = %q", conn.mdnDN)
			}
			if conn.mdnRDN != tc.wantRDN || conn.mdnSuperior != tc.wantSuperior {
				t.Errorf("rdn/superior = %q / %q, want %q / %q",
					conn.mdnRDN, conn.mdnSuperior, tc.wantRDN, tc.wantSuperior)
			}
		})
	}
}

func TestAdDelete(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "delete", DN: lit("cn=Alt,ou=users,dc=x"),
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if conn.delDN != "cn=Alt,ou=users,dc=x" {
		t.Errorf("delete dn = %q", conn.delDN)
	}
}

// A create supplies the objectClass chain the operation implies when the model does
// not, because AD rejects an add without one and forgetting it is not a business
// decision worth making every author repeat.
func TestAdCreateSuppliesObjectClass(t *testing.T) {
	for _, tc := range []struct {
		op, entry string
		want      []string
	}{
		{"create-user", `{"sAMAccountName":"arno"}`, []string{"top", "person", "organizationalPerson", "user"}},
		{"create-group", `{"sAMAccountName":"team"}`, []string{"top", "group"}},
		// An authored objectClass always wins.
		{"create-group", `{"objectClass":["top","group","posixGroup"]}`, []string{"top", "group", "posixGroup"}},
	} {
		t.Run(tc.op+"/"+tc.entry, func(t *testing.T) {
			log, store := openStore(t)
			cp, jobType := adProcess(t, compiler.AdConfig{
				URL: lit(adURL), Op: tc.op, DN: lit("cn=X,dc=x"), EntryVar: "entry",
			}, false)
			conn := &fakeConn{}
			drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
				model.VariableValue{Name: "entry", Kind: model.VarJSON, Text: tc.entry})
			got := conn.addAt["objectClass"]
			if len(got) != len(tc.want) {
				t.Fatalf("objectClass = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("objectClass = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}

// A move whose newDN resolves to nothing fails the job rather than issuing a
// ModifyDN with an empty name.
func TestAdMoveEmptyNewDN(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "move", DN: lit("cn=Arno,dc=x"), NewDN: lit(""),
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if conn.mdnDN != "" {
		t.Errorf("a ModifyDN was issued with no target: %q", conn.mdnDN)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}
