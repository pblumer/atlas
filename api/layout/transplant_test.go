package layout

import (
	"errors"
	"strings"
	"testing"
)

// storedModel is a deployed definition as it sits in the store: one task, a
// diagram, and a script whose indentation is load-bearing — the thing a
// transplant must be unable to touch.
const storedModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Definitions_1" targetNamespace="http://atlas/bpmn" exporter="Atlas" exporterVersion="1.0">
  <bpmn:process id="p" name="Freigabe" isExecutable="true">
    <bpmn:startEvent id="start" name="Antrag eingegangen"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:scriptTask id="calc" name="Betrag prüfen">
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
      <bpmn:script>if amount &gt; 100:
    result = "gross"
else:
    result = "klein"</bpmn:script>
    </bpmn:scriptTask>
    <bpmn:endEvent id="end" name="Fertig"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="calc"/>
    <bpmn:sequenceFlow id="f2" sourceRef="calc" targetRef="end"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="p">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="150" y="100" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="calc_di" bpmnElement="calc"><dc:Bounds x="240" y="78" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="end_di" bpmnElement="end"><dc:Bounds x="400" y="100" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="186" y="118"/><di:waypoint x="240" y="118"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="340" y="118"/><di:waypoint x="400" y="118"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`

// movedDiagram is the same model back from an editor: every shape nudged, the
// document reformatted and re-stamped, prefixes renamed — and not one thing about
// the process changed.
const movedDiagram = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:di2="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:c="http://www.omg.org/spec/DD/20100524/DC" xmlns:w="http://www.omg.org/spec/DD/20100524/DI" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Definitions_1" targetNamespace="http://atlas/bpmn" exporter="bpmn-js" exporterVersion="17.0.0">
  <process id="p" name="Freigabe" isExecutable="true">
    <startEvent id="start" name="Antrag eingegangen">
      <outgoing>f1</outgoing>
    </startEvent>
    <scriptTask id="calc" name="Betrag prüfen">
      <incoming>f1</incoming>
      <outgoing>f2</outgoing>
      <script>if amount &gt; 100:
    result = "gross"
else:
    result = "klein"</script>
    </scriptTask>
    <endEvent id="end" name="Fertig">
      <incoming>f2</incoming>
    </endEvent>
    <sequenceFlow id="f1" sourceRef="start" targetRef="calc"/>
    <sequenceFlow id="f2" sourceRef="calc" targetRef="end"/>
  </process>
  <di2:BPMNDiagram id="D">
    <di2:BPMNPlane id="P" bpmnElement="p">
      <di2:BPMNShape id="start_di" bpmnElement="start"><c:Bounds x="150" y="300" width="36" height="36"/></di2:BPMNShape>
      <di2:BPMNShape id="calc_di" bpmnElement="calc"><c:Bounds x="240" y="278" width="100" height="80"/></di2:BPMNShape>
      <di2:BPMNShape id="end_di" bpmnElement="end"><c:Bounds x="400" y="300" width="36" height="36"/></di2:BPMNShape>
      <di2:BPMNEdge id="f1_di" bpmnElement="f1"><w:waypoint x="186" y="318"/><w:waypoint x="240" y="318"/></di2:BPMNEdge>
      <di2:BPMNEdge id="f2_di" bpmnElement="f2"><w:waypoint x="340" y="318"/><w:waypoint x="400" y="318"/></di2:BPMNEdge>
    </di2:BPMNPlane>
  </di2:BPMNDiagram>
</definitions>`

// TestTransplantMovesOnlyTheDiagram is the whole promise in one assertion pair:
// the new coordinates arrive, and the semantic half comes back byte for byte —
// including a script whose indentation an editor round-trip is free to have
// rewritten, because those bytes are never the ones that land.
func TestTransplantMovesOnlyTheDiagram(t *testing.T) {
	out, err := Transplant([]byte(storedModel), []byte(movedDiagram))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, `x="150" y="300"`) {
		t.Errorf("the moved start-event bounds did not arrive:\n%s", got)
	}
	if strings.Contains(got, `x="150" y="100"`) {
		t.Errorf("the old start-event bounds survived the transplant:\n%s", got)
	}
	// The stored semantic half, exactly as it was. BPMN writes the diagram after the
	// model, so everything up to the stored document's own <BPMNDiagram> is the half
	// the compiler reads — and it has to come back byte for byte, not merely
	// equivalent, because those bytes are what the deployed CompiledProcess was
	// built from.
	semantic := storedModel[:strings.Index(storedModel, "<bpmndi:BPMNDiagram")]
	if !strings.Contains(got, semantic) {
		t.Errorf("the semantic half did not survive verbatim.\ngot:\n%s\nwant it to contain:\n%s", got, semantic)
	}
	if !strings.Contains(got, "\n    result = \"gross\"\n") {
		t.Errorf("the script task's indentation did not survive:\n%s", got)
	}
	// And it still reads as the same model, which is the check a second transplant
	// onto this result would make.
	before, err := semanticDigest([]byte(storedModel))
	if err != nil {
		t.Fatalf("digest the stored model: %v", err)
	}
	after, err := semanticDigest(out)
	if err != nil {
		t.Fatalf("digest the result: %v", err)
	}
	if before != after {
		t.Error("the transplant changed what the model means")
	}
	// Exactly one diagram: the old one is gone rather than kept alongside.
	if n := strings.Count(got, "<di2:BPMNDiagram") + strings.Count(got, "<bpmndi:BPMNDiagram"); n != 1 {
		t.Errorf("expected one diagram in the result, found %d:\n%s", n, got)
	}
}

// TestTransplantMakesTheDiagramSelfContained: the incoming document declared the
// DI namespaces under its own prefixes, and the stored root has never heard of
// them. The fragment has to carry its bindings across or it splices in as
// well-formed nonsense — elements in no namespace bpmn-js will resolve.
func TestTransplantMakesTheDiagramSelfContained(t *testing.T) {
	out, err := Transplant([]byte(storedModel), []byte(movedDiagram))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	got := string(out)
	for prefix, uri := range map[string]string{
		"di2": "http://www.omg.org/spec/BPMN/20100524/DI",
		"c":   "http://www.omg.org/spec/DD/20100524/DC",
		"w":   "http://www.omg.org/spec/DD/20100524/DI",
	} {
		if !strings.Contains(got, `xmlns:`+prefix+`="`+uri+`"`) {
			t.Errorf("the transplanted diagram does not re-declare xmlns:%s=%q:\n%s", prefix, uri, got)
		}
	}
	// The declarations belong on the diagram element, not scattered through it.
	head := got[strings.Index(got, "<di2:BPMNDiagram"):]
	head = head[:strings.Index(head, ">")]
	if !strings.Contains(head, "xmlns:di2=") {
		t.Errorf("the declarations are not on the <BPMNDiagram> start tag: %q", head)
	}
}

// TestTransplantRefusesADifferentModel: a layout only means something against the
// shapes it was drawn for, so a document whose process differs is refused rather
// than grafted on.
func TestTransplantRefusesADifferentModel(t *testing.T) {
	renamed := strings.Replace(movedDiagram, `name="Betrag prüfen"`, `name="Betrag prüfen (neu)"`, 1)
	if _, err := Transplant([]byte(storedModel), []byte(renamed)); !errors.Is(err, ErrDifferentModel) {
		t.Fatalf("renaming a task: got %v, want ErrDifferentModel", err)
	}
	added := strings.Replace(movedDiagram, "</process>",
		`<task id="extra" name="Zusatz"/></process>`, 1)
	if _, err := Transplant([]byte(storedModel), []byte(added)); !errors.Is(err, ErrDifferentModel) {
		t.Fatalf("adding a task: got %v, want ErrDifferentModel", err)
	}
	retyped := strings.Replace(movedDiagram, "<scriptTask ", "<serviceTask ", 1)
	retyped = strings.Replace(retyped, "</scriptTask>", "</serviceTask>", 1)
	if _, err := Transplant([]byte(storedModel), []byte(retyped)); !errors.Is(err, ErrDifferentModel) {
		t.Fatalf("changing an element's type: got %v, want ErrDifferentModel", err)
	}
}

// TestTransplantRefusesAScriptEdit is the case the digest exists to catch beyond
// element structure: same elements, same attributes, different code inside one of
// them. The transplant would keep the stored script either way — but a caller who
// edited it believes they saved that edit, and silently discarding it is worse
// than refusing.
func TestTransplantRefusesAScriptEdit(t *testing.T) {
	edited := strings.Replace(movedDiagram, `result = "gross"`, `result = "riesig"`, 1)
	if _, err := Transplant([]byte(storedModel), []byte(edited)); !errors.Is(err, ErrDifferentModel) {
		t.Fatalf("editing a script body: got %v, want ErrDifferentModel", err)
	}
}

// TestTransplantForgivesReformatting: an editor reformats freely, and every one of
// these round-trips is the same model. Refusing them would make the feature
// unusable, since no editor writes a document back the way it read it.
func TestTransplantForgivesReformatting(t *testing.T) {
	for name, incoming := range map[string]string{
		"prefix and layout differ": movedDiagram,
		"attribute order differs": strings.Replace(movedDiagram,
			`<sequenceFlow id="f1" sourceRef="start" targetRef="calc"/>`,
			`<sequenceFlow targetRef="calc" sourceRef="start" id="f1"/>`, 1),
		"root re-stamped": strings.Replace(movedDiagram,
			`exporter="bpmn-js" exporterVersion="17.0.0"`,
			`exporter="Some Modeler" exporterVersion="9.9" xmlns:extra="urn:x"`, 1),
		"comment added": strings.Replace(movedDiagram, "<process ", "<!-- drawn by hand --><process ", 1),
	} {
		if _, err := Transplant([]byte(storedModel), []byte(incoming)); err != nil {
			t.Errorf("%s: Transplant refused a document that means the same thing: %v", name, err)
		}
	}
}

// TestTransplantRefusesABlankDiagram: replacing a picture with an empty plane is
// not an adjustment, it is a diagram nobody can read. A body with no diagram at
// all is the same refusal for the same reason.
func TestTransplantRefusesABlankDiagram(t *testing.T) {
	blank := strings.Replace(movedDiagram,
		movedDiagram[strings.Index(movedDiagram, "<di2:BPMNDiagram"):strings.Index(movedDiagram, "</di2:BPMNDiagram>")+len("</di2:BPMNDiagram>")],
		`<di2:BPMNDiagram id="D"><di2:BPMNPlane id="P" bpmnElement="p"/></di2:BPMNDiagram>`, 1)
	if _, err := Transplant([]byte(storedModel), []byte(blank)); !errors.Is(err, ErrNoDiagram) {
		t.Fatalf("an empty plane: got %v, want ErrNoDiagram", err)
	}
	semanticOnly := stripDiagram([]byte(movedDiagram))
	if _, err := Transplant([]byte(storedModel), semanticOnly); !errors.Is(err, ErrNoDiagram) {
		t.Fatalf("no diagram at all: got %v, want ErrNoDiagram", err)
	}
}

// TestTransplantOntoALayoutlessModel covers the deployment that was pushed as
// pure semantic XML: the UI renders it through Ensure's generated layout, so what
// comes back from the editor is the first diagram this definition has ever had.
// It has to land like any other.
func TestTransplantOntoALayoutlessModel(t *testing.T) {
	semanticOnly := stripDiagram([]byte(storedModel))
	out, err := Transplant(semanticOnly, []byte(movedDiagram))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	if !strings.Contains(string(out), `x="150" y="300"`) {
		t.Errorf("the diagram did not arrive on a layout-less model:\n%s", out)
	}
	if !strings.Contains(string(out), "</bpmn:definitions>") {
		t.Errorf("the result lost its root close tag:\n%s", out)
	}
}

// TestTransplantRejectsUnparseableXML: a caller that posts something that is not
// a BPMN document gets told so, rather than having it spliced in.
func TestTransplantRejectsUnparseableXML(t *testing.T) {
	if _, err := Transplant([]byte(storedModel), []byte("<definitions><process></definitions>")); err == nil {
		t.Fatal("expected an error for malformed XML")
	}
	if _, err := Transplant([]byte("<definitions>"), []byte(movedDiagram)); err == nil {
		t.Fatal("expected an error for a malformed stored model")
	}
}

// TestSameModel is the question the UI asks before it offers to save a layout,
// so it has to answer both halves: same model, and not.
func TestSameModel(t *testing.T) {
	same, err := SameModel([]byte(storedModel), []byte(movedDiagram))
	if err != nil {
		t.Fatalf("SameModel: %v", err)
	}
	if !same {
		t.Error("a moved diagram of the same process reads as a different model")
	}
	same, err = SameModel([]byte(storedModel),
		[]byte(strings.Replace(movedDiagram, `id="calc"`, `id="calc2"`, 1)))
	if err != nil {
		t.Fatalf("SameModel: %v", err)
	}
	if same {
		t.Error("a renamed element id reads as the same model")
	}
}

// TestTransplantKeepsDiagramExtensions: colours and other DI extensions are the
// operator's adjustment as much as the coordinates are. A transplant that
// normalised the diagram into the generator's own vocabulary would drop them.
func TestTransplantKeepsDiagramExtensions(t *testing.T) {
	coloured := strings.Replace(movedDiagram,
		`<di2:BPMNShape id="calc_di" bpmnElement="calc">`,
		`<di2:BPMNShape id="calc_di" bpmnElement="calc" bioc:stroke="#831311" bioc:fill="#ffcdd2" xmlns:bioc="http://bpmn.io/schema/bpmn/biocolor/1.0">`, 1)
	out, err := Transplant([]byte(storedModel), []byte(coloured))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	if !strings.Contains(string(out), `bioc:fill="#ffcdd2"`) {
		t.Errorf("the shape colour was dropped:\n%s", out)
	}
}

// TestTransplantTwoDiagramsBothMove: a collaboration can carry more than one
// <BPMNDiagram>, and a transplant that took only the first would leave the rest
// of the model drawn the old way.
func TestTransplantTwoDiagramsBothMove(t *testing.T) {
	two := func(src, y1, y2 string) string {
		one := src[strings.Index(src, "<di2:BPMNDiagram") : strings.Index(src, "</di2:BPMNDiagram>")+len("</di2:BPMNDiagram>")]
		return strings.Replace(src, one,
			strings.Replace(one, `y="300"`, `y="`+y1+`"`, 1)+
				strings.Replace(strings.Replace(one, `id="D"`, `id="D2"`, 1), `y="300"`, `y="`+y2+`"`, 1), 1)
	}
	out, err := Transplant([]byte(storedModel), []byte(two(movedDiagram, "500", "700")))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `y="500"`) || !strings.Contains(got, `y="700"`) {
		t.Errorf("both diagrams should have arrived:\n%s", got)
	}
	if n := strings.Count(got, "<di2:BPMNDiagram"); n != 2 {
		t.Errorf("expected 2 diagrams in the result, found %d", n)
	}
}
