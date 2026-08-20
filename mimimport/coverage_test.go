package mimimport

import (
	"strings"
	"testing"
)

// TestConvertParallel exercises the ParallelActivity path (split/join parallel
// gateways) and a multi-row layout, including an empty branch.
func TestConvertParallel(t *testing.T) {
	src := `<SequentialWorkflow>
	  <ParallelActivity Description="Fan out">
	    <SequenceActivity><NotificationActivity Description="A"/></SequenceActivity>
	    <SequenceActivity><UpdateResourceActivity Description="B"/></SequenceActivity>
	    <SequenceActivity/>
	  </ParallelActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "Par")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if strings.Count(string(res.BPMN), "<parallelGateway") < 2 {
		t.Errorf("want a split and a join parallel gateway:\n%s", res.BPMN)
	}
}

// TestConvertBareActivityRoot covers workflowBody's bare-activity branch (the
// root is an activity rather than a *Workflow* container) and a group container.
func TestConvertBareActivityRoot(t *testing.T) {
	res, err := Convert(strings.NewReader(`<ApprovalActivity Description="Lone approval"/>`), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), "<userTask") {
		t.Errorf("bare ApprovalActivity should become a user task:\n%s", res.BPMN)
	}

	grp := `<SequentialWorkflow><ConditionedActivityGroup Description="CAG">
	  <CodeActivity Description="Run code"/></ConditionedActivityGroup></SequentialWorkflow>`
	res, err = Convert(strings.NewReader(grp), "Grp")
	if err != nil {
		t.Fatalf("Convert group: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), `type="mim-code"`) {
		t.Errorf("CodeActivity should map to a mim-code service task:\n%s", res.BPMN)
	}
}

// TestConvertNonWorkflowNonActivityRoot covers workflowBody's fallback: a root
// that is neither a *Workflow* nor an *Activity* but contains activities.
func TestConvertNonWorkflowNonActivityRoot(t *testing.T) {
	res, err := Convert(strings.NewReader(`<Root><NotificationActivity Description="hi"/></Root>`), "R")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), `type="mim-notification"`) {
		t.Errorf("expected the notification to convert:\n%s", res.BPMN)
	}
}

// TestWrapperCDATA covers the CDATA-wrapped embedded XOML path.
func TestWrapperCDATA(t *testing.T) {
	inner := `<SequentialWorkflow><ApprovalActivity Description="OK"/></SequentialWorkflow>`
	wrapper := `<Export><XOML><![CDATA[` + inner + `]]></XOML></Export>`
	res, err := Convert(strings.NewReader(wrapper), "Wrap")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), "<userTask") {
		t.Errorf("embedded CDATA XOML should convert:\n%s", res.BPMN)
	}
}

func TestConvertInvalidInput(t *testing.T) {
	if _, err := Convert(strings.NewReader("not xml at all <<<"), ""); err == nil {
		t.Error("expected an error for non-XML input")
	}
}

func TestCDATAHelper(t *testing.T) {
	if got := cdata("<a/>"); !strings.HasPrefix(got, "<![CDATA[") {
		t.Errorf("plain payload should be wrapped in CDATA, got %q", got)
	}
	if got := cdata("x]]>y"); strings.Contains(got, "CDATA") {
		t.Errorf("payload with terminator must fall back to escaping, got %q", got)
	}
}

func TestSanitizeIDHelper(t *testing.T) {
	cases := map[string]string{
		"Hello World": "Hello_World",
		"   ":         "def",
		"123go":       "mim_123go",
		"a/b#c":       "abc",
		"-_trim_-":    "trim",
	}
	for in, want := range cases {
		if got := sanitizeID(in, "def"); got != want {
			t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyLeafHelper(t *testing.T) {
	for _, tc := range []struct {
		in   string
		kind string
	}{
		{"CodeActivity", "serviceTask"},
		{"PowerShellActivity", "serviceTask"},
		{"SendMailActivity", "serviceTask"},
		{"CreateResourceActivity", "serviceTask"},
		{"WeirdCustomActivity", "task"},
	} {
		if k, _, _, _ := classifyLeaf(tc.in); k != tc.kind {
			t.Errorf("classifyLeaf(%q) kind = %q, want %q", tc.in, k, tc.kind)
		}
	}
}

func TestCleanEmbeddedHelper(t *testing.T) {
	if got := cleanEmbedded("<![CDATA[<x/>]]>"); got != "<x/>" {
		t.Errorf("CDATA strip = %q", got)
	}
	if got := cleanEmbedded("&lt;x/&gt;"); got != "<x/>" {
		t.Errorf("unescape = %q", got)
	}
}

func TestLastSegmentHelper(t *testing.T) {
	if got := lastSegment("http://a/b/xaml/"); got != "xaml" {
		t.Errorf("lastSegment trailing = %q", got)
	}
	if got := lastSegment("plain"); got != "plain" {
		t.Errorf("lastSegment plain = %q", got)
	}
}

// TestConditionAttributeAndUnnamedBranch covers a branch condition supplied as an
// attribute (not a child) and a branch activity with no display name.
func TestConditionAttributeAndUnnamedBranch(t *testing.T) {
	src := `<SequentialWorkflow>
	  <IfElseActivity Description="pick">
	    <IfElseBranchActivity Condition="a = b"><ApprovalActivity/></IfElseBranchActivity>
	    <IfElseBranchActivity><NotificationActivity/></IfElseBranchActivity>
	  </IfElseActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "C")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
}

// TestWhileConditionAttribute covers a while loop whose condition is an attribute.
func TestWhileConditionAttribute(t *testing.T) {
	res, err := Convert(strings.NewReader(
		`<SequentialWorkflow><WhileActivity Condition="x > 0"><CodeActivity/></WhileActivity></SequentialWorkflow>`), "W2")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
}

// TestWrapperFallbackAndAttributeXOML covers parseXOML's fallback (a wrapper with
// neither activities nor embedded XOML) and XOML carried as an element's own text
// under an AttributeName="XOML" element (no <Value> child).
func TestWrapperFallbackAndAttributeXOML(t *testing.T) {
	res, err := Convert(strings.NewReader(`<Wrapper><Foo/></Wrapper>`), "W")
	if err != nil {
		t.Fatalf("Convert fallback: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), "<task ") {
		t.Errorf("unknown Foo should become a plain task:\n%s", res.BPMN)
	}

	inner := `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`
	wrap := `<Attr AttributeName="XOML">` + escapeXML(inner) + `</Attr>`
	res, err = Convert(strings.NewReader(wrap), "X")
	if err != nil {
		t.Fatalf("Convert attr XOML: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), "<userTask") {
		t.Errorf("attribute-named XOML should convert:\n%s", res.BPMN)
	}
}
