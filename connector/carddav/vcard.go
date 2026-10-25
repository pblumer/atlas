package carddav

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// prodID names the writer of a card this Worker creates (RFC 6350 §6.7.3).
const prodID = "-//atlas//CardDAV Worker//EN"

// Card is one vCard (RFC 6350, and vCard 3.0 of RFC 2426) as the list of its
// properties, in the order they were read.
//
// It is deliberately not a struct of the fields this Worker understands. A contact on a
// CardDAV server carries what every client that ever touched it put there — a photo,
// Apple's labels and grouping, a social profile, a vendor's X- property — and an update
// that parsed the card into the fields it knows and wrote those back would delete the
// rest without anybody noticing. So a write changes the properties a contact key maps
// to and writes every other one from the line it was read from.
type Card struct {
	Props []*Prop
}

// Prop is one content line: `[group.]name *(;param) : value`.
type Prop struct {
	// Group is Apple's grouping prefix (`item1` in `item1.EMAIL`), "" for none.
	Group string
	// Name is the property name as written; compare it with [Prop.Is].
	Name   string
	Params []Param
	// Value is the value as it stands on the line: still escaped. Text and Components
	// decode it.
	Value string
	// raw is the unfolded line as it was read, written back verbatim while the property
	// is unchanged; "" once it has been changed or for a property built here.
	raw string
}

// Param is one parameter with its values, unquoted.
type Param struct {
	Name   string
	Values []string
}

// errNoCard is a document that holds no vCard at all.
var errNoCard = errors.New("carddav: the document holds no vCard (no BEGIN:VCARD … END:VCARD)")

// ParseCard reads the first vCard of a document. It is lenient where real servers are
// sloppy — LF line ends, tab continuations, lower-case names, a vCard 2.1 parameter
// without a name — because refusing a card a server holds would make the contact
// unreachable. A line it cannot read at all is kept as it was and written back as it
// was; it never becomes a property this Worker acts on.
func ParseCard(data []byte) (*Card, error) {
	lines := unfold(data)
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "BEGIN:VCARD") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errNoCard
	}
	c := &Card{}
	for _, l := range lines[start+1:] {
		if strings.EqualFold(strings.TrimSpace(l), "END:VCARD") {
			return c, nil
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		c.Props = append(c.Props, parseLine(l))
	}
	return nil, errNoCard
}

// unfold splits a document into logical lines: a line that begins with a space or a tab
// continues the one before it (RFC 6350 §3.2).
func unfold(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// parseLine reads one unfolded content line. A line with no colon outside quotes is not
// a property; it is kept with its text and no name.
func parseLine(line string) *Prop {
	p := &Prop{raw: line}
	// The value begins at the first colon that is not inside a quoted parameter value.
	colon, quoted := -1, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return p
	}
	head := line[:colon]
	p.Value = line[colon+1:]
	parts := splitParams(head)
	name := parts[0]
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		p.Group, name = name[:dot], name[dot+1:]
	}
	p.Name = strings.TrimSpace(name)
	for _, raw := range parts[1:] {
		pname, pvalue, hasValue := strings.Cut(raw, "=")
		if !hasValue {
			// vCard 2.1's bare parameter (`TEL;HOME:`), which 3.0 cards from old
			// exporters still carry: it is a TYPE.
			p.Params = append(p.Params, Param{Name: "TYPE", Values: []string{raw}})
			continue
		}
		p.Params = append(p.Params, Param{Name: pname, Values: splitParamValues(pvalue)})
	}
	return p
}

// splitParams splits a line's head on the semicolons that are not inside quotes.
func splitParams(head string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(head); i++ {
		ch := head[i]
		switch {
		case ch == '"':
			quoted = !quoted
			cur.WriteByte(ch)
		case ch == ';' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(out, cur.String())
}

// splitParamValues splits a parameter's values on the commas outside quotes, and
// unquotes each.
func splitParamValues(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			quoted = !quoted
		case ch == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(out, cur.String())
}

// Bytes writes the card: BEGIN, every property in order, END, each line folded at 75
// octets.
func (c *Card) Bytes() []byte {
	var b bytes.Buffer
	b.WriteString("BEGIN:VCARD\r\n")
	for _, p := range c.Props {
		writeFolded(&b, p.line())
	}
	b.WriteString("END:VCARD\r\n")
	return b.Bytes()
}

// writeFolded writes one logical line folded at 75 octets (RFC 6350 §3.2), never inside
// a UTF-8 sequence.
func writeFolded(b *bytes.Buffer, line string) {
	limit := 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n ")
		line = line[cut:]
		// A continuation line starts with the space, which counts toward its 75.
		limit = 74
	}
	b.WriteString(line)
	b.WriteString("\r\n")
}

// line is the property as one unfolded content line: the line it was read from while
// it is unchanged, else built from its parts.
func (p *Prop) line() string {
	if p.raw != "" {
		return p.raw
	}
	var b strings.Builder
	if p.Group != "" {
		b.WriteString(p.Group)
		b.WriteByte('.')
	}
	b.WriteString(p.Name)
	for _, prm := range p.Params {
		b.WriteByte(';')
		b.WriteString(prm.Name)
		b.WriteByte('=')
		for i, v := range prm.Values {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(quoteParam(v))
		}
	}
	b.WriteByte(':')
	b.WriteString(p.Value)
	return b.String()
}

// quoteParam quotes a parameter value that holds a separator. A double quote cannot be
// written inside one at all in vCard 3.0, so it is dropped rather than breaking the line.
func quoteParam(v string) string {
	v = strings.ReplaceAll(v, `"`, "")
	v = strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
	if strings.ContainsAny(v, ";:,") {
		return `"` + v + `"`
	}
	return v
}

// Version is the card's VERSION, "3.0" when it states none.
func (c *Card) Version() string {
	if v := c.First("VERSION"); v != nil {
		return strings.TrimSpace(v.Value)
	}
	return "3.0"
}

// v4 reports whether the card is vCard 4.0, which spells a few things differently.
func (c *Card) v4() bool { return strings.HasPrefix(c.Version(), "4") }

// Is reports whether the property has that name, ignoring case as vCard does.
func (p *Prop) Is(name string) bool {
	return p != nil && p.Name != "" && strings.EqualFold(p.Name, name)
}

// All lists the properties of a name, in order.
func (c *Card) All(name string) []*Prop {
	var out []*Prop
	for _, p := range c.Props {
		if p.Is(name) {
			out = append(out, p)
		}
	}
	return out
}

// First is the first property of a name, nil for none.
func (c *Card) First(name string) *Prop {
	for _, p := range c.Props {
		if p.Is(name) {
			return p
		}
	}
	return nil
}

// Add appends a property and returns it.
func (c *Card) Add(p *Prop) *Prop {
	c.Props = append(c.Props, p)
	return p
}

// Insert puts a property at position i (clamped to the card).
func (c *Card) Insert(i int, p *Prop) {
	if i < 0 || i > len(c.Props) {
		i = len(c.Props)
	}
	c.Props = append(c.Props, nil)
	copy(c.Props[i+1:], c.Props[i:])
	c.Props[i] = p
}

// index is a property's position in the card, -1 when it is not in it.
func (c *Card) index(p *Prop) int {
	for i, q := range c.Props {
		if q == p {
			return i
		}
	}
	return -1
}

// Remove takes properties out of the card. A removed property that belonged to one of
// Apple's groups takes that group's label and address format along (`item1.X-ABLabel`,
// `item1.X-ABADR`) once nothing else is left in the group: a label for a value that is
// gone would show in Apple Contacts as an empty labelled row.
func (c *Card) Remove(props ...*Prop) {
	gone := map[*Prop]bool{}
	groups := map[string]bool{}
	for _, p := range props {
		gone[p] = true
		if p.Group != "" {
			groups[strings.ToLower(p.Group)] = true
		}
	}
	kept := c.Props[:0]
	for _, p := range c.Props {
		if !gone[p] {
			kept = append(kept, p)
		}
	}
	c.Props = kept
	for g := range groups {
		var members, aux []*Prop
		for _, p := range c.Props {
			if strings.ToLower(p.Group) != g {
				continue
			}
			if p.Is("X-ABLABEL") || p.Is("X-ABADR") {
				aux = append(aux, p)
			} else {
				members = append(members, p)
			}
		}
		if len(members) == 0 && len(aux) > 0 {
			c.Remove(aux...)
		}
	}
}

// NewCard is an empty vCard 3.0 with its UID — the version every CardDAV server here
// accepts, iCloud's included.
func NewCard(uid string) *Card {
	c := &Card{}
	c.Add(&Prop{Name: "VERSION", Value: "3.0"})
	c.Add(&Prop{Name: "PRODID", Value: prodID})
	c.Add(&Prop{Name: "UID"}).SetText(uid)
	return c
}

// Text is the value as text: unescaped.
func (p *Prop) Text() string {
	if p == nil {
		return ""
	}
	return unescapeText(p.Value)
}

// SetText sets the value to a text, escaped.
func (p *Prop) SetText(s string) {
	p.Value = escapeText(s)
	p.raw = ""
}

// Components are a structured value's components (N, ADR, ORG), each unescaped.
func (p *Prop) Components() []string {
	if p == nil {
		return nil
	}
	raw := splitEscaped(p.Value, ';')
	out := make([]string, len(raw))
	for i, r := range raw {
		out[i] = unescapeText(r)
	}
	return out
}

// SetComponents sets a structured value from its components.
func (p *Prop) SetComponents(parts []string) {
	p.Value = joinComponents(parts)
	p.raw = ""
}

// List is a list value's items (CATEGORIES), each unescaped and trimmed, empty ones
// dropped.
func (p *Prop) List() []string {
	var out []string
	for _, r := range splitEscaped(p.Value, ',') {
		if s := strings.TrimSpace(unescapeText(r)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SetList sets a list value from its items.
func (p *Prop) SetList(items []string) {
	esc := make([]string, len(items))
	for i, s := range items {
		esc[i] = escapeText(s)
	}
	p.Value = strings.Join(esc, ",")
	p.raw = ""
}

// Param is the values of every parameter of that name, in order.
func (p *Prop) Param(name string) []string {
	var out []string
	for _, prm := range p.Params {
		if strings.EqualFold(prm.Name, name) {
			out = append(out, prm.Values...)
		}
	}
	return out
}

// SetParam replaces every parameter of that name with one holding the values; no
// values removes it.
func (p *Prop) SetParam(name string, values ...string) {
	kept := p.Params[:0]
	at := -1
	for _, prm := range p.Params {
		if strings.EqualFold(prm.Name, name) {
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, prm)
	}
	p.Params = kept
	if len(values) > 0 {
		prm := Param{Name: name, Values: values}
		if at < 0 || at > len(p.Params) {
			p.Params = append(p.Params, prm)
		} else {
			p.Params = append(p.Params[:at], append([]Param{prm}, p.Params[at:]...)...)
		}
	}
	p.raw = ""
}

// Types are the property's TYPE values, lower-cased, across every TYPE parameter and
// every comma inside one.
func (p *Prop) Types() []string {
	var out []string
	for _, v := range p.Param("TYPE") {
		for _, t := range strings.Split(v, ",") {
			if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// HasType reports whether the property carries a TYPE value, ignoring case.
func (p *Prop) HasType(t string) bool {
	for _, have := range p.Types() {
		if strings.EqualFold(have, t) {
			return true
		}
	}
	return false
}

// prefRank is how preferred a property is: its vCard 4.0 PREF (1 is the most), 1 for
// vCard 3.0's TYPE=pref, and 101 — below every PREF there is — for neither.
func (p *Prop) prefRank() int {
	for _, v := range p.Param("PREF") {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	if p.HasType("pref") {
		return 1
	}
	return 101
}

// preferred is the most preferred of the properties, the first among equals; nil for
// none.
func preferred(props []*Prop) *Prop {
	var best *Prop
	for _, p := range props {
		if best == nil || p.prefRank() < best.prefRank() {
			best = p
		}
	}
	return best
}

// unescapeText decodes a text value (RFC 6350 §3.4): \n and \N are a line break, a
// backslash before any other character stands for that character.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// escapeText encodes a text value: backslash, comma, semicolon and line break.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.NewReplacer(`\`, `\\`, ",", `\,`, ";", `\;`, "\n", `\n`, "\r", `\n`).Replace(s)
}

// splitEscaped splits a raw value on a separator that is not escaped, leaving each part
// escaped.
func splitEscaped(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// joinComponents builds a structured value from its components, each escaped.
func joinComponents(parts []string) string {
	esc := make([]string, len(parts))
	for i, s := range parts {
		esc[i] = escapeText(s)
	}
	return strings.Join(esc, ";")
}
