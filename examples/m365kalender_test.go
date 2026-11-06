package examples

import (
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// kalenderParameters reads the parameters of the first task in kalender.bpmn that runs
// operation op, as the model ships them.
func kalenderParameters(t *testing.T, op string) map[string]any {
	t.Helper()
	src, err := os.ReadFile("m365-dienste/kalender.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`operation="` + regexp.QuoteMeta(op) + `" parameters='([^']*)'`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("no %s task in kalender.bpmn", op)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(string(m[1]))), &params); err != nil {
		t.Fatalf("%s parameters: %v", op, err)
	}
	return params
}

func evalKalender(t *testing.T, src string, vars map[string]expr.Value) string {
	t.Helper()
	c, err := expr.CompileAuto(strings.TrimPrefix(src, "="))
	if err != nil {
		t.Fatalf("compile %s: %v", src, err)
	}
	v, err := c.Eval(vars)
	if err != nil {
		t.Fatalf("eval %s: %v", src, err)
	}
	got, _ := expr.ToJSON(v)
	return got
}

// TestM365CalendarGroupsTakeTheListsAsTyped evaluates the Members the calendar's
// permission groups are created with. The lists are personal data, so the engine cannot
// loop over them; the Exchange worker builds the member list from the answer as it was
// typed — one UPN a line, or separated by commas — and an empty answer must leave the
// parameter out (null), not send an empty -Members that New-DistributionGroup refuses.
func TestM365CalendarGroupsTakeTheListsAsTyped(t *testing.T) {
	members, _ := kalenderParameters(t, "create-distribution-group")["Members"].(string)
	if members == "" {
		t.Fatal("create-distribution-group carries no Members expression")
	}
	ebene := func(key string) expr.Value {
		return expr.ContextOf(map[string]expr.Value{"key": expr.String(key)})
	}
	cases := []struct {
		name, ebene string
		leser       expr.Value
		bearbeiter  expr.Value
		want        string
	}{
		{"lines with spaces", "lesen", expr.String(" anna@contoso.ch\r\n\nbeat@contoso.ch ;claudia@contoso.ch\n"), expr.Null,
			`["anna@contoso.ch","beat@contoso.ch","claudia@contoso.ch"]`},
		{"the other list", "bearbeiten", expr.String("anna@contoso.ch"), expr.String("dora@contoso.ch, eva@contoso.ch"),
			`["dora@contoso.ch","eva@contoso.ch"]`},
		{"not answered", "lesen", expr.Null, expr.String("dora@contoso.ch"), "null"},
		{"only blanks", "bearbeiten", expr.Null, expr.String(" \n "), "null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalKalender(t, members, map[string]expr.Value{"ebene": ebene(c.ebene), "leser": c.leser, "bearbeiter": c.bearbeiter})
			if got != c.want {
				t.Errorf("Members = %s, want %s", got, c.want)
			}
		})
	}
}

// TestM365CalendarPermissionNamesTheCalendarFolder evaluates the folder identity the
// permissions are granted on: the mailbox's address, a colon, a backslash and the name
// of its default calendar, which depends on the mailbox's language.
func TestM365CalendarPermissionNamesTheCalendarFolder(t *testing.T) {
	identity, _ := kalenderParameters(t, "add-folder-permission")["Identity"].(string)
	folders := expr.FromJSON([]any{
		map[string]any{"Name": "Geburtstage", "FolderType": "User Created"},
		map[string]any{"Name": "Kalender", "FolderType": "Calendar"},
	})
	got := evalKalender(t, identity, map[string]expr.Value{
		"regel":          expr.ContextOf(map[string]expr.Value{"alias": expr.String("atlas-test-58o4-kal-ferienplan")}),
		"application":    expr.ContextOf(map[string]expr.Value{"MAIL_DOMAIN": expr.String("contoso.ch")}),
		"kalenderOrdner": folders,
	})
	if want := `"atlas-test-58o4-kal-ferienplan@contoso.ch:\\Kalender"`; got != want {
		t.Errorf("Identity = %s, want %s", got, want)
	}
}

// TestM365CalendarKeepsThePeopleOutOfTheEngine pins the design the process documents:
// the lists of people are declared personal, and the only collections the engine loops
// over are the literal list of levels and the groups found by their marking.
func TestM365CalendarKeepsThePeopleOutOfTheEngine(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/kalender.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	deployables, err := compiler.ParseAll(1, 1, strings.NewReader(string(src)))
	if err != nil {
		t.Fatalf("kalender.bpmn does not deploy: %v", err)
	}
	cp := deployables[0].Process
	for _, v := range []string{"besitzer", "leser", "bearbeiter", "person"} {
		if !cp.IsPersonal(v) {
			t.Errorf("%s is not declared personal: %v", v, cp.PersonalVariables())
		}
	}
	// Looping over a list of people is what the compiler must refuse.
	broken := strings.Replace(string(src), `inputCollection="=gruppen"`, `inputCollection="=leser"`, 1)
	if broken == string(src) {
		t.Fatal("anchor inputCollection=\"=gruppen\" not found; the test no longer checks what it claims")
	}
	if _, err := compiler.ParseAll(1, 1, strings.NewReader(broken)); err == nil {
		t.Error("a multi-instance over leser was accepted; the personal-data rule does not bite on this model")
	}
}
