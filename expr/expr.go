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
	"strings"

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

// CompileAuto compiles src while discovering the variable names it references:
// it starts with no declared variables and, each time the FEEL compiler reports
// an "unknown variable", declares that name and retries, until the expression
// compiles. This lets a script author reference process variables without
// declaring them up front, while still letting the compiler — not us — decide
// what is a genuine free variable (bound loop/quantifier names, path members and
// built-ins never surface as unknown). A syntax or type error (not a missing
// declaration) is returned as-is.
func CompileAuto(src string) (*Compiled, error) {
	var declared []string
	seen := map[string]bool{}
	for {
		env := feel.NewEnv(declared...)
		fn, refs, err := feel.CompileStringRefs(src, env)
		if err == nil {
			return &Compiled{fn: fn, env: env, inputs: refs}, nil
		}
		name, ok := unknownVariable(err)
		if !ok || seen[name] {
			return nil, err // a real error, or we already declared it (no progress)
		}
		seen[name] = true
		declared = append(declared, name)
	}
}

// unknownVariable extracts the name from a FEEL "unknown variable %q" compile
// error, reporting whether the error was of that shape.
func unknownVariable(err error) (string, bool) {
	const marker = `unknown variable "`
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
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

// ValueKind classifies a FEEL value into the scalar subset Atlas persists today.
type ValueKind uint8

const (
	KindNull ValueKind = iota
	KindBool
	KindNumber
	KindString
)

// Classify reduces a FEEL value to a storable (kind, bool, text) triple: text is
// the number's canonical decimal string or the string's contents. Non-scalar
// values (lists, contexts, temporals) are rendered to their canonical FEEL text
// and stored as a string — lossy but stable — until Atlas models them natively.
func Classify(v Value) (ValueKind, bool, string) {
	if value.IsNull(v) {
		return KindNull, false, ""
	}
	switch v.Kind() {
	case value.KindBool:
		return KindBool, bool(v.(value.Bool)), ""
	case value.KindNumber:
		return KindNumber, false, v.String()
	case value.KindString:
		return KindString, false, v.String()
	default:
		return KindString, false, v.String()
	}
}

// FromStored reconstructs a FEEL value from Atlas's stored scalar form — the
// inverse of Classify — for binding variables into an evaluation. An unparseable
// number becomes null.
func FromStored(kind ValueKind, b bool, text string) Value {
	switch kind {
	case KindBool:
		return value.BoolOf(b)
	case KindNumber:
		n, err := value.ParseNumber(text)
		if err != nil {
			return value.Null
		}
		return n
	case KindString:
		return value.Str(text)
	default:
		return value.Null
	}
}

// Number returns a FEEL number value for an integer, for building bindings.
func Number(i int64) Value { return value.NumberFromInt64(i) }

// String returns a FEEL string value.
func String(s string) Value { return value.Str(s) }

// Bool returns a FEEL boolean value.
func Bool(b bool) Value { return value.BoolOf(b) }

// Null is the FEEL null value.
var Null = value.Null
