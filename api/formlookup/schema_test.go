package formlookup

import (
	"strings"
	"testing"
)

// exampleForm is the form the example process ships: a department chosen from fixed
// options, and a person searched within it — a select whose options come from the
// remote data source.
const exampleForm = `{
  "type": "default",
  "id": "entra-benutzerauswahl",
  "components": [
    {"type": "select", "key": "abteilung", "label": "Abteilung",
     "values": [{"label": "IT", "value": "IT"}, {"label": "Finanzen", "value": "Finance"}]},
    {"type": "select", "key": "user", "label": "Person", "validate": {"required": true},
     "atlasDataSource": {
       "version": 1, "workerType": "entra", "worker": "ucc_entra", "resource": "users",
       "searchFields": ["displayName", "mail", "userPrincipalName"],
       "filters": [{"property": "department", "field": "abteilung"}],
       "option": {"label": "displayName", "description": "mail", "details": ["department"]},
       "pageSize": 25
     }}
  ]
}`

func TestTheExampleFormParsesIntoOneDirectoryField(t *testing.T) {
	form, err := Parse([]byte(exampleForm))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("user")
	if !ok {
		t.Fatalf("fields = %+v, want user", form.Fields)
	}
	if f.Provider.WorkerType != "entra" || f.Provider.Resource != "users" || f.Source.Worker != "ucc_entra" {
		t.Errorf("field = %+v", f)
	}
	if !f.Required {
		t.Error("the field's own validate.required was not read")
	}
	if f.PageSize() != 25 || f.MinSearchLength() != 2 || !f.ActiveOnly() || f.Verify() != VerifyLookup {
		t.Errorf("defaults: page %d min %d active %v verify %q", f.PageSize(), f.MinSearchLength(), f.ActiveOnly(), f.Verify())
	}
	if got := f.Properties(); strings.Join(got, ",") != "id,displayName,mail,userPrincipalName,department" {
		t.Errorf("properties = %v", got)
	}
	opts, ok := form.StaticOptions("abteilung")
	if !ok || len(opts) != 2 || opts[1] != "Finance" {
		t.Errorf("static options of abteilung = %v (%v)", opts, ok)
	}
}

// A form without a directory field is every form that exists today, and parsing it
// must neither fail nor find anything.
func TestAnOrdinaryFormHasNoDirectoryField(t *testing.T) {
	for _, schema := range []string{
		`{"type":"default","components":[]}`,
		`{"type":"default","components":[{"type":"textfield","key":"name"},{"type":"group","components":[{"type":"number","key":"n"}]}]}`,
		`{"type":"default"}`,
	} {
		form, err := Parse([]byte(schema))
		if err != nil || len(form.Fields) != 0 {
			t.Errorf("%s: fields %v err %v", schema, form.Fields, err)
		}
	}
}

func TestADirectoryFieldInsideALayoutGroupIsFound(t *testing.T) {
	schema := strings.Replace(exampleForm, `"components": [`, `"components": [{"type":"group","components":[`, 1)
	schema = strings.Replace(schema, `]
}`, `]}]
}`, 1)
	form, err := Parse([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := form.Field("user"); !ok {
		t.Fatal("the field inside the group was not found")
	}
}

// What the server will run is decided when the form is saved, and refused there with
// the reason, rather than discovered by the first person who searches.
func TestAFormIsRefusedForADataSourceTheServerCannotHonour(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"newer version":      {`"version": 1`, `"version": 2`, "version 2"},
		"no version":         {`"version": 1, `, ``, "version"},
		"unknown type":       {`"workerType": "entra"`, `"workerType": "ldap"`, "no remote data source"},
		"unknown resource":   {`"resource": "users"`, `"resource": "devices"`, "no remote data source"},
		"no worker":          {`"worker": "ucc_entra"`, `"worker": ""`, "worker"},
		"FEEL worker":        {`"worker": "ucc_entra"`, `"worker": "= tenant"`, "plain name"},
		"secret search":      {`"searchFields": ["displayName", "mail", "userPrincipalName"]`, `"searchFields": ["passwordProfile"]`, "passwordProfile"},
		"filter property":    {`"property": "department"`, `"property": "employeeId"`, "employeeId"},
		"filter both":        {`"field": "abteilung"`, `"field": "abteilung", "value": "IT"`, "exactly one"},
		"filter neither":     {`, "field": "abteilung"`, ``, "exactly one"},
		"unknown field":      {`"field": "abteilung"`, `"field": "kostenstelle"`, "kostenstelle"},
		"itself":             {`"field": "abteilung"`, `"field": "user"`, "itself"},
		"unknown member":     {`"pageSize": 25`, `"pageSize": 25, "url": "https://graph.microsoft.com/v1.0/users"`, "url"},
		"page too big":       {`"pageSize": 25`, `"pageSize": 51`, "pageSize"},
		"page negative":      {`"pageSize": 25`, `"pageSize": -1`, "pageSize"},
		"label not readable": {`"label": "displayName"`, `"label": "passwordProfile"`, "passwordProfile"},
		"too many details":   {`"details": ["department"]`, `"details": ["department","jobTitle","mail","surname","givenName"]`, "details"},
		"verify":             {`"pageSize": 25`, `"pageSize": 25, "verify": "trust"`, "verify"},
		"min search":         {`"pageSize": 25`, `"pageSize": 25, "minSearchLength": 11`, "minSearchLength"},
		"not a select":       {`{"type": "select", "key": "user"`, `{"type": "radio", "key": "user"`, "select"},
		"static options too": {`"key": "user", "label": "Person"`, `"key": "user", "label": "Person", "values": [{"label": "A", "value": "a"}]`, "values"},
		"input options too":  {`"key": "user", "label": "Person"`, `"key": "user", "label": "Person", "valuesKey": "people"`, "valuesKey"},
		"FEEL options too":   {`"key": "user", "label": "Person"`, `"key": "user", "label": "Person", "valuesExpression": "=people"`, "valuesExpression"},
		"no key":             {`"key": "user", "label": "Person"`, `"label": "Person"`, "key"},
		"not an object":      {`"atlasDataSource": {`, `"atlasDataSource": "x", "ignored": {`, "atlasDataSource"},
	} {
		schema := strings.Replace(exampleForm, tc.from, tc.to, 1)
		if schema == exampleForm {
			t.Fatalf("%s: the mutation %q did not apply", name, tc.from)
		}
		_, err := Parse([]byte(schema))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

// A directory field saved as a textfield — the shape such fields were saved in first — is
// read as it was, so a form stored before the select still saves and completes.
func TestADirectoryFieldSavedAsATextfieldIsStillRead(t *testing.T) {
	schema := strings.Replace(exampleForm, `{"type": "select", "key": "user"`, `{"type": "textfield", "key": "user"`, 1)
	if schema == exampleForm {
		t.Fatal("the mutation did not apply")
	}
	form, err := Parse([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("user")
	if !ok || f.Source.Worker != "ucc_entra" || !f.Required {
		t.Fatalf("field = %+v (%v)", f, ok)
	}
}

func TestADirectoryFieldMayNotRepeat(t *testing.T) {
	schema := `{"type":"default","components":[{"type":"dynamiclist","path":"people","components":[
	  {"type":"textfield","key":"user","atlasDataSource":{"version":1,"workerType":"entra","worker":"w","resource":"users"}}]}]}`
	if _, err := Parse([]byte(schema)); err == nil || !strings.Contains(err.Error(), "repeat") {
		t.Fatalf("err = %v, want a refusal of a repeating directory field", err)
	}
}

func TestTwoFieldsMayNotShareAKey(t *testing.T) {
	schema := strings.Replace(exampleForm, `"key": "abteilung"`, `"key": "user"`, 1)
	schema = strings.Replace(schema, `"field": "abteilung"`, `"value": "IT"`, 1)
	if _, err := Parse([]byte(schema)); err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("err = %v, want a refusal naming the shared key", err)
	}
}

func TestADataSourceDefaultsToTheProvidersSearchAndOption(t *testing.T) {
	schema := `{"type":"default","components":[{"type":"textfield","key":"user",
	  "atlasDataSource":{"version":1,"workerType":"entra","worker":"ucc_entra","resource":"users","minSearchLength":0,"activeOnly":false,"verify":"format"}}]}`
	form, err := Parse([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := form.Field("user")
	if got := strings.Join(f.SearchFields(), ","); got != "displayName,givenName,surname,mail,userPrincipalName" {
		t.Errorf("search fields = %s", got)
	}
	if f.LabelProperty() != "displayName" || f.DescriptionProperty() != "mail" {
		t.Errorf("option = %s / %s", f.LabelProperty(), f.DescriptionProperty())
	}
	if f.MinSearchLength() != 0 || f.ActiveOnly() || f.Verify() != VerifyFormat || f.PageSize() != 25 {
		t.Errorf("explicit values not kept: %+v", f.Source)
	}
}

func TestASchemaThatIsNotAFormHasNoFields(t *testing.T) {
	if _, err := Parse([]byte(`[1,2]`)); err != nil {
		t.Errorf("err = %v: shape is the form store's question, not this package's", err)
	}
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Error("an unparseable schema was accepted")
	}
}

func TestMoreDataSourcesTheServerCannotHonourAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"negative version":  {`"version": 1`, `"version": -1`, "not one this server reads"},
		"control character": {`"worker": "ucc_entra"`, `"worker": "ucc\u0001entra"`, "not a Worker name"},
		"long worker name":  {`"worker": "ucc_entra"`, `"worker": "` + strings.Repeat("w", maxWorkerName+1) + `"`, "not a Worker name"},
		"filtered twice": {`[{"property": "department", "field": "abteilung"}]`,
			`[{"property": "department", "field": "abteilung"}, {"property": "department", "value": "IT"}]`, "filtered twice"},
		"fixed value":         {`"field": "abteilung"`, `"value": "I\u0000T"`, "not one a filter can carry"},
		"fixed and required":  {`"field": "abteilung"`, `"value": "IT", "required": true`, "cannot be required"},
		"detail not readable": {`"details": ["department"]`, `"details": ["passwordProfile"]`, "passwordProfile"},
	} {
		schema := strings.Replace(exampleForm, tc.from, tc.to, 1)
		if schema == exampleForm {
			t.Fatalf("%s: the mutation %q did not apply", name, tc.from)
		}
		_, err := Parse([]byte(schema))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
		if cerr := Check([]byte(schema)); cerr == nil || cerr.Error() != err.Error() {
			t.Errorf("%s: a form save would be told %v, not %v", name, cerr, err)
		}
	}
	if err := Check([]byte(exampleForm)); err != nil {
		t.Errorf("the example form may not be saved: %v", err)
	}
}
