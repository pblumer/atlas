package entra

import (
	"context"
	"strings"
	"testing"
)

// deltaResp builds one page of a delta query: the objects it carries, the @odata.nextLink
// to the next page (empty on the last), and the @odata.deltaLink cursor (present only on
// the last). It is the delta twin of page().
func deltaResp(next, delta string, ids ...string) map[string]any {
	vals := make([]any, 0, len(ids))
	for _, id := range ids {
		vals = append(vals, map[string]any{"id": id})
	}
	p := map[string]any{"value": vals}
	if next != "" {
		p["@odata.nextLink"] = next
	}
	if delta != "" {
		p["@odata.deltaLink"] = delta
	}
	return p
}

// deltaResult reads a delta result variable into its two halves — the changed objects'
// ids and the cursor to persist — so the assertions read as "what changed, and the link
// to resume from".
func deltaResult(t *testing.T, v any) (ids []string, deltaLink string) {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want an object {value, deltaLink}", v)
	}
	deltaLink, _ = obj["deltaLink"].(string)
	list, ok := obj["value"].([]any)
	if !ok {
		t.Fatalf("result.value = %#v, want a JSON array", obj["value"])
	}
	for _, u := range list {
		m, ok := u.(map[string]any)
		if !ok {
			t.Fatalf("changed object = %#v, want an object", u)
		}
		ids = append(ids, m["id"].(string))
	}
	return ids, deltaLink
}

// A delta query is one result across every page, exactly like a listing — and it ends by
// handing back the cursor a next run resumes from. Following the pages and capturing that
// @odata.deltaLink is the worker's job, not something a process models with a loop.
func TestDeltaUsersFollowsPagesAndCapturesTheCursor(t *testing.T) {
	const next1 = "https://graph.microsoft.com/v1.0/users/delta?$skiptoken=A"
	const delta = "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z"
	c := &pagingClient{pages: []any{
		deltaResp(next1, "", "u1", "u2"),
		deltaResp("", delta, "u3"),
	}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "aenderungen",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids, link := deltaResult(t, got["aenderungen"])
	if strings.Join(ids, ",") != "u1,u2,u3" {
		t.Errorf("changed = %v, want every page's, in order", ids)
	}
	if link != delta {
		t.Errorf("deltaLink = %q, want the cursor the last page carried", link)
	}
	// A fresh delta starts at /users/delta; the continuation is Graph's link verbatim.
	if want := []string{"/users/delta", next1}; strings.Join(c.paths, " ") != strings.Join(want, " ") {
		t.Errorf("requested %v, want %v", c.paths, want)
	}
}

// The whole reason the operation returns a cursor: a second run fetches the deltaLink
// verbatim rather than re-enumerating the directory, and reads only what changed since.
func TestDeltaUsersResumesFromTheCursor(t *testing.T) {
	const resume = "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z"
	const delta2 = "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z2"
	c := &pagingClient{pages: []any{deltaResp("", delta2, "u9")}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "aenderungen",
		DeltaLink: resume,
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids, link := deltaResult(t, got["aenderungen"])
	if strings.Join(ids, ",") != "u9" || link != delta2 {
		t.Errorf("resume result = %v / %q, want just what changed and the next cursor", ids, link)
	}
	// The first request is the resume link itself, not a fresh /users/delta.
	if len(c.paths) != 1 || c.paths[0] != resume {
		t.Errorf("requested %v, want the resume deltaLink verbatim", c.paths)
	}
}

// A deletion is a change a leaver flow is watching for. Graph reports it as an item
// carrying an @removed annotation, so the worker passes the page through as-is rather
// than filtering removed objects out — dropping them would hide the very change.
func TestDeltaPassesRemovedObjectsThrough(t *testing.T) {
	const delta = "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z"
	removed := map[string]any{"id": "gone", "@removed": map[string]any{"reason": "changed"}}
	c := &pagingClient{pages: []any{
		map[string]any{"value": []any{map[string]any{"id": "u1"}, removed}, "@odata.deltaLink": delta},
	}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "aenderungen",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	obj := got["aenderungen"].(map[string]any)
	list := obj["value"].([]any)
	if len(list) != 2 {
		t.Fatalf("changed = %#v, want the live and the removed object both", list)
	}
	last := list[1].(map[string]any)
	if _, ok := last["@removed"]; !ok {
		t.Errorf("removed object lost its @removed annotation: %#v", last)
	}
}

// A completed delta always carries a cursor. A last page with neither another page nor a
// deltaLink is malformed: returning "" as the cursor would make the next run silently
// re-enumerate the whole directory, so it is reported instead.
func TestDeltaWithoutACursorIsAnError(t *testing.T) {
	c := &pagingClient{pages: []any{deltaResp("", "", "u1")}}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "aenderungen",
	}, regWith("contoso", c))
	if err == nil || !strings.Contains(err.Error(), "resume from") {
		t.Errorf("err = %v, want a complaint about the missing delta cursor", err)
	}
}

// The maxUsers cap fails the job rather than truncating: a truncated change set would
// persist a deltaLink having skipped changes, so the next run would never see them.
func TestDeltaCapFailsTheJob(t *testing.T) {
	const next1 = "https://graph.microsoft.com/v1.0/users/delta?$skiptoken=A"
	c := &pagingClient{pages: []any{
		deltaResp(next1, "", "u1", "u2"),
		deltaResp("", "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z", "u3"),
	}}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "aenderungen", MaxUsers: 2,
	}, regWith("contoso", c))
	if err == nil || !strings.Contains(err.Error(), "maxUsers cap") {
		t.Errorf("err = %v, want the cap to fail the job", err)
	}
}

// A delta that discards its result is a directory read nothing asked for — the worker
// refuses it, as the compiler does at deploy.
func TestDeltaNeedsAResultVariable(t *testing.T) {
	c := &pagingClient{pages: []any{deltaResp("", "d", "u1")}}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users",
	}, regWith("contoso", c))
	if err == nil || !strings.Contains(err.Error(), "resultVariable") {
		t.Errorf("err = %v, want a missing-resultVariable error", err)
	}
}

// A fresh delta carries $select and $top through to the /delta request; the cursor then
// threads $select forward on its own, so a resume sends nothing but the link.
func TestDeltaFreshRequestCarriesSelectAndTop(t *testing.T) {
	c := &pagingClient{pages: []any{deltaResp("", "d", "u1")}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-groups", ResultVariable: "aenderungen",
		Select: "id,displayName", PageSize: 50,
	}, regWith("contoso", c)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := c.paths[0]
	if !strings.HasPrefix(got, "/groups/delta?") || !strings.Contains(got, "$select=") || !strings.Contains(got, "$top=50") {
		t.Errorf("first request = %q, want /groups/delta with $select and $top", got)
	}
}
