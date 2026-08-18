package compiler

import (
	"errors"
	"strings"
	"testing"
)

// Documentation is BPMN's own place for prose about an element — a <bpmn:documentation>
// child every process, pool, flow node, sequence flow and data object may carry, which
// the Modeler's properties panel authors (ADR-0025). The compiler *carries* it as
// design-time metadata (so a surface such as the Tasks app can show a user task's
// documentation to the person doing the work) but never *acts* on it. Those are two
// separate guarantees, and both are pinned down here: a documented model compiles to
// exactly the graph its undocumented twin compiles to, and the prose it carries comes
// back out per element. If the parser ever started tripping over documentation — or
// quietly changing the graph because of it — these tests fail rather than a user's deploy.

// documentedModel is one model exercising every element kind the panel documents. With
// doc=true each element carries a <bpmn:documentation> child — except the "merge"
// gateway, deliberately left blank, because a real author documents what needs
// explaining and skips the obvious. With doc=false the very same model carries none;
// everything else about the two is byte-identical.
func documentedModel(doc bool) string {
	d := func(text string) string {
		if !doc {
			return ""
		}
		return "<bpmn:documentation>" + text + "</bpmn:documentation>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_done" name="fertig">` + d("Meldet die Freigabe an das Nachbarsystem.") + `</bpmn:message>
  <bpmn:process id="antrag" isExecutable="true">` + d("Bearbeitet Anträge von der Prüfung bis zur Auszahlung.") + `
    <bpmn:laneSet id="lanes">
      <bpmn:lane id="lane_sb" name="Sachbearbeitung">` + d("Alles, was die Sachbearbeitung selbst erledigt.") + `
        <bpmn:flowNodeRef>review</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:dataObjectReference id="dref" name="antrag" dataObjectRef="dobj">` + d("Der eingereichte Antrag mit allen Anlagen.") + `</bpmn:dataObjectReference>
    <bpmn:dataObject id="dobj">` + d("Fachliches Datenobjekt hinter der Referenz.") + `</bpmn:dataObject>
    <bpmn:startEvent id="start">` + d("Startet, sobald der Antrag eingeht.") + `</bpmn:startEvent>
    <bpmn:userTask id="review">` + d("Vier-Augen-Prinzip: eine zweite Person prüft.") + `</bpmn:userTask>
    <bpmn:boundaryEvent id="bnd" attachedToRef="review">` + d("Nach einer Stunde ohne Prüfung eskalieren.") + `
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT1H</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:exclusiveGateway id="split" default="f3">` + d("Genehmigt, wenn alle Unterlagen vorliegen.") + `</bpmn:exclusiveGateway>
    <bpmn:serviceTask id="pay">` + d("Zahlt über den Zahlungsdienst aus.") + `
      <bpmn:extensionElements><zeebe:taskDefinition type="payment"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:scriptTask id="calc">` + d("Rechnet die Gebühr aus.") + `
      <bpmn:extensionElements><zeebe:script expression="= 1 + 1" resultVariable="gebuehr"/></bpmn:extensionElements>
    </bpmn:scriptTask>
    <bpmn:exclusiveGateway id="merge"/>
    <bpmn:parallelGateway id="fork">` + d("Wartefrist und Nacharbeit laufen parallel.") + `</bpmn:parallelGateway>
    <bpmn:intermediateCatchEvent id="wait">` + d("Gesetzliche Wartefrist.") + `
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT30M</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:intermediateCatchEvent>
    <bpmn:subProcess id="sub">` + d("Nacharbeit, gekapselt als eigener Abschnitt.") + `
      <bpmn:startEvent id="substart">` + d("Beginn der Nacharbeit.") + `</bpmn:startEvent>
      <bpmn:endEvent id="subend">` + d("Nacharbeit erledigt.") + `</bpmn:endEvent>
      <bpmn:sequenceFlow id="fs1" sourceRef="substart" targetRef="subend"/>
    </bpmn:subProcess>
    <bpmn:parallelGateway id="join">` + d("Wartet auf beide Zweige.") + `</bpmn:parallelGateway>
    <bpmn:intermediateThrowEvent id="ping">` + d("Meldet die Auszahlung weiter.") + `
      <bpmn:messageEventDefinition id="ped" messageRef="Msg_done"/>
    </bpmn:intermediateThrowEvent>
    <bpmn:endEvent id="end">` + d("Der Antrag ist abgeschlossen.") + `</bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review">` + d("Immer.") + `</bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="split"/>
    <bpmn:sequenceFlow id="f3" sourceRef="split" targetRef="pay">` + d("Der Standardweg.") + `</bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f4" sourceRef="split" targetRef="calc">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">= gebuehrPflichtig</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f5" sourceRef="pay" targetRef="merge"/>
    <bpmn:sequenceFlow id="f6" sourceRef="calc" targetRef="merge"/>
    <bpmn:sequenceFlow id="f7" sourceRef="merge" targetRef="fork"/>
    <bpmn:sequenceFlow id="f8" sourceRef="fork" targetRef="wait"/>
    <bpmn:sequenceFlow id="f9" sourceRef="fork" targetRef="sub"/>
    <bpmn:sequenceFlow id="f10" sourceRef="wait" targetRef="join"/>
    <bpmn:sequenceFlow id="f11" sourceRef="sub" targetRef="join"/>
    <bpmn:sequenceFlow id="f12" sourceRef="join" targetRef="ping"/>
    <bpmn:sequenceFlow id="f13" sourceRef="ping" targetRef="end"/>
    <bpmn:sequenceFlow id="f14" sourceRef="bnd" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
}

// A model documented on every element compiles, and compiles to the same graph as the
// undocumented one: same nodes in the same order, with the same types, scopes, element
// ids and details, and the same flows between them.
func TestDocumentationDoesNotChangeTheCompiledGraph(t *testing.T) {
	plain, err := Parse(7, 1, strings.NewReader(documentedModel(false)))
	if err != nil {
		t.Fatalf("Parse (undocumented): %v", err)
	}
	documented, err := Parse(7, 1, strings.NewReader(documentedModel(true)))
	if err != nil {
		t.Fatalf("Parse (documented): %v", err)
	}

	// Guard the comparison against a fixture that has quietly degenerated: it must
	// still be a real graph — every element kind the panel documents, and a branch
	// carrying a compiled condition — or "the two agree" would mean nothing.
	if len(plain.nodes) < 14 {
		t.Fatalf("fixture has only %d nodes; it is meant to cover every documented element kind", len(plain.nodes))
	}
	conditioned := 0
	for i := range plain.flows {
		if plain.flows[i].Condition != nil {
			conditioned++
		}
	}
	if conditioned == 0 {
		t.Fatal("fixture has no conditional flow; it no longer exercises flow compilation")
	}

	if len(documented.nodes) != len(plain.nodes) {
		t.Fatalf("documented model has %d nodes, undocumented has %d", len(documented.nodes), len(plain.nodes))
	}
	for i := range plain.nodes {
		got, want := documented.nodes[i], plain.nodes[i]
		if got != want {
			t.Errorf("node %d (%s) differs: %+v, want %+v", i, plain.ElementBpmnId(int32(i)), got, want)
		}
		if id, wantID := documented.ElementBpmnId(int32(i)), plain.ElementBpmnId(int32(i)); id != wantID {
			t.Errorf("node %d element id = %q, want %q", i, id, wantID)
		}
	}

	if len(documented.flows) != len(plain.flows) {
		t.Fatalf("documented model has %d flows, undocumented has %d", len(documented.flows), len(plain.flows))
	}
	for i := range plain.flows {
		got, want := documented.flows[i], plain.flows[i]
		if got.Source != want.Source || got.Target != want.Target || got.Default != want.Default {
			t.Errorf("flow %d differs: %+v, want %+v", i, got, want)
		}
		if (got.Condition == nil) != (want.Condition == nil) {
			t.Errorf("flow %d condition presence differs: %v, want %v", i, got.Condition != nil, want.Condition != nil)
		}
	}

	if got := documented.ProcessId(); got != plain.ProcessId() {
		t.Errorf("process id = %q, want %q", got, plain.ProcessId())
	}
	// The undocumented twin carries no prose at all: documentation is the only thing
	// that differs between the two models, and it is metadata beside the graph.
	if got := plain.Documentation(); got != "" {
		t.Errorf("undocumented model reports process documentation %q", got)
	}
	for i := range plain.nodes {
		if got := plain.ElementDocumentation(int32(i)); got != "" {
			t.Errorf("undocumented node %d reports documentation %q", i, got)
		}
	}
}

// nodeOf returns the node id of a source BPMN element id, for tests that address a
// node by the id the model gives it.
func nodeOf(t *testing.T, cp *CompiledProcess, bpmnID string) int32 {
	t.Helper()
	for i := range cp.nodes {
		if cp.ElementBpmnId(int32(i)) == bpmnID {
			return int32(i)
		}
	}
	t.Fatalf("no node with element id %q", bpmnID)
	return -1
}

// The compiler carries each element's documentation into the compiled process, so a
// runtime surface can show it without re-reading the model's XML (ADR-0025 amended, and
// invariant I5: the XML is parsed once, at deploy). The engine never reads it — see the
// graph-equality test above — but the API does, for a user task's work instruction.
func TestElementDocumentationIsRetained(t *testing.T) {
	cp, err := Parse(7, 1, strings.NewReader(documentedModel(true)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := cp.Documentation(), "Bearbeitet Anträge von der Prüfung bis zur Auszahlung."; got != want {
		t.Errorf("Documentation() = %q, want %q", got, want)
	}
	cases := map[string]string{
		"review":   "Vier-Augen-Prinzip: eine zweite Person prüft.",
		"pay":      "Zahlt über den Zahlungsdienst aus.",
		"split":    "Genehmigt, wenn alle Unterlagen vorliegen.",
		"start":    "Startet, sobald der Antrag eingeht.",
		"end":      "Der Antrag ist abgeschlossen.",
		"bnd":      "Nach einer Stunde ohne Prüfung eskalieren.",
		"sub":      "Nacharbeit, gekapselt als eigener Abschnitt.",
		"wait":     "Gesetzliche Wartefrist.",
		"substart": "Beginn der Nacharbeit.",
	}
	for bpmnID, want := range cases {
		if got := cp.ElementDocumentation(nodeOf(t, cp, bpmnID)); got != want {
			t.Errorf("ElementDocumentation(%q) = %q, want %q", bpmnID, got, want)
		}
	}

	// An undocumented element reports nothing, and so does a node id that is not one.
	// -1 is what the runtime passes for "no element", so it must not index the table.
	if got := cp.ElementDocumentation(nodeOf(t, cp, "merge")); got != "" {
		t.Errorf("undocumented element reports %q, want \"\"", got)
	}
	for _, id := range []int32{-1, int32(len(cp.nodes)), 1 << 20} {
		if got := cp.ElementDocumentation(id); got != "" {
			t.Errorf("ElementDocumentation(%d) = %q, want \"\" for an out-of-range node", id, got)
		}
	}
}

// A process with no <bpmn:documentation> anywhere reports empty strings rather than a
// stray interned value — the -1 sentinel must not be confused with intern index 0,
// which is a reserved job type.
func TestUndocumentedProcessReportsNothing(t *testing.T) {
	cp, err := Parse(7, 1, strings.NewReader(documentedModel(false)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.Documentation(); got != "" {
		t.Errorf("Documentation() = %q, want empty", got)
	}
	if got := cp.ElementDocumentation(nodeOf(t, cp, "review")); got != "" {
		t.Errorf("ElementDocumentation(review) = %q, want empty", got)
	}
}

// A documented collaboration compiles the same way: documentation on the collaboration,
// on each participant and on each pool's process changes none of the deployables.
func TestDocumentationOnCollaborationIsPassthrough(t *testing.T) {
	model := func(doc bool) string {
		d := func(text string) string {
			if !doc {
				return ""
			}
			return "<bpmn:documentation>" + text + "</bpmn:documentation>"
		}
		return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <bpmn:collaboration id="collab">` + d("Zusammenspiel von Vertrieb und Bank.") + `
    <bpmn:participant id="pool_v" name="Vertrieb" processRef="vertrieb">` + d("Nimmt die Anfrage auf.") + `</bpmn:participant>
    <bpmn:participant id="pool_b" name="Bank"/>
  </bpmn:collaboration>
  <bpmn:process id="vertrieb" isExecutable="true">` + d("Führt die Anfrage bis zum Angebot.") + `
    <bpmn:startEvent id="start">` + d("Anfrage geht ein.") + `</bpmn:startEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	}

	plain, err := ParseAll(1, 1, strings.NewReader(model(false)))
	if err != nil {
		t.Fatalf("ParseAll (undocumented): %v", err)
	}
	documented, err := ParseAll(1, 1, strings.NewReader(model(true)))
	if err != nil {
		t.Fatalf("ParseAll (documented): %v", err)
	}
	if len(documented) != len(plain) {
		t.Fatalf("documented model yields %d deployables, undocumented %d", len(documented), len(plain))
	}
	for i := range plain {
		if documented[i].PoolName != plain[i].PoolName || documented[i].ProcessName != plain[i].ProcessName {
			t.Errorf("deployable %d metadata differs: %+v, want %+v", i, documented[i], plain[i])
		}
		if len(documented[i].Process.nodes) != len(plain[i].Process.nodes) {
			t.Errorf("deployable %d node count = %d, want %d", i, len(documented[i].Process.nodes), len(plain[i].Process.nodes))
		}
	}
}

// elementDocumentation is the generic walk that indexes documentation by element id.
// It runs over every deployed model, so its edges are worth stating: several
// <documentation> children join, an element without an id records nothing (there is no
// key to record it under), a foreign-namespace <documentation> belongs to whatever
// extension declared it, and whitespace-only prose is not documentation.
func TestElementDocumentationIndex(t *testing.T) {
	const model = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:other="http://example.com/other" id="defs">
  <bpmn:documentation>The model as a whole.</bpmn:documentation>
  <bpmn:process id="p">
    <bpmn:documentation>First paragraph.</bpmn:documentation>
    <bpmn:documentation textFormat="text/html">Second paragraph.</bpmn:documentation>
    <bpmn:startEvent id="start"><bpmn:documentation>   Trimmed.   </bpmn:documentation></bpmn:startEvent>
    <bpmn:endEvent id="blank"><bpmn:documentation>   </bpmn:documentation></bpmn:endEvent>
    <bpmn:task><bpmn:documentation>Nowhere to file this.</bpmn:documentation></bpmn:task>
    <bpmn:userTask id="foreign"><other:documentation>Not the element's own prose.</other:documentation></bpmn:userTask>
  </bpmn:process>
</bpmn:definitions>`

	docs := elementDocumentation([]byte(model))
	want := map[string]string{
		"defs":  "The model as a whole.",
		"p":     "First paragraph.\n\nSecond paragraph.",
		"start": "Trimmed.",
	}
	for id, w := range want {
		if got := docs[id]; got != w {
			t.Errorf("docs[%q] = %q, want %q", id, got, w)
		}
	}
	for _, id := range []string{"blank", "foreign"} {
		if got, ok := docs[id]; ok {
			t.Errorf("docs[%q] = %q, want no entry", id, got)
		}
	}
	if len(docs) != len(want) {
		t.Errorf("indexed %d elements (%v), want exactly %d", len(docs), docs, len(want))
	}

	// A <documentation> with no element around it belongs to nothing; it is recorded
	// under no key rather than panicking on an empty owner stack.
	if got := elementDocumentation([]byte(`<documentation>Orphan.</documentation>`)); len(got) != 0 {
		t.Errorf("root-level documentation indexed %v, want nothing", got)
	}
}

// The walk is best-effort by design: decodeDefinitions has already rejected malformed
// XML, so a truncated document here returns what it found rather than failing a deploy
// over metadata.
func TestElementDocumentationToleratesTruncatedXML(t *testing.T) {
	const truncated = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="p"><bpmn:documentation>Kept.</bpmn:documentation>
    <bpmn:startEvent id="start"><bpmn:doc`
	docs := elementDocumentation([]byte(truncated))
	if got := docs["p"]; got != "Kept." {
		t.Errorf("docs[p] = %q, want %q", got, "Kept.")
	}
}

// SetElementDocumentation ignores a node id that is not one, like the other node
// setters — a caller mistake must not panic mid-deploy.
func TestSetElementDocumentationIgnoresUnknownNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	b.SetElementDocumentation(start, "Kept.")
	b.SetElementDocumentation(-1, "Dropped.")
	b.SetElementDocumentation(99, "Dropped.")

	b.AddEndEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ElementDocumentation(start); got != "Kept." {
		t.Errorf("ElementDocumentation(start) = %q, want %q", got, "Kept.")
	}
}

// A model that cannot be read at all fails the parse with the read error rather than
// compiling an empty process — decodeDefinitions reads the whole model into memory to
// walk it twice, so the read itself is now a failure point worth naming.
func TestParseReportsAReadFailure(t *testing.T) {
	_, err := Parse(1, 1, failingReader{})
	if err == nil {
		t.Fatal("Parse over a failing reader returned no error")
	}
	if !strings.Contains(err.Error(), "read BPMN") {
		t.Errorf("error = %v, want it to name the read failure", err)
	}
}

// failingReader fails on the first read, standing in for a truncated upload.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errRead }

var errRead = errors.New("connection reset")
