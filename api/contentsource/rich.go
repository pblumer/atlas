package contentsource

import (
	"fmt"
	"slices"
	"strings"
)

// The rich description (ADR-0491, amendment of 2026-10-28): a page as the source
// lays it out — headings, paragraphs, lists, quotes, code, panels, tables,
// dividers, its images and the items it embeds — rather than as one run of text.
//
// It is data, never markup. Every block holds plain text in fields of its own and
// the shop draws it with text nodes, so nothing a wiki author writes can become
// script or style in the shop. The security rules are those of the plain
// description, applied to every block that holds text: each protected passage and
// each mention is removed by its mark before anything else is done with the text,
// a mark that does not lie in its text refuses the page, and a placeholder left
// anywhere refuses it too. An embedded item passes the rules of § 8 on its own, as
// a child page does, and is left out when it does not.

// Rich is one block of a rich description.
type Rich struct {
	// Kind is heading, paragraph, bullet, ordered, quote, code, panel, table,
	// divider, image, page (a child page: Title and Blocks) or embed (an
	// embedded item: Title and Blocks).
	Kind string `json:"kind"`
	// Level is a heading's level, 1 to 3.
	Level int `json:"level,omitempty"`
	// Text is the block's text with every protected passage and mention
	// removed; an image's caption.
	Text string `json:"text,omitempty"`
	// Checked is a task's state, for a bullet that came from one.
	Checked bool `json:"checked,omitempty"`
	// Tone is a panel's tone, as the source names it.
	Tone string `json:"tone,omitempty"`
	// Rows are a table's cells, the first row its header.
	Rows [][]string `json:"rows,omitempty"`
	// Image is an image's id in Atlas's own store: the shop never asks the
	// source for anything. Empty in a preview, which copies nothing.
	Image string `json:"image,omitempty"`
	// Width is an image's width in percent of the column, 0 for its own size;
	// Align its alignment, as the source names it.
	Width int    `json:"width,omitempty"`
	Align string `json:"align,omitempty"`
	// Title and Blocks are a child page's or an embedded item's.
	Title  string `json:"title,omitempty"`
	Blocks []Rich `json:"blocks,omitempty"`

	// owner and ref name the attachment an image shows, or the item an embed
	// shows, until a fetch resolves them.
	owner, ref string
}

// richConverted is one page's body as rich blocks, and what the conversion left
// out — the plain conversion's [Converted], for the rich description.
type richConverted struct {
	Blocks   []Rich
	Passages []string
	Dropped  []string
	Problems []string
}

// tableRows is a table's text as rows of cells: one row per line, cells by tab.
func tableRows(text string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		for i, c := range cells {
			cells[i] = tidy(c)
		}
		rows = append(rows, cells)
	}
	return rows
}

// convertRich turns a page's body into rich blocks (§ 7, § 8, as amended).
func convertRich(p Page) richConverted {
	var c richConverted
	for i, b := range p.Body {
		switch b.Kind {
		case BlockDivider:
			c.Blocks = append(c.Blocks, Rich{Kind: "divider"})
			continue
		case BlockEmbed:
			if b.Ref == "" {
				c.Dropped = append(c.Dropped, nonEmpty(b.Source, string(BlockEmbed)))
				continue
			}
			c.Blocks = append(c.Blocks, Rich{Kind: "embed", ref: b.Ref})
			continue
		case BlockOther, "":
			c.Dropped = append(c.Dropped, nonEmpty(b.Source, string(BlockOther)))
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
		if strings.ContainsRune(text, Placeholder) {
			c.Problems = append(c.Problems, fmt.Sprintf("block %d: the text holds the placeholder %s outside any "+
				"protected passage: something is veiled that is not marked as a passage", i+1, string(Placeholder)))
			continue
		}
		r := Rich{Kind: string(b.Kind)}
		switch b.Kind {
		case BlockHeading:
			r.Level = min(max(b.Level, 1), 3)
			r.Text = tidy(text)
		case BlockParagraph, BlockQuote:
			r.Text, r.Align = tidy(text), b.Align
		case BlockBullet, BlockOrdered:
			r.Text, r.Checked = tidy(text), b.Checked
		case BlockPanel:
			r.Text, r.Tone = tidy(text), b.Tone
		case BlockCode:
			// Code keeps its lines and its indentation: it is read as written.
			r.Text = strings.TrimRight(text, " \t\n")
		case BlockTable:
			r.Rows = tableRows(text)
			if len(r.Rows) == 0 {
				continue
			}
		case BlockImage:
			if b.Ref == "" {
				c.Dropped = append(c.Dropped, nonEmpty(b.Source, string(BlockImage)))
				continue
			}
			r.Text, r.Width, r.Align, r.owner, r.ref = tidy(text), b.Width, b.Align, p.ID, b.Ref
		}
		if r.Text == "" && r.Rows == nil && b.Kind != BlockImage {
			continue
		}
		c.Blocks = append(c.Blocks, r)
	}
	return c
}

// RichText is a rich description as plain text, for a reader that keeps only
// text: headings and paragraphs stand apart, list items are lines, a table is its
// rows with cells separated by " | ", a child page or an embedded item is its
// title followed by its own text. An image is its caption, where it has one.
func RichText(blocks []Rich) string {
	var parts []string
	ordinal := 0
	prev := ""
	for _, b := range blocks {
		var text string
		switch b.Kind {
		case "bullet":
			text = "- " + b.Text
		case "ordered":
			if prev != "ordered" {
				ordinal = 0
			}
			ordinal++
			text = fmt.Sprintf("%d. %s", ordinal, b.Text)
		case "table":
			var rows []string
			for _, r := range b.Rows {
				rows = append(rows, strings.Join(r, " | "))
			}
			text = strings.Join(rows, "\n")
		case "page", "embed":
			text = strings.TrimSpace(b.Title + "\n\n" + RichText(b.Blocks))
		case "divider":
			text = ""
		default:
			text = b.Text
		}
		listed := b.Kind == "bullet" || b.Kind == "ordered"
		if text != "" {
			if len(parts) > 0 && listed && (prev == "bullet" || prev == "ordered") {
				parts[len(parts)-1] += "\n" + text
			} else {
				parts = append(parts, text)
			}
		}
		prev = b.Kind
	}
	return strings.Join(parts, "\n\n")
}

// walkRich calls f for every block, depth first, with a pointer to it.
func walkRich(blocks []Rich, f func(*Rich)) {
	for i := range blocks {
		f(&blocks[i])
		walkRich(blocks[i].Blocks, f)
	}
}

// dropRich removes every block keep refuses, at every depth.
func dropRich(blocks []Rich, keep func(*Rich) bool) []Rich {
	var out []Rich
	for _, b := range blocks {
		if !keep(&b) {
			continue
		}
		b.Blocks = dropRich(b.Blocks, keep)
		out = append(out, b)
	}
	return out
}

// CopyRich is a list of blocks sharing no memory with this one.
func CopyRich(blocks []Rich) []Rich {
	if blocks == nil {
		return nil
	}
	out := make([]Rich, len(blocks))
	for i, b := range blocks {
		if b.Rows != nil {
			rows := make([][]string, len(b.Rows))
			for j, r := range b.Rows {
				rows[j] = append([]string(nil), r...)
			}
			b.Rows = rows
		}
		b.Blocks = CopyRich(b.Blocks)
		out[i] = b
	}
	return out
}
