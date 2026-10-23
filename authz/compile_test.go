package authz_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/authz"
)

// Publication is where a policy that cannot mean what it says is refused: a typo that
// silently matched everything, or a rule that can never decide, is a mistake made at
// the write and must be caught there.
func TestPublicationRefusesWhatCannotMeanAnything(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"unknown field", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","when":{"subjectGroupAny":["g"]}}]}`, "unknown field"},
		{"bad id", `{"id":"Bad Id","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`, "id"},
		{"no rules", `{"id":"p","ruleCombining":"first-applicable","rules":[]}`, "no rules"},
		{"no algorithm", `{"id":"p","rules":[{"id":"r","effect":"Permit"}]}`, "ruleCombining"},
		{"unknown algorithm", `{"id":"p","ruleCombining":"permit-overrides","rules":[{"id":"r","effect":"Permit"}]}`, "ruleCombining"},
		{"duplicate rule", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"},{"id":"r","effect":"Deny"}]}`, "twice"},
		{"bad effect", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Allow"}]}`, "effect"},
		{"effect and decision", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","decision":"x"}]}`, "either"},
		{"neither", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r"}]}`, "either"},
		{"obligation on a deny", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Deny","obligations":[{"id":"o","type":"approval","approverGroup":"g","approvals":1,"validForSeconds":3600}]}]}`, "Permit"},
		{"unknown obligation", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"o","type":"pray"}]}]}`, "type"},
		{"approval without a group", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"o","type":"approval","approvals":1,"validForSeconds":3600}]}]}`, "approverGroup"},
		{"approval count", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"o","type":"approval","approverGroup":"g","approvals":0,"validForSeconds":3600}]}]}`, "approvals"},
		{"approval validity", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"o","type":"approval","approverGroup":"g","approvals":1}]}]}`, "validForSeconds"},
		{"fresh auth age", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","obligations":[{"id":"o","type":"fresh-authentication"}]}]}`, "maxAgeSeconds"},
		{"bad reason code", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Deny","reasonCode":"not shouting"}]}`, "reasonCode"},
		{"condition reads an unknown name", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"approved = true"}]}`, "condition"},
		{"condition reads the clock", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"now() > context.time"}]}`, "clock"},
		{"condition reads today", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"today( ) = today()"}]}`, "clock"},
		{"decision without a model", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","decision":"access"}]}`, "dmn"},
		{"decision the model lacks", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","decision":"nope"}],"dmn":` + jsonString(accessTable) + `}`, "nope"},
		{"model reads the clock", `{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","decision":"access"}],"dmn":` + jsonString(strings.Replace(accessTable, `"manager")`, `"manager") and now() != null`, 1)) + `}`, "clock"},
		{"unknown identity mode", `{"id":"p","target":{"identityModes":["root"]},"ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit"}]}`, "identity mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := authz.ParseDocument([]byte(tc.src))
			if err == nil {
				_, _, err = authz.CompilePolicy(context.Background(), doc, 1)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestAConditionMentioningNowInAStringIsNotAClockRead(t *testing.T) {
	doc, err := authz.ParseDocument([]byte(`{"id":"p","ruleCombining":"first-applicable","rules":[{"id":"r","effect":"Permit","condition":"context.processId != \"now()\""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := authz.CompilePolicy(context.Background(), doc, 1); err != nil {
		t.Fatalf("a string literal was read as a call: %v", err)
	}
}

// A deny-list over one spelling of a SharePoint site can be walked around by another
// spelling of the same site; a permit-list cannot. Publication says so.
func TestADenyByResourceNameIsWarnedAbout(t *testing.T) {
	doc, err := authz.ParseDocument([]byte(`{"id":"p","ruleCombining":"first-applicable","rules":[
	  {"id":"no-hr","effect":"Deny","when":{"resourcePrefixes":["sharepoint:site=contoso.sharepoint.com:/sites/hr;"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, warnings, err := authz.CompilePolicy(context.Background(), doc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "permit-list") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestASetRefusesTwoPoliciesOfOneID(t *testing.T) {
	doc, err := authz.ParseDocument([]byte(permitMembers))
	if err != nil {
		t.Fatal(err)
	}
	_, err = authz.CompileSet(context.Background(), []authz.Published{{Document: doc, Version: 1}, {Document: doc, Version: 2}})
	if err == nil || !strings.Contains(err.Error(), "sp-members") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheDigestIsStableAndSensitive(t *testing.T) {
	params := func(site string) map[string]any {
		return map[string]any{"site": site, "list": "Documents", "fields": map[string]any{"b": 2.0, "a": "x"}}
	}
	a, err := authz.Digest("sharepoint", "sp-prod", "delete-file", "sharepoint:site=s", params("s"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := authz.Digest("sharepoint", "sp-prod", "delete-file", "sharepoint:site=s", params("s"))
	if a != b || len(a) != 64 {
		t.Fatalf("digest not stable: %s / %s", a, b)
	}
	for name, other := range map[string]func() (string, error){
		"site": func() (string, error) {
			return authz.Digest("sharepoint", "sp-prod", "delete-file", "sharepoint:site=s", params("t"))
		},
		"operation": func() (string, error) {
			return authz.Digest("sharepoint", "sp-prod", "get-file", "sharepoint:site=s", params("s"))
		},
		"worker": func() (string, error) {
			return authz.Digest("sharepoint", "sp-test", "delete-file", "sharepoint:site=s", params("s"))
		},
		"resource": func() (string, error) {
			return authz.Digest("sharepoint", "sp-prod", "delete-file", "sharepoint:site=t", params("s"))
		},
	} {
		d, err := other()
		if err != nil {
			t.Fatal(err)
		}
		if d == a {
			t.Errorf("changing the %s did not change the digest", name)
		}
	}
}

// A prefix that ends inside a value matches every value that merely begins with it:
// "list=Inv" also admits "list=Invoices-Secret". Publication says so, and a prefix
// that ends at a separator is not warned about.
func TestAPrefixThatEndsInsideAValueIsWarnedAbout(t *testing.T) {
	for prefix, warned := range map[string]bool{
		"sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Inv":    true,
		"sharepoint:site=contoso.sharepoint.com:/sites/hr":             true,
		"sharepoint:site=contoso.sharepoint.com:/sites/hr;":            false,
		"sharepoint:site=contoso.sharepoint.com:/sites/hr;path=/Docs/": false,
		"sharepoint:": false,
	} {
		doc, err := authz.ParseDocument([]byte(`{"id":"p","ruleCombining":"first-applicable","rules":[
		  {"id":"read","effect":"Permit","when":{"resourcePrefixes":["` + prefix + `"]}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		_, warnings, err := authz.CompilePolicy(context.Background(), doc, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(warnings) == 1 && strings.Contains(warnings[0], "boundary"); got != warned {
			t.Errorf("%q: warnings = %v", prefix, warnings)
		}
	}
}
