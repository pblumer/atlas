package carddav

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/carddav/catalog"
)

// project turns a card into the flat contact a model reads: Google Contacts' contact
// (ADR-0476), filled from vCard. groups are the contact's groups as the Worker's group
// form reads them — its CATEGORIES, or the group cards it is a member of — which the
// caller knows and the card alone may not.
//
// Every key is present whatever the card holds — a missing scalar is null, a missing
// list empty — so a model can write `count(kunde.emails)` or `kunde.email = null`
// without first asking whether the key exists.
func project(id string, c *Card, groups []string) map[string]any {
	out := map[string]any{
		catalog.KeyID: nil, catalog.KeyDisplayName: nil,
		catalog.KeyGivenName: nil, catalog.KeyFamilyName: nil,
		catalog.KeyEmail: nil, catalog.KeyEmails: []any{},
		catalog.KeyPhone: nil, catalog.KeyPhones: []any{},
		catalog.KeyOrganization: nil, catalog.KeyJobTitle: nil, catalog.KeyDepartment: nil,
		catalog.KeyAddress: nil, catalog.KeyAddresses: []any{},
		catalog.KeyBirthday: nil, catalog.KeyNotes: nil,
		catalog.KeyFields: map[string]any{}, catalog.KeyGroups: []any{},
	}
	set := func(key, v string) {
		if v = strings.TrimSpace(v); v != "" {
			out[key] = v
		}
	}
	set(catalog.KeyID, id)
	set(catalog.KeyDisplayName, c.First("FN").Text())
	if n := c.First("N").Components(); len(n) > 0 {
		set(catalog.KeyFamilyName, n[0])
		if len(n) > 1 {
			set(catalog.KeyGivenName, n[1])
		}
	}
	out[catalog.KeyEmails], out[catalog.KeyEmail] = valueEntries(c.All("EMAIL"), entryValue)
	out[catalog.KeyPhones], out[catalog.KeyPhone] = valueEntries(c.All("TEL"), phoneValue)
	if org := c.First("ORG").Components(); len(org) > 0 {
		set(catalog.KeyOrganization, org[0])
		if len(org) > 1 {
			set(catalog.KeyDepartment, org[1])
		}
	}
	set(catalog.KeyJobTitle, c.First("TITLE").Text())
	adrs := c.All("ADR")
	list := make([]any, 0, len(adrs))
	for _, a := range adrs {
		list = append(list, addressOf(a))
	}
	out[catalog.KeyAddresses] = list
	if a := preferred(adrs); a != nil {
		out[catalog.KeyAddress] = addressOf(a)
	}
	if b := c.First("BDAY"); b != nil {
		set(catalog.KeyBirthday, birthdayOf(b))
	}
	set(catalog.KeyNotes, c.First("NOTE").Text())
	fields := map[string]any{}
	for _, p := range customFields(c) {
		name := p.Name[2:]
		if _, dup := fieldIn(fields, name); !dup {
			fields[name] = p.Text()
		}
	}
	out[catalog.KeyFields] = fields
	gl := make([]any, 0, len(groups))
	for _, g := range groups {
		gl = append(gl, g)
	}
	out[catalog.KeyGroups] = gl
	return out
}

// customFields are the card's own extension properties: ungrouped X- properties that
// are not another client's bookkeeping (catalog.IsVendorProperty). A grouped one
// belongs to an Apple label row, not to the contact.
func customFields(c *Card) []*Prop {
	var out []*Prop
	for _, p := range c.Props {
		if p.Group != "" || len(p.Name) < 3 || !strings.EqualFold(p.Name[:2], "X-") || catalog.IsVendorProperty(p.Name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// fieldIn finds a field name in a projected fields map, ignoring case.
func fieldIn(fields map[string]any, name string) (string, bool) {
	for k := range fields {
		if strings.EqualFold(k, name) {
			return k, true
		}
	}
	return "", false
}

// valueEntries projects e-mail addresses or phone numbers: every entry as {value,
// type}, and the preferred one's value alone.
func valueEntries(props []*Prop, value func(*Prop) string) ([]any, any) {
	list := make([]any, 0, len(props))
	for _, p := range props {
		item := map[string]any{"value": value(p)}
		if t := entryType(p); t != "" {
			item["type"] = t
		}
		list = append(list, item)
	}
	if p := preferred(props); p != nil && value(p) != "" {
		return list, value(p)
	}
	return list, nil
}

func entryValue(p *Prop) string { return strings.TrimSpace(p.Text()) }

// phoneValue is a TEL's number: vCard 4.0 may write it as a tel: URI, which a process
// compares and shows as the number it holds.
func phoneValue(p *Prop) string {
	v := strings.TrimSpace(p.Text())
	if len(v) > 4 && strings.EqualFold(v[:4], "tel:") {
		v = v[4:]
	}
	return v
}

// unTyped are the TYPE values that say nothing a process distinguishes: the preference
// (which `email` and `phone` already express), vCard 3.0's INTERNET on every address,
// and VOICE on every number.
var unTyped = map[string]bool{"pref": true, "internet": true, "voice": true}

// entryType is an entry's TYPE as a model reads it: lower-case, the values that say
// nothing dropped, and vCard's `cell` read as `mobile` — Google Contacts' word for it, so
// a type means the same in both Worker Types.
func entryType(p *Prop) string {
	var out []string
	for _, t := range p.Types() {
		if unTyped[t] {
			continue
		}
		if t == "cell" {
			t = "mobile"
		}
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

// typeParam is a model's type as TYPE values: comma-separated, `mobile` written as vCard's
// CELL, upper-case in vCard 3.0 as its writers spell it and lower-case in 4.0.
func typeParam(t string, v4 bool) []string {
	var out []string
	for _, part := range strings.Split(t, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, "mobile") {
			part = "cell"
		}
		if v4 {
			out = append(out, strings.ToLower(part))
		} else {
			out = append(out, strings.ToUpper(part))
		}
	}
	return out
}

// adrKeys are ADR's seven components in their order (RFC 6350 §6.3.1), under Google
// Contacts' names for them.
var adrKeys = []string{"poBox", "extendedAddress", "streetAddress", "city", "region", "postalCode", "country"}

// addressOf projects one ADR to the keys a model reads and writes.
func addressOf(p *Prop) map[string]any {
	out := map[string]any{}
	parts := p.Components()
	for i, k := range adrKeys {
		if i < len(parts) {
			if v := strings.TrimSpace(parts[i]); v != "" {
				out[k] = v
			}
		}
	}
	if t := entryType(p); t != "" {
		out["type"] = t
	}
	return out
}

// appleNoYear is the year Apple writes for a birthday without one, in vCard 3.0, which
// has no yearless date; X-APPLE-OMIT-YEAR names it on the property.
const appleNoYear = "1604"

// birthdayOf formats a BDAY as a contact writes one: the date when the card has one,
// --MM-DD without a year, and the card's text otherwise.
func birthdayOf(p *Prop) string {
	v := strings.TrimSpace(p.Text())
	if strings.EqualFold(firstOf(p.Param("VALUE")), "text") {
		return v
	}
	date, _, _ := strings.Cut(v, "T")
	compact := strings.ReplaceAll(date, "-", "")
	switch {
	case strings.HasPrefix(date, "--") && len(compact) == 4:
		if validMonthDay(compact[0:2], compact[2:4]) {
			return "--" + compact[0:2] + "-" + compact[2:4]
		}
	case len(compact) == 8 && isDigits(compact):
		y, m, d := compact[0:4], compact[4:6], compact[6:8]
		if !validMonthDay(m, d) {
			return v
		}
		if omit := p.Param("X-APPLE-OMIT-YEAR"); len(omit) > 0 && strings.TrimSpace(omit[0]) == y {
			return "--" + m + "-" + d
		}
		return y + "-" + m + "-" + d
	}
	return v
}

func validMonthDay(m, d string) bool {
	mi, err1 := strconv.Atoi(m)
	di, err2 := strconv.Atoi(d)
	return err1 == nil && err2 == nil && mi >= 1 && mi <= 12 && di >= 1 && di <= 31
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func firstOf(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}

// categoriesOf is the contact's CATEGORIES as a set: every value of every CATEGORIES
// property, the first spelling of each kept, ignoring case.
func categoriesOf(c *Card) []string {
	var out []string
	for _, p := range c.All("CATEGORIES") {
		for _, v := range p.List() {
			if !containsFold(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

func containsFold(xs []string, x string) bool {
	for _, s := range xs {
		if strings.EqualFold(s, x) {
			return true
		}
	}
	return false
}

// addCategory puts a group on the contact, reporting whether that changed the card: a
// group already there, in any case, is no change.
func addCategory(c *Card, name string) bool {
	all := categoriesOf(c)
	if containsFold(all, name) {
		return false
	}
	setCategories(c, append(all, name))
	return true
}

// removeCategory takes a group off the contact, reporting whether that changed the card.
func removeCategory(c *Card, name string) bool {
	all := categoriesOf(c)
	kept := all[:0]
	for _, v := range all {
		if !strings.EqualFold(v, name) {
			kept = append(kept, v)
		}
	}
	if len(kept) == len(all) {
		return false
	}
	setCategories(c, kept)
	return true
}

// setCategories writes the groups as one CATEGORIES property where the first one was,
// or none.
func setCategories(c *Card, names []string) {
	all := c.All("CATEGORIES")
	if len(names) == 0 {
		c.Remove(all...)
		return
	}
	if len(all) == 0 {
		c.Add(&Prop{Name: "CATEGORIES"}).SetList(names)
		return
	}
	all[0].SetList(names)
	c.Remove(all[1:]...)
}

// apply writes a contact object into a card, for create and update alike, and leaves
// every property no key maps to as it was read.
//
// The rules are Google Contacts' (ADR-0476):
//
//   - a null value changes nothing (an unset variable must never clear a contact);
//   - "" clears: `email: ""` removes the preferred address, `notes: ""` the notes, a
//     custom field set to "" is removed;
//   - a singular key changes the preferred entry and keeps the others, with its
//     parameters — `email` is the address that changed — and the plural key replaces
//     the list;
//   - `address` replaces the preferred address whole, because a street from the new
//     address and a city from the old one is no address; it keeps the old one's type
//     when the new one names none;
//   - givenName and familyName change those parts of N and keep the rest (middle name,
//     honorifics);
//   - `fields` sets the custom fields it names and keeps the others.
//
// FN, which vCard requires and stores rather than formats, follows from them: a
// displayName written is FN; otherwise FN is formatted again from N when the name
// parts change, or when it was the formatted form before the write — so a company's FN
// follows its organization — and is left alone otherwise. Groups are the caller's: they
// depend on the Worker's group form.
func apply(c *Card, contact map[string]any) error {
	before := derivedFN(c)
	fn := c.First("FN")
	wasDerived := fn == nil || strings.TrimSpace(fn.Text()) == "" || fn.Text() == before
	keys := make([]string, 0, len(contact))
	for k := range contact {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	v4 := c.v4()
	for _, k := range keys {
		v := contact[k]
		if v == nil {
			continue
		}
		switch k {
		case catalog.KeyGivenName:
			setNamePart(c, 1, v.(string))
		case catalog.KeyFamilyName:
			setNamePart(c, 0, v.(string))
		case catalog.KeyEmail:
			setPreferred(c, "EMAIL", v.(string))
		case catalog.KeyPhone:
			setPreferred(c, "TEL", v.(string))
		case catalog.KeyEmails:
			replaceAll(c, "EMAIL", entryProps("EMAIL", v.([]any), v4))
		case catalog.KeyPhones:
			replaceAll(c, "TEL", entryProps("TEL", v.([]any), v4))
		case catalog.KeyOrganization:
			setOrgPart(c, 0, v.(string))
		case catalog.KeyDepartment:
			setOrgPart(c, 1, v.(string))
		case catalog.KeyJobTitle:
			setSingle(c, "TITLE", v.(string))
		case catalog.KeyNotes:
			setSingle(c, "NOTE", v.(string))
		case catalog.KeyAddress:
			setPreferredAddress(c, v, v4)
		case catalog.KeyAddresses:
			var props []*Prop
			for _, a := range v.([]any) {
				props = append(props, addressProp(a.(map[string]any), v4))
			}
			replaceAll(c, "ADR", props)
		case catalog.KeyBirthday:
			if err := setBirthday(c, v.(string), v4); err != nil {
				return err
			}
		case catalog.KeyFields:
			setFields(c, v.(map[string]any))
		case catalog.KeyDisplayName, catalog.KeyGroups:
			// FN is decided below, once every other key is in; groups are the caller's.
		}
	}
	if !v4 && c.First("N") == nil {
		// vCard 3.0 requires N (RFC 2426 §3.1.2), a company's included.
		c.Insert(nameIndex(c), (&Prop{Name: "N", Value: ";;;;"}))
	}
	display, given := contact[catalog.KeyDisplayName].(string)
	switch {
	case given && strings.TrimSpace(display) != "":
		setFN(c, strings.TrimSpace(display))
	case given, contact[catalog.KeyGivenName] != nil, contact[catalog.KeyFamilyName] != nil, wasDerived:
		setFN(c, derivedFN(c))
	}
	return nil
}

// nameIndex is where a new name property goes: after VERSION, PRODID and UID.
func nameIndex(c *Card) int {
	i := 0
	for i < len(c.Props) && (c.Props[i].Is("VERSION") || c.Props[i].Is("PRODID") || c.Props[i].Is("UID")) {
		i++
	}
	return i
}

// derivedFN is the display name a card's other properties give it: the name from its
// parts, honorifics included; for a card without a name, its organization; then its
// e-mail address and phone number.
func derivedFN(c *Card) string {
	if n := c.First("N").Components(); len(n) > 0 {
		// prefix, given, additional, family, suffix
		order := []int{3, 1, 2, 0, 4}
		var parts []string
		for _, i := range order {
			if i < len(n) && strings.TrimSpace(n[i]) != "" {
				parts = append(parts, strings.TrimSpace(n[i]))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	if org := c.First("ORG").Components(); len(org) > 0 && strings.TrimSpace(org[0]) != "" {
		return strings.TrimSpace(org[0])
	}
	if e := preferred(c.All("EMAIL")); e != nil && entryValue(e) != "" {
		return entryValue(e)
	}
	if p := preferred(c.All("TEL")); p != nil {
		return phoneValue(p)
	}
	return ""
}

func setFN(c *Card, v string) {
	if fn := c.First("FN"); fn != nil {
		if fn.Text() != v {
			fn.SetText(v)
		}
		return
	}
	at := nameIndex(c)
	c.Insert(at, &Prop{Name: "FN"})
	c.Props[at].SetText(v)
}

// setNamePart changes one component of N, creating N when the card has none.
func setNamePart(c *Card, i int, v string) {
	n := c.First("N")
	if n == nil {
		n = &Prop{Name: "N"}
		c.Insert(nameIndex(c), n)
	}
	parts := n.Components()
	for len(parts) < 5 {
		parts = append(parts, "")
	}
	parts[i] = strings.TrimSpace(v)
	n.SetComponents(parts)
}

// setOrgPart changes the organization (0) or the department (1) in ORG, and removes ORG
// once both are empty.
func setOrgPart(c *Card, i int, v string) {
	org := c.First("ORG")
	v = strings.TrimSpace(v)
	if org == nil {
		if v == "" {
			return
		}
		org = c.Add(&Prop{Name: "ORG"})
	}
	parts := org.Components()
	for len(parts) < 2 {
		parts = append(parts, "")
	}
	parts[i] = v
	empty := true
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			empty = false
		}
	}
	if empty {
		c.Remove(org)
		return
	}
	org.SetComponents(parts)
}

// setSingle sets a property that a contact has one of (TITLE, NOTE): every one of them
// gives way to the new value, or to none for "".
func setSingle(c *Card, name, v string) {
	all := c.All(name)
	v = strings.TrimRight(v, " \t")
	if strings.TrimSpace(v) == "" {
		c.Remove(all...)
		return
	}
	if len(all) == 0 {
		c.Add(&Prop{Name: name}).SetText(v)
		return
	}
	if all[0].Text() != v {
		all[0].SetText(v)
	}
	c.Remove(all[1:]...)
}

// setPreferred changes the preferred EMAIL or TEL and keeps the others: "" removes it,
// a value replaces its value and keeps its parameters, and a card with none gets one.
func setPreferred(c *Card, name, v string) {
	v = strings.TrimSpace(v)
	p := preferred(c.All(name))
	switch {
	case v == "" && p != nil:
		c.Remove(p)
	case v == "":
	case p != nil:
		if p.Text() != v {
			// A changed number is text: a tel: URI it was written as no longer holds.
			p.SetParam("VALUE")
			p.SetText(v)
		}
	default:
		c.Add(&Prop{Name: name}).SetText(v)
	}
}

// entryProps builds EMAIL or TEL properties from what a model wrote: plain values, or
// {value, type} objects.
func entryProps(name string, items []any, v4 bool) []*Prop {
	var out []*Prop
	for _, it := range items {
		var value, typ string
		switch t := it.(type) {
		case string:
			value = t
		case map[string]any:
			value, _ = t["value"].(string)
			typ, _ = t["type"].(string)
		}
		p := &Prop{Name: name}
		if types := typeParam(typ, v4); len(types) > 0 {
			p.SetParam("TYPE", types...)
		}
		p.SetText(strings.TrimSpace(value))
		out = append(out, p)
	}
	return out
}

// replaceAll replaces every property of a name with new ones, at the place the first
// of them stood.
func replaceAll(c *Card, name string, props []*Prop) {
	old := c.All(name)
	at := len(c.Props)
	if len(old) > 0 {
		at = c.index(old[0])
	}
	c.Remove(old...)
	if at > len(c.Props) {
		at = len(c.Props)
	}
	for i, p := range props {
		c.Insert(at+i, p)
	}
}

// addressProp builds an ADR from an address object.
func addressProp(m map[string]any, v4 bool) *Prop {
	p := &Prop{Name: "ADR"}
	if t, _ := m["type"].(string); t != "" {
		p.SetParam("TYPE", typeParam(t, v4)...)
	}
	parts := make([]string, len(adrKeys))
	for i, k := range adrKeys {
		s, _ := m[k].(string)
		parts[i] = strings.TrimSpace(s)
	}
	p.SetComponents(parts)
	return p
}

// setPreferredAddress replaces the preferred ADR whole, keeping its parameters — its
// type, unless the new address names one — or removes it for "" and {}.
func setPreferredAddress(c *Card, v any, v4 bool) {
	m, _ := v.(map[string]any)
	p := preferred(c.All("ADR"))
	if len(m) == 0 {
		if p != nil {
			c.Remove(p)
		}
		return
	}
	next := addressProp(m, v4)
	if p == nil {
		c.Add(next)
		return
	}
	if t, _ := m["type"].(string); t != "" {
		p.SetParam("TYPE", next.Param("TYPE")...)
	}
	// vCard 4.0's formatted form of the old address no longer describes it.
	p.SetParam("LABEL")
	p.SetComponents(next.Components())
	if p.Group != "" {
		// Apple's country code for the old address, in the same group.
		var stale []*Prop
		for _, q := range c.Props {
			if q != p && strings.EqualFold(q.Group, p.Group) && q.Is("X-ABADR") {
				stale = append(stale, q)
			}
		}
		c.Remove(stale...)
	}
}

// setBirthday writes BDAY: vCard 4.0's date forms, or in 3.0 — which has no date without
// a year — Apple's year 1604 with X-APPLE-OMIT-YEAR, which Apple Contacts and DAVx5
// read as "no year".
func setBirthday(c *Card, v string, v4 bool) error {
	old := c.All("BDAY")
	if v == "" {
		c.Remove(old...)
		return nil
	}
	if !catalog.ValidBirthday(v) {
		return fmt.Errorf("carddav: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", v)
	}
	var y, m, d string
	if strings.HasPrefix(v, "--") {
		m, d = v[2:4], v[5:7]
	} else {
		y, m, d = v[0:4], v[5:7], v[8:10]
	}
	if !validMonthDay(m, d) {
		return fmt.Errorf("carddav: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", v)
	}
	p := &Prop{Name: "BDAY"}
	switch {
	case v4 && y == "":
		p.Value = "--" + m + d
	case v4:
		p.Value = y + m + d
	case y == "":
		p.SetParam("X-APPLE-OMIT-YEAR", appleNoYear)
		p.Value = appleNoYear + "-" + m + "-" + d
	default:
		p.Value = y + "-" + m + "-" + d
	}
	replaceAll(c, "BDAY", []*Prop{p})
	return nil
}

// setFields sets the custom fields a contact names, each its X- property: an existing
// one, matched ignoring case, keeps its spelling; "" removes it; a new one is added as
// X-<name>.
func setFields(c *Card, fields map[string]any) {
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		v := fields[name]
		if v == nil {
			continue
		}
		text := scalarText(v)
		var have []*Prop
		for _, p := range customFields(c) {
			if strings.EqualFold(p.Name[2:], name) {
				have = append(have, p)
			}
		}
		switch {
		case text == "":
			c.Remove(have...)
		case len(have) > 0:
			if have[0].Text() != text {
				have[0].SetText(text)
			}
			c.Remove(have[1:]...)
		default:
			c.Add(&Prop{Name: "X-" + name}).SetText(text)
		}
	}
}

// scalarText is a custom field's value as a vCard holds it: text. A number keeps its
// exact decimal form, which is why a customer number written as 4711 reads back as
// "4711" and not as "4711.0".
func scalarText(v any) string {
	if t, ok := v.(string); ok {
		return strings.TrimSpace(t)
	}
	return fmt.Sprint(v)
}
