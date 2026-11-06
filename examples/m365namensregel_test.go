package examples

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/expr"
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
		{name: "shared tenant device", objekttyp: "geraet", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-GE-FIN Kreditoren", wantAlias: "atlas-test-58o4-ge-fin-kreditoren"},
		{name: "shared tenant community", objekttyp: "community", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-EC-FIN Kreditoren", wantAlias: "atlas-test-58o4-ec-fin-kreditoren"},
		{name: "shared tenant site", objekttyp: "site", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-Kreditoren", wantAlias: "atlas-test-58o4-kreditoren"},
		{name: "site without prefix", objekttyp: "site", nullPrefix: true,
			wantAnzeigename: "Kreditoren", wantAlias: "kreditoren"},
		{name: "shared tenant calendar", objekttyp: "kalender", praefix: "atlas-test-58o4-",
			wantAnzeigename: "atlas-test-58o4-KAL-Kreditoren", wantAlias: "atlas-test-58o4-kal-kreditoren"},
		{name: "calendar without prefix", objekttyp: "kalender", nullPrefix: true,
			wantAnzeigename: "KAL-Kreditoren", wantAlias: "kal-kreditoren"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bereich := "fin"
			if c.objekttyp == "kalender" {
				bereich = "" // a calendar has no department: the process hands ""
			}
			in := map[string]any{"objekttyp": c.objekttyp, "slug": "kreditoren", "bereich": bereich, "bezeichnung": "Kreditoren"}
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
		"m365-kalender-bestellung": "besitzer",
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

// TestM365PartnerAccountNamesAreTrimmedAndPrefixed evaluates the attributes the partner
// process hands to create-user: the public form takes names as they were typed, so a
// leading or trailing space must not reach Entra, and the display name carries
// NAME_PREFIX like the UPN, so the test environment's cleanup finds the account.
func TestM365PartnerAccountNamesAreTrimmedAndPrefixed(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/partnerzugang.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`operation="create-user" attributes='([^']*)'`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no create-user task in partnerzugang.bpmn")
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(string(m[1]))), &attrs); err != nil {
		t.Fatalf("attributes: %v", err)
	}
	vars := map[string]expr.Value{
		"vorname":     expr.String("  Max"),
		"nachname":    expr.String("Partner "),
		"firma":       expr.String(" Muster AG "),
		"email":       expr.String("max.partner@muster-ag.example"),
		"application": expr.ContextOf(map[string]expr.Value{"NAME_PREFIX": expr.String("atlas-test-ab12-"), "PARTNER_UPN_DOMAIN": expr.String("contoso.onmicrosoft.com")}),
	}
	want := map[string]string{
		"displayName":       "atlas-test-ab12-Max Partner (Muster AG)",
		"givenName":         "Max",
		"surname":           "Partner",
		"companyName":       "Muster AG",
		"userPrincipalName": "atlas-test-ab12-ext.max.partner.muster-ag@contoso.onmicrosoft.com",
	}
	for name, w := range want {
		s, _ := attrs[name].(string)
		c, err := expr.CompileAuto(strings.TrimPrefix(s, "="))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v, err := c.Eval(vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, _ := expr.ToJSON(v); got != strconv.Quote(w) {
			t.Errorf("%s = %s, want %q", name, got, w)
		}
	}
}

// TestM365GovernanceCountsTeamsOutsideTheNamingRule evaluates the governance check's
// count the way the engine compiles it (expr.CompileAuto): a Microsoft 365 group whose
// alias does not follow the naming rule is a deviation, with and without NAME_PREFIX.
// Against the shared test tenant the comparison stood in a filter, where
// application.NAME_PREFIX read null, and the check reported no deviation at all.
func TestM365GovernanceCountsTeamsOutsideTheNamingRule(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/governance-pruefung.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<scriptTask id="sc_zahl".*?expression='([^']*)'`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no sc_zahl in governance-pruefung.bpmn")
	}
	c, err := expr.CompileAuto(strings.TrimPrefix(html.UnescapeString(string(m[1])), "="))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		prefix string
		groups []any
		want   string
	}{
		{"atlas-test-ab12-", []any{
			map[string]any{"mailNickname": "atlas-test-ab12-tm-acc-projekt"},
			map[string]any{"mailNickname": "atlas-test-ab12-projektraum"},
		}, "1"},
		{"", []any{
			map[string]any{"mailNickname": "tm-fin-budget"},
			map[string]any{"mailNickname": "marketing"},
			map[string]any{"mailNickname": "tm-x-zu-kurz"},
		}, "2"},
	} {
		v, err := c.Eval(map[string]expr.Value{
			"postfaecherOhne": expr.FromJSON([]any{}), "verteilerOhne": expr.FromJSON([]any{}),
			"gruppen":     expr.FromJSON(tc.groups),
			"application": expr.ContextOf(map[string]expr.Value{"NAME_PREFIX": expr.String(tc.prefix)}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := expr.ToJSON(v); got != tc.want {
			t.Errorf("prefix %q: %s deviations, want %s", tc.prefix, got, tc.want)
		}
	}
}

// TestM365SiteActionsFindTheSiteUnderItsPrefixedAlias evaluates the alias every change
// action of the SharePoint team site builds from the ordered URL. Provisioning takes the
// alias from the naming rule, which puts NAME_PREFIX in front; an action that searched
// for the bare URL found neither the group nor the inventory entry on a shared tenant, so
// a deletion deleted nothing and stopped on an incident.
func TestM365SiteActionsFindTheSiteUnderItsPrefixedAlias(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/sharepoint-site.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<scriptTask id="(sc_alias[2-7])"[^>]*>.*?<zeebe:script expression='=([^']*)' resultVariable="alias"/>`).FindAllSubmatch(src, -1)
	if len(m) != 6 {
		t.Fatalf("found %d alias scripts of change actions, want 6", len(m))
	}
	cases := []struct {
		prefix expr.Value
		want   string
	}{
		{expr.String("atlas-test-ab12-"), "atlas-test-ab12-tratsch-share"},
		{expr.String(""), "tratsch-share"},
		{expr.Null, "tratsch-share"},
	}
	for _, tc := range cases {
		prefix, want := tc.prefix, tc.want
		vars := map[string]expr.Value{
			"url":         expr.String("Tratsch-Share"),
			"application": expr.ContextOf(map[string]expr.Value{"NAME_PREFIX": prefix}),
		}
		for _, x := range m {
			c, err := expr.CompileAuto(html.UnescapeString(string(x[2])))
			if err != nil {
				t.Fatalf("%s: %v", x[1], err)
			}
			v, err := c.Eval(vars)
			if err != nil {
				t.Fatalf("%s: %v", x[1], err)
			}
			if got, _ := expr.ToJSON(v); got != strconv.Quote(want) {
				t.Errorf("%s with NAME_PREFIX %v: alias = %s, want %q", x[1], prefix, got, want)
			}
		}
	}
}
