package layout

import (
	"errors"
	"strings"
	"testing"
)

// TestTransplantReadsSingleQuotedDeclarations: XML allows either quote, and an
// exporter that writes the root's namespace declarations in single quotes must still
// get a diagram that carries its prefixes with it.
func TestTransplantReadsSingleQuotedDeclarations(t *testing.T) {
	quoted := strings.Replace(movedDiagram,
		`xmlns:di2="http://www.omg.org/spec/BPMN/20100524/DI"`,
		`xmlns:di2='http://www.omg.org/spec/BPMN/20100524/DI'`, 1)
	out, err := Transplant([]byte(storedModel), []byte(quoted))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	head := string(out)[strings.Index(string(out), "<di2:BPMNDiagram"):]
	head = head[:strings.Index(head, ">")]
	if !strings.Contains(head, `xmlns:di2="http://www.omg.org/spec/BPMN/20100524/DI"`) {
		t.Fatalf("the diagram does not re-declare the single-quoted prefix: %q", head)
	}
}

// TestTransplantLeavesASelfContainedDiagramAsItIs: a diagram that already declares
// every prefix it uses needs nothing added, and arrives byte for byte.
func TestTransplantLeavesASelfContainedDiagramAsItIs(t *testing.T) {
	block := `<di2:BPMNDiagram xmlns:di2="http://www.omg.org/spec/BPMN/20100524/DI" ` +
		`xmlns:c="http://www.omg.org/spec/DD/20100524/DC" id="D">` +
		`<di2:BPMNPlane id="P" bpmnElement="p">` +
		`<di2:BPMNShape id="start_di" bpmnElement="start"><c:Bounds x="1" y="2" width="36" height="36"/></di2:BPMNShape>` +
		`</di2:BPMNPlane></di2:BPMNDiagram>`
	from := strings.Index(movedDiagram, "<di2:BPMNDiagram")
	to := strings.Index(movedDiagram, "</di2:BPMNDiagram>") + len("</di2:BPMNDiagram>")
	incoming := movedDiagram[:from] + block + movedDiagram[to:]

	out, err := Transplant([]byte(storedModel), []byte(incoming))
	if err != nil {
		t.Fatalf("Transplant: %v", err)
	}
	if !strings.Contains(string(out), block) {
		t.Fatalf("the self-contained diagram was rewritten:\n%s", out)
	}
}

// TestASubmissionWithContentAfterItsRootIsNamed: a document pasted twice still has
// the right model at its start, and the refusal says the difference is that it
// carries on past the end, not somewhere inside it.
func TestASubmissionWithContentAfterItsRootIsNamed(t *testing.T) {
	_, err := Transplant([]byte(storedModel), []byte(movedDiagram+movedDiagram))
	if !errors.Is(err, ErrDifferentModel) {
		t.Fatalf("Transplant = %v, want ErrDifferentModel", err)
	}
	if !strings.Contains(err.Error(), "the submitted model continues past the end of <definitions>") {
		t.Fatalf("Transplant = %v, want the trailing content named", err)
	}
}
