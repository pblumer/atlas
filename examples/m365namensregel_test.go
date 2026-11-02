package examples

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/dmn"
)

// TestM365NamingRuleTakesThePrefix evaluates the naming rule of m365-dienste: without a
// prefix it builds the names the README documents, and with NAME_PREFIX (a shared tenant,
// deploy/opentofu/m365-devenv) every display name and alias starts with that prefix, so
// what the catalogue creates stays inside the test environment's Exchange scope.
func TestM365NamingRuleTakesThePrefix(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/m365-namensregel.dmn")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule, err := dmn.CompileStandalone(ctx, src, "M365Namensregel")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, objekttyp, praefix   string
		nullPrefix                 bool
		wantAnzeigename, wantAlias string
	}{
		{name: "no prefix (null)", objekttyp: "funktionspostfach", nullPrefix: true,
			wantAnzeigename: "FP-FIN Kreditoren", wantAlias: "fp-fin-kreditoren"},
		{name: "empty prefix", objekttyp: "verteiler", praefix: "",
			wantAnzeigename: "VL-FIN Kreditoren", wantAlias: "vl-fin-kreditoren"},
		{name: "shared tenant", objekttyp: "team", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-TM-FIN Kreditoren", wantAlias: "atlas-test-58o4-tm-fin-kreditoren"},
		{name: "shared tenant room", objekttyp: "raum", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-RM-FIN Kreditoren", wantAlias: "atlas-test-58o4-rm-fin-kreditoren"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := map[string]any{"objekttyp": c.objekttyp, "slug": "kreditoren", "bereich": "fin", "bezeichnung": "Kreditoren"}
			if c.nullPrefix {
				in["praefix"] = nil
			} else {
				in["praefix"] = c.praefix
			}
			got, err := rule.Evaluate(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			m, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("result %T %v, want a context", got, got)
			}
			if m["erlaubt"] != true || m["anzeigename"] != c.wantAnzeigename || m["alias"] != c.wantAlias {
				t.Fatalf("got erlaubt=%v anzeigename=%q alias=%q, want true %q %q", m["erlaubt"], m["anzeigename"], m["alias"], c.wantAnzeigename, c.wantAlias)
			}
		})
	}

	// The prefix never turns a refusal into a name: a reserved word stays refused.
	got, err := rule.Evaluate(ctx, map[string]any{"objekttyp": "verteiler", "slug": "admin", "bereich": "it", "bezeichnung": "admin", "praefix": "atlas-test-58o4-"})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := got.(map[string]any); m["erlaubt"] != false || m["alias"] != nil {
		t.Fatalf("a reserved word with a prefix: got %v, want erlaubt=false and no alias", got)
	}
}

// TestM365OrderFormsSearchThePersonTheyName holds the order forms of m365-dienste to
// the check the server makes when a form is saved (formlookup.Check), and to the one
// promise the processes rely on: the person a field names arrives as a user principal
// name, as the text field it replaced submitted it.
func TestM365OrderFormsSearchThePersonTheyName(t *testing.T) {
	for form, key := range map[string]string{
		"m365-person": "upn", "m365-team-bestellung": "besitzer", "m365-funktionspostfach-bestellung": "besitzer",
		"m365-verteiler-bestellung": "besitzer", "m365-postfachzugriff-bestellung": "person",
	} {
		raw, err := os.ReadFile("m365-dienste/" + form + ".form.json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Schema json.RawMessage `json:"schema"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		schema := doc.Schema
		if len(schema) == 0 {
			schema = raw
		}
		if err := formlookup.Check(schema); err != nil {
			t.Errorf("%s: the server would refuse the form: %v", form, err)
			continue
		}
		parsed, err := formlookup.Parse(schema)
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		f, ok := parsed.Field(key)
		if !ok {
			t.Errorf("%s: %q is no directory field", form, key)
			continue
		}
		if f.ValueProperty() != "userPrincipalName" {
			t.Errorf("%s: %q submits %s; the processes address people by user principal name", form, key, f.ValueProperty())
		}
	}
}
