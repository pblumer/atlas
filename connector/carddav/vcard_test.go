package carddav

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// appleCard is a contact as Apple Contacts writes it to iCloud: vCard 3.0, a photo
// folded over several lines, a custom label on a grouped e-mail address, and the
// yearless birthday iCloud stores with X-APPLE-OMIT-YEAR.
const appleCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"PRODID:-//Apple Inc.//iPhone OS 17.0//EN\r\n" +
	"N:Muster;Anna;Maria;Dr.;\r\n" +
	"FN:Dr. Anna Maria Muster\r\n" +
	"ORG:Firma AG;Verkauf;\r\n" +
	"TITLE:Leiterin\r\n" +
	"EMAIL;type=INTERNET;type=WORK;type=pref:anna@firma.ch\r\n" +
	"item1.EMAIL;type=INTERNET:anna@privat.ch\r\n" +
	"item1.X-ABLabel:Privat2\r\n" +
	"TEL;type=CELL;type=VOICE;type=pref:+41 79 123 45 67\r\n" +
	"TEL;type=WORK;type=VOICE:044 123 45 67\r\n" +
	"item2.ADR;type=WORK;type=pref:;;Bahnhofstrasse 1;Zürich;ZH;8001;Schweiz\r\n" +
	"item2.X-ABADR:ch\r\n" +
	"BDAY;X-APPLE-OMIT-YEAR=1604:1604-05-17\r\n" +
	"NOTE:Erste Zeile\\nzweite\\, mit Komma\r\n" +
	"X-Kundennummer:4711\r\n" +
	"X-ABUID:1A2B3C4D-0000-0000-0000-000000000000:ABPerson\r\n" +
	"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0\r\n" +
	" Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIy\r\n" +
	" MjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAA\r\n" +
	"UID:1A2B3C4D-0000-0000-0000-000000000000\r\n" +
	"END:VCARD\r\n"

func mustParse(t *testing.T, s string) *Card {
	t.Helper()
	c, err := ParseCard([]byte(s))
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	return c
}

func TestParseCardReadsWhatAppleWrites(t *testing.T) {
	c := mustParse(t, appleCard)
	if c.Version() != "3.0" {
		t.Errorf("version = %q", c.Version())
	}
	if got := c.First("fn").Text(); got != "Dr. Anna Maria Muster" {
		t.Errorf("FN = %q", got)
	}
	emails := c.All("EMAIL")
	if len(emails) != 2 || emails[1].Group != "item1" || emails[1].Text() != "anna@privat.ch" {
		t.Fatalf("emails = %+v", emails)
	}
	if !emails[0].HasType("pref") || !emails[0].HasType("work") || emails[1].HasType("pref") {
		t.Error("TYPE values are not read across repeated params")
	}
	if got := c.First("NOTE").Text(); got != "Erste Zeile\nzweite, mit Komma" {
		t.Errorf("NOTE = %q", got)
	}
	adr := c.First("ADR").Components()
	if len(adr) != 7 || adr[2] != "Bahnhofstrasse 1" || adr[3] != "Zürich" || adr[6] != "Schweiz" {
		t.Errorf("ADR = %q", adr)
	}
	photo := c.First("PHOTO")
	if !strings.HasPrefix(photo.Value, "/9j/") || !strings.HasSuffix(photo.Value, "/8QAFQABAQAA") || strings.Contains(photo.Value, " ") {
		t.Errorf("the folded photo was not unfolded: %q", photo.Value)
	}
	if got := c.First("bday").Param("X-APPLE-OMIT-YEAR"); len(got) != 1 || got[0] != "1604" {
		t.Errorf("BDAY param = %v", got)
	}
}

// A card that is written back without a change is the card that was read, property
// for property — the photo, Apple's labels and its private X-ABUID included.
func TestUnchangedCardSerializesToWhatWasRead(t *testing.T) {
	c := mustParse(t, appleCard)
	again := mustParse(t, string(c.Bytes()))
	if len(again.Props) != len(c.Props) {
		t.Fatalf("%d properties became %d", len(c.Props), len(again.Props))
	}
	for i, p := range c.Props {
		q := again.Props[i]
		if p.Group != q.Group || p.Name != q.Name || p.Value != q.Value || len(p.Params) != len(q.Params) {
			t.Errorf("property %d: %+v became %+v", i, p, q)
		}
	}
	for _, line := range strings.Split(string(c.Bytes()), "\r\n") {
		if len(line) > 75 {
			t.Errorf("a line is longer than 75 octets: %q", line)
		}
	}
	// Unfolded, every untouched line is byte for byte the one that was read.
	unfolded := strings.ReplaceAll(string(c.Bytes()), "\r\n ", "")
	for _, line := range strings.Split(strings.ReplaceAll(appleCard, "\r\n ", ""), "\r\n") {
		if line != "" && !strings.Contains(unfolded, line+"\r\n") {
			t.Errorf("the line %q did not survive", line)
		}
	}
}

func TestParseCardTolerance(t *testing.T) {
	// LF line ends, a tab continuation, a quoted parameter holding ; and :, a bare
	// vCard 2.1-style parameter, lower-case names and a line with no colon.
	c := mustParse(t, "begin:vcard\nversion:4.0\nfn:Anna\n\tMuster\nemail;pref=1;label=\"Büro; 2. Stock: links\":a@b.ch\ntel;HOME:044\nkaputt\nend:vcard\n")
	if c.Version() != "4.0" || c.First("FN").Text() != "AnnaMuster" {
		t.Errorf("version %q FN %q", c.Version(), c.First("FN").Text())
	}
	e := c.First("EMAIL")
	if got := e.Param("LABEL"); len(got) != 1 || got[0] != "Büro; 2. Stock: links" {
		t.Errorf("quoted param = %v", got)
	}
	if e.Text() != "a@b.ch" || e.prefRank() != 1 {
		t.Errorf("EMAIL %q rank %d", e.Text(), e.prefRank())
	}
	if !c.First("TEL").HasType("home") {
		t.Error("a bare parameter is a TYPE")
	}
	// The line with no colon is kept as it was, and found by no name.
	if !strings.Contains(string(c.Bytes()), "kaputt\r\n") {
		t.Errorf("an unparsable line was dropped:\n%s", c.Bytes())
	}
	for _, bad := range []string{"", "VERSION:3.0\r\n", "BEGIN:VCARD\r\nFN:x\r\n", "END:VCARD\r\nBEGIN:VCARD\r\n"} {
		if _, err := ParseCard([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	// What precedes BEGIN and follows END is not the card.
	c = mustParse(t, "junk\r\nBEGIN:VCARD\r\nFN:x\r\nEND:VCARD\r\nBEGIN:VCARD\r\nFN:y\r\nEND:VCARD\r\n")
	if len(c.All("FN")) != 1 || c.First("FN").Text() != "x" {
		t.Errorf("FN = %+v", c.All("FN"))
	}
}

func TestEscaping(t *testing.T) {
	for raw, text := range map[string]string{
		`a\,b\;c\\d\ne\Nf`: "a,b;c\\d\ne\nf",
		`kein escape`:      "kein escape",
		`a\:b`:             "a:b",
		`trailing\`:        `trailing\`,
	} {
		if got := unescapeText(raw); got != text {
			t.Errorf("unescape(%q) = %q, want %q", raw, got, text)
		}
	}
	if got := escapeText("a,b;c\\d\r\ne\nf"); got != `a\,b\;c\\d\ne\nf` {
		t.Errorf("escape = %q", got)
	}
	if got := splitEscaped(`a\;b;c;;d\\;e`, ';'); strings.Join(got, "|") != `a\;b|c||d\\|e` {
		t.Errorf("split = %q", got)
	}
	if got := joinComponents([]string{"Weg 1, Haus 2", "", "Bern;Stadt"}); got != `Weg 1\, Haus 2;;Bern\;Stadt` {
		t.Errorf("join = %q", got)
	}
}

func TestChangedPropertyIsWrittenFromItsParts(t *testing.T) {
	c := mustParse(t, appleCard)
	e := c.All("EMAIL")[0]
	e.SetText("neu@firma.ch")
	e.SetParam("TYPE", "WORK")
	note := c.First("NOTE")
	note.SetText(strings.Repeat("ä", 60))
	out := string(c.Bytes())
	if !strings.Contains(out, "EMAIL;TYPE=WORK:neu@firma.ch\r\n") {
		t.Errorf("changed EMAIL not written from its parts:\n%s", out)
	}
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line over 75 octets: %q", line)
		}
		if !utf8.ValidString(line) {
			t.Errorf("a fold split a character: %q", line)
		}
	}
	if got := mustParse(t, out).First("NOTE").Text(); got != strings.Repeat("ä", 60) {
		t.Errorf("NOTE round trip = %q", got)
	}
	// A parameter value holding a separator is quoted; a quote it cannot hold is dropped.
	p := &Prop{Name: "ADR"}
	p.SetParam("LABEL", `Weg 1; "Haus" 2`)
	p.SetComponents([]string{"", "", "Weg 1"})
	if got := p.line(); got != `ADR;LABEL="Weg 1; Haus 2":;;Weg 1` {
		t.Errorf("line = %q", got)
	}
}

func TestRemoveTakesItsAppleLabelAlong(t *testing.T) {
	c := mustParse(t, appleCard)
	c.Remove(c.All("EMAIL")[1])
	out := string(c.Bytes())
	if strings.Contains(out, "item1.") {
		t.Errorf("the label of a removed property stayed behind:\n%s", out)
	}
	if !strings.Contains(out, "item2.X-ABADR:ch") {
		t.Error("another group's label was removed")
	}
}

func TestNewCardIsVCard3(t *testing.T) {
	c := NewCard("ABC")
	c.Add(&Prop{Name: "FN"}).SetText("Anna")
	out := string(c.Bytes())
	for _, want := range []string{"BEGIN:VCARD\r\n", "VERSION:3.0\r\n", "PRODID:" + prodID + "\r\n", "UID:ABC\r\n", "FN:Anna\r\n", "END:VCARD\r\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if !strings.HasPrefix(out, "BEGIN:VCARD\r\nVERSION:3.0\r\n") {
		t.Errorf("VERSION must follow BEGIN:\n%s", out)
	}
}
