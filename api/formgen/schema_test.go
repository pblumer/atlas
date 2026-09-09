package formgen

import (
	"encoding/json"
	"github.com/pblumer/atlas/limits"
	"strings"
	"testing"
)

// components is a small reader for the assertions below.
func components(t *testing.T, s map[string]any) []map[string]any {
	t.Helper()
	raw, ok := s["components"].([]any)
	if !ok {
		t.Fatalf("schema has no components array: %+v", s)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("component is not an object: %#v", c)
		}
		out = append(out, m)
	}
	return out
}

// A model answers in prose habits: a sentence of introduction, a fenced code block, a
// closing offer to adjust it. None of that is a failure of the generation — the document
// is in there — and refusing it would fail a good form over its wrapping.
func TestSchemaFromReadsTheDocumentOutOfWhateverItCameWrappedIn(t *testing.T) {
	for _, tc := range []struct{ name, answer string }{
		{"bare", `{"type":"default","components":[{"type":"textfield","key":"name","label":"Name"}]}`},
		{"fenced", "```json\n{\"type\":\"default\",\"components\":[{\"type\":\"textfield\",\"key\":\"name\",\"label\":\"Name\"}]}\n```"},
		{"chatty", "Hier ist das Formular:\n\n{\"type\":\"default\",\"components\":[{\"type\":\"textfield\",\"key\":\"name\",\"label\":\"Name\"}]}\n\nSag Bescheid, wenn du Felder ergänzen willst."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SchemaFrom(tc.answer, "urlaub", limits.Default().Asset)
			if err != nil {
				t.Fatalf("SchemaFrom: %v", err)
			}
			if c := components(t, got); len(c) != 1 || c[0]["key"] != "name" {
				t.Errorf("components = %+v", c)
			}
		})
	}
}

// The form's identity is the author's, not the model's: the editor is already holding a
// form under an id a user task may bind, and a generated document that renamed it would
// silently unbind that task on the next save (ADR-0222).
func TestSchemaFromKeepsTheFormsOwnIdentity(t *testing.T) {
	got, err := SchemaFrom(`{"type":"custom","id":"was-das-modell-sich-ausdachte","components":[]}`, "urlaub-pruefen", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	if got["id"] != "urlaub-pruefen" {
		t.Errorf("id = %v, want the id the editor is holding", got["id"])
	}
	if got["type"] != "default" {
		t.Errorf("root type = %v, want form-js's only root type", got["type"])
	}
}

// Every input needs a key: it is the process variable the answer lands in, and form-js
// drops a component that has none. A label is what the model reliably produces, so the
// key is derived from it rather than the generation being failed over it.
func TestSchemaFromGivesEveryInputAUsableKey(t *testing.T) {
	got, err := SchemaFrom(`{"components":[
		{"type":"textfield","label":"Vorname des Antragstellers"},
		{"type":"textfield","label":"Vorname des Antragstellers"},
		{"type":"checkbox"}
	]}`, "f", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	c := components(t, got)
	first, _ := c[0]["key"].(string)
	second, _ := c[1]["key"].(string)
	third, _ := c[2]["key"].(string)
	if first == "" || strings.ContainsAny(first, " äöü.") {
		t.Errorf("key = %q, want a technical name derived from the label", first)
	}
	if second == first || second == "" {
		t.Errorf("keys %q and %q collide; the second field would overwrite the first", first, second)
	}
	if third == "" {
		t.Errorf("a labelless input got no key at all")
	}
}

// A key the model wrote is left alone. Rewriting it would break the one thing the
// process outline was handed to the model for: naming a field the way the process
// already names that datum.
func TestSchemaFromLeavesAuthoredKeysAlone(t *testing.T) {
	got, err := SchemaFrom(`{"components":[{"type":"number","key":"urlaubstage","label":"Tage"}]}`, "f", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	if k := components(t, got)[0]["key"]; k != "urlaubstage" {
		t.Errorf("key = %v, want the name the process uses", k)
	}
}

// Layout components hold components, and an unkeyed field two levels down is as broken
// as one at the top.
func TestSchemaFromWalksIntoGroups(t *testing.T) {
	got, err := SchemaFrom(`{"components":[
		{"type":"group","label":"Adresse","components":[{"type":"textfield","label":"Straße"}]}
	]}`, "f", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	inner, ok := components(t, got)[0]["components"].([]any)
	if !ok || len(inner) != 1 {
		t.Fatalf("group lost its children: %+v", got)
	}
	if k, _ := inner[0].(map[string]any)["key"].(string); k == "" {
		t.Errorf("a field inside a group got no key: %+v", inner[0])
	}
}

// The vocabulary is a subset of form-js on purpose: what renders in a task form with no
// further wiring. A component outside it is refused by name rather than dropped, because
// a form quietly missing the field the author asked for is worse than one that says it
// could not be written.
func TestSchemaFromRefusesWhatATaskFormCannotRender(t *testing.T) {
	_, err := SchemaFrom(`{"components":[{"type":"iframe","url":"https://example.invalid"}]}`, "f", limits.Default().Asset)
	if err == nil {
		t.Fatal("an iframe was accepted into a generated form")
	}
	if !strings.Contains(err.Error(), "iframe") {
		t.Errorf("err = %v, want it to name the component it refused", err)
	}
}

// What a model can fail at, it eventually will. Each of these is a distinct failure and
// each has to reach the author as a sentence rather than as a broken editor.
func TestSchemaFromRefusesWhatIsNotAForm(t *testing.T) {
	for _, tc := range []struct{ name, answer, want string }{
		{"no JSON at all", "Ich kann dieses Formular nicht erstellen.", "no JSON"},
		{"not an object", `["textfield"]`, "no JSON"},
		{"no components", `{"type":"default"}`, "components"},
		{"components not a list", `{"components":{"type":"textfield"}}`, "components"},
		{"a component that is not an object", `{"components":["textfield"]}`, "component 1"},
		{"a component with no type", `{"components":[{"key":"x"}]}`, "type"},
		{"truncated", `{"type":"default","components":[{"type":"textfi`, "no JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SchemaFrom(tc.answer, "f", limits.Default().Asset)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The store caps a form at 1 MiB and the browser has to render whatever this returns. A
// model that loops — the same field a thousand times — is a real failure mode, and the
// place to stop it is before it reaches either.
func TestSchemaFromRefusesARunawayDocument(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"components":[`)
	for i := 0; i < 20000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"textfield","key":"feld","label":"Ein ziemlich langes Label für ein Feld"}`)
	}
	b.WriteString(`]}`)
	if _, err := SchemaFrom(b.String(), "f", limits.Default().Asset); err == nil {
		t.Fatal("a runaway document was accepted")
	}
}

// The result goes into a JSON response and then into the store, so it has to survive the
// round trip it is about to make.
func TestSchemaFromProducesSomethingSerializable(t *testing.T) {
	got, err := SchemaFrom(`{"components":[{"type":"text","text":"# Urlaubsantrag"},
		{"type":"select","key":"art","label":"Art","values":[{"label":"Erholung","value":"erholung"}]}]}`, "urlaub", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "Erholung") {
		t.Errorf("the select lost its options: %s", raw)
	}
}

// A key is a name somebody will read in a variable list, in a FEEL expression and in an
// export, for as long as the process lives. Dropping the letters an ASCII keyboard lacks
// would spell "Übergabe" as "bergabe", which nobody would have chosen.
func TestKeysAreTransliteratedNotStripped(t *testing.T) {
	for _, tc := range []struct{ label, want string }{
		{"Übergabe", "uebergabe"},
		{"Grund für den Antrag", "grundFuerDenAntrag"},
		{"Straße", "strasse"},
		{"Prénom", "prenom"},
		{"2. Vorname", "vorname"},
		{"€ %%%", ""},
	} {
		if got := technicalName(tc.label); got != tc.want {
			t.Errorf("technicalName(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
}

// A label can be a sentence. A key that is a sentence is a key nobody types twice.
func TestADerivedKeyIsBounded(t *testing.T) {
	long := strings.Repeat("Sehr langes Label ", 20)
	if got := technicalName(long); len([]rune(got)) > maxKeyRunes {
		t.Errorf("key is %d runes: %q", len([]rune(got)), got)
	}
}

// A model that opens a fence and never closes it has still written the document; the
// fence is punctuation, not structure.
func TestAnUnclosedFenceStillYieldsTheDocument(t *testing.T) {
	got, err := SchemaFrom("```json\n{\"components\":[{\"type\":\"text\",\"text\":\"hi\"}]}", "f", limits.Default().Asset)
	if err != nil {
		t.Fatalf("SchemaFrom: %v", err)
	}
	if len(components(t, got)) != 1 {
		t.Errorf("schema = %+v", got)
	}
}

// The component cap counts the whole document, not each list, or a model could nest its
// way past it.
func TestTheComponentCapCountsNestedComponents(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"components":[{"type":"group","label":"G","components":[`)
	for i := 0; i < maxComponents+10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"textfield","label":"Feld"}`)
	}
	b.WriteString(`]}]}`)
	_, err := SchemaFrom(b.String(), "f", limits.Default().Asset)
	if err == nil || !strings.Contains(err.Error(), "components") {
		t.Fatalf("err = %v, want the cap to hold inside a group too", err)
	}
}

// An error from inside a group has to say where it is, or the author is left hunting
// through a document they did not write.
func TestAFailureInsideAGroupSaysWhereItIs(t *testing.T) {
	_, err := SchemaFrom(`{"components":[{"type":"group","components":[{"type":"iframe"}]}]}`, "f", limits.Default().Asset)
	if err == nil || !strings.Contains(err.Error(), "component 1 → component 1") {
		t.Fatalf("err = %v, want the path to the component it refused", err)
	}
}

// The bound holds for a label made entirely of letters that transliterate, which an
// in-loop check on the ASCII branch alone would have walked straight past.
func TestADerivedKeyIsBoundedEvenWhenEveryLetterExpands(t *testing.T) {
	if got := technicalName(strings.Repeat("ä", 200)); len([]rune(got)) > maxKeyRunes {
		t.Errorf("key is %d runes: %q", len([]rune(got)), got)
	}
}
