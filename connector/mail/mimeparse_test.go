package mail

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseAuthResultsTakesTheTopmostHeader(t *testing.T) {
	got := parseAuthResults([]string{
		"mx.google.com; dkim=pass header.i=@example.com header.s=s1; " +
			"spf=pass (google.com: domain of a@example.com; designates 1.2.3.4) smtp.mailfrom=a@example.com; " +
			"dmarc=pass (p=REJECT sp=REJECT dis=NONE) header.from=example.com",
		// Lower down, so written before the receiving server saw the message — by the
		// sender, possibly. It must not be what the verdict is read from.
		"attacker.example; dmarc=pass; spf=pass",
	})
	if got != (AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"}) {
		t.Errorf("parseAuthResults = %+v", got)
	}
	forged := parseAuthResults([]string{"mx.example; dmarc=fail", "attacker.example; dmarc=pass"})
	if forged.DMARC != "fail" {
		t.Errorf("the verdict must come from the topmost header, got dmarc=%q", forged.DMARC)
	}
}

func TestParseAuthResultsMicrosoftShape(t *testing.T) {
	// Exchange Online writes no authserv-id in front; the first segment is a result.
	got := parseAuthResults([]string{
		"spf=pass (sender IP is 192.0.2.1) smtp.mailfrom=example.com; dkim=pass (signature was verified) " +
			"header.d=example.com;dmarc=bestguesspass action=none header.from=example.com;compauth=pass reason=109",
	})
	if got != (AuthResults{SPF: "pass", DKIM: "pass", DMARC: "bestguesspass"}) {
		t.Errorf("parseAuthResults = %+v", got)
	}
}

func TestParseAuthResultsEdges(t *testing.T) {
	if got := parseAuthResults(nil); got != (AuthResults{}) {
		t.Errorf("no header = %+v, want empty", got)
	}
	got := parseAuthResults([]string{"mx.example 1; DKIM=Pass header.d=a; dkim=fail header.d=b; spf=SoftFail"})
	if got.DKIM != "pass" || got.SPF != "softfail" || got.DMARC != "" {
		t.Errorf("parseAuthResults = %+v: the first result of a method wins, case is folded, and an absent method stays empty", got)
	}
	if got := parseAuthResults([]string{"mx.example; none"}); got != (AuthResults{}) {
		t.Errorf("an explicit none = %+v, want empty", got)
	}
}

func TestDecodeHeader(t *testing.T) {
	cases := map[string]string{
		"=?ISO-8859-1?Q?Gr=FCezi?=":       "Grüezi",
		"=?UTF-8?B?w6TDtsO8?=":            "äöü",
		"=?windows-1252?Q?=80_Rechnung?=": "€ Rechnung",
		"plain subject":                   "plain subject",
		"=?x-unknown?Q?abc?=":             "=?x-unknown?Q?abc?=",
	}
	for in, want := range cases {
		if got := decodeHeader(in); got != want {
			t.Errorf("decodeHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeContent(t *testing.T) {
	cases := []struct {
		raw, enc, charset, want string
	}{
		{"Gr=FCezi =\r\nmiteinand", "quoted-printable", "iso-8859-1", "Grüezi miteinand"},
		{"w6TDtsO8", "base64", "utf-8", "äöü"},
		{"w6TDtsO8\r\n", "BASE64", "", "äöü"},
		{"plain", "7bit", "us-ascii", "plain"},
		{"\x80", "8bit", "windows-1252", "€"},
		{"keep", "", "x-no-such-charset", "keep"},
	}
	for _, c := range cases {
		if got := decodeContent([]byte(c.raw), c.enc, c.charset); got != c.want {
			t.Errorf("decodeContent(%q, %q, %q) = %q, want %q", c.raw, c.enc, c.charset, got, c.want)
		}
	}
	// Bytes that are no text in any charset still come back as valid UTF-8, because
	// they become a variable and a variable is text.
	if got := decodeContent([]byte{0xff, 0xfe, 'a'}, "", "x-no-such-charset"); !utf8.ValidString(got) {
		t.Errorf("decodeContent returned invalid UTF-8: %q", got)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText(`<html><head><style>p{x:y}</style><title>t</title></head><body>` +
		`<p>Hallo&nbsp;<b>Welt</b></p><script>alert(1)</script><div>Zeile&nbsp;2<br>Zeile 3</div>` +
		`<ul><li>eins</li><li>zwei</li></ul></body></html>`)
	want := "Hallo Welt\nZeile 2\nZeile 3\neins\nzwei"
	if got != want {
		t.Errorf("htmlToText = %q, want %q", got, want)
	}
}

func TestCapBody(t *testing.T) {
	if got, cut := capBody("short"); got != "short" || cut {
		t.Errorf("capBody(short) = %q, %v", got, cut)
	}
	long := strings.Repeat("a", MaxBodyBytes+10)
	if got, cut := capBody(long); len(got) != MaxBodyBytes || !cut {
		t.Errorf("capBody(long) = %d bytes, cut %v", len(got), cut)
	}
	// Cut on a rune boundary: half a character would be invalid UTF-8 in a variable.
	umlauts := strings.Repeat("ä", MaxBodyBytes)
	got, cut := capBody(umlauts)
	if !cut || !utf8.ValidString(got) || len(got) > MaxBodyBytes {
		t.Errorf("capBody(umlauts) = %d bytes, valid %v, cut %v", len(got), utf8.ValidString(got), cut)
	}
}

func TestParseAddressLists(t *testing.T) {
	addrs := parseAddressList("Anna Muster <Anna@Example.com>, bob@example.com")
	if strings.Join(addrs, ",") != "anna@example.com,bob@example.com" {
		t.Errorf("parseAddressList = %q", addrs)
	}
	if got := parseAddressList("undisclosed-recipients:;"); len(got) != 0 {
		t.Errorf("an empty group = %q, want none", got)
	}
	if got := parseAddressList(""); len(got) != 0 {
		t.Errorf("no header = %q", got)
	}
	if got := parseAddressList("=?UTF-8?B?w6TDtsO8?= <x@example.com>"); strings.Join(got, ",") != "x@example.com" {
		t.Errorf("an encoded display name = %q", got)
	}
	// Something net/mail cannot parse is kept as written rather than dropped: a
	// recipient nobody can see is worse than one that looks odd.
	if got := parseAddressList("broken <<x@example.com"); len(got) != 1 {
		t.Errorf("an unparseable list = %q, want it kept", got)
	}
	addr, name := parseAddress("=?ISO-8859-1?Q?J=FCrg?= <JUERG@example.com>")
	if addr != "juerg@example.com" || name != "Jürg" {
		t.Errorf("parseAddress = %q, %q", addr, name)
	}
	if addr, name := parseAddress(""); addr != "" || name != "" {
		t.Errorf("parseAddress(empty) = %q, %q", addr, name)
	}
}

func TestMimePartSelection(t *testing.T) {
	root := mimePart{Type: "multipart", Subtype: "mixed", Children: []mimePart{
		{Type: "multipart", Subtype: "alternative", Children: []mimePart{
			{Type: "text", Subtype: "plain", Section: "1.1", Params: map[string]string{"charset": "utf-8"}},
			{Type: "text", Subtype: "html", Section: "1.2"},
		}},
		{Type: "application", Subtype: "pdf", Section: "2", Disposition: "attachment",
			DispParams: map[string]string{"filename": "Rechnung.pdf"}, Size: 1000},
		{Type: "image", Subtype: "png", Section: "3", Disposition: "inline",
			Params: map[string]string{"name": "logo.png"}, Size: 50},
		// A text part that is an attachment is a document, not the body.
		{Type: "text", Subtype: "plain", Section: "4", Disposition: "attachment",
			DispParams: map[string]string{"filename": "notes.txt"}, Size: 7},
	}}
	plain, html := root.bodyParts()
	if plain == nil || plain.Section != "1.1" || html == nil || html.Section != "1.2" {
		t.Fatalf("bodyParts = %+v, %+v", plain, html)
	}
	atts := root.attachments()
	var names []string
	for _, a := range atts {
		names = append(names, a.Name+"|"+a.ContentType)
	}
	if strings.Join(names, ",") != "Rechnung.pdf|application/pdf,logo.png|image/png,notes.txt|text/plain" {
		t.Errorf("attachments = %q", names)
	}
	// An HTML-only message has no plain part; the HTML part is what a body is made from.
	htmlOnly := mimePart{Type: "text", Subtype: "html", Section: "1"}
	if p, h := htmlOnly.bodyParts(); p != nil || h == nil {
		t.Errorf("html-only bodyParts = %+v, %+v", p, h)
	}
	// A forwarded message is an attachment, named after nothing it carries.
	fwd := mimePart{Type: "multipart", Subtype: "mixed", Children: []mimePart{
		{Type: "text", Subtype: "plain", Section: "1"},
		{Type: "message", Subtype: "rfc822", Section: "2", Size: 300},
	}}
	if atts := fwd.attachments(); len(atts) != 1 || atts[0].Name != "message.eml" {
		t.Errorf("forwarded attachments = %+v", atts)
	}
}

func TestParamValueDecodesBothEncodings(t *testing.T) {
	cases := []struct {
		params map[string]string
		want   string
	}{
		{map[string]string{"filename*": "utf-8'de'Rechnung%20M%C3%A4rz.pdf"}, "Rechnung März.pdf"},
		{map[string]string{"filename": "=?UTF-8?Q?Gr=C3=BCsse.txt?="}, "Grüsse.txt"},
		{map[string]string{"filename": "plain.pdf"}, "plain.pdf"},
		{map[string]string{"filename*": "garbage", "filename": "fallback.pdf"}, "fallback.pdf"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := paramValue(c.params, "filename"); got != c.want {
			t.Errorf("paramValue(%v) = %q, want %q", c.params, got, c.want)
		}
	}
}
