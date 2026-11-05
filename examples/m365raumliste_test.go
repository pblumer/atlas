package examples

import (
	"context"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/expr"
)

// TestM365RoomListFollowsTheLocation evaluates what «Sitzungszimmer auffindbar machen»
// (raumliste.bpmn) derives from an ordered location, the way the engine compiles it: the
// script that splits «Bern, 5. Stock» into city, floor label and floor, the slug the
// naming rule reads, and the room list name M365Namensregel builds from them, with and
// without NAME_PREFIX. Room Finder filters by city and floor and lists rooms through
// room lists, so a wrong split is a room nobody finds.
func TestM365RoomListFollowsTheLocation(t *testing.T) {
	src, err := os.ReadFile("m365-dienste/raumliste.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	script := regexp.MustCompile(`<scriptTask id="sc_lage".*?expression='=([^']*)' resultVariable="lage"/>`).FindSubmatch(src)
	if script == nil {
		t.Fatal("no sc_lage script in raumliste.bpmn")
	}
	lage, err := expr.CompileAuto(html.UnescapeString(string(script[1])))
	if err != nil {
		t.Fatal(err)
	}
	slugSrc := regexp.MustCompile(`<zeebe:input source='=([^']*)' target="slug"/>`).FindSubmatch(src)
	if slugSrc == nil {
		t.Fatal("no slug input on br_rl")
	}
	slug, err := expr.CompileAuto(html.UnescapeString(string(slugSrc[1])))
	if err != nil {
		t.Fatal(err)
	}
	dmnSrc, err := os.ReadFile("m365-dienste/m365-namensregel.dmn")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule, err := dmn.CompileStandalone(ctx, dmnSrc, "M365Namensregel")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		standort                   expr.Value
		prefix                     string
		ort, label, etage          string // JSON of each part
		erlaubt                    bool
		wantAnzeigename, wantAlias string
	}{
		{standort: expr.String("Bern, 5. Stock"), prefix: "atlas-test-58o4-", ort: `"Bern"`, label: `"5. Stock"`, etage: "5",
			erlaubt: true, wantAnzeigename: "atlas-test-58o4-RL-Bern", wantAlias: "atlas-test-58o4-rl-bern"},
		{standort: expr.String("  Zürich ,  EG "), ort: `"Zürich"`, label: `"EG"`, etage: "0",
			erlaubt: true, wantAnzeigename: "RL-Zürich", wantAlias: "rl-zuerich"},
		{standort: expr.String("St. Gallen, Haus B, 12. OG"), ort: `"St. Gallen"`, label: `"Haus B, 12. OG"`, etage: "12",
			erlaubt: true, wantAnzeigename: "RL-St. Gallen", wantAlias: "rl-st-gallen"},
		{standort: expr.String("Basel"), ort: `"Basel"`, label: "null", etage: "null",
			erlaubt: true, wantAnzeigename: "RL-Basel", wantAlias: "rl-basel"},
		{standort: expr.String("Luzern, Dachgeschoss"), ort: `"Luzern"`, label: `"Dachgeschoss"`, etage: "null",
			erlaubt: true, wantAnzeigename: "RL-Luzern", wantAlias: "rl-luzern"},
		// No city: no room list, and Set-Place leaves City out.
		{standort: expr.Null, ort: "null", label: "null", etage: "null"},
		{standort: expr.String(", 3. Stock"), ort: "null", label: `"3. Stock"`, etage: "3"},
	}
	for _, c := range cases {
		name, _ := expr.ToJSON(c.standort)
		t.Run(name, func(t *testing.T) {
			v, err := lage.Eval(map[string]expr.Value{"standort": c.standort})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := expr.ToJSON(v)
			want := `{"etage":` + c.etage + `,"etagenLabel":` + c.label + `,"ort":` + c.ort + `}`
			if got != want {
				t.Fatalf("lage = %s, want %s", got, want)
			}
			vars := map[string]expr.Value{"lage": v}
			s, err := slug.Eval(vars)
			if err != nil {
				t.Fatal(err)
			}
			sv, _ := expr.ToJSON(s)
			ort := ""
			if c.ort != "null" {
				ort = strings.Trim(c.ort, `"`)
			}
			in := map[string]any{"objekttyp": "raumliste", "slug": strings.Trim(sv, `"`), "bereich": "", "bezeichnung": ort, "praefix": c.prefix}
			res, err := rule.Evaluate(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			m, _ := res.(map[string]any)
			if m["erlaubt"] != c.erlaubt {
				t.Fatalf("erlaubt = %v (%v), want %v", m["erlaubt"], m["grund"], c.erlaubt)
			}
			if c.erlaubt && (m["anzeigename"] != c.wantAnzeigename || m["alias"] != c.wantAlias) {
				t.Errorf("room list %q / %q, want %q / %q", m["anzeigename"], m["alias"], c.wantAnzeigename, c.wantAlias)
			}
		})
	}
}

// TestM365RoomsReachRoomFinderOnEveryPath holds the room processes to what makes a room
// findable and a removed one gone: provisioning calls the room list process, the repair
// calls it once per room and one room after the other (two new rooms of one city must not
// both create its room list), the return takes the room out of its room lists before it
// removes the mailbox, and Set-Place runs only while ORTSANGABEN is true — the Exchange
// Server copy of the example switches it off, because the cmdlet exists in Exchange
// Online only.
func TestM365RoomsReachRoomFinderOnEveryPath(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile("m365-dienste/" + f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	einrichten, entfernen, nachfuehren, liste := read("raum-einrichten.bpmn"), read("raum-entfernen.bpmn"), read("raum-nachfuehren.bpmn"), read("raumliste.bpmn")
	for name, src := range map[string]string{"raum-einrichten": einrichten, "raum-nachfuehren": nachfuehren} {
		if !strings.Contains(src, `<zeebe:calledElement processId="proc_m365_raumliste"`) {
			t.Errorf("%s does not call proc_m365_raumliste", name)
		}
	}
	if !strings.Contains(nachfuehren, `<multiInstanceLoopCharacteristics isSequential="true">`) {
		t.Error("the repair does not call the room list process one room after the other")
	}
	if i, j := strings.Index(entfernen, `operation="remove-distribution-group-member"`), strings.Index(entfernen, `operation="remove-mailbox"`); i < 0 || j < 0 || i > j {
		t.Error("the return does not take the room out of its room lists before it removes the mailbox")
	}
	for _, want := range []string{`operation="create-room-list"`, `operation="add-distribution-group-member"`, `operation="set-place"`,
		`<conditionExpression>=application.ORTSANGABEN = true</conditionExpression>`, `name="ORTSANGABEN" type="json" source="application" value="true"`} {
		if !strings.Contains(liste, want) {
			t.Errorf("raumliste.bpmn lacks %s", want)
		}
	}
}
