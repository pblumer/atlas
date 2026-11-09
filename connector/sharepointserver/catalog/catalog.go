// Package catalog is the SharePoint Server Worker's operation table: which parameters
// a model must and may author for each low-code operation, and how each value is
// checked (ADR-draft-sharepoint-server-worker).
//
// It imports nothing of Atlas's for the reason Exchange's catalog does: the compiler
// (deploy-time checks) and connector/sharepointserver (call-time checks) read the same
// rows, so the rule a deploy enforces cannot drift from the one the Worker applies.
//
// A site collection is this Worker's unit: on SharePoint Server there is no Microsoft
// 365 group behind it. A model names one by its leaf under the Worker's managed path
// (projekt-alpha for http://sp01/sites/projekt-alpha), never by a URL, so a task cannot
// reach outside the web application the Worker was configured for.
package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Result is what an operation hands back to a result variable.
type Result int

const (
	// ResultNone marks an operation that returns nothing worth keeping.
	ResultNone Result = iota
	// ResultObject is one site, or null when there is none.
	ResultObject
	// ResultList is an array of sites.
	ResultList
)

// Areas are the groups the Modeler lists operations under, in display order.
var Areas = []string{"site", "access", "storage"}

// DefaultTemplate is the template create-site uses when a model names none:
// Microsoft's New-SPSite reference calls STS#0 "the Team site template".
const DefaultTemplate = "STS#0"

// The group names update-group-members takes: the site's three default groups, which
// SharePoint titles after the site and in the site's language, so a model cannot name
// them by title.
const (
	GroupOwners   = "owners"
	GroupMembers  = "members"
	GroupVisitors = "visitors"
)

// Listing bounds: a site listing is capped for the reason an Exchange one is, and the
// ceiling keeps a process variable a process variable.
const (
	DefaultLimit = 200
	MaxLimit     = 5000
)

// Op is one low-code operation.
type Op struct {
	// Area groups the operation in the Modeler (one of [Areas]).
	Area string
	// Label describes the operation in an error message.
	Label string
	// Required are the parameters a model must author; Optional the ones it may. An
	// undeclared parameter is refused.
	Required []string
	Optional []string
	// AnyOf requires at least one of Optional: a change that changes nothing is a call
	// made for nothing.
	AnyOf bool
	// Result is what the operation returns; NeedsResult that it is a read whose result a
	// model must keep.
	Result      Result
	NeedsResult bool
}

// Ops is the operation table.
var Ops = map[string]Op{
	"create-site": {Area: "site", Label: "create a team site",
		Required: []string{"Site", "Name", "OwnerAlias"},
		Optional: []string{"SecondaryOwnerAlias", "OwnerEmail", "Description", "Language", "Template", "ContentDatabase", "QuotaTemplate", "Properties"},
		Result:   ResultObject},
	"get-site": {Area: "site", Label: "read a site",
		Required: []string{"Site"}, Result: ResultObject, NeedsResult: true},
	"list-sites": {Area: "site", Label: "list the sites of the web application",
		Optional: []string{"Limit"}, Result: ResultList, NeedsResult: true},
	"delete-site": {Area: "site", Label: "delete a site to the recycle bin",
		Required: []string{"Site"}},
	"restore-site": {Area: "site", Label: "restore a deleted site",
		Required: []string{"Site"}, Optional: []string{"ContentDatabase"}, Result: ResultObject},
	"set-site-owners": {Area: "access", Label: "set a site's owners",
		Required: []string{"Site", "OwnerAlias"}, Optional: []string{"SecondaryOwnerAlias"}},
	"update-site-admins": {Area: "access", Label: "change a site's administrators",
		Required: []string{"Site"}, Optional: []string{"Add", "Remove"}, AnyOf: true},
	"update-group-members": {Area: "access", Label: "change the members of a default group",
		Required: []string{"Site", "Group"}, Optional: []string{"Add", "Remove"}, AnyOf: true},
	"set-site-quota": {Area: "storage", Label: "set a site's storage quota",
		Required: []string{"Site", "QuotaGB"}, Optional: []string{"WarningPercent"}},
}

// Names lists the operations, sorted.
func Names() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup finds an operation by name.
func Lookup(name string) (Op, bool) {
	o, ok := Ops[name]
	return o, ok
}

var (
	siteLeaf     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
	templateName = regexp.MustCompile(`^[A-Za-z0-9_.-]+#[0-9]+$`)
	databaseName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_ .-]{0,127}$`)
	propertyKey  = regexp.MustCompile(`^atlas\.[a-z0-9.-]{1,64}$`)
	emailAddress = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

// validAccount reads an account or AD group as SharePoint's cmdlets take one:
// DOMAIN\name, a user principal name, or a claims login (i:0#.w|DOMAIN\name). An AD
// group's name may hold spaces, so nothing beyond the form is checked; whether the
// account exists is the directory's to say, on the worker.
func validAccount(s string) bool {
	if strings.TrimSpace(s) != s || s == "" || len(s) > 512 || strings.ContainsAny(s, "\r\n\t\x00") {
		return false
	}
	return strings.Contains(s, `\`) || emailAddress.MatchString(s)
}

// ValidSite reports whether s can name a site under the managed path.
func ValidSite(s string) bool { return siteLeaf.MatchString(s) }

// ValidGroup reports whether s names one of the default groups.
func ValidGroup(s string) bool {
	return s == GroupOwners || s == GroupMembers || s == GroupVisitors
}

// CheckAuthored refuses, at deploy, a parameter object that can never work: an
// undeclared name, a missing required one, or a literal value of the wrong form. A
// value that is FEEL (starts with =) is left to [Op.Build] on the worker.
func (o Op) CheckAuthored(name string, params map[string]any) error {
	allowed := map[string]bool{}
	for _, p := range append(append([]string{}, o.Required...), o.Optional...) {
		allowed[p] = true
	}
	for key := range params {
		if !allowed[key] {
			return fmt.Errorf("sharepointserver: operation %q does not take a parameter %q (it takes %s)", name, key, strings.Join(sortedKeys(allowed), ", "))
		}
	}
	for _, p := range o.Required {
		if _, ok := params[p]; !ok {
			return fmt.Errorf("sharepointserver: operation %q needs the parameter %q", name, p)
		}
	}
	if o.AnyOf {
		found := false
		for _, p := range o.Optional {
			if _, ok := params[p]; ok {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("sharepointserver: operation %q needs at least one of %s; without one it changes nothing", name, strings.Join(o.Optional, ", "))
		}
	}
	for key, v := range params {
		if isFEEL(v) {
			continue
		}
		if err := checkValue(name, key, v); err != nil {
			return err
		}
	}
	return nil
}

func isFEEL(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "=")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checkValue(op, key string, v any) error {
	bad := func(want string) error {
		return fmt.Errorf("sharepointserver: operation %q: %s must be %s", op, key, want)
	}
	text, isText := v.(string)
	switch key {
	case "Site":
		if !isText || !ValidSite(text) {
			return bad("the site's name under the managed path (letters, digits, - and _; no URL)")
		}
	case "OwnerAlias", "SecondaryOwnerAlias":
		if !isText || !validAccount(text) {
			return bad(`an account as DOMAIN\name or name@domain`)
		}
	case "Add", "Remove":
		list, ok := accountList(v)
		if !ok {
			return bad(`an account or AD group as DOMAIN\name or name@domain, or a list of them`)
		}
		for _, a := range list {
			if !validAccount(a) {
				return bad(`an account or AD group as DOMAIN\name or name@domain, or a list of them`)
			}
		}
	case "OwnerEmail":
		if !isText || !emailAddress.MatchString(text) {
			return bad("an e-mail address")
		}
	case "Name", "Description":
		if !isText || strings.ContainsAny(text, "\r\n\x00") || (key == "Name" && strings.TrimSpace(text) == "") {
			return bad("one line of text")
		}
	case "Language":
		if n, ok := wholeNumber(v); !ok || n < 1 || n > math.MaxUint16 {
			return bad("a language identifier (LCID, 1031 for German, 1033 for English)")
		}
	case "Template":
		if !isText || !templateName.MatchString(text) {
			return bad("a template name such as STS#0")
		}
	case "ContentDatabase", "QuotaTemplate":
		if !isText || !databaseName.MatchString(text) {
			return bad("a name of letters, digits, spaces, _ . and -")
		}
	case "Group":
		if !isText || !ValidGroup(text) {
			return bad("owners, members or visitors")
		}
	case "QuotaGB":
		if n, ok := decimal(v); !ok || n <= 0 || n > 1<<20 {
			return bad("a size in GB above 0")
		}
	case "WarningPercent":
		// Below 100: Microsoft's reference says both that MaxSize must be larger than
		// WarningSize and that WarningSize may reach MaxSize, so the strict one holds.
		if n, ok := decimal(v); !ok || n < 1 || n >= 100 {
			return bad("a percentage from 1 to below 100")
		}
	case "Limit":
		if n, ok := wholeNumber(v); !ok || n < 1 || n > MaxLimit {
			return bad(fmt.Sprintf("a whole number from 1 to %d", MaxLimit))
		}
	case "Properties":
		m, ok := v.(map[string]any)
		if !ok {
			return bad("an object of text values")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !propertyKey.MatchString(k) || !ok || len(s) > 1024 {
				return bad(`an object of text values whose keys start with "atlas."`)
			}
		}
	}
	return nil
}

// wholeNumber reads a JSON number or its text form as an integer.
func wholeNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		if n != math.Trunc(n) {
			return 0, false
		}
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	}
	return 0, false
}

// decimal reads a JSON number or its text form.
func decimal(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// accountList reads one account or a list of them. A form's checklist answers with a
// list and a textfield with a string, and a model should not have to care.
func accountList(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// Invocation is one fully checked call: the operation and the arguments the bootstrap
// reads, with the site still by name and the sizes in bytes.
type Invocation struct {
	Op   string
	Site string
	Args map[string]any
}

// Build checks a job's evaluated parameters and turns them into an [Invocation]. Every
// check runs here again because a FEEL value is only known now.
func (o Op) Build(name string, params map[string]any) (Invocation, error) {
	if err := o.CheckAuthored(name, params); err != nil {
		return Invocation{}, err
	}
	// CheckAuthored skips values that still read as FEEL; on the worker a value starting
	// with = is simply a value of the wrong form.
	for key, v := range params {
		if isFEEL(v) {
			if err := checkValue(name, key, v); err != nil {
				return Invocation{}, err
			}
		}
	}
	inv := Invocation{Op: name, Args: map[string]any{}}
	for key, v := range params {
		switch key {
		case "Site":
			inv.Site = v.(string)
		case "Add", "Remove":
			list, _ := accountList(v)
			inv.Args[key] = list
		case "Language":
			n, _ := wholeNumber(v)
			inv.Args[key] = n
		case "QuotaGB", "WarningPercent", "Limit":
		default:
			inv.Args[key] = v
		}
	}
	switch name {
	case "create-site":
		if _, ok := inv.Args["Template"]; !ok {
			inv.Args["Template"] = DefaultTemplate
		}
	case "set-site-quota":
		// Set-SPSite takes bytes. A GB here is 2^30 bytes, the binary unit SharePoint's
		// Central Administration shows a quota in, so the two agree.
		gb, _ := decimal(params["QuotaGB"])
		maxSize := int64(math.Round(gb * (1 << 30)))
		pct := 90.0
		if _, ok := params["WarningPercent"]; ok {
			pct, _ = decimal(params["WarningPercent"])
		}
		inv.Args["MaxSize"] = maxSize
		inv.Args["WarningSize"] = int64(math.Round(float64(maxSize) * pct / 100))
	case "list-sites":
		limit := int64(DefaultLimit)
		if _, ok := params["Limit"]; ok {
			limit, _ = wholeNumber(params["Limit"])
		}
		inv.Args["Limit"] = limit
	}
	return inv, nil
}
