// Package formavailability serves the live check behind a name field: a person types
// the name of something they are ordering — a SharePoint site, a Microsoft 365 group —
// and the form says at once whether that name is still free (ADR-0519).
//
// It is ADR-0473's ephemeral worker query asked a narrower question. A directory field
// searches and offers a page; an availability field asks one value lookup per source
// the field names and learns one bit from each: is there something under this name. The
// answer the browser receives is that bit, folded over every source, and nothing else —
// no id, no display name, nothing about what holds the name.
//
// **What the request may say** is the value typed and nothing more. Which Worker,
// which tenant, which resource, which SharePoint host and managed path — all of that is
// read from the stored form and checked against the provider table below, at save and
// again here, and a request naming any of it is refused.
//
// **The check is a courtesy, not the rule.** Two people can be told the same name is
// free a second apart, and a name can be taken between the order and its provisioning.
// The process that creates the object asks again and owns the refusal; this check only
// spares a person an order that was going to come back.
//
// **This area owns no state** (I3): its collaborators are the server's, a query waits
// on the request's own goroutine, and nothing here reaches the log, the processor or
// recovery.
package formavailability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/exchange"
	"github.com/pblumer/atlas/connector/lookup"
	"github.com/pblumer/atlas/connector/sharepoint"
)

// Member is the component member that makes a textfield an availability field.
const Member = "atlasAvailability"

// Version is the one version of the block this server reads.
const Version = 1

// Bounds on what a block may say. Product rules, not installation budgets.
const (
	// maxChecks bounds the sources one field asks. A name is free when every source
	// says so, and each is a round trip a person waits for.
	maxChecks = 4
	// maxWorkerName matches the directory field's bound.
	maxWorkerName = 128
	// defaultMinLength is how many characters a value needs before it is checked; a
	// one-letter name is not one anybody means to order.
	defaultMinLength = 3
	maxMinLength     = 20
)

// FieldTypes are the form-js fields an availability field may be: the one that holds
// a typed name.
var FieldTypes = []string{"textfield"}

// Provider is one source a field may ask: a resource of a Worker Type, answered by a
// value lookup.
type Provider struct {
	WorkerType string `json:"workerType"`
	Resource   string `json:"resource"`
	// JobType routes the query to Worker Instances serving this Worker Type.
	JobType string `json:"-"`
	// Label names the source to a model author.
	Label string `json:"label"`
	// Property is what the source returns about a record that holds the name; it is
	// asked for so the Worker has something to project, and never passed on.
	Property string `json:"-"`
	// Located marks a source that needs a host — SharePoint, whose sites are addressed
	// under the tenant's host and a managed path.
	Located bool `json:"located,omitempty"`
	// ValueFormat says in words what a value must look like, for an error message.
	ValueFormat string `json:"valueFormat"`

	valid func(string) bool
}

// Valid reports whether v has the shape of a name this source can hold.
func (p Provider) Valid(v string) bool { return p.valid != nil && p.valid(v) }

// providers are the sources an availability field may ask.
var providers = []Provider{
	{
		WorkerType: "entra", Resource: "groups", JobType: compiler.EntraJobType, Property: "mailNickname",
		Label:       "Microsoft Entra ID — groups by alias (mailNickname)",
		ValueFormat: "letters, digits, '.', '-' and '_', starting with a letter or a digit, at most 64",
		valid:       func(v string) bool { return entra.ValidLookupValue("groups", v) },
	},
	{
		WorkerType: "entra", Resource: "deletedGroups", JobType: compiler.EntraJobType, Property: "mailNickname",
		Label:       "Microsoft Entra ID — deleted groups by alias, which keep it for 30 days",
		ValueFormat: "letters, digits, '.', '-' and '_', starting with a letter or a digit, at most 64",
		valid:       func(v string) bool { return entra.ValidLookupValue("deletedGroups", v) },
	},
	{
		WorkerType: "exchange", Resource: exchange.LookupRecipients, JobType: compiler.ExchangeJobType, Property: "Alias",
		Label:       "Microsoft Exchange — a recipient with this alias or an address with this local part",
		ValueFormat: "letters, digits, '.', '-' and '_', starting with a letter or a digit, at most 64",
		valid:       exchange.ValidAlias,
	},
	{
		WorkerType: "sharepoint", Resource: sharepoint.LookupSites, JobType: compiler.SharePointOpsJobType, Property: "name",
		Label: "Microsoft SharePoint — a site at https://<host>/<managed path>/<name>", Located: true,
		ValueFormat: "letters, digits, '-' and '_', starting with a letter or a digit, at most 64",
		valid:       sharepoint.ValidSiteName,
	},
}

// Providers lists the sources this server offers, for the editor and for the Worker
// switch that opens a Worker to form lookups.
func Providers() []Provider { return slices.Clone(providers) }

// providerFor finds a source by Worker Type and resource.
func providerFor(workerType, resource string) (Provider, bool) {
	for _, p := range providers {
		if p.WorkerType == workerType && p.Resource == resource {
			return p, true
		}
	}
	return Provider{}, false
}

// Check is one source as a block names it.
type Check struct {
	WorkerType string `json:"workerType"`
	Worker     string `json:"worker"`
	Resource   string `json:"resource"`
	// Host and ManagedPath locate a SharePoint site: https://<host>/<managedPath>/<name>.
	Host        string `json:"host,omitempty"`
	ManagedPath string `json:"managedPath,omitempty"`
}

// Source is the atlasAvailability block as a form states it.
type Source struct {
	Version int     `json:"version"`
	Checks  []Check `json:"checks"`
	// MinLength is how many characters a value needs before it is checked. Absent
	// means 3.
	MinLength *int `json:"minLength,omitempty"`
}

// Field is one availability field of a form, checked.
type Field struct {
	Key       string
	Source    Source
	Providers []Provider
}

// MinLength is the field's threshold.
func (f Field) MinLength() int {
	if f.Source.MinLength == nil {
		return defaultMinLength
	}
	return *f.Source.MinLength
}

// Form is the availability fields of one form, by key.
type Form struct {
	Fields map[string]Field
}

// Field finds a field by key.
func (f *Form) Field(key string) (Field, bool) {
	fl, ok := f.Fields[key]
	return fl, ok
}

// component is what this package reads of a form-js component.
type component struct {
	Type         string          `json:"type"`
	Key          string          `json:"key"`
	Components   json.RawMessage `json:"components"`
	Availability json.RawMessage `json:"atlasAvailability"`
}

// Parse reads a form's availability fields and checks every block. A form without any
// parses to an empty set; one with a malformed block is an error naming the field.
func Parse(schema []byte) (*Form, error) {
	out := &Form{Fields: map[string]Field{}}
	if !bytes.Contains(schema, []byte(Member)) {
		return out, nil
	}
	var root struct {
		Components json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, fmt.Errorf("the form is not JSON: %v", err)
	}
	if err := walk(root.Components, out); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(raw json.RawMessage, out *Form) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var comps []component
	if err := json.Unmarshal(raw, &comps); err != nil {
		return fmt.Errorf("the form's components are not a list: %v", err)
	}
	for _, c := range comps {
		if err := walk(c.Components, out); err != nil {
			return err
		}
		if len(c.Availability) == 0 {
			continue
		}
		f, err := field(c)
		if err != nil {
			return err
		}
		if _, dup := out.Fields[f.Key]; dup {
			return fmt.Errorf("two availability fields write %q", f.Key)
		}
		out.Fields[f.Key] = f
	}
	return nil
}

// field checks one block against the provider table.
func field(c component) (Field, error) {
	key := strings.TrimSpace(c.Key)
	if key == "" {
		return Field{}, fmt.Errorf("an availability field needs a key: the variable the name is written to")
	}
	if !slices.Contains(FieldTypes, c.Type) {
		return Field{}, fmt.Errorf("the field %q is a %s; an availability check belongs on a textfield, where a name is typed", key, c.Type)
	}
	var src Source
	dec := json.NewDecoder(bytes.NewReader(c.Availability))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&src); err != nil {
		return Field{}, fmt.Errorf("the field %q has an %s block this server cannot read: %v", key, Member, err)
	}
	if src.Version != Version {
		return Field{}, fmt.Errorf("the field %q states %s version %d; this server reads version %d", key, Member, src.Version, Version)
	}
	if len(src.Checks) == 0 || len(src.Checks) > maxChecks {
		return Field{}, fmt.Errorf("the field %q names %d sources to check; name 1 to %d", key, len(src.Checks), maxChecks)
	}
	if src.MinLength != nil && (*src.MinLength < 1 || *src.MinLength > maxMinLength) {
		return Field{}, fmt.Errorf("the field %q has minLength %d; it must be 1 to %d", key, *src.MinLength, maxMinLength)
	}
	f := Field{Key: key, Source: src}
	seen := map[string]bool{}
	for i, ck := range src.Checks {
		p, ok := providerFor(ck.WorkerType, ck.Resource)
		if !ok {
			return Field{}, fmt.Errorf("the field %q check %d names %s/%s, which is not a source this server checks (%s)",
				key, i+1, ck.WorkerType, ck.Resource, strings.Join(sourceNames(), ", "))
		}
		name := strings.TrimSpace(ck.Worker)
		if name == "" || utf8.RuneCountInString(name) > maxWorkerName || !lookup.CleanText(name) {
			return Field{}, fmt.Errorf("the field %q check %d needs the name of a configured %s Worker", key, i+1, ck.WorkerType)
		}
		if p.Located {
			if !sharepoint.ValidHostName(ck.Host) {
				return Field{}, fmt.Errorf("the field %q check %d needs the tenant's SharePoint host, e.g. contoso.sharepoint.com", key, i+1)
			}
			if ck.ManagedPath != "" && !slices.Contains(sharepoint.LookupManagedPaths, ck.ManagedPath) {
				return Field{}, fmt.Errorf("the field %q check %d has the managed path %q; it is one of %s",
					key, i+1, ck.ManagedPath, strings.Join(sharepoint.LookupManagedPaths, ", "))
			}
		} else if ck.Host != "" || ck.ManagedPath != "" {
			return Field{}, fmt.Errorf("the field %q check %d sets a host or a managed path, which %s/%s does not use; remove it",
				key, i+1, ck.WorkerType, ck.Resource)
		}
		id := ck.WorkerType + "/" + name + "/" + ck.Resource + "/" + ck.Host + "/" + ck.ManagedPath
		if seen[id] {
			return Field{}, fmt.Errorf("the field %q names the same source twice", key)
		}
		seen[id] = true
		f.Providers = append(f.Providers, p)
	}
	return f, nil
}

func sourceNames() []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.WorkerType+"/"+p.Resource)
	}
	return out
}

// CheckSchema is the save-time check: a form whose availability blocks this server could not
// serve is refused with the reason, rather than saved and failing when somebody types.
func CheckSchema(schema []byte) error {
	_, err := Parse(schema)
	if err != nil {
		return fmt.Errorf("availability field: %w", err)
	}
	return nil
}

// Normalize is the value as it is checked: trimmed and lower-cased. A Microsoft 365
// alias and a SharePoint site address are both case-insensitive, so "Dev-HuDa" is taken
// when "dev-huda" is.
func Normalize(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// query is the lookup one check sends for a value.
func query(ck Check, p Provider, value string) lookup.Query {
	q := lookup.Query{Resource: p.Resource, Value: value, Properties: []string{p.Property}, PageSize: 1}
	if p.Located {
		mp := ck.ManagedPath
		if mp == "" {
			mp = "sites"
		}
		q.Filters = []lookup.Filter{
			{Property: sharepoint.LookupFilterHost, Value: ck.Host},
			{Property: sharepoint.LookupFilterManagedPath, Value: mp},
		}
	}
	return q
}
