package entra

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// invite-guest supplies the redirect Graph requires when the model names none, and
// leaves one it did name alone (ADR-0172, amended 2026-11-03).
func TestInviteGuestDefaultsOnlyAnUnsetRedirect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs map[string]any
		want  any
	}{
		{"unset", map[string]any{"invitedUserEmailAddress": "gast@fabrikam.com"}, "https://myapps.microsoft.com"},
		{"null", map[string]any{"invitedUserEmailAddress": "gast@fabrikam.com", "inviteRedirectUrl": nil}, "https://myapps.microsoft.com"},
		{"empty", map[string]any{"invitedUserEmailAddress": "gast@fabrikam.com", "inviteRedirectUrl": " "}, "https://myapps.microsoft.com"},
		{"authored", map[string]any{"invitedUserEmailAddress": "gast@fabrikam.com", "inviteRedirectUrl": "https://portal.contoso.com/willkommen"}, "https://portal.contoso.com/willkommen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &recordingClient{res: map[string]any{"id": "inv-1", "invitedUser": map[string]any{"id": "guest-1"}}}
			attrs := map[string]any{}
			for k, v := range tc.attrs {
				attrs[k] = v
			}
			out, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: "invite-guest", ResultVariable: "einladung",
				Attributes: attrs,
			}, regWith("contoso", c))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if c.method != "POST" || c.path != "/invitations" {
				t.Errorf("request = %s %s, want POST /invitations", c.method, c.path)
			}
			body, ok := c.body.(map[string]any)
			if !ok {
				t.Fatalf("body = %#v, want an object", c.body)
			}
			if body["inviteRedirectUrl"] != tc.want {
				t.Errorf("inviteRedirectUrl = %#v, want %#v", body["inviteRedirectUrl"], tc.want)
			}
			if body["invitedUserEmailAddress"] != "gast@fabrikam.com" {
				t.Errorf("invitedUserEmailAddress = %#v; the authored attributes must be sent", body["invitedUserEmailAddress"])
			}
			// The default is the worker's, not the model's: the authored map is not
			// written to.
			if _, had := tc.attrs["inviteRedirectUrl"]; !had {
				if _, has := attrs["inviteRedirectUrl"]; has {
					t.Error("the default was written into the authored attributes")
				}
			}
			// The invitation is the answer, and its invitedUser.id the new guest.
			inv, _ := out["einladung"].(map[string]any)
			if guest, _ := inv["invitedUser"].(map[string]any); guest["id"] != "guest-1" {
				t.Errorf("result = %#v, want the invitation Graph answered", out)
			}
		})
	}
	// A redirect of the wrong type is the model's and is sent as authored, so Graph's
	// refusal names it, rather than being replaced by the default in silence.
	if got := withRedirect(map[string]any{"inviteRedirectUrl": 42})["inviteRedirectUrl"]; got != 42 {
		t.Errorf("a non-string redirect became %#v", got)
	}
}

func TestRestoreGroupPostsToTheRecycleBin(t *testing.T) {
	c := &recordingClient{res: map[string]any{"id": "g 1", "displayName": "Vertrieb"}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "restore-group", GroupID: " g 1 ", ResultVariable: "gruppe",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.method != "POST" || c.path != "/directory/deletedItems/g%201/restore" {
		t.Errorf("request = %s %s, want the trimmed, escaped id under /directory/deletedItems", c.method, c.path)
	}
	if c.body != nil {
		t.Errorf("body = %#v, want none", c.body)
	}
	if g, _ := out["gruppe"].(map[string]any); g["displayName"] != "Vertrieb" {
		t.Errorf("result = %#v, want the restored group", out)
	}
	// Without a group there is nothing to restore; the worker says so by name.
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "restore-group"}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "groupId") {
		t.Errorf("err = %v, want the missing groupId named", err)
	}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "invite-guest"}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "attributes") {
		t.Errorf("err = %v, want the missing attributes named", err)
	}
}

// list-deleted-groups is a listing like list-groups: the same query machinery over the
// recycle bin's group collection, every page followed.
func TestListDeletedGroupsUsesTheListingMachinery(t *testing.T) {
	c := &pagingClient{pages: []any{
		page("https://graph.microsoft.com/v1.0/directory/deletedItems/microsoft.graph.group?$skiptoken=2", "g1", "g2"),
		page("", "g3"),
	}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-deleted-groups", ResultVariable: "papierkorb",
		Filter: "startswith(displayName,'Pro')", Select: "id,displayName,deletedDateTime", PageSize: 50, MaxUsers: 10,
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(idsOf(t, out["papierkorb"]), ","); got != "g1,g2,g3" {
		t.Errorf("ids = %s, want every page", got)
	}
	path, q := decoded(t, c.paths[0])
	if path != "/directory/deletedItems/microsoft.graph.group" {
		t.Errorf("collection = %q", path)
	}
	if q.Get("$filter") != "startswith(displayName,'Pro')" || q.Get("$select") != "id,displayName,deletedDateTime" || q.Get("$top") != "50" {
		t.Errorf("query = %v", q)
	}
	if len(c.paths) != 2 || !strings.Contains(c.paths[1], "$skiptoken=2") {
		t.Errorf("paths = %v, want the continuation followed", c.paths)
	}
	// Like every listing it needs somewhere to put the collection.
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "list-deleted-groups"}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "resultVariable") {
		t.Errorf("err = %v, want the missing resultVariable named", err)
	}
}

// The compiler accepts each new operation as authored and refuses each without what it
// needs, with the reason named.
func TestCompilerTakesTheGuestAndRecycleBinOperations(t *testing.T) {
	for _, attrs := range []string{
		`connector="c" operation="invite-guest" attributesVariable="einladung"`,
		`connector="c" operation="invite-guest" attributes="{&#34;invitedUserEmailAddress&#34;:&#34;=antrag.mail&#34;}" resultVariable="r"`,
		`connector="c" operation="restore-group" groupId="=gruppe.id"`,
		`connector="c" operation="list-deleted-groups" resultVariable="r" filter="startswith(displayName,'P')" select="id,displayName" pageSize="100" maxUsers="50" advancedQuery="true"`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs))); err != nil {
			t.Errorf("%s: %v", attrs, err)
		}
	}
	for _, tc := range []struct{ attrs, want string }{
		{`connector="c" operation="invite-guest"`, "attributesVariable"},
		{`connector="c" operation="restore-group"`, "groupId"},
		{`connector="c" operation="restore-group" groupId="g" attributesVariable="a"`, "sends no body"},
		{`connector="c" operation="restore-group" groupId="g" filter="x eq 1"`, "not a listing"},
		{`connector="c" operation="list-deleted-groups"`, "resultVariable"},
		{`connector="c" operation="list-deleted-groups" resultVariable="r" deltaLink="https://x"`, "not a change-tracking query"},
	} {
		_, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.attrs, err, tc.want)
		}
	}
}
