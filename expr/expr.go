// Package expr is Atlas's boundary to a FEEL engine. It wraps
// github.com/pblumer/feel so the rest of Atlas depends on this small, stable
// surface rather than on the FEEL library's full API.
//
// It implements the ADR-0008 contract: expressions are compiled once at deploy
// time (parse + type-check + lower to a closure) and evaluated many times with no
// re-parsing and minimal allocation on the hot path. Compilation also reports an
// expression's inputs — the variable names it reads — so the engine can load only
// those from a scope instead of materializing the whole variable set.
//
// Reuse of an existing FEEL engine (versus building our own subset) is exactly
// what ADR-0008 permits when the library offers a genuine compile-once/eval-many
// API; feel does. See ADR-0014.
package expr

import (
	"github.com/pblumer/feel"
	"github.com/pblumer/feel/value"
)

// Value is a FEEL runtime value. Re-exported so engine code refers to
// expr.Value rather than importing the FEEL value package throughout.
type Value = value.Value

// Compiled is a FEEL expression compiled once. It is immutable and safe for
// concurrent evaluation (like the CompiledProcess it lives in). Evaluate it with
// Eval; Inputs reports the variables it reads.
type Compiled struct {
	fn     feel.CompiledExpr
	env    *feel.Env
	inputs []string
}

// Compile parses, type-checks and lowers src into a reusable form. vars declares
// the variable names the expression is allowed to reference; referencing a name
// not in vars is a compile error, which is how deploy-time validation catches a
// script that reads an undeclared variable.
func Compile(src string, vars ...string) (*Compiled, error) {
	env := feel.NewEnv(vars...)
	fn, refs, err := feel.CompileStringRefs(src, env)
	if err != nil {
		return nil, err
	}
	return &Compiled{fn: fn, env: env, inputs: refs}, nil
}

// Inputs returns the variable names the expression actually reads (sorted,
// unique) — a subset of the names passed to Compile.
func (c *Compiled) Inputs() []string { return c.inputs }

// Eval evaluates the expression against the given variable bindings. Names in
// vars that the expression does not read are ignored; declared names absent from
// vars evaluate as FEEL null.
func (c *Compiled) Eval(vars map[string]Value) (Value, error) {
	return c.fn(c.env.NewScope(vars))
}

// Number returns a FEEL number value for an integer, for building bindings.
func Number(i int64) Value { return value.NumberFromInt64(i) }

// String returns a FEEL string value.
func String(s string) Value { return value.Str(s) }

// Bool returns a FEEL boolean value.
func Bool(b bool) Value { return value.BoolOf(b) }

// Null is the FEEL null value.
var Null = value.Null
