package api

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// groupsPathsSeed files group records directly, on the loop.
func groupsPathsSeed(t *testing.T, srv *Server, recs ...group) {
	t.Helper()
	var err error
	srv.do(func() {
		for _, g := range recs {
			if err = srv.groups.Save(g); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed groups: %v", err)
	}
}

// groupsPathsGet reads one group back, on the loop.
func groupsPathsGet(t *testing.T, srv *Server, id string) group {
	t.Helper()
	var (
		g   group
		ok  bool
		err error
	)
	srv.do(func() { g, ok, err = srv.groups.Get(id) })
	if err != nil || !ok {
		t.Fatalf("read group %s: ok=%v err=%v", id, ok, err)
	}
	return g
}

// TestGroupsRefusesAMalformedBody: a create or rename whose body is not JSON names
// nothing, and no group is made or renamed from it.
func TestGroupsRefusesAMalformedBody(t *testing.T) {
	srv := newServerForErrors(t)
	groupsPathsSeed(t, srv, group{ID: "g-1", Name: "Team", CreatedAt: 1})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/groups"},
		{http.MethodPatch, "/api/v1/groups/g-1"},
	} {
		if code, body := serveInternal(t, srv, tc.method, tc.path, `{"name":`, "application/json"); code != http.StatusBadRequest {
			t.Errorf("%s %s with a malformed body: %d (%s), want 400", tc.method, tc.path, code, body)
		}
	}
	if g := groupsPathsGet(t, srv, "g-1"); g.Name != "Team" {
		t.Errorf("name = %q after refused requests", g.Name)
	}
}

// TestGroupsListsOldestFirst: the list an administrator works down is in the order
// the groups were made, so a new group lands at the end rather than reshuffling it.
func TestGroupsListsOldestFirst(t *testing.T) {
	srv := newServerForErrors(t)
	groupsPathsSeed(t, srv,
		group{ID: "g-a", Name: "Newer", CreatedAt: 200},
		group{ID: "g-b", Name: "Older", CreatedAt: 100})
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/groups", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []groupView
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 2 || rows[0].ID != "g-b" || rows[1].ID != "g-a" {
		t.Errorf("rows = %+v, want the older group first", rows)
	}
}

// TestGroupsRenameCannotCheckANameAgainstGroupsItCannotRead: names are unique, and
// uniqueness is a question about every group. With one of them unreadable the rename
// cannot know it is not taking a name already in use, so it stops.
func TestGroupsRenameCannotCheckANameAgainstGroupsItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	groupsPathsSeed(t, srv, group{ID: "g-1", Name: "Team", CreatedAt: 1})
	corrupt(t, srv.groups.Dir(), "g-broken")
	code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/groups/g-1", `{"name":"Crew"}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "rename group") {
		t.Fatalf("unreadable sibling: %d (%s), want 500 'rename group'", code, body)
	}
	if g := groupsPathsGet(t, srv, "g-1"); g.Name != "Team" {
		t.Errorf("renamed to %q although the name could not be checked", g.Name)
	}
}

// TestGroupsReportsAChangeItCouldNotStore: a rename or a membership change that did
// not land must not come back as the changed group. Membership least of all — the
// administrator would believe somebody's access was granted or withdrawn.
func TestGroupsReportsAChangeItCouldNotStore(t *testing.T) {
	srv := newServerForErrors(t)
	groupsPathsSeed(t, srv, group{ID: "g-1", Name: "Team", Members: []string{"usr_bob"}, CreatedAt: 1})
	var err error
	srv.do(func() { err = srv.users.Save(User{ID: "usr_cleo", Username: "cleo"}) })
	if err != nil {
		t.Fatal(err)
	}
	before := groupsPathsGet(t, srv, "g-1")
	// A directory where the store writes its temporary file: the group reads, the
	// write fails.
	if err := os.MkdirAll(srv.groups.FileFor("g-1")+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ method, path, body, says string }{
		{http.MethodPatch, "/api/v1/groups/g-1", `{"name":"Crew"}`, "rename group"},
		{http.MethodPut, "/api/v1/groups/g-1/members/usr_cleo", "", "add group member"},
		{http.MethodDelete, "/api/v1/groups/g-1/members/usr_bob", "", "remove group member"},
	} {
		code, body := serveInternal(t, srv, tc.method, tc.path, tc.body, "application/json")
		if code != http.StatusInternalServerError || !strings.Contains(string(body), tc.says) {
			t.Errorf("%s %s: %d (%s), want 500 %q", tc.method, tc.path, code, body, tc.says)
		}
	}
	if after := groupsPathsGet(t, srv, "g-1"); !reflect.DeepEqual(before, after) {
		t.Errorf("a failed write still changed the group: %+v -> %+v", before, after)
	}
}
