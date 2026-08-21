package adr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepo lays out a miniature repository — docs/adr plus a little code that
// cites a record — so the merge-time numbering can be exercised end to end
// without touching the real directory.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

const miniIndex = `# Architecture Decision Records

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-first.md) | The first decision | Accepted |

## Status values

- **Proposed** — under discussion
`

const firstRecord = `# ADR-0001: The first decision

- **Status:** Accepted
`

// TestLoadRecordsReadsNumberedAndDraftRecords is the shape the rest of this
// package rests on: a numbered record carries its number in both the file name
// and the heading, and a record still in flight carries neither.
func TestLoadRecordsReadsNumberedAndDraftRecords(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":       miniIndex,
		"docs/adr/template.md":     "# ADR-NNNN: Title\n",
		"docs/adr/0001-first.md":   firstRecord,
		"docs/adr/draft-second.md": "# ADR-DRAFT: The second decision\n\n- **Status:** Proposed\n",
	})

	records, err := LoadRecords(filepath.Join(root, "docs", "adr"))
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (README.md and template.md are not records)", len(records))
	}
	if got := records[0]; got.Num != 1 || got.Slug != "first" || got.Title != "The first decision" || got.Status != "Accepted" {
		t.Errorf("numbered record parsed as %+v", got)
	}
	if got := records[1]; got.Num != 0 || !got.IsDraft() || got.Slug != "second" || got.Title != "The second decision" {
		t.Errorf("draft parsed as %+v", got)
	}
}

// TestLoadRecordsRejectsAMisshapenRecord keeps the parser strict: every failure
// mode it can name here is one that would otherwise surface as a broken index or
// an unresolvable citation much later.
func TestLoadRecordsRejectsAMisshapenRecord(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"a number in the draft heading": {
			"docs/adr/draft-second.md": "# ADR-0002: The second decision\n\n- **Status:** Proposed\n",
		},
		"a draft heading on a numbered file": {
			"docs/adr/0002-second.md": "# ADR-DRAFT: The second decision\n\n- **Status:** Proposed\n",
		},
		"heading and file name disagree": {
			"docs/adr/0002-second.md": "# ADR-0003: The second decision\n\n- **Status:** Proposed\n",
		},
		"no status line": {
			"docs/adr/draft-second.md": "# ADR-DRAFT: The second decision\n",
		},
		"not a record file name": {
			"docs/adr/notes.md": "# ADR-DRAFT: Notes\n\n- **Status:** Proposed\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			files["docs/adr/README.md"] = miniIndex
			root := writeRepo(t, files)
			if _, err := LoadRecords(filepath.Join(root, "docs", "adr")); err == nil {
				t.Fatal("LoadRecords accepted it; want an error")
			}
		})
	}
}

// TestAssignNumbersNumbersEveryDraft is the merge-time half of the scheme: the
// draft becomes a numbered record, the heading follows the file name, the index
// gains a row, and every citation of the draft moves with it.
func TestAssignNumbersNumbersEveryDraft(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":       miniIndex,
		"docs/adr/0001-first.md":   firstRecord,
		"docs/adr/draft-second.md": "# ADR-DRAFT: The second decision\n\n- **Status:** Proposed\n",
		"engine/thing.go":          "package engine\n\n// Why it works this way (ADR-draft-second).\n// See docs/adr/draft-second.md for the argument.\nvar x = 1\n",
	})

	assigned, err := AssignNumbers(root)
	if err != nil {
		t.Fatalf("AssignNumbers: %v", err)
	}
	if len(assigned) != 1 {
		t.Fatalf("got %d assignments, want 1", len(assigned))
	}
	if a := assigned[0]; a.Num != 2 || a.From != "draft-second.md" || a.To != "0002-second.md" {
		t.Errorf("assignment is %+v, want ADR-0002 from draft-second.md", a)
	}

	if _, err := os.Stat(filepath.Join(root, "docs", "adr", "draft-second.md")); !os.IsNotExist(err) {
		t.Error("the draft file is still there after numbering")
	}
	if got := read(t, root, "docs/adr/0002-second.md"); !strings.HasPrefix(got, "# ADR-0002: The second decision\n") {
		t.Errorf("numbered record starts with %q", strings.SplitN(got, "\n", 2)[0])
	}

	code := read(t, root, "engine/thing.go")
	if !strings.Contains(code, "(ADR-0002)") {
		t.Errorf("the ADR-draft-second citation was not rewritten:\n%s", code)
	}
	if !strings.Contains(code, "docs/adr/0002-second.md") {
		t.Errorf("the draft-second.md link was not rewritten:\n%s", code)
	}
	if strings.Contains(code, "draft-second") {
		t.Errorf("a draft citation survived numbering:\n%s", code)
	}

	readme := read(t, root, "docs/adr/README.md")
	wantRow := "| [0002](0002-second.md) | The second decision | Proposed |"
	if !strings.Contains(readme, wantRow) {
		t.Errorf("index has no row %q:\n%s", wantRow, readme)
	}
	lines := strings.Split(readme, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "| [0001]") && lines[i+1] != wantRow {
			t.Errorf("the new row does not directly follow ADR-0001's; a blank line between rows ends the table")
		}
	}
}

// TestAssignNumbersOrdersSeveralDraftsBySlug pins the order down. Two records
// merging before anyone numbers them is the normal case, not the exception, and
// the numbers they end up with must not depend on directory iteration order.
func TestAssignNumbersOrdersSeveralDraftsBySlug(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":       miniIndex,
		"docs/adr/0001-first.md":   firstRecord,
		"docs/adr/draft-zulu.md":   "# ADR-DRAFT: Zulu\n\n- **Status:** Proposed\n",
		"docs/adr/draft-alpha.md":  "# ADR-DRAFT: Alpha\n\n- **Status:** Accepted\n",
		"docs/adr/draft-alpha2.md": "# ADR-DRAFT: Alpha two\n\n- **Status:** Accepted\n",
		"engine/thing.go":          "package engine\n\n// ADR-draft-alpha and ADR-draft-alpha2 are different records.\nvar x = 1\n",
	})

	assigned, err := AssignNumbers(root)
	if err != nil {
		t.Fatalf("AssignNumbers: %v", err)
	}
	got := make(map[string]int, len(assigned))
	for _, a := range assigned {
		got[a.Slug] = a.Num
	}
	want := map[string]int{"alpha": 2, "alpha2": 3, "zulu": 4}
	for slug, num := range want {
		if got[slug] != num {
			t.Errorf("%s got ADR-%04d, want ADR-%04d (drafts are numbered in slug order)", slug, got[slug], num)
		}
	}

	// ADR-draft-alpha is a prefix of ADR-draft-alpha2: rewriting the short slug
	// first would turn the longer citation into "ADR-00022".
	code := read(t, root, "engine/thing.go")
	if !strings.Contains(code, "ADR-0002 and ADR-0003 are different records") {
		t.Errorf("prefix slugs were rewritten wrong:\n%s", code)
	}
}

// TestAssignNumbersIsANoOpWithoutDrafts matters because the workflow runs it on
// every push to main, most of which carry no record at all.
func TestAssignNumbersIsANoOpWithoutDrafts(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":     miniIndex,
		"docs/adr/0001-first.md": firstRecord,
	})

	assigned, err := AssignNumbers(root)
	if err != nil {
		t.Fatalf("AssignNumbers: %v", err)
	}
	if len(assigned) != 0 {
		t.Fatalf("got %d assignments, want none", len(assigned))
	}
	if got := read(t, root, "docs/adr/README.md"); got != miniIndex {
		t.Errorf("the index was rewritten with nothing to number:\n%s", got)
	}
}

// TestAssignNumbersRefusesABrokenDirectory: numbering renames files and rewrites
// citations across the whole repository. Doing that on top of a directory that is
// already inconsistent turns one problem into a scattered one.
func TestAssignNumbersRefusesABrokenDirectory(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":      miniIndex,
		"docs/adr/0001-first.md":  firstRecord,
		"docs/adr/0002-second.md": "# ADR-0009: Mismatched\n\n- **Status:** Accepted\n",
		"docs/adr/draft-third.md": "# ADR-DRAFT: The third decision\n\n- **Status:** Proposed\n",
	})

	if _, err := AssignNumbers(root); err == nil {
		t.Fatal("AssignNumbers ran against a broken directory; want an error")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "adr", "draft-third.md")); err != nil {
		t.Errorf("the draft was numbered anyway: %v", err)
	}
}

// TestAssignNumbersRejectsATitleThatBreaksTheTable — a pipe in a title would end
// the cell early and quietly corrupt the index for every row after it.
func TestAssignNumbersRejectsATitleThatBreaksTheTable(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":     miniIndex,
		"docs/adr/0001-first.md": firstRecord,
		"docs/adr/draft-good.md": "# ADR-DRAFT: A fine title\n\n- **Status:** Proposed\n",
		"docs/adr/draft-pipe.md": "# ADR-DRAFT: A | in the title\n\n- **Status:** Proposed\n",
	})

	if _, err := AssignNumbers(root); err == nil {
		t.Fatal("AssignNumbers accepted a title containing a pipe")
	}
	// And it refused before touching anything: the good draft sorts first, so a
	// check inside the rename loop would already have numbered it.
	if _, err := os.Stat(filepath.Join(root, "docs", "adr", "draft-good.md")); err != nil {
		t.Errorf("a draft was numbered before the run refused: %v", err)
	}
}

// TestLoadRecordsOnAMissingDirectory: the numbering runs from a repository root
// given on the command line, and a wrong one should say so rather than report an
// empty directory as "nothing to number".
func TestLoadRecordsOnAMissingDirectory(t *testing.T) {
	if _, err := LoadRecords(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("LoadRecords accepted a directory that does not exist")
	}
	if _, err := AssignNumbers(t.TempDir()); err == nil {
		t.Fatal("AssignNumbers accepted a root with no docs/adr")
	}
}

// TestParseRecordNamesEveryDefect covers the shapes a half-written record takes,
// because each of them reaches the guard tests as a confusing failure somewhere
// else — a missing index row, an unresolvable link — rather than as itself.
func TestParseRecordNamesEveryDefect(t *testing.T) {
	for name, body := range map[string]string{
		"draft with no heading":   "Some prose.\n\n- **Status:** Proposed\n",
		"draft with no status":    "# ADR-DRAFT: A title\n",
		"draft with both missing": "Some prose.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRecord("draft-thing.md", body); err == nil {
				t.Fatalf("parseRecord accepted %q", body)
			}
		})
	}
	if _, err := parseRecord("0002-thing.md", "Some prose.\n\n- **Status:** Accepted\n"); err == nil {
		t.Fatal("parseRecord accepted a numbered record with no heading")
	}
}

// TestAssignNumbersNeedsAnIndexToAppendTo: without a table there is nowhere to put
// the row, and silently skipping it is how a record ends up numbered but invisible.
func TestAssignNumbersNeedsAnIndexToAppendTo(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":      "# Architecture Decision Records\n\nNo table here.\n",
		"docs/adr/draft-first.md": "# ADR-DRAFT: The first decision\n\n- **Status:** Proposed\n",
	})

	if _, err := AssignNumbers(root); err == nil {
		t.Fatal("AssignNumbers accepted a README with no index table")
	}
}

// TestRewriteSkipsWhatItCannotVouchFor pins the reach of the citation rewrite: a
// vendored tree is not ours to edit, and a file type outside the allowlist is one
// this pass deliberately does not touch. The draft-citation guard test in
// adr_test.go is what catches the second case, so it must stay observable.
func TestRewriteSkipsWhatItCannotVouchFor(t *testing.T) {
	const cite = "see ADR-draft-second\n"
	root := writeRepo(t, map[string]string{
		"docs/adr/README.md":            miniIndex,
		"docs/adr/0001-first.md":        firstRecord,
		"docs/adr/draft-second.md":      "# ADR-DRAFT: The second decision\n\n- **Status:** Proposed\n",
		"api/web/vendor/thing/notes.md": cite,
		"docs/notes.rst":                cite,
		"engine/thing.go":               "package engine\n\n// " + cite,
	})

	if _, err := AssignNumbers(root); err != nil {
		t.Fatalf("AssignNumbers: %v", err)
	}
	if got := read(t, root, "engine/thing.go"); !strings.Contains(got, "ADR-0002") {
		t.Errorf("a Go comment was not rewritten: %q", got)
	}
	for _, untouched := range []string{"api/web/vendor/thing/notes.md", "docs/notes.rst"} {
		if got := read(t, root, untouched); got != cite {
			t.Errorf("%s was rewritten to %q; it is outside what this pass touches", untouched, got)
		}
	}
}
