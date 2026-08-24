package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

type fakeVarStore struct {
	vars    map[uint64][]model.VariableValue
	ei      map[uint64]model.ElementInstanceValue
	varsErr error
	eiErr   error
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
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

// buildTask compiles a one-task process of the given product and returns the pieces
// Resolve takes. Going through the real Builder keeps the test honest about interning.
func buildTask(t *testing.T, cfg compiler.SqlConfig) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	el := b.AddSqlConnectorTask(cfg)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(el).Detail)
}

func jsonVar(name, text string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarJSON, Text: text}
}

// The engine's half: the authored statement travels verbatim, and the parameters are
// read out of the instance's scope chain (ADR-0168).
func TestResolveProducesAPlainJob(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "hr-db", Op: "query",
		Statement: "SELECT id FROM personen WHERE abteilung = $1",
		ParamsVar: "params", ResultVar: "zeilen", MaxRows: 50,
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		10: {jsonVar("params", `["IT"]`)},
	}}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Connector != "hr-db" || j.Product != "postgres" || j.Operation != "query" {
		t.Errorf("job = %+v", j)
	}
	if j.Statement != "SELECT id FROM personen WHERE abteilung = $1" {
		t.Errorf("statement = %q", j.Statement)
	}
	if len(j.Params) != 1 || j.Params[0] != "IT" {
		t.Errorf("params = %#v, want [IT]", j.Params)
	}
	if j.MaxRows != 50 || j.ResultVariable != "zeilen" {
		t.Errorf("maxRows/result = %d/%q", j.MaxRows, j.ResultVariable)
	}
}

// A number binds as an integer, not as the float64 a plain JSON decode would give:
// a primary key round-tripped through 1e+06 finds nothing.
func TestResolveBindsIntegersAsIntegers(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {jsonVar("p", `[1000000, 1.5, "x", true, null]`)},
	}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []any{int64(1000000), 1.5, "x", true, nil}
	if len(j.Params) != len(want) {
		t.Fatalf("params = %#v", j.Params)
	}
	for i := range want {
		if j.Params[i] != want[i] {
			t.Errorf("params[%d] = %#v (%T), want %#v", i, j.Params[i], j.Params[i], want[i])
		}
	}
}

// A scalar variable is the one-placeholder case and binds positionally.
func TestResolveBindsScalarVariables(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    model.VariableValue
		want any
	}{
		{"string", model.VariableValue{Name: "p", Kind: model.VarString, Text: "IT"}, "IT"},
		{"number", model.VariableValue{Name: "p", Kind: model.VarNumber, Text: "42"}, int64(42)},
		{"bool", model.VariableValue{Name: "p", Kind: model.VarBool, Bool: true}, true},
		{"null", model.VariableValue{Name: "p", Kind: model.VarNull}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, d := buildTask(t, compiler.SqlConfig{
				JobType: compiler.MariaDBJobType, Connector: "db", Op: "query",
				Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
			})
			store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {tc.v}}}
			j, err := Resolve(store, cp, d, 1)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if len(j.Params) != 1 || j.Params[0] != tc.want {
				t.Errorf("params = %#v, want [%#v]", j.Params, tc.want)
			}
		})
	}
}

// SQL Server binds by name; the other two do not, and an object is refused rather
// than flattened into an order the author never wrote.
func TestResolveNamedParameters(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.MsSqlJobType, Connector: "db", Op: "query",
		Statement: "SELECT id FROM p WHERE abt = @abt", ParamsVar: "p", ResultVar: "r",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {jsonVar("p", `{"abt":"IT"}`)}}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Named["abt"] != "IT" || j.Params != nil {
		t.Errorf("named = %#v, params = %#v", j.Named, j.Params)
	}

	for _, jt := range []string{compiler.MariaDBJobType, compiler.PostgresJobType} {
		cp2, d2 := buildTask(t, compiler.SqlConfig{
			JobType: jt, Connector: "db", Op: "query", Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
		})
		_, err := Resolve(store, cp2, d2, 1)
		if err == nil {
			t.Errorf("%s: an object parameters variable must be refused", jt)
			continue
		}
		if !strings.Contains(err.Error(), "must be a list") {
			t.Errorf("%s: error = %v", jt, err)
		}
	}
}

func TestResolveErrors(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "fehlt", ResultVar: "r",
	})
	if _, err := Resolve(fakeVarStore{}, cp, nil, 1); err == nil {
		t.Error("a nil detail must fail")
	}
	// A named parameters variable that is not set is an error, not zero parameters:
	// binding none to a statement with placeholders fails less legibly, later.
	if _, err := Resolve(fakeVarStore{}, cp, d, 1); err == nil {
		t.Error("a missing parameters variable must fail")
	}
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {jsonVar("fehlt", `{not json`)}}}
	if _, err := Resolve(store, cp, d, 1); err == nil {
		t.Error("a malformed parameters variable must fail")
	}
	// A task with no parameters variable resolves without touching the store.
	cpNo, dNo := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query", Statement: "SELECT 1", ResultVar: "r",
	})
	if _, err := Resolve(fakeVarStore{}, cpNo, dNo, 1); err != nil {
		t.Errorf("a statement with no placeholders must resolve: %v", err)
	}
}

// A detail whose job type is not one of the three is not a SQL task.
func TestResolveRejectsAForeignJobType(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	el := b.AddSoapConnectorTask(compiler.SoapConfig{
		Endpoint: compiler.RestExpr{Literal: "http://x"}, Op: "Ping",
		Body: compiler.RestExpr{Literal: "<a/>"}, Version: "1.1",
	})
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Resolve(fakeVarStore{}, cp, cp.ConnectorTask(cp.Node(el).Detail), 1); err == nil {
		t.Error("a SOAP task must not resolve as SQL")
	}
}

// regWith registers one client under a name.
func regWith(name string, c *Client) *Registry {
	r := NewRegistry()
	r.Register(name, c)
	return r
}

// The worker's half, end to end against the fake driver.
func TestRunQuery(t *testing.T) {
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id"},
		rows: [][]driver.Value{{int64(1)}, {int64(2)}},
	})
	got, err := Run(context.Background(), Job{
		Connector: "hr", Product: "postgres", Operation: "query",
		Statement: "SELECT id FROM t", ResultVariable: "zeilen",
	}, regWith("hr", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, ok := got["zeilen"].([]map[string]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("result = %#v", got)
	}
}

func TestRunQueryOne(t *testing.T) {
	p := mustProduct(t, "postgres")
	// One row: the object itself, not a list of one.
	one := openFake(t, p, &fakeResult{cols: []string{"id"}, rows: [][]driver.Value{{int64(1)}}})
	got, err := Run(context.Background(), Job{
		Connector: "hr", Product: "postgres", Operation: "query-one",
		Statement: "SELECT id FROM t WHERE id = $1", ResultVariable: "person",
	}, regWith("hr", one))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if row, ok := got["person"].(map[string]any); !ok || row["id"] != int64(1) {
		t.Errorf("result = %#v, want the row object", got)
	}
}

// No match is a null result, not an error: "this person has no record" is an answer a
// process can branch on.
func TestRunQueryOneNoRowsIsNull(t *testing.T) {
	none := openFake(t, mustProduct(t, "postgres"), &fakeResult{cols: []string{"id"}})
	got, err := Run(context.Background(), Job{
		Connector: "hr", Product: "postgres", Operation: "query-one",
		Statement: "SELECT id FROM t", ResultVariable: "person",
	}, regWith("hr", none))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v, ok := got["person"]; !ok || v != nil {
		t.Errorf("result = %#v, want a present nil", got)
	}
}

// Two matches is a bad assumption, reported rather than silently resolved to the first.
func TestRunQueryOneRejectsASecondRow(t *testing.T) {
	two := openFake(t, mustProduct(t, "postgres"), &fakeResult{
		cols: []string{"id"}, rows: [][]driver.Value{{int64(1)}, {int64(2)}},
	})
	_, err := Run(context.Background(), Job{
		Connector: "hr", Product: "postgres", Operation: "query-one",
		Statement: "SELECT id FROM t", ResultVariable: "person",
	}, regWith("hr", two))
	if err == nil || !strings.Contains(err.Error(), "more than one row") {
		t.Errorf("err = %v, want a more-than-one-row error", err)
	}
}

func TestRunExecute(t *testing.T) {
	c := openFake(t, mustProduct(t, "mariadb"), &fakeResult{affected: 4})
	got, err := Run(context.Background(), Job{
		Connector: "hr", Product: "mariadb", Operation: "execute",
		Statement: "DELETE FROM t WHERE x = ?", Params: []any{int64(1)}, ResultVariable: "betroffen",
	}, regWith("hr", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got["betroffen"] != int64(4) {
		t.Errorf("result = %#v, want 4", got)
	}
	// execute may discard its count.
	quiet := openFake(t, mustProduct(t, "mariadb"), &fakeResult{affected: 4})
	got2, err := Run(context.Background(), Job{
		Connector: "hr", Product: "mariadb", Operation: "execute", Statement: "DELETE FROM t",
	}, regWith("hr", quiet))
	if err != nil || got2 != nil {
		t.Errorf("a discarded count should complete with no variables: %#v, %v", got2, err)
	}
}

// Named parameters reach the driver as sql.Named, which is what makes @name binding
// work at all.
func TestRunBindsNamedParameters(t *testing.T) {
	r := &fakeResult{cols: []string{"id"}, rows: [][]driver.Value{{int64(1)}}}
	c := openFake(t, mustProduct(t, "mssql"), r)
	_, err := Run(context.Background(), Job{
		Connector: "hr", Product: "mssql", Operation: "query",
		Statement: "SELECT id FROM p WHERE abt = @abt", Named: map[string]any{"abt": "IT"},
		ResultVariable: "r",
	}, regWith("hr", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fakeMu.Lock()
	args := append([]driver.NamedValue(nil), r.gotArgs...)
	fakeMu.Unlock()
	if len(args) != 1 || args[0].Name != "abt" || args[0].Value != "IT" {
		t.Errorf("args = %#v, want one named argument", args)
	}
}

func TestRunErrors(t *testing.T) {
	c := openFake(t, mustProduct(t, "postgres"), &fakeResult{cols: []string{"id"}})

	// An unconfigured name is the actionable failure and is reported first.
	if _, err := Run(context.Background(), Job{Connector: "nope", Product: "postgres", Operation: "query"}, NewRegistry()); err == nil {
		t.Error("an unregistered connector must fail")
	}
	// A product mismatch cannot come from the modeler, but can from a hand-edited
	// worker configuration, and binding $1 against SQL Server would fail obscurely.
	if _, err := Run(context.Background(), Job{Connector: "hr", Product: "mssql", Operation: "query"}, regWith("hr", c)); err == nil {
		t.Error("a product mismatch must fail")
	}
	if _, err := Run(context.Background(), Job{Connector: "hr", Product: "postgres", Operation: "vacuum"}, regWith("hr", c)); err == nil {
		t.Error("an unknown operation must fail")
	}
	// Named parameters against a positional driver are refused at the bind, too —
	// Resolve catches the ordinary path, this catches a job built by hand.
	if _, err := Run(context.Background(), Job{
		Connector: "hr", Product: "postgres", Operation: "query",
		Named: map[string]any{"a": 1}, ResultVariable: "r",
	}, regWith("hr", c)); err == nil {
		t.Error("named parameters against postgres must fail")
	}
}

// A client closes its pool.
func TestClientClose(t *testing.T) {
	db, err := sql.Open("atlasfake", registerFake(t, &fakeResult{}))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := NewClient(db, mustProduct(t, "postgres")).Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// A bare JSON scalar in the parameters variable is the one-placeholder case.
func TestResolveBareJSONScalarBindsPositionally(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {jsonVar("p", `"IT"`)}}}
	j, err := Resolve(store, cp, d, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(j.Params) != 1 || j.Params[0] != "IT" {
		t.Errorf("params = %#v", j.Params)
	}
}

// A variable kind nothing can be bound from is reported rather than sent as a zero.
func TestResolveRejectsAnUnbindableVariable(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
	})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{
		1: {{Name: "p", Kind: model.VarKind(99)}},
	}}
	if _, err := Resolve(store, cp, d, 1); err == nil {
		t.Fatal("an unbindable variable kind must fail")
	}
}

// A store that cannot be read fails the resolve rather than binding nothing.
func TestResolveStoreErrors(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
	})
	if _, err := Resolve(fakeVarStore{varsErr: errTest}, cp, d, 1); err == nil {
		t.Error("a variables error must fail the resolve")
	}
	if _, err := Resolve(fakeVarStore{eiErr: errTest}, cp, d, 1); err == nil {
		t.Error("an element-instance error must fail the resolve")
	}
}

var errTest = errors.New("store unavailable")

// A parameter set on an outer scope is visible at the task, and a nearer one wins.
func TestResolveWalksTheScopeChain(t *testing.T) {
	cp, d := buildTask(t, compiler.SqlConfig{
		JobType: compiler.PostgresJobType, Connector: "db", Op: "query",
		Statement: "SELECT 1", ParamsVar: "p", ResultVar: "r",
	})
	store := fakeVarStore{
		vars: map[uint64][]model.VariableValue{
			99: {jsonVar("p", `["aussen"]`)},
		},
		ei: map[uint64]model.ElementInstanceValue{
			10: {FlowScopeKey: 99},
		},
	}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(j.Params) != 1 || j.Params[0] != "aussen" {
		t.Errorf("params = %#v, want the outer scope's value", j.Params)
	}
}

// jsonScalar keeps a non-integral number a float and falls back to the text for a
// number no Go type holds.
func TestJSONScalar(t *testing.T) {
	if got := jsonScalar(json.Number("1.5")); got != 1.5 {
		t.Errorf("1.5 = %#v", got)
	}
	huge := json.Number("1e999")
	if got := jsonScalar(huge); got != "1e999" {
		t.Errorf("an unrepresentable number should keep its text, got %#v", got)
	}
	if got := jsonScalar("plain"); got != "plain" {
		t.Errorf("a non-number should pass through, got %#v", got)
	}
}
