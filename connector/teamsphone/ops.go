package teamsphone

import "sort"

// Op describes what one operation requires of a model and what it may carry. The table
// is shared by the compiler's own copy of these rules (the compiler cannot import this
// package; TestTeamsPhoneOpsMatchTheCompiler keeps the two in step) and by [Run], which
// refuses a request the table does not allow before any PowerShell starts.
//
// Required and Optional name authored values: each is a literal or, with a leading '=',
// a FEEL expression evaluated over the variables the task sees at call time. Settings
// name static values the compiler validates against [Settings], because each decides the
// shape of what is created rather than its content.
//
// Reconciled names the optional values and settings an operation also brings an existing
// object to, not only a new one. For those, not naming a value and naming an empty or a
// default one mean different things — leave it as it is, and make it so — so [Prepare]
// lists in [Request.Reconcile] only what the model named, passes a reconciled list on
// only when the model named it, and refuses one that resolved to nothing rather than
// reading a variable nobody set as "nobody".
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
		// What a model names is what the queue is: a process run again after a change of
		// staff or of routing means the new values, and a queue that kept the old ones
		// while the task reported success would be the failure nobody sees (ADR-0463,
		// amendments 2026-10-19 and 2026-10-20). The language is not reconciled: it only
		// changes what the queue's announcements say.
		Reconciled:  []string{"agents", "authorizedUsers", "routingMethod", "presenceBasedRouting", "conferenceMode", "allowOptOut"},
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
	// A phone number is given to a user or a resource account alike: a queue's hotline
	// and a person's direct line are the same assignment to Teams. identity is either's
	// user principal name or object id. The number type is the tenant's way of reaching
	// the telephone network and decides what Teams checks, so it is fixed in the model.
	"ensure-phone-number": {
		Label:       "ensure a phone number",
		Required:    []string{"identity", "phoneNumber"},
		Optional:    []string{"locationId"},
		Settings:    []string{"numberType"},
		NeedsResult: true,
	},
	// Without a phoneNumber, every number the identity holds is removed: a leaver path
	// should not have to know them.
	"remove-phone-number": {
		Label:    "remove a phone number",
		Required: []string{"identity"},
		Optional: []string{"phoneNumber"},
	},
	// A whole hotline in one task, in the one order that never leaves a caller ringing
	// into nothing: auto attendants, then queues, then each account's numbers and the
	// account. names finds a voice app an earlier run left without its account.
	"remove-hotline": {
		Label:    "remove a hotline",
		Required: []string{"resourceAccountIds"},
		Optional: []string{"names"},
	},
	// The numbers in the tenant's inventory nobody holds, that Teams would give to the
	// kind of holder named — what a person choosing a number for ensure-phone-number
	// picks from. The type is the one that ensure-phone-number will assign under, so it
	// is fixed in the model like there.
	"list-free-phone-numbers": {
		Label:       "list free phone numbers",
		Optional:    []string{"numberPrefix", "max"},
		Settings:    []string{"numberType", "assignableTo"},
		NeedsResult: true,
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
	// PhoneNumber is one telephone number, written as people write one — with spaces,
	// dashes or brackets, or as a tel: URI — and handed on as Teams takes it: digits with
	// an optional leading + and an optional ;ext= extension.
	PhoneNumber
	// Limit is how many entries an answer may hold: a whole number from 1 to
	// MaxFreeNumbers, DefaultFreeNumbers when the model names none.
	Limit
)

// Attributes are every authored value an operation can name, with the shape it is read
// as. An attribute absent here cannot be authored at all.
var Attributes = map[string]Shape{
	"users":              List,
	"userPrincipalName":  Text,
	"displayName":        Text,
	"name":               Text,
	"resourceAccountId":  Text,
	"transferTarget":     Text,
	"agents":             List,
	"authorizedUsers":    List,
	"languageId":         Text,
	"timeZone":           Text,
	"holidays":           Holidays,
	"identity":           Text,
	"phoneNumber":        PhoneNumber,
	"locationId":         Text,
	"resourceAccountIds": List,
	"names":              List,
	"numberPrefix":       PhoneNumber,
	"max":                Limit,
}

// Setting is one static value: the values it may take, and the one an object the request
// creates gets when the model authors none. An empty Default makes the setting required.
type Setting struct {
	Values  []string
	Default string
}

// Settings are the static values an operation may carry. They are compiled structure,
// not authored content: the compiler refuses a value outside Values (I5). It does not
// apply the default — whether the model named a setting is what tells an existing
// object's setting to change from one to keep — so [Prepare] applies it, for an object
// the request creates.
var Settings = map[string]Setting{
	// accountType decides the application the resource account is created for, and so
	// which kind of voice app it can later be associated with. It has no default: a
	// guess would create an account that cannot carry the voice app it was meant for.
	"accountType":          {Values: []string{AccountCallQueue, AccountAutoAttendant}},
	"routingMethod":        {Values: []string{"attendant", "serial", "round-robin", "longest-idle"}, Default: "attendant"},
	"presenceBasedRouting": {Values: []string{"true", "false"}, Default: "true"},
	"conferenceMode":       {Values: []string{"true", "false"}, Default: "true"},
	"allowOptOut":          {Values: []string{"true", "false"}, Default: "true"},
	// numberType has no default: Direct Routing, a Calling Plan and Operator Connect are
	// three ways a tenant reaches the telephone network, and a guess would hand Teams a
	// number under the wrong one.
	"numberType": {Values: []string{NumberDirectRouting, NumberCallingPlan, NumberOperatorConnect}},
	// assignableTo has no default either: Teams gives a number to a person or to a voice
	// app's account only when the number carries that capability, and a list for the
	// wrong kind of holder offers numbers that are then refused.
	"assignableTo": {Values: []string{AssignableToUser, AssignableToResourceAccount}},
}

// The two kinds of holder a free number is listed for, and the capability Teams names
// each by.
const (
	AssignableToUser            = "user"
	AssignableToResourceAccount = "resource-account"
)

// How many free numbers one list-free-phone-numbers answers: a person choosing one
// needs a page, not the inventory, and Teams answers 500 at a time unless told
// otherwise. An answer that stopped short says so in truncated.
const (
	DefaultFreeNumbers = 100
	MaxFreeNumbers     = 500
)

// The three kinds of phone number, by how the tenant reaches the telephone network. A
// Direct Routing number is the operator's own carrier's and needs no inventory in the
// tenant; the other two must be numbers the tenant has.
const (
	NumberDirectRouting   = "direct-routing"
	NumberCallingPlan     = "calling-plan"
	NumberOperatorConnect = "operator-connect"
)

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
