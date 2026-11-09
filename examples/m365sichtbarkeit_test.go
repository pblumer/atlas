package examples

import (
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// m365Read returns a file of examples/m365-dienste as text.
func m365Read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("m365-dienste/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// m365Feel compiles the FEEL expression the pattern's first group captures in src, the
// way the engine reads it out of the XML attribute.
func m365Feel(t *testing.T, src, pattern string) *expr.Compiled {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no match for %s", pattern)
	}
	c, err := expr.CompileAuto(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatalf("%s: %v", m[1], err)
	}
	return c
}

func m365JSON(t *testing.T, text string) expr.Value {
	t.Helper()
	v, err := expr.ParseJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestM365VisibilityActionIsDeclaredAndModelled holds the action «Sichtbarkeit ändern» of
// m365-team and m365-sharepoint-site together: the catalogue declares it as a change the
// holder and an operator may ask for, with the form that asks for the new visibility; the
// lifecycle process starts on its message, sets visibility through update-group on the
// group it found and answers the action with completed or rejected.
func TestM365VisibilityActionIsDeclaredAndModelled(t *testing.T) {
	var cat struct {
		Products []struct {
			ID               string
			LifecycleProcess string
			Actions          []struct {
				Key, Message, Effect, Form string
				Triggers                   []string
			}
		}
	}
	if err := json.Unmarshal([]byte(m365Read(t, "katalog.json")), &cat); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"m365-team": "team.bpmn", "m365-sharepoint-site": "sharepoint-site.bpmn"}
	messages := map[string]string{"m365-team": "m365.team.sichtbarkeit", "m365-sharepoint-site": "m365.site.sichtbarkeit"}
	found := 0
	for _, p := range cat.Products {
		file, ok := files[p.ID]
		if !ok {
			continue
		}
		for _, a := range p.Actions {
			if a.Key != "sichtbarkeit" {
				continue
			}
			found++
			if a.Message != messages[p.ID] || a.Effect != "change" || a.Form != "m365-sichtbarkeit" ||
				strings.Join(a.Triggers, ",") != "customer,operator" {
				t.Errorf("%s: action sichtbarkeit = %+v", p.ID, a)
			}
			src := m365Read(t, file)
			for _, want := range []string{
				`name="` + a.Message + `"`,
				`operation="update-group" groupId="=treffer[1].id" attributes='{"visibility": "=neueSichtbarkeit"}'`,
				`<atlas:shopTask mode="outcome" action="sichtbarkeit" outcome="completed"/>`,
				`<atlas:shopTask mode="outcome" action="sichtbarkeit" outcome="rejected"/>`,
				`sichtbarkeitBegruendung"`,
			} {
				if !strings.Contains(src, want) {
					t.Errorf("%s lacks %s", file, want)
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("found the action sichtbarkeit on %d products, want 2", found)
	}
	if s := m365Read(t, "sharepoint-site.bpmn"); !strings.Contains(s,
		`<scriptTask id="sc_alias8" name="Alias bilden">`) || !regexp.MustCompile(
		`id="sc_alias8".*?expression='=\(if application\.NAME_PREFIX = null then "" else application\.NAME_PREFIX\) \+ lower case\(url\)'`).MatchString(s) {
		t.Error("the site's visibility action does not find the group under its prefixed alias")
	}
}

// TestM365VisibilityGatewayIsFailClosed evaluates the condition both lifecycle processes
// put before update-group: one group and a visibility Graph knows, nothing else.
func TestM365VisibilityGatewayIsFailClosed(t *testing.T) {
	one := `[{"id":"g1","mailNickname":"tm-hw-alle","visibility":"Private"}]`
	two := `[{"id":"g1"},{"id":"g2"}]`
	cases := []struct {
		treffer, sicht string
		want           bool
	}{
		{one, "Public", true},
		{one, "Private", true},
		{one, "HiddenMembership", false},
		{one, "public", false},
		{"[]", "Public", false},
		{two, "Public", false},
	}
	for _, file := range []string{"team.bpmn", "sharepoint-site.bpmn"} {
		cond := m365Feel(t, m365Read(t, file),
			`<sequenceFlow id="f_gw_sicht__st_sicht"[^>]*><conditionExpression>=([^<]*)</conditionExpression>`)
		for _, c := range cases {
			v, err := cond.Eval(map[string]expr.Value{"treffer": m365JSON(t, c.treffer), "neueSichtbarkeit": expr.String(c.sicht)})
			if err != nil {
				t.Fatal(err)
			}
			if got := v == expr.Bool(true); got != c.want {
				t.Errorf("%s: treffer %s, %s: %v, want %v", file, c.treffer, c.sicht, v, c.want)
			}
		}
	}
}

// TestM365VisibilityRepairFindsOnlyTheCataloguesGroup evaluates «Sichtbarkeit nachführen»,
// the operator's way for a team or site ordered before the action existed: the alias it
// searches under carries NAME_PREFIX once and nothing that could break the OData filter,
// and the gateway changes only a single group whose description names its catalogue
// position.
func TestM365VisibilityRepairFindsOnlyTheCataloguesGroup(t *testing.T) {
	src := m365Read(t, "sichtbarkeit-nachfuehren.bpmn")
	alias := m365Feel(t, src, `<scriptTask id="sc_alias".*?expression='=([^']*)' resultVariable="gruppenAlias"/>`)
	prefix := "atlas-test-58o4-"
	for _, c := range []struct{ in, prefix, want string }{
		{"atlas-test-58o4-tm-hw-alle", prefix, "atlas-test-58o4-tm-hw-alle"},
		{"tm-hw-alle", prefix, "atlas-test-58o4-tm-hw-alle"},
		{" ATLAS-test-58o4-TM-HW-Alle ", prefix, "atlas-test-58o4-tm-hw-alle"},
		{"tm-hw-alle' or mailNickname eq 'x", "", "tm-hw-alleormailnicknameeqx"},
		{"tm-hw-alle", "", "tm-hw-alle"},
	} {
		v, err := alias.Eval(map[string]expr.Value{
			"alias":       expr.String(c.in),
			"application": expr.ContextOf(map[string]expr.Value{"NAME_PREFIX": expr.String(c.prefix)}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if v != expr.String(c.want) {
			t.Errorf("alias %q with prefix %q = %v, want %q", c.in, c.prefix, v, c.want)
		}
	}
	cond := m365Feel(t, src, `<sequenceFlow id="f_gw_gef__st_sicht"[^>]*><conditionExpression>=([^<]*)</conditionExpression>`)
	ours := `[{"id":"g1","description":"Alle der HW | Katalog-Position o-1/p-1"}]`
	for _, c := range []struct {
		treffer, sicht string
		want           bool
	}{
		{ours, "Public", true},
		{`[{"id":"g1","description":"Projekt | SharePoint-Site | Katalog-Position o-2/p-1"}]`, "Private", true},
		{`[{"id":"g1","description":"von Hand angelegt"}]`, "Public", false},
		{`[{"id":"g1"}]`, "Public", false},
		{ours, "Hidden", false},
		{"[]", "Public", false},
	} {
		v, err := cond.Eval(map[string]expr.Value{"treffer": m365JSON(t, c.treffer), "neueSichtbarkeit": expr.String(c.sicht)})
		if err != nil {
			t.Fatal(err)
		}
		if got := v == expr.Bool(true); got != c.want {
			t.Errorf("treffer %s, %s: %v, want %v", c.treffer, c.sicht, v, c.want)
		}
	}
}
