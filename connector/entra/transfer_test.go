package entra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The operations a transfer of resources from one account to another needs
// (ADR-0534): the groups the source account is a
// direct member of, and the recycle bin for users — restore and final deletion.

// list-user-groups reads the user's direct memberships, cast to groups, with the
// listing machinery: every page followed, the authored query fields sent.
func TestListUserGroupsReadsTheDirectMemberships(t *testing.T) {
	c := &pagingClient{pages: []any{
		page("https://graph.microsoft.com/v1.0/users/u%201/memberOf/microsoft.graph.group?$skiptoken=2", "g1", "g2"),
		page("", "g3"),
	}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-user-groups", UserID: " u 1 ", ResultVariable: "gruppen",
		Filter: "securityEnabled eq true", Select: "id,displayName,mailNickname", PageSize: 50, MaxUsers: 10,
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(idsOf(t, out["gruppen"]), ","); got != "g1,g2,g3" {
		t.Errorf("ids = %s, want every page", got)
	}
	path, q := decoded(t, c.paths[0])
	// memberOf, not transitiveMemberOf: a transfer copies the memberships somebody
	// granted, and a nested group's members follow from the group itself.
	if path != "/users/u%201/memberOf/microsoft.graph.group" || q.Get("$count") != "true" {
		t.Errorf("collection = %q, want the trimmed, escaped user's direct memberships cast to groups", path)
	}
	if q.Get("$filter") != "securityEnabled eq true" || q.Get("$select") != "id,displayName,mailNickname" || q.Get("$top") != "50" {
		t.Errorf("query = %v", q)
	}
	if len(c.paths) != 2 || !strings.Contains(c.paths[1], "$skiptoken=2") {
		t.Errorf("paths = %v, want the continuation followed", c.paths)
	}

	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{"no user", Job{Connector: "contoso", Operation: "list-user-groups", ResultVariable: "r"}, "userId"},
		{"no result variable", Job{Connector: "contoso", Operation: "list-user-groups", UserID: "u1"}, "resultVariable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Run(context.Background(), tc.job, regWith("contoso", &pagingClient{})); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// Microsoft documents ConsistencyLevel: eventual and $count as required for an OData
// cast on memberOf — and the cast to microsoft.graph.group is what the operation is —
// so every request of it is an advanced query, whether the model asked for one or not.
func TestListUserGroupsIsAlwaysAnAdvancedQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
	}{
		{"bare", Job{}},
		{"a search", Job{Search: `"displayName:Vertrieb"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &eventualClient{}
			j := tc.job
			j.Connector, j.Operation, j.UserID, j.ResultVariable = "contoso", "list-user-groups", "u1", "r"
			if _, err := Run(context.Background(), j, regWith("contoso", c)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !c.eventual {
				t.Error("the memberships were read without ConsistencyLevel: eventual")
			}
			path, q := decoded(t, c.path)
			if path != "/users/u1/memberOf/microsoft.graph.group" || q.Get("$count") != "true" || q.Get("$search") != j.Search {
				t.Errorf("request = %s", c.path)
			}
		})
	}
}

// eventualClient records the one request a single-page listing makes, header included.
type eventualClient struct {
	path     string
	eventual bool
}

func (c *eventualClient) BaseURL() string { return "https://graph.microsoft.com/v1.0" }
func (c *eventualClient) Call(_ context.Context, r Request) (any, error) {
	c.path, c.eventual = r.Path, r.Eventual
	return map[string]any{"value": []any{}}, nil
}

// restore-user takes a soft-deleted user back out of the recycle bin; Graph answers
// the restored user, which lands in the result variable when one is named.
func TestRestoreUserPostsToTheRecycleBin(t *testing.T) {
	c := &recordingClient{res: map[string]any{"id": "u 1", "userPrincipalName": "anna@contoso.com"}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "restore-user", UserID: " u 1 ", ResultVariable: "konto",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.method != "POST" || c.path != "/directory/deletedItems/u%201/restore" {
		t.Errorf("request = %s %s, want the trimmed, escaped id under /directory/deletedItems", c.method, c.path)
	}
	if c.body != nil {
		t.Errorf("body = %#v, want none", c.body)
	}
	if u, _ := out["konto"].(map[string]any); u["userPrincipalName"] != "anna@contoso.com" {
		t.Errorf("result = %#v, want the restored user", out)
	}
	// The result is optional: a restore whose answer nobody keeps still runs.
	c = &recordingClient{res: map[string]any{"id": "u1"}}
	if out, err := Run(context.Background(), Job{Connector: "contoso", Operation: "restore-user", UserID: "u1"}, regWith("contoso", c)); err != nil || len(out) != 0 {
		t.Errorf("restore without a result variable: out = %v, err = %v", out, err)
	}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "restore-user"}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "userId") {
		t.Errorf("err = %v, want the missing userId named", err)
	}
}

// permanently-delete-user deletes the user from the recycle bin, never from the
// directory: the path is under /directory/deletedItems, so an id of an active user
// is Graph's 404 rather than a deletion.
func TestPermanentlyDeleteUserDeletesFromTheRecycleBin(t *testing.T) {
	c := &recordingClient{}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "permanently-delete-user", UserID: " u 1 ",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.method != "DELETE" || c.path != "/directory/deletedItems/u%201" {
		t.Errorf("request = %s %s, want DELETE on the recycle bin's item", c.method, c.path)
	}
	if c.body != nil || len(out) != 0 {
		t.Errorf("body = %#v, out = %#v, want neither", c.body, out)
	}
	if _, err := Run(context.Background(), Job{Connector: "contoso", Operation: "permanently-delete-user"}, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "userId") {
		t.Errorf("err = %v, want the missing userId named", err)
	}
}

// A 404 from the recycle bin fails the job, over the real client: the id is in no
// recycle bin — never deleted, restored meanwhile, or gone after the 30 days — and
// reading that as "deleted" would let a process record a final deletion that never
// happened. The same holds for restore-user.
func TestRecycleBinNotFoundFailsTheJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"Request_ResourceNotFound","message":"Resource 'u1' does not exist."}}`))
	}))
	t.Cleanup(srv.Close)
	client := NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	for _, op := range []string{"permanently-delete-user", "restore-user"} {
		t.Run(op, func(t *testing.T) {
			out, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: op, UserID: "u1", ResultVariable: "r",
				ErrorCodes: []string{CodeUserNotFound},
			}, regWith("contoso", client))
			var ge *GraphError
			if !errors.As(err, &ge) || ge.Status != http.StatusNotFound {
				t.Fatalf("err = %v (out %v), want Graph's 404 to fail the job", err, out)
			}
		})
	}
}

// The compiler takes the three operations as authored and refuses each without what
// it needs, with the reason named.
func TestCompilerTakesTheTransferOperations(t *testing.T) {
	for _, attrs := range []string{
		`connector="c" operation="list-user-groups" userId="=quelle.id" resultVariable="gruppen"`,
		`connector="c" operation="list-user-groups" userId="u" resultVariable="r" filter="securityEnabled eq true" select="id,displayName" pageSize="100" maxUsers="50" advancedQuery="true"`,
		`connector="c" operation="restore-user" userId="=konto.id"`,
		`connector="c" operation="restore-user" userId="=konto.id" resultVariable="konto"`,
		`connector="c" operation="permanently-delete-user" userId="=konto.id"`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs))); err != nil {
			t.Errorf("%s: %v", attrs, err)
		}
	}
	for _, tc := range []struct{ attrs, want string }{
		{`connector="c" operation="list-user-groups" resultVariable="r"`, "userId"},
		{`connector="c" operation="list-user-groups" userId="u"`, "resultVariable"},
		{`connector="c" operation="list-user-groups" userId="u" resultVariable="r" deltaLink="https://x"`, "not a change-tracking query"},
		{`connector="c" operation="list-user-groups" userId="u" resultVariable="r" advancedQuery="false"`, "always an advanced query"},
		{`connector="c" operation="restore-user"`, "userId"},
		{`connector="c" operation="restore-user" userId="u" attributesVariable="a"`, "sends no body"},
		{`connector="c" operation="restore-user" userId="u" select="id"`, "reads nothing to project"},
		{`connector="c" operation="permanently-delete-user"`, "userId"},
		{`connector="c" operation="permanently-delete-user" userId="u" filter="x eq 1"`, "not a listing"},
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs))); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.attrs, err, tc.want)
		}
	}
}
