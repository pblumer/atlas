package carddav

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func contactOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestProjectReadsAnAppleCard: the contact a model reads is Google Contacts' flat
// contact, filled from what Apple Contacts writes.
func TestProjectReadsAnAppleCard(t *testing.T) {
	got := project("1A2B.vcf", mustParse(t, appleCard), nil)
	want := map[string]any{
		"id": "1A2B.vcf", "displayName": "Dr. Anna Maria Muster", "givenName": "Anna", "familyName": "Muster",
		"email": "anna@firma.ch",
		"emails": []any{
			map[string]any{"value": "anna@firma.ch", "type": "work"},
			map[string]any{"value": "anna@privat.ch"},
		},
		"phone": "+41 79 123 45 67",
		"phones": []any{
			map[string]any{"value": "+41 79 123 45 67", "type": "mobile"},
			map[string]any{"value": "044 123 45 67", "type": "work"},
		},
		"organization": "Firma AG", "jobTitle": "Leiterin", "department": "Verkauf",
		"address": map[string]any{"streetAddress": "Bahnhofstrasse 1", "city": "Zürich", "region": "ZH", "postalCode": "8001", "country": "Schweiz", "type": "work"},
		"addresses": []any{
			map[string]any{"streetAddress": "Bahnhofstrasse 1", "city": "Zürich", "region": "ZH", "postalCode": "8001", "country": "Schweiz", "type": "work"},
		},
		"birthday": "--05-17", "notes": "Erste Zeile\nzweite, mit Komma",
		// X-ABUID is Apple's own bookkeeping, not a field anybody gave the contact.
		"fields": map[string]any{"Kundennummer": "4711"},
		"groups": []any{},
	}
	if jsonOf(got) != jsonOf(want) {
		t.Errorf("project =\n%s\nwant\n%s", jsonOf(got), jsonOf(want))
	}
}

func TestProjectReadsVCard4(t *testing.T) {
	c := mustParse(t, "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Firma AG\r\nORG:Firma AG\r\n"+
		"TEL;VALUE=uri;TYPE=\"cell,voice\";PREF=2:tel:+41-79-123-45-67\r\n"+
		"TEL;VALUE=uri;TYPE=work;PREF=1:tel:+41-44-000-00-00\r\n"+
		"BDAY:19800517\r\nCATEGORIES:Kunden,VIP\r\nCATEGORIES:kunden\r\nEND:VCARD\r\n")
	got := project("f.vcf", c, categoriesOf(c))
	if got["phone"] != "+41-44-000-00-00" {
		t.Errorf("the lowest PREF is the preferred one: phone = %v", got["phone"])
	}
	if p := got["phones"].([]any)[0].(map[string]any); p["type"] != "mobile" || p["value"] != "+41-79-123-45-67" {
		t.Errorf("phones[0] = %v", p)
	}
	if got["birthday"] != "1980-05-17" || got["givenName"] != nil || got["displayName"] != "Firma AG" {
		t.Errorf("birthday %v givenName %v displayName %v", got["birthday"], got["givenName"], got["displayName"])
	}
	// Categories are a set: the same group written twice in two cases is one group.
	if !reflect.DeepEqual(got["groups"], []any{"Kunden", "VIP"}) {
		t.Errorf("groups = %v", got["groups"])
	}
}

func TestBirthdayForms(t *testing.T) {
	for line, want := range map[string]string{
		"BDAY:1980-05-17":                        "1980-05-17",
		"BDAY:19800517":                          "1980-05-17",
		"BDAY:1980-05-17T00:00:00Z":              "1980-05-17",
		"BDAY:--0517":                            "--05-17",
		"BDAY:--05-17":                           "--05-17",
		"BDAY;X-APPLE-OMIT-YEAR=1604:1604-05-17": "--05-17",
		"BDAY;X-APPLE-OMIT-YEAR=1604:1980-05-17": "1980-05-17",
		"BDAY;VALUE=text:circa 1800":             "circa 1800",
		"BDAY:kein datum":                        "kein datum",
	} {
		c := mustParse(t, "BEGIN:VCARD\r\nVERSION:3.0\r\n"+line+"\r\nEND:VCARD\r\n")
		if got := project("x", c, nil)["birthday"]; got != want {
			t.Errorf("%s: birthday = %v, want %s", line, got, want)
		}
	}
}

// TestApplyKeepsWhatItDoesNotChange: an update changes the properties its keys map to
// and nothing else — the photo, Apple's labels and X-ABUID are written back as read.
func TestApplyKeepsWhatItDoesNotChange(t *testing.T) {
	c := mustParse(t, appleCard)
	if err := apply(c, contactOf(t, `{"email":"neu@firma.ch","notes":null,"jobTitle":"CEO"}`)); err != nil {
		t.Fatal(err)
	}
	out := string(c.Bytes())
	for _, kept := range []string{"PHOTO;ENCODING=b;TYPE=JPEG:/9j/", "item1.X-ABLabel:Privat2", "X-ABUID:1A2B3C4D", "item2.X-ABADR:ch", "NOTE:Erste Zeile\\nzweite\\, mit Komma"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q did not survive the update:\n%s", kept, out)
		}
	}
	got := project("x", c, nil)
	if got["email"] != "neu@firma.ch" || got["jobTitle"] != "CEO" {
		t.Errorf("email %v jobTitle %v", got["email"], got["jobTitle"])
	}
	// The preferred address changed; the other one, and the preference, stayed.
	emails := got["emails"].([]any)
	if len(emails) != 2 || emails[1].(map[string]any)["value"] != "anna@privat.ch" {
		t.Errorf("emails = %v", emails)
	}
	if !strings.Contains(out, "EMAIL;type=INTERNET;type=WORK;type=pref:neu@firma.ch") {
		t.Errorf("the changed address lost its parameters:\n%s", out)
	}
}

func TestApplyRules(t *testing.T) {
	for _, c := range []struct {
		name, write string
		check       func(t *testing.T, got map[string]any, out string)
	}{
		{"an empty string clears the preferred entry and keeps the others", `{"email":"","phone":""}`, func(t *testing.T, got map[string]any, _ string) {
			if got["email"] != "anna@privat.ch" || got["phone"] != "044 123 45 67" {
				t.Errorf("email %v phone %v", got["email"], got["phone"])
			}
		}},
		{"a list replaces the list and takes the Apple labels with it", `{"emails":["a@b.ch",{"value":"c@d.ch","type":"home"}],"phones":[{"value":"079","type":"mobile"}]}`, func(t *testing.T, got map[string]any, out string) {
			if jsonOf(got["emails"]) != `[{"value":"a@b.ch"},{"type":"home","value":"c@d.ch"}]` || jsonOf(got["phones"]) != `[{"type":"mobile","value":"079"}]` {
				t.Errorf("emails %v phones %v", got["emails"], got["phones"])
			}
			if strings.Contains(out, "item1.") || !strings.Contains(out, "TEL;TYPE=CELL:079") {
				t.Errorf("out:\n%s", out)
			}
		}},
		{"the name parts change N and FN is formatted again, honorifics kept", `{"givenName":"Petra","familyName":"Meier"}`, func(t *testing.T, got map[string]any, out string) {
			if got["displayName"] != "Dr. Petra Maria Meier" || !strings.Contains(out, "N:Meier;Petra;Maria;Dr.;") {
				t.Errorf("displayName %v\n%s", got["displayName"], out)
			}
		}},
		{"a display name given is the display name", `{"displayName":"Anni","givenName":"Annina"}`, func(t *testing.T, got map[string]any, _ string) {
			if got["displayName"] != "Anni" || got["givenName"] != "Annina" {
				t.Errorf("displayName %v givenName %v", got["displayName"], got["givenName"])
			}
		}},
		{"an address replaces the preferred one whole and keeps its type", `{"address":{"streetAddress":"Weg 2","city":"Bern"}}`, func(t *testing.T, got map[string]any, out string) {
			if jsonOf(got["address"]) != `{"city":"Bern","streetAddress":"Weg 2","type":"work"}` {
				t.Errorf("address %v", got["address"])
			}
			// Apple's country code belonged to the old address.
			if strings.Contains(out, "X-ABADR") {
				t.Errorf("a stale country code stayed:\n%s", out)
			}
		}},
		{"an empty address removes the preferred one", `{"address":""}`, func(t *testing.T, got map[string]any, _ string) {
			if got["address"] != nil || len(got["addresses"].([]any)) != 0 {
				t.Errorf("address %v addresses %v", got["address"], got["addresses"])
			}
		}},
		{"addresses replace the list", `{"addresses":[{"city":"Bern","type":"home"},{"city":"Thun"}]}`, func(t *testing.T, got map[string]any, out string) {
			if len(got["addresses"].([]any)) != 2 || !strings.Contains(out, "ADR;TYPE=HOME:;;;Bern;;;") {
				t.Errorf("addresses %v\n%s", got["addresses"], out)
			}
		}},
		{"organization and department share ORG", `{"organization":"","department":"Einkauf"}`, func(t *testing.T, got map[string]any, out string) {
			if got["organization"] != nil || got["department"] != "Einkauf" || !strings.Contains(out, "ORG:;Einkauf;") {
				t.Errorf("organization %v department %v\n%s", got["organization"], got["department"], out)
			}
		}},
		{"an empty organization and department remove ORG", `{"organization":"","department":""}`, func(t *testing.T, _ map[string]any, out string) {
			if strings.Contains(out, "\r\nORG") {
				t.Errorf("ORG stayed:\n%s", out)
			}
		}},
		{"fields set what they name and keep the rest", `{"fields":{"kundennummer":4712,"Segment":"A","Leer":null}}`, func(t *testing.T, got map[string]any, out string) {
			// The existing property is matched ignoring case and keeps its spelling.
			if jsonOf(got["fields"]) != `{"Kundennummer":"4712","Segment":"A"}` || !strings.Contains(out, "X-Kundennummer:4712") {
				t.Errorf("fields %v\n%s", got["fields"], out)
			}
		}},
		{"an empty field removes it", `{"fields":{"Kundennummer":""}}`, func(t *testing.T, got map[string]any, _ string) {
			if jsonOf(got["fields"]) != `{}` {
				t.Errorf("fields %v", got["fields"])
			}
		}},
		{"a yearless birthday in vCard 3.0 is written in Apple's form", `{"birthday":"--12-24"}`, func(t *testing.T, got map[string]any, out string) {
			if got["birthday"] != "--12-24" || !strings.Contains(out, "BDAY;X-APPLE-OMIT-YEAR=1604:1604-12-24") {
				t.Errorf("birthday %v\n%s", got["birthday"], out)
			}
		}},
		{"a birthday with a year replaces Apple's yearless one", `{"birthday":"1980-12-24"}`, func(t *testing.T, got map[string]any, out string) {
			if got["birthday"] != "1980-12-24" || strings.Contains(out, "OMIT-YEAR") {
				t.Errorf("birthday %v\n%s", got["birthday"], out)
			}
		}},
		{"an empty birthday and notes remove them", `{"birthday":"","notes":""}`, func(t *testing.T, got map[string]any, _ string) {
			if got["birthday"] != nil || got["notes"] != nil {
				t.Errorf("birthday %v notes %v", got["birthday"], got["notes"])
			}
		}},
		{"a null changes nothing", `{"givenName":null,"emails":null,"fields":null}`, func(t *testing.T, got map[string]any, _ string) {
			if got["givenName"] != "Anna" || len(got["emails"].([]any)) != 2 || got["displayName"] != "Dr. Anna Maria Muster" {
				t.Errorf("%v", got)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			card := mustParse(t, appleCard)
			if err := apply(card, contactOf(t, c.write)); err != nil {
				t.Fatal(err)
			}
			out := string(card.Bytes())
			c.check(t, project("x", mustParse(t, out), nil), out)
		})
	}
}

func TestApplyOnVCard4(t *testing.T) {
	c := mustParse(t, "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:A\r\nN:;A;;;\r\nTEL;VALUE=uri;TYPE=cell:tel:+41-79\r\nEND:VCARD\r\n")
	if err := apply(c, contactOf(t, `{"phone":"+41 79 999","birthday":"--05-17","phones":null}`)); err != nil {
		t.Fatal(err)
	}
	out := string(c.Bytes())
	// A changed number is text, so it loses VALUE=uri; vCard 4.0 writes the yearless
	// date as such.
	if !strings.Contains(out, "TEL;TYPE=cell:+41 79 999") || !strings.Contains(out, "BDAY:--0517") {
		t.Errorf("out:\n%s", out)
	}
	if err := apply(c, contactOf(t, `{"birthday":"1980-05-17","phones":[{"value":"1","type":"mobile"}]}`)); err != nil {
		t.Fatal(err)
	}
	if out := string(c.Bytes()); !strings.Contains(out, "BDAY:19800517") || !strings.Contains(out, "TEL;TYPE=cell:1") {
		t.Errorf("out:\n%s", out)
	}
}

// A new card gets the name, FN and N vCard 3.0 requires, whatever the model wrote.
func TestApplyToANewCard(t *testing.T) {
	for _, c := range []struct{ write, fn string }{
		{`{"givenName":"Anna","familyName":"Muster","email":"a@b.ch"}`, "Anna Muster"},
		{`{"organization":"Firma AG"}`, "Firma AG"},
		{`{"email":"info@firma.ch"}`, "info@firma.ch"},
		{`{"phones":["079"]}`, "079"},
		{`{"displayName":"Hauswart","notes":"x"}`, "Hauswart"},
	} {
		card := NewCard("U1")
		if err := apply(card, contactOf(t, c.write)); err != nil {
			t.Fatal(err)
		}
		out := string(card.Bytes())
		if got := project("x", card, nil)["displayName"]; got != c.fn {
			t.Errorf("%s: displayName = %v, want %s", c.write, got, c.fn)
		}
		if card.First("N") == nil || card.First("FN") == nil {
			t.Errorf("%s: N or FN missing:\n%s", c.write, out)
		}
	}
}

func TestApplyRefusesABirthdayThatIsNoDate(t *testing.T) {
	for _, b := range []string{"1980-13-01", "--00-10", "1980-05-32"} {
		if err := apply(NewCard("u"), map[string]any{"birthday": b}); err == nil || !strings.Contains(err.Error(), "not a date") {
			t.Errorf("%s: err = %v", b, err)
		}
	}
}

func TestCategories(t *testing.T) {
	c := mustParse(t, "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nCATEGORIES:Kunden,VIP\r\nEND:VCARD\r\n")
	if !addCategory(c, "kunden") {
		// Already there, ignoring case: no change.
	} else {
		t.Error("a category already present was added again")
	}
	if !addCategory(c, "Lieferanten") || !reflect.DeepEqual(categoriesOf(c), []string{"Kunden", "VIP", "Lieferanten"}) {
		t.Errorf("categories = %v", categoriesOf(c))
	}
	if !removeCategory(c, "vip") || removeCategory(c, "vip") {
		t.Error("removing a category is a change once and no change after")
	}
	removeCategory(c, "Kunden")
	removeCategory(c, "Lieferanten")
	if c.First("CATEGORIES") != nil {
		t.Errorf("an empty CATEGORIES stayed:\n%s", c.Bytes())
	}
	addCategory(c, "Neu, mit Komma")
	if got := categoriesOf(mustParse(t, string(c.Bytes()))); !reflect.DeepEqual(got, []string{"Neu, mit Komma"}) {
		t.Errorf("a comma inside a group name did not survive: %v\n%s", got, c.Bytes())
	}
}
