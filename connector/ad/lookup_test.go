package ad_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/lookup"
)

// A form lookup against AD (ADR-0473, amended 2026-11-04), answered by the mock forest
// through the same RunLookup the worker calls.

func lookupForest() (*ad.MockDirectory, *ad.Registry) {
	person := func(cn, sam, upn, display, dept string, uac string) ad.Entry {
		return ad.Entry{
			DN: "CN=" + cn + ",OU=Personen,OU=AtlasTest,DC=atlastest,DC=internal",
			Attributes: map[string][]string{
				"objectClass":        {"top", "person", "organizationalPerson", "user"},
				"cn":                 {cn},
				"sAMAccountName":     {sam},
				"userPrincipalName":  {upn},
				"displayName":        {display},
				"department":         {dept},
				"userAccountControl": {uac},
			},
		}
	}
	d := ad.NewMockDirectory(
		person("Anna Muster", "anna.muster", "anna.muster@example.org", "Anna Muster", "FIN", "512"),
		person("Anton Alt", "anton.alt", "anton.alt@example.org", "Anton Alt", "FIN", "514"),
		person("Beat Beispiel", "beat.beispiel", "beat.beispiel@example.org", "Beat Beispiel", "IT", "512"),
		person("Mü Ller", "mueller", "mueller@example.org", "Müller Hans", "IT", "512"),
		ad.Entry{
			DN: "CN=WS01,OU=Computer,DC=atlastest,DC=internal",
			Attributes: map[string][]string{
				"objectClass":    {"top", "person", "organizationalPerson", "user", "computer"},
				"sAMAccountName": {"WS01$"},
				"displayName":    {"Anna's workstation"},
			},
		},
		ad.Entry{
			DN: "CN=SG-APP-CRM-Benutzer,OU=Gruppen,DC=atlastest,DC=internal",
			Attributes: map[string][]string{
				"objectClass":    {"top", "group"},
				"cn":             {"SG-APP-CRM-Benutzer"},
				"sAMAccountName": {"SG-APP-CRM-Benutzer"},
				"description":    {"CRM"},
			},
		},
	)
	reg := ad.NewRegistry()
	reg.Register("ad_verzeichnis", ad.Directory{URL: "ldaps://dc01.atlastest.internal:636", BindDN: "CN=svc,DC=atlastest,DC=internal", Password: "x"})
	return d, reg
}

func userQuery(term string) lookup.Query {
	return lookup.Query{
		Resource:     "users",
		Search:       term,
		SearchFields: []string{"displayName", "sAMAccountName"},
		Properties:   []string{"sAMAccountName", "displayName", "userPrincipalName"},
		ActiveOnly:   true,
	}
}

func values(p lookup.Page, prop string) []string {
	var out []string
	for _, it := range p.Items {
		s, _ := it[prop].(string)
		out = append(out, s)
	}
	return out
}

func TestADLookupFindsEnabledPeopleByPrefixAndLeavesComputersOut(t *testing.T) {
	d, reg := lookupForest()
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", userQuery("an"), d, reg)
	if err != nil {
		t.Fatal(err)
	}
	// Anton is disabled (514), WS01 is a computer; both start with "An".
	if v := values(got, "sAMAccountName"); len(v) != 1 || v[0] != "anna.muster" {
		t.Fatalf("got %v, want only anna.muster", v)
	}
	if got.Next != "" {
		t.Errorf("a lookup has no continuation, got %q", got.Next)
	}
	q := userQuery("an")
	q.ActiveOnly = false
	got, err = ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if v := values(got, "sAMAccountName"); len(v) != 2 || v[0] != "anna.muster" || v[1] != "anton.alt" {
		t.Fatalf("without activeOnly got %v, want anna.muster and anton.alt sorted by name", v)
	}
}

func TestADLookupProjectsExactlyTheNamedProperties(t *testing.T) {
	d, reg := lookupForest()
	q := userQuery("beat")
	q.Properties = []string{"sAMAccountName", "mail"}
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %v", got.Items)
	}
	it := got.Items[0]
	if len(it) != 2 || it["sAMAccountName"] != "beat.beispiel" || it["mail"] != nil {
		t.Fatalf("record %v: want sAMAccountName and a null mail, nothing else", it)
	}
}

func TestADLookupMatchesNonASCIITermsAndFilters(t *testing.T) {
	d, reg := lookupForest()
	q := userQuery("Mü")
	q.Filters = []lookup.Filter{{Property: "department", Value: "IT"}}
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if v := values(got, "sAMAccountName"); len(v) != 1 || v[0] != "mueller" {
		t.Fatalf("got %v, want mueller", v)
	}
	q.Filters = []lookup.Filter{{Property: "department", Value: "FIN"}}
	got, err = ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("department FIN still found %v", got.Items)
	}
}

func TestADLookupTreatsFilterSyntaxInATermAsText(t *testing.T) {
	d, reg := lookupForest()
	for _, term := range []string{"*", "*)(objectClass=*", `\2a`, "a)(|(sAMAccountName=*"} {
		got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", userQuery(term), d, reg)
		if err != nil {
			t.Fatalf("%q: %v", term, err)
		}
		if len(got.Items) != 0 {
			t.Errorf("%q widened the search: %v", term, values(got, "sAMAccountName"))
		}
	}
}

func TestADLookupByValueFindsOneAccountByEitherName(t *testing.T) {
	d, reg := lookupForest()
	q := lookup.Query{Resource: "users", Value: "beat.beispiel", Properties: []string{"sAMAccountName", "displayName"}, ActiveOnly: true}
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil || len(got.Items) != 1 || got.Items[0]["displayName"] != "Beat Beispiel" {
		t.Fatalf("by sAMAccountName: %v %v", got.Items, err)
	}
	q = lookup.Query{Resource: "users", Value: "anna.muster@example.org", ValueProperty: "userPrincipalName", Properties: []string{"userPrincipalName"}, ActiveOnly: true}
	got, err = ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("by userPrincipalName: %v %v", got.Items, err)
	}
	// A disabled account is not found when the field wants enabled ones.
	q = lookup.Query{Resource: "users", Value: "anton.alt", Properties: []string{"sAMAccountName"}, ActiveOnly: true}
	got, err = ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("disabled account: %v %v", got.Items, err)
	}
}

func TestADLookupSearchesGroups(t *testing.T) {
	d, reg := lookupForest()
	q := lookup.Query{Resource: "groups", Search: "sg-app", SearchFields: []string{"cn"}, Properties: []string{"sAMAccountName", "description"}}
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if v := values(got, "sAMAccountName"); len(v) != 1 || v[0] != "SG-APP-CRM-Benutzer" {
		t.Fatalf("got %v", v)
	}
}

func TestADLookupHonoursThePageSize(t *testing.T) {
	d, reg := lookupForest()
	q := userQuery("")
	q.PageSize = 2
	got, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d records, want 2", len(got.Items))
	}
}

func TestADLookupRefusesWhatItsTableDoesNotAllow(t *testing.T) {
	d, reg := lookupForest()
	bad := []lookup.Query{
		{Resource: "computers", Properties: []string{"sAMAccountName"}},
		{Resource: "users", Properties: []string{"displayName"}},                  // value property not read
		{Resource: "users", Properties: []string{"sAMAccountName", "objectGUID"}}, // binary, never readable
		{Resource: "users", Properties: []string{"sAMAccountName", "unicodePwd"}}, // not readable
		{Resource: "users", Properties: []string{"sAMAccountName"}, SearchFields: []string{"info"}, Search: "a"},
		{Resource: "users", Properties: []string{"sAMAccountName"}, Filters: []lookup.Filter{{Property: "memberOf", Value: "x"}}},
		{Resource: "users", Properties: []string{"sAMAccountName"}, ValueProperty: "objectGUID", Value: "x"},
		{Resource: "users", Properties: []string{"sAMAccountName"}, Value: "a*b"},
		{Resource: "users", Properties: []string{"sAMAccountName"}, Next: "cookie"},
		{Resource: "groups", Properties: []string{"sAMAccountName"}, ActiveOnly: true},
		{Resource: "users", Properties: []string{"sAMAccountName"}, PageSize: lookup.MaxPageSize + 1},
	}
	for i, q := range bad {
		_, err := ad.RunLookup(context.Background(), "ad_verzeichnis", q, d, reg)
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("query %d: got %v, want %s", i, err, lookup.CodeInvalid)
		}
	}
}

func TestADLookupNamesAnUnknownDirectoryUnconfigured(t *testing.T) {
	d, reg := lookupForest()
	for _, r := range []*ad.Registry{reg, nil} {
		_, err := ad.RunLookup(context.Background(), "anderes", userQuery("a"), d, r)
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeUnconfigured {
			t.Errorf("got %v, want %s", err, lookup.CodeUnconfigured)
		}
	}
}

func TestValidLookupValueHoldsLogonNamesToTheirShape(t *testing.T) {
	for v, want := range map[string]bool{
		"anna.muster":         true,
		"SG-APP-CRM-Benutzer": true,
		"svc-backup$":         true,
		"":                    false,
		" anna":               false,
		"a*":                  false,
		"a)(b":                true, // AD allows parentheses; the filter escapes them
		"anna@example.org":    false,
		"a\\b":                false,
	} {
		if got := ad.ValidLookupValue("users", v); got != want {
			t.Errorf("ValidLookupValue(users, %q) = %v, want %v", v, got, want)
		}
	}
	if !ad.ValidLookupValueAs("users", "userPrincipalName", "anna@example.org") {
		t.Error("a user principal name is a value of users")
	}
	if ad.ValidLookupValueAs("groups", "userPrincipalName", "anna@example.org") {
		t.Error("a group has no user principal name to be identified by")
	}
}
