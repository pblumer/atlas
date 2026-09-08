package mimimport

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
)

// mimSources returns the payload of every <atlas:mimSource> element in a
// generated document, unescaped by the XML decoder exactly as a consumer of the
// extension element would read it.
func mimSources(t *testing.T, bpmn []byte) []string {
	t.Helper()
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(bpmn))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("generated BPMN did not parse: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "mimSource" {
			continue
		}
		var payload string
		if err := dec.DecodeElement(&payload, &se); err != nil {
			t.Fatalf("mimSource did not decode: %v", err)
		}
		out = append(out, payload)
	}
}

// TestConvertMIMWALWorkflow covers a workflow as the MIMWAL activity library
// really serialises one: xmlns declarations written without quotes, the author's
// label in ActivityDisplayName rather than DisplayName, and MIM expressions that
// contain quotation marks.
func TestConvertMIMWALWorkflow(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	// The document only parses after its unquoted xmlns values are repaired, and
	// that repair must be reported rather than assumed.
	if len(res.Report.Warnings) != 1 || !strings.Contains(res.Report.Warnings[0], "unquoted attribute values") {
		t.Errorf("expected a warning about the repaired input, got %q", res.Report.Warnings)
	}
	if !strings.Contains(res.Report.String(), "warning") {
		t.Error("report should render its warnings")
	}
	if !strings.Contains(bpmn, "Hinweis: Die Eingabe war nicht wohlgeformt") {
		t.Errorf("process documentation should carry the warning:\n%s", bpmn)
	}

	// Names come from ActivityDisplayName, not from the WF designer id in x:Name.
	for _, want := range []string{
		`name="Abhängige Objekte abfragen"`,
		`name="Eindeutigen Kontonamen erzeugen"`,
	} {
		if !strings.Contains(bpmn, want) {
			t.Errorf("generated BPMN is missing %s\n%s", want, bpmn)
		}
	}
	// x:Name survives inside the preserved source, but must never become a node
	// name.
	if strings.Contains(bpmn, `name="actionActivity`) {
		t.Errorf("no node should be named after its x:Name designer id:\n%s", bpmn)
	}

	// Re-parsing the preserved source must yield the activity's original
	// attribute values, quotation marks included.
	sources := mimSources(t, res.BPMN)
	if len(sources) != 2 {
		t.Fatalf("want a preserved source per activity, got %d", len(sources))
	}
	update, _, err := decodeNode([]byte(sources[0]))
	if err != nil {
		t.Fatalf("preserved UpdateResources did not re-parse: %v\n%s", err, sources[0])
	}
	const wantCond = `Not(ParametersContain([//Request/RequestParameter], "ReApplyBusinessObject"))`
	if got, _ := update.attr("ActivityExecutionCondition"); got != wantCond {
		t.Errorf("ActivityExecutionCondition = %q, want %q", got, wantCond)
	}
	if !strings.Contains(update.Inner, `/Group[DependsOn='[//Target/ObjectID]']`) {
		t.Errorf("the MIMWAL queries table was not preserved:\n%s", update.Inner)
	}

	unique, _, err := decodeNode([]byte(sources[1]))
	if err != nil {
		t.Fatalf("preserved GenerateUniqueValue did not re-parse: %v\n%s", err, sources[1])
	}
	const wantFilter = `/*[(AccountName = '[//Value]')]`
	if got, _ := unique.attr("ConflictFilter"); got != wantFilter {
		t.Errorf("ConflictFilter = %q, want %q", got, wantFilter)
	}
}

// TestQuoteAttrValues checks the repair pass in isolation: it must quote bare
// values and leave everything that only looks like one untouched.
func TestQuoteAttrValues(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		changed        bool
	}{
		{
			name:    "bare xmlns value",
			in:      `<a xmlns=http://x/y b="1"/>`,
			want:    `<a xmlns="http://x/y" b="1"/>`,
			changed: true,
		},
		{
			name: "value ending at a self-closing tag",
			in:   `<a b=1/>`, want: `<a b="1"/>`, changed: true,
		},
		{
			name: "equals inside a quoted value is left alone",
			in:   `<a b="x=y" c='p=q'/>`, want: `<a b="x=y" c='p=q'/>`,
		},
		{
			name: "markup inside text and comments is left alone",
			in:   `<a><!-- b=c --><![CDATA[d=e]]>f=g</a>`,
			want: `<a><!-- b=c --><![CDATA[d=e]]>f=g</a>`,
		},
		{
			name: "bare value containing a quote falls back to apostrophes",
			in:   `<a b=x"y/>`, want: `<a b='x"y'/>`, changed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := quoteAttrValues([]byte(tc.in))
			if string(got) != tc.want {
				t.Errorf("quoteAttrValues() = %q, want %q", got, tc.want)
			}
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

// TestDecodeNodeKeepsOriginalError checks that input which stays broken after a
// repair still fails, and that well-formed input is never rewritten.
func TestDecodeNodeKeepsOriginalError(t *testing.T) {
	if _, _, err := decodeNode([]byte(`<a b=1`)); err == nil {
		t.Error("truncated input should still be an error")
	}
	if _, repaired, err := decodeNode([]byte(`<a b="1"/>`)); err != nil || repaired {
		t.Errorf("well-formed input needs no repair: repaired=%v err=%v", repaired, err)
	}
}
