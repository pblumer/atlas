package googlecontacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
)

// anna is a customer as Google stores one: a work and a private address with the work
// one primary, a phone number Google has put into international form, an organization,
// an address, a birthday without a year, notes and a customer number.
func anna() person {
	return person{
		"names": []any{map[string]any{"givenName": "Anna", "familyName": "Muster", "displayName": "Anna Muster",
			"metadata": map[string]any{"primary": true, "source": map[string]any{"type": "CONTACT"}}}},
		"emailAddresses": []any{
			map[string]any{"value": "anna@privat.ch", "type": "home"},
			map[string]any{"value": "anna.muster@firma.ch", "type": "work", "metadata": map[string]any{"primary": true}},
		},
		"phoneNumbers":  []any{map[string]any{"value": "079 123 45 67", "canonicalForm": "+41791234567", "type": "mobile"}},
		"organizations": []any{map[string]any{"name": "Firma AG", "title": "Einkauf", "department": "Beschaffung"}},
		"addresses": []any{map[string]any{"streetAddress": "Bahnhofstrasse 1", "postalCode": "8001", "city": "Zürich",
			"countryCode": "CH", "type": "work", "formattedValue": "Bahnhofstrasse 1\n8001 Zürich"}},
		"birthdays":   []any{map[string]any{"date": map[string]any{"month": 5, "day": 17}}},
		"biographies": []any{map[string]any{"value": "Rückruf nur vormittags", "contentType": "TEXT_PLAIN"}},
		"userDefined": []any{map[string]any{"key": "Kundennummer", "value": "K-4711"}},
	}
}

func runJob(t *testing.T, f *fakePeople, j Job) (map[string]any, error) {
	t.Helper()
	if j.Connector == "" {
		j.Connector = "verkauf"
	}
	return Run(context.Background(), j, f.registry("verkauf"))
}

func mustRun(t *testing.T, f *fakePeople, j Job) map[string]any {
	t.Helper()
	out, err := runJob(t, f, j)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGetContactProjectsTheFlatContact(t *testing.T) {
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	rn := f.add(anna(), kunden, "contactGroups/starred")

	out := mustRun(t, f, Job{Operation: "get-contact", ContactID: rn, ResultVariable: "kunde"})
	want := map[string]any{
		"id": rn, "displayName": "Anna Muster", "givenName": "Anna", "familyName": "Muster",
		"email": "anna.muster@firma.ch",
		"emails": []any{
			map[string]any{"value": "anna@privat.ch", "type": "home"},
			map[string]any{"value": "anna.muster@firma.ch", "type": "work"},
		},
		"phone":        "079 123 45 67",
		"phones":       []any{map[string]any{"value": "079 123 45 67", "type": "mobile"}},
		"organization": "Firma AG", "jobTitle": "Einkauf", "department": "Beschaffung",
		"address": map[string]any{"streetAddress": "Bahnhofstrasse 1", "postalCode": "8001", "city": "Zürich",
			"countryCode": "CH", "type": "work", "formattedValue": "Bahnhofstrasse 1\n8001 Zürich"},
		"addresses": []any{map[string]any{"streetAddress": "Bahnhofstrasse 1", "postalCode": "8001", "city": "Zürich",
			"countryCode": "CH", "type": "work", "formattedValue": "Bahnhofstrasse 1\n8001 Zürich"}},
		"birthday": "--05-17", "notes": "Rückruf nur vormittags",
		"fields": map[string]any{"Kundennummer": "K-4711"},
		// Only the label a person made: myContacts and starred are not labels.
		"groups": []any{"Kunden"},
	}
	if !reflect.DeepEqual(out["kunde"], want) {
		t.Errorf("contact =\n%#v\nwant\n%#v", out["kunde"], want)
	}
	// The bare id addresses the same contact.
	out = mustRun(t, f, Job{Operation: "get-contact", ContactID: strings.TrimPrefix(rn, "people/"), ResultVariable: "kunde"})
	if out["kunde"].(map[string]any)["id"] != rn {
		t.Errorf("bare id read %v", out["kunde"])
	}
}

// TestProjectHasEveryKeyForAnEmptyContact: a model reads kunde.email without asking
// first whether the key is there.
func TestProjectHasEveryKeyForAnEmptyContact(t *testing.T) {
	got := project(person{"resourceName": "people/c1"}, nil)
	want := map[string]any{
		"id": "people/c1", "displayName": nil, "givenName": nil, "familyName": nil,
		"email": nil, "emails": []any{}, "phone": nil, "phones": []any{},
		"organization": nil, "jobTitle": nil, "department": nil,
		"address": nil, "addresses": []any{}, "birthday": nil, "notes": nil,
		"fields": map[string]any{}, "groups": []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("project = %#v", got)
	}
}

func TestProjectSkipsTheProfilesEntriesAndFormatsBirthdays(t *testing.T) {
	p := person{
		"emailAddresses": []any{
			map[string]any{"value": "profil@gmail.com", "metadata": map[string]any{"primary": true, "source": map[string]any{"type": "PROFILE"}}},
			map[string]any{"value": "kontakt@firma.ch", "metadata": map[string]any{"source": map[string]any{"type": "CONTACT"}}},
		},
		"birthdays": []any{map[string]any{"date": map[string]any{"year": json.Number("1980"), "month": json.Number("5"), "day": json.Number("7")}}},
	}
	got := project(p, nil)
	if got["email"] != "kontakt@firma.ch" || len(got["emails"].([]any)) != 1 {
		t.Errorf("emails = %v / %v", got["email"], got["emails"])
	}
	if got["birthday"] != "1980-05-07" {
		t.Errorf("birthday = %v", got["birthday"])
	}
	if b := birthdayOf(map[string]any{"text": "im Mai"}); b != "im Mai" {
		t.Errorf("text birthday = %q", b)
	}
}

func TestGetContactThatDoesNotExist(t *testing.T) {
	f := newFakePeople(t)
	_, err := runJob(t, f, Job{Operation: "get-contact", ContactID: "people/c999", ResultVariable: "k"})
	var be *job.BPMNError
	if err == nil || errors.As(err, &be) {
		t.Fatalf("uncaught: err = %v, want a failure", err)
	}
	if strings.Contains(err.Error(), "c999") {
		t.Errorf("the message names the contact id: %v", err)
	}
	_, err = runJob(t, f, Job{Operation: "get-contact", ContactID: "people/c999", ResultVariable: "k",
		ErrorCodes: []string{"OTHER", CodeContactNotFound}})
	if !errors.As(err, &be) || be.Code != CodeContactNotFound {
		t.Fatalf("caught: err = %v, want the BPMN error %s", err, CodeContactNotFound)
	}
}

func TestFindContactsMatchesExactly(t *testing.T) {
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	a := f.add(anna(), kunden)
	f.add(person{"names": []any{map[string]any{"familyName": "Annaheim"}},
		"emailAddresses": []any{map[string]any{"value": "anna.muster@firma.ch.example"}}})
	b := f.add(person{"names": []any{map[string]any{"familyName": "Beispiel"}},
		"phoneNumbers": []any{map[string]any{"value": "+41 79 123 45 67"}},
		"userDefined":  []any{map[string]any{"key": "kundennummer", "value": " k-4711 "}}})

	ids := func(out map[string]any) []string {
		var got []string
		for _, c := range out["treffer"].([]any) {
			got = append(got, c.(map[string]any)["id"].(string))
		}
		return got
	}
	for _, c := range []struct {
		name, query, field, group string
		want                      []string
	}{
		// Exact and case-insensitive: the near miss with a longer domain is not a match.
		{"e-mail", "Anna.Muster@Firma.CH", "", "", []string{a}},
		{"secondary e-mail", "anna@privat.ch", "", "", []string{a}},
		// By digits, against the number as stored or Google's international form.
		{"phone international", "+41 79 123 45 67", "", "", []string{b, a}},
		{"phone national", "0791234567", "", "", []string{a}},
		{"custom field", "K-4711", "Kundennummer", "", []string{b, a}},
		{"custom field in a label", "K-4711", "Kundennummer", "Kunden", []string{a}},
		{"nothing", "niemand@nirgends.ch", "", "", nil},
		// A query that is no phone number never matches one by its digits.
		{"short code", "123", "", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := mustRun(t, f, Job{Operation: "find-contacts", Query: c.query, Field: c.field, Group: c.group, MaxResults: 100, ResultVariable: "treffer"})
			if got := ids(out); !reflect.DeepEqual(got, c.want) {
				t.Errorf("found %v, want %v", got, c.want)
			}
		})
	}
	// find never asks the search cache.
	if n := len(f.requestsTo("search")); n != 0 {
		t.Errorf("find used searchContacts %d times", n)
	}
	// More matches than the cap fail rather than truncate.
	_, err := runJob(t, f, Job{Operation: "find-contacts", Query: "+41791234567", MaxResults: 1, ResultVariable: "treffer"})
	if err == nil || !strings.Contains(err.Error(), "matched 2") {
		t.Errorf("err = %v, want the cap refusal", err)
	}
}

func TestFindContactsReadsEveryPage(t *testing.T) {
	f := newFakePeople(t)
	for i := 0; i < connectionsPageSize+5; i++ {
		f.add(person{"names": []any{map[string]any{"familyName": "A"}}})
	}
	last := f.add(person{"names": []any{map[string]any{"familyName": "Z"}}, "emailAddresses": []any{map[string]any{"value": "z@x.ch"}}})
	out := mustRun(t, f, Job{Operation: "find-contacts", Query: "z@x.ch", MaxResults: 10, ResultVariable: "r"})
	if got := out["r"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != last {
		t.Errorf("found %v", got)
	}
	if n := len(f.requestsTo("connections")); n != 2 {
		t.Errorf("connections calls = %d, want 2 pages", n)
	}
}

func TestSearchContactsWarmsTheCacheFirst(t *testing.T) {
	f := newFakePeople(t)
	f.add(anna())
	f.add(person{"names": []any{map[string]any{"givenName": "Annabelle", "familyName": "Zweite"}}})
	f.add(person{"names": []any{map[string]any{"givenName": "Bruno"}}})
	out := mustRun(t, f, Job{Operation: "search-contacts", Query: "Ann", MaxResults: 1, ResultVariable: "r"})
	if got := out["r"].([]any); len(got) != 1 || got[0].(map[string]any)["givenName"] != "Anna" {
		t.Errorf("search = %v", got)
	}
	reqs := f.requestsTo("search")
	if len(reqs) != 2 {
		t.Fatalf("search calls = %d, want warmup + search", len(reqs))
	}
	if q := reqs[0].URL.Query(); q.Get("query") != "" || q.Get("pageSize") != "1" {
		t.Errorf("warmup = %v", q)
	}
	if q := reqs[1].URL.Query(); q.Get("query") != "Ann" || q.Get("pageSize") != "1" || q.Get("readMask") != readFields {
		t.Errorf("search = %v", q)
	}
}

func TestListContactsFailsAboveItsCap(t *testing.T) {
	f := newFakePeople(t)
	f.add(person{"names": []any{map[string]any{"familyName": "Zeta"}}})
	f.add(person{"names": []any{map[string]any{"familyName": "Alpha"}}})
	out := mustRun(t, f, Job{Operation: "list-contacts", MaxResults: 2, ResultVariable: "r"})
	got := out["r"].([]any)
	if len(got) != 2 || got[0].(map[string]any)["familyName"] != "Alpha" {
		t.Errorf("list = %v", got)
	}
	if q := f.requestsTo("connections")[0].URL.Query(); q.Get("sortOrder") != "LAST_NAME_ASCENDING" {
		t.Errorf("list query = %v", q)
	}
	if _, err := runJob(t, f, Job{Operation: "list-contacts", MaxResults: 1, ResultVariable: "r"}); err == nil || !strings.Contains(err.Error(), "maxResults cap of 1") {
		t.Errorf("err = %v, want the cap refusal", err)
	}
}

// TestListContactsOfALabelReadsItsMembersInBatches: a label's members are read by
// resource name, two hundred to a call, and the label is named as a person sees it.
func TestListContactsOfALabelReadsItsMembersInBatches(t *testing.T) {
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	for i := 0; i < 250; i++ {
		f.add(person{"names": []any{map[string]any{"familyName": "K"}}}, kunden)
	}
	f.add(person{"names": []any{map[string]any{"familyName": "Lieferant"}}})
	out := mustRun(t, f, Job{Operation: "list-contacts", Group: "kunden", MaxResults: 1000, ResultVariable: "r"})
	got := out["r"].([]any)
	if len(got) != 250 {
		t.Fatalf("members = %d", len(got))
	}
	if groups := got[0].(map[string]any)["groups"]; !reflect.DeepEqual(groups, []any{"Kunden"}) {
		t.Errorf("groups = %v", groups)
	}
	if n := len(f.requestsTo("batchGet")); n != 2 {
		t.Errorf("batchGet calls = %d, want 2", n)
	}
	// The labels were read once for the whole listing.
	if n := len(f.requestsTo("groups")); n != 1 {
		t.Errorf("contactGroups.list calls = %d, want 1", n)
	}
	if _, err := runJob(t, f, Job{Operation: "list-contacts", Group: "Kunden", MaxResults: 100, ResultVariable: "r"}); err == nil || !strings.Contains(err.Error(), "holds 250") {
		t.Errorf("err = %v, want the cap refusal naming the count", err)
	}
}

func TestCreateContactWritesEveryFieldAndItsLabelsInOneCall(t *testing.T) {
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	out := mustRun(t, f, Job{Operation: "create-contact", ResultVariable: "kunde", Contact: map[string]any{
		"givenName": "Beat", "familyName": "Beispiel", "email": "beat@beispiel.ch",
		"phones":       []any{"+41 44 000 00 00", map[string]any{"value": "079 000 00 00", "type": "mobile"}},
		"organization": "Beispiel GmbH", "jobTitle": "Inhaber",
		"address":  map[string]any{"streetAddress": "Weg 2", "postalCode": "3000", "city": "Bern", "countryCode": "CH"},
		"birthday": "1975-02-28", "notes": "Neukunde",
		"fields": map[string]any{"Kundennummer": json.Number("4711"), "Newsletter": true, "Leer": nil},
		"groups": []any{"Kunden"},
	}})
	body := f.bodyOf("create")
	want := person{
		"names":          []any{map[string]any{"givenName": "Beat", "familyName": "Beispiel"}},
		"emailAddresses": []any{map[string]any{"value": "beat@beispiel.ch"}},
		"phoneNumbers":   []any{map[string]any{"value": "+41 44 000 00 00"}, map[string]any{"value": "079 000 00 00", "type": "mobile"}},
		"organizations":  []any{map[string]any{"name": "Beispiel GmbH", "title": "Inhaber"}},
		"addresses":      []any{map[string]any{"streetAddress": "Weg 2", "postalCode": "3000", "city": "Bern", "countryCode": "CH"}},
		"birthdays":      []any{map[string]any{"date": map[string]any{"year": float64(1975), "month": float64(2), "day": float64(28)}}},
		"biographies":    []any{map[string]any{"value": "Neukunde", "contentType": "TEXT_PLAIN"}},
		"userDefined":    []any{map[string]any{"key": "Kundennummer", "value": "4711"}, map[string]any{"key": "Newsletter", "value": "true"}},
		"memberships":    []any{membership("contactGroups/myContacts"), membership(kunden)},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("create body =\n%#v\nwant\n%#v", body, want)
	}
	if q := f.requestsTo("create")[0].URL.Query(); q.Get("personFields") != readFields {
		t.Errorf("create query = %v", q)
	}
	k := out["kunde"].(map[string]any)
	if k["id"] == nil || k["email"] != "beat@beispiel.ch" || !reflect.DeepEqual(k["groups"], []any{"Kunden"}) {
		t.Errorf("created = %#v", k)
	}
}

func TestCreateContactRefusesAnUnknownLabelBeforeCreating(t *testing.T) {
	f := newFakePeople(t)
	f.addGroup("kunden1", "Kunden")
	_, err := runJob(t, f, Job{Operation: "create-contact", Contact: map[string]any{"familyName": "X", "groups": []any{"Kundne"}}})
	if err == nil || !strings.Contains(err.Error(), `no label "Kundne"`) || !strings.Contains(err.Error(), "Kunden") {
		t.Errorf("err = %v", err)
	}
	if n := len(f.requestsTo("create")); n != 0 {
		t.Errorf("a contact was created with an unknown label")
	}
}

func TestUpdateContactChangesWhatItNamesAndKeepsTheRest(t *testing.T) {
	f := newFakePeople(t)
	p := anna()
	p["names"] = []any{map[string]any{"givenName": "Anna", "familyName": "Muster", "honorificPrefix": "Dr.", "unstructuredName": "Dr. Anna Muster"}}
	p["userDefined"] = []any{map[string]any{"key": "Kundennummer", "value": "K-4711"}, map[string]any{"key": "Alt", "value": "x"}}
	rn := f.add(p)

	out := mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "kunde", Contact: map[string]any{
		"familyName": "Muster-Meier", // the rest of the name stays
		"email":      "anna@neu.ch",  // the primary address changes, the private one stays
		"jobTitle":   "",             // the title goes, the organization stays
		"notes":      "",             // cleared
		"fields":     map[string]any{"Alt": "", "Segment": "A"},
		"phone":      nil, // null: untouched
	}})
	k := out["kunde"].(map[string]any)
	if k["givenName"] != "Anna" || k["familyName"] != "Muster-Meier" {
		t.Errorf("name = %v %v", k["givenName"], k["familyName"])
	}
	if k["email"] != "anna@neu.ch" || len(k["emails"].([]any)) != 2 {
		t.Errorf("emails = %v / %v", k["email"], k["emails"])
	}
	if k["organization"] != "Firma AG" || k["jobTitle"] != nil {
		t.Errorf("organization = %v / %v", k["organization"], k["jobTitle"])
	}
	if k["notes"] != nil || k["phone"] != "079 123 45 67" {
		t.Errorf("notes %v phone %v", k["notes"], k["phone"])
	}
	if !reflect.DeepEqual(k["fields"], map[string]any{"Kundennummer": "K-4711", "Segment": "A"}) {
		t.Errorf("fields = %v", k["fields"])
	}
	req := f.requestsTo("update")[0].URL.Query()
	if req.Get("updatePersonFields") != "biographies,emailAddresses,names,organizations,userDefined" {
		t.Errorf("updatePersonFields = %q", req.Get("updatePersonFields"))
	}
	body := f.bodyOf("update")
	name := body["names"].([]any)[0].(map[string]any)
	if name["honorificPrefix"] != "Dr." || name["unstructuredName"] != nil {
		t.Errorf("name sent = %v", name)
	}
	if body["etag"] != "e1" || body["metadata"] == nil {
		t.Errorf("the update did not carry the version it read: %v", body)
	}
	// The read before the write asked only for what it changes.
	if q := f.requestsTo("get")[0].URL.Query(); q.Get("personFields") != "biographies,emailAddresses,names,organizations,userDefined,metadata" {
		t.Errorf("read before write = %v", q.Get("personFields"))
	}
}

func TestUpdateContactReplacesListsAndTheWholeAddress(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(anna())
	out := mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "k", Contact: map[string]any{
		"emails":   []any{"neu@firma.ch"},
		"address":  map[string]any{"streetAddress": "Neugasse 5", "postalCode": "8005", "city": "Zürich"},
		"birthday": "",
	}})
	k := out["k"].(map[string]any)
	if !reflect.DeepEqual(k["emails"], []any{map[string]any{"value": "neu@firma.ch"}}) {
		t.Errorf("emails = %v", k["emails"])
	}
	// The new address replaces the old one whole, and keeps its type.
	if !reflect.DeepEqual(k["address"], map[string]any{"streetAddress": "Neugasse 5", "postalCode": "8005", "city": "Zürich", "type": "work"}) {
		t.Errorf("address = %v", k["address"])
	}
	if k["birthday"] != nil {
		t.Errorf("birthday = %v", k["birthday"])
	}
	mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, Contact: map[string]any{"address": "", "email": "", "organization": "", "department": "", "jobTitle": ""}})
	k = mustRun(t, f, Job{Operation: "get-contact", ContactID: rn, ResultVariable: "k"})["k"].(map[string]any)
	if k["address"] != nil || k["email"] != nil || k["organization"] != nil {
		t.Errorf("after clearing: %v", k)
	}
}

func TestUpdateContactRereadsAContactThatChanged(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(anna())
	f.conflicts = 2
	out := mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "k", Contact: map[string]any{"notes": "neu"}})
	if out["k"].(map[string]any)["notes"] != "neu" {
		t.Errorf("notes = %v", out["k"])
	}
	if n := len(f.requestsTo("update")); n != 3 {
		t.Errorf("update calls = %d, want 3", n)
	}
	f.conflicts = updateAttempts
	_, err := runJob(t, f, Job{Operation: "update-contact", ContactID: rn, Contact: map[string]any{"notes": "x"}})
	if err == nil || !strings.Contains(err.Error(), "changed between reading and writing") {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateContactWithOnlyNullsChangesNothing(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(anna())
	out := mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "k", Contact: map[string]any{"notes": nil, "groups": nil}})
	if out["k"].(map[string]any)["notes"] != "Rückruf nur vormittags" {
		t.Errorf("k = %v", out["k"])
	}
	if n := len(f.requestsTo("update")); n != 0 {
		t.Errorf("an update of nothing was sent")
	}
	_, err := runJob(t, f, Job{Operation: "update-contact", ContactID: "people/c404", Contact: map[string]any{"notes": nil}, ErrorCodes: []string{CodeContactNotFound}})
	var be *job.BPMNError
	if !errors.As(err, &be) {
		t.Errorf("err = %v, want the not-found code", err)
	}
}

func TestUpdateContactOfAMissingContact(t *testing.T) {
	f := newFakePeople(t)
	_, err := runJob(t, f, Job{Operation: "update-contact", ContactID: "people/c404", Contact: map[string]any{"notes": "x"}, ErrorCodes: []string{CodeContactNotFound}})
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != CodeContactNotFound {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteContactIsSafeToRepeat(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(anna())
	for i := 0; i < 2; i++ {
		out, err := runJob(t, f, Job{Operation: "delete-contact", ContactID: rn})
		if err != nil || out != nil {
			t.Fatalf("round %d: out %v err %v", i, out, err)
		}
	}
	if _, ok := f.people[rn]; ok {
		t.Error("the contact is still there")
	}
}

func TestLabelsAreAddedAndRemovedByName(t *testing.T) {
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	rn := f.add(anna())
	for i := 0; i < 2; i++ { // adding twice is one membership
		mustRun(t, f, Job{Operation: "add-to-group", ContactID: rn, Group: "KUNDEN"})
	}
	if !reflect.DeepEqual(f.members[kunden], []string{rn}) {
		t.Errorf("members = %v", f.members[kunden])
	}
	if body := f.bodyOf("modify"); !reflect.DeepEqual(body, person{"resourceNamesToAdd": []any{rn}}) {
		t.Errorf("modify body = %v", body)
	}
	mustRun(t, f, Job{Operation: "remove-from-group", ContactID: rn, Group: kunden})
	if len(f.members[kunden]) != 0 {
		t.Errorf("members = %v", f.members[kunden])
	}

	_, err := runJob(t, f, Job{Operation: "add-to-group", ContactID: "people/c404", Group: "Kunden", ErrorCodes: []string{CodeContactNotFound}})
	var be *job.BPMNError
	if !errors.As(err, &be) {
		t.Errorf("missing contact: err = %v", err)
	}
	f.lastOnly[rn] = true
	if _, err := runJob(t, f, Job{Operation: "remove-from-group", ContactID: rn, Group: "myContacts"}); err == nil || !strings.Contains(err.Error(), "last") {
		t.Errorf("last group: err = %v", err)
	}
	f.addGroup("kunden2", "kunden")
	if _, err := runJob(t, f, Job{Operation: "add-to-group", ContactID: rn, Group: "KUNDEN"}); err == nil || !strings.Contains(err.Error(), "2 labels") {
		t.Errorf("ambiguous: err = %v", err)
	}
	// An exact match wins over the ones that differ only in case.
	mustRun(t, f, Job{Operation: "add-to-group", ContactID: rn, Group: "kunden"})
	if !reflect.DeepEqual(f.members["contactGroups/kunden2"], []string{rn}) {
		t.Errorf("exact match: members = %v", f.members["contactGroups/kunden2"])
	}
}

func TestSearchDirectoryAsksBothDirectorySources(t *testing.T) {
	f := newFakePeople(t)
	f.dir = []person{{"resourceName": "people/1001", "names": []any{map[string]any{"givenName": "Claudia", "familyName": "Kollegin"}},
		"emailAddresses": []any{map[string]any{"value": "claudia@firma.ch"}}}}
	out := mustRun(t, f, Job{Operation: "search-directory", Query: "clau", MaxResults: 5, ResultVariable: "r"})
	got := out["r"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["email"] != "claudia@firma.ch" {
		t.Errorf("directory = %v", got)
	}
	q := f.requestsTo("directory")[0].URL.Query()
	if !reflect.DeepEqual(q["sources"], []string{"DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE", "DIRECTORY_SOURCE_TYPE_DOMAIN_CONTACT"}) ||
		q.Get("readMask") != directoryFields || q.Get("pageSize") != "5" {
		t.Errorf("directory query = %v", q)
	}
}

func TestRunRefusals(t *testing.T) {
	f := newFakePeople(t)
	for _, c := range []struct {
		name string
		j    Job
		want string
	}{
		{"unknown worker", Job{Connector: "andere", Operation: "get-contact", ContactID: "c1"}, `no worker registered as "andere"`},
		{"unknown operation", Job{Operation: "merge-contacts"}, "unknown operation"},
		{"bad id", Job{Operation: "get-contact", ContactID: "people/../contactGroups/x"}, "not a contact id"},
		{"empty query", Job{Operation: "find-contacts", Query: " "}, "query evaluated to nothing"},
		{"empty label", Job{Operation: "add-to-group", ContactID: "c1"}, "label evaluated to nothing"},
		{"no contact", Job{Operation: "create-contact"}, "carries no contact"},
		{"unknown key", Job{Operation: "create-contact", Contact: map[string]any{"famillyName": "X"}}, `no key "famillyName"`},
		// At call time a value starting with = is a value, held to its key's shape.
		{"value shaped like FEEL", Job{Operation: "create-contact", Contact: map[string]any{"emails": "=liste"}}, "emails must be"},
		{"bad birthday", Job{Operation: "create-contact", Contact: map[string]any{"birthday": "17.05.1980"}}, "not a date"},
		{"labels on update", Job{Operation: "update-contact", ContactID: "c1", Contact: map[string]any{"groups": []any{"Kunden"}}}, "add-to-group"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := runJob(t, f, c.j)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
	if len(f.requests) != 0 {
		t.Errorf("a refused job reached Google %d times", len(f.requests))
	}
}

// TestErrorsDoNotQuoteTheQuery: what a process looked for is often personal data, and
// an incident's message is kept with the instance.
func TestErrorsDoNotQuoteTheQuery(t *testing.T) {
	f := newFakePeople(t)
	f.fail["connections"] = 500
	f.fail["search"] = 503
	for _, op := range []string{"find-contacts", "search-contacts"} {
		_, err := runJob(t, f, Job{Operation: op, Query: "geheim@kunde.ch", ResultVariable: "r"})
		if err == nil || strings.Contains(err.Error(), "geheim") {
			t.Errorf("%s: err = %v", op, err)
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.HTTP < 500 {
			t.Errorf("%s: err = %#v, want Google's status", op, err)
		}
	}
}

func TestWithoutResultVariableNothingIsWritten(t *testing.T) {
	f := newFakePeople(t)
	out := mustRun(t, f, Job{Operation: "create-contact", Contact: map[string]any{"familyName": "X"}})
	if out != nil {
		t.Errorf("out = %v", out)
	}
}

func TestAPIErrorReadsGooglesEnvelopeOrTheBody(t *testing.T) {
	e := apiError("people.get", 403, []byte(`{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`))
	if e.Error() != "googlecontacts: people.get returned HTTP 403: The caller does not have permission" {
		t.Errorf("err = %v", e)
	}
	long := strings.Repeat("x", 400)
	e = apiError("people.get", 502, []byte("<html>"+long+"</html>"))
	if !strings.Contains(e.Error(), "HTTP 502") || len(e.Error()) > 400 {
		t.Errorf("err = %v", e)
	}
	if (&APIError{Call: "c", HTTP: 500}).Error() != "googlecontacts: c returned HTTP 500: Internal Server Error" {
		t.Error("an empty message falls back to the status text")
	}
}

// TestGooglesRefusalsReachTheJob: whichever People API call of an operation fails, the
// job fails with Google's status — nothing is reported done that was not.
func TestGooglesRefusalsReachTheJob(t *testing.T) {
	for _, c := range []struct {
		name, method string
		j            Job
		setup        func(f *fakePeople, rn string)
	}{
		{"get", "get", Job{Operation: "get-contact", ResultVariable: "k"}, nil},
		{"labels of a read", "groups", Job{Operation: "get-contact", ResultVariable: "k"}, nil},
		{"labels of a listing", "groups", Job{Operation: "list-contacts", MaxResults: 10, ResultVariable: "k"}, nil},
		{"label of a listing", "groups", Job{Operation: "list-contacts", Group: "Kunden", MaxResults: 10, ResultVariable: "k"}, nil},
		{"label members", "group", Job{Operation: "list-contacts", Group: "Kunden", MaxResults: 10, ResultVariable: "k"}, nil},
		{"label member batch", "batchGet", Job{Operation: "list-contacts", Group: "Kunden", MaxResults: 10, ResultVariable: "k"}, nil},
		{"find in a label", "group", Job{Operation: "find-contacts", Query: "a@b.ch", Group: "Kunden", MaxResults: 10, ResultVariable: "k"}, nil},
		{"search warmup", "search", Job{Operation: "search-contacts", Query: "Ann", MaxResults: 5, ResultVariable: "k"}, nil},
		{"directory", "directory", Job{Operation: "search-directory", Query: "Ann", MaxResults: 5, ResultVariable: "k"}, nil},
		{"create", "create", Job{Operation: "create-contact", Contact: map[string]any{"familyName": "X"}}, nil},
		{"labels of a create", "groups", Job{Operation: "create-contact", Contact: map[string]any{"familyName": "X", "groups": []any{"Kunden"}}}, nil},
		{"read before update", "get", Job{Operation: "update-contact", Contact: map[string]any{"notes": "x"}}, nil},
		{"read of an update of nothing", "get", Job{Operation: "update-contact", Contact: map[string]any{"notes": nil}}, nil},
		{"update", "update", Job{Operation: "update-contact", Contact: map[string]any{"notes": "x"}}, nil},
		{"delete", "delete", Job{Operation: "delete-contact"}, nil},
		{"label lookup", "groups", Job{Operation: "add-to-group", Group: "Kunden"}, nil},
		{"label change", "modify", Job{Operation: "add-to-group", Group: "Kunden"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakePeople(t)
			kunden := f.addGroup("kunden1", "Kunden")
			rn := f.add(anna(), kunden)
			c.j.ContactID = rn
			f.fail[c.method] = 500
			_, err := runJob(t, f, c.j)
			var ae *APIError
			if !errors.As(err, &ae) || ae.HTTP != 500 {
				t.Errorf("err = %v, want Google's 500", err)
			}
		})
	}
}

func TestLabelsArePagedAndAFindInALabelIsBounded(t *testing.T) {
	f := newFakePeople(t)
	f.groupPage = 1
	kunden := f.addGroup("kunden1", "Kunden")
	rn := f.add(anna(), kunden)
	out := mustRun(t, f, Job{Operation: "get-contact", ContactID: rn, ResultVariable: "k"})
	if !reflect.DeepEqual(out["k"].(map[string]any)["groups"], []any{"Kunden"}) {
		t.Errorf("groups = %v", out["k"])
	}
	if n := len(f.requestsTo("groups")); n != 3 {
		t.Errorf("contactGroups.list pages = %d, want 3", n)
	}
	// A find in a label too large to read is refused rather than read in part.
	for i := 0; i < 3; i++ {
		f.members[kunden] = append(f.members[kunden], rn)
	}
	b := &book{c: f.client()}
	people, count, err := b.members(context.Background(), "Kunden", 2)
	if err != nil || people != nil || count != 4 {
		t.Errorf("members = %v, %d, %v", people, count, err)
	}
}

// TestUpdateBuildsNamesAndOrganizationsFromNothing: a contact without a name or an
// organization gets one, and one whose last part is cleared loses the entry.
func TestUpdateBuildsNamesAndOrganizationsFromNothing(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(person{"emailAddresses": []any{map[string]any{"value": "x@y.ch"}}})
	k := mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "k",
		Contact: map[string]any{"givenName": "Neu", "department": "Verkauf", "phone": "044 111 11 11", "email": "", "birthday": "--12-24", "address": map[string]any{"city": "Chur"}}})["k"].(map[string]any)
	if k["givenName"] != "Neu" || k["department"] != "Verkauf" || k["phone"] != "044 111 11 11" || k["email"] != nil ||
		k["birthday"] != "--12-24" || k["address"].(map[string]any)["city"] != "Chur" {
		t.Errorf("k = %v", k)
	}
	k = mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, ResultVariable: "k",
		Contact: map[string]any{"givenName": "", "department": "", "organization": "", "phone": "", "fields": map[string]any{"Neu": "", "Leer": nil}}})["k"].(map[string]any)
	if k["givenName"] != nil || k["department"] != nil || k["phone"] != nil {
		t.Errorf("k = %v", k)
	}
	if body := f.bodyOf("update"); len(body["names"].([]any)) != 0 || len(body["organizations"].([]any)) != 0 {
		t.Errorf("cleared entries were sent: %v", body)
	}
	if _, err := runJob(t, f, Job{Operation: "update-contact", ContactID: rn, Contact: map[string]any{"birthday": "1980-13-40"}}); err == nil || !strings.Contains(err.Error(), "not a date") {
		t.Errorf("err = %v", err)
	}
}

// TestAChangedPhoneNumberLosesItsOldCanonicalForm: Google's E.164 form belongs to the
// number it was computed from, and the written value is trimmed like every other.
func TestAChangedPhoneNumberLosesItsOldCanonicalForm(t *testing.T) {
	f := newFakePeople(t)
	rn := f.add(anna())
	mustRun(t, f, Job{Operation: "update-contact", ContactID: rn, Contact: map[string]any{"phone": " 044 222 22 22 ", "givenName": " Anna-Lena "}})
	body := f.bodyOf("update")
	phone := body["phoneNumbers"].([]any)[0].(map[string]any)
	if phone["value"] != "044 222 22 22" || phone["canonicalForm"] != nil || phone["type"] != "mobile" {
		t.Errorf("phone sent = %v", phone)
	}
	if name := body["names"].([]any)[0].(map[string]any); name["givenName"] != "Anna-Lena" {
		t.Errorf("name sent = %v", name)
	}
}

func TestAnEndlessLabelListingIsRefused(t *testing.T) {
	f := newFakePeople(t)
	f.groupPage = 1
	for i := 0; i < maxGroupPages+1; i++ {
		f.addGroup(fmt.Sprintf("g%d", i), fmt.Sprintf("Label %d", i))
	}
	if _, err := f.client().groups(context.Background()); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("err = %v", err)
	}
}
