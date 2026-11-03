package formlookup

import (
	"strings"
	"testing"
)

// upnForm is exampleForm with a field that submits the user principal name instead
// of the object id.
var upnForm = strings.Replace(exampleForm, `"option": {"label": "displayName",`,
	`"option": {"value": "userPrincipalName", "label": "displayName",`, 1)

// TestAFieldSubmitsTheValuePropertyItsOptionNames: a field whose option names
// userPrincipalName submits and checks that, and says so in words; an object id is no
// longer one of its values.
func TestAFieldSubmitsTheValuePropertyItsOptionNames(t *testing.T) {
	form, err := Parse([]byte(upnForm))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("user")
	if !ok {
		t.Fatalf("fields = %+v, want user", form.Fields)
	}
	if got := f.ValueProperty(); got != "userPrincipalName" {
		t.Errorf("ValueProperty = %q, want userPrincipalName", got)
	}
	if !f.ValidValue("anna@example.com") {
		t.Error("a user principal name is not a valid value of the field")
	}
	if f.ValidValue(anna) {
		t.Error("an object id is still a valid value of a field that submits the user principal name")
	}
	if got := f.ValueFormat(); got != "a user principal name (name@domain)" {
		t.Errorf("ValueFormat = %q", got)
	}
}

// TestAnOptionValueTheResourceDoesNotAdmitIsRefused: a field cannot submit a property
// its resource does not identify records by.
func TestAnOptionValueTheResourceDoesNotAdmitIsRefused(t *testing.T) {
	bad := strings.Replace(upnForm, `"value": "userPrincipalName"`, `"value": "mail"`, 1)
	if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "cannot be the value") {
		t.Fatalf("Parse = %v, want the option value refused", err)
	}
}
