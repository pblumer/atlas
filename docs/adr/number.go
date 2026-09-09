// Package adr carries the ADR directory's own conventions as code: the parser
// for a decision record, the guard tests in adr_test.go that keep the directory
// and its index honest, and the merge-time numbering below.
//
// The problem it solves is structural. A number used to be taken when a record
// was written, on a branch — so two branches that each wrote a record each took
// the same "next free" number, and whichever merged second had to renumber: rename
// the file, fix the heading, move the index row, and chase every citation. Numbers
// 0090, 0103 and 0105 were each shared by two unrelated decisions that way before
// there was a test; afterwards the test caught it, but the renumbering churn
// stayed, and one record walked 0164 → 0165 → 0166 → 0167 → 0168 → 0169 across six
// merges without a word of its content changing.
//
// So a record in flight carries no number at all. It lives at draft-<slug>.md,
// heads with `# ADR-DRAFT: title`, and is cited as ADR-draft-<slug>. Two branches
// writing records touch different files and no shared line of the index, so they
// cannot collide. AssignNumbers gives every draft its number once it is on main —
// where "the next free number" is finally a question with one answer — and rewrites
// the file name, the heading, the index row and every citation in one pass.
//
// A number, once assigned, is never reassigned. That is what makes a citation
// safe: ADR-0168 in a comment means today what it will mean in a year.
package adr

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Record is one decision record on disk. A record still in flight has Num 0 and
// lives at draft-<slug>.md; a record that has landed carries its number in both
// the file name and the heading, and the guard tests insist the two agree.
type Record struct {
	Num    int    // 0 while the record is a draft
	Name   string // file name within docs/adr
	Slug   string // the file name's kebab-case part, stable across numbering
	Title  string // from the `# ADR-NNNN: title` heading
	Status string // from the `- **Status:** ...` line
	// Implementation is the second half of a record's state, from the
	// `- **Implementation:** ...` line. Status answers "does this decision hold";
	// Implementation answers "is it built". One field carried both for 295 records
	// and could answer neither: a decision whose code had shipped still read
	// `Proposed`, because nothing in the process ever went back to change it.
	// ADR-draft-two-states-for-a-record says why they are separate rather than merged.
	Implementation string
	// OpenQuestion and QuestionChecked are the optional pair a record carries when
	// its reasoning rests on something nobody could answer: what the question is,
	// and the YYYY-MM month somebody last looked at it. They come as a pair or not
	// at all — a question with no date cannot go stale and would quietly read as
	// settled, a date with no question says a thing was checked without saying what
	// — and a guard test fails once a date has stood for a year.
	OpenQuestion    string
	QuestionChecked string
}

// IsDraft reports whether this record is still waiting for a number.
func (r Record) IsDraft() bool { return r.Num == 0 }

var (
	numberedNamePattern = regexp.MustCompile(`^(\d{4})-([a-z0-9-]+)\.md$`)
	draftNamePattern    = regexp.MustCompile(`^draft-([a-z0-9-]+)\.md$`)
	numberedHeading     = regexp.MustCompile(`(?m)^# ADR-(\d{4}): (.+)$`)
	draftHeading        = regexp.MustCompile(`(?m)^# ADR-DRAFT: (.+)$`)
	statusPattern       = regexp.MustCompile(`(?m)^- \*\*Status:\*\* (.+)$`)
	implPattern         = regexp.MustCompile(`(?m)^- \*\*Implementation:\*\* (.+)$`)
	// The value runs to the end of the line and onto any lines indented under it,
	// because these records wrap at prose width and a question truncated at the
	// margin would be truncated in the very message meant to state it.
	openQuestionPattern = regexp.MustCompile(`(?m)^- \*\*Open question:\*\* (.+(?:\n[ \t]+\S.*)*)$`)
	questionCheckedPtrn = regexp.MustCompile(`(?m)^- \*\*Question checked:\*\* (.+)$`)
	monthPattern        = regexp.MustCompile(`^\d{4}-\d{2}$`)
	// A cell may not contain a `|`, so each is matched as "not a pipe" rather than
	// greedily: with three cells after the link, a greedy title would swallow the
	// status and leave the last cell to be read as both.
	indexRowPattern = regexp.MustCompile(`^\| \[(\d{4})\]\(([^)]+)\) \| ([^|]*) \| ([^|]+) \| ([^|]+) \|$`)

	// notRecords are the two files in docs/adr that carry no decision.
	notRecords = map[string]bool{"README.md": true, "template.md": true}
)

// LoadRecords reads every record in dir — numbered and draft alike — and reports
// every malformed one in a single joined error, so a run names all the problems
// rather than the first. Records that parsed well enough to identify are returned
// even when the error is non-nil, which is what lets the guard tests check the
// index against the directory while still reporting a record's own defects.
func LoadRecords(dir string) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var (
		records  []Record
		problems []error
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || notRecords[e.Name()] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		r, err := parseRecord(e.Name(), string(body))
		if err != nil {
			problems = append(problems, err)
		}
		if r.Slug != "" {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, errors.Join(problems...)
}

// parseRecord turns one file into a Record, naming every way it fails to be one.
func parseRecord(name, body string) (Record, error) {
	r := Record{Name: name}
	switch {
	case numberedNamePattern.MatchString(name):
		m := numberedNamePattern.FindStringSubmatch(name)
		num, err := strconv.Atoi(m[1])
		if err != nil { // unreachable: the pattern is four digits
			return Record{}, fmt.Errorf("%s: %v", name, err)
		}
		r.Num, r.Slug = num, m[2]
	case draftNamePattern.MatchString(name):
		r.Slug = draftNamePattern.FindStringSubmatch(name)[1]
	default:
		return Record{}, fmt.Errorf("%s: not a NNNN-lowercase-slug.md or draft-lowercase-slug.md file name", name)
	}

	var problems []error
	if h := numberedHeading.FindStringSubmatch(body); h != nil {
		r.Title = h[2]
		switch {
		case r.IsDraft():
			problems = append(problems, fmt.Errorf("%s: heading says ADR-%s, but a record in flight carries no number — head it `# ADR-DRAFT: title` and let the merge assign one", name, h[1]))
		default:
			if num, _ := strconv.Atoi(h[1]); num != r.Num {
				problems = append(problems, fmt.Errorf("%s: heading says ADR-%s but the file name says ADR-%04d", name, h[1], r.Num))
			}
		}
	} else if h := draftHeading.FindStringSubmatch(body); h != nil {
		r.Title = h[1]
		if !r.IsDraft() {
			problems = append(problems, fmt.Errorf("%s: heading says ADR-DRAFT but the file name already carries number %04d", name, r.Num))
		}
	}
	if r.Title == "" {
		if r.IsDraft() {
			problems = append(problems, fmt.Errorf("%s: no `# ADR-DRAFT: title` heading", name))
		} else {
			problems = append(problems, fmt.Errorf("%s: no `# ADR-NNNN: title` heading", name))
		}
	}
	if s := statusPattern.FindStringSubmatch(body); s != nil {
		r.Status = strings.TrimSpace(s[1])
	} else {
		problems = append(problems, fmt.Errorf("%s: no `- **Status:** ...` line", name))
	}
	if i := implPattern.FindStringSubmatch(body); i != nil {
		r.Implementation = strings.TrimSpace(i[1])
	} else {
		problems = append(problems, fmt.Errorf("%s: no `- **Implementation:** ...` line — "+
			"say whether the decision is %s, because Status alone cannot", name, strings.Join(implementationWords, ", ")))
	}
	problems = append(problems, parseState(name, &r)...)
	problems = append(problems, parseOpenQuestion(name, body, &r)...)
	return r, errors.Join(problems...)
}

// statusWords are the values a record's Status may take, beside the parameterised
// `Superseded by ADR-NNNN`. The template has listed them since the directory
// existed; nothing enforced them, and two records drifted to `Draft` — a word that
// means "not numbered yet" everywhere else in this package, on records that are
// numbered and whose code has shipped.
var statusWords = []string{"Proposed", "Accepted", "Deprecated"}

// implementationWords are the values a record's Implementation may take.
//
//   - Not started — none of the chosen option is in the tree.
//   - Partial — some of what the record decided is built and some is not. An
//     extension the record itself defers is not what makes a record partial;
//     a piece of the decision that is missing is.
//   - Landed — the chosen option is in production code with tests.
//   - Superseded — a later record replaced the decision, so there is nothing here
//     left to measure.
var implementationWords = []string{"Not started", "Partial", "Landed", "Superseded"}

// supersededBy matches the one status that carries a parameter.
var supersededBy = regexp.MustCompile(`^Superseded by ADR-\d{4}$`)

// BaseStatus strips a status's parenthetical. The index deliberately abbreviates
// "Accepted (amended 2026-08-17: …)" to "Accepted (amended)", so only the status
// itself — Proposed, Accepted, Superseded by ADR-NNNN, Deprecated — is comparable.
func BaseStatus(s string) string {
	if i := strings.Index(s, "("); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// parseState checks the two state fields against their vocabularies and against
// each other. The pair exists because one field could not answer both questions it
// was being asked, so the checks that matter are the ones that keep them from
// drifting back into one: a record whose code has shipped is a decision that was
// taken, whatever the process forgot to write down.
func parseState(name string, r *Record) []error {
	var problems []error
	base := BaseStatus(r.Status)
	if r.Status != "" && !slices.Contains(statusWords, base) && !supersededBy.MatchString(base) {
		problems = append(problems, fmt.Errorf("%s: status %q is not one of %s or `Superseded by ADR-NNNN` — "+
			"a parenthetical after the word is fine, a different word is not", name, base, strings.Join(statusWords, ", ")))
	}
	if r.Implementation != "" && !slices.Contains(implementationWords, r.Implementation) {
		problems = append(problems, fmt.Errorf("%s: implementation %q is not one of %s — this field is read by machine, so it carries no parenthetical; "+
			"what is built and what is not belongs in the record", name, r.Implementation, strings.Join(implementationWords, ", ")))
	}
	switch r.Implementation {
	case "Landed":
		if base != "Accepted" {
			problems = append(problems, fmt.Errorf("%s: implementation is Landed but status is %q — "+
				"code merged against a decision is the decision being taken, so a landed record is Accepted", name, base))
		}
	case "Superseded":
		if !supersededBy.MatchString(base) {
			problems = append(problems, fmt.Errorf("%s: implementation is Superseded but status is %q — "+
				"say which record replaced it: `Superseded by ADR-NNNN`", name, base))
		}
	}
	return problems
}

// parseOpenQuestion reads the optional open-question pair and names every way it
// is half-written. The pair is what keeps a decision taken on a gap in what we
// know from reading, a year later, as one taken on knowledge: it states the gap
// and the month somebody last looked at it, and the freshness guard in
// openquestion_test.go fails once that month is a year old.
func parseOpenQuestion(name, body string, r *Record) []error {
	if q := openQuestionPattern.FindStringSubmatch(body); q != nil {
		r.OpenQuestion = unwrap(q[1])
	}
	if c := questionCheckedPtrn.FindStringSubmatch(body); c != nil {
		r.QuestionChecked = strings.TrimSpace(c[1])
	}
	var problems []error
	switch {
	case r.OpenQuestion == "" && r.QuestionChecked == "":
		return nil // the ordinary record: nothing unresolved to declare
	case r.QuestionChecked == "":
		problems = append(problems, fmt.Errorf("%s: states an open question but no `- **Question checked:** YYYY-MM` line — "+
			"a question with no date cannot go stale, so it would read as settled the moment nobody remembers it was open", name))
	case r.OpenQuestion == "":
		problems = append(problems, fmt.Errorf("%s: dates a question check but has no `- **Open question:** ...` line — "+
			"a date alone says a thing was checked without saying what", name))
	}
	if r.QuestionChecked != "" && !monthPattern.MatchString(r.QuestionChecked) {
		problems = append(problems, fmt.Errorf("%s: `- **Question checked:** %s` is not a YYYY-MM month", name, r.QuestionChecked))
	}
	return problems
}

// Assignment is one draft becoming a numbered record.
type Assignment struct {
	Slug           string
	Num            int
	From           string // draft-<slug>.md
	To             string // NNNN-<slug>.md
	Title          string
	Status         string
	Implementation string
}

// QuestionAge reports how long ago the record's open question was last looked at,
// relative to now, and whether there was a month to read at all. A negative age is
// a date in the future — a typo, and the one that would make a freshness check
// quieter rather than louder, so the guard can fail on it rather than wait it out.
//
// The month is taken at its first day: a check stated as "2026-09" is treated as
// having happened on 1 September, which ages it slightly faster than it happened.
// That is the safe direction for a freshness rule.
func (r Record) QuestionAge(now time.Time) (time.Duration, bool) {
	if r.QuestionChecked == "" {
		return 0, false
	}
	checked, err := time.Parse("2006-01", r.QuestionChecked)
	if err != nil {
		return 0, false // parseRecord reports an unreadable month; this only declines to age it
	}
	return now.Sub(checked), true
}

// unwrap joins a field's continuation lines back into one, collapsing the
// indentation the record wrapped it with.
func unwrap(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// AssignNumbers gives every draft under root/docs/adr the next free number and
// makes the whole repository agree about it: the file is renamed, its heading
// rewritten, its row appended to the index, and every `ADR-draft-<slug>` citation
// and `draft-<slug>.md` link in the tree rewritten to the number it just got.
//
// It is meant to run on main, where "the next free number" has one answer — see
// the package comment. With no drafts present it changes nothing, which is the
// common case for the workflow that runs it on every push.
func AssignNumbers(root string) ([]Assignment, error) {
	dir := filepath.Join(root, "docs", "adr")
	records, err := LoadRecords(dir)
	if err != nil {
		// Renaming files and rewriting citations across the tree on top of a
		// directory that is already inconsistent turns one problem into a
		// scattered one. Refuse, and let the guard tests say what is wrong.
		return nil, fmt.Errorf("docs/adr is not consistent, so nothing was numbered:\n%w", err)
	}

	next := 0
	var drafts []Record
	for _, r := range records {
		if r.IsDraft() {
			drafts = append(drafts, r)
			continue
		}
		if r.Num > next {
			next = r.Num
		}
	}
	if len(drafts) == 0 {
		return nil, nil
	}
	// Slug order, not directory order: two records merging before either is
	// numbered is the normal case, and which number each gets must not depend on
	// how the filesystem happened to list them.
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].Slug < drafts[j].Slug })

	// Check every draft before renaming any of them: a refusal halfway through
	// would leave some records numbered and the rest not, which is a worse state
	// than the one this run started in.
	for _, d := range drafts {
		if strings.Contains(d.Title, "|") || strings.Contains(d.Status, "|") || strings.Contains(d.Implementation, "|") {
			return nil, fmt.Errorf("%s: a `|` in the title, status or implementation would end the index cell early and corrupt every row after it", d.Name)
		}
	}

	var assigned []Assignment
	for _, d := range drafts {
		next++
		a := Assignment{Slug: d.Slug, Num: next, From: d.Name, Title: d.Title, Status: d.Status, Implementation: d.Implementation}
		a.To = fmt.Sprintf("%04d-%s.md", a.Num, a.Slug)

		body, err := os.ReadFile(filepath.Join(dir, a.From))
		if err != nil {
			return assigned, err
		}
		body = draftHeading.ReplaceAll(body, []byte(fmt.Sprintf("# ADR-%04d: $1", a.Num)))
		// Rewrite in place, then rename. The other order leaves a window in which
		// both the draft and the numbered record exist, and a run interrupted
		// there would look like one decision filed twice.
		if err := os.WriteFile(filepath.Join(dir, a.From), body, 0o644); err != nil {
			return assigned, err
		}
		if err := os.Rename(filepath.Join(dir, a.From), filepath.Join(dir, a.To)); err != nil {
			return assigned, err
		}
		assigned = append(assigned, a)
	}

	if err := rewriteCitations(root, assigned); err != nil {
		return assigned, err
	}
	if err := appendIndexRows(filepath.Join(dir, "README.md"), assigned); err != nil {
		return assigned, err
	}
	return assigned, nil
}

// citable are the file types that carry ADR citations. An allowlist rather than a
// binary sniff: this pass rewrites files in place, and it should never be a
// surprise which ones it can touch.
var citable = map[string]bool{
	".bpmn": true, ".css": true, ".dmn": true, ".go": true, ".html": true,
	".js": true, ".json": true, ".md": true, ".mjs": true, ".sh": true,
	".ts": true, ".txt": true, ".yaml": true, ".yml": true,
}

// skipDirs are trees that never cite an Atlas ADR and are expensive to walk.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"dist": true, "bin": true, "coverage": true,
}

// rewriteCitations moves every reference to a draft onto the number it was just
// given: the `ADR-draft-<slug>` token a comment or a document uses, and the
// `draft-<slug>.md` file name a link uses.
func rewriteCitations(root string, assigned []Assignment) error {
	// Longest slug first. `ADR-draft-alpha` is a prefix of `ADR-draft-alpha2`, and
	// RE2 has no lookahead to say "not followed by more slug", so replacing the
	// short one first would turn the long citation into `ADR-00022`.
	order := append([]Assignment(nil), assigned...)
	sort.Slice(order, func(i, j int) bool { return len(order[i].Slug) > len(order[j].Slug) })

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !citable[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := body
		for _, a := range order {
			out = bytes.ReplaceAll(out, []byte("ADR-draft-"+a.Slug), []byte(fmt.Sprintf("ADR-%04d", a.Num)))
			out = bytes.ReplaceAll(out, []byte("draft-"+a.Slug+".md"), []byte(a.To))
		}
		if bytes.Equal(out, body) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(path, out, info.Mode().Perm())
	})
}

// appendIndexRows adds one row per newly numbered record directly after the last
// existing row. Directly after, because a Markdown table ends at the first line
// that is not a row: a blank line between two rows silently stops every row below
// it from rendering as a table at all.
func appendIndexRows(readme string, assigned []Assignment) error {
	body, err := os.ReadFile(readme)
	if err != nil {
		return err
	}
	lines := strings.Split(string(body), "\n")
	last := -1
	for i, line := range lines {
		if indexRowPattern.MatchString(line) {
			last = i
		}
	}
	if last < 0 {
		return fmt.Errorf("%s: no index rows to append to", readme)
	}

	rows := make([]string, 0, len(assigned))
	for _, a := range assigned {
		rows = append(rows, fmt.Sprintf("| [%04d](%s) | %s | %s | %s |", a.Num, a.To, a.Title, a.Status, a.Implementation))
	}
	out := make([]string, 0, len(lines)+len(rows))
	out = append(out, lines[:last+1]...)
	out = append(out, rows...)
	out = append(out, lines[last+1:]...)
	return os.WriteFile(readme, []byte(strings.Join(out, "\n")), 0o644)
}
