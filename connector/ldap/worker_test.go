package ldap_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ldap"
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
	searchResult []ldap.Entry
	err          error // returned by every operation

	searchReq   *ldap.SearchRequest
	addDN       string
	addAttrs    map[string][]string
	modifyDN    string
	modifyAttrs map[string][]string
	deleteDN    string
	pwDN, pwNew string
	closed      bool
}

func (c *fakeConn) Search(req ldap.SearchRequest) ([]ldap.Entry, error) {
	c.searchReq = &req
	return c.searchResult, c.err
}
func (c *fakeConn) Add(dn string, a map[string][]string) error {
	c.addDN, c.addAttrs = dn, a
	return c.err
}
func (c *fakeConn) Modify(dn string, a map[string][]string) error {
	c.modifyDN, c.modifyAttrs = dn, a
	return c.err
}
func (c *fakeConn) Delete(dn string) error          { c.deleteDN = dn; return c.err }
func (c *fakeConn) SetPassword(dn, np string) error { c.pwDN, c.pwNew = dn, np; return c.err }
func (c *fakeConn) Close() error                    { c.closed = true; return nil }

// fakeDialer captures the dial parameters and hands back its conn (or an error).
type fakeDialer struct {
	conn                      *fakeConn
	err                       error
	dials                     int
	url, bindDN, bindPassword string
	startTLS                  bool
}

func (d *fakeDialer) Dial(url, bindDN, bindPassword string, startTLS bool) (ldap.Conn, error) {
	d.dials++
	d.url, d.bindDN, d.bindPassword, d.startTLS = url, bindDN, bindPassword, startTLS
	if d.err != nil {
		return nil, d.err
	}
	return d.conn, nil
}

func noSecret(string) string { return "" }

const ldapDefKey = 91
const ldapURL = "ldap://dir.example.com:389"

func lit(s string) compiler.RestExpr { return compiler.RestExpr{Literal: s} }

// ldapProcess builds Start → LDAP connector task → (optional park) → End.
func ldapProcess(t *testing.T, cfg compiler.LdapConfig, park bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	b := compiler.NewBuilder(ldapDefKey, "directory", 1)
	start := b.AddStartEvent()
	call := b.AddLdapConnectorTask(cfg)
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

func soleInstanceKey(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("live instances = %d, want exactly 1", len(keys))
	}
	return keys[0]
}

func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var found *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			cp := *v
			found = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return found
}

// drive deploys cp, registers the LDAP worker with the given dialer/secret, creates
// one instance with vars, and drives to quiescence.
func drive(t *testing.T, cp *compiler.CompiledProcess, jobType int32, dialer ldap.Dialer, secret ldap.SecretResolver, store *state.Store, log *wal.Log, vars ...model.VariableValue) {
	t.Helper()
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return ldap.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, dialer, secret)
	})
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
}

// TestLdapSearchWritesEntries is the vertical slice: a search binds with the resolved
// password, runs the model's base/scope/filter, and writes the entries as JSON into
// the result variable. The process parks so the variable is readable.
func TestLdapSearchWritesEntries(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{
		URL:        lit(ldapURL),
		BindDN:     lit("cn=admin,dc=example,dc=com"),
		BindSecret: "LDAP_PW",
		Op:         "search",
		BaseDN:     lit("ou=people,dc=example,dc=com"),
		Filter:     lit("(uid=ada)"),
		Scope:      "sub",
		ResultVar:  "hits",
	}, true)
	if jobType != compiler.LdapJobTypeIndex {
		t.Fatalf("LDAP job type index = %d, want the reserved %d", jobType, compiler.LdapJobTypeIndex)
	}
	conn := &fakeConn{searchResult: []ldap.Entry{{DN: "uid=ada,ou=people,dc=example,dc=com", Attributes: map[string][]string{"mail": {"ada@example.com"}, "objectClass": {"top", "person"}}}}}
	dialer := &fakeDialer{conn: conn}
	secret := func(ref string) string {
		if ref == "LDAP_PW" {
			return "s3cr3t"
		}
		return ""
	}
	drive(t, cp, jobType, dialer, secret, store, log)

	if dialer.dials != 1 || dialer.url != ldapURL || dialer.bindDN != "cn=admin,dc=example,dc=com" || dialer.bindPassword != "s3cr3t" {
		t.Errorf("dial = %+v, want the model url/bindDN with the resolved password", dialer)
	}
	if conn.searchReq == nil || conn.searchReq.BaseDN != "ou=people,dc=example,dc=com" || conn.searchReq.Scope != "sub" || conn.searchReq.Filter != "(uid=ada)" {
		t.Errorf("search request = %+v, want the model base/scope/filter", conn.searchReq)
	}
	if !conn.closed {
		t.Error("connection was not closed")
	}
	scope := soleInstanceKey(t, store)
	hits := readVar(t, store, scope, "hits")
	if hits == nil || hits.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want a structured VarJSON array", hits)
	}
}

// TestLdapAddUsesEntryVar proves an add sends the DN and the attribute object from the
// named entry variable, coercing scalars and arrays into multi-valued attributes.
func TestLdapAddUsesEntryVar(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{
		URL:      lit(ldapURL),
		BindDN:   lit("cn=admin"),
		Op:       "add",
		DN:       lit("uid=ada,ou=people,dc=example,dc=com"),
		EntryVar: "entry",
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		model.VariableValue{Name: "entry", Kind: model.VarJSON, Text: `{"uid":"ada","objectClass":["top","person"],"employeeNumber":42}`})

	if conn.addDN != "uid=ada,ou=people,dc=example,dc=com" {
		t.Errorf("add DN = %q, want the model DN", conn.addDN)
	}
	if got := conn.addAttrs["uid"]; len(got) != 1 || got[0] != "ada" {
		t.Errorf("uid = %v, want [ada]", got)
	}
	if got := conn.addAttrs["objectClass"]; len(got) != 2 || got[0] != "top" || got[1] != "person" {
		t.Errorf("objectClass = %v, want [top person]", got)
	}
	if got := conn.addAttrs["employeeNumber"]; len(got) != 1 || got[0] != "42" {
		t.Errorf("employeeNumber = %v, want [42] (number coerced)", got)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after add: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestLdapModifyDeletePassword covers the remaining mutating operations.
func TestLdapModifyDeletePassword(t *testing.T) {
	t.Run("modify", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "modify", DN: lit("uid=ada"), EntryVar: "entry"}, false)
		conn := &fakeConn{}
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
			model.VariableValue{Name: "entry", Kind: model.VarJSON, Text: `{"mail":"new@example.com"}`})
		if conn.modifyDN != "uid=ada" || len(conn.modifyAttrs["mail"]) != 1 || conn.modifyAttrs["mail"][0] != "new@example.com" {
			t.Errorf("modify = %q %v, want the replaced mail", conn.modifyDN, conn.modifyAttrs)
		}
	})
	t.Run("delete", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "delete", DN: lit("uid=bob")}, false)
		conn := &fakeConn{}
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.deleteDN != "uid=bob" {
			t.Errorf("delete DN = %q, want uid=bob", conn.deleteDN)
		}
	})
	t.Run("modify-password", func(t *testing.T) {
		log, store := openStore(t)
		cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "modify-password", DN: lit("uid=ada"), NewPassword: lit("N3wPass!")}, false)
		conn := &fakeConn{}
		drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
		if conn.pwDN != "uid=ada" || conn.pwNew != "N3wPass!" {
			t.Errorf("password set = %q/%q, want uid=ada with the new password", conn.pwDN, conn.pwNew)
		}
	})
}

// TestLdapAnonymousBind proves an empty bind DN dials without a password and needs no
// secret.
func TestLdapAnonymousBind(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "search", BaseDN: lit("dc=example,dc=com"), Scope: "sub"}, false)
	dialer := &fakeDialer{conn: &fakeConn{}}
	drive(t, cp, jobType, dialer, noSecret, store, log)
	if dialer.bindDN != "" || dialer.bindPassword != "" {
		t.Errorf("dial bind = %q/%q, want an anonymous bind", dialer.bindDN, dialer.bindPassword)
	}
	if dialer.dials != 1 {
		t.Errorf("dials = %d, want 1", dialer.dials)
	}
}

// TestLdapBindSecretMissing proves a bind DN whose secret is unresolvable fails the
// job before any dial.
func TestLdapBindSecretMissing(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), BindDN: lit("cn=admin"), BindSecret: "MISSING", Op: "delete", DN: lit("uid=x")}, false)
	dialer := &fakeDialer{conn: &fakeConn{}}
	drive(t, cp, jobType, dialer, noSecret, store, log)
	if dialer.dials != 0 {
		t.Errorf("dials = %d, want 0 (no dial when the bind secret is missing)", dialer.dials)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestLdapDialError proves a dial/bind failure fails the job (incident).
func TestLdapDialError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "delete", DN: lit("uid=x")}, false)
	drive(t, cp, jobType, &fakeDialer{err: context.DeadlineExceeded}, noSecret, store, log)
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after dial error: active=%d, want 1 (job never completed)", pi)
	}
}

// TestLdapOperationError proves a directory operation error fails the job and still
// closes the connection.
func TestLdapOperationError(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "delete", DN: lit("uid=x")}, false)
	conn := &fakeConn{err: context.DeadlineExceeded}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if !conn.closed {
		t.Error("connection was not closed after an operation error")
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Fatalf("after op error: active=%d, want 1", pi)
	}
}

// TestLdapFeelFields exercises FEEL url/dn/baseDN/filter evaluated over the instance's
// variables of every kind (string/number/bool/json), and a search result written back.
func TestLdapFeelFields(t *testing.T) {
	log, store := openStore(t)
	feel := func(src string) compiler.RestExpr {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		return compiler.RestExpr{Expr: e}
	}
	cp, jobType := ldapProcess(t, compiler.LdapConfig{
		URL:    feel(`"ldap://" + host`),
		Op:     "search",
		BaseDN: feel(`"ou=" + unit + ",dc=example,dc=com"`),
		// A filter mixing string + number + bool from variables.
		Filter:    feel(`"(&(employeeNumber=" + string(empno) + ")(active=" + string(active) + "))"`),
		Scope:     "one",
		ResultVar: "hits",
	}, true)
	conn := &fakeConn{searchResult: []ldap.Entry{{DN: "uid=ada", Attributes: map[string][]string{"cn": {"Ada"}}}}}
	dialer := &fakeDialer{conn: conn}
	drive(t, cp, jobType, dialer, noSecret, store, log,
		model.VariableValue{Name: "host", Kind: model.VarString, Text: "dir.example.com"},
		model.VariableValue{Name: "unit", Kind: model.VarString, Text: "people"},
		model.VariableValue{Name: "empno", Kind: model.VarNumber, Text: "42"},
		model.VariableValue{Name: "active", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "extra", Kind: model.VarJSON, Text: `{"a":1}`},
	)
	if dialer.url != "ldap://dir.example.com" {
		t.Errorf("url = %q, want the FEEL-built server URL", dialer.url)
	}
	if conn.searchReq == nil || conn.searchReq.BaseDN != "ou=people,dc=example,dc=com" || conn.searchReq.Scope != "one" {
		t.Errorf("search = %+v, want the FEEL base and scope one", conn.searchReq)
	}
	if conn.searchReq.Filter != "(&(employeeNumber=42)(active=true))" {
		t.Errorf("filter = %q, want the evaluated filter", conn.searchReq.Filter)
	}
	scope := soleInstanceKey(t, store)
	if hits := readVar(t, store, scope, "hits"); hits == nil || hits.Kind != model.VarJSON {
		t.Fatalf("hits = %+v, want a VarJSON array", hits)
	}
}

// TestLdapAddCoercesScalars covers the scalar-coercion arms of the attribute map: a
// bool, a null, and a nested object (rendered via fmt).
func TestLdapAddCoercesScalars(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "add", DN: lit("uid=z"), EntryVar: "entry"}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		model.VariableValue{Name: "entry", Kind: model.VarJSON, Text: `{"enabled":true,"note":null,"meta":{"k":"v"}}`})
	if got := conn.addAttrs["enabled"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("enabled = %v, want [true]", got)
	}
	if got := conn.addAttrs["note"]; len(got) != 1 || got[0] != "" {
		t.Errorf("note(null) = %v, want [\"\"]", got)
	}
	if got := conn.addAttrs["meta"]; len(got) != 1 || got[0] == "" {
		t.Errorf("meta(object) = %v, want a single stringified value", got)
	}
}

// TestLdapAddMissingEntryVar proves a missing entry variable fails the job before any
// dial-side write reaches the directory (the connection is still opened then closed).
func TestLdapAddMissingEntryVar(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "add", DN: lit("uid=z"), EntryVar: "entry"}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log) // no "entry" var set
	if conn.addDN != "" {
		t.Errorf("add was performed (%q) despite a missing entry variable", conn.addDN)
	}
	if pi := mustActiveProcs(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (job parked on incident)", pi)
	}
}

// TestLdapHandlerElementInstanceGone covers the vanished-element guard.
func TestLdapHandlerElementInstanceGone(t *testing.T) {
	_, store := openStore(t)
	h := ldap.Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &fakeDialer{conn: &fakeConn{}}, noSecret)
	out, err := h(job.Job{ElementInstanceKey: 424242})
	if err != nil || out != nil {
		t.Fatalf("handler for a vanished element instance: out=%v err=%v, want nil,nil", out, err)
	}
}

// TestLdapRecoversAcrossRestart runs to the waiting LDAP job, simulates a crash, and
// recovers, proving the connector job survives replay like any other job.
func TestLdapRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := ldapProcess(t, compiler.LdapConfig{URL: lit(ldapURL), Op: "delete", DN: lit("uid=x")}, false)
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
	if pi := mustActiveProcs(t, store1); pi != 1 {
		t.Fatalf("before crash: active=%d, want 1 (waiting on LDAP job)", pi)
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
	conn := &fakeConn{}
	runner := job.NewRunner(store2, p2)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler {
		return ldap.Handler(store2, lookup, &fakeDialer{conn: conn}, noSecret)
	})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if conn.deleteDN != "uid=x" {
		t.Fatalf("after recovery delete DN = %q, want uid=x", conn.deleteDN)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
