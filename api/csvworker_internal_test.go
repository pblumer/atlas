package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// fakeVarStore is a csvVarStore for exercising the scope-chain walk and its error
// paths without a real store.
type fakeVarStore struct {
	vars    map[uint64][]model.VariableValue
	ei      map[uint64]model.ElementInstanceValue
	varsErr error
	eiErr   error
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	if f.varsErr != nil {
		return f.varsErr
	}
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if f.eiErr != nil {
		return nil, false, f.eiErr
	}
	ei, ok := f.ei[key]
	if !ok {
		return nil, false, nil
	}
	return &ei, true, nil
}

// TestCSVScopeChainVars covers the scope-chain walk: nearest scope wins across
// levels, and the two store-read error paths.
func TestCSVScopeChainVars(t *testing.T) {
	// Element 10 (an inner scope) has csvText; its parent 20 has columnConfig and a
	// shadowed csvText the nearer scope must win over. 20 is the root (no element).
	store := fakeVarStore{
		vars: map[uint64][]model.VariableValue{
			10: {{Name: "csvText", Kind: model.VarString, Text: "inner"}},
			20: {{Name: "csvText", Kind: model.VarString, Text: "outer"}, {Name: "columnConfig", Kind: model.VarJSON, Text: "{}"}},
		},
		ei: map[uint64]model.ElementInstanceValue{10: {FlowScopeKey: 20}},
	}
	got, err := csvScopeChainVars(store, 10)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if got["csvText"].Text != "inner" {
		t.Errorf("csvText = %q, want the nearer scope's \"inner\"", got["csvText"].Text)
	}
	if got["columnConfig"].Text != "{}" {
		t.Errorf("columnConfig = %q, want it read from the enclosing scope", got["columnConfig"].Text)
	}

	if _, err := csvScopeChainVars(fakeVarStore{varsErr: errors.New("boom")}, 1); err == nil {
		t.Error("VariablesOfScope error: want an error, got nil")
	}
	if _, err := csvScopeChainVars(fakeVarStore{eiErr: errors.New("boom")}, 1); err == nil {
		t.Error("GetElementInstance error: want an error, got nil")
	}
}

// TestCSVImportHandler covers the handler's element-instance and scope-read
// branches on the legacy (nil-lookup) path.
func TestCSVImportHandler(t *testing.T) {
	// A present element instance, but reading its scope variables fails: the handler
	// wraps it as a read-variables error.
	readErr := fakeVarStore{
		ei:      map[uint64]model.ElementInstanceValue{1: {}},
		varsErr: errors.New("boom"),
	}
	h := csvImportHandler(readErr, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "read variables") {
		t.Fatalf("error = %v, want a read-variables error", err)
	}

	// GetElementInstance failing is surfaced as a get-element-instance error.
	h = csvImportHandler(fakeVarStore{eiErr: errors.New("boom")}, nil)
	if _, err := h(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "get element instance") {
		t.Fatalf("error = %v, want a get-element-instance error", err)
	}

	// A gone element instance (ok=false) is a no-op, not an error.
	h = csvImportHandler(fakeVarStore{}, nil)
	if out, err := h(job.Job{ElementInstanceKey: 99}); err != nil || out != nil {
		t.Fatalf("gone instance: out=%v err=%v, want nil, nil", out, err)
	}
}

func vjson(text string) model.VariableValue {
	return model.VariableValue{Kind: model.VarJSON, Text: text}
}
func vstr(text string) model.VariableValue {
	return model.VariableValue{Kind: model.VarString, Text: text}
}
func vnum(text string) model.VariableValue {
	return model.VariableValue{Kind: model.VarNumber, Text: text}
}
func vars(kv map[string]model.VariableValue) map[string]model.VariableValue { return kv }

// TestCSVRowsFromVars covers the CSV-import worker's pure core directly: the happy
// parse and each input/parse-error branch, without standing up the engine.
func TestCSVRowsFromVars(t *testing.T) {
	okCfg := vjson(`{"columns":[{"name":"email"},{"name":"group"}]}`)

	t.Run("happy path", func(t *testing.T) {
		out, err := csvRowsFromVars(vars(map[string]model.VariableValue{
			"csvText":      vstr("email,group\nada@x.io,users\nbob,ops\n"),
			"columnConfig": okCfg,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 2 || out[0].Name != "rows" || out[1].Name != "rowCount" {
			t.Fatalf("outputs = %+v, want rows + rowCount", out)
		}
		if out[1].Text != "2" {
			t.Errorf("rowCount = %q, want 2", out[1].Text)
		}
		if out[0].Kind != model.VarJSON || !strings.Contains(out[0].Text, `"email":"ada@x.io"`) {
			t.Errorf("rows = %+v, want a JSON array with ada@x.io", out[0])
		}
	})

	errCases := []struct {
		name string
		in   map[string]model.VariableValue
		want string
	}{
		{"missing csvText", map[string]model.VariableValue{"columnConfig": okCfg}, "csvText"},
		{"csvText wrong kind", map[string]model.VariableValue{"csvText": vnum("1"), "columnConfig": okCfg}, "csvText"},
		{"empty csvText", map[string]model.VariableValue{"csvText": vstr(""), "columnConfig": okCfg}, "csvText"},
		{"missing columnConfig", map[string]model.VariableValue{"csvText": vstr("email\nx\n")}, "columnConfig"},
		{"columnConfig wrong kind", map[string]model.VariableValue{"csvText": vstr("email\nx\n"), "columnConfig": vstr("{}")}, "columnConfig"},
		{"columnConfig bad JSON", map[string]model.VariableValue{"csvText": vstr("email\nx\n"), "columnConfig": vjson("{")}, "invalid columnConfig"},
		{"unparseable layout", map[string]model.VariableValue{"csvText": vstr("email\nx\n"), "columnConfig": vjson(`{"columns":[],"hasHeader":false}`)}, "at least one column"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := csvRowsFromVars(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
