// Package catalog is the CardDAV Worker's operation table and the contact shape a model
// reads and writes (ADR-0490): which values each operation needs and
// takes, what it returns, and which keys a contact object may carry.
//
// It imports nothing of atlas's, for the reason connector/exchange/catalog gives: the
// compiler reads these rows to refuse a model at deploy, and connector/carddav reads the
// same rows to build its calls, so there is one table and nothing to keep in step.
//
// # The contact a model sees
//
// It is the Google Contacts Worker's flat contact (ADR-0476), on purpose: a model that
// moves from a Google address book to a CardDAV one changes its Worker and little else.
// The keys are the same and mean the same; where vCard cannot hold what Google holds,
// the key is refused here rather than accepted and dropped. That is not a claim that the
// two Worker Types are interchangeable — ADR-0480 keeps such a claim for a contract that
// has been shown, and the record lists where they differ.
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
	// group change). The compiler refuses a result variable on it.
	ResultNone Result = iota
	// ResultObject is one contact.
	ResultObject
	// ResultList is a list of contacts, empty when nothing matched.
	ResultList
)

// Areas are the groups the Modeler lists operations under, in display order.
var Areas = []string{"contact", "group"}

// Bounds holds an operation's maxResults: the value a model gets when it authors none,
// and the most it may author. They are Google Contacts' bounds, sized to the variable
// budget for the same contact (limits.Variable); only the search, which here runs on the
// worker and not against a server's limit, takes its own ceiling.
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
	// NeedsQuery marks the two lookups: the e-mail address or phone number a find
	// matches exactly, or the text a search looks for.
	NeedsQuery bool
	// TakesField belongs to find-contacts alone: match the query against one custom
	// field instead of the e-mail addresses and phone numbers.
	TakesField bool
	// NeedsGroup marks the two group changes; TakesGroup lets a read narrow itself to
	// one group.
	NeedsGroup bool
	TakesGroup bool
	// NeedsContact marks the two writes, which carry a contact object.
	NeedsContact bool
	// Create distinguishes create-contact from update-contact for the contact check: a
	// new contact may be given groups, an existing one changes them through the group
	// operations.
	Create bool
	// MaxResults bounds the operation's list, and is the zero value on every operation
	// that returns none.
	MaxResults Bounds
	// Truncates marks the search: it answers with the first maxResults matches in the
	// listing's order. Every other listing fails above its bound, because a short list
	// is a wrong answer a process would act on.
	Truncates bool
	// Result is what the operation returns; NeedsResult that it is a read whose answer
	// a model must keep.
	Result      Result
	NeedsResult bool
}

// ops is the operation table: Google Contacts' table without its directory search,
// which CardDAV has no counterpart for. Photos, address book administration, sync
// tokens and creating groups are not steps a process takes.
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
		NeedsQuery: true, MaxResults: Bounds{Default: 10, Max: 100}, Truncates: true,
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
		Area: "group", Label: "add a contact to a group",
		NeedsContactID: true, NeedsGroup: true,
	},
	"remove-from-group": {
		Area: "group", Label: "remove a contact from a group",
		NeedsContactID: true, NeedsGroup: true,
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
// are both offered and mutually exclusive: `email` is "the" address, which on an update
// replaces the preferred one and keeps the others, while `emails` is the whole list,
// replacing it.
const (
	KeyDisplayName  = "displayName"
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

// KeyID is the key a contact carries on the way out only: its resource name in the
// address book, which a model hands back as contactId.
const KeyID = "id"

// writable are the keys create-contact and update-contact accept, in the order an error
// message lists them. displayName is among them, unlike in Google Contacts: vCard
// stores FN rather than formatting it, and a company has one and no name parts.
var writable = []string{
	KeyDisplayName, KeyGivenName, KeyFamilyName, KeyEmail, KeyEmails, KeyPhone, KeyPhones,
	KeyOrganization, KeyJobTitle, KeyDepartment, KeyAddress, KeyAddresses,
	KeyBirthday, KeyNotes, KeyFields, KeyGroups,
}

// WritableKeys lists the keys a contact object may carry when it is written.
func WritableKeys() []string { return append([]string(nil), writable...) }

// AddressKeys are the keys of one address: Google Contacts' names for the seven
// components of vCard's ADR, and its type. Google's countryCode and formattedValue have
// no place in ADR and are therefore not keys here.
var AddressKeys = []string{
	"streetAddress", "extendedAddress", "poBox", "postalCode", "city", "region", "country", "type",
}

// EntryKeys are the keys of one e-mail address or phone number.
var EntryKeys = []string{"value", "type"}

// The two forms a CardDAV server keeps groups in, one per Worker (its bundle's
// "groups"). They do not mix: a client that writes one does not read the other.
const (
	// GroupsCategories keeps a group as a value of the contact's CATEGORIES (RFC 6350
	// §6.7.1) — Nextcloud, ownCloud and most self-hosted servers.
	GroupsCategories = "categories"
	// GroupsVCards keeps a group as a vCard of its own whose member lines name its
	// contacts — Apple's form, so iCloud's and Fastmail's.
	GroupsVCards = "group-vcards"
)

// ParseGroupStyle reads a bundle's "groups" setting; empty is [GroupsCategories].
func ParseGroupStyle(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", GroupsCategories:
		return GroupsCategories, nil
	case GroupsVCards:
		return GroupsVCards, nil
	}
	return "", fmt.Errorf("carddav: the bundle's groups is %q; want %q (a group is a value of the contact's CATEGORIES — Nextcloud, ownCloud, most servers) or %q (a group is a vCard of its own — iCloud, Fastmail)", clip(s), GroupsCategories, GroupsVCards)
}

// birthdayRe is the two forms a birthday is written in: a whole date, or ISO 8601's form
// without a year.
var birthdayRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|--\d{2}-\d{2})$`)

// ValidBirthday reports whether s is a birthday as a contact writes one.
func ValidBirthday(s string) bool { return birthdayRe.MatchString(s) }

// IsExpression reports whether a template leaf is a FEEL expression the compiler
// evaluates at call time, whose shape a deploy therefore cannot see.
func IsExpression(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(strings.TrimSpace(s), "=")
}

// fieldNameRe is what a custom field's name may be: the part of a vCard extension
// property's name after "X-" (RFC 6350 §3.3), which is letters, digits and hyphens.
var fieldNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)

// vendorProperties and vendorPrefixes are the extension properties other clients keep
// in a card for their own purposes: Apple's labels and grouping, the group cards of
// Apple's address book server, Evolution's, Thunderbird's, Outlook's and KDE's
// bookkeeping, phonetic names and social profiles. They are not custom fields a person
// gave a contact, so they are neither read as one nor written as one — a field written
// must read back as the same field, and must not change what another client meant.
var (
	vendorProperties = map[string]bool{
		"X-ABUID": true, "X-ABLABEL": true, "X-ABADR": true, "X-ABDATE": true,
		"X-ABRELATEDNAMES": true, "X-ABSHOWAS": true, "X-SOCIALPROFILE": true,
	}
	vendorPrefixes = []string{
		"X-ADDRESSBOOKSERVER-", "X-APPLE-", "X-EVOLUTION-", "X-MOZILLA-", "X-MS-",
		"X-PHONETIC-", "X-KADDRESSBOOK-", "X-ANDROID-",
	}
)

// IsVendorProperty reports whether a vCard property name is one another client keeps
// for itself rather than a custom field.
func IsVendorProperty(name string) bool {
	up := strings.ToUpper(name)
	if vendorProperties[up] {
		return true
	}
	for _, p := range vendorPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	return false
}

// CheckFieldName checks a custom field's name: a vCard property name once "X-" is put
// in front of it, and not one of another client's own.
func CheckFieldName(name string) error {
	if !fieldNameRe.MatchString(name) {
		return fmt.Errorf("custom field %q must be named with letters, digits and hyphens (it is stored as the vCard property X-%s), e.g. Kundennummer or Kunden-Nr", clip(name), clip(name))
	}
	if IsVendorProperty("X-" + name) {
		return fmt.Errorf("custom field %q is a property another client keeps for itself (X-%s); choose another name", name, name)
	}
	return nil
}

// CheckContact checks a contact object a model wrote: at deploy, the literal template
// (where a FEEL leaf stands for a value the check cannot see yet and passes), and at
// call time, the evaluated object (where there are none left).
//
// A key that is not one of [WritableKeys] is refused rather than dropped: a misspelt
// `famillyName` would otherwise be a write that changed nothing, reported as done. A
// null value is accepted and means "leave this as it is".
func CheckContact(op string, contact map[string]any) error {
	return checker{templates: true}.contact(op, contact)
}

// CheckValues is [CheckContact] for an evaluated contact, at call time: a string that
// starts with "=" is then a value somebody wrote, not an expression.
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
		return fmt.Errorf("carddav: operation %q writes no contact", op)
	}
	if len(contact) == 0 {
		return fmt.Errorf("carddav: %s needs a contact with at least one of %s", op, strings.Join(writable, ", "))
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
			return fmt.Errorf("carddav: %s: a contact's id is not written in the contact; name the contact with contactId", op)
		case !allowed[k]:
			return fmt.Errorf("carddav: %s: a contact has no key %q (want %s)", op, k, strings.Join(writable, ", "))
		case k == KeyGroups && !spec.Create && v != nil:
			return fmt.Errorf("carddav: %s: an existing contact's groups change through add-to-group and remove-from-group, not through groups", op)
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
			return fmt.Errorf("carddav: %s: the contact sets both %s and %s; %s is the preferred one and %s the whole list, so give one of them", op, pair[0], pair[1], pair[0], pair[1])
		}
	}
	// Every value null: at deploy an author wrote a contact of nothing, and at call time
	// a create would make an empty contact. An update of nothing is a form submitted
	// without a change, and is let through.
	if set == 0 && !c.anyExpression(contact) && (c.templates || spec.Create) {
		return fmt.Errorf("carddav: %s: every value of the contact is null, so there is nothing to write", op)
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
		return fmt.Errorf("carddav: %s: %s must be %s, not %s", op, key, want, typeName(v))
	}
	switch key {
	case KeyDisplayName, KeyGivenName, KeyFamilyName, KeyOrganization, KeyJobTitle, KeyDepartment, KeyNotes, KeyEmail, KeyPhone:
		if _, ok := v.(string); !ok {
			return bad("text")
		}
	case KeyBirthday:
		s, ok := v.(string)
		if !ok {
			return bad(`text ("1980-05-17", or "--05-17" without a year)`)
		}
		if s != "" && !ValidBirthday(s) {
			return fmt.Errorf("carddav: %s: birthday %q is not a date; write 1980-05-17, or --05-17 without a year", op, clip(s))
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
			return nil // "" removes the preferred address
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
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("carddav: %s: a custom field needs a name", op)
			}
			if err := CheckFieldName(name); err != nil {
				return fmt.Errorf("carddav: %s: %w", op, err)
			}
			fv := m[name]
			switch fv.(type) {
			case nil, string, bool, float64:
			default:
				if !isNumber(fv) {
					return fmt.Errorf("carddav: %s: custom field %q must be text, a number or a boolean, not %s", op, name, typeName(fv))
				}
			}
		}
	case KeyGroups:
		list, ok := v.([]any)
		if !ok {
			return bad("a list of group names")
		}
		for _, g := range list {
			if c.expr(g) {
				continue
			}
			if s, ok := g.(string); !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("carddav: %s: groups must name groups, not %s", op, typeName(g))
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
			return fmt.Errorf("carddav: %s: %s[%d] is empty", op, key, i+1)
		}
		return nil
	case map[string]any:
		for k, v := range t {
			if !contains(EntryKeys, k) {
				return fmt.Errorf("carddav: %s: %s[%d] has no key %q (want value, type)", op, key, i+1, k)
			}
			if _, ok := v.(string); !ok && !c.expr(v) && v != nil {
				return fmt.Errorf("carddav: %s: %s[%d].%s must be text", op, key, i+1, k)
			}
		}
		if v, ok := t["value"].(string); !c.expr(t["value"]) && (!ok || strings.TrimSpace(v) == "") {
			return fmt.Errorf("carddav: %s: %s[%d] needs a value", op, key, i+1)
		}
		return nil
	}
	return fmt.Errorf("carddav: %s: %s[%d] must be a value or a {\"value\", \"type\"} object, not %s", op, key, i+1, typeName(e))
}

// address checks one address object.
func (c checker) address(op, what string, a any) error {
	if c.expr(a) {
		return nil
	}
	m, ok := a.(map[string]any)
	if !ok {
		return fmt.Errorf("carddav: %s: %s must be an address object (%s), not %s", op, what, strings.Join(AddressKeys, ", "), typeName(a))
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !contains(AddressKeys, k) {
			return fmt.Errorf("carddav: %s: %s has no key %q (want %s)", op, what, k, strings.Join(AddressKeys, ", "))
		}
		if _, ok := m[k].(string); !ok && !c.expr(m[k]) && m[k] != nil {
			return fmt.Errorf("carddav: %s: %s.%s must be text", op, what, k)
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

// contactIDRe is a contact id: the last segment of the contact's URL in the address
// book, as the server spells it — the characters RFC 3986 allows in a path segment,
// percent-encoded where they must be. It is checked before it is put into a URL, so a
// value cannot address anything but a member of the Worker's own address book.
var contactIDRe = regexp.MustCompile(`^(?:[A-Za-z0-9\-._~!$&'()*+,;=:@]|%[0-9A-Fa-f]{2}){1,255}$`)

// ContactID is a contact id as a model holds it, trimmed.
func ContactID(id string) string { return strings.TrimSpace(id) }

// CheckContactID refuses anything that is not one segment of a path: a slash, a dot
// segment, or an encoded slash, backslash or NUL a server might decode into one.
func CheckContactID(id string) error {
	id = ContactID(id)
	lower := strings.ToLower(id)
	if !contactIDRe.MatchString(id) || id == "." || id == ".." ||
		strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
		return fmt.Errorf("carddav: %q is not a contact id (want the id a read, a find or a create answered with, e.g. 5F3A….vcf)", clip(id))
	}
	return nil
}

// clip shortens a refused value for an error message, so a value that is not an id — a
// whole contact pasted into the wrong field — does not become the message.
func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
