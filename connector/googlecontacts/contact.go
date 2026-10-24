package googlecontacts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/googlecontacts/catalog"
)

// The person fields a contact is read with. readFields is everything the flat contact
// shows; directoryFields is the part a directory profile has — it carries no labels,
// custom fields, notes or birthday a process could use.
const (
	readFields      = "names,emailAddresses,phoneNumbers,organizations,addresses,birthdays,biographies,userDefined,memberships"
	directoryFields = "names,emailAddresses,phoneNumbers,organizations,addresses"
)

// labels maps a label's resource name to the name a person gave it. Only labels a
// person made are in it; a system group (myContacts, starred) is not a label anybody
// put on a contact on purpose.
type labels map[string]string

// project turns a People API person into the flat contact a model reads.
//
// Every key is present whatever the contact holds — a missing scalar is null, a missing
// list empty — so a model can write `count(kunde.emails)` or `kunde.email = null`
// without first asking whether the key exists, and the variable view shows the whole
// shape.
func project(p person, lbl labels) map[string]any {
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
		if v != "" {
			out[key] = v
		}
	}
	set(catalog.KeyID, str(p, "resourceName"))
	if n := primaryOf(entries(p, "names")); n != nil {
		set(catalog.KeyDisplayName, str(n, "displayName"))
		set(catalog.KeyGivenName, str(n, "givenName"))
		set(catalog.KeyFamilyName, str(n, "familyName"))
	}
	out[catalog.KeyEmails], out[catalog.KeyEmail] = valueEntries(entries(p, "emailAddresses"))
	out[catalog.KeyPhones], out[catalog.KeyPhone] = valueEntries(entries(p, "phoneNumbers"))
	if o := primaryOf(entries(p, "organizations")); o != nil {
		set(catalog.KeyOrganization, str(o, "name"))
		set(catalog.KeyJobTitle, str(o, "title"))
		set(catalog.KeyDepartment, str(o, "department"))
	}
	addrs := entries(p, "addresses")
	list := make([]any, 0, len(addrs))
	for _, a := range addrs {
		list = append(list, addressOf(a))
	}
	out[catalog.KeyAddresses] = list
	if a := primaryOf(addrs); a != nil {
		out[catalog.KeyAddress] = addressOf(a)
	}
	if b := primaryOf(entries(p, "birthdays")); b != nil {
		set(catalog.KeyBirthday, birthdayOf(b))
	}
	if b := primaryOf(entries(p, "biographies")); b != nil {
		set(catalog.KeyNotes, str(b, "value"))
	}
	fields := map[string]any{}
	for _, u := range entries(p, "userDefined") {
		if k := str(u, "key"); k != "" {
			fields[k] = str(u, "value")
		}
	}
	out[catalog.KeyFields] = fields
	groups := []any{}
	for _, m := range entries(p, "memberships") {
		cg, _ := m["contactGroupMembership"].(map[string]any)
		if name, ok := lbl[str(cg, "contactGroupResourceName")]; ok {
			groups = append(groups, name)
		}
	}
	out[catalog.KeyGroups] = groups
	return out
}

// valueEntries projects e-mail addresses or phone numbers: every entry as
// {value, type}, and the primary one's value alone.
func valueEntries(es []map[string]any) ([]any, any) {
	list := make([]any, 0, len(es))
	for _, e := range es {
		item := map[string]any{"value": str(e, "value")}
		if t := str(e, "type"); t != "" {
			item["type"] = t
		}
		list = append(list, item)
	}
	if p := primaryOf(es); p != nil && str(p, "value") != "" {
		return list, str(p, "value")
	}
	return list, nil
}

// addressOf projects one address to the keys a model reads and writes.
func addressOf(a map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range catalog.AddressKeys {
		if v := str(a, k); v != "" {
			out[k] = v
		}
	}
	return out
}

// birthdayOf formats a birthday as a contact writes one: the date when Google has it,
// without the year when the year is not known, and Google's free text otherwise.
func birthdayOf(b map[string]any) string {
	d, _ := b["date"].(map[string]any)
	y, m, day := num(d, "year"), num(d, "month"), num(d, "day")
	switch {
	case m > 0 && day > 0 && y > 0:
		return fmt.Sprintf("%04d-%02d-%02d", y, m, day)
	case m > 0 && day > 0:
		return fmt.Sprintf("--%02d-%02d", m, day)
	}
	return str(b, "text")
}

// entries returns a person field's entries that are the contact's own. A contact linked
// to a Google profile also shows the profile's entries, which the People API never
// changes through a contact; acting on one of those — "the primary e-mail" being the
// profile's — would be an update reported as done that changed nothing.
func entries(p person, field string) []map[string]any {
	raw, _ := p[field].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		src, _ := meta["source"].(map[string]any)
		if t := str(src, "type"); t != "" && t != "CONTACT" && field != "memberships" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// primaryIndex is the entry Google marks primary, else the first; -1 for none.
func primaryIndex(es []map[string]any) int {
	for i, e := range es {
		meta, _ := e["metadata"].(map[string]any)
		if b, _ := meta["primary"].(bool); b {
			return i
		}
		if b, _ := meta["sourcePrimary"].(bool); b {
			return i
		}
	}
	if len(es) > 0 {
		return 0
	}
	return -1
}

func primaryOf(es []map[string]any) map[string]any {
	if i := primaryIndex(es); i >= 0 {
		return es[i]
	}
	return nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// num reads a number Google sent; the client decodes with UseNumber, so it arrives as a
// json.Number or not at all.
func num(m map[string]any, key string) int {
	n, _ := m[key].(json.Number)
	i, _ := n.Int64()
	return int(i)
}

// fieldOf is the People API field each contact key writes.
var fieldOf = map[string]string{
	catalog.KeyGivenName: "names", catalog.KeyFamilyName: "names",
	catalog.KeyEmail: "emailAddresses", catalog.KeyEmails: "emailAddresses",
	catalog.KeyPhone: "phoneNumbers", catalog.KeyPhones: "phoneNumbers",
	catalog.KeyOrganization: "organizations", catalog.KeyJobTitle: "organizations", catalog.KeyDepartment: "organizations",
	catalog.KeyAddress: "addresses", catalog.KeyAddresses: "addresses",
	catalog.KeyBirthday: "birthdays", catalog.KeyNotes: "biographies", catalog.KeyFields: "userDefined",
	catalog.KeyGroups: "memberships",
}

// fieldsFor lists the People API fields a contact object writes, sorted, without
// memberships — the labels of a new contact are set from resolved names, not read.
func fieldsFor(contact map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for k, v := range contact {
		f := fieldOf[k]
		if v == nil || f == "" || f == "memberships" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// apply writes a contact object into a person: the People API fields it touches, each
// replaced as a whole as updateContact replaces it, and the list of them.
//
// What a key means is decided here, for create and update alike:
//
//   - a null value changes nothing (an unset variable must never clear a contact);
//   - "" clears: `email: ""` removes the primary address, `notes: ""` the notes, a
//     custom field set to "" is removed;
//   - a singular key changes the primary entry and keeps the others — `email` is the
//     address that changed, not the whole list — and the plural key replaces the list;
//   - `address` replaces the primary address whole, because a street from the new
//     address and a city from the old one is no address; it keeps the old one's type
//     when the new one names none;
//   - givenName and familyName change those parts of the name and keep the rest
//     (middle name, honorifics); the formatted forms are dropped so Google formats them
//     again;
//   - `fields` sets the custom fields it names and keeps the others.
//
// p is changed in place; on a create it starts empty.
func apply(p person, contact map[string]any) ([]string, error) {
	keys := make([]string, 0, len(contact))
	for k := range contact {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := contact[k]
		if v == nil {
			continue
		}
		var err error
		switch k {
		case catalog.KeyGivenName, catalog.KeyFamilyName:
			setNamePart(p, k, v.(string))
		case catalog.KeyEmail:
			setPrimaryValue(p, "emailAddresses", v.(string))
		case catalog.KeyPhone:
			setPrimaryValue(p, "phoneNumbers", v.(string))
		case catalog.KeyEmails:
			p["emailAddresses"] = valueList(v.([]any))
		case catalog.KeyPhones:
			p["phoneNumbers"] = valueList(v.([]any))
		case catalog.KeyOrganization:
			setOrganization(p, "name", v.(string))
		case catalog.KeyJobTitle:
			setOrganization(p, "title", v.(string))
		case catalog.KeyDepartment:
			setOrganization(p, "department", v.(string))
		case catalog.KeyAddress:
			setPrimaryAddress(p, v)
		case catalog.KeyAddresses:
			list := make([]any, 0)
			for _, a := range v.([]any) {
				list = append(list, addressEntry(a.(map[string]any)))
			}
			p["addresses"] = list
		case catalog.KeyBirthday:
			err = setBirthday(p, v.(string))
		case catalog.KeyNotes:
			p["biographies"] = []any{}
			if s := v.(string); s != "" {
				p["biographies"] = []any{map[string]any{"value": s, "contentType": "TEXT_PLAIN"}}
			}
		case catalog.KeyFields:
			setFields(p, v.(map[string]any))
		case catalog.KeyGroups:
			// Resolved and written by the caller, which knows the address book's labels.
		}
		if err != nil {
			return nil, err
		}
	}
	return fieldsFor(contact), nil
}

// own returns a field's entries the contact owns, as apply rewrites them: the field is
// written back with these alone, which is all updateContact keeps anyway.
func own(p person, field string) []map[string]any { return entries(p, field) }

func put(p person, field string, es []map[string]any) {
	list := make([]any, 0, len(es))
	for _, e := range es {
		list = append(list, e)
	}
	p[field] = list
}

func setNamePart(p person, key, v string) {
	v = strings.TrimSpace(v)
	es := own(p, "names")
	i := primaryIndex(es)
	var n map[string]any
	if i >= 0 {
		n = es[i]
	} else {
		n = map[string]any{}
	}
	if v == "" {
		delete(n, key)
	} else {
		n[key] = v
	}
	// Google formats these from the parts; left in place, the old full name would
	// contradict the new parts.
	delete(n, "unstructuredName")
	delete(n, "displayName")
	delete(n, "displayNameLastFirst")
	// names is a singleton for a contact: one entry, or none once every part is empty.
	if hasText(n, "metadata") {
		put(p, "names", []map[string]any{n})
	} else {
		put(p, "names", nil)
	}
}

func setPrimaryValue(p person, field, v string) {
	v = strings.TrimSpace(v)
	es := own(p, field)
	i := primaryIndex(es)
	switch {
	case v == "" && i >= 0:
		es = append(es[:i], es[i+1:]...)
	case v == "":
	case i >= 0:
		es[i]["value"] = v
		// Google's E.164 form of the old number; it formats the new one itself.
		delete(es[i], "canonicalForm")
	default:
		es = append(es, map[string]any{"value": v})
	}
	put(p, field, es)
}

// valueList builds e-mail or phone entries from what a model wrote: plain values, or
// {value, type} objects.
func valueList(items []any) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		switch t := it.(type) {
		case string:
			out = append(out, map[string]any{"value": strings.TrimSpace(t)})
		case map[string]any:
			e := map[string]any{"value": strings.TrimSpace(str(t, "value"))}
			if ty := str(t, "type"); ty != "" {
				e["type"] = ty
			}
			out = append(out, e)
		}
	}
	return out
}

func setOrganization(p person, key, v string) {
	v = strings.TrimSpace(v)
	es := own(p, "organizations")
	i := primaryIndex(es)
	if i < 0 {
		if v == "" {
			put(p, "organizations", es)
			return
		}
		es = append(es, map[string]any{})
		i = len(es) - 1
	}
	if v == "" {
		delete(es[i], key)
	} else {
		es[i][key] = v
	}
	if !hasText(es[i], "metadata", "type", "formattedType", "current") {
		es = append(es[:i], es[i+1:]...)
	}
	put(p, "organizations", es)
}

func setPrimaryAddress(p person, v any) {
	es := own(p, "addresses")
	i := primaryIndex(es)
	m, _ := v.(map[string]any)
	if len(m) == 0 { // "" or {}: the primary address goes
		if i >= 0 {
			es = append(es[:i], es[i+1:]...)
		}
		put(p, "addresses", es)
		return
	}
	e := addressEntry(m)
	if i >= 0 {
		if _, typed := e["type"]; !typed && str(es[i], "type") != "" {
			e["type"] = str(es[i], "type")
		}
		if meta, ok := es[i]["metadata"]; ok {
			e["metadata"] = meta
		}
		es[i] = e
	} else {
		es = append(es, e)
	}
	put(p, "addresses", es)
}

func addressEntry(m map[string]any) map[string]any {
	e := map[string]any{}
	for _, k := range catalog.AddressKeys {
		if s := strings.TrimSpace(str(m, k)); s != "" {
			e[k] = s
		}
	}
	return e
}

func setBirthday(p person, v string) error {
	if v == "" {
		p["birthdays"] = []any{}
		return nil
	}
	if !catalog.ValidBirthday(v) {
		return fmt.Errorf("googlecontacts: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", v)
	}
	var y, m, d int
	if strings.HasPrefix(v, "--") {
		m, _ = strconv.Atoi(v[2:4])
		d, _ = strconv.Atoi(v[5:7])
	} else {
		y, _ = strconv.Atoi(v[0:4])
		m, _ = strconv.Atoi(v[5:7])
		d, _ = strconv.Atoi(v[8:10])
	}
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return fmt.Errorf("googlecontacts: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", v)
	}
	date := map[string]any{"month": m, "day": d}
	if y > 0 {
		date["year"] = y
	}
	p["birthdays"] = []any{map[string]any{"date": date}}
	return nil
}

func setFields(p person, fields map[string]any) {
	es := own(p, "userDefined")
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := fields[k]
		if v == nil {
			continue
		}
		text := scalarText(v)
		at := -1
		for i, e := range es {
			if str(e, "key") == k {
				at = i
				break
			}
		}
		switch {
		case text == "" && at >= 0:
			es = append(es[:at], es[at+1:]...)
		case text == "":
		case at >= 0:
			es[at]["value"] = text
		default:
			es = append(es, map[string]any{"key": k, "value": text})
		}
	}
	put(p, "userDefined", es)
}

// scalarText is a custom field's value as Google stores it: text. A number keeps its
// exact decimal form, which is why a customer number written as 4711 reads back as
// "4711" and not as "4711.0".
func scalarText(v any) string {
	if t, ok := v.(string); ok {
		return strings.TrimSpace(t)
	}
	return fmt.Sprint(v) // a json.Number as written, a boolean as true or false
}

// hasText reports whether an entry holds a non-empty string anywhere but the named
// bookkeeping keys.
func hasText(e map[string]any, skip ...string) bool {
	for k, v := range e {
		if containsKey(skip, k) {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func containsKey(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
