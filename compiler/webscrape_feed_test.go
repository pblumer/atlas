package compiler

import (
	"strings"
	"testing"
)

// parseWebScrapeProcess compiles a one-task model carrying the given extension. It
// returns the whole compiled process, because a field list is read back through its
// string table (Intern) and not from the detail alone.
func parseWebScrapeProcess(t *testing.T, ext string) *CompiledProcess {
	t.Helper()
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + ext + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// webScrapeDetail is the compiled connector detail of that model's single task.
func webScrapeDetail(t *testing.T, cp *CompiledProcess) *ConnectorTaskDetail {
	t.Helper()
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp.ConnectorTask(cp.Node(task).Detail)
}

func parseWebScrapeExtension(t *testing.T, ext string) *ConnectorTaskDetail {
	t.Helper()
	return webScrapeDetail(t, parseWebScrapeProcess(t, ext))
}

func parseWebScrapeError(t *testing.T, ext, want string) {
	t.Helper()
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + ext + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Parse error = %v, want one containing %q", err, want)
	}
}

func TestWebScrapeHTMLDefaultRemainsCompatible(t *testing.T) {
	d := parseWebScrapeExtension(t, `<atlas:webscrapeConnector url="https://example.com" selector=".headline" attribute="href" resultVariable="r"/>`)
	if d.ScrapeFormat != WebScrapeFormatHTML {
		t.Errorf("format = %v, want HTML default", d.ScrapeFormat)
	}
	if d.ScrapeMaxItems != 0 {
		t.Errorf("maxItems = %d, want unlimited", d.ScrapeMaxItems)
	}
}

func TestWebScrapeFormatsAndMaxItemsCompile(t *testing.T) {
	cases := []struct {
		name   string
		ext    string
		format WebScrapeFormat
		max    int32
	}{
		{"html", `<atlas:webscrapeConnector url="https://example.com" format="html" selector=".x" maxItems="2" resultVariable="r"/>`, WebScrapeFormatHTML, 2},
		{"rss", `<atlas:webscrapeConnector url="https://example.com/rss.xml" format="rss" maxItems="10" resultVariable="r"/>`, WebScrapeFormatRSS, 10},
		{"atom", `<atlas:webscrapeConnector url="https://example.com/atom.xml" format="atom" maxItems="1" resultVariable="r"/>`, WebScrapeFormatAtom, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := parseWebScrapeExtension(t, tc.ext)
			if d.ScrapeFormat != tc.format || d.ScrapeMaxItems != tc.max {
				t.Fatalf("compiled format/max = %v/%d, want %v/%d", d.ScrapeFormat, d.ScrapeMaxItems, tc.format, tc.max)
			}
		})
	}
}

func TestWebScrapeFeedValidation(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		want string
	}{
		{"unknown format", `<atlas:webscrapeConnector url="https://x" format="json" resultVariable="r"/>`, "unknown format"},
		{"rss selector", `<atlas:webscrapeConnector url="https://x" format="rss" selector="item" resultVariable="r"/>`, "selector"},
		{"atom attribute", `<atlas:webscrapeConnector url="https://x" format="atom" attribute="href" resultVariable="r"/>`, "attribute"},
		{"rss feel selector", `<atlas:webscrapeConnector url="https://x" format="rss" selector="=sel" resultVariable="r"/>`, "selector"},
		{"nonnumeric maxItems", `<atlas:webscrapeConnector url="https://x" format="rss" maxItems="many" resultVariable="r"/>`, "non-numeric maxItems"},
		{"negative maxItems", `<atlas:webscrapeConnector url="https://x" format="atom" maxItems="-1" resultVariable="r"/>`, "negative maxItems"},
		{"html without selector", `<atlas:webscrapeConnector url="https://x" format="html" resultVariable="r"/>`, "needs a selector"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { parseWebScrapeError(t, tc.ext, tc.want) })
	}
}

func TestWebScrapeFormatString(t *testing.T) {
	for format, want := range map[WebScrapeFormat]string{
		WebScrapeFormatHTML: "html",
		WebScrapeFormatRSS:  "rss",
		WebScrapeFormatAtom: "atom",
	} {
		if got := format.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", format, got, want)
		}
	}
	if got := WebScrapeFormat(99).String(); got != "" {
		t.Errorf("unknown format String() = %q, want empty", got)
	}
}
