package sharepointserver

import (
	"context"
	"slices"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
	"github.com/pblumer/atlas/workersdk/lookup"
)

// A site lookup answers an order form's question whether a site name is still free on
// the farm (ADR-0519, amended 2026-11-10). It rides ADR-0473's form-lookup protocol: an
// ephemeral query this Worker's instance answers as the account it runs as, never a
// job. It is a value lookup and nothing else: no search, no listing, no continuation.
//
// Unlike the SharePoint Online lookup it takes no host and no managed path. The Worker
// was configured with its web application and managed path, and a site is named by its
// leaf under them, exactly as a task names one (catalog.ValidSite), so neither a form
// nor a person can point this Worker at another web application.

// LookupSites is the one resource a SharePoint Server lookup reads.
const LookupSites = "sites"

// LookupReadable are the properties a site lookup may return. The lookup hands back
// only the name it was asked about: get-site's answer names owners and members, and
// none of that leaves the Worker.
var LookupReadable = []string{"name"}

// ValidLookupValue reports whether v can name a site under the managed path.
func ValidLookupValue(v string) bool { return catalog.ValidSite(v) }

// CheckLookup holds a query to what this Worker Type answers. The server runs the same
// value check, from the same function, before it sends one.
func CheckLookup(q lookup.Query) error {
	switch {
	case q.Resource != LookupSites, !ValidLookupValue(q.Value):
		return lookup.Fail(lookup.CodeInvalid)
	case q.Search != "", len(q.SearchFields) > 0, q.Next != "", q.ActiveOnly, len(q.Filters) > 0:
		return lookup.Fail(lookup.CodeInvalid)
	case q.PageSize < 0, q.PageSize > lookup.MaxPageSize:
		return lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.Properties {
		if !slices.Contains(LookupReadable, p) {
			return lookup.Fail(lookup.CodeInvalid)
		}
	}
	return nil
}

// RunLookup answers a site lookup against the Worker connector names: one item when a
// site lives at the address, none when it is free. It is get-site, the operation the
// team site's process asks before create-site, so the form and the process agree on
// what "taken" means.
//
// Every call starts PowerShell and loads the SharePoint snap-in, which takes seconds;
// a form waits ADR-0519's bound for it and reports a check that did not finish as one
// that could not run, which does not stop the order.
func RunLookup(ctx context.Context, connector string, q lookup.Query, reg *Registry) (lookup.Page, error) {
	client, ok := reg.Client(connector)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	if err := CheckLookup(q); err != nil {
		return lookup.Page{}, err
	}
	op, _ := catalog.Lookup("get-site")
	inv, err := op.Build("get-site", map[string]any{"Site": q.Value})
	if err != nil {
		return lookup.Page{}, lookup.Fail(lookup.CodeInvalid)
	}
	out, err := client.Invoke(ctx, inv)
	if err != nil {
		// The error text carries the address, which carries what the person typed; only
		// the code leaves this function. The bootstrap gives no code a lookup could act
		// on, and the account's permissions are not told apart from a farm that is down.
		return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
	}
	if len(out) == 0 {
		return lookup.Page{Items: []map[string]any{}}, nil
	}
	item := map[string]any{}
	for _, p := range q.Properties {
		if p == "name" {
			item[p] = q.Value
		}
	}
	return lookup.Page{Items: []map[string]any{item}}, nil
}
