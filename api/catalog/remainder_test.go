package catalog

import (
	"errors"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

type fakeRemainders struct {
	got []string
	err error
}

func (f *fakeRemainders) Remainder(items []Item) ([]Remainder, error) {
	for _, it := range items {
		f.got = append(f.got, it.ID)
	}
	if f.err != nil {
		return nil, f.err
	}
	return []Remainder{{ItemID: "laptop", Process: "old-deprov", Lines: 3}}, nil
}

func lifecycleBody(id, home string) string {
	return `{"id":"` + id + `","homeCatalog":"` + home + `","state":"active",` +
		`"texts":{"de":"X"},"approval":{"kind":"none"},"lifecycleProcess":"lc",` +
		`"operations":{"provision":"p","deprovision":"d"}}`
}

// TestTheFulfilmentReportCarriesTheRemainder: the report asks for the remainder of
// the converted products only, and passes the count through (ADR-0427); a failing
// lookup is an error, not an empty remainder.
func TestTheFulfilmentReportCarriesTheRemainder(t *testing.T) {
	s := serviceWithAdmin(t)
	s.Processes = healthy()
	rem := &fakeRemainders{}
	s.Remainders = rem
	cat := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", lifecycleBody("laptop", cat.ID))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", boundBody("vpn", cat.ID, "proc_vpn"))
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"items":["laptop","vpn"]}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := as(t, s.HandlePublish, user("usr_a"), "POST", "", "id", cat.ID); rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", rec.Code, rec.Body.String())
	}

	got := decode[FulfilmentReport](t, as(t, s.HandleFulfilmentReport, user("usr_a"), "GET", ""))
	if len(got.Remainder) != 1 || got.Remainder[0].Lines != 3 {
		t.Fatalf("remainder = %+v, want the lookup's one entry", got.Remainder)
	}
	if len(rem.got) != 1 || rem.got[0] != "laptop" {
		t.Errorf("asked about %v, want only the converted product", rem.got)
	}

	rem.err = errors.New("orders unreadable")
	if rec := as(t, s.HandleFulfilmentReport, user("usr_a"), "GET", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failing lookup: %d, want 500", rec.Code)
	}
}

// TestMayEditIsTheWriteRight: the exported check answers as the write guard does —
// the owner and an editor may, a viewer and an anonymous caller may not.
func TestMayEditIsTheWriteRight(t *testing.T) {
	s := newService(t)
	c := Catalog{ID: "c", OwnerID: "usr_owner", Members: []Member{
		{Ref: PrincipalRef{Type: "user", ID: "usr_ed"}, Role: RoleEditor},
		{Ref: PrincipalRef{Type: "user", ID: "usr_view"}, Role: RoleViewer},
	}}
	for _, tc := range []struct {
		p    *httpapi.Principal
		want bool
	}{
		{&httpapi.Principal{UserID: "usr_owner"}, true},
		{&httpapi.Principal{UserID: "usr_ed"}, true},
		{&httpapi.Principal{UserID: "usr_view"}, false},
		{&httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}}, true},
		{nil, false},
	} {
		if got := s.MayEdit(c, tc.p); got != tc.want {
			t.Errorf("MayEdit(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}
