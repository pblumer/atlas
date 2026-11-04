package entra

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// get-sync-behavior reads a synchronised user's source of authority from
// /users/{id}/onPremisesSyncBehavior, projected to isCloudManaged, and answers the
// object Graph returned (ADR-0172, amended 2026-11-04).
func TestGetSyncBehaviorReadsTheSourceOfAuthority(t *testing.T) {
	c := &recordingClient{res: map[string]any{"isCloudManaged": false}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "get-sync-behavior", UserID: " u 1 ", ResultVariable: "soa",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.method != "GET" || c.path != "/users/u%201/onPremisesSyncBehavior?$select=isCloudManaged" {
		t.Errorf("request = %s %s, want the user's onPremisesSyncBehavior with isCloudManaged selected", c.method, c.path)
	}
	if c.body != nil {
		t.Errorf("body = %#v, want none", c.body)
	}
	if soa, _ := out["soa"].(map[string]any); soa["isCloudManaged"] != false {
		t.Errorf("result = %#v, want the object Graph answered", out)
	}
}

// set-sync-behavior PATCHes the one property, and refuses a body that is not exactly
// {"isCloudManaged": <bool>} before anything reaches Graph: a transfer of authority is
// not a call to send with a typo or a string "true" in it.
func TestSetSyncBehaviorSendsOnlyIsCloudManaged(t *testing.T) {
	c := &recordingClient{res: nil}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "set-sync-behavior", UserID: "u1",
		Attributes: map[string]any{"isCloudManaged": true},
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.method != "PATCH" || c.path != "/users/u1/onPremisesSyncBehavior" {
		t.Errorf("request = %s %s, want PATCH on the user's onPremisesSyncBehavior", c.method, c.path)
	}
	if m, _ := c.body.(map[string]any); len(m) != 1 || m["isCloudManaged"] != true {
		t.Errorf("body = %#v, want {isCloudManaged: true}", c.body)
	}
	if len(out) != 0 {
		t.Errorf("result = %#v, want nothing (Graph answers 204)", out)
	}

	for _, tc := range []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		{"misspelt", map[string]any{"isCloudManged": true}, "exactly one attribute"},
		{"a second property", map[string]any{"isCloudManaged": true, "displayName": "x"}, "exactly one attribute"},
		{"a string", map[string]any{"isCloudManaged": "true"}, "true or false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &recordingClient{}
			_, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: "set-sync-behavior", UserID: "u1", Attributes: tc.attrs,
			}, regWith("contoso", c))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
			if c.method != "" {
				t.Errorf("a refused body still reached Graph: %s %s", c.method, c.path)
			}
		})
	}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "set-sync-behavior", Attributes: map[string]any{"isCloudManaged": true}}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "userId") {
		t.Errorf("err = %v, want the missing userId named", err)
	}
}

// The compiler takes both operations as authored and refuses each without what it
// needs, with the reason named.
func TestCompilerTakesTheSyncBehaviorOperations(t *testing.T) {
	for _, attrs := range []string{
		`connector="c" operation="get-sync-behavior" userId="=konto.id" resultVariable="soa"`,
		`connector="c" operation="set-sync-behavior" userId="=konto.id" attributes="{&#34;isCloudManaged&#34;: true}"`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs))); err != nil {
			t.Errorf("%s: %v", attrs, err)
		}
	}
	for _, tc := range []struct{ attrs, want string }{
		{`connector="c" operation="get-sync-behavior"`, "userId"},
		{`connector="c" operation="get-sync-behavior" userId="u" attributesVariable="a"`, "sends no body"},
		{`connector="c" operation="set-sync-behavior" userId="u"`, "attributesVariable"},
		{`connector="c" operation="set-sync-behavior" attributesVariable="a"`, "userId"},
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs))); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.attrs, err, tc.want)
		}
	}
}
