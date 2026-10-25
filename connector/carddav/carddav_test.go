package carddav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/carddav/catalog"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/limits"
)

func card(uid, fn, n, extra string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nFN:" + fn + "\r\nN:" + n + "\r\n" + extra + "END:VCARD\r\n"
}

var (
	annaCard = card("anna-uid", "Anna Muster", "Muster;Anna;;;",
		"EMAIL;TYPE=WORK:Anna@Firma.ch\r\nTEL;TYPE=CELL:+41 79 123 45 67\r\nX-Kundennummer:4711\r\nCATEGORIES:Kunden\r\n"+
			"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD\r\nREV:20200101T000000Z\r\n")
	bertCard = card("bert-uid", "Bert Beispiel", "Beispiel;Bert;;;",
		"EMAIL:bert@beispiel.ch\r\nTEL:044 000 00 00\r\nORG:Beispiel GmbH\r\nCATEGORIES:Lieferanten\r\n")
	carlCard = card("urn:uuid:carl-uid", "Carl Arnold", "Arnold;Carl;;;", "EMAIL:carl@arnold.ch\r\n")
	// Apple's group card: vCard 3.0 with X-ADDRESSBOOKSERVER-KIND and its members by
	// urn:uuid:.
	kundenGroup = "BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//Apple Inc.//AddressBook 6.1//EN\r\nN:Kunden;;;;\r\nFN:Kunden\r\n" +
		"X-ADDRESSBOOKSERVER-KIND:group\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:anna-uid\r\nUID:grp-kunden\r\nEND:VCARD\r\n"
	vipGroup = "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:VIP\r\nKIND:group\r\nUID:grp-vip\r\nEND:VCARD\r\n"
)

// seeded is a fake holding Anna, Bert and Carl, and the two group cards.
func seeded(t *testing.T) *fakeDAV {
	f := newFakeDAV(t)
	f.put("anna.vcf", annaCard)
	f.put("bert.vcf", bertCard)
	f.put("carl.vcf", carlCard)
	f.put("grp-kunden.vcf", kundenGroup)
	f.put("grp-vip.vcf", vipGroup)
	return f
}

func registry(t *testing.T, f *fakeDAV, endpoint, groups string) *Registry {
	t.Helper()
	if endpoint == "" {
		endpoint = f.srv.URL + "/dav/addressbooks/anna/kontakte/"
	}
	c, err := NewClient(endpoint, f.bundle(groups), Options{Now: func() time.Time { return time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("verkauf", c)
	return reg
}

func run1(t *testing.T, reg *Registry, j Job) map[string]any {
	t.Helper()
	if j.Connector == "" {
		j.Connector = "verkauf"
	}
	if j.ResultVariable == "" {
		if op, _ := catalog.Lookup(j.Operation); op.TakesResult() {
			j.ResultVariable = "r"
		}
	}
	out, err := Run(context.Background(), j, reg)
	if err != nil {
		t.Fatalf("%s: %v", j.Operation, err)
	}
	return out
}

func list1(t *testing.T, out map[string]any) []any {
	t.Helper()
	l, ok := out["r"].([]any)
	if !ok {
		t.Fatalf("result = %#v", out)
	}
	return l
}

func ids(l []any) string {
	var out []string
	for _, c := range l {
		out = append(out, c.(map[string]any)["id"].(string))
	}
	return strings.Join(out, ",")
}

// The endpoint may be the server's root: the address book is found through
// /.well-known/carddav, the principal and the home, the calendar beside it is no
// address book, and the answer is kept for the next job.
func TestDiscoveryFromTheServersRoot(t *testing.T) {
	f := seeded(t)
	f.plainRoot = true
	reg := registry(t, f, f.srv.URL, "")
	out := run1(t, reg, Job{Operation: "get-contact", ContactID: "anna.vcf"})
	if out["r"].(map[string]any)["email"] != "Anna@Firma.ch" {
		t.Errorf("out = %v", out)
	}
	propfinds := f.count("PROPFIND")
	run1(t, reg, Job{Operation: "get-contact", ContactID: "bert.vcf"})
	if f.count("PROPFIND") != propfinds {
		t.Error("the address book was discovered again for the second job")
	}
	if !strings.Contains(strings.Join(f.requests, "\n"), "PROPFIND /.well-known/carddav") {
		t.Errorf("the well-known URL was not asked: %v", f.requests)
	}
}

// The endpoint may be the principal or the address book itself.
func TestDiscoveryFromThePrincipalAndFromTheBook(t *testing.T) {
	for _, path := range []string{"/dav/principals/anna/", "/dav/addressbooks/anna/kontakte"} {
		f := seeded(t)
		reg := registry(t, f, f.srv.URL+path, "")
		if out := run1(t, reg, Job{Operation: "get-contact", ContactID: "anna.vcf"}); out["r"] == nil {
			t.Errorf("%s: %v", path, out)
		}
	}
}

// iCloud names a home on another host than the one the account signs in at; the
// credential goes there too, because the server it was configured for said so.
func TestTheHomeOnAnotherHost(t *testing.T) {
	books := seeded(t)
	front := newFakeDAV(t)
	front.homeURL = books.srv.URL + books.home
	reg := registry(t, front, front.srv.URL+"/dav/", "")
	out := run1(t, reg, Job{Operation: "list-contacts", MaxResults: 10})
	if ids(list1(t, out)) != "carl.vcf,bert.vcf,anna.vcf" {
		t.Errorf("listing = %s", ids(list1(t, out)))
	}
}

func TestDiscoveryRefusals(t *testing.T) {
	t.Run("two address books are named, not guessed between", func(t *testing.T) {
		f := seeded(t)
		f.books["/dav/addressbooks/anna/privat/"] = &fakeBook{name: "Privat", cards: map[string]*fakeCard{}}
		_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, registry(t, f, f.srv.URL+"/dav/", ""))
		if err == nil || !strings.Contains(err.Error(), `2 address books ("Kontakte" at /dav/addressbooks/anna/kontakte/, "Privat" at /dav/addressbooks/anna/privat/)`) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("Nextcloud's generated address books are not a choice", func(t *testing.T) {
		f := seeded(t)
		f.books["/dav/addressbooks/anna/z-app-generated--contactsinteraction--recent/"] = &fakeBook{name: "Recently contacted", cards: map[string]*fakeCard{}}
		run1(t, registry(t, f, f.srv.URL+"/dav/", ""), Job{Operation: "list-contacts", MaxResults: 10})
	})
	t.Run("no address book", func(t *testing.T) {
		f := newFakeDAV(t)
		delete(f.books, "/dav/addressbooks/anna/kontakte/")
		_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, registry(t, f, f.srv.URL+"/dav/", ""))
		if err == nil || !strings.Contains(err.Error(), "no address book") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a wrong password", func(t *testing.T) {
		f := seeded(t)
		reg := registry(t, f, "", "")
		f.pass = "anders"
		_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, reg)
		if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "app-specific password") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a root that knows nothing and no well-known", func(t *testing.T) {
		f := seeded(t)
		f.plainRoot = true
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
		defer srv.Close()
		_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, registry(t, f, srv.URL, ""))
		if err == nil || !strings.Contains(err.Error(), "endpoint") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an endpoint that is not found is not second-guessed", func(t *testing.T) {
		f := seeded(t)
		_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, registry(t, f, f.srv.URL+"/falsch/", ""))
		if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("err = %v", err)
		}
	})
}

// A redirect from https to http would send the credential in the clear, and is refused.
func TestNoRedirectToPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the credential reached a plain-http server")
	}))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/dav/", http.StatusTemporaryRedirect)
	}))
	defer tls.Close()
	c, err := NewClient(tls.URL+"/dav/", `{"username":"a","password":"b"}`, Options{HTTPClient: tls.Client()})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("verkauf", c)
	_, err = Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, reg)
	if err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Errorf("err = %v", err)
	}
	// The client handed in is copied, not changed.
	if tls.Client().CheckRedirect != nil {
		t.Error("the caller's HTTP client was changed")
	}
}

func TestRedirectLoop(t *testing.T) {
	srv := httptest.NewServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dav/", http.StatusPermanentRedirect)
	})
	defer srv.Close()
	reg := NewRegistry()
	c, _ := NewClient(srv.URL+"/dav/", `{"username":"a","password":"b"}`, Options{})
	reg.Register("verkauf", c)
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts"}, reg)
	if err == nil || !strings.Contains(err.Error(), "redirected more than 5 times") {
		t.Errorf("err = %v", err)
	}
}

func TestGetContact(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	k := run1(t, reg, Job{Operation: "get-contact", ContactID: " anna.vcf "})["r"].(map[string]any)
	if k["id"] != "anna.vcf" || k["familyName"] != "Muster" || k["phone"] != "+41 79 123 45 67" || jsonOf(k["groups"]) != `["Kunden"]` || jsonOf(k["fields"]) != `{"Kundennummer":"4711"}` {
		t.Errorf("contact = %v", k)
	}
	// In the group-vcards form a contact's groups are the group cards naming it.
	k = run1(t, registry(t, f, "", catalog.GroupsVCards), Job{Operation: "get-contact", ContactID: "anna.vcf"})["r"].(map[string]any)
	if jsonOf(k["groups"]) != `["Kunden"]` {
		t.Errorf("groups = %v", k["groups"])
	}
}

// A contact that is gone ends the operation with the code the process catches — as a
// BPMN error when it does, as a failure otherwise.
func TestAMissingContact(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	for _, op := range []string{"get-contact", "update-contact", "add-to-group", "remove-from-group"} {
		j := Job{Connector: "verkauf", Operation: op, ContactID: "weg.vcf", Group: "Kunden", Contact: map[string]any{"notes": "x"}, ResultVariable: "r"}
		_, err := Run(context.Background(), j, reg)
		var nf *notFound
		if !errors.As(err, &nf) {
			t.Errorf("%s: err = %v", op, err)
		}
		j.ErrorCodes = []string{CodeContactNotFound}
		_, err = Run(context.Background(), j, reg)
		var be *job.BPMNError
		if !errors.As(err, &be) || be.Code != CodeContactNotFound {
			t.Errorf("%s caught: err = %v", op, err)
		}
	}
	// A delete of a contact already gone did what it was asked.
	run1(t, reg, Job{Operation: "delete-contact", ContactID: "weg.vcf"})
	// In the group-vcards form the group change looks the contact up first.
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "add-to-group", ContactID: "weg.vcf", Group: "Kunden"}, registry(t, f, "", catalog.GroupsVCards))
	var nf *notFound
	if !errors.As(err, &nf) {
		t.Errorf("group-vcards add: err = %v", err)
	}
}

func TestFindContacts(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	for _, c := range []struct {
		name string
		j    Job
		want string
	}{
		{"an e-mail address ignoring case", Job{Query: "anna@firma.CH"}, "anna.vcf"},
		{"a phone number by its digits", Job{Query: "+41791234567"}, "anna.vcf"},
		{"another number format", Job{Query: "044-000-00-00"}, "bert.vcf"},
		{"a custom field", Job{Query: " 4711 ", Field: "kundennummer"}, "anna.vcf"},
		{"a field nobody has", Job{Query: "4711", Field: "Segment"}, ""},
		{"a short number matches no phone", Job{Query: "4711"}, ""},
		{"within a group", Job{Query: "bert@beispiel.ch", Group: "kunden"}, ""},
		{"within the other group", Job{Query: "bert@beispiel.ch", Group: "Lieferanten"}, "bert.vcf"},
		// A group card is not a contact, whatever it is asked for.
		{"a group card is never found", Job{Query: "Kunden"}, ""},
	} {
		c.j.Operation, c.j.MaxResults = "find-contacts", 100
		if got := ids(list1(t, run1(t, reg, c.j))); got != c.want {
			t.Errorf("%s: found %q, want %q", c.name, got, c.want)
		}
	}
	// In the group-vcards form a group is a card, and a find within it reads its members.
	gv := registry(t, f, "", catalog.GroupsVCards)
	if got := ids(list1(t, run1(t, gv, Job{Operation: "find-contacts", Query: "anna@firma.ch", Group: "Kunden", MaxResults: 10}))); got != "anna.vcf" {
		t.Errorf("group-vcards find = %q", got)
	}
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "find-contacts", Query: "a@b.ch", Group: "Kundne", ResultVariable: "r"}, gv)
	if err == nil || !strings.Contains(err.Error(), `no group "Kundne" (its groups: Kunden, VIP)`) {
		t.Errorf("err = %v", err)
	}
	// Above the bound a find fails: matching that many is not finding one.
	f.put("anna2.vcf", card("anna2", "Anna Zwei", "Zwei;Anna;;;", "EMAIL:anna@firma.ch\r\n"))
	_, err = Run(context.Background(), Job{Connector: "verkauf", Operation: "find-contacts", Query: "anna@firma.ch", MaxResults: 1, ResultVariable: "r"}, reg)
	if err == nil || !strings.Contains(err.Error(), "matched 2 contacts") {
		t.Errorf("err = %v", err)
	}
}

func TestSearchAndListContacts(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	if got := ids(list1(t, run1(t, reg, Job{Operation: "search-contacts", Query: "beispiel", MaxResults: 10}))); got != "bert.vcf" {
		t.Errorf("search = %q", got)
	}
	if got := ids(list1(t, run1(t, reg, Job{Operation: "search-contacts", Query: "9 123", MaxResults: 10}))); got != "" {
		t.Errorf("a query of four digits matched a phone number: %q", got)
	}
	if got := ids(list1(t, run1(t, reg, Job{Operation: "search-contacts", Query: "7912345", MaxResults: 10}))); got != "anna.vcf" {
		t.Errorf("search by digits = %q", got)
	}
	// The search answers with its first matches, in the listing's order.
	if got := ids(list1(t, run1(t, reg, Job{Operation: "search-contacts", Query: "r", MaxResults: 2}))); got != "carl.vcf,bert.vcf" {
		t.Errorf("truncated search = %q", got)
	}
	if got := ids(list1(t, run1(t, reg, Job{Operation: "list-contacts", MaxResults: 10}))); got != "carl.vcf,bert.vcf,anna.vcf" {
		t.Errorf("listing = %q", got)
	}
	if got := ids(list1(t, run1(t, reg, Job{Operation: "list-contacts", Group: "Kunden", MaxResults: 10}))); got != "anna.vcf" {
		t.Errorf("group listing = %q", got)
	}
	// A group in the categories form exists by being on a contact: a name nobody uses
	// lists nobody.
	if got := ids(list1(t, run1(t, reg, Job{Operation: "list-contacts", Group: "Niemand", MaxResults: 10}))); got != "" {
		t.Errorf("group listing = %q", got)
	}
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts", MaxResults: 2, ResultVariable: "r"}, reg)
	if err == nil || !strings.Contains(err.Error(), "holds 3 contacts, more than the maxResults cap of 2") {
		t.Errorf("err = %v", err)
	}
	// A job without a bound gets the default rather than an unbounded read.
	if l := list1(t, run1(t, reg, Job{Operation: "list-contacts"})); len(l) != 3 {
		t.Errorf("listing = %d", len(l))
	}
	gv := registry(t, f, "", catalog.GroupsVCards)
	l := list1(t, run1(t, gv, Job{Operation: "list-contacts", MaxResults: 10}))
	if jsonOf(l[2].(map[string]any)["groups"]) != `["Kunden"]` || jsonOf(l[1].(map[string]any)["groups"]) != `[]` {
		t.Errorf("groups in the listing = %v", l)
	}
}

// TestCreateContactIsSafeToRepeat: the create key names the contact, so a second
// delivery of the same job finds the contact the first made instead of making another.
func TestCreateContactIsSafeToRepeat(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	j := Job{Operation: "create-contact", CreateKey: CreateKey("node", 7),
		Contact: map[string]any{"givenName": "Dora", "familyName": "Neu", "email": "dora@neu.ch", "groups": []any{"Kunden", "kunden", "VIP"}}}
	first := run1(t, reg, j)["r"].(map[string]any)
	uid := CreateUID(j.CreateKey)
	if first["id"] != uid+".vcf" || first["displayName"] != "Dora Neu" || jsonOf(first["groups"]) != `["Kunden","VIP"]` {
		t.Errorf("created = %v", first)
	}
	stored := f.card(uid + ".vcf")
	for _, want := range []string{"UID:" + uid, "CATEGORIES:Kunden,VIP", "EMAIL:dora@neu.ch", "N:Neu;Dora;;;", "VERSION:3.0"} {
		if !strings.Contains(stored, want) {
			t.Errorf("the stored card lacks %q:\n%s", want, stored)
		}
	}
	puts := f.count("PUT")
	second := run1(t, reg, j)["r"].(map[string]any)
	if second["id"] != first["id"] || len(f.book().cards) != 6 || f.count("PUT") != puts+1 {
		t.Errorf("a repeated create made a second contact: %v (%d cards)", second, len(f.book().cards))
	}
	// Another job is another contact.
	run1(t, reg, Job{Operation: "create-contact", CreateKey: CreateKey("node", 8), Contact: map[string]any{"organization": "Firma AG"}})
	if len(f.book().cards) != 7 {
		t.Errorf("%d cards", len(f.book().cards))
	}
}

func TestCreateContactInTheGroupVCardsForm(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", catalog.GroupsVCards)
	j := Job{Operation: "create-contact", CreateKey: CreateKey("node", 9),
		Contact: map[string]any{"familyName": "Neu", "groups": []any{"kunden", "VIP"}}}
	created := run1(t, reg, j)["r"].(map[string]any)
	uid := CreateUID(j.CreateKey)
	if jsonOf(created["groups"]) != `["Kunden","VIP"]` {
		t.Errorf("groups = %v", created["groups"])
	}
	if !strings.Contains(f.card("grp-kunden.vcf"), "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:"+uid) {
		t.Errorf("Apple's group card does not name the contact:\n%s", f.card("grp-kunden.vcf"))
	}
	if !strings.Contains(f.card("grp-vip.vcf"), "MEMBER:urn:uuid:"+uid) || strings.Contains(f.card("grp-vip.vcf"), "X-ADDRESSBOOKSERVER-MEMBER") {
		t.Errorf("a vCard 4.0 group card names members with MEMBER:\n%s", f.card("grp-vip.vcf"))
	}
	if strings.Contains(f.card(uid+".vcf"), "CATEGORIES") {
		t.Error("the group-vcards form wrote CATEGORIES")
	}
	// The retry finds the contact and the memberships, and writes neither again.
	puts := f.count("PUT")
	run1(t, reg, j)
	if f.count("PUT") != puts+1 || strings.Count(f.card("grp-kunden.vcf"), uid) != 1 {
		t.Errorf("the retry wrote %d times", f.count("PUT")-puts)
	}
	// A group that does not exist is refused before anything is written.
	cards := len(f.book().cards)
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "create-contact", CreateKey: CreateKey("node", 10),
		Contact: map[string]any{"familyName": "X", "groups": []any{"Kundne"}}, ResultVariable: "r"}, reg)
	if err == nil || !strings.Contains(err.Error(), `no group "Kundne"`) || len(f.book().cards) != cards {
		t.Errorf("err = %v, %d cards", err, len(f.book().cards))
	}
}

func TestUpdateContact(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	k := run1(t, reg, Job{Operation: "update-contact", ContactID: "anna.vcf",
		Contact: map[string]any{"email": "anna@neu.ch", "fields": map[string]any{"Kundennummer": "4712"}, "notes": nil}})["r"].(map[string]any)
	if k["email"] != "anna@neu.ch" || jsonOf(k["fields"]) != `{"Kundennummer":"4712"}` || jsonOf(k["groups"]) != `["Kunden"]` {
		t.Errorf("updated = %v", k)
	}
	stored := f.card("anna.vcf")
	for _, kept := range []string{"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQ", "EMAIL;TYPE=WORK:anna@neu.ch", "REV:20261025T093000Z"} {
		if !strings.Contains(stored, kept) {
			t.Errorf("stored card lacks %q:\n%s", kept, stored)
		}
	}
	// Every value null: nothing is written, and the answer is the contact as it is.
	puts := f.count("PUT")
	k = run1(t, reg, Job{Operation: "update-contact", ContactID: "anna.vcf", Contact: map[string]any{"notes": nil}})["r"].(map[string]any)
	if f.count("PUT") != puts || k["email"] != "anna@neu.ch" {
		t.Errorf("an update of nothing wrote: %v", k)
	}
	// A key the vCard cannot hold is refused before the server is reached.
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "update-contact", ContactID: "anna.vcf",
		Contact: map[string]any{"address": map[string]any{"countryCode": "CH"}}}, reg)
	if err == nil || !strings.Contains(err.Error(), "countryCode") {
		t.Errorf("err = %v", err)
	}
}

// TestUpdateReadsAgainWhenTheCardChanged: a write against a card another client changed
// in between is refused with 412; the worker reads the new version, applies its change
// to it, and writes that — keeping the other client's change.
func TestUpdateReadsAgainWhenTheCardChanged(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	f.beforePut = func(string) {
		f.put("anna.vcf", strings.Replace(annaCard, "END:VCARD", "TITLE:Chefin\r\nEND:VCARD", 1))
	}
	k := run1(t, reg, Job{Operation: "update-contact", ContactID: "anna.vcf", Contact: map[string]any{"phone": "079 000 00 00"}})["r"].(map[string]any)
	if k["phone"] != "079 000 00 00" || k["jobTitle"] != "Chefin" {
		t.Errorf("updated = %v", k)
	}
	if f.count("GET") != 2 {
		t.Errorf("GETs = %d, want a second read after the 412", f.count("GET"))
	}
	// A card that keeps changing fails the job rather than spinning.
	var hook func(string)
	hook = func(p string) {
		f.mu.Lock()
		f.failPut[p] = http.StatusPreconditionFailed
		f.beforePut = hook
		f.mu.Unlock()
	}
	f.beforePut = hook
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "update-contact", ContactID: "anna.vcf", Contact: map[string]any{"notes": "x"}}, reg)
	if err == nil || !strings.Contains(err.Error(), "changed between reading and writing it 3 times") {
		t.Errorf("err = %v", err)
	}
}

// A weak ETag cannot pass If-Match, so it is not sent: the update would otherwise fail
// on a card nobody else touched.
func TestAWeakETagIsNoPrecondition(t *testing.T) {
	f := newFakeDAV(t)
	f.weakETags = true
	reg := registry(t, f, "", "")
	created := run1(t, reg, Job{Operation: "create-contact", CreateKey: CreateKey("n", 1), Contact: map[string]any{"familyName": "W"}})["r"].(map[string]any)
	run1(t, reg, Job{Operation: "update-contact", ContactID: created["id"].(string), Contact: map[string]any{"notes": "x"}})
	if !strings.Contains(f.card(created["id"].(string)), "NOTE:x") {
		t.Error("the update with a weak ETag was not written")
	}
}

func TestDeleteContact(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	if out := run1(t, reg, Job{Operation: "delete-contact", ContactID: "bert.vcf"}); out != nil {
		t.Errorf("a delete returned %v", out)
	}
	if f.card("bert.vcf") != "" {
		t.Error("the contact is still there")
	}
	run1(t, reg, Job{Operation: "delete-contact", ContactID: "bert.vcf"})
}

func TestGroupChangesInTheCategoriesForm(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	run1(t, reg, Job{Operation: "add-to-group", ContactID: "bert.vcf", Group: "Kunden"})
	if !strings.Contains(f.card("bert.vcf"), "CATEGORIES:Lieferanten,Kunden") {
		t.Errorf("card:\n%s", f.card("bert.vcf"))
	}
	puts := f.count("PUT")
	run1(t, reg, Job{Operation: "add-to-group", ContactID: "bert.vcf", Group: "kunden"})
	if f.count("PUT") != puts {
		t.Error("a group already on the contact was written again")
	}
	run1(t, reg, Job{Operation: "remove-from-group", ContactID: "bert.vcf", Group: "LIEFERANTEN"})
	run1(t, reg, Job{Operation: "remove-from-group", ContactID: "bert.vcf", Group: "Lieferanten"})
	if !strings.Contains(f.card("bert.vcf"), "CATEGORIES:Kunden\r\n") {
		t.Errorf("card:\n%s", f.card("bert.vcf"))
	}
}

func TestGroupChangesInTheGroupVCardsForm(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", catalog.GroupsVCards)
	run1(t, reg, Job{Operation: "add-to-group", ContactID: "carl.vcf", Group: "Kunden"})
	// Carl's UID already carries urn:uuid:, so it is not prefixed twice.
	if !strings.Contains(f.card("grp-kunden.vcf"), "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:carl-uid\r\n") {
		t.Errorf("group card:\n%s", f.card("grp-kunden.vcf"))
	}
	puts := f.count("PUT")
	run1(t, reg, Job{Operation: "add-to-group", ContactID: "carl.vcf", Group: "kunden"})
	if f.count("PUT") != puts {
		t.Error("a member already in the group was written again")
	}
	run1(t, reg, Job{Operation: "remove-from-group", ContactID: "anna.vcf", Group: "Kunden"})
	run1(t, reg, Job{Operation: "remove-from-group", ContactID: "anna.vcf", Group: "Kunden"})
	if strings.Contains(f.card("grp-kunden.vcf"), "anna-uid") {
		t.Errorf("group card:\n%s", f.card("grp-kunden.vcf"))
	}
	f.put("ohne.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ohne UID\r\nN:;;;;\r\nEND:VCARD\r\n")
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "add-to-group", ContactID: "ohne.vcf", Group: "Kunden"}, reg)
	if err == nil || !strings.Contains(err.Error(), "no UID") {
		t.Errorf("err = %v", err)
	}
	f.put("grp-doppelt.vcf", strings.Replace(vipGroup, "grp-vip", "grp-vip2", 1))
	_, err = Run(context.Background(), Job{Connector: "verkauf", Operation: "add-to-group", ContactID: "carl.vcf", Group: "vip"}, registry(t, f, "", catalog.GroupsVCards))
	if err == nil || !strings.Contains(err.Error(), `2 groups are called "vip"`) {
		t.Errorf("err = %v", err)
	}
}

// TestRefusalsBeforeTheServerIsReached: every authored value is checked before a request
// is made, so a refused job has changed nothing.
func TestRefusalsBeforeTheServerIsReached(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", "")
	for _, c := range []struct {
		j    Job
		want string
	}{
		{Job{Connector: "anderes", Operation: "list-contacts"}, "anderes"},
		{Job{Operation: "merge-contacts"}, "unknown operation"},
		{Job{Operation: "get-contact", ContactID: "../../etc/passwd"}, "not a contact id"},
		{Job{Operation: "get-contact", ContactID: "a%2Fb.vcf"}, "not a contact id"},
		{Job{Operation: "find-contacts", Query: " "}, "query evaluated to nothing"},
		{Job{Operation: "add-to-group", ContactID: "a.vcf"}, "group evaluated to nothing"},
		{Job{Operation: "create-contact"}, "carries no contact"},
		{Job{Operation: "create-contact", Contact: map[string]any{"famillyName": "X"}}, `no key "famillyName"`},
	} {
		if c.j.Connector == "" {
			c.j.Connector = "verkauf"
		}
		before := len(f.requests)
		_, err := Run(context.Background(), c.j, reg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want mention of %q", c.j.Operation, err, c.want)
		}
		if len(f.requests) != before {
			t.Errorf("%s: the server was reached: %v", c.j.Operation, f.requests[before:])
		}
	}
}

// A card in the address book that is not a vCard fails a read of the book, naming it,
// rather than being skipped — a find that skipped it could answer "unknown".
func TestAnUnreadableCardFailsTheRead(t *testing.T) {
	f := seeded(t)
	f.mu.Lock()
	f.book().cards["kaputt.vcf"] = &fakeCard{data: "kein vcard", etag: `"x"`}
	f.mu.Unlock()
	_, err := Run(context.Background(), Job{Connector: "verkauf", Operation: "list-contacts", ResultVariable: "r"}, registry(t, f, "", ""))
	if err == nil || !strings.Contains(err.Error(), "kaputt.vcf") {
		t.Errorf("err = %v", err)
	}
}

// The listing reads the cards in batches, so an address book larger than one batch is
// read whole.
func TestALargeAddressBookIsReadInBatches(t *testing.T) {
	f := newFakeDAV(t)
	for i := 0; i < multigetBatch*2+3; i++ {
		n := fmt.Sprintf("%03d", i)
		f.put("c"+n+".vcf", card("u"+n, "N "+n, "N"+n+";;;;", ""))
	}
	l := list1(t, run1(t, registry(t, f, "", ""), Job{Operation: "list-contacts", MaxResults: 1000}))
	if len(l) != multigetBatch*2+3 || f.count("REPORT") != 3 {
		t.Errorf("%d contacts in %d multigets", len(l), f.count("REPORT"))
	}
}

// A group card another client changes between the read and the write is read again; a
// group card deleted while the job ran is said to be gone.
func TestGroupCardChangesWhileTheJobRuns(t *testing.T) {
	f := seeded(t)
	reg := registry(t, f, "", catalog.GroupsVCards)
	f.beforePut = func(string) {
		f.put("grp-kunden.vcf", strings.Replace(kundenGroup, "END:VCARD", "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:bert-uid\r\nEND:VCARD", 1))
	}
	run1(t, reg, Job{Operation: "add-to-group", ContactID: "carl.vcf", Group: "Kunden"})
	stored := f.card("grp-kunden.vcf")
	for _, uid := range []string{"anna-uid", "bert-uid", "carl-uid"} {
		if !strings.Contains(stored, uid) {
			t.Errorf("the group card lost %s:\n%s", uid, stored)
		}
	}
	f.beforePut = func(string) {
		f.mu.Lock()
		delete(f.book().cards, "grp-vip.vcf")
		f.mu.Unlock()
	}
	reg = registry(t, f, "", catalog.GroupsVCards)
	// The card is gone by the time it is written: the precondition fails, and the
	// re-read finds nothing.
	err := runErr(reg, Job{Operation: "add-to-group", ContactID: "carl.vcf", Group: "VIP", ResultVariable: "-"})
	if err == nil || !strings.Contains(err.Error(), `group "VIP" was deleted while this job ran`) {
		t.Errorf("err = %v", err)
	}
	var hook func(string)
	hook = func(p string) {
		f.mu.Lock()
		f.failPut[p] = http.StatusPreconditionFailed
		f.beforePut = hook
		f.mu.Unlock()
	}
	f.beforePut = hook
	err = runErr(registry(t, f, "", catalog.GroupsVCards), Job{Operation: "remove-from-group", ContactID: "anna.vcf", Group: "Kunden", ResultVariable: "-"})
	if err == nil || !strings.Contains(err.Error(), `group "Kunden" changed between reading and writing it 3 times`) {
		t.Errorf("err = %v", err)
	}
	f.beforePut = hook
	err = runErr(registry(t, f, "", ""), Job{Operation: "add-to-group", ContactID: "anna.vcf", Group: "Neu", ResultVariable: "-"})
	if err == nil || !strings.Contains(err.Error(), "changed between reading and writing it 3 times") {
		t.Errorf("err = %v", err)
	}
}

// A group card without FN is named by its N, as Apple Contacts names it.
func TestGroupNameFallsBackToN(t *testing.T) {
	c := mustParse(t, "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Partner;;;;\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nEND:VCARD\r\n")
	if !isGroupCard(c) || groupName(c) != "Partner" {
		t.Errorf("group %v name %q", isGroupCard(c), groupName(c))
	}
	if groupName(mustParse(t, "BEGIN:VCARD\r\nKIND:group\r\nEND:VCARD\r\n")) != "" {
		t.Error("a group card without any name got one")
	}
	if c.Version() != "3.0" || mustParse(t, "BEGIN:VCARD\r\nEND:VCARD\r\n").Version() != "3.0" {
		t.Error("a card without VERSION is vCard 3.0")
	}
}

// A contact deleted between the read and the write of an update or a group change is a
// missing contact, not a failure of another kind.
func TestAContactDeletedDuringTheWrite(t *testing.T) {
	for _, j := range []Job{
		{Operation: "update-contact", ContactID: "anna.vcf", Contact: map[string]any{"notes": "x"}},
		{Operation: "add-to-group", ContactID: "anna.vcf", Group: "Neu", ResultVariable: "-"},
	} {
		f := seeded(t)
		f.beforePut = func(p string) {
			f.mu.Lock()
			f.failPut[p] = http.StatusNotFound
			f.mu.Unlock()
		}
		var nf *notFound
		if err := runErr(registry(t, f, "", ""), j); !errors.As(err, &nf) {
			t.Errorf("%s: err = %v", j.Operation, err)
		}
	}
}

// A collection whose listing is redirected elsewhere still lists its members: they are
// judged against the collection the answer came from, not the one asked for, or the
// address book would read as empty — the answer a find must never give wrongly.
func TestARedirectedListingKeepsItsMembers(t *testing.T) {
	f := seeded(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, moved := strings.CutPrefix(r.URL.Path, "/alt/kontakte/"); moved {
			real := "/dav/addressbooks/anna/kontakte/" + rest
			if r.Method == "PROPFIND" && r.Header.Get("Depth") == "0" {
				r.URL.Path = real
				inner.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, real, http.StatusPermanentRedirect)
			return
		}
		inner.ServeHTTP(w, r)
	})
	reg := registry(t, f, f.srv.URL+"/alt/kontakte/", "")
	if got := ids(list1(t, run1(t, reg, Job{Operation: "list-contacts", MaxResults: 10}))); got != "carl.vcf,bert.vcf,anna.vcf" {
		t.Errorf("listing = %q", got)
	}
	if k := run1(t, reg, Job{Operation: "get-contact", ContactID: "anna.vcf"})["r"].(map[string]any); k["familyName"] != "Muster" {
		t.Errorf("contact = %v", k)
	}
}

// Photos make one batch of cards far larger than another: a batch whose answer is above
// the payload budget is asked for again in halves, and only a single card above it is
// refused, by its name.
func TestABatchAboveThePayloadBudgetIsHalved(t *testing.T) {
	f := seeded(t)
	photo := strings.Repeat("A", int(limits.Default().Payload*6/10))
	f.put("foto1.vcf", card("f1", "Foto Eins", "Eins;Foto;;;", "PHOTO;ENCODING=b;TYPE=JPEG:"+photo+"\r\n"))
	f.put("foto2.vcf", card("f2", "Foto Zwei", "Zwei;Foto;;;", "PHOTO;ENCODING=b;TYPE=JPEG:"+photo+"\r\n"))
	reg := registry(t, f, "", "")
	if l := list1(t, run1(t, reg, Job{Operation: "list-contacts", MaxResults: 10})); len(l) != 5 {
		t.Errorf("listed %d contacts", len(l))
	}
	if f.count("REPORT") < 3 {
		t.Errorf("%d multigets; the oversized batch was not halved", f.count("REPORT"))
	}
	f.put("riesig.vcf", card("r", "Riesig", "Riesig;;;;", "PHOTO;ENCODING=b;TYPE=JPEG:"+strings.Repeat("A", int(limits.Default().Payload))+"\r\n"))
	err := runErr(registry(t, f, "", ""), Job{Operation: "list-contacts"})
	if err == nil || !strings.Contains(err.Error(), "riesig.vcf is larger than the installation's payload budget") {
		t.Errorf("err = %v", err)
	}
}
