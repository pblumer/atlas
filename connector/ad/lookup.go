package ad

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/workersdk/lookup"
)

// A form lookup against Active Directory: the AD twin of Entra's (ADR-0473, amended
// 2026-11-04). A person types a few letters into a form field and sees the first
// matching accounts of the directory a Worker names, read with that Worker's own bind
// account and never written anywhere.
//
// The shape is Entra's on purpose — a table of resources, each with what may be
// searched, filtered, read and submitted, held here and viewed by the server — so a
// form author meets one model of "a directory field" whichever directory sits behind
// it. What differs is LDAP:
//
//   - The filter is written here from the table, and every term and value goes through
//     RFC 4515 escaping ([goldap.EscapeFilter]). A person typing "*)(objectClass=*" is
//     searching for that text, not widening the search.
//   - A record is identified by its sAMAccountName, or its userPrincipalName when the
//     field says so — the names processes already address AD accounts by. Never by
//     objectGUID: it is binary, and a binary value is not something a form can carry.
//   - The search base is the directory's own default naming context, read from the
//     rootDSE on each lookup, so a Worker needs no extra setting to be searchable. A
//     field narrows by its equality filters (department, company), as an Entra field
//     does.
//   - A lookup is one page and has no continuation. An LDAP paged-results cookie is
//     bound to the connection that asked, and a lookup's connection ends with its
//     answer; the person narrows by typing another letter, which is what the page size
//     already asks of them.

// LookupResource is one kind of AD object as a form may search it.
type LookupResource struct {
	// Class is the LDAP filter that selects the kind. It is and-ed into every search.
	Class string
	// ValueProperty identifies a record: what a form submits, unless the field names
	// another of ValueProperties.
	ValueProperty string
	// ValueProperties are the properties a field may submit instead. Each is unique in
	// the directory, so a value lookup finds one record.
	ValueProperties []string
	// Searchable are the attributes a term may be a prefix of.
	Searchable []string
	// Filterable are the attributes an equality filter may name.
	Filterable []string
	// Readable are the attributes a lookup may return. Nothing outside it is asked of
	// the directory, and nothing outside what a query names is passed on. Every one is
	// single-valued text in AD's schema, so a record is a flat object of strings.
	Readable []string
	// ActiveOnly says whether an ActiveOnly query is answerable: users carry the
	// "account disabled" bit of userAccountControl, groups have no such flag.
	ActiveOnly bool
}

// LookupResources are the kinds a form may search: user accounts, and security or
// distribution groups.
//
// Users are selected as objectClass user without objectClass computer, because a
// computer account is a user in AD's schema and a person choosing a colleague should
// not be offered a workstation. Contacts are not users and are not offered either.
var LookupResources = map[string]LookupResource{
	"users": {
		Class:           "(&(objectClass=user)(!(objectClass=computer)))",
		ValueProperty:   "sAMAccountName",
		ValueProperties: []string{"sAMAccountName", "userPrincipalName"},
		Searchable:      []string{"displayName", "givenName", "sn", "mail", "sAMAccountName", "userPrincipalName"},
		Filterable:      []string{"department", "company"},
		Readable:        []string{"sAMAccountName", "userPrincipalName", "displayName", "givenName", "sn", "mail", "department", "company", "title"},
		ActiveOnly:      true,
	},
	"groups": {
		Class:           "(objectClass=group)",
		ValueProperty:   "sAMAccountName",
		ValueProperties: []string{"sAMAccountName"},
		Searchable:      []string{"cn", "displayName", "sAMAccountName", "mail"},
		Readable:        []string{"sAMAccountName", "cn", "displayName", "description", "mail"},
	},
}

// disabledBit is the filter for an account whose ACCOUNTDISABLE bit (2) is set, through
// AD's bitwise-AND matching rule. Negated, it is "only enabled accounts" — asked of the
// directory, not compared here.
const disabledBit = "(userAccountControl:1.2.840.113556.1.4.803:=2)"

// samAccountName is the shape of a logon name a value may take: what AD allows in one
// (no " / \ [ ] : ; | = , + * ? < > @), at most 256 characters, no control character
// and no space at either end. It is narrower than nothing and wider than a user's 20
// characters, because groups share the attribute. The value is escaped into the filter
// regardless; the shape is what lets a submission be refused before anyone is asked.
var samAccountName = regexp.MustCompile(`^[^"/\\\[\]:;|=,+*?<>@\s]([^"/\\\[\]:;|=,+*?<>@]{0,254}[^"/\\\[\]:;|=,+*?<>@\s])?$`)

// principalName is the shape of a user principal name: one "@" with a local part and a
// domain suffix.
var principalName = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+=?^_{|}~-]+@[A-Za-z0-9.-]+$`)

// ValidLookupValue reports whether v has the shape of a record's value in resource.
func ValidLookupValue(resource, v string) bool {
	res, ok := LookupResources[resource]
	if !ok {
		return false
	}
	return ValidLookupValueAs(resource, res.ValueProperty, v)
}

// ValidLookupValueAs is ValidLookupValue for a value read by property. A property the
// resource does not admit as a value holds no valid value.
func ValidLookupValueAs(resource, property, v string) bool {
	res, ok := LookupResources[resource]
	if !ok || (property != res.ValueProperty && !slices.Contains(res.ValueProperties, property)) {
		return false
	}
	if utf8.RuneCountInString(v) > lookup.MaxValueLength || !lookup.CleanText(v) {
		return false
	}
	switch property {
	case "sAMAccountName":
		return samAccountName.MatchString(v)
	case "userPrincipalName":
		return principalName.MatchString(v)
	}
	return false
}

// valueProperty is the property a query identifies records by: its own choice when the
// resource admits it, the resource's identifier when it names none, and "" otherwise.
func (res LookupResource) valueProperty(q lookup.Query) string {
	switch {
	case q.ValueProperty == "":
		return res.ValueProperty
	case q.ValueProperty == res.ValueProperty || slices.Contains(res.ValueProperties, q.ValueProperty):
		return q.ValueProperty
	}
	return ""
}

// CheckLookup holds a query to its resource's allowlist and to the bounds every lookup
// keeps. The server runs the same check from this same table before a query is sent;
// running it here too is what makes the table the Worker's rule.
func CheckLookup(q lookup.Query) (LookupResource, error) {
	res, ok := LookupResources[q.Resource]
	if !ok {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	vp := res.valueProperty(q)
	if vp == "" || len(q.Properties) == 0 || !slices.Contains(q.Properties, vp) {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.Properties {
		if !slices.Contains(res.Readable, p) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	if q.PageSize < 0 || q.PageSize > lookup.MaxPageSize {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	seen := map[string]bool{}
	for _, f := range q.Filters {
		if !slices.Contains(res.Filterable, f.Property) || seen[f.Property] {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
		seen[f.Property] = true
		if f.Value == "" || utf8.RuneCountInString(f.Value) > lookup.MaxValueLength || !lookup.CleanText(f.Value) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	// Refused rather than ignored, as Entra refuses it: a form that asked for enabled
	// accounts only must not be handed disabled ones.
	if q.ActiveOnly && !res.ActiveOnly {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	// There is no continuation to accept (see the note at the top of this file); one
	// arriving is a query this Worker did not hand out.
	if q.Next != "" {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	if q.Value != "" {
		if !ValidLookupValueAs(q.Resource, vp, q.Value) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
		return res, nil
	}
	if utf8.RuneCountInString(q.Search) > lookup.MaxSearchLength || !lookup.CleanText(q.Search) {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	if strings.TrimSpace(q.Search) != "" && len(q.SearchFields) == 0 {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.SearchFields {
		if !slices.Contains(res.Searchable, p) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	return res, nil
}

// RunLookup answers one lookup against the directory connector names, from the
// directories this Worker holds. Only a Console-configured directory can be searched:
// a task that carries its own URL names no Worker a form could name.
func RunLookup(_ context.Context, connector string, q lookup.Query, dialer Dialer, dirs *Registry) (lookup.Page, error) {
	if dirs == nil {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	d, ok := dirs.Client(connector)
	if !ok || d.URL == "" {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	res, err := CheckLookup(q)
	if err != nil {
		return lookup.Page{}, err
	}
	conn, err := dialer.Dial(d.URL, d.BindDN, d.Password, d.StartTLS)
	if err != nil {
		return lookup.Page{}, lookupFailure(err)
	}
	defer conn.Close()
	base, err := namingContext(conn)
	if err != nil {
		return lookup.Page{}, err
	}
	vp := res.valueProperty(q)
	size := q.PageSize
	if size == 0 {
		size = lookup.DefaultPageSize
	}
	if q.Value != "" {
		// One record at most is offered; asking for two would only tell a duplicate
		// apart, which a unique attribute does not have.
		size = 1
	}
	entries, err := conn.Search(SearchRequest{
		BaseDN:     base,
		Scope:      "sub",
		Filter:     lookupFilter(res, vp, q),
		Attributes: q.Properties,
		Limit:      int32(size),
	})
	if err != nil {
		return lookup.Page{}, lookupFailure(err)
	}
	items := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		rec := project(e.Attributes, q.Properties)
		if v, _ := rec[vp].(string); v == "" {
			// A record without its value cannot be chosen, so it is not offered.
			continue
		}
		items = append(items, rec)
	}
	// A directory answers in no promised order. A person reads the page, so it is
	// sorted by what they read first.
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(sortKey(items[i], vp)) < strings.ToLower(sortKey(items[j], vp))
	})
	return lookup.Page{Items: items}, nil
}

// lookupFilter writes a lookup's filter: the resource's class, then either the value
// (a value lookup) or a prefix of the term over each search attribute joined by or,
// then each equality filter and the enabled-only clause. Every literal goes through
// [goldap.EscapeFilter]; that is the whole defence, and it is enough, because RFC 4515
// gives a value no other way out of its assertion.
func lookupFilter(res LookupResource, vp string, q lookup.Query) string {
	var b strings.Builder
	b.WriteString("(&")
	b.WriteString(res.Class)
	if q.Value != "" {
		b.WriteString("(" + vp + "=" + goldap.EscapeFilter(q.Value) + ")")
	} else if term := lookup.NormalizeSearch(q.Search); term != "" {
		b.WriteString("(|")
		for _, p := range q.SearchFields {
			b.WriteString("(" + p + "=" + goldap.EscapeFilter(term) + "*)")
		}
		b.WriteString(")")
	}
	for _, f := range q.Filters {
		b.WriteString("(" + f.Property + "=" + goldap.EscapeFilter(f.Value) + ")")
	}
	if q.ActiveOnly {
		b.WriteString("(!" + disabledBit + ")")
	}
	b.WriteString(")")
	return b.String()
}

// namingContext reads the directory's default naming context from its rootDSE: where
// its accounts are. A directory that names none cannot be searched without a base, and
// says so as unavailable rather than being searched from nowhere.
func namingContext(conn Conn) (string, error) {
	entries, err := conn.Search(SearchRequest{Scope: "base", Attributes: []string{"defaultNamingContext"}})
	if err != nil {
		return "", lookupFailure(err)
	}
	for _, e := range entries {
		for name, vals := range e.Attributes {
			if strings.EqualFold(name, "defaultNamingContext") && len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
				return vals[0], nil
			}
		}
	}
	return "", lookup.Fail(lookup.CodeUnavailable)
}

// project keeps exactly the attributes a query named, each as its first value, matched
// without regard to case as the directory matches attribute names. One the directory
// did not send reads as null, so every record of a page has the same shape.
func project(attrs map[string][]string, props []string) map[string]any {
	out := make(map[string]any, len(props))
	for _, p := range props {
		out[p] = nil
		for name, vals := range attrs {
			if strings.EqualFold(name, p) && len(vals) > 0 {
				out[p] = vals[0]
				break
			}
		}
	}
	return out
}

// sortKey is what a record is ordered by: its display name, or its value when it has
// none.
func sortKey(rec map[string]any, vp string) string {
	if s, _ := rec["displayName"].(string); s != "" {
		return s
	}
	s, _ := rec[vp].(string)
	return s
}

// lookupFailure turns whatever the directory or the network answered into a lookup
// code. The code is all that leaves this function: an LDAP error can carry the filter,
// and the filter carries the person's term.
func lookupFailure(err error) error {
	var le *lookup.Error
	if errors.As(err, &le) {
		return le
	}
	if goldap.IsErrorAnyOf(err,
		goldap.LDAPResultInvalidCredentials,
		goldap.LDAPResultInsufficientAccessRights,
		goldap.LDAPResultInappropriateAuthentication,
		goldap.LDAPResultConfidentialityRequired,
		goldap.LDAPResultStrongAuthRequired,
		goldap.LDAPResultUnwillingToPerform,
		goldap.LDAPResultInappropriateMatching,
		goldap.LDAPResultUndefinedAttributeType,
		goldap.LDAPResultFilterError,
	) {
		return lookup.Fail(lookup.CodeRefused)
	}
	return lookup.Fail(lookup.CodeUnavailable)
}
