package mail

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Cleaning a received mail's HTML body before it becomes a process variable
// (ADR-0489).
//
// The HTML is written by whoever sent the mail. It is shown to a person in a task form,
// inside a sandboxed frame that runs no script and loads nothing until that person asks
// for the images — that frame is the guarantee. Cleaning here is the second line, and
// it is about what the variable carries: what a process receives is in the log, every
// checkpoint, the exporter and every backup (ADR-0438), and a script, a form posting
// elsewhere or a refresh to another address has no business there, whoever renders the
// variable later and however carefully.
//
// It is a parse and a re-render, never string surgery: the result is the tree the
// browser would have built, with the dangerous parts removed, so nothing can hide in a
// malformed tag. Styles stay — a mail is laid out with them — and so do links, which
// open in a new tab; addresses that run code (javascript:, vbscript:) or carry a
// document (data: other than an image) are dropped.

// dropped are the elements removed together with everything inside them: what runs,
// what embeds another document, what loads from elsewhere on its own, and what changes
// how the document itself is read.
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Noscript: true, atom.Template: true,
	atom.Iframe: true, atom.Frame: true, atom.Frameset: true,
	atom.Object: true, atom.Embed: true, atom.Applet: true, atom.Param: true,
	atom.Base: true, atom.Link: true, atom.Meta: true,
	atom.Svg: true, atom.Math: true,
	atom.Audio: true, atom.Video: true, atom.Source: true, atom.Track: true,
	atom.Canvas: true, atom.Dialog: true,
	atom.Input: true, atom.Select: true, atom.Textarea: true, atom.Option: true,
}

// unwrapped are the elements removed while what they hold stays: a form around a
// newsletter's content, a button with a label.
var unwrapped = map[atom.Atom]bool{
	atom.Form: true, atom.Button: true, atom.Fieldset: true, atom.Label: true,
}

// urlAttrs are the attributes that hold an address the browser would fetch or follow.
var urlAttrs = map[string]bool{
	"href": true, "src": true, "background": true, "poster": true, "cite": true,
	"longdesc": true, "action": true, "formaction": true, "xlink:href": true, "lowsrc": true, "dynsrc": true,
}

// cleanHTML parses an HTML body and renders it back without what has no place in a
// variable a person will look at. The parser accepts any input, as a browser does, and
// fails only when its reader does, which a string's cannot; nor can a render into a
// strings.Builder.
func cleanHTML(src string) string {
	doc, _ := html.Parse(strings.NewReader(src))
	cleanNode(doc)
	var b strings.Builder
	_ = html.Render(&b, doc)
	return b.String()
}

func cleanNode(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode:
			n.RemoveChild(c)
		case c.Type == html.ElementNode && (dropped[c.DataAtom] || c.Namespace != ""):
			// A foreign namespace is SVG or MathML that slipped in by another name;
			// neither belongs in a mail a person reads.
			n.RemoveChild(c)
		case c.Type == html.ElementNode && unwrapped[c.DataAtom]:
			cleanNode(c)
			for gc := c.FirstChild; gc != nil; {
				gnext := gc.NextSibling
				c.RemoveChild(gc)
				n.InsertBefore(gc, c)
				gc = gnext
			}
			n.RemoveChild(c)
		case c.Type == html.ElementNode:
			c.Attr = cleanAttrs(c)
			cleanNode(c)
		}
		c = next
	}
}

// cleanAttrs keeps an element's attributes except handlers, addresses that run code and
// styles that try to; a link opens in a new tab, detached from the page it came from.
func cleanAttrs(n *html.Node) []html.Attribute {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" {
			key = strings.ToLower(a.Namespace) + ":" + key
		}
		switch {
		case strings.HasPrefix(key, "on"), key == "srcdoc", key == "srcset", key == "ping", key == "http-equiv":
			continue
		case key == "target" || key == "rel":
			continue // set below for a link, meaningless elsewhere
		case urlAttrs[key] && !safeURL(a.Val, key == "src" || key == "background" || key == "poster"):
			continue
		case key == "style" && unsafeStyle(a.Val):
			continue
		}
		out = append(out, a)
	}
	if (n.DataAtom == atom.A || n.DataAtom == atom.Area) && !inPage(out) {
		out = append(out, html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
	}
	return out
}

// inPage reports a link to a place in the mail itself, which stays in the frame it is in.
func inPage(attrs []html.Attribute) bool {
	for _, a := range attrs {
		if strings.EqualFold(a.Key, "href") {
			return strings.HasPrefix(strings.TrimSpace(a.Val), "#")
		}
	}
	return false
}

// safeURL reports whether an address may stay: one without a scheme (an anchor, a path
// that resolves to nothing in a frame of its own), or one of the schemes a mail links or
// shows images with. A data: address stays only as an image.
func safeURL(raw string, image bool) bool {
	u := strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1 // what browsers ignore inside a scheme, "java\tscript:" included
		}
		return r
	}, raw)
	colon := strings.IndexByte(u, ':')
	if colon < 0 || strings.ContainsAny(u[:colon], "/?#") {
		return true
	}
	switch scheme := strings.ToLower(u[:colon]); scheme {
	case "http", "https", "mailto", "tel", "cid":
		return true
	case "data":
		return image && strings.HasPrefix(strings.ToLower(u[colon+1:]), "image/")
	default:
		return false
	}
}

// unsafeStyle reports a style that tries to run something. Browsers no longer run any of
// these; a variable still has no reason to carry them.
func unsafeStyle(v string) bool {
	s := strings.ToLower(strings.Join(strings.Fields(v), ""))
	return strings.Contains(s, "javascript:") || strings.Contains(s, "vbscript:") ||
		strings.Contains(s, "expression(") || strings.Contains(s, "-moz-binding") || strings.Contains(s, "behavior:")
}

// capHTML cuts a cleaned HTML body to at most max bytes and cleans the cut again, so
// what is left is still a whole document: the cut falls before a tag, never inside
// one, and the parser closes what the cut left open.
func capHTML(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	step := max / 8
	if step < 1 {
		step = 1
	}
	for limit := max; limit > 0; limit -= step {
		n := limit
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		if lt := strings.LastIndexByte(s[:n], '<'); lt > 0 {
			n = lt
		}
		if out := cleanHTML(s[:n]); len(out) <= max {
			return out, true
		}
	}
	return "", true
}
