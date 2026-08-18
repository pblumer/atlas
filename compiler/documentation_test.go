package compiler

import (
	"strings"
	"testing"
)

// Documentation is BPMN's own place for prose about an element — a <bpmn:documentation>
// child every process, pool, flow node, sequence flow and data object may carry, which
// the Modeler's properties panel authors (ADR-0025). Atlas treats it as pure passthrough:
// the model carries it, the compiler ignores it. That is a guarantee worth pinning down,
// because it is what lets authors document a process freely — a documented model must
// deploy, and must compile to exactly the graph its undocumented twin compiles to. If the
// parser ever started tripping over documentation (or, worse, quietly changing the graph
// because of it), these tests fail rather than a user's deploy.

// documentedModel is one model exercising every element kind the panel documents. With
// doc=true each element carries a <bpmn:documentation> child; with doc=false the very
// same model carries none. Everything else about the two is byte-identical.
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
    <bpmn:exclusiveGateway id="merge">` + d("Führt beide Wege wieder zusammen.") + `</bpmn:exclusiveGateway>
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

	// The documentation itself is not compiled into anything the runtime can read —
	// it lives in the model, and the model alone.
	if got := documented.ProcessId(); got != plain.ProcessId() {
		t.Errorf("process id = %q, want %q", got, plain.ProcessId())
	}
	for _, s := range documented.strings {
		if strings.Contains(s, "Bearbeitet Anträge") || strings.Contains(s, "Vier-Augen-Prinzip") {
			t.Errorf("documentation text %q was interned into the compiled process", s)
		}
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
