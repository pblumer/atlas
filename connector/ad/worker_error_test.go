package ad_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// fakeReader is a minimal state.Reader that lets a test drive the Handler's early
// guards directly — a store failure, a vanished process, a non-connector element, or a
// variable-read failure — without standing up the engine.
type fakeReader struct {
	ei      *model.ElementInstanceValue
	eiOK    bool
	getErr  error
	varsErr error
}

func (r *fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return r.ei, r.eiOK, r.getErr
}

func (r *fakeReader) VariablesOfScope(uint64, func(*model.VariableValue) error) error {
	return r.varsErr
}

// adProcessIDs builds a one-connector process and returns it with the worker node's
// element id and the start event's, so a test can point a fake element instance at
// either a real task or a non-connector node.
func adProcessIDs(t *testing.T, cfg compiler.AdConfig) (cp *compiler.CompiledProcess, connID, startID int32) {
	t.Helper()
	if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	b := compiler.NewBuilder(adDefKey, "directory", 1)
	startID = b.AddStartEvent()
	connID = b.AddAdConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(startID, connID)
	b.Connect(connID, end)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, connID, startID
}

// TestAdHandlerStoreError proves a store failure reading the element instance surfaces
// as the job's error (retry, then an incident), not a silent no-op.
func TestAdHandlerStoreError(t *testing.T) {
	boom := errors.New("boom")
	h := ad.Handler(&fakeReader{getErr: boom}, func(uint64) *compiler.CompiledProcess { return nil }, &fakeDialer{conn: &fakeConn{}}, noSecret, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store error", err)
	}
}

// TestAdHandlerNoCompiledProcess proves a missing compiled process fails the job.
func TestAdHandlerNoCompiledProcess(t *testing.T) {
	r := &fakeReader{ei: &model.ElementInstanceValue{ProcessDefKey: 5}, eiOK: true}
	h := ad.Handler(r, func(uint64) *compiler.CompiledProcess { return nil }, &fakeDialer{conn: &fakeConn{}}, noSecret, nil)
	_, err := h(job.Job{ElementInstanceKey: 1})
	if err == nil {
		t.Fatal("want an error when the compiled process is gone")
	}
}

// TestAdHandlerNotAConnectorTask proves an element instance that points at a
// non-connector node (here the start event) fails the job.
func TestAdHandlerNotAConnectorTask(t *testing.T) {
	cp, _, startID := adProcessIDs(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=x")})
	r := &fakeReader{ei: &model.ElementInstanceValue{ProcessDefKey: cp.Key, ElementId: startID}, eiOK: true}
	h := ad.Handler(r, func(uint64) *compiler.CompiledProcess { return cp }, &fakeDialer{conn: &fakeConn{}}, noSecret, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); err == nil {
		t.Fatal("want an error when the element is not a task")
	}
}

// TestAdHandlerReadVarsError proves a failure reading the scope's variables fails the
// job before any dial.
func TestAdHandlerReadVarsError(t *testing.T) {
	cp, connID, _ := adProcessIDs(t, compiler.AdConfig{URL: lit(adURL), Op: "disable", DN: lit("cn=x")})
	r := &fakeReader{
		ei:      &model.ElementInstanceValue{ProcessDefKey: cp.Key, ElementId: connID, ProcessInstanceKey: 1},
		eiOK:    true,
		varsErr: errors.New("read failed"),
	}
	dialer := &fakeDialer{conn: &fakeConn{}}
	h := ad.Handler(r, func(uint64) *compiler.CompiledProcess { return cp }, dialer, noSecret, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); err == nil {
		t.Fatal("want an error when the variable read fails")
	}
	if dialer.dials != 0 {
		t.Errorf("dials = %d, want 0 (no dial when the variable read fails)", dialer.dials)
	}
}

// TestAdSetPasswordEmpty proves a set-password whose newPassword resolves to empty (a
// FEEL reference to an absent variable) fails the job rather than writing a blank
// password.
func TestAdSetPasswordEmpty(t *testing.T) {
	log, store := openStore(t)
	e, err := expr.CompileAuto("missingPw")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cp, jobType := adProcess(t, compiler.AdConfig{
		URL: lit(adURL), Op: "set-password", DN: lit("cn=x"), NewPassword: compiler.RestExpr{Expr: e},
	}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if conn.modDN != "" {
		t.Errorf("modify ran (%q) despite an empty newPassword", conn.modDN)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// TestAdUnknownOperation proves an authored operation the dispatcher does not know
// fails the job (a defence in depth behind the compiler's operation check).
func TestAdUnknownOperation(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adProcess(t, compiler.AdConfig{URL: lit(adURL), Op: "frobnicate", DN: lit("cn=x")}, false)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if conn.modDN != "" || conn.addDN != "" {
		t.Errorf("an operation ran despite an unknown op: mod=%q add=%q", conn.modDN, conn.addDN)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

var _ = state.Reader(&fakeReader{})
