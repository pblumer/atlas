package catalog

import (
	"errors"
	"net/http"
	"testing"
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
