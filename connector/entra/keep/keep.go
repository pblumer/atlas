// Package keep is the Entra Worker's delta filter: the tiny predicate a delta-users or
// delta-groups task names in its keep attribute, and the decision it makes about one
// object of a delta page (ADR-0553).
//
// It exists because Graph's /users/delta and /groups/delta take no $filter beyond one
// on ids, and because a job's result is frozen into the instance's events before any
// output mapping runs (invariant I6): a model that filters what came back has already
// written every object of the tenant into the log. So the Worker filters each page
// before it answers, and only objects the predicate keeps leave it.
//
// The language is deliberately not OData and not FEEL. It is three forms and two
// connectives:
//
//	<field> eq '<literal>'
//	startswith(<field>,'<literal>')
//	<predicate> and <predicate>
//	<predicate> or <predicate>
//	( <predicate> )
//
// and binds tighter than or, as in OData. Keywords are case-insensitive (startsWith is
// startswith). A literal is single-quoted, and a quote inside it is written twice, which
// is OData's own escape:
//
//	displayName eq 'O''Brien'
//
// A field is one top-level property name; there are no paths, no lambdas, no functions
// beyond startswith, no negation.
//
// Comparison is case-insensitive. Graph compares directory strings that way in a $filter
// (department eq 'it' finds IT), and a keep that disagreed with the $filter an author
// already knows would be a second rule to remember. Only a string value can match: null,
// a number, a boolean, a list or an object compares false.
//
// The package imports nothing of Atlas, so both the compiler (which refuses a bad keep
// at deploy) and the Worker (which applies it) can use it.
package keep

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits on what a keep may be. A keep is authored by hand and read by a person; one
// that reaches these is no longer the small statement the attribute is for, and the
// bounds keep a hostile or broken one from costing the Worker anything.
const (
	MaxLength      = 1024
	MaxComparisons = 16
	MaxDepth       = 8
)

// Predicate is a parsed keep. The zero value is not usable; obtain one from [Parse].
type Predicate struct {
	root   node
	fields []string
}

// Fields lists the distinct property names the predicate reads, in the order they first
// appear. The compiler holds them against the task's $select.
func (p *Predicate) Fields() []string { return append([]string(nil), p.fields...) }

// Verdict is the decision about one object.
type Verdict int

const (
	// Drop: the object does not match.
	Drop Verdict = iota
	// Keep: the object matches.
	Keep
	// Undecided: the object lacks a property the decision needs. A delta page after
	// the first carries only what changed, so a kept user whose display name changed
	// arrives without its department. Undecided is never kept — keeping it would let
	// a stranger's change out of the Worker — but it is counted separately, because
	// it is the one way this filter can lose a change a process wanted.
	Undecided
)

// Decide evaluates the predicate over one object. A property that is absent from the
// object is unknown (not the same as null, which is a value and compares false), and
// and/or follow Kleene's three-valued logic: false and unknown is false, true or
// unknown is true, everything else involving unknown is unknown.
//
// The property is looked up by its exact name first and then case-insensitively, since
// $select is case-insensitive and Graph answers with the property's canonical spelling.
func (p *Predicate) Decide(obj map[string]any) Verdict {
	switch p.root.eval(obj) {
	case tTrue:
		return Keep
	case tFalse:
		return Drop
	default:
		return Undecided
	}
}

// Parse reads a keep. The error names what is wrong and where, in words an author can
// act on at deploy.
func Parse(src string) (*Predicate, error) {
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("keep is empty")
	}
	if len(src) > MaxLength {
		return nil, fmt.Errorf("keep is %d bytes long, above the %d a keep may be", len(src), MaxLength)
	}
	if !utf8.ValidString(src) {
		return nil, errors.New("keep is not valid UTF-8")
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	ps := &parser{toks: toks}
	root, err := ps.or(0)
	if err != nil {
		return nil, err
	}
	if t := ps.peek(); t.kind != tokEOF {
		return nil, fmt.Errorf("keep has %s at offset %d where the predicate should have ended (combine predicates with and / or)", t.describe(), t.pos)
	}
	return &Predicate{root: root, fields: ps.fields}, nil
}

// ternary is a three-valued truth value.
type ternary int

const (
	tFalse ternary = iota
	tTrue
	tUnknown
)

type node interface{ eval(map[string]any) ternary }

type cmpOp int

const (
	opEq cmpOp = iota
	opStartsWith
)

type cmp struct {
	op      cmpOp
	field   string
	literal string // folded to lower case at parse
}

func (c cmp) eval(obj map[string]any) ternary {
	v, present := lookup(obj, c.field)
	if !present {
		return tUnknown
	}
	s, ok := v.(string)
	if !ok {
		return tFalse
	}
	s = strings.ToLower(s)
	switch c.op {
	case opEq:
		return truth(s == c.literal)
	default:
		return truth(strings.HasPrefix(s, c.literal))
	}
}

type and struct{ l, r node }

func (a and) eval(obj map[string]any) ternary {
	l := a.l.eval(obj)
	if l == tFalse {
		return tFalse
	}
	r := a.r.eval(obj)
	switch {
	case r == tFalse:
		return tFalse
	case l == tTrue && r == tTrue:
		return tTrue
	default:
		return tUnknown
	}
}

type or struct{ l, r node }

func (o or) eval(obj map[string]any) ternary {
	l := o.l.eval(obj)
	if l == tTrue {
		return tTrue
	}
	r := o.r.eval(obj)
	switch {
	case r == tTrue:
		return tTrue
	case l == tFalse && r == tFalse:
		return tFalse
	default:
		return tUnknown
	}
}

func truth(b bool) ternary {
	if b {
		return tTrue
	}
	return tFalse
}

func lookup(obj map[string]any, field string) (any, bool) {
	if v, ok := obj[field]; ok {
		return v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

// --- lexer ---

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokString
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokKind
	text string // identifier as written, or the unescaped literal
	pos  int
}

func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "the end"
	case tokIdent:
		return fmt.Sprintf("%q", t.text)
	case tokString:
		return fmt.Sprintf("the literal '%s'", t.text)
	case tokLParen:
		return `"("`
	case tokRParen:
		return `")"`
	default:
		return `","`
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

func lex(src string) ([]token, error) {
	var out []token
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{kind: tokLParen, pos: i})
			i++
		case c == ')':
			out = append(out, token{kind: tokRParen, pos: i})
			i++
		case c == ',':
			out = append(out, token{kind: tokComma, pos: i})
			i++
		case c == '\'':
			start := i
			var b strings.Builder
			i++
			closed := false
			for i < len(src) {
				if src[i] == '\'' {
					if i+1 < len(src) && src[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				b.WriteByte(src[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("keep has a literal at offset %d that is never closed (a quote inside a literal is written twice: 'O''Brien')", start)
			}
			out = append(out, token{kind: tokString, text: b.String(), pos: start})
		case isIdentStart(c):
			start := i
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			out = append(out, token{kind: tokIdent, text: src[start:i], pos: start})
		case c == '"':
			return nil, fmt.Errorf("keep has a double quote at offset %d; a literal is single-quoted, as in OData: department eq 'IT'", i)
		default:
			return nil, fmt.Errorf("keep has %q at offset %d, which is not part of the language (a field, eq, startswith(...), and, or, parentheses and single-quoted literals)", rune(c), i)
		}
	}
	out = append(out, token{kind: tokEOF, pos: len(src)})
	return out, nil
}

// --- parser ---

type parser struct {
	toks   []token
	i      int
	cmps   int
	fields []string
}

func (p *parser) peek() token { return p.toks[p.i] }

func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tokEOF {
		p.i++
	}
	return t
}

func (p *parser) keyword(t token, kw string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) or(depth int) (node, error) {
	l, err := p.and(depth)
	if err != nil {
		return nil, err
	}
	for p.keyword(p.peek(), "or") {
		p.next()
		r, err := p.and(depth)
		if err != nil {
			return nil, err
		}
		l = or{l, r}
	}
	return l, nil
}

func (p *parser) and(depth int) (node, error) {
	l, err := p.primary(depth)
	if err != nil {
		return nil, err
	}
	for p.keyword(p.peek(), "and") {
		p.next()
		r, err := p.primary(depth)
		if err != nil {
			return nil, err
		}
		l = and{l, r}
	}
	return l, nil
}

// unsupported are words an author coming from OData is likely to try. Naming them
// turns "unexpected token" into the sentence the author needs.
var unsupported = map[string]string{
	"ne": "an operator", "gt": "an operator", "ge": "an operator", "lt": "an operator", "le": "an operator",
	"in": "an operator", "has": "an operator", "not": "negation",
	"endswith": "a function", "contains": "a function", "tolower": "a function", "toupper": "a function",
	"any": "a lambda", "all": "a lambda",
}

func (p *parser) primary(depth int) (node, error) {
	t := p.next()
	switch {
	case t.kind == tokLParen:
		if depth+1 > MaxDepth {
			return nil, fmt.Errorf("keep nests parentheses deeper than %d at offset %d", MaxDepth, t.pos)
		}
		n, err := p.or(depth + 1)
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tokRParen {
			return nil, fmt.Errorf("keep has %s at offset %d where \")\" should close the parenthesis opened at offset %d", c.describe(), c.pos, t.pos)
		}
		return n, nil
	case p.keyword(t, "startswith"):
		if o := p.next(); o.kind != tokLParen {
			return nil, fmt.Errorf("keep has startswith at offset %d without its argument list: startswith(displayName,'prefix')", t.pos)
		}
		field, err := p.field(p.next())
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tokComma {
			return nil, fmt.Errorf("keep has %s at offset %d where startswith wants a comma between the field and the literal", c.describe(), c.pos)
		}
		lit, err := p.literal(p.next())
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tokRParen {
			return nil, fmt.Errorf("keep has %s at offset %d where startswith's \")\" should be", c.describe(), c.pos)
		}
		return p.comparison(opStartsWith, field, lit, t.pos)
	case t.kind == tokIdent:
		if what, ok := unsupported[strings.ToLower(t.text)]; ok {
			return nil, fmt.Errorf("keep uses %q at offset %d, %s the keep language does not have (it has eq, startswith, and, or)", t.text, t.pos, what)
		}
		field, err := p.field(t)
		if err != nil {
			return nil, err
		}
		op := p.next()
		if !p.keyword(op, "eq") {
			if op.kind == tokIdent {
				if what, ok := unsupported[strings.ToLower(op.text)]; ok {
					return nil, fmt.Errorf("keep uses %q at offset %d, %s the keep language does not have (it has eq, startswith, and, or)", op.text, op.pos, what)
				}
				return nil, fmt.Errorf("keep has the unknown operator %q at offset %d (the only comparison is eq; a prefix is startswith(field,'…'))", op.text, op.pos)
			}
			return nil, fmt.Errorf("keep has %s at offset %d where an operator should follow %q (field eq 'literal')", op.describe(), op.pos, field)
		}
		lit, err := p.literal(p.next())
		if err != nil {
			return nil, err
		}
		return p.comparison(opEq, field, lit, t.pos)
	default:
		return nil, fmt.Errorf("keep has %s at offset %d where a comparison should start (field eq 'literal' or startswith(field,'literal'))", t.describe(), t.pos)
	}
}

func (p *parser) field(t token) (string, error) {
	if t.kind != tokIdent {
		return "", fmt.Errorf("keep has %s at offset %d where a field name should be", t.describe(), t.pos)
	}
	for _, kw := range []string{"and", "or", "eq", "startswith"} {
		if strings.EqualFold(t.text, kw) {
			return "", fmt.Errorf("keep has %q at offset %d where a field name should be", t.text, t.pos)
		}
	}
	return t.text, nil
}

func (p *parser) literal(t token) (string, error) {
	if t.kind != tokString {
		return "", fmt.Errorf("keep has %s at offset %d where a single-quoted literal should be ('…'); the literal is never a FEEL expression or a variable", t.describe(), t.pos)
	}
	if t.text == "" {
		return "", fmt.Errorf("keep has an empty literal at offset %d: eq '' matches nothing Graph sends (an unset property is null), and startswith(…,'') would keep every object", t.pos)
	}
	return t.text, nil
}

func (p *parser) comparison(op cmpOp, field, lit string, pos int) (node, error) {
	p.cmps++
	if p.cmps > MaxComparisons {
		return nil, fmt.Errorf("keep has more than %d comparisons (at offset %d)", MaxComparisons, pos)
	}
	seen := false
	for _, f := range p.fields {
		if strings.EqualFold(f, field) {
			seen = true
			break
		}
	}
	if !seen {
		p.fields = append(p.fields, field)
	}
	return cmp{op: op, field: field, literal: strings.ToLower(lit)}, nil
}
