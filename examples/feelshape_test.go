package examples

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// What a FEEL expression in a shipped model actually produces.
//
// `TestShippedModelsCompile` proves the models parse and their expressions
// compile. That is a weaker claim than it sounds, and the gap is where the real
// defects live: `append(a, b)` compiles and appends the whole list `b` as **one
// element**, and `split("de, fr", ",")` compiles and yields `[ "de", " fr" ]` with
// the space still on it. Both are valid FEEL doing the wrong thing quietly — the
// catalogue receives nested edges it cannot read, and a keyword nobody will ever
// match. Neither fails until somebody runs the process against a real server and
// reads the result carefully.
//
// So this evaluates the expressions out of the model itself, against variables
// shaped like the ones the process carries, and states what must come back. It
// covers the shapes that were wrong once; a model edited back to `append` or to an
// untrimmed `split` fails here rather than in somebody's catalogue.

type feelDefs struct {
	Process struct {
		ServiceTasks []struct {
			ID                string `xml:"id,attr"`
			ExtensionElements struct {
				IoMapping struct {
					Inputs []struct {
						Source string `xml:"source,attr"`
						Target string `xml:"target,attr"`
					} `xml:"input"`
				} `xml:"ioMapping"`
			} `xml:"extensionElements"`
		} `xml:"serviceTask"`
	} `xml:"process"`
}

// bodyInputs reads one service task's ioMapping inputs out of a model, so the
// test evaluates what ships rather than a copy of it that can drift.
//
// All of them, in order, because the mappings *are* the request body: a REST
// connector task sends its activity-local scope (ADR-0174), one JSON key per
// mapping. This used to read a single input targeting "body", from the days these
// were plain service tasks whose whole payload was one expression — a shape that
// could never be sent, because the scope would have nested it one level deep
// under "body".
func bodyInputs(t *testing.T, path, taskID string) [][2]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var defs feelDefs
	if err := xml.Unmarshal(data, &defs); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, st := range defs.Process.ServiceTasks {
		if st.ID != taskID {
			continue
		}
		out := make([][2]string, 0, len(st.ExtensionElements.IoMapping.Inputs))
		for _, in := range st.ExtensionElements.IoMapping.Inputs {
			out = append(out, [2]string{in.Target, in.Source})
		}
		if len(out) == 0 {
			t.Fatalf("%s: service task %q maps no inputs, so it sends an empty body", path, taskID)
		}
		return out
	}
	t.Fatalf("%s has no service task %q", path, taskID)
	return nil
}

func TestTheCaptureProcessBuildsTheShapesTheCatalogueExpects(t *testing.T) {
	const model = "produkt-erfassung/produkt-erfassung.bpmn"

	vars := map[string]expr.Value{
		"katalogId":          expr.FromJSON("cat_1"),
		"produktId":          expr.FromJSON("arbeitsplatz"),
		"nameDe":             expr.FromJSON("Arbeitsplatz"),
		"nameFr":             expr.FromJSON("Poste de travail"),
		"beschreibungDe":     expr.FromJSON("Notebook, Dock und Lizenzen."),
		"beschreibungFr":     expr.FromJSON("Portable, station et licences."),
		"kategorie":          expr.FromJSON("Arbeitsplatz"),
		"produktgruppe":      expr.FromJSON("Hardware"),
		"suchbegriffe":       expr.FromJSON("Laptop, mobiles Gerät ,,M365"),
		"genehmigungsart":    expr.FromJSON("superior"),
		"genehmigungsref":    expr.FromJSON(""),
		"provisionProzess":   expr.FromJSON("prov-arbeitsplatz"),
		"deprovisionProzess": expr.FromJSON("deprov-arbeitsplatz"),
		"mehrfach":           expr.FromJSON(false),
		"preis":              expr.FromJSON("CHF 1'200.–"),
		"maxTage":            expr.FromJSON(float64(365)),
		"integraleTeile":     expr.FromJSON([]any{"notebook", "dock"}),
		"optionaleTeile":     expr.FromJSON([]any{"zweitbildschirm"}),
		"katalogJetzt": expr.FromJSON(map[string]any{
			"revision": float64(7),
			"items":    []any{"vpn", "arbeitsplatz"},
			"edges": []any{
				map[string]any{"from": "vpn", "to": "token", "kind": "composition"},
			},
		}),
	}

	eval := func(src string) any {
		t.Helper()
		c, err := expr.CompileAuto(trimFeel(src))
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		got, err := c.Eval(vars)
		if err != nil {
			t.Fatalf("eval %q: %v", src, err)
		}
		return goValue(t, got)
	}

	// The request body as the connector assembles it: every input mapping
	// evaluated, keyed by what it targets.
	bodyOf := func(task string) map[string]any {
		t.Helper()
		out := map[string]any{}
		for _, in := range bodyInputs(t, model, task) {
			out[in[0]] = eval(in[1])
		}
		return out
	}

	t.Run("die Kanten sind eine flache Liste", func(t *testing.T) {
		body := bodyOf("katalog_schreiben")
		edges, ok := body["edges"].([]any)
		if !ok {
			t.Fatalf("edges is %T, not a list", body["edges"])
		}
		// Three appended to the one already there. The defect this states against
		// produced two: the catalogue's own, and the whole batch as one element.
		if len(edges) != 4 {
			t.Fatalf("edges has %d entries, want 4 — a nested list means append was used where concatenate is meant: %#v", len(edges), edges)
		}
		for i, e := range edges {
			m, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("edge %d is %T, not an edge — the list is nested", i, e)
			}
			if m["from"] == nil || m["to"] == nil || m["kind"] == nil {
				t.Errorf("edge %d is missing a field: %#v", i, m)
			}
		}
		// And the existing edge survived, which is the other half of not losing
		// somebody else's work.
		if first := edges[0].(map[string]any); first["from"] != "vpn" {
			t.Errorf("the catalogue's own edge is not first: %#v", edges[0])
		}
		// The item list carries the product once, not twice.
		items := body["items"].([]any)
		var seen int
		for _, it := range items {
			if it == "arbeitsplatz" {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("the product appears %d times in items, want once: %#v", seen, items)
		}
		// The revision travels, or the write is the silent overwrite the whole
		// read-first step exists to prevent.
		if body["revision"] != float64(7) {
			t.Errorf("the revision did not travel: %#v", body["revision"])
		}
	})

	t.Run("die Suchbegriffe sind beschnitten und ohne Leereintraege", func(t *testing.T) {
		body := bodyOf("entwurf_sichern")
		got, _ := body["keywords"].([]any)
		want := []any{"Laptop", "mobiles Gerät", "M365"}
		if len(got) != len(want) {
			t.Fatalf("keywords = %#v, want %#v — split leaves the space after a comma", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("keyword %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("der Entwurf traegt jedes erfasste Feld", func(t *testing.T) {
		// A save is a full replace, so a field the expression forgets is a field
		// this step deletes. Both bodies are checked, because the second one is
		// written from the first by hand and that is exactly how one drifts.
		for _, task := range []string{"entwurf_sichern", "aktiv_setzen"} {
			body := bodyOf(task)
			for _, key := range []string{
				"id", "homeCatalog", "state", "texts", "descriptions", "category",
				"productGroup", "keywords", "approval", "provisionProcess",
				"deprovisionProcess", "multipleAllowed", "price", "maxDays",
			} {
				if _, ok := body[key]; !ok {
					t.Errorf("%s writes no %q — a full replace would clear it", task, key)
				}
			}
			texts := body["texts"].(map[string]any)
			descs := body["descriptions"].(map[string]any)
			if texts["de"] == nil || texts["fr"] == nil {
				t.Errorf("%s loses a name: %#v", task, texts)
			}
			if descs["de"] == nil || descs["fr"] == nil {
				t.Errorf("%s loses a description: %#v", task, descs)
			}
		}
		draft := bodyOf("entwurf_sichern")
		active := bodyOf("aktiv_setzen")
		if draft["state"] != "draft" || active["state"] != "active" {
			t.Errorf("the two saves do not carry the two states: %v and %v", draft["state"], active["state"])
		}
	})
}

// trimFeel drops the leading "=" a zeebe expression carries; the compiler here
// takes the expression itself.
func trimFeel(src string) string {
	if len(src) > 0 && src[0] == '=' {
		return src[1:]
	}
	return src
}

// goValue turns a FEEL value into plain Go through the JSON the engine would
// write, so the assertions read as the catalogue would receive it.
func goValue(t *testing.T, v expr.Value) any {
	t.Helper()
	text, ok := expr.ToJSON(v)
	if !ok {
		t.Fatalf("value is not representable as JSON: %#v", v)
	}
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return out
}
