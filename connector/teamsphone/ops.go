package teamsphone

import "sort"

// Op describes what one operation requires of a model and what it may carry. The table
// is shared by the compiler's own copy of these rules (the compiler cannot import this
// package; TestTeamsPhoneOpsMatchTheCompiler keeps the two in step) and by [Run], which
// refuses a request the table does not allow before any PowerShell starts.
//
// Required and Optional name authored values: each is a literal or, with a leading '=',
// a FEEL expression evaluated over the variables the task sees at call time. Settings
// name static values the compiler validates against [Settings] and defaults, because
// each decides the shape of what is created rather than its content.
//
// Reconciled names the optional values an operation also brings an existing object to,
// not only a new one. For those, not naming the value and naming an empty one mean
// different things — leave it as it is, and make it empty — so [Prepare] passes such a
// value on only when the model named it, and refuses one that resolved to nothing rather
// than reading a variable nobody set as "empty".
type Op struct {
	Label       string
	Required    []string
	Optional    []string
	Settings    []string
	Reconciled  []string
	NeedsResult bool
}

// Ops is the operation table: what a process needs to make a queue reachable, and to
// take it away again. The order of a provisioning run is resolve-users, two
// ensure-resource-account, ensure-call-queue and ensure-auto-attendant; a leaver path
// runs the three removes in the reverse order.
var Ops = map[string]Op{
	"resolve-users": {
		Label:       "resolve users",
		Required:    []string{"users"},
		NeedsResult: true,
	},
	"ensure-resource-account": {
		Label:       "ensure a resource account",
		Required:    []string{"userPrincipalName", "displayName"},
		Settings:    []string{"accountType"},
		NeedsResult: true,
	},
	"ensure-call-queue": {
		Label:    "ensure a call queue",
		Required: []string{"name", "resourceAccountId"},
		Optional: []string{"agents", "authorizedUsers", "languageId"},
		Settings: []string{"routingMethod", "presenceBasedRouting", "conferenceMode", "allowOptOut"},
		// The agents a model names are the queue's agents: a process run again after a
		// change of staff means the new list, and a queue that kept the old one while the
		// task reported success would be the failure nobody sees (ADR-0463, amendment
		// 2026-10-19).
		Reconciled:  []string{"agents"},
		NeedsResult: true,
	},
	"ensure-auto-attendant": {
		Label: "ensure an auto attendant",
		// Teams refuses an auto attendant without a language and a time zone, and any
		// default here would be one organization's; so a model names both.
		Required:    []string{"name", "resourceAccountId", "transferTarget", "languageId", "timeZone"},
		Optional:    []string{"authorizedUsers", "holidays"},
		NeedsResult: true,
	},
	// The removes take the voice app's name as an optional fallback: a previous run that
	// removed the association and then failed leaves a voice app no account points at,
	// and only its name finds it again.
	"remove-auto-attendant": {
		Label:    "remove an auto attendant",
		Required: []string{"resourceAccountId"},
		Optional: []string{"name"},
	},
	"remove-call-queue": {
		Label:    "remove a call queue",
		Required: []string{"resourceAccountId"},
		Optional: []string{"name"},
	},
	"remove-resource-account": {
		Label:    "remove a resource account",
		Required: []string{"resourceAccountId"},
	},
}

// OpNames lists the operations, sorted, for the messages that have to say what was
// expected.
func OpNames() []string {
	out := make([]string, 0, len(Ops))
	for name := range Ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Shape is how an authored value is read once it is resolved.
type Shape int

const (
	// Text is one string. A FEEL number is read as its decimal text.
	Text Shape = iota
	// List is a list of strings: a FEEL list, or one text separated by commas,
	// semicolons or line breaks — the form a form field or a constant naturally has.
	List
	// Holidays is a list of {name, start, end} objects with ISO dates, as a FEEL list
	// of contexts or as the JSON text of one.
	Holidays
)

// Attributes are every authored value an operation can name, with the shape it is read
// as. An attribute absent here cannot be authored at all.
var Attributes = map[string]Shape{
	"users":             List,
	"userPrincipalName": Text,
	"displayName":       Text,
	"name":              Text,
	"resourceAccountId": Text,
	"transferTarget":    Text,
	"agents":            List,
	"authorizedUsers":   List,
	"languageId":        Text,
	"timeZone":          Text,
	"holidays":          Holidays,
}

// Setting is one static value: the values it may take, and the one a model that
// authors none gets. An empty Default makes the setting required.
type Setting struct {
	Values  []string
	Default string
}

// Settings are the static values an operation may carry. They are compiled structure,
// not authored content: the compiler refuses a value outside Values and applies the
// default, so a job always carries one of these words and the runtime interprets
// nothing (I5).
var Settings = map[string]Setting{
	// accountType decides the application the resource account is created for, and so
	// which kind of voice app it can later be associated with. It has no default: a
	// guess would create an account that cannot carry the voice app it was meant for.
	"accountType":          {Values: []string{AccountCallQueue, AccountAutoAttendant}},
	"routingMethod":        {Values: []string{"attendant", "serial", "round-robin", "longest-idle"}, Default: "attendant"},
	"presenceBasedRouting": {Values: []string{"true", "false"}, Default: "true"},
	"conferenceMode":       {Values: []string{"true", "false"}, Default: "true"},
	"allowOptOut":          {Values: []string{"true", "false"}, Default: "true"},
}

// The two kinds of resource account.
const (
	AccountCallQueue     = "call-queue"
	AccountAutoAttendant = "auto-attendant"
)

// Microsoft's application ids for the two kinds of resource account. They are the same
// in every tenant; the account's application is what Teams checks when it is associated
// with a voice app.
const (
	CallQueueApplicationID     = "11cd3e2e-fccb-42ad-ad00-878b93575e07"
	AutoAttendantApplicationID = "ce933385-9390-45d1-9512-c8d228074e07"
)

// MaxAgents is how many agents a call queue takes directly. Teams serves larger queues
// from a group or a channel, which this worker does not offer; refusing a longer list
// here names the limit instead of passing on Teams' own refusal.
const MaxAgents = 20
