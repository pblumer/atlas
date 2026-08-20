// Package adr carries no code — it exists so the ADR directory can guard its own
// conventions with a Go test that runs in the mandatory `go test ./...` sweep.
//
// The failure this catches is real and has happened three times: two feature
// branches each take "the next free ADR number", both merge, and the repository
// ends up with two unrelated decisions sharing one number. Every `(ADR-NNNN)`
// comment pointing at that number is then ambiguous, and the ambiguity spreads
// through the code as later work cites it. Numbers 0090, 0103 and 0105 each
// collided that way before this test existed; the later ADR of each pair was
// renumbered to 0139, 0140 and 0141. The branch that did so then collided with a
// fresh 0138 on main while it was open — which is the whole argument for a test
// that runs before the merge rather than a convention that runs after it.
package adr

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// adrFile is one decision record on disk: its number, its file name, and the
// title and status parsed out of the document itself.
type adrFile struct {
	num    int
	name   string
	title  string
	status string
}

var (
	fileNamePattern = regexp.MustCompile(`^(\d{4})-[a-z0-9-]+\.md$`)
	headingPattern  = regexp.MustCompile(`^# ADR-(\d{4}): (.+)$`)
	statusPattern   = regexp.MustCompile(`^- \*\*Status:\*\* (.+)$`)
	indexRowPattern = regexp.MustCompile(`^\| \[(\d{4})\]\(([^)]+)\) \| (.*) \| (.+) \|$`)
)

// loadADRs reads every NNNN-*.md in this directory. README.md and template.md are
// not decisions and carry no number, so they fall out by the file-name pattern.
func loadADRs(t *testing.T) []adrFile {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read adr dir: %v", err)
	}
	var adrs []adrFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		m := fileNamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			if e.Name() != "README.md" && e.Name() != "template.md" {
				t.Errorf("%s: not a NNNN-lowercase-slug.md file name", e.Name())
			}
			continue
		}
		num, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		a := adrFile{num: num, name: e.Name()}
		body, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if h := headingPattern.FindStringSubmatch(line); h != nil && a.title == "" {
				if h[1] != m[1] {
					t.Errorf("%s: heading says ADR-%s but the file name says ADR-%s", e.Name(), h[1], m[1])
				}
				a.title = h[2]
			}
			if s := statusPattern.FindStringSubmatch(line); s != nil && a.status == "" {
				a.status = strings.TrimSpace(s[1])
			}
		}
		if a.title == "" {
			t.Errorf("%s: no `# ADR-NNNN: title` heading", e.Name())
		}
		if a.status == "" {
			t.Errorf("%s: no `- **Status:** ...` line", e.Name())
		}
		adrs = append(adrs, a)
	}
	if len(adrs) == 0 {
		t.Fatal("no ADRs found — is the test running outside docs/adr?")
	}
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].name < adrs[j].name })
	return adrs
}

// TestADRNumbersAreUniqueAndContiguous is the guard against the collision this
// test was written for: one number, one decision. It also insists the numbers run
// 0001..N without a hole, so a branch that skips ahead is caught too — an ADR is
// never deleted (a superseded one keeps its number and says so), so a gap always
// means a number was misread rather than retired.
func TestADRNumbersAreUniqueAndContiguous(t *testing.T) {
	adrs := loadADRs(t)

	byNum := make(map[int][]string, len(adrs))
	for _, a := range adrs {
		byNum[a.num] = append(byNum[a.num], a.name)
	}
	for num, names := range byNum {
		if len(names) > 1 {
			sort.Strings(names)
			t.Errorf("ADR-%04d is used by %d files: %s — renumber all but one to the next free number",
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

// TestADRIndexMatchesDirectory keeps README.md's table honest: every ADR appears
// exactly once, in order, linked to the file that exists, with the title and
// status the document itself declares. Both ADRs missing from the index when the
// collisions were found (0090's and 0103's) would have failed here.
func TestADRIndexMatchesDirectory(t *testing.T) {
	adrs := loadADRs(t)

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	rows := indexRows(t, string(readme))

	if len(rows) != len(adrs) {
		t.Errorf("README index has %d rows for %d ADR files", len(rows), len(adrs))
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
		r, ok := indexed[a.num]
		if !ok {
			t.Errorf("%s is not in the README index — add a row for it", a.name)
			continue
		}
		if r.link != a.name {
			t.Errorf("README row for ADR-%04d links to %s, but the file is %s", a.num, r.link, a.name)
		}
		if r.title != a.title {
			t.Errorf("README row for ADR-%04d has title %q, but %s declares %q — the index carries the record's title, not a summary of it",
				a.num, r.title, a.name, a.title)
		}
		if got, want := baseStatus(r.status), baseStatus(a.status); got != want {
			t.Errorf("README row for ADR-%04d says status %q, but %s says %q", a.num, got, a.name, want)
		}
		delete(indexed, a.num)
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

// TestADRReferencesResolve checks that every `[ADR-NNNN](NNNN-....md)` link inside
// the records points at a file that exists. A renumbering that misses a link is
// the other half of the collision problem: the number in the prose and the file it
// links to drift apart silently.
func TestADRReferencesResolve(t *testing.T) {
	adrs := loadADRs(t)
	exists := make(map[string]bool, len(adrs))
	for _, a := range adrs {
		exists[a.name] = true
	}
	linkPattern := regexp.MustCompile(`\((\d{4}-[a-z0-9-]+\.md)\)`)
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

// baseStatus strips a status's parenthetical. The index deliberately abbreviates
// "Accepted (amended 2026-08-17: …)" to "Accepted (amended)", so only the status
// word itself — Proposed, Accepted, Superseded, Deprecated — is comparable.
func baseStatus(s string) string {
	if i := strings.Index(s, "("); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func namesOf(adrs []adrFile) []string {
	names := make([]string, len(adrs))
	for i, a := range adrs {
		names[i] = a.name
	}
	return names
}
