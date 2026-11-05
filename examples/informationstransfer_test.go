package examples

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The Informationstransfer (examples/informationstransfer) moves the resources of one
// account to another, and three of its steps destroy something: the soft and the hard
// delete of the Entra ID transfer and the disconnection of the source mailbox. These
// tests hold the package to the promises its README makes about them — nothing is
// deleted without a release task immediately before it, the Entra transfer is off unless
// an operator switches it on — and hold the decision table to the rules it states, so a
// changed row that would let a forbidden combination through is caught here.

const itDir = "informationstransfer"

// itRule compiles InfotransferOptionen and evaluates it.
func itRule(t *testing.T) func(in map[string]any) []map[string]any {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(itDir, "it-optionen.dmn"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule, err := dmn.CompileStandalone(ctx, src, "InfotransferOptionen")
	if err != nil {
		t.Fatal(err)
	}
	return func(in map[string]any) []map[string]any {
		t.Helper()
		got, err := rule.Evaluate(ctx, in)
		if err != nil {
			t.Fatalf("%v: %v", in, err)
		}
		rows, ok := got.([]any)
		if !ok {
			t.Fatalf("%v: COLLECT answered %T, not a list", in, got)
		}
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.(map[string]any))
		}
		return out
	}
}

// itOrder is a valid order: an internalisation that moves the primary address within the
// same domain, in an environment where everything is found and switched on.
func itOrder(change map[string]any) map[string]any {
	in := map[string]any{
		"szenario": "internalisierung", "postfachOption": true, "mail": true, "inhalt": false, "kontingent": false,
		"telefon": false, "rechte": false, "kuerzel": false, "entra": false, "quelleDa": true, "zielDa": true,
		"quelleTransferiert": false, "gleicheDomain": true, "domainBestaetigt": false, "pfQuelleDa": true, "pfZielDa": true,
		"entraTransferAktiv": true, "kuerzelGeneriert": true, "postfachSystem": "exchange-server",
	}
	for k, v := range change {
		in[k] = v
	}
	return in
}

func itTexts(rows []map[string]any, art string) []string {
	var out []string
	for _, r := range rows {
		if r["art"] == art {
			out = append(out, r["text"].(string))
		}
	}
	return out
}

func TestTheInfotransferOptionenTableDecides(t *testing.T) {
	rule := itRule(t)
	if g := itTexts(rule(itOrder(nil)), "grund"); len(g) != 0 {
		t.Fatalf("a valid order is refused: %v", g)
	}
	for _, c := range []struct {
		name   string
		change map[string]any
		want   string // a fragment of the reason
	}{
		{"nothing chosen", map[string]any{"mail": false, "postfachOption": false}, "mindestens eine Ressource"},
		{"Entra on an externalisation", map[string]any{"entra": true, "szenario": "externalisierung"}, "nur bei einer Internalisierung"},
		{"Entra on a change of department", map[string]any{"entra": true, "szenario": "abteilungswechsel"}, "nur bei einer Internalisierung"},
		{"Entra switched off", map[string]any{"entra": true, "entraTransferAktiv": false}, "ENTRA_TRANSFER"},
		{"another domain, unconfirmed", map[string]any{"gleicheDomain": false}, "Mail-Domain"},
		{"imported short codes", map[string]any{"kuerzel": true, "kuerzelGeneriert": false}, "KUERZEL_HERKUNFT"},
		{"Entra with the phone number", map[string]any{"entra": true, "telefon": true}, "Telefonnummer"},
		{"Entra with a cloud mailbox", map[string]any{"entra": true, "postfachSystem": "exchange-online"}, "Exchange Online"},
		{"source without mailbox", map[string]any{"pfQuelleDa": false}, "Postfach der Quelle"},
		{"target without mailbox", map[string]any{"pfZielDa": false}, "Postfach des Ziels"},
		{"already transferred", map[string]any{"quelleTransferiert": true}, "Ressourcen transferiert"},
		{"same account", map[string]any{"zielDa": false}, "dasselbe Konto"},
		{"unknown scenario", map[string]any{"szenario": "umzug"}, "Szenario ist unbekannt"},
		{"no scenario", map[string]any{"szenario": nil}, "Szenario ist unbekannt"},
	} {
		g := itTexts(rule(itOrder(c.change)), "grund")
		if !slices.ContainsFunc(g, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a reason with %q, got %v", c.name, c.want, g)
		}
	}
	// Allowed with an explicit confirmation, and said so in the approval.
	rows := rule(itOrder(map[string]any{"gleicheDomain": false, "domainBestaetigt": true}))
	if g := itTexts(rows, "grund"); len(g) != 0 {
		t.Errorf("a confirmed transfer across domains is refused: %v", g)
	}
	if h := itTexts(rows, "hinweis"); !slices.ContainsFunc(h, func(s string) bool { return strings.Contains(s, "ausdrücklich bestätigt") }) {
		t.Errorf("the approval is not told the domains differ: %v", h)
	}
	// Combined address and content: no prefix on the source.
	h := itTexts(rule(itOrder(map[string]any{"inhalt": true})), "hinweis")
	if !slices.ContainsFunc(h, func(s string) bool { return strings.Contains(s, "kein Präfix") }) {
		t.Errorf("the combined variant does not say the source keeps no prefix: %v", h)
	}
	// The Entra transfer of an internalisation, switched on, is allowed — and announced.
	rows = rule(itOrder(map[string]any{"mail": false, "postfachOption": false, "entra": true}))
	if g := itTexts(rows, "grund"); len(g) != 0 {
		t.Errorf("an internalisation's Entra transfer is refused: %v", g)
	}
	if h := itTexts(rows, "hinweis"); !slices.ContainsFunc(h, func(s string) bool { return strings.Contains(s, "endgültig") }) {
		t.Errorf("the approval is not told an account is deleted for good: %v", h)
	}
}

// itDefs is what TestNothingIsDeletedWithoutARelease reads of a model.
type itDefs struct {
	Process struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"process"`
}

type itNode struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Source  string `xml:"sourceRef,attr"`
	Target  string `xml:"targetRef,attr"`
	Inner   string `xml:",innerxml"`
}

// TestNothingIsDeletedWithoutARelease: in every model of the package, each task that
// destroys something (delete-user, permanently-delete-user, disable-mailbox) is
// unreachable from the start once its release task is taken out of the graph, the
// release is a user task of the group the README names, and the only way past it is a
// flow that requires both the release and the confirmation.
func TestNothingIsDeletedWithoutARelease(t *testing.T) {
	destructive := []string{`operation="delete-user"`, `operation="permanently-delete-user"`, `operation="disable-mailbox"`}
	release := map[string]string{"entra.bpmn": "ut_frei", "inhalt.bpmn": "ut_frei"}
	group := map[string]string{"entra.bpmn": "GRUPPE_INFORMATIONSSICHERHEIT", "inhalt.bpmn": "GRUPPE_IDENTITY_MANAGEMENT"}
	files, _ := filepath.Glob(filepath.Join(itDir, "*.bpmn"))
	found := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var d itDefs
		if err := xml.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		var nodes []itNode
		dec := xml.NewDecoder(strings.NewReader("<p>" + string(d.Process.Inner) + "</p>"))
		var wrap struct {
			Nodes []itNode `xml:",any"`
		}
		if err := dec.Decode(&wrap); err != nil {
			t.Fatal(err)
		}
		nodes = wrap.Nodes
		next := map[string][]string{}
		var starts, danger []string
		byID := map[string]itNode{}
		for _, n := range nodes {
			byID[n.ID] = n
			switch {
			case n.XMLName.Local == "sequenceFlow":
				next[n.Source] = append(next[n.Source], n.Target)
			case n.XMLName.Local == "startEvent":
				starts = append(starts, n.ID)
			}
			for _, op := range destructive {
				if strings.Contains(n.Inner, op) {
					danger = append(danger, n.ID)
				}
			}
		}
		base := filepath.Base(f)
		if len(danger) == 0 {
			continue
		}
		rel, ok := release[base]
		if !ok {
			t.Errorf("%s deletes something (%v) and names no release task in this test", base, danger)
			continue
		}
		u := byID[rel]
		if u.XMLName.Local != "userTask" || !strings.Contains(u.Inner, `candidateGroups="=application.`+group[base]+`"`) {
			t.Errorf("%s: the release %s is not a user task of %s", base, rel, group[base])
		}
		// Reachable without passing the release?
		seen := map[string]bool{rel: true}
		todo := append([]string(nil), starts...)
		for len(todo) > 0 {
			id := todo[0]
			todo = todo[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			todo = append(todo, next[id]...)
		}
		for _, id := range danger {
			found++
			if seen[id] && id != rel {
				t.Errorf("%s: %s deletes and is reachable without the release %s", base, id, rel)
			}
		}
		// The gateway after the release lets through only a release that is confirmed.
		for _, n := range nodes {
			if n.XMLName.Local == "sequenceFlow" && n.Source == "gw_frei" && slices.Contains(danger, n.Target) &&
				!strings.Contains(n.Inner, `freigabe = "freigegeben" and bestaetigt = true`) {
				t.Errorf("%s: %s goes on without both the release and the confirmation: %s", base, n.ID, n.Inner)
			}
		}
	}
	if found < 4 {
		t.Fatalf("found %d destructive tasks; the package has four (two soft deletes, a hard delete, a disconnection)", found)
	}
}

// itEngine deploys models of the package on a real engine.
func itEngine(t *testing.T, files ...string) (*engine.Processor, *state.Store, map[string]*compiler.CompiledProcess) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = log.Close() })
	p := engine.New(1, log, store, lzClock{})
	byID := map[string]*compiler.CompiledProcess{}
	for i, f := range files {
		src, err := os.Open(filepath.Join(itDir, f))
		if err != nil {
			t.Fatal(err)
		}
		cp, err := compiler.Parse(uint64(200+i), 1, src)
		_ = src.Close()
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		p.Deploy(cp)
		byID[cp.ProcessId()] = cp
	}
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	return p, store, byID
}

func itString(name, v string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarString, Text: v}
}

// TestTheEntraTransferStaysOffByDefault: an order that asks for the Entra transfer of an
// internalisation, run where nobody has set ENTRA_TRANSFER, ends without a single job —
// no directory read, no deletion. The constant's fallback in the model is the switch.
func TestTheEntraTransferStaysOffByDefault(t *testing.T) {
	for _, szenario := range []string{"internalisierung", "externalisierung"} {
		p, store, byID := itEngine(t, "entra.bpmn")
		p.CreateInstance(byID["proc_it_entra"].Key, itString("optEntra", "ja"), itString("variantId", szenario))
		if err := p.RunUntilIdle(); err != nil {
			t.Fatal(err)
		}
		jobs := 0
		if err := store.AllActivatableJobs(func(uint64) error { jobs++; return nil }); err != nil {
			t.Fatal(err)
		}
		done := 0
		if err := store.CompletedProcessInstances(func(uint64, *model.ProcessInstanceValue) error { done++; return nil }); err != nil {
			t.Fatal(err)
		}
		if jobs != 0 || done != 1 {
			t.Errorf("%s with ENTRA_TRANSFER unset: %d job(s), %d instance(s) completed; want none and one", szenario, jobs, done)
		}
	}
}
