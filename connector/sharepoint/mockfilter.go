package sharepoint

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// mockFilterSubset names what the mockup's filter understands, for the message that
// refuses the rest.
const mockFilterSubset = "fields/<column> eq|ne|gt|ge|lt|le <'text'|number|true|false|null>, " +
	"startswith(fields/<column>,'text'), and, or, not and parentheses"

// parseMockFilter reads list-items' filter in the subset of OData the mockup evaluates
// and answers a predicate over an item. The empty filter matches everything.
//
// The subset is what a process filters a SharePoint list by in practice: a column
// compared with a value, a text column's prefix, and the two combined. What lies outside
// it — lambda operators, arithmetic, functions other than startswith, a property that
// is not under fields/ — is refused with the subset named, rather than read as something
// it might mean: a mockup that guessed would pass a model the site then refuses.
//
// A column must be one the list has had a value written to. SharePoint refuses a filter
// on a column a list does not have, and here that is the only way a misspelt column name
// shows itself before the process reads it.
func parseMockFilter(op, filter string, l *mockList) (mockPredicate, error) {
	if strings.TrimSpace(filter) == "" {
		return func(*mockItem) bool { return true }, nil
	}
	toks, err := lexMockFilter(filter)
	if err != nil {
		return nil, refuse(op, "filter %q: %v; the mockup understands %s", filter, err, mockFilterSubset)
	}
	p := &mockFilterParser{toks: toks, list: l}
	match, err := p.or()
	if err == nil && p.pos < len(p.toks) {
		err = fmt.Errorf("unexpected %q", p.toks[p.pos].text)
	}
	if err != nil {
		if p.columnErr != nil {
			return nil, refuse(op, "%v", p.columnErr)
		}
		return nil, refuse(op, "filter %q: %v; the mockup understands %s", filter, err, mockFilterSubset)
	}
	return match, nil
}

type mockTokenKind int

const (
	tokWord mockTokenKind = iota // a name, a keyword or a number
	tokString
	tokOpen
	tokClose
	tokComma
)

type mockToken struct {
	kind mockTokenKind
	text string
}

// lexMockFilter splits a filter into words, quoted strings (a doubled quote is a quote
// inside one) and the punctuation the subset uses.
func lexMockFilter(s string) ([]mockToken, error) {
	var toks []mockToken
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '(':
			toks = append(toks, mockToken{tokOpen, "("})
			i++
		case ch == ')':
			toks = append(toks, mockToken{tokClose, ")"})
			i++
		case ch == ',':
			toks = append(toks, mockToken{tokComma, ","})
			i++
		case ch == '\'':
			var b strings.Builder
			i++
			for {
				if i >= len(s) {
					return nil, fmt.Errorf("a quoted value is not closed")
				}
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
			toks = append(toks, mockToken{tokString, b.String()})
		default:
			start := i
			for i < len(s) && !strings.ContainsRune(" \t\n\r(),'", rune(s[i])) {
				i++
			}
			toks = append(toks, mockToken{tokWord, s[start:i]})
		}
	}
	return toks, nil
}

type mockFilterParser struct {
	toks []mockToken
	pos  int
	list *mockList
	// columnErr is set when the filter names a column the list does not have: a fault in
	// the model rather than in the filter's syntax, and reported as such.
	columnErr error
}

type mockPredicate = func(*mockItem) bool

func (p *mockFilterParser) peekWord(word string) bool {
	return p.pos < len(p.toks) && p.toks[p.pos].kind == tokWord && strings.EqualFold(p.toks[p.pos].text, word)
}

func (p *mockFilterParser) expect(kind mockTokenKind, what string) (mockToken, error) {
	if p.pos >= len(p.toks) {
		return mockToken{}, fmt.Errorf("%s expected at the end", what)
	}
	t := p.toks[p.pos]
	if t.kind != kind {
		return mockToken{}, fmt.Errorf("%s expected, found %q", what, t.text)
	}
	p.pos++
	return t, nil
}

func (p *mockFilterParser) or() (mockPredicate, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peekWord("or") {
		p.pos++
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(it *mockItem) bool { return l(it) || r(it) }
	}
	return left, nil
}

func (p *mockFilterParser) and() (mockPredicate, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.peekWord("and") {
		p.pos++
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(it *mockItem) bool { return l(it) && r(it) }
	}
	return left, nil
}

func (p *mockFilterParser) unary() (mockPredicate, error) {
	if p.peekWord("not") {
		p.pos++
		inner, err := p.unary()
		if err != nil {
			return nil, err
		}
		return func(it *mockItem) bool { return !inner(it) }, nil
	}
	if p.pos < len(p.toks) && p.toks[p.pos].kind == tokOpen {
		p.pos++
		inner, err := p.or()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokClose, "\")\""); err != nil {
			return nil, err
		}
		return inner, nil
	}
	if p.peekWord("startswith") {
		return p.startsWith()
	}
	return p.comparison()
}

func (p *mockFilterParser) startsWith() (mockPredicate, error) {
	p.pos++
	if _, err := p.expect(tokOpen, "\"(\" after startswith"); err != nil {
		return nil, err
	}
	col, err := p.column()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokComma, "\",\" in startswith"); err != nil {
		return nil, err
	}
	prefix, err := p.expect(tokString, "a quoted prefix in startswith")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokClose, "\")\" after startswith"); err != nil {
		return nil, err
	}
	want := strings.ToLower(prefix.text)
	return func(it *mockItem) bool {
		s, ok := it.fields[col].(string)
		return ok && strings.HasPrefix(strings.ToLower(s), want)
	}, nil
}

// column reads fields/<name> and checks the list has the column. fields/id is the
// item's id, which every list has.
func (p *mockFilterParser) column() (string, error) {
	t, err := p.expect(tokWord, "fields/<column>")
	if err != nil {
		return "", err
	}
	name, ok := strings.CutPrefix(t.text, "fields/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("%q is not fields/<column>", t.text)
	}
	if strings.EqualFold(name, "id") {
		return "", nil // the id, which no column write can shadow
	}
	if !p.list.columns[name] {
		p.columnErr = fmt.Errorf("the list %q has no column %q — the mockup knows a column from the first value written to it, and column names are SharePoint's internal ones, case and all", p.list.title, name)
		return "", p.columnErr
	}
	return name, nil
}

func (p *mockFilterParser) comparison() (mockPredicate, error) {
	col, err := p.column()
	if err != nil {
		return nil, err
	}
	opTok, err := p.expect(tokWord, "eq, ne, gt, ge, lt or le")
	if err != nil {
		return nil, err
	}
	cmp := strings.ToLower(opTok.text)
	switch cmp {
	case "eq", "ne", "gt", "ge", "lt", "le":
	default:
		return nil, fmt.Errorf("%q is not eq, ne, gt, ge, lt or le", opTok.text)
	}
	lit, err := p.literal()
	if err != nil {
		return nil, err
	}
	if lit == nil && cmp != "eq" && cmp != "ne" {
		return nil, fmt.Errorf("null compares only with eq or ne")
	}
	return func(it *mockItem) bool {
		if col == "" {
			// The id is a number to SharePoint and text in every answer, so a model may
			// compare it either way.
			if _, quoted := lit.(string); quoted {
				return compareMock(strconv.Itoa(it.id), cmp, lit)
			}
			return compareMock(json.Number(strconv.Itoa(it.id)), cmp, lit)
		}
		return compareMock(it.fields[col], cmp, lit)
	}, nil
}

// literal reads a value: a quoted string, a number, true, false or null.
func (p *mockFilterParser) literal() (any, error) {
	if p.pos >= len(p.toks) {
		return nil, fmt.Errorf("a value expected at the end")
	}
	t := p.toks[p.pos]
	p.pos++
	switch {
	case t.kind == tokString:
		return t.text, nil
	case t.kind != tokWord:
		return nil, fmt.Errorf("a value expected, found %q", t.text)
	case strings.EqualFold(t.text, "true"):
		return true, nil
	case strings.EqualFold(t.text, "false"):
		return false, nil
	case strings.EqualFold(t.text, "null"):
		return nil, nil
	}
	if _, err := strconv.ParseFloat(t.text, 64); err == nil && (unicode.IsDigit(rune(t.text[0])) || t.text[0] == '-' || t.text[0] == '.') {
		return json.Number(t.text), nil
	}
	return nil, fmt.Errorf("%q is not a value: text is quoted, as in 'Erledigt'", t.text)
}

// compareMock compares a column's value with a literal. A missing column is null. Text
// compares without regard to case, as SharePoint's collation does; a number compares as
// a number whichever JSON shape carried it; a value of another type than the literal
// matches only ne.
func compareMock(v any, cmp string, lit any) bool {
	if lit == nil {
		isNull := v == nil
		return (cmp == "eq") == isNull
	}
	if v == nil {
		return cmp == "ne"
	}
	var c int
	switch want := lit.(type) {
	case string:
		got, ok := v.(string)
		if !ok {
			return cmp == "ne"
		}
		c = strings.Compare(strings.ToLower(got), strings.ToLower(want))
	case bool:
		got, ok := v.(bool)
		if !ok {
			return cmp == "ne"
		}
		if cmp != "eq" && cmp != "ne" {
			return false
		}
		if got == want {
			c = 0
		} else {
			c = 1
		}
	case json.Number:
		got, ok := numberOf(v)
		if !ok {
			return cmp == "ne"
		}
		w, _ := want.Float64()
		switch {
		case got < w:
			c = -1
		case got > w:
			c = 1
		}
	}
	switch cmp {
	case "eq":
		return c == 0
	case "ne":
		return c != 0
	case "gt":
		return c > 0
	case "ge":
		return c >= 0
	case "lt":
		return c < 0
	default:
		return c <= 0
	}
}

// numberOf reads a column value as a number, in any of the shapes a JSON number can
// reach the mockup in.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
