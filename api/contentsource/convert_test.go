package contentsource

import (
	"reflect"
	"strings"
	"testing"
)

// Conversion (§ 7) and the passage rule (§ 8). wikiwikibangbang counts mark
// offsets in Unicode code points, so every text below carries a character that is
// more than one byte — a byte-offset implementation removes the wrong text here.

func para(text string, marks ...Mark) Block {
	return Block{Kind: BlockParagraph, Source: "paragraph", Text: text, Marks: marks}
}

func TestAPassageIsRemovedByItsMarkInRunes(t *testing.T) {
	// "Zugang für Büro: " is 17 runes and 19 bytes.
	text := "Zugang für Büro: PIN 4711 bitte."
	start := len([]rune("Zugang für Büro: "))
	end := start + len([]rune("PIN 4711"))
	p := Page{Body: []Block{para(text, Mark{Kind: MarkPassage, Start: start, End: end, Ref: "sec_1"})}}
	c := Convert(p)
	if strings.Contains(c.Text, "4711") || strings.Contains(c.Text, "PIN") {
		t.Fatalf("text %q still holds the passage", c.Text)
	}
	if c.Text != "Zugang für Büro: bitte." {
		t.Fatalf("text = %q", c.Text)
	}
	if !reflect.DeepEqual(c.Passages, []string{"sec_1"}) || len(c.Problems) != 0 {
		t.Fatalf("passages %v problems %v", c.Passages, c.Problems)
	}
}

// For an account that may not read a passage, the source veils it with the
// placeholder and keeps the mark over the placeholder. It is removed the same way,
// so the result is the same whether or not the account could read it.
func TestAnUnreadablePassageIsRemovedTheSameWay(t *testing.T) {
	ph := "▒▒▒▒▒▒▒▒"
	text := "Zugang für Büro: " + ph + " bitte."
	start := len([]rune("Zugang für Büro: "))
	p := Page{Body: []Block{para(text, Mark{Kind: MarkPassage, Start: start, End: start + 8, Ref: "sec_1"})}}
	c := Convert(p)
	if c.Text != "Zugang für Büro: bitte." || len(c.Problems) != 0 {
		t.Fatalf("text %q problems %v", c.Text, c.Problems)
	}
}

func TestAPlaceholderOutsideAnyPassageRefusesThePage(t *testing.T) {
	c := Convert(Page{Body: []Block{para("Geheim: ▒▒▒▒▒▒▒▒ ok")}})
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "placeholder") {
		t.Fatalf("problems = %v", c.Problems)
	}
}

func TestAMarkOutsideItsTextRefuses(t *testing.T) {
	c := Convert(Page{Body: []Block{para("kurz", Mark{Kind: MarkPassage, Start: 2, End: 40})}})
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "outside its text") {
		t.Fatalf("problems = %v", c.Problems)
	}
	c = Convert(Page{Body: []Block{para("kurz", Mark{Kind: MarkMention, Start: -1, End: 2})}})
	if len(c.Problems) != 1 {
		t.Fatalf("a mention mark before its text must refuse too: %v", c.Problems)
	}
}

func TestMentionsGoLinksKeepTheirLabel(t *testing.T) {
	text := "Frag @anna oder lies die Anleitung."
	m := Mark{Kind: MarkMention, Start: 5, End: 10}
	link := Mark{Kind: MarkOther, Start: 25, End: 34}
	c := Convert(Page{Body: []Block{para(text, m, link)}})
	if c.Text != "Frag oder lies die Anleitung." {
		t.Fatalf("text = %q", c.Text)
	}
}

func TestOnlyParagraphsHeadingsAndListItemsAreText(t *testing.T) {
	p := Page{Body: []Block{
		{Kind: BlockHeading, Source: "heading", Text: "Was es ist"},
		para("Ein VPN."),
		{Kind: BlockBullet, Source: "bullet", Text: "schnell"},
		{Kind: BlockBullet, Source: "task", Text: "sicher"},
		{Kind: BlockOther, Source: "embed"},
		{Kind: BlockOrdered, Source: "ordered", Text: "eins"},
		{Kind: BlockOrdered, Source: "ordered", Text: "zwei"},
		{Kind: BlockOther, Source: "code", Text: "rm -rf /"},
		{Kind: BlockOther, Source: "table", Text: "a\tb"},
		para("   "),
	}}
	c := Convert(p)
	want := "Was es ist\n\nEin VPN.\n\n- schnell\n- sicher\n\n1. eins\n2. zwei"
	if c.Text != want {
		t.Fatalf("text =\n%q\nwant\n%q", c.Text, want)
	}
	if !reflect.DeepEqual(c.Dropped, []string{"embed", "code", "table"}) {
		t.Fatalf("dropped = %v", c.Dropped)
	}
}

func TestKeywordsAreTheLabelsMinusTheExcluded(t *testing.T) {
	src := Source{ExcludedLabels: []string{"withdrawn"}}.Normalized()
	got := Keywords(src, []string{"VPN", "withdrawn", "remote"}, []string{"vpn", "Fernzugriff", " "})
	if !reflect.DeepEqual(got, []string{"fernzugriff", "remote", "vpn"}) {
		t.Fatalf("keywords = %v", got)
	}
}

func TestPictureNames(t *testing.T) {
	p := Page{Properties: map[string]any{"bild": "a.png, b.png\nc.png", "list": []any{"x.png", 3, " "}, "n": 5}}
	if got := PictureNames(p, "bild"); !reflect.DeepEqual(got, []string{"a.png", "b.png", "c.png"}) {
		t.Fatalf("string field = %v", got)
	}
	if got := PictureNames(p, "list"); !reflect.DeepEqual(got, []string{"x.png"}) {
		t.Fatalf("list field = %v", got)
	}
	if PictureNames(p, "n") != nil || PictureNames(p, "") != nil || PictureNames(p, "none") != nil {
		t.Fatal("a field naming nothing names no picture")
	}
	p.Properties["strs"] = []string{"s.png"}
	if got := PictureNames(p, "strs"); !reflect.DeepEqual(got, []string{"s.png"}) {
		t.Fatalf("[]string field = %v", got)
	}
}

func TestASourceIsHeldToItsShape(t *testing.T) {
	ok := Source{ID: "wiki", Name: "Wiki", Kind: KindWikiWikiBangBang, BaseURL: "https://wiki.example.org/", CredentialsRef: "wiki-token"}
	if why := ok.Problem(); why != "" {
		t.Fatalf("a good source refused: %s", why)
	}
	for name, mut := range map[string]func(*Source){
		"bad id":          func(s *Source) { s.ID = "Wiki Main" },
		"no name":         func(s *Source) { s.Name = " " },
		"unknown kind":    func(s *Source) { s.Kind = "confluence" },
		"no credential":   func(s *Source) { s.CredentialsRef = "" },
		"plain http":      func(s *Source) { s.BaseURL = "http://wiki.example.org" },
		"relative":        func(s *Source) { s.BaseURL = "/wiki" },
		"userinfo":        func(s *Source) { s.BaseURL = "https://u:p@wiki.example.org" },
		"query":           func(s *Source) { s.BaseURL = "https://wiki.example.org/?a=b" },
		"blank exclusion": func(s *Source) { s.ExcludedLabels = []string{" "} },
	} {
		s := ok
		mut(&s)
		if s.Problem() == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	n := Source{BaseURL: " https://w.example.org/// ", ExcludedLabels: []string{"B", "a", "b", " "}}.Normalized()
	if n.BaseURL != "https://w.example.org" || !reflect.DeepEqual(n.ExcludedLabels, []string{"a", "b"}) {
		t.Fatalf("normalized = %+v", n)
	}
	if !n.Excluded(" A ") || n.Excluded("c") {
		t.Fatal("Excluded")
	}
}
