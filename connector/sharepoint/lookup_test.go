package sharepoint

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk/lookup"
)

func siteQuery(name string, filters ...lookup.Filter) lookup.Query {
	if filters == nil {
		filters = []lookup.Filter{{Property: LookupFilterHost, Value: "contoso.sharepoint.com"}}
	}
	return lookup.Query{Resource: LookupSites, Value: name, Filters: filters, Properties: []string{"name"}}
}

// A site lookup is a value lookup on sites under a host, and nothing else: anything a
// search would say, an unknown filter, a malformed name or host is refused before the
// tenant is asked.
func TestCheckSiteLookup(t *testing.T) {
	host, mp, err := CheckLookup(siteQuery("dev-huda"))
	if err != nil || host != "contoso.sharepoint.com" || mp != "sites" {
		t.Fatalf("CheckLookup = %q, %q, %v", host, mp, err)
	}
	if _, mp, err := CheckLookup(siteQuery("x", lookup.Filter{Property: LookupFilterHost, Value: "contoso.sharepoint.com"},
		lookup.Filter{Property: LookupFilterManagedPath, Value: "teams"})); err != nil || mp != "teams" {
		t.Fatalf("teams path: %q, %v", mp, err)
	}
	for name, q := range map[string]lookup.Query{
		"no value":            siteQuery(""),
		"a slash":             siteQuery("a/b"),
		"a dot dot":           siteQuery(".."),
		"a quote":             siteQuery("a'b"),
		"too long":            siteQuery(strings.Repeat("a", 65)),
		"no host":             siteQuery("a", lookup.Filter{Property: LookupFilterManagedPath, Value: "sites"}),
		"a host with path":    siteQuery("a", lookup.Filter{Property: LookupFilterHost, Value: "contoso.sharepoint.com/x"}),
		"another path":        siteQuery("a", lookup.Filter{Property: LookupFilterHost, Value: "contoso.sharepoint.com"}, lookup.Filter{Property: LookupFilterManagedPath, Value: "personal"}),
		"an unknown filter":   siteQuery("a", lookup.Filter{Property: LookupFilterHost, Value: "contoso.sharepoint.com"}, lookup.Filter{Property: "owner", Value: "x"}),
		"a search":            {Resource: LookupSites, Value: "a", Search: "a", Filters: siteQuery("a").Filters},
		"another resource":    {Resource: "lists", Value: "a", Filters: siteQuery("a").Filters},
		"an unknown property": {Resource: LookupSites, Value: "a", Filters: siteQuery("a").Filters, Properties: []string{"id"}},
	} {
		var le *lookup.Error
		if _, _, err := CheckLookup(q); !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("%s: err = %v, want invalid_query", name, err)
		}
	}
}

// The lookup asks Graph for the site at host:/sites/name and answers one item when it
// is there and none when Graph says 404; a refusal and a throttle keep their codes and
// nothing of the address.
func TestRunSiteLookup(t *testing.T) {
	status := http.StatusOK
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/drive"):
			answer(w, http.StatusOK, `{"quota":{"used":1}}`)
		case status != http.StatusOK:
			answer(w, status, `{"error":{"code":"x"}}`)
		default:
			answer(w, http.StatusOK, `{"id":"h,1,2","name":"dev-huda","webUrl":"https://contoso.sharepoint.com/sites/dev-huda"}`)
		}
	})
	reg := NewRegistry()
	reg.Register("sp", c)
	page, err := RunLookup(context.Background(), "sp", siteQuery("dev-huda"), reg)
	if err != nil || len(page.Items) != 1 || page.Items[0]["name"] != "dev-huda" || len(page.Items[0]) != 1 {
		t.Fatalf("taken site: %#v, %v", page, err)
	}
	if got := fg.requests[0].path; got != "/sites/contoso.sharepoint.com:/sites/dev-huda" {
		t.Errorf("asked %s, want the site by host and managed path", got)
	}
	status = http.StatusNotFound
	if page, err := RunLookup(context.Background(), "sp", siteQuery("frei"), reg); err != nil || len(page.Items) != 0 {
		t.Fatalf("free site: %#v, %v", page, err)
	}
	for st, want := range map[int]string{
		http.StatusForbidden:           lookup.CodeRefused,
		http.StatusTooManyRequests:     lookup.CodeThrottled,
		http.StatusInternalServerError: lookup.CodeUnavailable,
	} {
		status = st
		_, err := RunLookup(context.Background(), "sp", siteQuery("dev-huda"), reg)
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != want || strings.Contains(err.Error(), "dev-huda") {
			t.Errorf("HTTP %d: err = %v, want %s without the name", st, err, want)
		}
	}
	if _, err := RunLookup(context.Background(), "other", siteQuery("dev-huda"), reg); err == nil || !strings.Contains(err.Error(), lookup.CodeUnconfigured) {
		t.Errorf("unknown Worker: err = %v, want worker_unconfigured", err)
	}
}

// A cancelled asker, an answer that is not a site, and a page larger than a lookup's.
func TestSiteLookupEdges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var le *lookup.Error
	if err := siteLookupFailure(ctx, errors.New("sharepoint: get-site returned HTTP 403")); !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
		t.Errorf("cancelled: %v", err)
	}
	reg := NewRegistry()
	reg.Register("odd", oddClient{})
	if _, err := RunLookup(context.Background(), "odd", siteQuery("dev-huda"), reg); !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
		t.Errorf("an answer that is not a site: %v", err)
	}
	q := siteQuery("dev-huda")
	q.PageSize = lookup.MaxPageSize + 1
	if _, _, err := CheckLookup(q); err == nil {
		t.Error("a page larger than a lookup's was accepted")
	}
	q = siteQuery("dev-huda", lookup.Filter{Property: LookupFilterHost, Value: "contoso.sharepoint.com"}, lookup.Filter{Property: LookupFilterHost, Value: "fabrikam.sharepoint.com"})
	if _, _, err := CheckLookup(q); err == nil {
		t.Error("a host named twice was accepted")
	}
}

// oddClient answers every operation with something that is not an object.
type oddClient struct{}

func (oddClient) Do(context.Context, Request) (any, error) { return "not a site", nil }

// A query the Worker would refuse is refused before the tenant is asked.
func TestSiteLookupRefusesBeforeAsking(t *testing.T) {
	reg := NewRegistry()
	reg.Register("odd", oddClient{})
	if _, err := RunLookup(context.Background(), "odd", siteQuery("a/b"), reg); err == nil {
		t.Error("a malformed site name was asked")
	}
}
