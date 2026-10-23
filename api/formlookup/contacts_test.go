package formlookup

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/lookup"
)

const (
	kundeAnna  = "people/c1001"
	kundeCarla = "people/c1003"
)

// contactsForm chooses a customer from a Google address book: the contacts labelled
// "Kunden", narrowed by the organization a person may type first.
const contactsForm = `{
  "type": "default",
  "id": "google-kundenauswahl",
  "components": [
    {"type": "textfield", "key": "firma", "label": "Firma"},
    {"type": "select", "key": "kunde", "label": "Kunde", "validate": {"required": true},
     "atlasDataSource": {
       "version": 1, "workerType": "googlecontacts", "worker": "verkauf", "resource": "contacts",
       "searchFields": ["displayName", "email", "organization"],
       "filters": [{"property": "groups", "value": "Kunden"}, {"property": "organization", "field": "firma"}],
       "option": {"label": "displayName", "description": "email", "details": ["organization", "phone"]}
     }}
  ]
}`

func contactsFakes() *fakes {
	f := newFakes()
	f.tasks[taskID] = fakeTask{schema: contactsForm, holder: "anna"}
	f.workers = map[string]Worker{"verkauf": {Name: "verkauf", WorkerType: "googlecontacts", Enabled: true, FormLookups: true}}
	return f
}

func TestAContactsFieldParsesWithoutAnActiveFilter(t *testing.T) {
	form, err := Parse([]byte(contactsForm))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("kunde")
	if !ok {
		t.Fatalf("fields = %+v, want kunde", form.Fields)
	}
	if f.Provider.WorkerType != "googlecontacts" || f.Provider.JobType != compiler.GoogleContactsJobType || f.Source.Worker != "verkauf" {
		t.Errorf("field = %+v", f)
	}
	if f.ActiveOnly() {
		t.Error("an address book has no disabled contacts, but the field leaves some out")
	}
	if got := strings.Join(f.Properties(), ","); got != "id,displayName,email,organization,phone" {
		t.Errorf("properties = %s", got)
	}
}

// Leaving out disabled entries is a promise only a source with disabled entries can
// keep; a form that asks it of an address book is told so when it is saved.
func TestAContactsFieldMayNotAskForActiveOnly(t *testing.T) {
	for _, v := range []string{"true", "false"} {
		schema := strings.Replace(contactsForm, `"resource": "contacts",`, `"resource": "contacts", "activeOnly": `+v+`,`, 1)
		if err := Check([]byte(schema)); err == nil || !strings.Contains(err.Error(), "activeOnly") {
			t.Errorf("activeOnly %s: err = %v", v, err)
		}
	}
}

func TestAContactsFieldIsRefusedForWhatAnAddressBookCannotAnswer(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"entra property":  {`"searchFields": ["displayName", "email", "organization"]`, `"searchFields": ["mail"]`, "mail"},
		"not filterable":  {`"property": "groups"`, `"property": "phone"`, "phone"},
		"not readable":    {`"details": ["organization", "phone"]`, `"details": ["groups"]`, "groups"},
		"another worker":  {`"resource": "contacts"`, `"resource": "users"`, "no remote data source"},
		"entra on google": {`"workerType": "googlecontacts"`, `"workerType": "entra"`, "no remote data source"},
	} {
		schema := strings.Replace(contactsForm, tc.from, tc.to, 1)
		if schema == contactsForm {
			t.Fatalf("%s: the mutation %q did not apply", name, tc.from)
		}
		if err := Check([]byte(schema)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestAContactsSearchReachesTheAddressBooksWorker(t *testing.T) {
	f := contactsFakes()
	f.page = lookup.Page{Items: []map[string]any{
		{"id": kundeAnna, "displayName": "Anna Annen", "email": "anna@kunde.ch", "organization": "Annen AG", "phone": "+41 44 555 01 01"},
		{"id": kundeCarla, "displayName": nil, "email": "info@zweifel.ch", "organization": "Zweifel GmbH"},
		{"id": "c1004", "displayName": "No resource name"},
	}}
	r := f.query(t, f.service(), "anna", taskID, "kunde", `{"search":"ann","filters":{"organization":"Annen AG"}}`)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %v", r.status, r.body)
	}
	if f.jobType != compiler.GoogleContactsJobType || f.conn != "verkauf" {
		t.Errorf("routed to %s/%s", f.jobType, f.conn)
	}
	want := lookup.Query{Resource: "contacts", Search: "ann", SearchFields: []string{"displayName", "email", "organization"},
		Filters:    []lookup.Filter{{Property: "groups", Value: "Kunden"}, {Property: "organization", Value: "Annen AG"}},
		Properties: []string{"id", "displayName", "email", "organization", "phone"}, PageSize: 25}
	if gj, wj := mustJSON(f.asked[0]), mustJSON(want); gj != wj {
		t.Errorf("query =\n  %s\nwant\n  %s", gj, wj)
	}
	items := r.body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v, want the two contacts with a resource name", items)
	}
	first := items[0].(map[string]any)
	if first["value"] != kundeAnna || first["label"] != "Anna Annen" || first["description"] != "anna@kunde.ch" {
		t.Errorf("first = %v", first)
	}
	if d := first["details"].(map[string]any); d["organization"] != "Annen AG" || d["phone"] != "+41 44 555 01 01" {
		t.Errorf("details = %v", d)
	}
	if second := items[1].(map[string]any); second["label"] != "info@zweifel.ch" || second["description"] != nil {
		t.Errorf("a contact without a name reads %v, want its e-mail address once", second)
	}
}

// A request for the label to search in is the form's to make, not the person's: a
// fixed filter cannot be replaced from the browser.
func TestAContactsFieldsFixedLabelCannotBeChangedByTheRequest(t *testing.T) {
	f := contactsFakes()
	r := f.query(t, f.service(), "anna", taskID, "kunde", `{"search":"ann","filters":{"groups":"Lieferanten"}}`)
	if r.status != http.StatusBadRequest || code(r) != "invalid_query" {
		t.Errorf("status %d %v", r.status, r.body)
	}
	if len(f.asked) != 0 {
		t.Error("the request reached the worker")
	}
}

func TestASubmittedContactIsCheckedInItsLabel(t *testing.T) {
	f := contactsFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": kundeAnna}}}
	if err := check(f, contactsForm, map[string]any{"kunde": kundeAnna, "firma": "Annen AG"}); err != nil {
		t.Fatalf("a valid selection was refused: %v", err)
	}
	q := f.asked[0]
	if q.Value != kundeAnna || q.ActiveOnly || len(q.Filters) != 2 || q.Filters[0].Value != "Kunden" || q.Filters[1].Value != "Annen AG" {
		t.Errorf("verification query = %+v", q)
	}
	for name, v := range map[string]any{
		"an e-mail address": "anna@kunde.ch",
		"an Entra id":       anna,
		"a bare id":         "c1001",
		"another resource":  "contactGroups/kunden",
	} {
		f := contactsFakes()
		err := check(f, contactsForm, map[string]any{"kunde": v})
		if err == nil || err.Code != "invalid_value" || !strings.Contains(err.Message, "people/c") {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(f.asked) != 0 {
			t.Errorf("%s: a value of the wrong shape was looked up", name)
		}
	}
	f = contactsFakes()
	f.page = lookup.Page{}
	if err := check(f, contactsForm, map[string]any{"kunde": kundeCarla}); err == nil || err.Code != "value_not_found" {
		t.Errorf("a contact outside the label: err = %v", err)
	}
}
