package sharepoint

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/pblumer/atlas/connector/lookup"
)

// A site lookup answers the one question an order form asks before somebody takes a
// name: is there already a site at this address (ADR-0519)? It rides the form-lookup
// protocol of ADR-0473 — an ephemeral query a Worker Instance answers with the
// Worker's own credential, never a job — and is a value lookup and nothing else: no
// search, no listing, no continuation. A person typing a site name learns whether the
// address is taken; they never see what else the tenant holds.
//
// The address is built here, from parts the server took out of the stored form and
// this file checks: the tenant's SharePoint host and the managed path ("sites" or
// "teams") as filters, and the name the person typed as the value. Neither a form nor
// a person hands this Worker a URL.

// LookupSites is the one resource a SharePoint lookup reads.
const LookupSites = "sites"

// The filters a site lookup takes. The host is required: a site name means nothing
// without the tenant it is looked for in. The managed path defaults to "sites".
const (
	LookupFilterHost        = "hostname"
	LookupFilterManagedPath = "managedPath"
)

// LookupManagedPaths are the managed paths a site may be looked for under: the two a
// SharePoint Online tenant has for team and communication sites.
var LookupManagedPaths = []string{"sites", "teams"}

// LookupReadable are the properties a site lookup may return.
var LookupReadable = []string{"name", "webUrl"}

var (
	// siteName is what a site's URL name may be: the characters SharePoint accepts in
	// a site address, which is also what an order form tells a person to type.
	siteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// hostName is a DNS host name, lower-case, as a tenant's SharePoint host is.
	hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// ValidSiteName reports whether v can be a site's URL name.
func ValidSiteName(v string) bool { return siteName.MatchString(v) }

// ValidHostName reports whether v can be a tenant's SharePoint host.
func ValidHostName(v string) bool { return len(v) <= 253 && hostName.MatchString(v) }

// CheckLookup holds a site lookup to what this Worker Type answers: a value lookup on
// sites, under a host, with nothing else asked. The server runs the same check from the
// same functions before it sends one.
func CheckLookup(q lookup.Query) (host, managedPath string, err error) {
	if q.Resource != LookupSites || q.Value == "" || !ValidSiteName(q.Value) {
		return "", "", lookup.Fail(lookup.CodeInvalid)
	}
	if q.Search != "" || len(q.SearchFields) > 0 || q.Next != "" || q.ActiveOnly {
		return "", "", lookup.Fail(lookup.CodeInvalid)
	}
	if q.PageSize < 0 || q.PageSize > lookup.MaxPageSize {
		return "", "", lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.Properties {
		if !slices.Contains(LookupReadable, p) {
			return "", "", lookup.Fail(lookup.CodeInvalid)
		}
	}
	managedPath = "sites"
	seen := map[string]bool{}
	for _, f := range q.Filters {
		if seen[f.Property] {
			return "", "", lookup.Fail(lookup.CodeInvalid)
		}
		seen[f.Property] = true
		switch f.Property {
		case LookupFilterHost:
			if !ValidHostName(f.Value) {
				return "", "", lookup.Fail(lookup.CodeInvalid)
			}
			host = f.Value
		case LookupFilterManagedPath:
			if !slices.Contains(LookupManagedPaths, f.Value) {
				return "", "", lookup.Fail(lookup.CodeInvalid)
			}
			managedPath = f.Value
		default:
			return "", "", lookup.Fail(lookup.CodeInvalid)
		}
	}
	if host == "" {
		return "", "", lookup.Fail(lookup.CodeInvalid)
	}
	return host, managedPath, nil
}

// RunLookup answers a site lookup against the Worker connector names: one item when a
// site is at the address, none when it is free. It is get-site, so the mockup and the
// real tenant answer it alike, and a site the get-site of a process would find is the
// site the order form reports as taken.
//
// A Worker that acts as a person (a brokered, delegated identity, ADR-0475) is not in
// reg and answers worker_unconfigured: a lookup has no job to authorize a person's
// token for.
func RunLookup(ctx context.Context, connector string, q lookup.Query, reg *Registry) (lookup.Page, error) {
	client, ok := reg.Client(connector)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	host, managedPath, err := CheckLookup(q)
	if err != nil {
		return lookup.Page{}, err
	}
	got, err := client.Do(ctx, Request{Operation: OpGetSite, Site: host + ":/" + managedPath + "/" + q.Value})
	if err != nil {
		// The error text carries the address, which carries what the person typed; only
		// the code leaves this function.
		return lookup.Page{}, siteLookupFailure(ctx, err)
	}
	site, _ := got.(map[string]any)
	if site == nil {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
	}
	if exists, _ := site["exists"].(bool); !exists {
		return lookup.Page{Items: []map[string]any{}}, nil
	}
	item := map[string]any{}
	for _, p := range q.Properties {
		item[p] = site[p]
	}
	return lookup.Page{Items: []map[string]any{item}}, nil
}

// siteLookupFailure turns a failed get-site into a lookup code. The client's errors are
// text — they were written for an incident, not for a branch — so the status is read
// from the one phrase graphFailure always writes.
func siteLookupFailure(ctx context.Context, err error) error {
	msg := err.Error()
	switch {
	case ctx.Err() != nil:
		return lookup.Fail(lookup.CodeUnavailable)
	case strings.Contains(msg, "returned HTTP 401"), strings.Contains(msg, "returned HTTP 403"):
		return lookup.Fail(lookup.CodeRefused)
	case strings.Contains(msg, "returned HTTP 429"):
		return lookup.Fail(lookup.CodeThrottled)
	default:
		return lookup.Fail(lookup.CodeUnavailable)
	}
}
