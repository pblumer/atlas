package sqldb

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// maxScopeDepth bounds the walk up an element's scope chain, so a malformed chain
// cannot loop. It matches the CSV connector's bound.
const maxScopeDepth = 64

// VarStore is the slice of the state store the engine half reads: the variables
// visible at an element, up its scope chain.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Job is a SQL task with everything already evaluated: which database, which
// statement, and the parameters bound to it. It is what travels with a leased job.
//
// There is nowhere here to put a DSN, a username, or a password, and that is the
// point: the engine resolves a Job and hands it over without ever having held a
// database credential (ADR-0170).
type Job struct {
	// Connector names the database the *worker* is configured for. A name and not an
	// address, because an address is half a credential.
	Connector string `json:"connector"`
	// Product is the operator-facing kind name ("mssql"|"mariadb"|"postgres"),
	// derived from the task's reserved job type.
	Product   string `json:"product"`
	Operation string `json:"operation"`
	Statement string `json:"statement"`
	// Params binds positionally, Named by key. At most one is ever set: the shape of
	// the authored parameters variable decides which.
	Params         []any          `json:"params,omitempty"`
	Named          map[string]any `json:"named,omitempty"`
	MaxRows        int            `json:"maxRows,omitempty"`
	ResultVariable string         `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled SQL connector task into a [Job]: the authored statement
// from the detail, and the parameters read up the task's scope chain. It is engine
// work by necessity — the compiled process and the scope live only there.
//
// The statement needs no resolving. It is literal by construction, which is the whole
// of this connector's injection defence (ADR-0170).
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("sqldb: connector task has no detail")
	}
	product, ok := ProductByJobType(cp.Intern(detail.JobType))
	if !ok {
		return Job{}, fmt.Errorf("sqldb: job type %q is not a SQL connector", cp.Intern(detail.JobType))
	}
	j := Job{
		Connector:      cp.Intern(detail.Connector),
		Product:        product.Name,
		Operation:      cp.Intern(detail.SqlOp),
		Statement:      cp.Intern(detail.SqlStatement),
		MaxRows:        int(detail.SqlMaxRows),
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	paramsVar := cp.Intern(detail.SqlParamsVar)
	if paramsVar == "" {
		return j, nil // a statement with no placeholders
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("sqldb: read variables: %w", err)
	}
	v, ok := vars[paramsVar]
	if !ok {
		return Job{}, fmt.Errorf("sqldb: parameters variable %q is not set at this task", paramsVar)
	}
	if err := bindParams(&j, v, product); err != nil {
		return Job{}, err
	}
	return j, nil
}

// bindParams reads the authored parameters variable into the job.
//
// A JSON array binds positionally and a JSON object binds by name; any other kind is
// the one-placeholder case and binds as a single positional value. An object against
// a driver with no named parameters is refused rather than flattened — map iteration
// has no order, so "flatten it" would mean binding the author's values in an order
// nobody wrote.
func bindParams(j *Job, v model.VariableValue, p Product) error {
	if v.Kind != model.VarJSON {
		val, err := scalarOf(v)
		if err != nil {
			return err
		}
		j.Params = []any{val}
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(v.Text)))
	dec.UseNumber() // an id must not come back as 1e+06
	var any1 any
	if err := dec.Decode(&any1); err != nil {
		return fmt.Errorf("sqldb: parameters variable is not valid JSON: %w", err)
	}
	switch t := any1.(type) {
	case []any:
		j.Params = make([]any, len(t))
		for i, e := range t {
			j.Params[i] = jsonScalar(e)
		}
	case map[string]any:
		if !p.NamedParams {
			return fmt.Errorf("sqldb: %s binds parameters positionally (%s), so the parameters variable must be a list, not an object", p.Name, p.Placeholder)
		}
		j.Named = make(map[string]any, len(t))
		for k, e := range t {
			j.Named[k] = jsonScalar(e)
		}
	default:
		j.Params = []any{jsonScalar(any1)}
	}
	return nil
}

// scalarOf turns a non-JSON process variable into a bindable Go value.
func scalarOf(v model.VariableValue) (any, error) {
	switch v.Kind {
	case model.VarNull:
		return nil, nil
	case model.VarBool:
		return v.Bool, nil
	case model.VarString:
		return v.Text, nil
	case model.VarNumber:
		return jsonScalar(json.Number(v.Text)), nil
	default:
		return nil, fmt.Errorf("sqldb: parameters variable has an unbindable kind %v", v.Kind)
	}
}

// jsonScalar narrows a decoded JSON value to what a driver can bind. json.Number
// becomes an int64 when it is integral and a float64 otherwise, so a primary key
// binds as an integer rather than as a float the database has to round-trip.
func jsonScalar(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}

// Run executes a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The connector lookup comes first, as it does for mail: an unconfigured name is the
// actionable failure, and reporting it ahead of anything about the statement keeps
// the message pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("sqldb", j.Connector)
	}
	if got := client.Product().Name; got != j.Product {
		return nil, fmt.Errorf("sqldb: the task is a %s task but connector %q is a %s database", j.Product, j.Connector, got)
	}
	args, err := bindArgs(j, client.Product())
	if err != nil {
		return nil, err
	}
	switch j.Operation {
	case "query":
		rows, err := client.Query(ctx, j.Statement, args, j.MaxRows)
		if err != nil {
			return nil, err
		}
		return result(j, rows), nil
	case "query-one":
		// Two rows are asked for, not one: "the row for this person" matching twice is
		// a bad assumption, and reporting it beats silently taking the first.
		rows, err := client.Query(ctx, j.Statement, args, 2)
		if err != nil {
			return nil, err
		}
		switch len(rows) {
		case 0:
			return result(j, nil), nil
		case 1:
			return result(j, rows[0]), nil
		default:
			return nil, fmt.Errorf("sqldb: query-one matched more than one row; use query, or narrow the statement")
		}
	case "execute":
		n, err := client.Exec(ctx, j.Statement, args)
		if err != nil {
			return nil, err
		}
		return result(j, n), nil
	default:
		return nil, fmt.Errorf("sqldb: unknown operation %q", j.Operation)
	}
}

// bindArgs turns the job's resolved parameters into database/sql arguments.
func bindArgs(j Job, p Product) ([]any, error) {
	if len(j.Named) > 0 {
		if !p.NamedParams {
			return nil, fmt.Errorf("sqldb: %s binds parameters positionally (%s), so named parameters cannot be used", p.Name, p.Placeholder)
		}
		args := make([]any, 0, len(j.Named))
		for k, v := range j.Named {
			args = append(args, sql.Named(k, v))
		}
		return args, nil
	}
	return j.Params, nil
}

// result wraps a value as the variables the job completes with, or none when the
// task discards its result (valid only for execute).
func result(j Job, v any) map[string]any {
	if j.ResultVariable == "" {
		return nil
	}
	return map[string]any{j.ResultVariable: v}
}

// scopeChainVars reads the variables an element sees up its scope chain (nearest
// wins), the same walk the CSV connector makes.
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	scope := elementInstanceKey
	for depth := 0; depth <= maxScopeDepth; depth++ {
		if err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
			if _, seen := vars[v.Name]; !seen {
				vars[v.Name] = *v
			}
			return nil
		}); err != nil {
			return nil, err
		}
		ei, ok, err := store.GetElementInstance(scope)
		if err != nil {
			return nil, err
		}
		if !ok || ei.FlowScopeKey == 0 || ei.FlowScopeKey == scope {
			break
		}
		scope = ei.FlowScopeKey
	}
	return vars, nil
}
