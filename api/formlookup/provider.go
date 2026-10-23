package formlookup

import (
	"slices"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/lookup"
)

// Provider is one remote data source a form may name: a resource of a Worker Type,
// what of it may be searched, filtered and shown, and how its records are told apart.
//
// It is the server's half of a rule the Worker Type keeps too. The lists come from the
// Worker Type's own table (for Entra, entra.LookupResources), so the server refuses at
// save and at query time exactly what the Worker would refuse at answer time, and the
// two cannot drift: there is one table, and this is a view of it.
type Provider struct {
	// WorkerType is the Worker Type a form's Worker must be of — the kind of its
	// Console record ("entra").
	WorkerType string `json:"workerType"`
	// Resource is what is searched, in the Worker Type's vocabulary ("users").
	Resource string `json:"resource"`
	// JobType routes the query: it reaches Worker Instances serving this job type.
	JobType string `json:"-"`
	// Label names the source to a model author.
	Label string `json:"label"`
	// ValueProperty identifies a record; it is what a form submits.
	ValueProperty string   `json:"valueProperty"`
	Searchable    []string `json:"searchable"`
	Filterable    []string `json:"filterable"`
	Readable      []string `json:"readable"`
	// DefaultLabel and DefaultDescription are what an option shows when the form
	// names nothing; Fallback stands in for either when a record leaves it empty — a
	// user without a mailbox still has a user principal name.
	DefaultLabel       string `json:"defaultLabel"`
	DefaultDescription string `json:"defaultDescription"`
	Fallback           string `json:"fallback,omitempty"`
	// ValueFormat says in words what a value looks like, for an error message.
	ValueFormat string `json:"valueFormat"`

	validValue func(string) bool
	check      func(lookup.Query) error
}

// ValidValue reports whether v has the shape of a value of this source.
func (p Provider) ValidValue(v string) bool { return p.validValue != nil && p.validValue(v) }

// Check holds a query to the Worker Type's own rules, before it is sent.
func (p Provider) Check(q lookup.Query) error {
	if p.check == nil {
		return lookup.Fail(lookup.CodeInvalid)
	}
	return p.check(q)
}

// providers are the remote data sources this server offers. One today; another is a
// row here and a query handler in its Worker Type.
var providers = []Provider{entraUsers()}

func entraUsers() Provider {
	res := entra.LookupResources["users"]
	return Provider{
		WorkerType:         "entra",
		Resource:           "users",
		JobType:            compiler.EntraJobType,
		Label:              "Microsoft Entra ID — users",
		ValueProperty:      res.ValueProperty,
		Searchable:         slices.Clone(res.Searchable),
		Filterable:         slices.Clone(res.Filterable),
		Readable:           slices.Clone(res.Readable),
		DefaultLabel:       "displayName",
		DefaultDescription: "mail",
		Fallback:           "userPrincipalName",
		ValueFormat:        "an Entra object id (a GUID)",
		validValue:         func(v string) bool { return entra.ValidLookupValue("users", v) },
		check: func(q lookup.Query) error {
			_, err := entra.CheckLookup(q)
			return err
		},
	}
}

// Providers lists the remote data sources this server offers.
func Providers() []Provider { return slices.Clone(providers) }

// ProviderFor finds the source a form names.
func ProviderFor(workerType, resource string) (Provider, bool) {
	for _, p := range providers {
		if p.WorkerType == workerType && p.Resource == resource {
			return p, true
		}
	}
	return Provider{}, false
}
