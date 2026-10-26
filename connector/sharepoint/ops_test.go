package sharepoint_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepoint"
)

// TestSharePointOpsMatchTheCompiler is the drift guard between this package's
// [sharepoint.Ops] table and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/sharepoint imports compiler, so
// the dependency only runs one way — which is why the rules exist twice. The check is
// therefore behavioural: every operation this package implements compiles from a model
// that supplies exactly what Ops says it needs, and does not compile with any one of
// those values missing; and an operation the compiler accepts that is not in Ops would
// reach a worker that refuses it on every token.
func TestSharePointOpsMatchTheCompiler(t *testing.T) {
	type value struct{ name, attr, val string }
	needed := func(spec sharepoint.Op) []value {
		var out []value
		if spec.NeedsList {
			out = append(out, value{"list", `list`, "Auftraege"})
		}
		if spec.NeedsItemID {
			out = append(out, value{"itemId", `itemId`, "7"})
		}
		if spec.NeedsPath {
			out = append(out, value{"path", `path`, "Akten/a.pdf"})
		}
		if spec.NeedsContent {
			out = append(out, value{"content", `content`, "Hallo"})
		}
		if spec.NeedsResult {
			out = append(out, value{"result", `resultVariable`, "r"})
		}
		return out
	}
	model := func(op string, spec sharepoint.Op, omit string) string {
		parts := []string{`connector="intranet"`, `operation="` + op + `"`, `site="contoso.sharepoint.com:/sites/hr"`}
		for _, v := range needed(spec) {
			if v.name != omit {
				parts = append(parts, v.attr+`="`+v.val+`"`)
			}
		}
		children := ""
		if spec.NeedsFields && omit != "fields" {
			children = `<atlas:itemField name="Status" value="Erledigt"/>`
		}
		return strings.Join(parts, " ") + ">" + children
	}
	for op, spec := range sharepoint.Ops {
		t.Run(op, func(t *testing.T) {
			if err := compileSharePoint(model(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			omissions := []string{"site"}
			for _, v := range needed(spec) {
				omissions = append(omissions, v.name)
			}
			if spec.NeedsFields {
				omissions = append(omissions, "fields")
			}
			for _, omit := range omissions {
				src := model(op, spec, omit)
				if omit == "site" {
					src = strings.Replace(src, `site="contoso.sharepoint.com:/sites/hr"`, "", 1)
				}
				if err := compileSharePoint(src); err == nil {
					t.Errorf("the compiler accepts %q without its required %s", op, omit)
				}
			}
			// What the operation takes is accepted; what it does not take is refused.
			for attr, takes := range map[string]bool{
				`filter="x"`:        spec.TakesFilter,
				`select="Title"`:    spec.TakesSelect,
				`pageToken="x"`:     spec.TakesPaging,
				`maxResults="5"`:    spec.TakesPaging,
				`encoding="base64"`: spec.TakesEncoding,
			} {
				err := compileSharePoint(strings.Replace(model(op, spec, ""), ">", " "+attr+">", 1))
				if takes && err != nil {
					t.Errorf("%s takes %s, and the compiler refuses it: %v", op, attr, err)
				}
				if !takes && err == nil {
					t.Errorf("%s does not take %s, and the compiler accepts it", op, attr)
				}
			}
		})
	}
	if err := compileSharePoint(`connector="c" operation="share-file" site="s" path="a">`); err == nil {
		t.Error("the compiler accepts an operation this package does not implement")
	}
}

// The compiler's authored ceilings and encodings are this package's own. The two exist
// separately for the dependency's sake, and a number that drifted would be refused at
// deploy under one rule and honoured at call time under another.
func TestTheCompilersCeilingsAreThisPackages(t *testing.T) {
	list := `connector="c" operation="list-items" site="s" list="l" resultVariable="r" maxResults="%d">`
	if err := compileSharePoint(fmt.Sprintf(list, sharepoint.MaxPageSize)); err != nil {
		t.Errorf("the compiler refuses a page at this package's cap: %v", err)
	}
	if err := compileSharePoint(fmt.Sprintf(list, sharepoint.MaxPageSize+1)); err == nil {
		t.Error("the compiler accepts a page past this package's cap")
	}
	cp, err := compiler.Parse(1, 1, strings.NewReader(sharePointModel(`connector="c" operation="list-folder" site="s" resultVariable="r">`)))
	if err != nil {
		t.Fatal(err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if got := cp.ConnectorTask(cp.Node(task).Detail).SharePointMaxResults; got != sharepoint.DefaultMaxResults {
		t.Errorf("the compiler's default page is %d, this package's %d", got, sharepoint.DefaultMaxResults)
	}
	for _, enc := range []string{sharepoint.EncodingText, sharepoint.EncodingBase64} {
		if err := compileSharePoint(`connector="c" operation="download-file" site="s" path="a" encoding="` + enc + `" resultVariable="r">`); err != nil {
			t.Errorf("the compiler refuses encoding %q, which this package names: %v", enc, err)
		}
	}
	if err := compileSharePoint(`connector="c" operation="download-file" site="s" path="a" encoding="hex" resultVariable="r">`); err == nil {
		t.Error("the compiler accepts an encoding neither side implements")
	}
}

// The operation a model leaving it out gets is the one this package reads an empty
// operation as.
func TestNoOperationIsCreateItemOnBothSides(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(sharePointModel(`connector="c" site="s" list="l">`)))
	if err != nil {
		t.Fatal(err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if got := cp.Intern(cp.ConnectorTask(cp.Node(task).Detail).SharePointOp); got != sharepoint.OpCreateItem {
		t.Errorf("the compiler reads no operation as %q, this package as %q", got, sharepoint.OpCreateItem)
	}
}

func sharePointModel(open string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:sharepointConnector ` + open + `</atlas:sharepointConnector></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func compileSharePoint(open string) error {
	_, err := compiler.Parse(1, 1, strings.NewReader(sharePointModel(open)))
	return err
}
