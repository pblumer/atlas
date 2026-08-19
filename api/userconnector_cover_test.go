package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// TestEnsureSystemProjectGetError covers ensureSystemProject's get-error branch:
// making the system project's record path a directory makes the read fail with a
// non-not-exist error.
func TestEnsureSystemProjectGetError(t *testing.T) {
	srv := newServerForErrors(t)
	// A fresh projects store where the system project's record path is a directory,
	// so get() fails with a non-not-exist error.
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects2"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.FileFor(systemProjectID), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	srv.projects = ps
	if err := srv.ensureSystemProject(1); err == nil {
		t.Error("ensureSystemProject with an unreadable record: want error")
	}
}

// fakeElemStore is a userConnStore that returns canned results, so the worker's
// store-error, unresolvable-process, and bad-element branches can be driven
// without a live engine.
type fakeElemStore struct {
	ei      *model.ElementInstanceValue
	ok      bool
	getErr  error
	varsErr error
}

func (f fakeElemStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return f.ei, f.ok, f.getErr
}

func (f fakeElemStore) VariablesOfScope(uint64, func(*model.VariableValue) error) error {
	return f.varsErr
}

// TestUserConnectorHandlerStoreBranches drives the worker's early error branches
// with a fake store and a real server (for processLookup/systemPIDs).
func TestUserConnectorHandlerStoreBranches(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses(), WithUserProvisioning())
	var aufnahme uint64
	for _, r := range listProcesses(t, srv.Handler()) {
		if r.ProcessID == "proc_benutzer_aufnahme" {
			aufnahme = r.Key
		}
	}
	if aufnahme == 0 {
		t.Fatal("intake process not deployed")
	}

	// GetElementInstance error → returned verbatim.
	if err := srv.userConnectorHandler(fakeElemStore{getErr: errors.New("boom")})(job.Job{}); err == nil {
		t.Error("get error: want error")
	}
	// Element instance gone → nil (no-op).
	if err := srv.userConnectorHandler(fakeElemStore{ok: false})(job.Job{}); err != nil {
		t.Errorf("element gone: want nil, got %v", err)
	}
	// Unknown process definition → cp nil error.
	unknown := fakeElemStore{ok: true, ei: &model.ElementInstanceValue{ProcessDefKey: 999999}}
	if err := srv.userConnectorHandler(unknown)(job.Job{}); err == nil {
		t.Error("cp nil: want error")
	}
	// A real system process but a bad element id → ConnectorTaskOf error (the gate
	// passes first, since the process id is a system process).
	badElem := fakeElemStore{ok: true, ei: &model.ElementInstanceValue{ProcessDefKey: aufnahme, ElementId: 9999}}
	if err := srv.userConnectorHandler(badElem)(job.Job{}); err == nil {
		t.Error("bad element id: want ConnectorTaskOf error")
	}
}

// TestUserReadScopeVarsError covers the read-variables error path directly.
func TestUserReadScopeVarsError(t *testing.T) {
	if _, err := userReadScopeVars(fakeElemStore{varsErr: errors.New("nope")}, 1); err == nil {
		t.Error("userReadScopeVars with a failing store: want error")
	}
}

// TestLoadSystemBundleErrors covers loadSystemBundle's packaging-error branches via
// synthetic filesystems.
func TestLoadSystemBundleErrors(t *testing.T) {
	// Missing bundle directory → read-dir error.
	if _, _, err := loadSystemBundle(fstest.MapFS{}); err == nil {
		t.Error("empty FS: want read-dir error")
	}
	// A .bpmn with no <process id>.
	badBpmn := fstest.MapFS{"systemprocesses/x.bpmn": {Data: []byte(`<definitions/>`)}}
	if _, _, err := loadSystemBundle(badBpmn); err == nil {
		t.Error("bpmn without process id: want error")
	}
	// Invalid JSON.
	badJSON := fstest.MapFS{"systemprocesses/x.json": {Data: []byte(`{`)}}
	if _, _, err := loadSystemBundle(badJSON); err == nil {
		t.Error("invalid json: want error")
	}
	// JSON without an id.
	noID := fstest.MapFS{"systemprocesses/x.json": {Data: []byte(`{}`)}}
	if _, _, err := loadSystemBundle(noID); err == nil {
		t.Error("json without id: want error")
	}
}

// TestEnsureSystemProcessesBundleError covers ensureSystemProcesses's bundle-error
// branch by pointing the bootstrap FS at a broken bundle.
func TestEnsureSystemProcessesBundleError(t *testing.T) {
	srv := newServerForErrors(t)
	orig := systemBundleFS
	t.Cleanup(func() { systemBundleFS = orig })
	systemBundleFS = fstest.MapFS{} // no bundle dir → loadSystemBundle errors
	if err := srv.ensureSystemProcesses(1); err == nil {
		t.Error("ensureSystemProcesses with a broken bundle FS: want error")
	}
}

// TestProvisionHashPasswordError covers the bcrypt-error branch: a password longer
// than 72 bytes makes hashPassword fail, for both create and set-password.
func TestProvisionHashPasswordError(t *testing.T) {
	srv := newServerForErrors(t)
	long := strings.Repeat("x", 80)
	if _, _, err := srv.provisionCreateUser("u", "", "", nil, long, 1); err == nil {
		t.Error("create with an 80-char password: want hash error")
	}
	// Seed a user, then a too-long set-password must fail at hashing.
	if _, _, err := srv.provisionCreateUser("bob", "", "", nil, "longenough1", 1); err != nil {
		t.Fatalf("seed bob: %v", err)
	}
	if err := srv.provisionSetPassword("bob", long, 1); err == nil {
		t.Error("set-password with an 80-char password: want hash error")
	}
}

// TestUserConnectorPureHelpers exercises the small FEEL-binding helpers across
// every branch: the kind mapping, the variable binding (builtin key, present and
// absent names, the empty-names short-circuit), a literal resolve, and role
// splitting.
func TestUserConnectorPureHelpers(t *testing.T) {
	kinds := map[model.VarKind]expr.ValueKind{
		model.VarBool:    expr.KindBool,
		model.VarNumber:  expr.KindNumber,
		model.VarString:  expr.KindString,
		model.VarJSON:    expr.KindJSON,
		model.VarKind(0): expr.KindNull,
	}
	for k, want := range kinds {
		if got := userToExprKind(k); got != want {
			t.Errorf("userToExprKind(%v) = %v, want %v", k, got, want)
		}
	}

	if userBindVars(1, nil, nil) != nil {
		t.Error("userBindVars with no names should be nil")
	}
	vars := map[string]model.VariableValue{"a": {Name: "a", Kind: model.VarString, Text: "x"}}
	m := userBindVars(42, vars, []string{"a", "missing", userBuiltinProcessInstanceKey})
	if _, ok := m["a"]; !ok {
		t.Error("present var a not bound")
	}
	if _, ok := m["missing"]; ok {
		t.Error("absent var missing should not be bound")
	}
	if _, _, text := expr.Classify(m[userBuiltinProcessInstanceKey]); text != "42" {
		t.Errorf("processInstanceKey bound to %q, want 42", text)
	}

	if got := userResolve(compiler.RestExpr{Literal: "lit"}, 1, nil); got != "lit" {
		t.Errorf("userResolve literal = %q, want lit", got)
	}

	if splitRoles("  ") != nil {
		t.Error("splitRoles of blank should be nil")
	}
	got := splitRoles("a, b ,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("splitRoles = %v, want [a b c]", got)
	}
}

// TestProvisionOpsStoreErrors covers the store-error returns of the three
// provisioning operations by pointing the user store at an unreadable directory,
// so the initial username lookup fails.
func TestProvisionOpsStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	srv.users = brokenStore(newUserStore(filepath.Join(t.TempDir(), "nope", "deeper")))

	if _, _, err := srv.provisionCreateUser("u", "", "", nil, "longenough1", 1); err == nil {
		t.Error("provisionCreateUser with broken store: want error")
	}
	if err := srv.provisionSetPassword("u", "longenough1", 1); err == nil {
		t.Error("provisionSetPassword with broken store: want error")
	}
	if err := srv.provisionDisableUser("u", 1); err == nil {
		t.Error("provisionDisableUser with broken store: want error")
	}
}

// TestEnsureSystemProcessesErrors covers the seed-error branches of the bootstrap:
// a broken form store fails the form seed, and a broken draft store fails the
// draft filing.
func TestEnsureSystemProcessesErrors(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())

	broken := func(sub string) string { return filepath.Join(t.TempDir(), sub, "deeper") }

	srv.forms = brokenStore(newFormStore(broken("forms")))
	if err := srv.ensureSystemProcesses(1); err == nil {
		t.Error("ensureSystemProcesses with broken form store: want error")
	}

	realForms, err := newFormStore(filepath.Join(t.TempDir(), "forms-ok"))
	if err != nil {
		t.Fatalf("newFormStore: %v", err)
	}
	srv.forms = realForms
	srv.drafts = brokenStore(newDraftStore(broken("drafts")))
	if err := srv.ensureSystemProcesses(1); err == nil {
		t.Error("ensureSystemProcesses with broken draft store: want error")
	}
}
