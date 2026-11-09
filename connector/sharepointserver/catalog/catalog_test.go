package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCheckAuthoredRefusesWhatCanNeverWork(t *testing.T) {
	op := func(n string) Op {
		o, ok := Lookup(n)
		if !ok {
			t.Fatalf("no op %s", n)
		}
		return o
	}
	base := map[string]any{"Site": "alpha", "Name": "Alpha", "OwnerAlias": `C\anna`}
	for _, tc := range []struct {
		name, op string
		params   map[string]any
		want     string
	}{
		{"unknown parameter", "get-site", map[string]any{"Site": "a", "Url": "x"}, "does not take"},
		{"missing required", "get-site", nil, "needs the parameter"},
		{"a URL is no site", "get-site", map[string]any{"Site": "http://evil/sites/x"}, "no URL"},
		{"account form", "create-site", map[string]any{"Site": "a", "Name": "A", "OwnerAlias": "anna"}, `DOMAIN\name`},
		{"nothing to change", "update-site-admins", map[string]any{"Site": "a"}, "changes nothing"},
		{"group", "update-group-members", map[string]any{"Site": "a", "Group": "Besitzer", "Add": `C\a`}, "owners, members or visitors"},
		{"a list of accounts", "update-group-members", map[string]any{"Site": "a", "Group": "members", "Add": []any{`C\a`, "bad"}}, "or a list"},
		{"a list holds text", "update-group-members", map[string]any{"Site": "a", "Group": "members", "Add": []any{7}}, "or a list"},
		{"quota above zero", "set-site-quota", map[string]any{"Site": "a", "QuotaGB": 0.0}, "above 0"},
		{"warning below 100", "set-site-quota", map[string]any{"Site": "a", "QuotaGB": 5.0, "WarningPercent": 100.0}, "below 100"},
		{"language", "create-site", merged(base, "Language", "de"), "LCID"},
		{"template", "create-site", merged(base, "Template", "team"), "STS#0"},
		{"database", "create-site", merged(base, "ContentDatabase", "a;b"), "letters"},
		{"mail", "create-site", merged(base, "OwnerEmail", "x"), "e-mail"},
		{"name", "create-site", merged(base, "Name", " "), "one line"},
		{"property key", "create-site", merged(base, "Properties", map[string]any{"x": "y"}), "atlas."},
		{"properties", "create-site", merged(base, "Properties", "x"), "object"},
		{"limit", "list-sites", map[string]any{"Limit": MaxLimit + 1}, "whole number"},
	} {
		if err := op(tc.op).CheckAuthored(tc.op, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := op("create-site").CheckAuthored("create-site", merged(base, "OwnerAlias", "=besitzer")); err != nil {
		t.Errorf("a FEEL value is the worker's to check: %v", err)
	}
	if err := op("list-sites").CheckAuthored("list-sites", nil); err != nil {
		t.Errorf("list-sites needs nothing: %v", err)
	}
}

func merged(base map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for a, b := range base {
		out[a] = b
	}
	out[k] = v
	return out
}

func TestBuildFillsInWhatTheCmdletNeeds(t *testing.T) {
	create, _ := Lookup("create-site")
	inv, err := create.Build("create-site", map[string]any{"Site": "alpha", "Name": "A", "OwnerAlias": "a@c.ch", "Language": json.Number("1031")})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Site != "alpha" || inv.Args["Template"] != DefaultTemplate || inv.Args["Language"] != int64(1031) {
		t.Errorf("inv = %+v", inv)
	}
	quota, _ := Lookup("set-site-quota")
	inv, err = quota.Build("set-site-quota", map[string]any{"Site": "a", "QuotaGB": json.Number("2"), "WarningPercent": "50"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Args["MaxSize"] != int64(2<<30) || inv.Args["WarningSize"] != int64(1<<30) {
		t.Errorf("quota = %v", inv.Args)
	}
	inv, _ = quota.Build("set-site-quota", map[string]any{"Site": "a", "QuotaGB": 1.0})
	if inv.Args["WarningSize"] != int64(966367642) {
		t.Errorf("default warning = %v", inv.Args["WarningSize"])
	}
	list, _ := Lookup("list-sites")
	inv, _ = list.Build("list-sites", nil)
	if inv.Args["Limit"] != int64(DefaultLimit) {
		t.Errorf("limit = %v", inv.Args)
	}
	inv, _ = list.Build("list-sites", map[string]any{"Limit": 7.0})
	if inv.Args["Limit"] != int64(7) {
		t.Errorf("limit = %v", inv.Args)
	}
	group, _ := Lookup("update-group-members")
	inv, _ = group.Build("update-group-members", map[string]any{"Site": "a", "Group": "members", "Add": `C\b`})
	if got := inv.Args["Add"].([]string); len(got) != 1 || got[0] != `C\b` {
		t.Errorf("add = %v", inv.Args)
	}
}

func TestBuildReChecksWhatFeelProduced(t *testing.T) {
	get, _ := Lookup("get-site")
	if _, err := get.Build("get-site", map[string]any{"Site": "=b"}); err == nil {
		t.Error("a value that still reads as FEEL on the worker is a wrong value")
	}
	if _, err := get.Build("get-site", map[string]any{"Site": 7}); err == nil {
		t.Error("a site must be text")
	}
}

func TestNumberReadingAndHelpers(t *testing.T) {
	for _, v := range []any{json.Number("3"), 3.0, "3"} {
		if n, ok := wholeNumber(v); !ok || n != 3 {
			t.Errorf("whole %v = %d %v", v, n, ok)
		}
		if n, ok := decimal(v); !ok || n != 3 {
			t.Errorf("decimal %v = %v %v", v, n, ok)
		}
	}
	for _, v := range []any{3.5, true} {
		if _, ok := wholeNumber(v); ok {
			t.Errorf("whole %v should fail", v)
		}
	}
	if _, ok := decimal(true); ok {
		t.Error("decimal(true) should fail")
	}
	if _, ok := accountList(7); ok {
		t.Error("accountList(7) should fail")
	}
	if len(Names()) != len(Ops) || !ValidGroup(GroupVisitors) || ValidSite("/x") {
		t.Error("helpers disagree with the table")
	}
	if len(Areas) == 0 {
		t.Error("no areas")
	}
	for name, o := range Ops {
		found := false
		for _, a := range Areas {
			found = found || a == o.Area
		}
		if !found {
			t.Errorf("%s is in no area", name)
		}
	}
}
