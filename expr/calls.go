package expr

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/feel"
	"github.com/pblumer/feel/builtins"
)

// Calls this build cannot make
// (ADR-0388).
//
// The FEEL engine compiles a call to a name it does not know into a constant
// null, and does so deliberately: DMN requires a decision to stay executable, so
// invocation is a total function and an unknown callee is a run-time null rather
// than a compile error. For a decision service that is the right answer, and it is
// the answer the TCK asks for.
//
// For a BPMN deploy it is the wrong one. Deploying is a moment where refusing
// costs nothing and helps: the model is not running yet and its author is looking
// at it. What happens without a refusal is silent in every direction — the call
// compiles, the deploy reports nothing, the arguments are not even evaluated, and
// the resulting null is indistinguishable from a null the data legitimately
// produced. Downstream it routes: a gateway condition reading that null is not
// true, so the token takes the default flow and nobody is told why.
//
// The commonest way in is not a typo but another engine's dialect. `is defined(x)`
// is a Camunda extension and one of the first things somebody arriving from there
// writes; Atlas speaks standard FEEL and has no such function.
//
// So the rules stay the engine's and the refusal is Atlas's. This reads the same
// AST the engine compiles and reports the calls that can only ever be null,
// leaving evaluation semantics untouched — a DMN decision evaluated through the
// same engine still behaves exactly as the specification requires.
//
// It errs quiet. A callee that any reading could bind to something callable — a
// function parameter, an iterator, a context key, a variable the caller declared —
// is left alone, because a false refusal blocks a model that works and a missed
// one only leaves today's behaviour in place.

// CallFault is one call an expression makes that this build can only answer with
// null. Name is the callee as written, and Message is the sentence to put in front
// of whoever wrote it.
type CallFault struct {
	Name    string
	Message string
}

// CheckCalls reports every call in src that this build cannot make: a callee no
// built-in has, and a built-in called with a number of arguments its signature
// cannot accept. declared names the variables the caller will bind, which are
// exempt — a declared variable could hold a function value, and the engine calls
// it at run time.
//
// A source that does not parse yields no faults: the syntax error is the real
// fault and the compiler reports it, so saying anything here would be a second
// message about one mistake.
func CheckCalls(src string, declared ...string) []CallFault {
	reg := builtins.Default()
	ast, err := feel.ParseWithNames(src, reg)
	if err != nil || ast == nil {
		return nil
	}
	c := &callCheck{reg: reg, bound: map[string]bool{"item": true}}
	for _, name := range declared {
		c.bound[name] = true
	}
	// Two passes: everything a name could be bound by is collected first, so a
	// call written above the binding that reaches it is judged the same as one
	// written below. The scope this builds is wider than FEEL's real one, which
	// is the direction to be wrong in.
	c.collectBindings(ast)
	c.walk(ast)
	sort.Slice(c.faults, func(i, j int) bool { return c.faults[i].Name < c.faults[j].Name })
	return c.faults
}

// CheckCallsError is CheckCalls as one error, or nil when there is nothing to
// say — the shape a compile step wants.
func CheckCallsError(src string, declared ...string) error {
	faults := CheckCalls(src, declared...)
	if len(faults) == 0 {
		return nil
	}
	msgs := make([]string, len(faults))
	for i, f := range faults {
		msgs[i] = f.Message
	}
	return fmt.Errorf("%s", strings.Join(msgs, " "))
}

type callCheck struct {
	reg    *builtins.Registry
	bound  map[string]bool
	faults []CallFault
	seen   map[string]bool
}

// standardEquivalent maps a name from another engine's FEEL dialect to how the
// same thing is said in the standard one. It is deliberately a message rather
// than an implementation: a foreign name carried over with almost its original
// semantics is worse than no name at all, because it computes something else
// quietly. Naming the equivalent teaches the dialect this build actually speaks.
var standardEquivalent = map[string]string{
	"is defined": "write `x != null` — a variable that was never set reads as null here, so the two questions are one",
	"is blank":   "write `x = null or x = \"\"`",
	"put":        "the standard name is `context put(context, key, value)`, which this build has",
	"put all":    "the standard name is `context merge([a, b])`, which this build has",
	"trim":       "write `replace(x, \"^\\s+|\\s+$\", \"\")`",
	"extract":    "use `matches` or `replace` with a regular expression",
	"assert":     "there is no equivalent: a FEEL expression answers with a value, it does not raise",
}

func (c *callCheck) report(name, message string) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[name] {
		return // one sentence per name, however many times it is called
	}
	c.seen[name] = true
	c.faults = append(c.faults, CallFault{Name: name, Message: message})
}

func (c *callCheck) checkCall(n *feel.CallExpr) {
	name, ok := n.Fn.(*feel.NameRef)
	if !ok {
		return // the callee is an expression; what it evaluates to is a run-time fact
	}
	if c.bound[name.Name] {
		return
	}
	b, known := c.reg.Lookup(name.Name)
	if !known {
		if hint, ok := standardEquivalent[name.Name]; ok {
			c.report(name.Name, fmt.Sprintf(
				"%q is not a FEEL function — it belongs to another engine's dialect, and this build speaks the standard one, "+
					"so the call can only ever be null: %s", name.Name, hint))
			return
		}
		c.report(name.Name, fmt.Sprintf(
			"%q is not a function this build has, so the call can only ever be null; check the spelling against the FEEL built-ins",
			name.Name))
		return
	}
	c.checkArity(b, n)
}

// checkArity mirrors the engine's own binding rule for a positional call: fewer
// arguments than the signature needs, or more than it takes, and the call is a
// null. A named call is left alone — an overloaded built-in binds against
// whichever signature covers the names given, and second-guessing that here would
// be the second copy of a rule.
func (c *callCheck) checkArity(b *builtins.Builtin, n *feel.CallExpr) {
	for _, a := range n.Args {
		if a.Name != "" {
			return
		}
	}
	count := len(n.Args)
	if count >= b.MinArgs && (b.Variadic() || count <= b.MaxArgs) {
		return
	}
	c.report(b.Name, fmt.Sprintf(
		"%q %s and is called with %s, so the call can only ever be null",
		b.Name, wants(b), plural(count, "argument")))
}

func wants(b *builtins.Builtin) string {
	switch {
	case b.Variadic() && b.MinArgs == 0:
		return "takes any number of arguments"
	case b.Variadic():
		return "takes at least " + plural(b.MinArgs, "argument")
	case b.MinArgs == b.MaxArgs:
		return "takes " + plural(b.MinArgs, "argument")
	default:
		return fmt.Sprintf("takes %d to %s", b.MinArgs, plural(b.MaxArgs, "argument"))
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// collectBindings gathers every name the expression itself introduces. A call to
// one of them is not judged here: the engine resolves it against the scope at run
// time, and it can legitimately hold a function.
func (c *callCheck) collectBindings(e feel.Expr) {
	switch n := e.(type) {
	case *feel.FunctionDefExpr:
		for _, p := range n.Params {
			c.bound[p.Name] = true
		}
		c.collectBindings(n.Body)
	case *feel.ForExpr:
		for _, it := range n.Iterators {
			c.bound[it.Name] = true
			c.collectBindings(it.In)
		}
		c.collectBindings(n.Return)
	case *feel.QuantifiedExpr:
		for _, it := range n.Iterators {
			c.bound[it.Name] = true
			c.collectBindings(it.In)
		}
		c.collectBindings(n.Satisfies)
	case *feel.ContextLit:
		for _, entry := range n.Entries {
			c.bound[entry.Key] = true
			c.collectBindings(entry.Value)
		}
	default:
		c.each(e, c.collectBindings)
	}
}

func (c *callCheck) walk(e feel.Expr) {
	if call, ok := e.(*feel.CallExpr); ok {
		c.checkCall(call)
	}
	c.each(e, c.walk)
}

// each applies fn to every child expression of e. A node type not named here has
// no children to descend into; a node type added to the AST later is a gap that
// makes this quieter, never louder.
func (c *callCheck) each(e feel.Expr, fn func(feel.Expr)) {
	visit := func(children ...feel.Expr) {
		for _, child := range children {
			if child != nil {
				fn(child)
			}
		}
	}
	switch n := e.(type) {
	case *feel.ListLit:
		visit(n.Elements...)
	case *feel.ContextLit:
		for _, entry := range n.Entries {
			visit(entry.Value)
		}
	case *feel.IntervalLit:
		visit(n.Low, n.High)
	case *feel.UnaryExpr:
		visit(n.X)
	case *feel.BinaryExpr:
		visit(n.X, n.Y)
	case *feel.BetweenExpr:
		visit(n.X, n.Low, n.High)
	case *feel.InExpr:
		visit(n.X)
		visit(n.Tests...)
	case *feel.CmpTest:
		visit(n.Y)
	case *feel.InstanceOfExpr:
		visit(n.X)
	case *feel.IfExpr:
		visit(n.Cond, n.Then, n.Else)
	case *feel.ForExpr:
		for _, it := range n.Iterators {
			visit(it.In)
		}
		visit(n.Return)
	case *feel.QuantifiedExpr:
		for _, it := range n.Iterators {
			visit(it.In)
		}
		visit(n.Satisfies)
	case *feel.PathExpr:
		visit(n.X)
	case *feel.FilterExpr:
		visit(n.X, n.Filter)
	case *feel.CallExpr:
		visit(n.Fn)
		for _, a := range n.Args {
			visit(a.Value)
		}
	case *feel.FunctionDefExpr:
		visit(n.Body)
	}
}
