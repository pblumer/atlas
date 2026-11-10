package sharepointserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pblumer/atlas/workersdk/lookup"
)

func lookupCode(err error) string {
	var le *lookup.Error
	if errors.As(err, &le) {
		return le.Code
	}
	return ""
}

// TestASiteLookupIsGetSiteOnTheWorkersOwnWebApplication: an order form's question
// whether a site name is free is get-site on <web application>/<managed path>/<name>,
// the Worker's own, and answers one item for a site and none for a free address.
func TestASiteLookupIsGetSiteOnTheWorkersOwnWebApplication(t *testing.T) {
	reg := NewRegistry()
	r := &fakeRunner{stdout: `{"ok":true,"value":[]}`}
	reg.Register("farm", client(t, r))
	q := lookup.Query{Resource: LookupSites, Value: "projekt-alpha", Properties: []string{"name"}, PageSize: 1}

	page, err := RunLookup(context.Background(), "farm", q, reg)
	if err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("a free address: %+v %v", page, err)
	}
	raw, _ := r.value("ATLAS_SHAREPOINTSERVER_REQUEST")
	var req request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if req.Op != "get-site" || req.SiteURL != "http://sp01.contoso.local/sites/projekt-alpha" {
		t.Errorf("request = %+v", req)
	}

	r.stdout = `{"ok":true,"value":[{"Url":"http://sp01.contoso.local/sites/projekt-alpha","Owner":"CONTOSO\\anna","Properties":{"atlas.position":"o:1"}}]}`
	page, err = RunLookup(context.Background(), "farm", q, reg)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("a taken address: %+v %v", page, err)
	}
	if len(page.Items[0]) != 1 || page.Items[0]["name"] != "projekt-alpha" {
		t.Errorf("the item carries only what was asked: %v", page.Items[0])
	}
}

// TestASiteLookupAsksNothingElse: no search, no listing, no continuation, no filter, no
// property beyond the readable ones and no name the managed path could not hold; and
// a Worker this instance does not hold answers worker_unconfigured.
func TestASiteLookupAsksNothingElse(t *testing.T) {
	reg := NewRegistry()
	r := &fakeRunner{stdout: `{"ok":true,"value":[]}`}
	reg.Register("farm", client(t, r))
	ok := lookup.Query{Resource: LookupSites, Value: "alpha", Properties: []string{"name"}, PageSize: 1}
	for name, q := range map[string]lookup.Query{
		"another resource": {Resource: "users", Value: "alpha"},
		"no value":         {Resource: LookupSites},
		"a URL":            {Resource: LookupSites, Value: "http://evil/sites/x"},
		"a path":           {Resource: LookupSites, Value: "a/b"},
		"a search":         {Resource: LookupSites, Value: "alpha", Search: "al"},
		"a continuation":   {Resource: LookupSites, Value: "alpha", Next: "x"},
		"a filter":         {Resource: LookupSites, Value: "alpha", Filters: []lookup.Filter{{Property: "hostname", Value: "sp02"}}},
		"a property":       {Resource: LookupSites, Value: "alpha", Properties: []string{"Owner"}},
		"a page too large": {Resource: LookupSites, Value: "alpha", PageSize: lookup.MaxPageSize + 1},
	} {
		r.env = nil
		if _, err := RunLookup(context.Background(), "farm", q, reg); lookupCode(err) != lookup.CodeInvalid {
			t.Errorf("%s: err = %v, want %s", name, err, lookup.CodeInvalid)
		}
		if r.env != nil {
			t.Errorf("%s reached PowerShell", name)
		}
	}
	if _, err := RunLookup(context.Background(), "other", ok, reg); lookupCode(err) != lookup.CodeUnconfigured {
		t.Errorf("unknown Worker: %v", err)
	}
}

// TestAFailedSiteLookupSaysOnlyItsCode: what PowerShell said carries the address, which
// carries what the person typed; only a code leaves the Worker.
func TestAFailedSiteLookupSaysOnlyItsCode(t *testing.T) {
	reg := NewRegistry()
	r := &fakeRunner{}
	reg.Register("farm", client(t, r))
	q := lookup.Query{Resource: LookupSites, Value: "alpha", Properties: []string{"name"}, PageSize: 1}
	for _, stdout := range []string{
		`{"ok":false,"error":{"code":"","message":"Access denied for http://sp01.contoso.local/sites/alpha"}}`,
		`not the answer`,
	} {
		r.stdout = stdout
		_, err := RunLookup(context.Background(), "farm", q, reg)
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeUnavailable || err.Error() != le.Error() {
			t.Errorf("%s: err = %#v", stdout, err)
		}
	}
}
