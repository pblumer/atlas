package releasenotes

import (
	"regexp"
	"strings"
	"unicode"
)

const repoURL = "https://github.com/pblumer/atlas"

// Summary is one release as the index lists it: enough to draw its row, without the
// changes the row expands into.
type Summary struct {
	Version     string `json:"version"`
	Date        string `json:"date,omitempty"`
	ChangeCount int    `json:"changeCount"`
}

// Release is one version's section of the CHANGELOG.
type Release struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	// Intro is what the section says before its first category: the release's own
	// account of itself and, where there is one, what to read before upgrading.
	Intro   []Block  `json:"intro"`
	Changes []Change `json:"changes"`
	// Link is the section in the CHANGELOG — as it was tagged, for a released version.
	Link Link `json:"link"`
}

// Block is a paragraph or a list of a release's intro. Text keeps two pieces of
// inline markdown, **bold** and `code`, for the Console to render; a link is reduced
// to its text.
type Block struct {
	Kind  string   `json:"kind"` // "p" or "list"
	Text  string   `json:"text,omitempty"`
	Items []string `json:"items,omitempty"`
}

// Change is one bullet under a category heading.
type Change struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	// Text is the rest of the bullet's first paragraph. Further paragraphs and nested
	// lists stay in the CHANGELOG, which the release links to.
	Text string `json:"text"`
	Link *Link  `json:"link,omitempty"`
}

// Link is where a change or a release points.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Notes is a parsed CHANGELOG.
type Notes struct {
	releases []Release
	byVer    map[string]int
}

// Releases lists every release that says something, newest first.
func (n *Notes) Releases() []Summary {
	out := make([]Summary, 0, len(n.releases))
	for _, r := range n.releases {
		out = append(out, Summary{Version: r.Version, Date: r.Date, ChangeCount: len(r.Changes)})
	}
	return out
}

// Release returns one release by its version, "Unreleased" included.
func (n *Notes) Release(version string) (Release, bool) {
	i, ok := n.byVer[version]
	if !ok {
		return Release{}, false
	}
	return n.releases[i], true
}

var (
	versionHead = regexp.MustCompile(`^## \[([^\]]+)\](?:\s+[—-]\s+(\d{4}-\d{2}-\d{2}))?`)
	categoryHd  = regexp.MustCompile(`^### (\S.*?)\s*$`)
	boldTitle   = regexp.MustCompile(`^\*\*(.+?)\*\*`)
	// leadIn strips the "(ref):" that regularly follows a bold headline. It allows one
	// level of nesting on purpose: the lead-in usually contains a parenthesis of its
	// own — "([ADR-0163](docs/adr/….md))" — and a flat \([^)]*\) stops at the link's
	// closing paren, leaving the text starting "): …".
	leadIn    = regexp.MustCompile(`^\s*\((?:[^()]|\([^()]*\))*\)\s*[:—-]?\s*`)
	leadPunct = regexp.MustCompile(`^\s*[.:—-]\s*`)
	runsOn    = regexp.MustCompile(`^\s*[,;]`)
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	space     = regexp.MustCompile(`\s+`)
	sentence  = regexp.MustCompile(`^(.*?[.!?])(\s|$)`)
	// nestedItem is the start of an item of a list nested in a bullet.
	nestedItem = regexp.MustCompile(`^([-*+]|\d+\.)\s`)

	prRef     = regexp.MustCompile(`\(#(\d+)\)`)
	adrLinked = regexp.MustCompile(`\]\((docs/adr/(\d{4})-[^)]+\.md)\)`)
	// adrRef is a citation of a record, as a link to its file (group 1) or bare
	// (group 2). The link comes first so that its label is not read a second time.
	adrRef = regexp.MustCompile(`\[[^\]]*\]\(docs/adr/(\d{4})-[^)]+\.md\)|\bADR-(\d{4})\b`)
	// citeGap is what may stand between two citations of one closing group, and
	// citeEnd what may follow the last: "… (ADR-0352, ADR-0237)." or "… here. ADR-0438."
	citeGap = regexp.MustCompile(`^[\s,;]*(?:and\s+)?$`)
	citeEnd = regexp.MustCompile(`^[\s.)\]]*$`)
)

// indexRow is a row of the decision records' index: "| [0438](0438-mailbox-worker.md) | …".
var indexRow = regexp.MustCompile(`(?m)^\| \[(\d{4})\]\((\d{4}-[^)/]+\.md)\)`)

// Records reads the decision records' index (docs/adr/README.md) into the path of the
// file each record number names.
func Records(index string) map[string]string {
	out := map[string]string{}
	for _, m := range indexRow.FindAllStringSubmatch(index, -1) {
		out[m[1]] = "docs/adr/" + m[2]
	}
	return out
}

// Parse reads a Keep a Changelog document. records maps a record number to the path of
// its file (Records); a citation of a number it does not hold resolves through a link
// to that record elsewhere in the document, or to the records' directory.
//
// It never fails: a line it does not recognise is a line it leaves out, and the guard
// on the embedded CHANGELOG (TestTheEmbeddedChangelogReadsWhole) is what notices a
// section it read wrongly.
func Parse(src string, records map[string]string) *Notes {
	p := parser{adrPaths: map[string]string{}}
	for n, path := range records {
		p.adrPaths[n] = path
	}
	// A record spelled out with its path anywhere in the document resolves a bare
	// citation of it everywhere, since the file name cannot be derived from the number.
	for _, m := range adrLinked.FindAllStringSubmatch(src, -1) {
		if _, seen := p.adrPaths[m[2]]; !seen {
			p.adrPaths[m[2]] = m[1]
		}
	}
	for _, line := range strings.Split(src, "\n") {
		p.line(strings.TrimRight(line, "\r"))
	}
	p.closeRelease()

	n := &Notes{releases: p.done, byVer: make(map[string]int, len(p.done))}
	for i, r := range n.releases {
		n.byVer[r.Version] = i
	}
	return n
}

// parser is a line-at-a-time reader. A release's lines fall into its intro until the
// first category heading, and into that category's changes after it.
type parser struct {
	adrPaths map[string]string
	done     []Release

	cur      *Release
	category string

	// The block being read. para collects the lines of the current paragraph or list
	// item; inBullet says it is a list item, and first says it is still the item's
	// first paragraph — the only one a change keeps.
	para     []string
	inBullet bool
	first    bool
	// bullet collects every line of the current change, for its link: the reference
	// is often at the end of a later paragraph.
	bullet  []string
	list    []string // the intro list being read
	inFence bool
}

func (p *parser) line(l string) {
	trimmed := strings.TrimSpace(l)
	if p.inFence || (p.cur != nil && strings.HasPrefix(trimmed, "```")) {
		// A code block is read as nothing but text for the link: a line in it that
		// looks like a bullet or a heading is neither.
		if strings.HasPrefix(trimmed, "```") {
			p.inFence = !p.inFence
		}
		if p.inBullet {
			p.first = false
			p.bullet = append(p.bullet, l)
		}
		return
	}
	if m := versionHead.FindStringSubmatch(l); m != nil {
		p.closeRelease()
		p.cur = &Release{Version: m[1], Date: m[2], Intro: []Block{}, Changes: []Change{}}
		p.cur.Link = releaseLink(m[1], strings.TrimPrefix(l, "## "))
		return
	}
	if strings.HasPrefix(l, "## ") {
		// A second-level heading that is not a version ends the releases.
		p.closeRelease()
		return
	}
	if p.cur == nil {
		return
	}
	if m := categoryHd.FindStringSubmatch(l); m != nil {
		p.closeBlock()
		p.closeList()
		p.category = m[1]
		return
	}

	switch {
	case trimmed == "":
		p.closeParagraph()
	case strings.HasPrefix(l, "- "):
		// A top-level bullet: a change under a category, a list item in the intro.
		p.closeBlock()
		p.inBullet, p.first = true, true
		p.para = []string{strings.TrimPrefix(l, "- ")}
		p.bullet = []string{l}
	case p.inBullet && strings.HasPrefix(l, " "):
		// The bullet goes on: its first paragraph wrapping, or a later paragraph or a
		// nested list, which only the link is read from.
		p.bullet = append(p.bullet, l)
		if nestedItem.MatchString(trimmed) {
			// A nested list ends the first paragraph even without a blank line before it.
			p.first = false
		}
		if p.first {
			p.para = append(p.para, trimmed)
		}
	case p.inBullet && len(p.para) > 0 && p.first:
		// A lazy continuation: markdown lets a wrapped line start at column 0.
		p.bullet = append(p.bullet, l)
		p.para = append(p.para, trimmed)
	default:
		if p.inBullet {
			p.closeBlock()
		}
		if p.category == "" {
			p.closeList()
			p.para = append(p.para, trimmed)
		}
		// A paragraph between a category's bullets is not a change, and the link
		// definitions Keep a Changelog ends with are not one either.
	}
}

// closeParagraph ends the paragraph being read. Inside a bullet that is the end of its
// first paragraph and not of the bullet: what follows indented still belongs to it.
func (p *parser) closeParagraph() {
	if p.inBullet {
		p.first = false
		return
	}
	if p.cur != nil && p.category == "" && len(p.para) > 0 {
		p.cur.Intro = append(p.cur.Intro, Block{Kind: "p", Text: clean(strings.Join(p.para, " "))})
	}
	p.para = nil
}

// closeBlock ends the bullet or paragraph being read, whatever its state.
func (p *parser) closeBlock() {
	if !p.inBullet {
		p.closeParagraph()
		return
	}
	text := clean(strings.Join(p.para, " "))
	if p.category == "" {
		p.list = append(p.list, text)
	} else {
		p.cur.Changes = append(p.cur.Changes, p.change(text, strings.Join(p.bullet, "\n")))
	}
	p.para, p.bullet, p.inBullet, p.first = nil, nil, false, false
}

func (p *parser) closeList() {
	if p.cur != nil && len(p.list) > 0 {
		p.cur.Intro = append(p.cur.Intro, Block{Kind: "list", Items: p.list})
	}
	p.list = nil
}

func (p *parser) closeRelease() {
	if p.cur != nil {
		p.closeBlock()
		p.closeList()
		if len(p.cur.Intro) > 0 || len(p.cur.Changes) > 0 {
			p.done = append(p.done, *p.cur)
		}
	}
	p.cur, p.category, p.inFence = nil, "", false
	p.para, p.bullet, p.list, p.inBullet, p.first = nil, nil, nil, false, false
}

// change splits a bullet's first paragraph into its headline and the rest. A bullet
// with no bold headline is still a change, and its first sentence stands in for one.
func (p *parser) change(text, whole string) Change {
	c := Change{Category: p.category, Link: p.link(whole)}
	// Bold that the sentence runs on from — "**Receive tasks**, and …" — is emphasis
	// in a plain bullet, not a headline.
	if m := boldTitle.FindStringSubmatch(text); m != nil && !runsOn.MatchString(text[len(m[0]):]) {
		c.Title = strings.TrimSpace(m[1])
		rest := leadIn.ReplaceAllString(text[len(m[0]):], "")
		c.Text = strings.TrimSpace(leadPunct.ReplaceAllString(rest, ""))
		return c
	}
	if m := sentence.FindStringSubmatch(text); m != nil {
		c.Title, c.Text = m[1], strings.TrimSpace(text[len(m[1]):])
		return c
	}
	c.Title = text
	return c
}

// link resolves the best reference in a bullet: a pull request, as the most
// user-facing; then the record the bullet is about. A record no entry spells out with
// its path opens the directory it is in, since its file name cannot be derived from
// its number.
func (p *parser) link(whole string) *Link {
	if m := prRef.FindStringSubmatch(whole); m != nil {
		return &Link{Label: "PR #" + m[1], URL: repoURL + "/pull/" + m[1]}
	}
	n := record(whole)
	if n == "" {
		return nil
	}
	if path, ok := p.adrPaths[n]; ok {
		return &Link{Label: "ADR-" + n, URL: repoURL + "/blob/main/" + path}
	}
	return &Link{Label: "ADR-" + n, URL: repoURL + "/tree/main/docs/adr"}
}

// record picks the record a bullet is about. The changelog closes an entry by citing
// it — "… stated here. ADR-0438." or "… (ADR-0352, ADR-0237)." — and cites the records
// it builds on along the way, so the first citation of a closing group is the entry's
// own. An entry with no closing citation names its record first, in the lead-in after
// its headline, when it names one at all.
func record(whole string) string {
	refs := adrRef.FindAllStringSubmatchIndex(whole, -1)
	if len(refs) == 0 {
		return ""
	}
	number := func(m []int) string {
		if m[2] >= 0 {
			return whole[m[2]:m[3]]
		}
		return whole[m[4]:m[5]]
	}
	last := len(refs) - 1
	if !citeEnd.MatchString(whole[refs[last][1]:]) {
		return number(refs[0])
	}
	first := last
	for first > 0 && citeGap.MatchString(whole[refs[first-1][1]:refs[first][0]]) {
		first--
	}
	return number(refs[first])
}

// releaseLink points at a release's section of the CHANGELOG: on main for what is not
// released yet, and as tagged for a version, so the notes read are the notes shipped.
func releaseLink(version, heading string) Link {
	ref := "main"
	if version != "Unreleased" {
		ref = "v" + version
	}
	return Link{Label: "CHANGELOG.md", URL: repoURL + "/blob/" + ref + "/CHANGELOG.md#" + anchor(heading)}
}

// anchor is the id GitHub gives a heading: lower-cased, with everything but letters,
// digits, spaces, hyphens and underscores dropped, and each space made a hyphen.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clean reduces a markdown paragraph to the text the Console shows: a link becomes its
// text and every run of whitespace one space. **bold** and `code` stay, for the
// Console to render.
func clean(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return strings.TrimSpace(space.ReplaceAllString(s, " "))
}
