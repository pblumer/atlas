package catalog

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestEveryOperationIsConsistent holds the rows to the rules the compiler and the
// worker both read off them, so a row added later cannot contradict itself.
func TestEveryOperationIsConsistent(t *testing.T) {
	names := Names()
	if !sort.StringsAreSorted(names) || len(names) != len(ops) {
		t.Fatalf("Names() = %v", names)
	}
	for _, name := range names {
		op, _ := Lookup(name)
		if !slices.Contains(Areas, op.Area) {
			t.Errorf("%s: area %q is not one of %v", name, op.Area, Areas)
		}
		if op.Label == "" {
			t.Errorf("%s has no label", name)
		}
		if (op.MaxResults.Max > 0) != (op.Result == ResultList) {
			t.Errorf("%s: a maxResults belongs to exactly the listings", name)
		}
		if op.MaxResults.Default > op.MaxResults.Max {
			t.Errorf("%s: default %d above ceiling %d", name, op.MaxResults.Default, op.MaxResults.Max)
		}
		if op.NeedsResult && op.Result == ResultNone {
			t.Errorf("%s needs a result it never returns", name)
		}
		if op.TakesResult() != (op.Result != ResultNone) {
			t.Errorf("%s: TakesResult disagrees with its result", name)
		}
		if op.Create && !op.NeedsContact {
			t.Errorf("%s creates without a contact", name)
		}
		if op.Truncates && op.MaxResults.Max == 0 {
			t.Errorf("%s truncates a list it has no bound for", name)
		}
	}
	if _, ok := Lookup("search-directory"); ok {
		t.Error("CardDAV has no directory, yet an operation searches one")
	}
}

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheckContactAcceptsWhatAModelWrites(t *testing.T) {
	for _, c := range []struct{ op, raw string }{
		{"create-contact", `{"givenName":"Anna","familyName":"Muster","email":"a@b.ch","phones":["079",{"value":"044","type":"work"}],
			"organization":"Firma","jobTitle":"CEO","department":"GL","address":{"streetAddress":"Weg 1","city":"Bern","type":"home"},
			"birthday":"1980-05-17","notes":"x","fields":{"Kundennummer":4711,"Aktiv":true,"Text":"a","Leer":null},"groups":["Kunden","=segment"]}`},
		{"create-contact", `{"emails":[{"value":"a@b.ch"}],"addresses":[{"city":"Bern"}],"birthday":"--05-17"}`},
		// A company has a display name and no name parts; vCard stores it as FN.
		{"create-contact", `{"displayName":"Firma AG","organization":"Firma AG"}`},
		// Every leaf may be FEEL, whose shape a deploy cannot see.
		{"create-contact", `{"emails":"=liste","address":"=adresse","fields":"=felder","groups":"=etiketten","birthday":"=datum"}`},
		{"update-contact", `{"email":"","address":"","birthday":"","notes":"","phones":[],"displayName":""}`},
		// A null leaves a field as it is; a contact of nulls and FEEL is fine.
		{"update-contact", `{"email":null,"notes":"=bemerkung"}`},
		{"update-contact", `{"groups":null,"notes":"x"}`},
		{"update-contact", `{"fields":{"Kunden-Nr":"=nr"}}`},
	} {
		if err := CheckContact(c.op, decode(t, c.raw)); err != nil {
			t.Errorf("%s %s: %v", c.op, c.raw, err)
		}
	}
}

func TestCheckContactRefusals(t *testing.T) {
	for _, c := range []struct{ op, raw, want string }{
		{"get-contact", `{"givenName":"A"}`, "writes no contact"},
		{"create-contact", `{}`, "at least one of"},
		{"create-contact", `{"famillyName":"A"}`, `no key "famillyName"`},
		{"create-contact", `{"id":"a.vcf"}`, "contactId"},
		{"update-contact", `{"groups":["Kunden"]}`, "add-to-group"},
		{"create-contact", `{"email":"a@b.ch","emails":["c@d.ch"]}`, "both email and emails"},
		{"create-contact", `{"phone":"1","phones":["2"]}`, "both phone and phones"},
		{"create-contact", `{"address":{"city":"Bern"},"addresses":[]}`, "both address and addresses"},
		{"create-contact", `{"givenName":1}`, "givenName must be text"},
		{"create-contact", `{"displayName":true}`, "displayName must be text"},
		{"create-contact", `{"birthday":5}`, "birthday must be"},
		{"create-contact", `{"birthday":"17.05.1980"}`, "not a date"},
		{"create-contact", `{"emails":"a@b.ch"}`, "emails must be"},
		{"create-contact", `{"emails":[""]}`, "is empty"},
		{"create-contact", `{"emails":[{"type":"work"}]}`, "needs a value"},
		{"create-contact", `{"emails":[{"value":"a","label":"x"}]}`, `no key "label"`},
		{"create-contact", `{"emails":[{"value":1}]}`, "must be text"},
		{"create-contact", `{"emails":[true]}`, "must be a value or"},
		{"create-contact", `{"address":"Weg 1, Bern"}`, "address object"},
		{"create-contact", `{"address":{"street":"Weg 1"}}`, `no key "street"`},
		// vCard's ADR has no country code and no formatted form, so a key the card
		// cannot hold is refused rather than reported as written.
		{"create-contact", `{"address":{"countryCode":"CH"}}`, `no key "countryCode"`},
		{"create-contact", `{"address":{"formattedValue":"Weg 1"}}`, `no key "formattedValue"`},
		{"create-contact", `{"address":{"city":3}}`, "must be text"},
		{"create-contact", `{"addresses":{"city":"Bern"}}`, "a list of addresses"},
		{"create-contact", `{"addresses":["Bern"]}`, "addresses[1]"},
		{"create-contact", `{"fields":["a"]}`, "custom field names"},
		{"create-contact", `{"fields":{" ":"a"}}`, "needs a name"},
		{"create-contact", `{"fields":{"Kunden Nr":"a"}}`, "letters, digits and hyphens"},
		{"create-contact", `{"fields":{"Kündennummer":"a"}}`, "letters, digits and hyphens"},
		{"create-contact", `{"fields":{"ABLabel":"a"}}`, "another client"},
		{"create-contact", `{"fields":{"apple-omit-year":"a"}}`, "another client"},
		{"create-contact", `{"fields":{"a":["b"]}}`, "text, a number or a boolean"},
		{"create-contact", `{"groups":"Kunden"}`, "a list of group names"},
		{"create-contact", `{"groups":[""]}`, "must name groups"},
		{"create-contact", `{"notes":null}`, "every value of the contact is null"},
	} {
		err := CheckContact(c.op, decode(t, c.raw))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: err = %v, want mention of %q", c.op, c.raw, err, c.want)
		}
	}
}

// TestCheckValuesHoldsAnEqualsSignToItsKey: at call time nothing is FEEL any more, so a
// value that starts with "=" is held to the shape of its key.
func TestCheckValuesHoldsAnEqualsSignToItsKey(t *testing.T) {
	if err := CheckValues("create-contact", decode(t, `{"emails":"=liste"}`)); err == nil {
		t.Error("a string passed for a list at call time")
	}
	if err := CheckValues("create-contact", decode(t, `{"notes":"=x+y ist eine Notiz"}`)); err != nil {
		t.Errorf("a note starting with = was refused: %v", err)
	}
	if err := CheckValues("update-contact", decode(t, `{"notes":null}`)); err != nil {
		t.Errorf("an update of nulls was refused: %v", err)
	}
	if err := CheckValues("create-contact", decode(t, `{"notes":null}`)); err == nil {
		t.Error("an empty contact may be created")
	}
	// A FEEL group name at deploy is a value at call time, and is then held to the rule.
	if err := CheckValues("create-contact", decode(t, `{"groups":["=x"]}`)); err != nil {
		t.Errorf("a group literally named =x was refused: %v", err)
	}
}

func TestContactIDs(t *testing.T) {
	for _, in := range []string{
		"5F3A0C2E-1B7D-4C9A-9E1F-2A3B4C5D6E7F.vcf", "abc", "a%40b.vcf", "user@host:1", "x_y-z.~vcf", " trimmed.vcf ",
	} {
		if err := CheckContactID(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, in := range []string{
		"", ".", "..", " ", "a/b", "../a.vcf", "a%2Fb", "a%2fb", "a%5Cb", "a%00b", "a%zzb", "a%4", "a b", "a?b", "a#b",
		strings.Repeat("a", 300),
	} {
		err := CheckContactID(in)
		if err == nil {
			t.Errorf("%q was accepted", in)
		} else if len(err.Error()) > 200 {
			t.Errorf("the refusal quotes the whole value: %v", err)
		}
	}
	if got := ContactID(" a.vcf "); got != "a.vcf" {
		t.Errorf("ContactID = %q", got)
	}
}

func TestFieldNames(t *testing.T) {
	for _, ok := range []string{"Kundennummer", "kunden-nr", "A1"} {
		if err := CheckFieldName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-", "a b", "a_b", "Ä", "ABUID", "ADDRESSBOOKSERVER-KIND", "evolution-x", strings.Repeat("a", 70)} {
		if err := CheckFieldName(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, vendor := range []string{"X-ABLabel", "x-apple-omit-year", "X-ADDRESSBOOKSERVER-MEMBER", "X-SOCIALPROFILE", "X-MS-CARDPICTURE", "X-ABUID"} {
		if !IsVendorProperty(vendor) {
			t.Errorf("%s is a vendor's own property", vendor)
		}
	}
	if IsVendorProperty("X-KUNDENNUMMER") {
		t.Error("a custom field was taken for a vendor's property")
	}
}

func TestGroupStyles(t *testing.T) {
	for in, want := range map[string]string{"": GroupsCategories, "categories": GroupsCategories, " GROUP-VCARDS ": GroupsVCards} {
		if got, err := ParseGroupStyle(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	if _, err := ParseGroupStyle("labels"); err == nil || !strings.Contains(err.Error(), "categories") {
		t.Errorf("err = %v", err)
	}
}

func TestWritableKeysIsACopy(t *testing.T) {
	keys := WritableKeys()
	keys[0] = "x"
	if WritableKeys()[0] == "x" {
		t.Error("WritableKeys hands out the table itself")
	}
}

func TestTypeNames(t *testing.T) {
	for v, want := range map[any]string{nil: "null", "a": "text", true: "a boolean", 1.5: "a number", json.Number("1"): "a number", 3: "int"} {
		if got := typeName(v); got != want {
			t.Errorf("typeName(%#v) = %q, want %q", v, got, want)
		}
	}
	if typeName([]any{}) != "a list" || typeName(map[string]any{}) != "an object" {
		t.Error("typeName of a list or an object")
	}
}
