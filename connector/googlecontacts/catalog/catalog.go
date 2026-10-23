// Package catalog is the Google Contacts Worker's operation table and the contact shape
// a model reads and writes (ADR-0476): which values each
// operation needs and takes, what it returns, and which keys a contact object may carry.
//
// It imports nothing of Atlas's, for the reason connector/exchange/catalog gives: the
// compiler reads these rows to refuse a model at deploy, and connector/googlecontacts
// reads the same rows to build its calls, so there is one table and nothing to keep in
// step. The connector package imports the compiler, so a table there could not be read
// back by it.
//
// # The contact a model sees
//
// The People API's person is a record of lists of entries with metadata — names[],
// emailAddresses[], phoneNumbers[], organizations[], each entry carrying its source and
// whether it is the primary one. A process wants "the customer's e-mail address". So a
// contact here is flat: the primary e-mail as `email`, every e-mail as `emails`, the
// first organization's name as `organization`, the labels as `groups`, and so on. The
// nested objects that remain (an address, an e-mail entry) keep Google's own field
// names, so Google's documentation of them stays true.
package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Result is what an operation hands back to a result variable.
type Result int

const (
	// ResultNone marks an operation that returns nothing worth keeping (a delete, a
	// label change). The compiler refuses a result variable on it.
	ResultNone Result = iota
	// ResultObject is one contact.
	ResultObject
	// ResultList is a list of contacts, empty when nothing matched.
	ResultList
)

// Areas are the groups the Modeler lists operations under, in display order.
var Areas = []string{"contact", "group", "directory"}

// Bounds holds an operation's maxResults: the value a model gets when it authors none,
// and the most it may author.
//
// The defaults are sized to the variable budget (1 MiB unless an operator raised it,
// limits.Variable): a contact as a model reads it is some 500 to 800 bytes, so a listing
// of 500 fits with room to spare, and a thousand would not reliably. The ceilings are
// for an installation that raised the budget; above them a list is an export, not a
// process variable.
type Bounds struct {
	Default int32
	Max     int32
}

// Op is one operation a model can author.
type Op struct {
	// Area groups the operation in the Modeler (one of [Areas]).
	Area string
	// Label describes the operation in an error message.
	Label string
	// NeedsContactID marks an operation on one existing contact.
	NeedsContactID bool
	// NeedsQuery marks the three lookups: the e-mail address or phone number find
	// matches exactly, or the prefix search and directory search match against.
	NeedsQuery bool
	// TakesField belongs to find-contacts alone: match the query against one custom
	// field instead of the e-mail addresses and phone numbers.
	TakesField bool
	// NeedsGroup marks the two label changes; TakesGroup lets a read narrow itself to
	// one label.
	NeedsGroup bool
	TakesGroup bool
	// NeedsContact marks the two writes, which carry a contact object.
	NeedsContact bool
	// Create distinguishes create-contact from update-contact for the contact check:
	// a new contact may be given labels, an existing one changes them through the
	// label operations.
	Create bool
	// MaxResults bounds the operation's list, and is the zero value on every
	// operation that returns none.
	MaxResults Bounds
	// Truncates marks a ranked search: it answers with the first maxResults matches,
	// as Google ranks them. Every other listing fails above its bound, because a short
	// list is a wrong answer a process would act on.
	Truncates bool
	// Result is what the operation returns; NeedsResult that it is a read whose
	// answer a model must keep.
	Result      Result
	NeedsResult bool
}

// ops is the operation table: what a process does with an address book. Find a
// contact by what it knows about a person, read one, list a label's members, add one,
// change one, remove one, and label it — plus a search of the organization's own
// directory, which is where "a colleague" lives rather than "a customer".
//
// It is deliberately not every People API method. Photos, "other contacts" (the
// addresses Gmail collected on its own), contact group administration and sync
// tokens are not steps a process takes; the generic REST Worker Type remains the way
// to reach anything else.
var ops = map[string]Op{
	"get-contact": {
		Area: "contact", Label: "read a contact",
		NeedsContactID: true, Result: ResultObject, NeedsResult: true,
	},
	"find-contacts": {
		Area: "contact", Label: "find contacts by e-mail, phone or custom field",
		NeedsQuery: true, TakesField: true, TakesGroup: true,
		MaxResults: Bounds{Default: 100, Max: 1000},
		Result:     ResultList, NeedsResult: true,
	},
	"search-contacts": {
		Area: "contact", Label: "search contacts",
		NeedsQuery: true, MaxResults: Bounds{Default: 10, Max: 30}, Truncates: true,
		Result: ResultList, NeedsResult: true,
	},
	"list-contacts": {
		Area: "contact", Label: "list contacts",
		TakesGroup: true, MaxResults: Bounds{Default: 500, Max: 10000},
		Result: ResultList, NeedsResult: true,
	},
	"create-contact": {
		Area: "contact", Label: "create a contact",
		NeedsContact: true, Create: true, Result: ResultObject,
	},
	"update-contact": {
		Area: "contact", Label: "update a contact",
		NeedsContactID: true, NeedsContact: true, Result: ResultObject,
	},
	"delete-contact": {
		Area: "contact", Label: "delete a contact",
		NeedsContactID: true,
	},
	"add-to-group": {
		Area: "group", Label: "add a contact to a label",
		NeedsContactID: true, NeedsGroup: true,
	},
	"remove-from-group": {
		Area: "group", Label: "remove a contact from a label",
		NeedsContactID: true, NeedsGroup: true,
	},
	"search-directory": {
		Area: "directory", Label: "search the organization's directory",
		NeedsQuery: true, MaxResults: Bounds{Default: 10, Max: 500}, Truncates: true,
		Result: ResultList, NeedsResult: true,
	},
}

// Lookup returns the operation of that name.
func Lookup(name string) (Op, bool) {
	op, ok := ops[name]
	return op, ok
}

// Names lists the operations, sorted, for the messages that have to say what was
// expected.
func Names() []string {
	out := make([]string, 0, len(ops))
	for name := range ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TakesResult reports whether a result variable may be authored on the operation.
func (o Op) TakesResult() bool { return o.Result != ResultNone }

// Contact keys a model writes. The singular and plural forms of a multi-valued field
// are both offered and mutually exclusive: `email` is "the" address, which on an
// update replaces the primary one and keeps the others, while `emails` is the whole
// list, replacing it.
const (
	KeyGivenName    = "givenName"
	KeyFamilyName   = "familyName"
	KeyEmail        = "email"
	KeyEmails       = "emails"
	KeyPhone        = "phone"
	KeyPhones       = "phones"
	KeyOrganization = "organization"
	KeyJobTitle     = "jobTitle"
	KeyDepartment   = "department"
	KeyAddress      = "address"
	KeyAddresses    = "addresses"
	KeyBirthday     = "birthday"
	KeyNotes        = "notes"
	KeyFields       = "fields"
	KeyGroups       = "groups"
)

// Keys a contact carries on the way out only: the resource name Google assigned and the
// display name it formats from the name parts.
const (
	KeyID          = "id"
	KeyDisplayName = "displayName"
)

// writable are the keys create-contact and update-contact accept, in the order an error
// message lists them.
var writable = []string{
	KeyGivenName, KeyFamilyName, KeyEmail, KeyEmails, KeyPhone, KeyPhones,
	KeyOrganization, KeyJobTitle, KeyDepartment, KeyAddress, KeyAddresses,
	KeyBirthday, KeyNotes, KeyFields, KeyGroups,
}

// WritableKeys lists the keys a contact object may carry when it is written.
func WritableKeys() []string { return append([]string(nil), writable...) }

// AddressKeys are the keys of one address: Google's own field names, so its
// documentation of them stays the documentation here.
var AddressKeys = []string{
	"streetAddress", "extendedAddress", "poBox", "postalCode", "city", "region",
	"country", "countryCode", "formattedValue", "type",
}

// EntryKeys are the keys of one e-mail address or phone number.
var EntryKeys = []string{"value", "type"}

// birthdayRe is the two forms a birthday is written in: a whole date, or ISO 8601's
// form without a year, which is how a person who keeps their age to themselves is
// recorded.
var birthdayRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|--\d{2}-\d{2})$`)

// ValidBirthday reports whether s is a birthday as a contact writes one.
func ValidBirthday(s string) bool { return birthdayRe.MatchString(s) }

// IsExpression reports whether a template leaf is a FEEL expression the compiler
// evaluates at call time, whose shape a deploy therefore cannot see.
func IsExpression(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(strings.TrimSpace(s), "=")
}

// CheckContact checks a contact object a model wrote: at deploy, the literal template
// (where a FEEL leaf stands for a value the check cannot see yet and passes), and at
// call time, the evaluated object (where there are none left).
//
// A key that is not one of [WritableKeys] is refused rather than dropped: a misspelt
// `famillyName` would otherwise be a write that changed nothing, reported as done. A
// null value is accepted and means "leave this as it is" — the shape a FEEL value has
// when the variable it reads is not set, which must never clear what a contact holds.
func CheckContact(op string, contact map[string]any) error {
	return checker{templates: true}.contact(op, contact)
}

// CheckValues is [CheckContact] for an evaluated contact, at call time: a string that
// starts with "=" is then a value somebody wrote, not an expression, and is held to the
// shape of its key like any other.
func CheckValues(op string, contact map[string]any) error {
	return checker{}.contact(op, contact)
}

// checker checks a contact object; templates says whether a "=…" leaf is a FEEL
// expression still to be evaluated (deploy) or a value (call time).
type checker struct{ templates bool }

func (c checker) expr(v any) bool { return c.templates && IsExpression(v) }

func (c checker) contact(op string, contact map[string]any) error {
	spec, ok := ops[op]
	if !ok || !spec.NeedsContact {
		return fmt.Errorf("googlecontacts: operation %q writes no contact", op)
	}
	if len(contact) == 0 {
		return fmt.Errorf("googlecontacts: %s needs a contact with at least one of %s", op, strings.Join(writable, ", "))
	}
	allowed := map[string]bool{}
	for _, k := range writable {
		allowed[k] = true
	}
	keys := make([]string, 0, len(contact))
	for k := range contact {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	set := 0
	for _, k := range keys {
		v := contact[k]
		switch {
		case k == KeyID:
			return fmt.Errorf("googlecontacts: %s: a contact's id is not written in the contact; name the contact with contactId", op)
		case k == KeyDisplayName:
			return fmt.Errorf("googlecontacts: %s: displayName is formatted by Google from givenName and familyName; write those instead", op)
		case !allowed[k]:
			return fmt.Errorf("googlecontacts: %s: a contact has no key %q (want %s)", op, k, strings.Join(writable, ", "))
		case k == KeyGroups && !spec.Create && v != nil:
			return fmt.Errorf("googlecontacts: %s: an existing contact's labels change through add-to-group and remove-from-group, not through groups", op)
		}
		if v != nil {
			set++
		}
		if c.expr(v) || v == nil {
			continue
		}
		if err := c.value(op, k, v); err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{KeyEmail, KeyEmails}, {KeyPhone, KeyPhones}, {KeyAddress, KeyAddresses}} {
		if contact[pair[0]] != nil && contact[pair[1]] != nil {
			return fmt.Errorf("googlecontacts: %s: the contact sets both %s and %s; %s is the primary one and %s the whole list, so give one of them", op, pair[0], pair[1], pair[0], pair[1])
		}
	}
	// Every value null: at deploy an author wrote a contact of nothing, and at call time
	// a create would make an empty contact. An update of nothing is what "null changes
	// nothing" says it is — a form submitted without a change — and is let through.
	if set == 0 && !c.anyExpression(contact) && (c.templates || spec.Create) {
		return fmt.Errorf("googlecontacts: %s: every value of the contact is null, so there is nothing to write", op)
	}
	return nil
}

func (c checker) anyExpression(contact map[string]any) bool {
	for _, v := range contact {
		if c.expr(v) {
			return true
		}
	}
	return false
}

// value checks one key's value.
func (c checker) value(op, key string, v any) error {
	bad := func(want string) error {
		return fmt.Errorf("googlecontacts: %s: %s must be %s, not %s", op, key, want, typeName(v))
	}
	switch key {
	case KeyGivenName, KeyFamilyName, KeyOrganization, KeyJobTitle, KeyDepartment, KeyNotes, KeyEmail, KeyPhone:
		if _, ok := v.(string); !ok {
			return bad("text")
		}
	case KeyBirthday:
		s, ok := v.(string)
		if !ok {
			return bad(`text ("1980-05-17", or "--05-17" without a year)`)
		}
		if s != "" && !ValidBirthday(s) {
			return fmt.Errorf("googlecontacts: %s: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", op, s)
		}
	case KeyEmails, KeyPhones:
		list, ok := v.([]any)
		if !ok {
			return bad(`a list of {"value", "type"} entries or of plain values`)
		}
		for i, e := range list {
			if err := c.entry(op, key, i, e); err != nil {
				return err
			}
		}
	case KeyAddress:
		if s, ok := v.(string); ok && s == "" {
			return nil // "" removes the primary address
		}
		return c.address(op, key, v)
	case KeyAddresses:
		list, ok := v.([]any)
		if !ok {
			return bad("a list of addresses")
		}
		for i, a := range list {
			if err := c.address(op, fmt.Sprintf("%s[%d]", key, i+1), a); err != nil {
				return err
			}
		}
	case KeyFields:
		m, ok := v.(map[string]any)
		if !ok {
			return bad("an object of custom field names and their values")
		}
		for name, fv := range m {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("googlecontacts: %s: a custom field needs a name", op)
			}
			switch fv.(type) {
			case nil, string, bool, float64:
			default:
				if !isNumber(fv) {
					return fmt.Errorf("googlecontacts: %s: custom field %q must be text, a number or a boolean, not %s", op, name, typeName(fv))
				}
			}
		}
	case KeyGroups:
		list, ok := v.([]any)
		if !ok {
			return bad("a list of label names")
		}
		for _, g := range list {
			if c.expr(g) {
				continue
			}
			if s, ok := g.(string); !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("googlecontacts: %s: groups must name labels, not %s", op, typeName(g))
			}
		}
	}
	return nil
}

// entry checks one e-mail address or phone number of a list: a plain value, or an
// object of [EntryKeys] with a value.
func (c checker) entry(op, key string, i int, e any) error {
	if c.expr(e) {
		return nil
	}
	switch t := e.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("googlecontacts: %s: %s[%d] is empty", op, key, i+1)
		}
		return nil
	case map[string]any:
		for k, v := range t {
			if !contains(EntryKeys, k) {
				return fmt.Errorf("googlecontacts: %s: %s[%d] has no key %q (want value, type)", op, key, i+1, k)
			}
			if _, ok := v.(string); !ok && !c.expr(v) && v != nil {
				return fmt.Errorf("googlecontacts: %s: %s[%d].%s must be text", op, key, i+1, k)
			}
		}
		if v, ok := t["value"].(string); !c.expr(t["value"]) && (!ok || strings.TrimSpace(v) == "") {
			return fmt.Errorf("googlecontacts: %s: %s[%d] needs a value", op, key, i+1)
		}
		return nil
	}
	return fmt.Errorf("googlecontacts: %s: %s[%d] must be a value or a {\"value\", \"type\"} object, not %s", op, key, i+1, typeName(e))
}

// address checks one address object.
func (c checker) address(op, what string, a any) error {
	if c.expr(a) {
		return nil
	}
	m, ok := a.(map[string]any)
	if !ok {
		return fmt.Errorf("googlecontacts: %s: %s must be an address object (%s), not %s", op, what, strings.Join(AddressKeys, ", "), typeName(a))
	}
	for k, v := range m {
		if !contains(AddressKeys, k) {
			return fmt.Errorf("googlecontacts: %s: %s has no key %q (want %s)", op, what, k, strings.Join(AddressKeys, ", "))
		}
		if _, ok := v.(string); !ok && !c.expr(v) && v != nil {
			return fmt.Errorf("googlecontacts: %s: %s.%s must be text", op, what, k)
		}
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// isNumber recognizes the number types a JSON decoder produces: float64, or a
// json.Number, which this package names by its method rather than importing
// encoding/json for.
func isNumber(v any) bool {
	_, ok := v.(interface{ Int64() (int64, error) })
	return ok
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "text"
	case bool:
		return "a boolean"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	if _, ok := v.(float64); ok || isNumber(v) {
		return "a number"
	}
	return fmt.Sprintf("%T", v)
}

// Contact-id and label forms. A contact is addressed by the resource name Google gave
// it, `people/c…`, or by the id alone; a label by its name as the person sees it, or
// by its resource name `contactGroups/…`. Both are checked before they are put into a
// URL path, so a value cannot address anything but a contact or a label.
var (
	contactIDRe = regexp.MustCompile(`^(people/)?[A-Za-z0-9_-]{1,128}$`)
	groupIDRe   = regexp.MustCompile(`^contactGroups/[A-Za-z0-9_-]{1,128}$`)
)

// ContactResourceName turns a contact id as a model holds it into the resource name the
// People API addresses, refusing anything else.
func ContactResourceName(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !contactIDRe.MatchString(id) {
		return "", fmt.Errorf("googlecontacts: %q is not a contact id (want people/c… as a contact's id reads, or the part after people/)", clip(id))
	}
	if !strings.HasPrefix(id, "people/") {
		id = "people/" + id
	}
	return id, nil
}

// IsGroupResourceName reports whether s names a label by its resource name rather than
// by the name a person gave it.
func IsGroupResourceName(s string) bool { return groupIDRe.MatchString(strings.TrimSpace(s)) }

// clip shortens a refused value for an error message, so a value that is not an id —
// a whole contact pasted into the wrong field — does not become the message.
func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
