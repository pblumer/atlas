package mail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestCleanHTMLKeepsTheMailAndDropsWhatRuns pins what a received mail's HTML loses
// before it becomes a variable (ADR-0489): everything that runs,
// embeds, posts or reloads, and every address that runs code — while its layout,
// styles, images and links stay.
func TestCleanHTMLKeepsTheMailAndDropsWhatRuns(t *testing.T) {
	src := `<!DOCTYPE html><html><head><meta http-equiv="refresh" content="0;url=https://evil.example">
<base href="https://evil.example/"><link rel="stylesheet" href="https://cdn.example/x.css">
<style>p { color: #333 }</style><script>alert(1)</script></head>
<body onload="steal()"><!-- tracking --><p class="intro" style="font-weight:bold" onclick="x()">Guten Tag <b>Anna</b></p>
<a href="https://shop.example/order/42" target="_self" rel="opener">Bestellung</a>
<a href="javascript:alert(1)">eins</a><a href="java	script:alert(2)">zwei</a><a href=" JAVASCRIPT:alert(3)">drei</a>
<a href="mailto:hilfe@example.com">Hilfe</a><a href="#oben">oben</a>
<img src="https://img.example/logo.png" alt="Logo"><img src="data:image/png;base64,iVBORw0KGgo=" alt="inline">
<img src="cid:logo@x" alt="cid"><a href="data:text/html;base64,PHNjcmlwdD4=">data</a>
<iframe src="https://evil.example"></iframe><object data="x.swf"></object><embed src="x.swf">
<form action="https://evil.example/post"><p>Im Formular</p><input name="pw"><button>Senden</button></form>
<svg><script>alert(4)</script></svg><div style="background:url(javascript:alert(5))">Hintergrund</div>
<noscript><img src="https://track.example/pixel"></noscript><table background="https://img.example/bg.png"><tr><td>Zelle</td></tr></table>
</body></html>`
	got := cleanHTML(src)
	for _, want := range []string{
		`<style>p { color: #333 }</style>`,
		`<p class="intro" style="font-weight:bold">Guten Tag <b>Anna</b></p>`,
		`<a href="https://shop.example/order/42" target="_blank" rel="noopener noreferrer">Bestellung</a>`,
		`<a href="mailto:hilfe@example.com" target="_blank" rel="noopener noreferrer">Hilfe</a>`,
		`<a href="#oben">oben</a>`,
		`<img src="https://img.example/logo.png" alt="Logo"/>`,
		`<img src="data:image/png;base64,iVBORw0KGgo=" alt="inline"/>`,
		`<img src="cid:logo@x" alt="cid"/>`,
		`<p>Im Formular</p>Senden`,
		`<table background="https://img.example/bg.png">`,
		`<!DOCTYPE html>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cleaned HTML lost %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{
		"<script", "alert(1)", "onload", "onclick", "<meta", "<base", "<link", "tracking",
		"javascript", "JAVASCRIPT", "java\tscript", "data:text/html", "<iframe", "<object", "<embed",
		"<form", "<input", "<button", "<svg", "alert(4)", "url(javascript", "<noscript", "track.example",
		`target="_self"`, `rel="opener"`,
	} {
		if strings.Contains(got, gone) {
			t.Errorf("cleaned HTML still carries %q:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, `<div>Hintergrund</div>`) {
		t.Errorf("a style that tries to run something loses the style, not the content:\n%s", got)
	}
}

func TestSafeURL(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		image bool
		ok    bool
	}{
		{"https://a.example/x", false, true},
		{"HTTP://a.example", false, true},
		{"tel:+41441234567", false, true},
		{"relative/path?x=1:2", false, true},
		{"/abs:path", false, true},
		{"vbscript:msgbox", false, false},
		{"file:///etc/passwd", false, false},
		{"data:image/gif;base64,R0lGOD", true, true},
		{"data:image/gif;base64,R0lGOD", false, false},
		{"data:text/plain,hi", true, false},
		{"\x01javascript:x", false, false},
	} {
		if got := safeURL(tc.raw, tc.image); got != tc.ok {
			t.Errorf("safeURL(%q, image=%v) = %v; want %v", tc.raw, tc.image, got, tc.ok)
		}
	}
}

// TestCapHTMLCutsBeforeATagAndStaysWhole holds the cut to what a parser can still
// read: never inside a tag, and closed again by the re-render.
func TestCapHTMLCutsBeforeATagAndStaysWhole(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString(`<p class="row">Zeile mit Ümlaut ` + strings.Repeat("x", 20) + `</p>`)
	}
	full := cleanHTML(b.String())
	got, cut := capHTML(full, 4096)
	if !cut || len(got) > 4096 {
		t.Fatalf("capHTML = %d bytes, cut %v; want at most 4096 and cut", len(got), cut)
	}
	if !strings.HasSuffix(got, "</body></html>") || strings.Count(got, "<p") != strings.Count(got, "</p>") {
		t.Errorf("a cut document must be whole again:\n…%s", got[len(got)-200:])
	}
	if same, cut := capHTML(full, len(full)); cut || same != full {
		t.Error("an HTML body within the cap is left as it is")
	}
	if tiny, cut := capHTML(full, 3); !cut || len(tiny) > 3 {
		t.Errorf("capHTML(3) = %q, %v", tiny, cut)
	}
}

func TestSetHTMLAndFields(t *testing.T) {
	var e Envelope
	e.setHTML("   ", false)
	f := fitHTML(e, 1<<20)
	if html, ok := f["html"]; !ok || html != "" || f["htmlTruncated"] != false {
		t.Errorf("a read that asked for HTML answers it even when there is none: %v", f)
	}
	e.setHTML(`<p onclick="x()">x</p>`, true)
	f = fitHTML(e, 1<<20)
	if f["htmlTruncated"] != true || !strings.Contains(f["html"].(string), "<p>x</p>") {
		t.Errorf("fitHTML = %v; a provider's cut is kept and the markup cleaned", f)
	}
	if _, ok := (Envelope{}).Fields(true)["html"]; ok {
		t.Error("a read that did not ask for HTML must not answer an html field")
	}
	// Whoever implemented the mailbox, what it answers is cleaned: an envelope built
	// outside this package cannot set withHTML and still gets an html field.
	outside := Envelope{ID: "x", HTML: "<script>alert(1)</script><b>ok</b>"}
	if f := fitHTML(outside, 1<<20); strings.Contains(f["html"].(string), "script") || !strings.Contains(f["html"].(string), "<b>ok</b>") {
		t.Errorf("fitHTML(outside) = %q", f["html"])
	}
}

// TestFitHTMLHoldsTheAnswerToTheVariableBudget measures the answer as it is stored —
// JSON, where markup grows — and cuts only the HTML until it fits.
func TestFitHTMLHoldsTheAnswerToTheVariableBudget(t *testing.T) {
	e := Envelope{ID: "m1", Subject: "Newsletter", Body: strings.Repeat("t", 1000), withHTML: true}
	e.HTML = strings.Repeat(`<p><b>&lt;</b></p>`, 3000)
	const budget = 20000
	fields := fitHTML(e, budget)
	raw, _ := json.Marshal(fields)
	if len(raw) > budget {
		t.Fatalf("answer = %d bytes as stored; want at most %d", len(raw), budget)
	}
	if fields["htmlTruncated"] != true || fields["body"] != e.Body || fields["html"] == "" {
		t.Errorf("only the HTML gives way, and says so: truncated=%v body kept=%v html empty=%v",
			fields["htmlTruncated"], fields["body"] == e.Body, fields["html"] == "")
	}
	small := Envelope{ID: "m2", withHTML: true, HTML: "<p>kurz</p>"}
	if f := fitHTML(small, budget); f["htmlTruncated"] != false || f["html"] != "<html><head></head><body><p>kurz</p></body></html>" {
		t.Errorf("an answer within the budget is left alone: %v", f)
	}
	if f := fitHTML(e, 10); f["html"] != "" {
		t.Errorf("an answer whose rest alone exceeds the budget drops the HTML: %d bytes left", len(f["html"].(string)))
	}
}

func TestIMAPGetHTML(t *testing.T) {
	f, ctls := newFakeIMAP(t)
	m := f.mailbox(t, ctls, nil)
	f.add("INBOX", msg1())
	f.add("INBOX", msg2())
	ctx := context.Background()

	got, err := m.GetHTML(ctx, "7/1/INBOX")
	if err != nil || got.Body != "Grüezi miteinand" || !strings.Contains(got.HTML, "<p>html</p>") {
		t.Fatalf("GetHTML = body %q html %q, %v; want the text part and the HTML part", got.Body, got.HTML, err)
	}
	only, err := m.GetHTML(ctx, "7/2/INBOX")
	if err != nil || !strings.Contains(only.HTML, "<b>Bob</b>") || only.Body == "" {
		t.Fatalf("an HTML-only mail = body %q html %q, %v; want its text derived and its HTML kept", only.Body, only.HTML, err)
	}
	if fields := only.Fields(true); fields["html"] == nil {
		t.Error("the answer carries the HTML")
	}
}

func TestGmailGetHTML(t *testing.T) {
	g := newGmailFake(t)
	g.messages["m1"] = gmailMsg("m1", []string{"INBOX"}, gmailPayload())
	e, err := gmailMailboxFor(t, g).(HTMLMailbox).GetHTML(context.Background(), "m1")
	if err != nil || e.Body != "Grüezi" || !strings.Contains(e.HTML, "<p>x</p>") {
		t.Fatalf("GetHTML = body %q html %q, %v", e.Body, e.HTML, err)
	}
}

func TestGraphGetHTML(t *testing.T) {
	g := newGraphFake(t, func(r graphReq) (int, any) {
		if strings.HasSuffix(r.path, "/attachments") {
			return 200, map[string]any{"value": []any{}}
		}
		return 200, graphJSONMsg("m1", "2026-10-02T06:30:00Z", map[string]any{
			"body": map[string]any{"contentType": "html", "content": `<p onclick="x()">Hallo&nbsp;<b>Welt</b></p>`},
		})
	})
	mb := graphMailboxFor(t, g).(HTMLMailbox)
	e, err := mb.GetHTML(context.Background(), "m1")
	if err != nil {
		t.Fatalf("GetHTML: %v", err)
	}
	if e.Body != "Hallo Welt" || !strings.Contains(e.HTML, "Hallo&nbsp;<b>Welt</b>") {
		t.Errorf("body %q html %q; want the text derived from the HTML and the HTML as Graph keeps it", e.Body, e.HTML)
	}
	if prefer := g.requests()[0].prefer; strings.Contains(prefer, "body-content-type") {
		t.Errorf("an HTML read must take the body as Graph keeps it, not as text: Prefer %q", prefer)
	}

	plain := newGraphFake(t, func(r graphReq) (int, any) {
		return 200, graphJSONMsg("m2", "2026-10-02T06:30:00Z", map[string]any{
			"body": map[string]any{"contentType": "text", "content": "nur Text"},
		})
	})
	e, err = graphMailboxFor(t, plain).(HTMLMailbox).GetHTML(context.Background(), "m2")
	if err != nil || e.Body != "nur Text" || e.HTML != "" || !e.withHTML {
		t.Errorf("a text-only mail = body %q html %q asked %v, %v", e.Body, e.HTML, e.withHTML, err)
	}
}

func TestHTMLFromPart(t *testing.T) {
	if got, err := htmlFromPart(nil, nil); got != "" || err != nil {
		t.Errorf("no HTML part = %q, %v", got, err)
	}
	failing := func(*mimePart) ([]byte, error) { return nil, context.DeadlineExceeded }
	if _, err := htmlFromPart(&mimePart{Type: "text", Subtype: "html"}, failing); err == nil {
		t.Error("a part that cannot be read must fail the read, not answer an empty body")
	}
	part := &mimePart{Type: "text", Subtype: "html", Encoding: "base64", Data: []byte("PHA+w6Q8L3A+")}
	if got, err := htmlFromPart(part, nil); got != "<p>ä</p>" || err != nil {
		t.Errorf("an inline base64 part = %q, %v", got, err)
	}
}
