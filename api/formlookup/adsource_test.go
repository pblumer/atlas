package formlookup

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk/lookup"
)

// An Active Directory field (ADR-0473, amended 2026-11-04): the same field model as
// Entra's, read from the AD Worker Type's own table.

func adField(t *testing.T, source string) (Field, error) {
	t.Helper()
	schema := `{"type":"default","components":[{"type":"select","key":"person","label":"Person",
	  "atlasDataSource": ` + source + `}]}`
	form, err := Parse([]byte(schema))
	if err != nil {
		return Field{}, err
	}
	f, ok := form.Field("person")
	if !ok {
		t.Fatal("no field person")
	}
	return f, nil
}

func TestAnADUserFieldSubmitsALogonName(t *testing.T) {
	f, err := adField(t, `{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"users",
	  "searchFields":["displayName","sAMAccountName"],"filters":[{"property":"department","value":"FIN"}],
	  "option":{"description":"mail"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if f.Provider.JobType == "" || f.Provider.ValueProperty != "sAMAccountName" || !f.ActiveOnly() {
		t.Errorf("field = %+v", f)
	}
	if got := strings.Join(f.Properties(), ","); !strings.HasPrefix(got, "sAMAccountName,") {
		t.Errorf("properties = %s, want the logon name first", got)
	}
	if !f.Provider.ValidValueAs("", "anna.muster") || f.Provider.ValidValueAs("", "a*") {
		t.Error("the value shape is not the AD table's")
	}
	if !f.Provider.ValidValueAs("userPrincipalName", "anna@example.org") {
		t.Error("a field may submit the principal name instead")
	}
}

func TestAnADFieldIsHeldToTheADTable(t *testing.T) {
	for _, src := range []string{
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"users","searchFields":["surname"]}`,
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"users","option":{"value":"objectGUID"}}`,
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"users","option":{"details":["unicodePwd"]}}`,
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"users","filters":[{"property":"memberOf","value":"x"}]}`,
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"computers"}`,
		`{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"groups","activeOnly":true}`,
	} {
		if _, err := adField(t, src); err == nil {
			t.Errorf("accepted %s", src)
		}
	}
}

// A group has no disabled state, so the "only enabled" default does not reach it — a
// group field would otherwise be refused by the Worker on every search.
func TestAGroupFieldAsksForNoActiveFlag(t *testing.T) {
	f, err := adField(t, `{"version":1,"workerType":"ad","worker":"ad_verzeichnis","resource":"groups","searchFields":["cn"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if f.ActiveOnly() {
		t.Error("a group field asks for enabled records only")
	}
	q := lookup.Query{Resource: "groups", Search: "sg", SearchFields: f.Source.SearchFields,
		Properties: f.Properties(), ActiveOnly: f.ActiveOnly()}
	if err := f.Provider.Check(q); err != nil {
		t.Errorf("the Worker would refuse the field's own query: %v", err)
	}
}
