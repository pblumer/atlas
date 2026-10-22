package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/order"
)

// The line manager is a reference on the account (ADR-0470).
//
// These hold the three places it is decided and read: the mirror that reports it, the
// approval that starts with it, and the refusal when there is nobody to start with.

// managerRef is Graph's manager@delta entry for one manager.
func managerRef(id string) []directoryMemberRef {
	return []directoryMemberRef{{ID: id, Type: "#microsoft.graph.user"}}
}

// recordOf finds the account a plan writes for a directory object.
func recordOf(t *testing.T, plan directoryPlan, oid string) User {
	t.Helper()
	for _, dec := range plan.Users {
		if dec.ObjectID == oid && dec.Record.ID != "" {
			return dec.Record
		}
	}
	t.Fatalf("the plan writes nothing for %s: %+v", oid, plan.Users)
	return User{}
}

func TestTheMirrorRecordsWhoSomebodyReportsTo(t *testing.T) {
	boss := person("oid-boss", "chefin@example.org", "Chefin")
	ada := person("oid-ada", "ada@example.org", "Ada")
	ada.ManagerDelta = managerRef("OID-BOSS") // an object id is a GUID; case is not information
	plan := decide(t, directorySyncMessage{Users: []directoryUser{ada, boss}}, directorySyncState{}, nil, nil)

	got, chefin := recordOf(t, plan, "oid-ada"), recordOf(t, plan, "oid-boss")
	if got.ManagerDirectoryID != "oid-boss" {
		t.Errorf("managerDirectoryId = %q, want the directory's id held", got.ManagerDirectoryID)
	}
	// The manager's account is made later in the same message, and the reference
	// still resolves: an order inside a page is not an order of the directory.
	if got.ManagerID != chefin.ID || got.ManagerID == "" {
		t.Errorf("managerId = %q, want %q", got.ManagerID, chefin.ID)
	}
}

func TestAManagerMayArriveBeforeTheirAccount(t *testing.T) {
	ada := person("oid-ada", "ada@example.org", "Ada")
	ada.ManagerDelta = managerRef("oid-late")
	first := decide(t, directorySyncMessage{Users: []directoryUser{ada}}, directorySyncState{}, nil, nil)
	held := recordOf(t, first, "oid-ada")
	if held.ManagerDirectoryID != "oid-late" || held.ManagerID != "" {
		t.Fatalf("held = %+v, want the directory id kept and nothing resolved", held)
	}
	if !hasNote(first, noteUnresolved, "resolves on the run that reads the account") {
		t.Errorf("an unresolved manager must be reported: %+v", first.Notes)
	}
	// Each run here mints ids from the start again; an applied plan would not.
	held.ID = "usr_ada"

	// The next run reads the manager and does not mention Ada. A delta read names an
	// unchanged person once, so the reference has to resolve without her.
	second := decide(t, directorySyncMessage{Users: []directoryUser{person("oid-late", "late@example.org", "Late")}},
		directorySyncState{}, []User{held}, nil)
	late := recordOf(t, second, "oid-late")
	resolved := recordOf(t, second, "oid-ada")
	if resolved.ManagerID != late.ID {
		t.Errorf("managerId = %q, want %q once the manager's account exists", resolved.ManagerID, late.ID)
	}
	if second.Counts.UsersUpdated != 1 {
		t.Errorf("counts = %+v, want Ada counted as updated", second.Counts)
	}
}

func TestTheDirectoryCanTakeAManagerAway(t *testing.T) {
	users := []User{
		{ID: "usr_boss", Username: "chefin", Source: SourceEntra, DirectoryID: "oid-boss", Roles: []string{RoleUser}},
		{ID: "usr_ada", Username: "ada", Source: SourceEntra, DirectoryID: "oid-ada", Roles: []string{RoleUser},
			ManagerID: "usr_boss", ManagerDirectoryID: "oid-boss"},
	}
	ada := person("oid-ada", "ada@example.org", "Ada")
	ada.ManagerDelta = []directoryMemberRef{{ID: "oid-boss", Removed: &directoryRemoval{Reason: "deleted"}}}
	got := recordOf(t, decide(t, directorySyncMessage{Users: []directoryUser{ada}}, directorySyncState{}, users, nil), "oid-ada")
	if got.ManagerID != "" || got.ManagerDirectoryID != "" {
		t.Errorf("record = %+v, want the manager gone", got)
	}
}

func TestSayingNothingAboutAManagerChangesNothing(t *testing.T) {
	users := []User{
		{ID: "usr_boss", Username: "chefin", Source: SourceEntra, DirectoryID: "oid-boss", Roles: []string{RoleUser}},
		{ID: "usr_ada", Username: "ada", Email: "ada@example.org", DisplayName: "Ada", Source: SourceEntra,
			DirectoryID: "oid-ada", Roles: []string{RoleUser}, ManagerID: "usr_boss", ManagerDirectoryID: "oid-boss"},
		// An account the directory says nothing about, whose manager an administrator set.
		{ID: "usr_lokal", Username: "lokal", Source: SourceLocal, Roles: []string{RoleUser}, ManagerID: "usr_boss"},
	}
	plan := decide(t, directorySyncMessage{Users: []directoryUser{person("oid-ada", "ada@example.org", "Ada")}},
		directorySyncState{}, users, nil)
	if plan.writes() {
		t.Errorf("a page that does not mention a manager would write: %+v", plan.Users)
	}
}

func TestAPlainManagerObjectIsReadToo(t *testing.T) {
	var ada directoryUser
	if err := json.Unmarshal([]byte(`{"id":"oid-ada","userPrincipalName":"ada@example.org",
		"manager":{"@odata.type":"#microsoft.graph.user","id":"oid-boss"}}`), &ada); err != nil {
		t.Fatal(err)
	}
	got := recordOf(t, decide(t, directorySyncMessage{Users: []directoryUser{ada}}, directorySyncState{}, nil, nil), "oid-ada")
	if got.ManagerDirectoryID != "oid-boss" {
		t.Errorf("managerDirectoryId = %q, want the manager a plain object names", got.ManagerDirectoryID)
	}

	var gone directoryUser
	if err := json.Unmarshal([]byte(`{"id":"oid-ada","manager":null}`), &gone); err != nil {
		t.Fatal(err)
	}
	if oid, said := gone.reportedManager(); !said || oid != "" {
		t.Errorf("a null manager = (%q, %v), want it said and empty", oid, said)
	}
}

// lineManagerServer is a server with the shipped approvals and three people: Ada,
// who orders; her manager; and the manager's own manager.
func lineManagerServer(t *testing.T) *Server {
	t.Helper()
	srv := answersServer(t, WithSystemProcesses())
	srv.do(func() {
		for _, u := range []User{
			{ID: "usr_ada", Username: "ada", Roles: []string{RoleUser}, ManagerID: "usr_chefin"},
			{ID: "usr_chefin", Username: "chefin", Roles: []string{RoleUser}, ManagerID: "usr_direktor"},
			{ID: "usr_direktor", Username: "direktor", Roles: []string{RoleUser}},
		} {
			if err := srv.users.Save(u); err != nil {
				t.Fatalf("save %s: %v", u.Username, err)
			}
		}
	})
	return srv
}

func setUser(t *testing.T, srv *Server, id string, change func(*User)) {
	t.Helper()
	srv.do(func() {
		u, ok, err := srv.users.Get(id)
		if err != nil || !ok {
			t.Fatalf("get %s: %v %v", id, ok, err)
		}
		change(&u)
		if err := srv.users.Save(u); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	})
}

func TestASuperiorApprovalStartsWithTheOrderersLineManager(t *testing.T) {
	srv := lineManagerServer(t)
	orderCancelPathsSave(t, srv, parkingOrder("ord_lm1", order.Approval{Kind: "superior"}, "prov-park"))
	vars := rootVars(t, srv, startLine(t, srv, "ord_lm1"))
	if got := vars["approvalRef"].Text; got != "usr_chefin" {
		t.Errorf("approvalRef = %q, want the orderer's line manager", got)
	}
	// The event catalogue promises listeners this variable, so the model keeps it.
	if got := vars["vorgesetzter"].Text; got != "usr_chefin" {
		t.Errorf("vorgesetzter = %q, want the same person", got)
	}
}

func TestASuperiorApprovalWithNobodyToGoToIsRefusedAndSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(ada, chefin *User)
		want   string
	}{
		{"no manager on record", func(ada, _ *User) { ada.ManagerID = "" }, "no line manager on record"},
		{"a manager the directory names before their account", func(ada, _ *User) {
			ada.ManagerID, ada.ManagerDirectoryID = "", "oid-unbekannt"
		}, "oid-unbekannt"},
		{"a disabled manager", func(_, chefin *User) { chefin.Disabled = true }, "disabled"},
		{"a manager whose account is gone", func(ada, _ *User) { ada.ManagerID = "usr_weg" }, "usr_weg"},
		{"the orderer as their own manager", func(ada, _ *User) { ada.ManagerID = "usr_ada" }, "own line manager"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := lineManagerServer(t)
			srv.do(func() {
				ada, _, _ := srv.users.Get("usr_ada")
				chefin, _, _ := srv.users.Get("usr_chefin")
				c.change(&ada, &chefin)
				_ = srv.users.Save(ada)
				_ = srv.users.Save(chefin)
			})
			orderCancelPathsSave(t, srv, parkingOrder("ord_lm2", order.Approval{Kind: "superior"}, "prov-park"))
			code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/orders/ord_lm2/lines/park/start", "", "")
			if code != http.StatusConflict {
				t.Fatalf("start = %d %s, want a conflict", code, body)
			}
			for _, want := range []string{"usr_ada", c.want} {
				if !strings.Contains(string(body), want) {
					t.Errorf("the refusal %s does not say %q", body, want)
				}
			}
		})
	}
}

func TestAnApprovalRefusedForWantOfAManagerStartsOnceThereIsOne(t *testing.T) {
	srv := lineManagerServer(t)
	setUser(t, srv, "usr_ada", func(ada *User) { ada.ManagerID = "" })
	orderCancelPathsSave(t, srv, parkingOrder("ord_lm3", order.Approval{Kind: "superior"}, "prov-park"))
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/orders/ord_lm3/lines/park/start", "", ""); code != http.StatusConflict {
		t.Fatalf("start without a manager = %d, want a conflict", code)
	}
	// What resolving the fulfilment's incident does: ask again.
	setUser(t, srv, "usr_ada", func(ada *User) { ada.ManagerID = "usr_chefin" })
	if got := rootVars(t, srv, startLine(t, srv, "ord_lm3"))["approvalRef"].Text; got != "usr_chefin" {
		t.Errorf("approvalRef = %q after the manager was set", got)
	}
}

func TestOnlyASuperiorApprovalIsGivenALineManager(t *testing.T) {
	srv := lineManagerServer(t)
	orderCancelPathsSave(t, srv, parkingOrder("ord_lm4", order.Approval{Kind: "fixed", Ref: "anna"}, "prov-park"))
	if got := rootVars(t, srv, startLine(t, srv, "ord_lm4"))["approvalRef"].Text; got != "anna" {
		t.Errorf("approvalRef = %q, want the product's own approver untouched", got)
	}
}

func TestTheDirectoryOwnsTheManagerOfAnAccountItMirrors(t *testing.T) {
	srv := lineManagerServer(t)
	setUser(t, srv, "usr_ada", func(ada *User) { ada.ManagerDirectoryID = "oid-chefin" })
	code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/users/usr_ada",
		`{"managerId":"usr_direktor"}`, "application/json")
	if code != http.StatusConflict || !strings.Contains(string(body), "directory") {
		t.Errorf("= %d %s, want a conflict that names the directory", code, body)
	}
	// Saying again what the directory already says changes nothing, so it refuses nothing.
	if code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/users/usr_ada",
		`{"managerId":"usr_chefin"}`, "application/json"); code != http.StatusOK {
		t.Errorf("the unchanged manager = %d %s, want it accepted", code, body)
	}
	// Everything else about the account may still change.
	if code, body := serveInternal(t, srv, http.MethodPatch, "/api/v1/users/usr_ada",
		`{"displayName":"Ada L."}`, "application/json"); code != http.StatusOK {
		t.Errorf("rename = %d %s", code, body)
	}
}

func TestTheLineManagerIsFoundByUsernameAsWellAsById(t *testing.T) {
	srv := lineManagerServer(t)
	for _, who := range []string{"usr_ada", "ada", "ADA"} {
		var got, why string
		var err error
		srv.do(func() { got, why, err = srv.lineManagerOf(who) })
		if err != nil || why != "" || got != "usr_chefin" {
			t.Errorf("lineManagerOf(%q) = (%q, %q, %v), want usr_chefin", who, got, why, err)
		}
	}
	var why string
	srv.do(func() { _, why, _ = srv.lineManagerOf("niemand") })
	if !strings.Contains(why, "no account") {
		t.Errorf("an unknown person = %q, want it said there is no account", why)
	}
}
