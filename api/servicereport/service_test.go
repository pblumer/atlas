package servicereport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/shopcontract"
)

// The report service against fakes of the shop contract (ADR-0515): what each
// route answers, whom it refuses, and that a report with names is audited and one
// without is not. The HTTP tests in package api drive the same handlers against a
// real server; these pin the decisions without one.

type fakeInventory struct {
	holds   []shopcontract.Hold
	history map[string][]shopcontract.Hold
	err     error
}

func (f fakeInventory) Read(fn func(shopcontract.InventoryView) error) error {
	if f.err != nil {
		return f.err
	}
	return fn(f)
}

func (f fakeInventory) EachHold(fn func(shopcontract.Hold) error) error {
	for _, h := range f.holds {
		if err := fn(h); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeInventory) HistoryOf(p string, fn func(shopcontract.Hold) error) error {
	for _, h := range f.history[p] {
		if err := fn(h); err != nil {
			return err
		}
	}
	return nil
}

type fakeDirectory struct {
	accounts []shopcontract.Account
	err      error
}

func (f fakeDirectory) Accounts() ([]shopcontract.Account, error) { return f.accounts, f.err }

type fixture struct {
	s       *Service
	audited []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	monday := time.Date(2026, 10, 26, 9, 0, 0, 0, zurich).UnixNano()
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, zurich).UnixNano()
	cat := catalog.Catalog{ID: "cat", Languages: []string{"de"}, Texts: map[string]string{"de": "Arbeitsplatz"},
		Groups: []string{"grp_kunden"}, ReportingGroups: []string{"grp_it"}, OwnerID: "pm"}
	rel := catalog.Release{ID: "rel_1", CatalogID: "cat", Items: []catalog.Item{
		{ID: "vpn", Texts: map[string]string{"de": "VPN-Zugang"}},
		{ID: "laptop", Texts: map[string]string{"fr": "Portable"}},
	}}
	orders := []order.Order{
		{ID: "o1", ReleaseID: "rel_1", Recipient: "ada", Orderer: "ada", CreatedAt: monday, Lines: []order.Line{
			{ItemID: "vpn", Status: order.StatusDone, Price: "CHF 5.–",
				ServiceLevel: &catalog.ServiceLevel{Name: "Standard", DeliveryDays: 3}},
			{ItemID: "laptop", Status: order.StatusReturned},
		}},
		{ID: "o2", ReleaseID: "rel_other", Recipient: "bob", CreatedAt: monday},
	}
	f := &fixture{}
	f.s = &Service{
		Now: func() int64 { return now }, Location: zurich,
		Catalogs:   func() ([]catalog.Catalog, error) { return []catalog.Catalog{cat}, nil },
		ReleasesOf: func(string) ([]catalog.Release, error) { return []catalog.Release{rel}, nil },
		MayEdit:    func(c catalog.Catalog, p *httpapi.Principal) bool { return p != nil && p.UserID == c.OwnerID },
		Orders:     func() ([]order.Order, error) { return orders, nil },
		Inventory: fakeInventory{
			holds: []shopcontract.Hold{{Principal: "ada", ItemID: "vpn", OrderID: "o1", Origin: "ordered",
				Since: time.Date(2026, 10, 27, 9, 0, 0, 0, zurich).UnixNano()}},
			history: map[string][]shopcontract.Hold{"ada": {{Principal: "ada", ItemID: "laptop", OrderID: "o1",
				Since: time.Date(2026, 10, 28, 9, 0, 0, 0, zurich).UnixNano()}}},
		},
		Directory: fakeDirectory{accounts: []shopcontract.Account{
			{ID: "ada", Name: "Ada", ManagerID: "boss", Groups: []string{"grp_kunden"}},
			{ID: "boss", Name: "Boss", Groups: []string{"grp_kunden"}},
			{ID: "gone", Name: "Gone", ManagerID: "boss", Disabled: true, Groups: []string{"grp_kunden"}},
			{ID: "cr", Name: "CR", Groups: []string{"grp_it"}},
		}},
		NameOf: func() func(string) string { return func(id string) string { return strings.ToUpper(id) } },
		Audit: func(r *http.Request, attrs ...slog.Attr) {
			var parts []string
			for _, a := range attrs {
				parts = append(parts, a.Key+"="+a.Value.String())
			}
			f.audited = append(f.audited, strings.Join(parts, " "))
		},
	}
	return f
}

func get(t *testing.T, h http.HandlerFunc, who, target string, groups ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	if who != "" {
		req = req.WithContext(httpapi.WithPrincipal(context.Background(),
			&httpapi.Principal{UserID: who, Username: who, GroupIDs: groups}))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestTheDeliveryRouteCountsAndAuditsNothing(t *testing.T) {
	f := newFixture(t)
	rec := get(t, f.s.HandleDeliveryReport, "pm", "/x?from=2026-10-01&to=2026-10-31")
	if rec.Code != 200 {
		t.Fatalf("delivery = %d %s", rec.Code, rec.Body)
	}
	var rep struct {
		Catalogs []string     `json:"catalogs"`
		Products []ProductRow `json:"products"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if len(rep.Products) != 2 || rep.Products[0].ItemID != "laptop" || rep.Products[0].Name != "Portable" ||
		rep.Products[1].Met != 1 || rep.Products[1].Held != 1 || rep.Products[0].Delivered != 1 {
		t.Errorf("products %+v", rep.Products)
	}
	if strings.Contains(rec.Body.String(), "ADA") {
		t.Error("the product manager's report names a person")
	}
	if rec := get(t, f.s.HandleDeliveryReport, "pm", "/x?format=csv"); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "item;name;") || rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Errorf("csv = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, f.s.HandleDeliveryReport, "stranger", "/x?catalog=cat"); rec.Code != 404 {
		t.Errorf("a stranger narrowing to the catalogue = %d", rec.Code)
	}
	rec = get(t, f.s.HandleDeliveryReport, "stranger", "/x")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "you maintain no catalogue") {
		t.Errorf("a stranger's report = %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/x?from=x", "/x?to=2026-02-30", "/x?from=2026-02-01&to=2026-01-01"} {
		if rec := get(t, f.s.HandleDeliveryReport, "pm", bad); rec.Code != 400 {
			t.Errorf("%s = %d", bad, rec.Code)
		}
	}
	if len(f.audited) != 0 {
		t.Errorf("a report without names was audited: %v", f.audited)
	}
}

func TestTheReportsWithNamesReachTheirReaders(t *testing.T) {
	f := newFixture(t)
	mine := func(who string, groups ...string) string {
		return get(t, f.s.HandleMyReports, who, "/x", groups...).Body.String()
	}
	if got := mine("boss"); !strings.Contains(got, `"team":1`) || !strings.Contains(got, `"organisations":[]`) {
		t.Errorf("boss = %s (a disabled report does not count)", got)
	}
	if got := mine("cr", "grp_it"); !strings.Contains(got, `"team":0`) || !strings.Contains(got, `"name":"Arbeitsplatz"`) {
		t.Errorf("cr = %s", got)
	}
	if got := get(t, f.s.HandleMyReports, "", "/x").Body.String(); !strings.Contains(got, `"team":0`) {
		t.Errorf("nobody = %s", got)
	}

	rec := get(t, f.s.HandleTeamReport, "boss", "/x?from=2026-10-01")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"recipientName":"ADA"`) ||
		!strings.Contains(rec.Body.String(), `"price":"CHF 5.–"`) {
		t.Fatalf("team = %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x?from=2026-10-01&format=csv&kind=positions"); !strings.Contains(rec.Body.String(), "o1;2026-10-26;ADA;ADA;vpn") {
		t.Errorf("positions csv = %s", rec.Body)
	}
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x?from=2026-10-01&format=csv"); !strings.Contains(rec.Body.String(), "ADA;vpn;VPN-Zugang;;ordered;CHF 5.–") {
		t.Errorf("holdings csv = %s", rec.Body)
	}
	if rec := get(t, f.s.HandleTeamReport, "ada", "/x"); rec.Code != 404 {
		t.Errorf("somebody nobody reports to = %d", rec.Code)
	}
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x?to=nie"); rec.Code != 400 {
		t.Errorf("bad period = %d", rec.Code)
	}

	rec = get(t, f.s.HandleOrganisationReport, "cr", "/x?catalog=cat&from=2026-10-01", "grp_it")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"catalogName":"Arbeitsplatz"`) ||
		!strings.Contains(rec.Body.String(), `"name":"ADA"`) || !strings.Contains(rec.Body.String(), `"name":"BOSS"`) {
		t.Fatalf("organisation = %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct{ who, q string }{{"boss", "cat"}, {"cr", "nope"}, {"", "cat"}} {
		if rec := get(t, f.s.HandleOrganisationReport, c.who, "/x?catalog="+c.q, "grp_kunden"); rec.Code != 404 {
			t.Errorf("%s on %s = %d", c.who, c.q, rec.Code)
		}
	}
	if len(f.audited) != 4 || !strings.HasPrefix(f.audited[0], "view=team people=1") ||
		!strings.Contains(f.audited[3], "catalog=cat") || !strings.Contains(f.audited[1], "format=csv") {
		t.Errorf("audited %q", f.audited)
	}
}

func TestTheReportsSayWhenAStoreFails(t *testing.T) {
	boom := errors.New("disk")
	f := newFixture(t)
	f.s.Inventory = fakeInventory{err: boom}
	if rec := get(t, f.s.HandleDeliveryReport, "pm", "/x"); rec.Code != 500 {
		t.Errorf("inventory failing = %d", rec.Code)
	}
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x"); rec.Code != 500 {
		t.Errorf("team, inventory failing = %d", rec.Code)
	}
	f = newFixture(t)
	f.s.Directory = fakeDirectory{err: boom}
	for name, h := range map[string]http.HandlerFunc{"mine": f.s.HandleMyReports, "team": f.s.HandleTeamReport} {
		if rec := get(t, h, "boss", "/x"); rec.Code != 500 {
			t.Errorf("%s, directory failing = %d", name, rec.Code)
		}
	}
	if rec := get(t, f.s.HandleOrganisationReport, "cr", "/x?catalog=cat", "grp_it"); rec.Code != 500 {
		t.Errorf("organisation, directory failing = %d", rec.Code)
	}
	f = newFixture(t)
	f.s.Catalogs = func() ([]catalog.Catalog, error) { return nil, boom }
	for name, h := range map[string]http.HandlerFunc{"delivery": f.s.HandleDeliveryReport, "mine": f.s.HandleMyReports,
		"team": f.s.HandleTeamReport, "org": f.s.HandleOrganisationReport} {
		if rec := get(t, h, "boss", "/x?catalog=cat"); rec.Code != 500 {
			t.Errorf("%s, catalogues failing = %d", name, rec.Code)
		}
	}
	f = newFixture(t)
	f.s.ReleasesOf = func(string) ([]catalog.Release, error) { return nil, boom }
	if rec := get(t, f.s.HandleDeliveryReport, "pm", "/x"); rec.Code != 500 {
		t.Errorf("releases failing = %d", rec.Code)
	}
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x"); rec.Code != 500 {
		t.Errorf("team, releases failing = %d", rec.Code)
	}
	f = newFixture(t)
	f.s.Orders = func() ([]order.Order, error) { return nil, boom }
	if rec := get(t, f.s.HandleDeliveryReport, "pm", "/x"); rec.Code != 500 {
		t.Errorf("orders failing = %d", rec.Code)
	}
	// With no directory there is nobody to report on, which is a 404, not a failure.
	f = newFixture(t)
	f.s.Directory = nil
	if rec := get(t, f.s.HandleTeamReport, "boss", "/x"); rec.Code != 404 {
		t.Errorf("no directory = %d", rec.Code)
	}
	if (&Service{}).loc() != time.Local {
		t.Error("no location is the local zone")
	}
}
