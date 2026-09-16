package feed

import (
	"os"
	"regexp"
	"strings"
)

// parsed is one CHANGELOG bullet, before any override is merged onto it.
type parsed struct {
	id, version, category, title, fallbackSummary string
	date                                          *string
	link                                          Link
}

var (
	adrMarkdown = regexp.MustCompile(`\[ADR-(\d+)\]\((docs/adr/[^)]+)\)`)
	adrFileName = regexp.MustCompile(`^(\d+)-.+\.md$`)
	adrBare     = regexp.MustCompile(`\bADR-(\d+)\b`)
	prRef       = regexp.MustCompile(`\(#(\d+)\)`)
	versionHead = regexp.MustCompile(`^## \[([^\]]+)\](?:\s+[—-]\s+(\d{4}-\d{2}-\d{2}))?`)
	newestDated = regexp.MustCompile(`(?m)^## \[[^\]]+\]\s+[—-]\s+(\d{4}-\d{2}-\d{2})`)
	categoryHd  = regexp.MustCompile(`^### (\w+)`)
	bulletStart = regexp.MustCompile(`^-\s+\*\*`)
	boldTitle   = regexp.MustCompile(`^-\s+\*\*(.+?)\*\*`)
	// leadIn strips the "(ref):" that regularly follows a bold headline. It allows
	// one level of nesting on purpose: the lead-in usually *contains* a parenthesis
	// — "([ADR-0163](docs/adr/….md))" — and a flat \([^)]*\) stops at the markdown
	// link's own closing paren, leaving the summary starting "): …".
	leadIn      = regexp.MustCompile(`^\s*\((?:[^()]|\([^()]*\))*\)\s*[:—-]?\s*`)
	leadPunct   = regexp.MustCompile(`^\s*[:—-]\s*`)
	titleBullet = regexp.MustCompile(`^-\s+\*\*.+?\*\*`)
	// nextBullet ends a bullet's first paragraph. `\s` rather than a literal space,
	// so a tab-indented list reads the same way it does in the original.
	nextBullet = regexp.MustCompile(`^-\s`)
)

// adrPathsIn scans the document for markdown ADR links, so a bullet naming an ADR
// only in bare form still resolves to the file another bullet spelled out.
func adrPathsIn(text string) map[string]string {
	out := map[string]string{}
	for _, m := range adrMarkdown.FindAllStringSubmatch(text, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// adrFilesIn reads docs/adr itself for the same lookup.
//
// A bare "(ADR-0058)" used to resolve only when some *other* bullet happened to
// link that record with a path; when none did, the entry pointed at the directory
// listing instead of the record it names — a link that opens the whole index and
// answers nothing. The files are right there, so they are read.
func adrFilesIn(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if m := adrFileName.FindStringSubmatch(e.Name()); m != nil {
			out[m[1]] = "docs/adr/" + e.Name()
		}
	}
	return out
}

// linkFor resolves the best reference in a bullet.
//
// A pull request wins, because it is the most user-facing; then a record, spelled
// out or bare; otherwise the version's own CHANGELOG section, so that every entry
// has somewhere to point.
func linkFor(block string, adrPaths map[string]string, versionAnchor string) Link {
	if m := prRef.FindStringSubmatch(block); m != nil {
		return Link{Label: "PR #" + m[1], URL: repoURL + "/pull/" + m[1]}
	}
	if m := adrMarkdown.FindStringSubmatch(block); m != nil {
		return Link{Label: "ADR-" + m[1], URL: blobURL + "/" + m[2]}
	}
	if m := adrBare.FindStringSubmatch(block); m != nil {
		path, ok := adrPaths[m[1]]
		url := blobURL + "/docs/adr"
		if ok {
			url = blobURL + "/" + path
		}
		return Link{Label: "ADR-" + m[1], URL: url}
	}
	return Link{Label: "Changelog", URL: blobURL + "/CHANGELOG.md#" + versionAnchor}
}

// parseChangelog walks the document top to bottom — it is already newest-first —
// and yields one record per "- **Title**" bullet, carrying the version and
// category it sits under and the block text its summary and link come from.
func parseChangelog(text, adrDir string) []parsed {
	adrPaths := adrFilesIn(adrDir)
	// A spelled-out path stays authoritative over the directory scan: an author who
	// wrote the path has said which file they mean.
	for k, v := range adrPathsIn(text) {
		adrPaths[k] = v
	}

	lines := strings.Split(text, "\n")
	var out []parsed
	version, category, anchor := "", "", ""
	var date *string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if m := versionHead.FindStringSubmatch(line); m != nil {
			version = m[1]
			date = nil
			if m[2] != "" {
				d := m[2]
				date = &d
			}
			slugSource := version
			if version == "Unreleased" {
				slugSource = "unreleased"
			}
			anchor = slugify(slugSource)
			category = ""
			continue
		}
		if m := categoryHd.FindStringSubmatch(line); m != nil {
			category = m[1]
			continue
		}
		if !bulletStart.MatchString(line) || version == "" {
			continue
		}

		// The bullet's first paragraph: this line plus its continuations, stopping at
		// a blank line or the next bullet or heading. A bold headline that wraps
		// across two lines is therefore still captured whole.
		parts := []string{line}
		j := i + 1
		for ; j < len(lines); j++ {
			l := lines[j]
			if strings.TrimSpace(l) == "" || nextBullet.MatchString(l) || strings.HasPrefix(l, "#") {
				break
			}
			parts = append(parts, l)
		}
		i = j - 1

		block := strings.Join(parts, " ")
		m := boldTitle.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		title := strings.TrimSpace(whitespace.ReplaceAllString(m[1], " "))

		prose := titleBullet.ReplaceAllString(block, "")
		prose = leadIn.ReplaceAllString(prose, "")
		prose = leadPunct.ReplaceAllString(prose, "")
		prose = strings.TrimSpace(prose)

		out = append(out, parsed{
			// The title slug is the id: unique per bullet — several bullets can share
			// one record, so a record-keyed id would collide — and stable across edits
			// to the prose beneath it.
			id:              slugify(title),
			version:         version,
			date:            date,
			category:        category,
			title:           title,
			link:            linkFor(block, adrPaths, anchor),
			fallbackSummary: firstSentence(prose),
		})
	}
	return out
}
