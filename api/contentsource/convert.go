package contentsource

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Placeholder is the character a source veils a passage with. wikiwikibangbang
// writes eight of them for every passage a reader may not read (its
// SecretPlaceholder); one is enough to know that something was veiled.
const Placeholder = '▒'

// Converted is one page's body as text, and what the conversion left out.
type Converted struct {
	// Text is the page's body as plain text. Never markup: it is rendered as
	// text wherever it is shown.
	Text string
	// Passages are the protected passages removed, by the id the source gives
	// them. They are kept so a recheck can tell a passage protected after the
	// copy was taken — whose text Atlas then holds — from one it removed.
	Passages []string
	// Dropped names the blocks not taken, by the source's kind, in order.
	Dropped []string
	// Problems refuse the page: something the conversion could not make safe.
	Problems []string
}

// Convert turns a page's body into text (§ 7, § 8).
//
// The order is the rule: every protected passage is removed by its mark *before*
// anything is converted, whether or not the account could read it, so the text of
// a passage never enters Atlas. Mentions go the same way. A link keeps its label
// and loses its target, because only the text is taken. Paragraphs, headings and
// list items are text; every other block is dropped and named.
//
// Then the guard: a placeholder character left anywhere in the result means the
// source veiled something Atlas did not recognise as a passage, and the page is
// refused rather than shown with a hole whose edges might be wrong.
func Convert(p Page) Converted {
	var c Converted
	var parts []string
	ordinal := 0
	prev := BlockKind("")
	for i, b := range p.Body {
		if !textKind(b.Kind) {
			c.Dropped = append(c.Dropped, nonEmpty(b.Source, string(BlockOther)))
			prev = BlockOther
			continue
		}
		text, passages, why := strip(b)
		if why != "" {
			c.Problems = append(c.Problems, fmt.Sprintf("block %d: %s", i+1, why))
			continue
		}
		for _, ref := range passages {
			if !slices.Contains(c.Passages, ref) {
				c.Passages = append(c.Passages, ref)
			}
		}
		text = tidy(text)
		if text == "" {
			continue
		}
		switch b.Kind {
		case BlockBullet:
			text = "- " + text
		case BlockOrdered:
			if prev != BlockOrdered {
				ordinal = 0
			}
			ordinal++
			text = strconv.Itoa(ordinal) + ". " + text
		}
		// A list is one paragraph of lines; everything else stands apart.
		if len(parts) > 0 && listKind(b.Kind) && listKind(prev) {
			parts[len(parts)-1] += "\n" + text
		} else {
			parts = append(parts, text)
		}
		prev = b.Kind
	}
	c.Text = strings.Join(parts, "\n\n")
	if strings.ContainsRune(c.Text, Placeholder) {
		c.Problems = append(c.Problems, "the text holds the placeholder "+string(Placeholder)+
			" outside any protected passage: something is veiled that is not marked as a passage")
	}
	return c
}

func textKind(k BlockKind) bool {
	switch k {
	case BlockParagraph, BlockHeading, BlockBullet, BlockOrdered:
		return true
	}
	return false
}

func listKind(k BlockKind) bool { return k == BlockBullet || k == BlockOrdered }

// strip removes the text under every passage and mention mark of a block, by rune
// offset. A mark that does not lie inside its block's text cannot be removed with
// certainty, so it refuses the block rather than guessing which text it meant.
func strip(b Block) (string, []string, string) {
	runes := []rune(b.Text)
	drop := make([]bool, len(runes))
	var passages []string
	for _, m := range b.Marks {
		if m.Kind != MarkPassage && m.Kind != MarkMention {
			continue
		}
		if m.Start < 0 || m.End < m.Start || m.End > len(runes) {
			return "", nil, fmt.Sprintf("a %s mark lies outside its text (%d–%d of %d)",
				m.Kind, m.Start, m.End, len(runes))
		}
		for i := m.Start; i < m.End; i++ {
			drop[i] = true
		}
		if m.Kind == MarkPassage {
			passages = append(passages, nonEmpty(m.Ref, "?"))
		}
	}
	out := make([]rune, 0, len(runes))
	for i, r := range runes {
		if !drop[i] {
			out = append(out, r)
		}
	}
	return string(out), passages, ""
}

// tidy closes the gaps a removed passage leaves — two spaces where a word was —
// and trims the line. It does not repair the sentence: a gap is a stated cost
// (§ Consequences), and inventing words around it would be worse.
func tidy(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(' ')
			space = true
			continue
		}
		if unicode.IsSpace(r) {
			if !space {
				b.WriteRune(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// Keywords are the labels of every language page, minus the excluded labels
// (§ 7), lower-cased, sorted and once each.
func Keywords(src Source, labels ...[]string) []string {
	var out []string
	for _, set := range labels {
		for _, l := range set {
			l = strings.ToLower(strings.TrimSpace(l))
			if l == "" || src.Excluded(l) || slices.Contains(out, l) {
				continue
			}
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// PictureNames are the attachment names a page's picture field names, in order.
// A string is a list separated by commas or new lines; a list of strings is one
// name each. Anything else names nothing.
func PictureNames(p Page, field string) []string {
	if field == "" {
		return nil
	}
	var raw []string
	switch v := p.Properties[field].(type) {
	case string:
		raw = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' })
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				raw = append(raw, s)
			}
		}
	case []string:
		raw = v
	}
	var out []string
	for _, n := range raw {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}
