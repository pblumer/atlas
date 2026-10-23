package archsketch

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Drawing a snapshot as an SVG (ADR-0471 §2, §3).
//
// The layout is fixed rather than computed: the ways in on the left, the Atlas node
// with the application in the middle, the dependencies on the right. A sketch of one
// application's boundary has exactly that shape, so a force layout would spend effort
// to arrive somewhere worse — and, unlike the landscape's, this one has to be drawn on
// the server, where there is no browser to run one.
//
// Three properties are load-bearing:
//
//   - **Deterministic.** The same snapshot and meta produce the same bytes. Nothing
//     reads a clock, a map's order or a font: the time drawn is the snapshot's own.
//   - **Self-contained.** No script, no external reference, no stylesheet: the file is
//     embedded in pages Atlas does not control, and it is served under a sandbox policy
//     on top of that.
//   - **Honest about what it is.** A solid arrow is a connection the environment opens
//     towards Atlas — every way in is a route of Atlas's own API. A dashed line is a
//     dependency and nothing more: who connects to whom on that side is the physical
//     view, which a snapshot of the logical part does not hold, and drawing an arrow
//     there would assert a direction nobody recorded.
//
// The server has no font metrics, so boxes have fixed widths and labels are cut by
// character count, with the whole text in each box's <title>.

const (
	svgWidth   = 1120
	colLeftX   = 24
	colRightX  = 776
	colWidth   = 320
	nodeX      = 424
	nodeWidth  = 272
	appInset   = 20
	boxHeight  = 56
	boxGap     = 12
	headerH    = 92
	nodeHeadH  = 30
	lineHeight = 17
	labelChars = 36
	fontStack  = "system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"

	// ArchiMate's own layer colours (ADR-0189 §6: the layer fills are never
	// recoloured), with a neutral ink and a white sheet, because the file is pasted
	// into pages whose theme it cannot know.
	colorApplication = "#b5ffff"
	colorTechnology  = "#c9e7b7"
	colorInk         = "#1f2328"
	colorMuted       = "#57606a"
	colorStroke      = "#4b5563"
	colorSheet       = "#ffffff"
)

// SVG renders the snapshot as a standalone SVG document.
func SVG(s Snapshot, m Meta) []byte {
	left := inboundBoxes(s)
	right := outboundBoxes(s)

	rows := max(len(left), len(right), 3)
	contentH := rows*boxHeight + (rows-1)*boxGap
	nodeTop := headerH + 18
	top := nodeTop + nodeHeadH
	notes := footerLines(s, m)
	footerTop := top + contentH + 40
	height := footerTop + 22 + len(notes)*lineHeight + 16

	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="sketch-title" font-family="%s" font-size="13">`+"\n",
		svgWidth, height, svgWidth, height, esc(fontStack))
	fmt.Fprintf(&b, `<title id="sketch-title">%s</title>`+"\n", esc(title(m)))
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="` + colorStroke + `"/></marker></defs>` + "\n")
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="%s"/>`+"\n", svgWidth, height, colorSheet)

	// Header: what this is and where it came from, in the image itself — a file pasted
	// into a page has nobody beside it to ask.
	fmt.Fprintf(&b, `<text x="%d" y="34" font-size="20" font-weight="600" fill="%s">%s</text>`+"\n", colLeftX, colorInk, esc(title(m)))
	fmt.Fprintf(&b, `<text x="%d" y="56" fill="%s">%s</text>`+"\n", colLeftX, colorMuted, esc(stampLine(s, m)))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-weight="600" fill="%s">Reached from outside</text>`+"\n", colLeftX, headerH+10, colorInk)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-weight="600" fill="%s">Depends on</text>`+"\n", colRightX, headerH+10, colorInk)

	// The Atlas node, and the application inside it.
	nodeH := nodeHeadH + contentH + 16
	fmt.Fprintf(&b, `<g><title>%s</title>`, esc("Atlas — the server the application runs on"))
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="4" fill="%s" stroke="%s"/>`, nodeX, nodeTop, nodeWidth, nodeH, colorTechnology, colorStroke)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-weight="600" fill="%s">Atlas</text>`, nodeX+12, nodeTop+20, colorInk)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="end" fill="%s">Node</text></g>`+"\n", nodeX+nodeWidth-10, nodeTop+20, colorMuted)

	appX := nodeX + appInset
	appW := nodeWidth - 2*appInset
	appTop := top
	fmt.Fprintf(&b, `<g><title>%s</title>`, esc(m.Application))
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s"/>`, appX, appTop, appW, contentH, colorApplication, colorStroke)
	appLines := applicationLines(s, m)
	for i, line := range appLines {
		weight := ""
		if i == 0 {
			weight = ` font-weight="600"`
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d"%s fill="%s">%s</text>`, appX+12, appTop+24+i*lineHeight, weight, colorInk, esc(cut(line, 30)))
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="end" fill="%s">Application component</text></g>`+"\n", appX+appW-10, appTop+contentH-10, colorMuted)

	// The ways in, each with a solid arrow into the application.
	for i, box := range left {
		y := top + i*(boxHeight+boxGap)
		drawBox(&b, colLeftX, y, box)
		mid := y + boxHeight/2
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5" marker-end="url(#arrow)"/>`+"\n",
			colLeftX+colWidth, mid, appX, clampY(mid, appTop, contentH), colorStroke)
	}

	// The dependencies, each with a dashed line and no arrowhead.
	if len(right) == 0 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s">No outbound dependencies.</text>`+"\n", colRightX, top+boxHeight/2+5, colorMuted)
	}
	for i, box := range right {
		y := top + i*(boxHeight+boxGap)
		drawBox(&b, colRightX, y, box)
		mid := y + boxHeight/2
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5" stroke-dasharray="6 4"/>`+"\n",
			appX+appW, clampY(mid, appTop, contentH), colRightX, mid, colorStroke)
	}

	// Footer: the key, and what this sketch does not show.
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#d0d7de"/>`+"\n", colLeftX, footerTop, svgWidth-colLeftX, footerTop)
	for i, line := range notes {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text>`+"\n", colLeftX, footerTop+22+i*lineHeight, colorMuted, esc(line))
	}
	b.WriteString("</svg>\n")
	return []byte(b.String())
}

// box is one entry on either side: a fill for its layer, two lines of text, and the
// whole of what it stands for in its tooltip.
type box struct {
	fill    string
	label   string
	detail  string
	tooltip string
	kind    string // the ArchiMate element type, written small in the corner
}

func drawBox(b *strings.Builder, x, y int, bx box) {
	fmt.Fprintf(b, `<g><title>%s</title>`, esc(bx.tooltip))
	fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s"/>`, x, y, colWidth, boxHeight, bx.fill, colorStroke)
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="10" fill="%s">%s</text>`, x+12, y+14, colorMuted, esc(bx.kind))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-weight="600" fill="%s">%s</text>`, x+12, y+31, colorInk, esc(cut(bx.label, labelChars)))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text></g>`+"\n", x+12, y+48, colorMuted, esc(cut(bx.detail, labelChars+10)))
}

// inboundBoxes is the left column: Atlas's API first, because every other way in is
// one of its routes, then each message the application receives.
func inboundBoxes(s Snapshot) []box {
	out := []box{{
		fill:    colorTechnology,
		label:   "Atlas API",
		detail:  "starting instances · messages · tasks",
		tooltip: "Atlas API — the HTTP API every way in reaches the application through",
		kind:    "Technology interface",
	}}
	for _, e := range s.EntryPoints {
		effect := "continues a running instance"
		if e.Starts {
			effect = "starts an instance"
		}
		out = append(out, box{
			fill:    colorApplication,
			label:   "Message " + e.Name,
			detail:  effect + " · " + elementCount(len(e.UsedBy)),
			tooltip: "Message " + e.Name + " — " + effect + "; received at " + usesText(e.UsedBy),
			kind:    "Application interface",
		})
	}
	return out
}

// outboundBoxes is the right column: the dependencies that leave the server.
func outboundBoxes(s Snapshot) []box {
	var out []box
	for _, d := range s.Outbound() {
		bx := box{fill: colorApplication, kind: "Application service"}
		switch d.Kind {
		case DependencyWorker:
			bx.label = d.Name
			bx.detail = workerTypeText(d.WorkerType) + " · " + elementCount(len(d.UsedBy))
			bx.tooltip = "Worker " + d.Name + " (" + workerTypeText(d.WorkerType) + ") — used at " + usesText(d.UsedBy)
		case DependencyRuntimeWorker:
			bx.label = "Decided at run time"
			bx.detail = workerTypeText(d.WorkerType) + " · " + d.Name
			bx.tooltip = "A worker whose name the model computes per instance (" + d.Name + ") — used at " + usesText(d.UsedBy)
		case DependencyCall:
			bx.kind = "Application component"
			bx.label = "Process " + d.Name
			if d.TargetApplication != "" {
				bx.detail = "in " + d.TargetApplication + " · " + elementCount(len(d.UsedBy))
			} else {
				bx.detail = "not deployed here when recorded · " + elementCount(len(d.UsedBy))
			}
			bx.tooltip = "Call activity into process " + d.Name + " — called at " + usesText(d.UsedBy)
		default:
			continue
		}
		out = append(out, bx)
	}
	return out
}

func applicationLines(s Snapshot, m Meta) []string {
	lines := []string{m.Application, fmt.Sprintf("Release v%d", m.Version)}
	lines = append(lines, plural(len(s.Processes), "process", "processes"))
	if n := s.LocalDecisions(); n > 0 {
		lines = append(lines, plural(n, "local decision", "local decisions"))
	}
	if n := s.UnreadProcesses(); n > 0 {
		lines = append(lines, plural(n, "process not readable", "processes not readable"))
	}
	return lines
}

func footerLines(s Snapshot, m Meta) []string {
	lines := []string{
		fmt.Sprintf("Generated by Atlas from release v%d of %s; nothing here was drawn by a person.", m.Version, m.Application),
		"Solid arrow: a connection opened towards Atlas. Dashed line: the application depends on it — who connects to whom there is not recorded in this snapshot.",
	}
	if n := s.UnreadProcesses(); n > 0 {
		lines = append(lines, fmt.Sprintf("Incomplete: %s could not be read when the snapshot was taken; what they offer and need is missing.",
			plural(n, "process", "processes")))
	}
	lines = append(lines, "Not shown:")
	for _, gap := range NotShown() {
		lines = append(lines, "  • "+gap)
	}
	return lines
}

func title(m Meta) string {
	return fmt.Sprintf("Architecture sketch — %s v%d", m.Application, m.Version)
}

func stampLine(s Snapshot, m Meta) string {
	return fmt.Sprintf("Logical view · snapshot taken %s · mapping version %d · release %s",
		formatTime(s.TakenAt), s.MappingVersion, m.ReleaseID)
}

func formatTime(unix int64) string {
	if unix <= 0 {
		return "at an unrecorded time"
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
}

func workerTypeText(t string) string {
	if t == "" {
		return "worker"
	}
	return t + " worker"
}

func elementCount(n int) string { return plural(n, "element", "elements") }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// usesText names the elements behind a line, for its tooltip. Bounded, because a
// worker used from a hundred tasks would otherwise make a tooltip nobody can read.
func usesText(uses []Use) string {
	const shown = 8
	parts := make([]string, 0, min(len(uses), shown))
	for i, u := range uses {
		if i == shown {
			break
		}
		if u.ElementID != "" {
			parts = append(parts, u.ProcessID+"/"+u.ElementID)
		} else {
			parts = append(parts, u.ProcessID)
		}
	}
	text := strings.Join(parts, ", ")
	if len(uses) > shown {
		text += fmt.Sprintf(" and %d more", len(uses)-shown)
	}
	if text == "" {
		return "no recorded element"
	}
	return text
}

func clampY(y, top, height int) int {
	return min(max(y, top+12), top+height-12)
}

// cut shortens a label to n characters, counted as runes so a name in any script is
// cut on a character rather than through one.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// esc makes text safe inside an XML element or a double-quoted attribute. Characters
// XML 1.0 cannot carry at all are dropped rather than escaped: there is no escape for
// them, and a document that contains one is not well-formed.
func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0xFFFE || r == 0xFFFF || r == utf8.RuneError:
			// not representable in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
