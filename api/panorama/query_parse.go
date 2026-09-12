package panorama

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type queryPlan struct {
	pathAlias string
	start     nodePattern
	edge      *edgePattern
	end       *nodePattern
	where     expr
	returns   []string
	order     *orderSpec
	limit     int
}

type nodePattern struct {
	alias string
	kind  string
	pos   int
}

type edgePattern struct {
	alias string
	kind  string
	min   int
	max   int
	pos   int
}

type orderSpec struct {
	ref  propertyRef
	desc bool
}

type propertyRef struct {
	alias    string
	property string
	pos      int
}

type expr interface {
	eval(Graph, match, map[string]any, string) (bool, error)
	validate(map[string]string, map[string]any, string) error
}

type boolExpr struct {
	op          string
	left, right expr
	pos         int
}

type notExpr struct{ inner expr }

type compareExpr struct {
	ref   propertyRef
	op    string
	value queryValue
	pos   int
}

type queryValue struct {
	literal any
	param   string
	pos     int
}

func parseGraphQuery(source string, limits QueryLimits) (queryPlan, error) {
	if strings.TrimSpace(source) == "" {
		return queryPlan{}, newQueryError("syntax", "query is empty", 0, source)
	}
	upper := strings.ToUpper(source)
	padded := " " + upper + " "
	for _, forbidden := range []string{
		" CREATE ", " INSERT ", " MERGE ", " DELETE ", " SET ", " REMOVE ",
		" CALL ", "LOAD CSV", " TRANSACTION ", " DROP ", " ALTER ", " COUNT(",
	} {
		if idx := strings.Index(padded, forbidden); idx >= 0 {
			pos := idx
			if pos > 0 {
				pos--
			}
			return queryPlan{}, newQueryError("unsupported", "unsupported or mutating construct", pos, source)
		}
	}
	tokens, err := lexGraphQuery(source, limits.MaxTokens)
	if err != nil {
		return queryPlan{}, err
	}
	p := queryParser{source: source, tokens: tokens}
	return p.parse()
}

func validatePlan(p queryPlan, params map[string]any, source string, limits QueryLimits) error {
	aliases := map[string]string{p.start.alias: p.start.kind}
	if p.end != nil {
		if _, exists := aliases[p.end.alias]; exists {
			return newQueryError("duplicate_alias", "duplicate node alias "+p.end.alias, p.end.pos, source)
		}
		aliases[p.end.alias] = p.end.kind
	}
	if p.pathAlias != "" {
		if _, exists := aliases[p.pathAlias]; exists {
			return newQueryError("duplicate_alias", "path alias conflicts with node alias "+p.pathAlias, 0, source)
		}
	}
	for alias, kind := range aliases {
		if alias == "" {
			return newQueryError("missing_alias", "every node in Panorama Graph Query v1 needs an alias", 0, source)
		}
		if kind != "" && !knownNodeKind(kind) {
			return newQueryError("unknown_node_kind", fmt.Sprintf("unknown node kind %q", kind), 0, source)
		}
	}
	if p.edge != nil {
		if p.edge.kind != "" && !knownEdgeKind(p.edge.kind) {
			return newQueryError("unknown_relationship_kind", fmt.Sprintf("unknown relationship kind %q", p.edge.kind), p.edge.pos, source)
		}
		if p.edge.min < 1 || p.edge.max < p.edge.min || p.edge.max > limits.MaxPathDepth {
			return newQueryError("path_depth", fmt.Sprintf("path depth must be bounded between 1 and %d", limits.MaxPathDepth), p.edge.pos, source)
		}
	}
	if p.where != nil {
		if err := p.where.validate(aliases, params, source); err != nil {
			return err
		}
	}
	validReturn := func(v string) bool {
		if v == p.pathAlias && p.pathAlias != "" {
			return true
		}
		if p.edge != nil && v == p.edge.alias && p.edge.alias != "" {
			return true
		}
		_, ok := aliases[v]
		return ok
	}
	for _, v := range p.returns {
		if !validReturn(v) {
			return newQueryError("unknown_return", fmt.Sprintf("RETURN references unknown variable %q", v), 0, source)
		}
	}
	if p.order != nil {
		if err := validatePropertyRef(p.order.ref, aliases, source); err != nil {
			return err
		}
	}
	return nil
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokParam
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
	tokColon
	tokComma
	tokDot
	tokStar
	tokDash
	tokArrow
	tokRange
	tokEq
	tokNE
	tokLT
	tokLE
	tokGT
	tokGE
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

func lexGraphQuery(source string, max int) ([]token, error) {
	out := make([]token, 0, 64)
	for i := 0; i < len(source); {
		if unicode.IsSpace(rune(source[i])) {
			i++
			continue
		}
		pos := i
		switch c := source[i]; c {
		case '(':
			out = append(out, token{kind: tokLParen, text: "(", pos: i})
			i++
		case ')':
			out = append(out, token{kind: tokRParen, text: ")", pos: i})
			i++
		case '[':
			out = append(out, token{kind: tokLBracket, text: "[", pos: i})
			i++
		case ']':
			out = append(out, token{kind: tokRBracket, text: "]", pos: i})
			i++
		case ':':
			out = append(out, token{kind: tokColon, text: ":", pos: i})
			i++
		case ',':
			out = append(out, token{kind: tokComma, text: ",", pos: i})
			i++
		case '*':
			out = append(out, token{kind: tokStar, text: "*", pos: i})
			i++
		case '-':
			if i+1 < len(source) && source[i+1] == '>' {
				out = append(out, token{kind: tokArrow, text: "->", pos: i})
				i += 2
			} else {
				out = append(out, token{kind: tokDash, text: "-", pos: i})
				i++
			}
		case '.':
			if i+1 < len(source) && source[i+1] == '.' {
				out = append(out, token{kind: tokRange, text: "..", pos: i})
				i += 2
			} else {
				out = append(out, token{kind: tokDot, text: ".", pos: i})
				i++
			}
		case '=':
			out = append(out, token{kind: tokEq, text: "=", pos: i})
			i++
		case '!':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokNE, text: "!=", pos: i})
				i += 2
			} else {
				return nil, newQueryError("syntax", "expected !=", pos, source)
			}
		case '<':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokLE, text: "<=", pos: i})
				i += 2
			} else if i+1 < len(source) && source[i+1] == '>' {
				out = append(out, token{kind: tokNE, text: "<>", pos: i})
				i += 2
			} else {
				out = append(out, token{kind: tokLT, text: "<", pos: i})
				i++
			}
		case '>':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokGE, text: ">=", pos: i})
				i += 2
			} else {
				out = append(out, token{kind: tokGT, text: ">", pos: i})
				i++
			}
		case '$':
			i++
			start := i
			for i < len(source) && isIdentPart(source[i]) {
				i++
			}
			if start == i {
				return nil, newQueryError("syntax", "parameter needs a name", pos, source)
			}
			out = append(out, token{kind: tokParam, text: source[start:i], pos: pos})
		case '\'', '"':
			quote := c
			i++
			var b strings.Builder
			closed := false
			for i < len(source) {
				if source[i] == quote {
					i++
					closed = true
					break
				}
				if source[i] == '\\' && i+1 < len(source) {
					i++
					switch source[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(source[i])
					}
					i++
					continue
				}
				b.WriteByte(source[i])
				i++
			}
			if !closed {
				return nil, newQueryError("syntax", "unterminated string", pos, source)
			}
			out = append(out, token{kind: tokString, text: b.String(), pos: pos})
		default:
			if isIdentStart(c) {
				i++
				for i < len(source) && isIdentPart(source[i]) {
					i++
				}
				out = append(out, token{kind: tokIdent, text: source[pos:i], pos: pos})
			} else if c >= '0' && c <= '9' {
				i++
				for i < len(source) && source[i] >= '0' && source[i] <= '9' {
					i++
				}
				if i < len(source) && source[i] == '.' && (i+1 >= len(source) || source[i+1] != '.') {
					i++
					for i < len(source) && source[i] >= '0' && source[i] <= '9' {
						i++
					}
				}
				out = append(out, token{kind: tokNumber, text: source[pos:i], pos: pos})
			} else {
				return nil, newQueryError("syntax", fmt.Sprintf("unexpected character %q", c), pos, source)
			}
		}
		if max > 0 && len(out) > max {
			return nil, newQueryError("token_limit", "query exceeds maximum token count", pos, source)
		}
	}
	out = append(out, token{kind: tokEOF, pos: len(source)})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

type queryParser struct {
	source string
	tokens []token
	i      int
}

func (p *queryParser) parse() (queryPlan, error) {
	plan := queryPlan{limit: -1}
	if err := p.keyword("MATCH"); err != nil {
		return plan, err
	}
	if p.peek().kind == tokIdent && p.peekN(1).kind == tokEq {
		plan.pathAlias = p.next().text
		p.next()
	}
	start, err := p.parseNode()
	if err != nil {
		return plan, err
	}
	plan.start = start
	if p.peek().kind == tokDash {
		edge, end, err := p.parseEdgeAndNode()
		if err != nil {
			return plan, err
		}
		plan.edge, plan.end = &edge, &end
	}
	if p.isKeyword("WHERE") {
		p.next()
		plan.where, err = p.parseOr()
		if err != nil {
			return plan, err
		}
	}
	if err := p.keyword("RETURN"); err != nil {
		return plan, err
	}
	for {
		t := p.next()
		if t.kind != tokIdent {
			return plan, newQueryError("syntax", "RETURN expects a variable", t.pos, p.source)
		}
		if p.peek().kind == tokDot || p.peek().kind == tokLParen {
			return plan, newQueryError("unsupported", "v1 RETURN accepts graph variables, not scalar projections", p.peek().pos, p.source)
		}
		plan.returns = append(plan.returns, t.text)
		if p.peek().kind != tokComma {
			break
		}
		p.next()
	}
	if p.isKeyword("ORDER") {
		p.next()
		if err := p.keyword("BY"); err != nil {
			return plan, err
		}
		ref, err := p.parsePropertyRef()
		if err != nil {
			return plan, err
		}
		plan.order = &orderSpec{ref: ref}
		if p.isKeyword("ASC") {
			p.next()
		} else if p.isKeyword("DESC") {
			p.next()
			plan.order.desc = true
		}
	}
	if p.isKeyword("LIMIT") {
		p.next()
		t := p.next()
		if t.kind != tokNumber || strings.Contains(t.text, ".") {
			return plan, newQueryError("syntax", "LIMIT expects a non-negative integer", t.pos, p.source)
		}
		n, err := strconv.Atoi(t.text)
		if err != nil || n < 0 {
			return plan, newQueryError("syntax", "invalid LIMIT", t.pos, p.source)
		}
		plan.limit = n
	}
	if p.peek().kind != tokEOF {
		return plan, newQueryError("syntax", "unexpected token "+p.peek().text, p.peek().pos, p.source)
	}
	if len(plan.returns) == 0 {
		return plan, newQueryError("syntax", "RETURN needs at least one graph variable", p.peek().pos, p.source)
	}
	return plan, nil
}

func (p *queryParser) parseNode() (nodePattern, error) {
	open := p.next()
	if open.kind != tokLParen {
		return nodePattern{}, newQueryError("syntax", "expected (", open.pos, p.source)
	}
	alias := p.next()
	if alias.kind != tokIdent {
		return nodePattern{}, newQueryError("syntax", "node needs an alias", alias.pos, p.source)
	}
	n := nodePattern{alias: alias.text, pos: alias.pos}
	if p.peek().kind == tokColon {
		p.next()
		kind := p.next()
		if kind.kind != tokIdent {
			return n, newQueryError("syntax", "expected node kind", kind.pos, p.source)
		}
		n.kind = strings.ToLower(kind.text)
	}
	close := p.next()
	if close.kind != tokRParen {
		return n, newQueryError("syntax", "expected )", close.pos, p.source)
	}
	return n, nil
}

func (p *queryParser) parseEdgeAndNode() (edgePattern, nodePattern, error) {
	dash := p.next()
	e := edgePattern{min: 1, max: 1, pos: dash.pos}
	if p.peek().kind == tokLBracket {
		p.next()
		if p.peek().kind == tokIdent {
			e.alias = p.next().text
		}
		if p.peek().kind == tokColon {
			p.next()
			t := p.next()
			if t.kind != tokIdent {
				return e, nodePattern{}, newQueryError("syntax", "expected relationship kind", t.pos, p.source)
			}
			e.kind = strings.ToLower(t.text)
		}
		if p.peek().kind == tokStar {
			star := p.next()
			if p.peek().kind != tokNumber {
				return e, nodePattern{}, newQueryError("path_depth", "unbounded variable-length paths are not supported; use *min..max", star.pos, p.source)
			}
			minTok := p.next()
			if strings.Contains(minTok.text, ".") {
				return e, nodePattern{}, newQueryError("syntax", "invalid path minimum", minTok.pos, p.source)
			}
			min, err := strconv.Atoi(minTok.text)
			if err != nil {
				return e, nodePattern{}, newQueryError("syntax", "invalid path minimum", minTok.pos, p.source)
			}
			if p.peek().kind != tokRange {
				return e, nodePattern{}, newQueryError("path_depth", "variable-length paths need an explicit min..max bound", star.pos, p.source)
			}
			p.next()
			maxTok := p.next()
			if maxTok.kind != tokNumber || strings.Contains(maxTok.text, ".") {
				return e, nodePattern{}, newQueryError("syntax", "invalid path maximum", maxTok.pos, p.source)
			}
			max, err := strconv.Atoi(maxTok.text)
			if err != nil {
				return e, nodePattern{}, newQueryError("syntax", "invalid path maximum", maxTok.pos, p.source)
			}
			e.min, e.max = min, max
		}
		close := p.next()
		if close.kind != tokRBracket {
			return e, nodePattern{}, newQueryError("syntax", "expected ]", close.pos, p.source)
		}
	}
	arrow := p.next()
	if arrow.kind != tokArrow {
		return e, nodePattern{}, newQueryError("syntax", "only directed -> relationships are supported in v1", arrow.pos, p.source)
	}
	end, err := p.parseNode()
	return e, end, err
}

func (p *queryParser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("OR") {
		t := p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = boolExpr{op: "OR", left: left, right: right, pos: t.pos}
	}
	return left, nil
}

func (p *queryParser) parseAnd() (expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("AND") {
		t := p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = boolExpr{op: "AND", left: left, right: right, pos: t.pos}
	}
	return left, nil
}

func (p *queryParser) parseUnary() (expr, error) {
	if p.isKeyword("NOT") {
		p.next()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notExpr{inner: inner}, nil
	}
	if p.peek().kind == tokLParen {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if t := p.next(); t.kind != tokRParen {
			return nil, newQueryError("syntax", "expected ) in WHERE", t.pos, p.source)
		}
		return inner, nil
	}
	return p.parseComparison()
}

func (p *queryParser) parseComparison() (expr, error) {
	ref, err := p.parsePropertyRef()
	if err != nil {
		return nil, err
	}
	op := p.next()
	switch op.kind {
	case tokEq, tokNE, tokLT, tokLE, tokGT, tokGE:
	default:
		return nil, newQueryError("syntax", "expected comparison operator", op.pos, p.source)
	}
	v := p.next()
	qv := queryValue{pos: v.pos}
	switch v.kind {
	case tokParam:
		qv.param = v.text
	case tokString:
		qv.literal = v.text
	case tokNumber:
		n, err := strconv.ParseFloat(v.text, 64)
		if err != nil {
			return nil, newQueryError("syntax", "invalid number", v.pos, p.source)
		}
		qv.literal = n
	case tokIdent:
		switch strings.ToUpper(v.text) {
		case "TRUE":
			qv.literal = true
		case "FALSE":
			qv.literal = false
		default:
			return nil, newQueryError("syntax", "expected literal or $parameter", v.pos, p.source)
		}
	default:
		return nil, newQueryError("syntax", "expected literal or $parameter", v.pos, p.source)
	}
	return compareExpr{ref: ref, op: op.text, value: qv, pos: op.pos}, nil
}

func (p *queryParser) parsePropertyRef() (propertyRef, error) {
	a := p.next()
	if a.kind != tokIdent {
		return propertyRef{}, newQueryError("syntax", "expected variable", a.pos, p.source)
	}
	if d := p.next(); d.kind != tokDot {
		return propertyRef{}, newQueryError("syntax", "expected . after variable", d.pos, p.source)
	}
	prop := p.next()
	if prop.kind != tokIdent {
		return propertyRef{}, newQueryError("syntax", "expected property name", prop.pos, p.source)
	}
	return propertyRef{alias: a.text, property: prop.text, pos: a.pos}, nil
}

func (p *queryParser) keyword(want string) error {
	t := p.next()
	if t.kind != tokIdent || !strings.EqualFold(t.text, want) {
		return newQueryError("syntax", "expected "+want, t.pos, p.source)
	}
	return nil
}

func (p *queryParser) isKeyword(want string) bool {
	t := p.peek()
	return t.kind == tokIdent && strings.EqualFold(t.text, want)
}

func (p *queryParser) peek() token { return p.tokens[p.i] }

func (p *queryParser) peekN(n int) token {
	if p.i+n >= len(p.tokens) {
		return token{kind: tokEOF, pos: len(p.source)}
	}
	return p.tokens[p.i+n]
}

func (p *queryParser) next() token {
	t := p.peek()
	if p.i < len(p.tokens)-1 {
		p.i++
	}
	return t
}
