// Package feelers renders the template language Atlas's forms already speak.
//
// form-js's Text and HTML views evaluate their content with feelers
// (bpmn-io/feelers 3.1.0, bundled in api/web/vendor/form-js): text with {{ FEEL }}
// placeholders, {{#if FEEL}}…{{/if}} and {{#loop FEEL}}…{{/loop}} sections. A mail
// text is written in the same language (ADR-0487) and is rendered on
// the server, so this package implements it in Go: parsed, and every FEEL block
// compiled, once when the model is deployed (I5); rendered when the mail is sent.
//
// It follows feelers wherever feelers decides, so a template reads the same in a form
// and in a mail:
//
//   - a placeholder renders FEEL's string() of its value, and null as nothing;
//   - {{#if}} renders its section when the value is truthy as JavaScript has it —
//     false, null, 0 and "" are not, everything else is, an empty list included;
//   - {{#loop}} renders its section once per list element, not at all for null and
//     once for any other single value. Inside, the element's fields are names of
//     their own, `this` is the element and `parent` the enclosing context; the
//     variables outside the loop are reached through `parent`, as in feelers;
//   - an opening tag swallows the line break directly after it, and so does a
//     closing tag, which puts one back after a rendered section that did not end
//     with one. That is what lets a section's tags stand on lines of their own.
//
// Where feelers is lenient because a form must show whatever it is given, this
// package is strict when it compiles: an unclosed {{, a section without its end, an
// end without its section and FEEL that does not compile are refused when the model
// is deployed, rather than rendered as a warning sign in a mail that has already
// gone out. When it renders it is as lenient as the rest of Atlas's FEEL: a
// placeholder whose expression fails renders as nothing, and is reported.
//
// A leading '=' is not this package's business. In Atlas it makes a whole value one
// FEEL expression, which the caller decides before it considers a template.
package feelers

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pblumer/atlas/expr"
)

type nodeKind uint8

const (
	nodeText nodeKind = iota
	nodeInsert
	nodeIf
	nodeLoop
)

// node is one piece of a template: literal text, a placeholder, or a section with
// the nodes it repeats or guards.
type node struct {
	kind nodeKind
	// text is the literal of a text node and the FEEL source of every other kind.
	text string
	expr *expr.Compiled
	body []node
	// closeNl records that the section's closing tag swallowed a line break.
	closeNl bool
}

// Template is a parsed template with its FEEL compiled. It is immutable and safe for
// concurrent rendering, like the compiled process that holds it.
type Template struct {
	src   string
	nodes []node
}

// Source returns the template as it was written.
func (t *Template) Source() string { return t.src }

// Inputs returns the variable names the template's FEEL reads, sorted and unique — what
// [expr.Compiled.Inputs] answers for one expression, over every placeholder and section.
// A name read only inside a loop may be a field of the loop's item rather than a
// variable; a caller asking whether a variable is read gets a yes for it either way.
func (t *Template) Inputs() []string {
	seen := map[string]bool{}
	var walk func(ns []node)
	walk = func(ns []node) {
		for i := range ns {
			if ns[i].expr != nil {
				for _, n := range ns[i].expr.Inputs() {
					seen[n] = true
				}
			}
			walk(ns[i].body)
		}
	}
	walk(t.nodes)
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// CompileFunc compiles one FEEL block. The compiler hands in its deploy gate, so the
// FEEL in a template is held to the rules every other FEEL in a model is (ADR-0388).
type CompileFunc func(src string) (*expr.Compiled, error)

// IsTemplate reports whether src uses the template language at all — whether it has
// a {{ in it. A value without one is plain text and needs no template.
func IsTemplate(src string) bool { return strings.Contains(src, "{{") }

// Error is a template that does not parse, or whose FEEL does not compile, with the
// place of the tag at fault: Line and Column count from 1, the column in characters.
type Error struct {
	Line, Column int
	Msg          string
	Err          error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("line %d, column %d: %s: %v", e.Line, e.Column, e.Msg, e.Err)
	}
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

// The tags, spelled as feelers' grammar (lezer-feelers 1.0.0) spells them. An opening
// tag's keyword is followed by exactly one space: {{#if}} is not a section in feelers
// either, and is refused below with a message saying so.
const (
	openIf    = "{{#if "
	openLoop  = "{{#loop "
	closeIf   = "{{/if}}"
	closeLoop = "{{/loop}}"
)

// Compile parses src and compiles each of its FEEL blocks with compile.
func Compile(src string, compile CompileFunc) (*Template, error) {
	p := &parser{src: src, compile: compile}
	nodes, _, err := p.scope("", 0)
	if err != nil {
		return nil, err
	}
	return &Template{src: src, nodes: nodes}, nil
}

type parser struct {
	src     string
	pos     int
	compile CompileFunc
}

// errAt builds an Error for the tag starting at byte offset at.
func (p *parser) errAt(at int, err error, format string, args ...any) error {
	before := p.src[:at]
	line := strings.Count(before, "\n") + 1
	col := utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1
	return &Error{Line: line, Column: col, Msg: fmt.Sprintf(format, args...), Err: err}
}

// scope parses nodes until the closing tag want, or to the end of the input when want
// is empty. opened is where the section being closed began, for the message when its
// end never comes. It answers whether the closing tag swallowed a line break.
func (p *parser) scope(want string, opened int) ([]node, bool, error) {
	var nodes []node
	for p.pos < len(p.src) {
		i := strings.Index(p.src[p.pos:], "{{")
		if i < 0 {
			nodes = append(nodes, node{kind: nodeText, text: p.src[p.pos:]})
			p.pos = len(p.src)
			break
		}
		if i > 0 {
			nodes = append(nodes, node{kind: nodeText, text: p.src[p.pos : p.pos+i]})
			p.pos += i
		}
		at := p.pos
		rest := p.src[at:]
		switch {
		case strings.HasPrefix(rest, closeIf), strings.HasPrefix(rest, closeLoop):
			tag := closeIf
			if strings.HasPrefix(rest, closeLoop) {
				tag = closeLoop
			}
			if tag != want {
				if want == "" {
					return nil, false, p.errAt(at, nil, "%s has no section to close", tag)
				}
				return nil, false, p.errAt(at, nil, "%s closes a section that %s should close", tag, want)
			}
			p.pos += len(tag)
			return nodes, p.newline(), nil
		case strings.HasPrefix(rest, openIf), strings.HasPrefix(rest, openLoop):
			kind, open, close, what := nodeIf, openIf, closeIf, "a condition"
			if strings.HasPrefix(rest, openLoop) {
				kind, open, close, what = nodeLoop, openLoop, closeLoop, "a list"
			}
			src, err := p.block(at, at+len(open))
			if err != nil {
				return nil, false, err
			}
			if strings.TrimSpace(src) == "" {
				return nil, false, p.errAt(at, nil, "%s}} needs %s", strings.TrimSpace(open), what)
			}
			compiled, err := p.compile(src)
			if err != nil {
				return nil, false, p.errAt(at, err, "%s%s}}", open, src)
			}
			p.newline()
			body, nl, err := p.scope(close, at)
			if err != nil {
				return nil, false, err
			}
			nodes = append(nodes, node{kind: kind, text: src, expr: compiled, body: body, closeNl: nl})
		default:
			start := at + 2
			if start < len(p.src) && p.src[start] == '=' {
				start++
			}
			src, err := p.block(at, start)
			if err != nil {
				return nil, false, err
			}
			trimmed := strings.TrimSpace(src)
			if trimmed == "" {
				continue // {{}} renders nothing, in feelers too
			}
			if trimmed[0] == '#' || trimmed[0] == '/' {
				return nil, false, p.errAt(at, nil,
					"{{%s}} is not a section this language knows: a section opens with {{#if condition}} or {{#loop list}} and closes with {{/if}} or {{/loop}}", trimmed)
			}
			compiled, err := p.compile(src)
			if err != nil {
				return nil, false, p.errAt(at, err, "{{%s}}", src)
			}
			nodes = append(nodes, node{kind: nodeInsert, text: src, expr: compiled})
		}
	}
	if want != "" {
		return nil, false, p.errAt(opened, nil, "this section is never closed with %s", want)
	}
	return nodes, false, nil
}

// block reads a FEEL block from from up to the next }} and moves past it. A block has
// no nesting: like feelers, the first }} ends it, wherever it stands.
func (p *parser) block(at, from int) (string, error) {
	end := strings.Index(p.src[from:], "}}")
	if end < 0 {
		return "", p.errAt(at, nil, "this {{ is never closed with }}")
	}
	p.pos = from + end + 2
	return p.src[from : from+end], nil
}

// newline swallows one line break directly at the cursor, reporting whether there was
// one. Only "\n": feelers' grammar names no other, so a "\r\n" stays in the text.
func (p *parser) newline() bool {
	if p.pos < len(p.src) && p.src[p.pos] == '\n' {
		p.pos++
		return true
	}
	return false
}

// Scope is what a template's names resolve against at its top level: the variables
// the task sees.
type Scope interface {
	// Lookup answers a variable's value, and whether it is set.
	Lookup(name string) (expr.Value, bool)
	// Names lists every variable. It is read only for `this` at the top level, which
	// names the whole scope as one context, as it does in feelers.
	Names() []string
}

// Vars is a [Scope] over FEEL values already in hand.
type Vars map[string]expr.Value

// Lookup implements [Scope].
func (v Vars) Lookup(name string) (expr.Value, bool) {
	x, ok := v[name]
	return x, ok
}

// Names implements [Scope].
func (v Vars) Names() []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	return out
}

// Problem is a placeholder or section that rendered as nothing: its expression failed,
// or answered null where a value was wanted. A mail goes out regardless, as one with a
// failed FEEL field always has; a preview shows the problems beside the result.
type Problem struct {
	Expression string `json:"expression"`
	Message    string `json:"message"`
}

// Render renders the template against scope. escape, when set, is applied to every
// value a placeholder inserts and never to the template's own text — an HTML body
// passes one, so a variable cannot add markup to it. Render does not fail: whatever
// cannot be evaluated renders as nothing and is listed in the problems.
func (t *Template) Render(scope Scope, escape func(string) string) (string, []Problem) {
	r := &renderer{escape: escape}
	var b strings.Builder
	r.nodes(&b, t.nodes, &frame{scope: scope})
	return b.String(), r.problems
}

type renderer struct {
	escape   func(string) string
	problems []Problem
}

func (r *renderer) report(src, msg string) {
	r.problems = append(r.problems, Problem{Expression: strings.TrimSpace(src), Message: msg})
}

func (r *renderer) nodes(b *strings.Builder, nodes []node, f *frame) {
	for i := range nodes {
		n := &nodes[i]
		switch n.kind {
		case nodeText:
			b.WriteString(n.text)
		case nodeInsert:
			v, ok := r.eval(n, f)
			if !ok {
				continue
			}
			if isNull(v) {
				r.report(n.text, "is null, so it renders as nothing")
				continue
			}
			s := text(v)
			if r.escape != nil {
				s = r.escape(s)
			}
			b.WriteString(s)
		case nodeIf:
			v, ok := r.eval(n, f)
			if !ok || !truthy(v) {
				continue
			}
			r.section(b, n, func(inner *strings.Builder) { r.nodes(inner, n.body, f) })
		case nodeLoop:
			v, ok := r.eval(n, f)
			if !ok {
				continue
			}
			r.section(b, n, func(inner *strings.Builder) {
				for _, item := range items(v) {
					r.nodes(inner, n.body, &frame{item: item, parent: f})
				}
			})
		}
	}
}

// section renders a section's body and puts back the line break its closing tag
// swallowed, unless the body already ended with one.
func (r *renderer) section(b *strings.Builder, n *node, body func(*strings.Builder)) {
	var inner strings.Builder
	body(&inner)
	s := inner.String()
	b.WriteString(s)
	if n.closeNl && !strings.HasSuffix(s, "\n") {
		b.WriteByte('\n')
	}
}

// eval evaluates a node's expression over the names it reads, reporting a failure.
// The engine answers null for nearly every runtime fault; what it reports as an error
// is a breached evaluation limit (ADR-0008).
func (r *renderer) eval(n *node, f *frame) (expr.Value, bool) {
	names := n.expr.Inputs()
	vars := make(map[string]expr.Value, len(names))
	for _, name := range names {
		if v := f.lookup(name); v != nil {
			vars[name] = v
		}
	}
	v, err := n.expr.Eval(vars)
	if err != nil {
		r.report(n.text, err.Error())
		return nil, false
	}
	return v, true
}

// frame is one level of names: the scope at the top, a loop element below it.
type frame struct {
	scope  Scope      // the top level's variables; nil inside a loop
	item   expr.Value // the loop element; nil at the top level
	parent *frame
	self   expr.Value // the top level's `this`, built once when first named
	ctx    expr.Value // this frame as `parent` names it one level down
}

// lookup resolves a name the way feelers' enhanceContext lays a level out:
// {this, parent, ...fields, _this_, _parent_} — so a field called this or parent
// shadows the keyword, and _this_ and _parent_ always mean what they say.
func (f *frame) lookup(name string) expr.Value {
	switch name {
	case "_this_":
		return f.this()
	case "_parent_":
		return f.parentValue()
	}
	if f.scope != nil {
		if v, ok := f.scope.Lookup(name); ok {
			return v
		}
	} else if v, ok := expr.ContextMember(f.item, name); ok {
		return v
	}
	switch name {
	case "this":
		return f.this()
	case "parent":
		return f.parentValue()
	}
	return nil
}

func (f *frame) this() expr.Value {
	if f.scope == nil {
		return f.item
	}
	if f.self == nil {
		m := map[string]expr.Value{}
		for _, n := range f.scope.Names() {
			if v, ok := f.scope.Lookup(n); ok {
				m[n] = v
			}
		}
		f.self = expr.ContextOf(m)
	}
	return f.self
}

func (f *frame) parentValue() expr.Value {
	if f.parent == nil {
		return expr.Null
	}
	return f.parent.context()
}

// context is the frame as one context — what `parent` names inside a loop.
func (f *frame) context() expr.Value {
	if f.ctx != nil {
		return f.ctx
	}
	m := map[string]expr.Value{"this": f.this(), "parent": f.parentValue()}
	if f.scope != nil {
		for _, n := range f.scope.Names() {
			if v, ok := f.scope.Lookup(n); ok {
				m[n] = v
			}
		}
	} else if keys, ok := expr.ContextKeys(f.item); ok {
		for _, k := range keys {
			m[k], _ = expr.ContextMember(f.item, k)
		}
	}
	m["_this_"] = f.this()
	m["_parent_"] = f.parentValue()
	f.ctx = expr.ContextOf(m)
	return f.ctx
}

// stringOf is FEEL's string(), which feelers wraps every placeholder in. Calling the
// function rather than re-implementing it keeps a list or a date rendering exactly as
// FEEL writes it.
var stringOf = mustCompile("string(v)", "v")

func mustCompile(src string, vars ...string) *expr.Compiled {
	c, err := expr.Compile(src, vars...)
	if err != nil {
		panic(err)
	}
	return c
}

func isNull(v expr.Value) bool {
	kind, _, _ := expr.Classify(v)
	return kind == expr.KindNull
}

// text renders a value as a placeholder inserts it.
func text(v expr.Value) string {
	// string() of a value is a string; there is nothing for it to fail on.
	s, _ := stringOf.Eval(map[string]expr.Value{"v": v})
	_, _, t := expr.Classify(s)
	return t
}

// truthy is JavaScript's truthiness over the value feelers would have received:
// false, null, 0 and "" are false, and everything else is true.
func truthy(v expr.Value) bool {
	kind, b, t := expr.Classify(v)
	switch kind {
	case expr.KindNull:
		return false
	case expr.KindBool:
		return b
	case expr.KindNumber:
		n, whole := expr.AsInt(v)
		return !whole || n != 0
	case expr.KindString:
		return t != ""
	default:
		return true
	}
}

// items is what a loop walks: a list's elements, nothing for null, and any other value
// as a list of one.
func items(v expr.Value) []expr.Value {
	if l, ok := expr.AsList(v); ok {
		return l
	}
	if isNull(v) {
		return nil
	}
	return []expr.Value{v}
}
