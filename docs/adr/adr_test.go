package adr

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This directory guards its own conventions with Go tests that run in the
// mandatory `go test ./...` sweep. The failure they exist for is real and has
// happened: two feature branches each take "the next free ADR number", both merge,
// and the repository ends up with two unrelated decisions sharing one number.
// Every `(ADR-NNNN)` comment pointing at that number is then ambiguous, and the
// ambiguity spreads through the code as later work cites it. Numbers 0090, 0103
// and 0105 each collided that way before these tests existed; the later ADR of
// each pair was renumbered to 0139, 0140 and 0141.
//
// The tests caught the collision, but catching it still cost a renumbering on
// every merge — one record walked 0164 → 0169 across six of them. So a record now
// carries no number until it lands on main (see number.go): these tests hold the
// numbered half of the directory to one-number-one-decision, and hold the draft
// half to a shape the merge-time numbering can finish.

// loadADRs reads every record, numbered and draft, and reports every malformed
// one. README.md and template.md are not decisions and fall out in LoadRecords.
func loadADRs(t *testing.T) []Record {
	t.Helper()
	records, err := LoadRecords(".")
	if err != nil {
		t.Error(err)
	}
	if len(records) == 0 {
		t.Fatal("no ADRs found — is the test running outside docs/adr?")
	}
	return records
}

// numberedADRs is the subset that has landed: the drafts are deliberately absent
// from the index, the numbering and the contiguity check until they get a number.
func numberedADRs(t *testing.T) []Record {
	t.Helper()
	var numbered []Record
	for _, r := range loadADRs(t) {
		if !r.IsDraft() {
			numbered = append(numbered, r)
		}
	}
	return numbered
}

// TestADRNumbersAreUniqueAndContiguous is the guard against the collision these
// tests were written for: one number, one decision. It also insists the numbers
// run 0001..N without a hole, so a branch that skips ahead is caught too — an ADR
// is never deleted (a superseded one keeps its number and says so), so a gap
// always means a number was misread rather than retired.
func TestADRNumbersAreUniqueAndContiguous(t *testing.T) {
	adrs := numberedADRs(t)

	byNum := make(map[int][]string, len(adrs))
	for _, a := range adrs {
		byNum[a.Num] = append(byNum[a.Num], a.Name)
	}
	for num, names := range byNum {
		if len(names) > 1 {
			sort.Strings(names)
			t.Errorf("ADR-%04d is used by %d files: %s — a number belongs to exactly one decision, and it is assigned on main by `make adr-number`, never taken on a branch",
				num, len(names), strings.Join(names, ", "))
		}
	}

	nums := make([]int, 0, len(byNum))
	for num := range byNum {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for i, num := range nums {
		if want := i + 1; num != want {
			t.Errorf("ADR numbering has a gap: expected ADR-%04d, found ADR-%04d (%s)",
				want, num, strings.Join(byNum[num], ", "))
			break
		}
	}
}

// TestDraftsAreNotInTheIndex: a draft has no number, so it has no row. The index
// is the one file every record in flight would otherwise have to touch, and that
// shared line is exactly what made parallel records conflict. `make adr-number`
// adds the row when the number is assigned.
func TestDraftsAreNotInTheIndex(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, line := range strings.Split(string(readme), "\n") {
		// Table rows only: the prose above the index links to drafts on purpose.
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, "](draft-") {
			t.Errorf("the index carries a draft row: %q — a record gets its row when it gets its number", line)
		}
	}
}

// TestADRIndexMatchesDirectory keeps README.md's table honest: every numbered ADR
// appears exactly once, in order, linked to the file that exists, with the title
// and status the document itself declares. Both ADRs missing from the index when
// the collisions were found (0090's and 0103's) would have failed here.
func TestADRIndexMatchesDirectory(t *testing.T) {
	adrs := numberedADRs(t)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	rows := indexRows(t, string(readme))

	if len(rows) != len(adrs) {
		t.Errorf("README index has %d rows for %d numbered ADR files", len(rows), len(adrs))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].num <= rows[i-1].num {
			t.Errorf("README index is out of order: ADR-%04d follows ADR-%04d", rows[i].num, rows[i-1].num)
		}
	}

	indexed := make(map[int]indexRow, len(rows))
	for _, r := range rows {
		indexed[r.num] = r
	}
	for _, a := range adrs {
		r, ok := indexed[a.Num]
		if !ok {
			t.Errorf("%s is not in the README index — add a row for it", a.Name)
			continue
		}
		if r.link != a.Name {
			t.Errorf("README row for ADR-%04d links to %s, but the file is %s", a.Num, r.link, a.Name)
		}
		if r.title != a.Title {
			t.Errorf("README row for ADR-%04d has title %q, but %s declares %q — the index carries the record's title, not a summary of it",
				a.Num, r.title, a.Name, a.Title)
		}
		if got, want := baseStatus(r.status), baseStatus(a.Status); got != want {
			t.Errorf("README row for ADR-%04d says status %q, but %s says %q", a.Num, got, a.Name, want)
		}
		delete(indexed, a.Num)
	}
	for num, r := range indexed {
		t.Errorf("README index row for ADR-%04d (%s) has no matching file", num, r.link)
	}
}

// TestADRIndexTableRenders guards the shape of the table rather than its contents.
// A Markdown table ends at the first line that is not a row, so a single blank line
// slipped between two rows does not fail any of the checks above — it silently cuts
// the index in half, and every row below it renders as a paragraph of raw pipes.
// That happened at ADR-0148 and went unnoticed for seventeen records, because each
// new ADR appended its row to the broken half.
func TestADRIndexTableRenders(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	lines := strings.Split(string(readme), "\n")

	first, last := -1, -1
	for i, line := range lines {
		if indexRowPattern.MatchString(line) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		t.Fatal("README.md has no index rows at all")
	}
	for i := first; i <= last; i++ {
		if !indexRowPattern.MatchString(lines[i]) {
			t.Errorf("README.md:%d interrupts the index table with %q — a Markdown table ends at the first line that is not a row, so every row below this one stops rendering as a table",
				i+1, lines[i])
		}
	}
	if first < 2 || !strings.HasPrefix(lines[first-1], "|--") || !strings.HasPrefix(lines[first-2], "| ADR ") {
		t.Errorf("README.md:%d — the first index row is not preceded by the `| ADR | Title | Status |` header and its separator", first+1)
	}
}

// indexRows parses README.md's index table into rows. The pattern is deliberately
// anchored: a row that does not match is a row this test cannot vouch for, and the
// count check in TestADRIndexMatchesDirectory turns that into a failure.
func indexRows(t *testing.T, readme string) []indexRow {
	t.Helper()
	var rows []indexRow
	for _, line := range strings.Split(readme, "\n") {
		m := indexRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("index row %q: %v", line, err)
		}
		rows = append(rows, indexRow{num: num, link: m[2], title: m[3], status: m[4]})
	}
	return rows
}

// indexRow is one row of README.md's index table.
type indexRow struct {
	num    int
	link   string
	title  string
	status string
}

// TestADRReferencesResolve checks that every link between records points at a file
// that exists — a numbered one, or a draft that is still in flight. A renumbering
// that misses a link is the other half of the collision problem: the number in the
// prose and the file it links to drift apart silently.
func TestADRReferencesResolve(t *testing.T) {
	adrs := loadADRs(t)
	exists := make(map[string]bool, len(adrs))
	for _, a := range adrs {
		exists[a.Name] = true
	}
	linkPattern := regexp.MustCompile(`\(((?:\d{4}|draft)-[a-z0-9-]+\.md)\)`)
	for _, name := range append([]string{"README.md"}, namesOf(adrs)...) {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range linkPattern.FindAllStringSubmatch(string(body), -1) {
			if !exists[m[1]] {
				t.Errorf("%s links to %s, which does not exist", name, m[1])
			}
		}
	}
}

// TestCitationsResolveAcrossTheRepository is the check the directory-local ones
// could not make: `ADR-0168` appears in Go comments, docs, the changelog and the
// UI, and nothing used to verify that the number a citation names still belongs to
// the record it meant. Under merge-time numbering a number is never reassigned, so
// this test states the property that guarantee buys — and it catches the other
// direction too, a draft citation left behind because it sat in a file type the
// rewrite does not touch.
func TestCitationsResolveAcrossTheRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the repository root two levels up from docs/adr: %v", err)
	}
	adrs := loadADRs(t)
	numbered := make(map[int]string, len(adrs))
	drafts := make(map[string]bool, len(adrs))
	for _, a := range adrs {
		if a.IsDraft() {
			drafts[a.Slug] = true
			continue
		}
		numbered[a.Num] = a.Name
	}

	numberCite := regexp.MustCompile(`ADR-(\d{4})`)
	draftCite := regexp.MustCompile(`ADR-draft-([a-z0-9-]+)`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		rel := filepath.ToSlash(mustRel(t, root, path))
		for _, m := range numberCite.FindAllStringSubmatch(string(body), -1) {
			num, _ := strconv.Atoi(m[1])
			if numbered[num] == "" {
				t.Errorf("%s cites ADR-%04d, which is not a record in docs/adr", rel, num)
			}
		}
		// The Go files in this directory implement the convention, so they spell
		// the draft citation form out — in a doc comment explaining it, and in the
		// fixtures that prove the rewrite. Those are definitions, not citations.
		if strings.HasPrefix(rel, "docs/adr/") && strings.HasSuffix(rel, ".go") {
			return nil
		}
		for _, m := range draftCite.FindAllStringSubmatch(string(body), -1) {
			if !drafts[m[1]] {
				t.Errorf("%s cites ADR-draft-%s, but there is no docs/adr/draft-%s.md — if it was just numbered, that citation should have been rewritten with it",
					rel, m[1], m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
}

// baseStatus strips a status's parenthetical. The index deliberately abbreviates
// "Accepted (amended 2026-08-17: …)" to "Accepted (amended)", so only the status
// word itself — Proposed, Accepted, Superseded, Deprecated — is comparable.
func baseStatus(s string) string {
	if i := strings.Index(s, "("); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// mustRel is only ever called with a path the walk produced under root.
func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relative path of %s: %v", path, err)
	}
	return rel
}

func namesOf(adrs []Record) []string {
	names := make([]string, len(adrs))
	for i, a := range adrs {
		names[i] = a.Name
	}
	return names
}

// TestRecordSlugsAreUnique catches the same decision filed twice under two names:
// most plausibly a draft whose copy survived the numbering, which would otherwise
// read as two records that merely happen to describe the same thing.
func TestRecordSlugsAreUnique(t *testing.T) {
	bySlug := make(map[string][]string)
	for _, r := range loadADRs(t) {
		bySlug[r.Slug] = append(bySlug[r.Slug], r.Name)
	}
	for slug, names := range bySlug {
		if len(names) > 1 {
			sort.Strings(names)
			t.Errorf("slug %q is used by %s — one decision, one record", slug, strings.Join(names, ", "))
		}
	}
}

// TestNoWorkflowFileCitesADraft keeps the numbering mechanism able to run.
//
// The rewrite in number.go treats .yml as citable, and .github/workflows is not a
// skipped tree, so a draft citation in a workflow file makes the numbering commit
// touch that file. GITHUB_TOKEN may not: GitHub refuses a push from a GitHub App
// that "creates or updates" a workflow without the `workflows` permission, and the
// numbering job deliberately does not hold it — a job that commits to main
// unreviewed should not be able to rewrite the definitions of CI itself.
//
// It is a sharp edge because nothing about writing the citation looks wrong. The
// job numbers the records, regenerates the feed, verifies the result, and only then
// fails on the push — so main keeps the drafts, and every later merge fails the same
// way until somebody removes the citation. It happened once, to a comment in
// helm.yml explaining why the chart's default render is the authenticated one.
//
// A numbered citation is fine, and stays useful: a number is never reassigned, so
// nothing rewrites it. Only the draft form is a problem, and only until it is
// numbered — which is exactly when the damage is done.
func TestNoWorkflowFileCitesADraft(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	// Both forms the rewrite replaces: the citation and the link to the file.
	cite := regexp.MustCompile(`ADR-draft-[a-z0-9-]+`)
	link := regexp.MustCompile(`draft-[a-z0-9-]+\.md`)
	seen := 0
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || !citable[ext] {
			continue
		}
		seen++
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range append(cite.FindAllString(string(body), -1), link.FindAllString(string(body), -1)...) {
			t.Errorf("%s cites %s. The numbering job rewrites that citation, which makes its commit "+
				"touch a workflow file, which GITHUB_TOKEN may not push — so every merge to main would "+
				"leave the records unnumbered. Name the decision in prose here, or cite it by number "+
				"once it has one.", e.Name(), m)
		}
	}
	if seen == 0 {
		t.Fatalf("no workflow files found under %s; this guard would pass vacuously", dir)
	}
}
