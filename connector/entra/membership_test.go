package entra

import (
	"context"
	"strings"
	"testing"
)

// The two membership reads exist for reconciliation (ADR-0334), and what that asks
// of them is narrower than "a listing that works": a run declares it read a scope
// **completely**, and Atlas then reads absence inside that scope as a finding. So a
// short answer here is not a smaller answer — it is a report that everybody the
// reply left out has lost their access.
//
// These tests hold the three properties that promise rests on: the request
// addresses the object the model named, every page is followed, and a listing that
// outgrows its bound fails instead of coming back short.

// The collection hangs off one object, so the id the model authored has to reach
// the path — escaped, because a user principal name is not URL-safe.
func TestMembershipReadsAddressTheObjectTheyName(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{
			name: "a group's members, users only",
			job:  Job{Operation: "list-group-members", GroupID: "8f9a-b2", ResultVariable: "leute"},
			want: "/groups/8f9a-b2/members/microsoft.graph.user",
		},
		{
			// A user principal name goes in as written: "@" and "+" are legal in a
			// path segment, and escaping them would address an account Graph does not
			// have. This is the same treatment get-user and disable already give an
			// authored id.
			name: "a user's groups, groups only",
			job:  Job{Operation: "list-user-groups", UserID: "arno@contoso.com", ResultVariable: "gruppen"},
			want: "/users/arno@contoso.com/memberOf/microsoft.graph.group",
		},
		{
			// What must not go in as written is a character that ends the segment. An
			// id carrying a slash would otherwise address something else entirely —
			// and a reconciliation reading that answer would report every right of
			// the account it meant to read as missing.
			name: "an id that would break out of its segment",
			job:  Job{Operation: "list-user-groups", UserID: "a b/c", ResultVariable: "gruppen"},
			want: "/users/a%20b%2Fc/memberOf/microsoft.graph.group",
		},
		{
			// The query fields apply as they do to any listing: the collection is
			// narrowed, the object it hangs off is not.
			name: "with a projection",
			job: Job{Operation: "list-group-members", GroupID: "g1", Select: "id,userPrincipalName",
				ResultVariable: "leute"},
			want: "/groups/g1/members/microsoft.graph.user?$select=id%2CuserPrincipalName",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &pagingClient{pages: []any{page("")}}
			tc.job.Connector = "contoso"
			if _, err := Run(context.Background(), tc.job, regWith("contoso", c)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(c.paths) == 0 || c.paths[0] != tc.want {
				t.Errorf("requested %v, want %q first", c.paths, tc.want)
			}
		})
	}
}

// A membership listing pages like any other, and the continuation is Graph's own
// link. A model that had to follow it would be carrying the paging protocol in its
// diagram — and a run that stopped at page one would promise a complete scope it
// had not read.
func TestMembershipReadsFollowEveryPage(t *testing.T) {
	const next = "https://graph.microsoft.com/v1.0/groups/g1/members/microsoft.graph.user?$skiptoken=A"
	c := &pagingClient{pages: []any{page(next, "u1", "u2"), page("", "u3")}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-group-members", GroupID: "g1", ResultVariable: "leute",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ids := idsOf(t, got["leute"]); strings.Join(ids, ",") != "u1,u2,u3" {
		t.Errorf("members = %v, want every page's, in order", ids)
	}
	if want := []string{"/groups/g1/members/microsoft.graph.user", next}; strings.Join(c.paths, " ") != strings.Join(want, " ") {
		t.Errorf("requested %v, want %v", c.paths, want)
	}
}

// The cap fails the job rather than truncating, and here that is the difference
// between a wrong answer and no answer: a truncated membership read, reported as a
// complete scope, tells a reconciliation that the members it did not see are gone.
func TestMembershipReadAboveItsCapFailsRatherThanTruncating(t *testing.T) {
	c := &pagingClient{pages: []any{page("", "u1", "u2", "u3")}}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-group-members", GroupID: "g1",
		MaxUsers: 2, ResultVariable: "leute",
	}, regWith("contoso", c))
	if err == nil {
		t.Fatal("a listing past its cap must fail; truncating it would be a wrong answer")
	}
	if !strings.Contains(err.Error(), "maxUsers") {
		t.Errorf("error = %v, want it to name the bound that stopped it", err)
	}
}

// The object is required on the worker as well as at deploy. A membership read
// without it would address the tenant-wide collection — every user in the
// directory answered to a question about one group.
func TestMembershipReadsRefuseWithoutTheirObject(t *testing.T) {
	for _, tc := range []struct{ name, op, want string }{
		{"a group's members with no group", "list-group-members", "groupId"},
		{"a user's groups with no user", "list-user-groups", "userId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &pagingClient{pages: []any{page("")}}
			_, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: tc.op, ResultVariable: "r",
			}, regWith("contoso", c))
			if err == nil {
				t.Fatal("want a refusal naming the missing id, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
			if len(c.paths) != 0 {
				t.Errorf("it called Graph anyway: %v", c.paths)
			}
		})
	}
}
