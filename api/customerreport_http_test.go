package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The two reports that name people, over HTTP
// (ADR-0515 §3).
//
// A line manager reads their direct reports; a customer responsible reads their
// catalogue's audience; anybody else reads neither, and is not told whether the
// catalogue they asked about exists. Every answer with names leaves an audit line.

type peopleReportResp struct {
	Scope   string `json:"scope"`
	Catalog string `json:"catalog"`
	People  []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Ordered  int    `json:"ordered"`
		Holdings []struct {
			ItemID string `json:"itemId"`
			Name   string `json:"name"`
			Origin string `json:"origin"`
		} `json:"holdings"`
	} `json:"people"`
	Positions []struct {
		ItemID        string `json:"itemId"`
		RecipientName string `json:"recipientName"`
		Verdict       string `json:"verdict"`
	} `json:"positions"`
	Products []struct {
		ItemID string `json:"itemId"`
		Held   int    `json:"held"`
		Met    int    `json:"met"`
	} `json:"products"`
}

func TestTheReportsThatNamePeopleReachOnlyWhoTheyAreFor(t *testing.T) {
	sink := captureLog(t)
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	as := func(c *http.Client) func(method, path, body string) (int, []byte) {
		return func(method, path, body string) (int, []byte) { return cReq(t, c, ts, method, path, body) }
	}
	root := as(admin)

	ids := map[string]string{}
	clients := map[string]*http.Client{}
	for _, u := range []string{"ada", "boss", "cr", "other"} {
		ids[u] = createUserWithRoles(t, admin, ts.URL, u, `["user"]`)
		clients[u] = newClient(t)
		if login(t, clients[u], ts, u, "a-password-that-is-long") != http.StatusOK {
			t.Fatalf("%s login failed", u)
		}
	}
	group := func(name string, members ...string) string {
		code, b := root("POST", "/api/v1/groups", `{"name":"`+name+`"}`)
		if code != http.StatusCreated {
			t.Fatalf("create group: %d (%s)", code, b)
		}
		var g struct{ ID string }
		_ = json.Unmarshal(b, &g)
		for _, m := range members {
			if code, b := root("PUT", "/api/v1/groups/"+g.ID+"/members/"+ids[m], ""); code != http.StatusNoContent && code != http.StatusOK {
				t.Fatalf("add %s: %d (%s)", m, code, b)
			}
		}
		return g.ID
	}
	customers := group("kunden", "ada", "boss")
	responsible := group("it-verantwortung", "cr")
	if code, b := root("PATCH", "/api/v1/users/"+ids["ada"], `{"managerId":"`+ids["boss"]+`"}`); code != http.StatusOK {
		t.Fatalf("set manager: %d (%s)", code, b)
	}

	code, body := root("POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"},"groups":["`+customers+`"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct{ ID string }
	_ = json.Unmarshal(body, &cat)
	product := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active","texts":{"de":"VPN-Zugang"},` +
		`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov",` +
		`"serviceLevel":{"name":"Standard","deliveryDays":3}}`
	if code, b := root("POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := root("PATCH", "/api/v1/catalogs/"+cat.ID, `{"items":["vpn"],"reportingGroups":["`+responsible+`"]}`); code != http.StatusOK {
		t.Fatalf("offer product: %d (%s)", code, b)
	}
	code, body = root("POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct{ ID string }
	_ = json.Unmarshal(body, &rel)
	code, body = as(clients["ada"])("POST", "/api/v1/orders", `{"releaseId":"`+rel.ID+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("ada orders: %d (%s)", code, body)
	}
	var ord struct{ ID string }
	_ = json.Unmarshal(body, &ord)
	if code, b := root("POST", fmt.Sprintf("/api/v1/orders/%s/lines/vpn", ord.ID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}

	mine := func(u string) (int, []string) {
		code, b := as(clients[u])("GET", "/api/v1/shop/reports", "")
		if code != http.StatusOK {
			t.Fatalf("%s reports = %d (%s)", u, code, b)
		}
		var r struct {
			Team          int `json:"team"`
			Organisations []struct {
				Catalog string `json:"catalog"`
			} `json:"organisations"`
		}
		_ = json.Unmarshal(b, &r)
		var orgs []string
		for _, o := range r.Organisations {
			orgs = append(orgs, o.Catalog)
		}
		return r.Team, orgs
	}
	if team, orgs := mine("boss"); team != 1 || len(orgs) != 0 {
		t.Errorf("boss may open team %d, organisations %v; want 1 and none", team, orgs)
	}
	if team, orgs := mine("cr"); team != 0 || len(orgs) != 1 || orgs[0] != cat.ID {
		t.Errorf("cr may open team %d, organisations %v; want none and the catalogue", team, orgs)
	}
	if team, orgs := mine("other"); team != 0 || len(orgs) != 0 {
		t.Errorf("other may open team %d, organisations %v; want nothing", team, orgs)
	}

	read := func(u, path string) peopleReportResp {
		t.Helper()
		code, b := as(clients[u])("GET", path, "")
		if code != http.StatusOK {
			t.Fatalf("%s %s = %d (%s)", u, path, code, b)
		}
		var r peopleReportResp
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("decode: %v (%s)", err, b)
		}
		return r
	}

	team := read("boss", "/api/v1/shop/team-report")
	if team.Scope != "team" || len(team.People) != 1 || team.People[0].Name != "ada" ||
		len(team.People[0].Holdings) != 1 || team.People[0].Holdings[0].Name != "VPN-Zugang" ||
		team.People[0].Holdings[0].Origin != "ordered" || team.People[0].Ordered != 1 {
		t.Errorf("team report: %+v", team)
	}
	if len(team.Positions) != 1 || team.Positions[0].RecipientName != "ada" || team.Positions[0].Verdict != "met" {
		t.Errorf("team positions: %+v", team.Positions)
	}

	org := read("cr", "/api/v1/shop/organisation-report?catalog="+cat.ID)
	if org.Scope != "organisation" || org.Catalog != cat.ID || len(org.People) != 2 ||
		org.People[0].Name != "ada" || org.People[1].Name != "boss" || len(org.People[1].Holdings) != 0 {
		t.Errorf("organisation report: %+v", org)
	}
	if len(org.Products) != 1 || org.Products[0].Held != 1 || org.Products[0].Met != 1 {
		t.Errorf("organisation products: %+v", org.Products)
	}

	code, b := as(clients["cr"])("GET", "/api/v1/shop/organisation-report?catalog="+cat.ID+"&format=csv&kind=positions", "")
	if code != http.StatusOK || !strings.Contains(string(b), ";ada;ada;vpn;VPN-Zugang;") {
		t.Errorf("positions csv = %d:\n%s", code, b)
	}
	code, b = as(clients["boss"])("GET", "/api/v1/shop/team-report?format=csv", "")
	if code != http.StatusOK || !strings.Contains(string(b), "ada;vpn;VPN-Zugang;;ordered;") {
		t.Errorf("holdings csv = %d:\n%s", code, b)
	}

	// Refused: nobody names other as manager, other is in no reporting group, and
	// boss — a member of the audience — is not a customer responsible.
	for _, c := range []struct{ who, path string }{
		{"other", "/api/v1/shop/team-report"},
		{"cr", "/api/v1/shop/team-report"},
		{"other", "/api/v1/shop/organisation-report?catalog=" + cat.ID},
		{"boss", "/api/v1/shop/organisation-report?catalog=" + cat.ID},
		{"cr", "/api/v1/shop/organisation-report?catalog=gibt-es-nicht"},
	} {
		if code, b := as(clients[c.who])("GET", c.path, ""); code != http.StatusNotFound {
			t.Errorf("%s %s = %d (%s), want 404", c.who, c.path, code, b)
		}
	}
	if code, _ := as(clients["boss"])("GET", "/api/v1/shop/team-report?from=morgen", ""); code != http.StatusBadRequest {
		t.Errorf("a bad period = %d, want 400", code)
	}

	// Four answers with names were sent; four audit lines say who read which.
	lines := auditLines(t, sink, "audit.report_names_read")
	if len(lines) != 4 {
		t.Fatalf("audit lines = %d, want 4\n%s", len(lines), sink.String())
	}
	if lines[0]["actor"] != "boss" || lines[0]["view"] != "team" || lines[0]["people"] != float64(1) {
		t.Errorf("team audit line: %v", lines[0])
	}
	if lines[1]["actor"] != "cr" || lines[1]["view"] != "organisation" || lines[1]["catalog"] != cat.ID {
		t.Errorf("organisation audit line: %v", lines[1])
	}
	if lines[2]["format"] != "csv" {
		t.Errorf("csv audit line: %v", lines[2])
	}
}
