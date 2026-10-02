package releasenotes

import (
	"reflect"
	"testing"
)

// fixture is a CHANGELOG in the shape the real one has: an Unreleased section with
// no preamble, a release with a preamble of paragraphs and an upgrade list, wrapped
// headlines, a lead-in reference after a headline, continuation paragraphs that are
// not part of the summary, and the link definitions Keep a Changelog ends with.
const fixture = `# Changelog

All notable changes to Atlas are documented here.

## [Unreleased]

### Added

- **A mail Worker can read its mailbox,
  and the mailbox stays its owner's.** An inbound watch publishes the new mail of one
  folder as an Atlas message. ADR-0438.

  A second paragraph the summary leaves out.

  - and a nested list it leaves out too

### Fixed

- **A wrapped [linked](docs/x.md) headline** ([ADR-0163](docs/adr/0163-dataset-snapshot.md)):
  the lead-in is dropped and ` + "`code`" + ` is kept (#1201).

## [0.8.0] — 2026-09-30

**This release is about Windows.** 0.7.0 could not start twice.

**Read this before upgrading from 0.7.0.** These act on an existing installation:

- A create nobody triggered is refused with **409**.
- Treat the upgrade as one-way,
  and take a backup.

### Security

- **A vault key is this account's alone.** See ADR-0163 again.

- A plain bullet with no headline. It still counts.

## [0.1.0] — 2026-08-11

### Notes

- **License:** AGPL-3.0-only.

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/pblumer/atlas/compare/v0.7.0...v0.8.0
`

func TestParseReadsReleasesNewestFirst(t *testing.T) {
	n := Parse(fixture, nil)
	got := n.Releases()
	want := []Summary{
		{Version: "Unreleased", ChangeCount: 2},
		{Version: "0.8.0", Date: "2026-09-30", ChangeCount: 2},
		{Version: "0.1.0", Date: "2026-08-11", ChangeCount: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Releases() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestParseReadsAChange(t *testing.T) {
	r, ok := Parse(fixture, nil).Release("Unreleased")
	if !ok {
		t.Fatal("Unreleased not found")
	}
	want := []Change{
		{
			Category: "Added",
			Title:    "A mail Worker can read its mailbox, and the mailbox stays its owner's.",
			Text:     "An inbound watch publishes the new mail of one folder as an Atlas message. ADR-0438.",
			// No other bullet links the record with a path, so the link names it and
			// opens the directory it is in.
			Link: &Link{Label: "ADR-0438", URL: repoURL + "/tree/main/docs/adr"},
		},
		{
			Category: "Fixed",
			Title:    "A wrapped linked headline",
			Text:     "the lead-in is dropped and `code` is kept (#1201).",
			// A pull request wins over a record, as the most user-facing reference.
			Link: &Link{Label: "PR #1201", URL: repoURL + "/pull/1201"},
		},
	}
	if !reflect.DeepEqual(r.Changes, want) {
		t.Fatalf("changes =\n  %+v\nwant\n  %+v", r.Changes, want)
	}
	if r.Link.URL != repoURL+"/blob/main/CHANGELOG.md#unreleased" {
		t.Errorf("Unreleased links to %q, want main's CHANGELOG at its section", r.Link.URL)
	}
}

func TestParseReadsAReleasePreamble(t *testing.T) {
	r, ok := Parse(fixture, nil).Release("0.8.0")
	if !ok {
		t.Fatal("0.8.0 not found")
	}
	want := []Block{
		{Kind: "p", Text: "**This release is about Windows.** 0.7.0 could not start twice."},
		{Kind: "p", Text: "**Read this before upgrading from 0.7.0.** These act on an existing installation:"},
		{Kind: "list", Items: []string{
			"A create nobody triggered is refused with **409**.",
			"Treat the upgrade as one-way, and take a backup.",
		}},
	}
	if !reflect.DeepEqual(r.Intro, want) {
		t.Fatalf("intro =\n  %+v\nwant\n  %+v", r.Intro, want)
	}
	// A record spelled out anywhere in the document resolves a bare citation of it.
	if got := r.Changes[0].Link; got == nil || got.URL != repoURL+"/blob/main/docs/adr/0163-dataset-snapshot.md" {
		t.Errorf("bare ADR-0163 links to %+v, want the file another entry names", got)
	}
	// A bullet without a headline is still a change: its first sentence stands in.
	if c := r.Changes[1]; c.Title != "A plain bullet with no headline." || c.Text != "It still counts." {
		t.Errorf("plain bullet = %+v", c)
	}
	// A released version links to the CHANGELOG as it was tagged, at its section.
	if r.Link.URL != repoURL+"/blob/v0.8.0/CHANGELOG.md#080--2026-09-30" {
		t.Errorf("0.8.0 links to %q", r.Link.URL)
	}
}

// TestParseDropsAnEmptyRelease: a binary built from a tag carries an Unreleased
// heading with nothing under it, and a release that says nothing is not one to list.
func TestParseDropsAnEmptyRelease(t *testing.T) {
	n := Parse("## [Unreleased]\n\n## [0.2.0] — 2026-08-19\n\n### Added\n\n- **One.** Two.\n", nil)
	if got := n.Releases(); len(got) != 1 || got[0].Version != "0.2.0" {
		t.Fatalf("Releases() = %+v, want 0.2.0 alone", got)
	}
	if _, ok := n.Release("Unreleased"); ok {
		t.Error("an empty Unreleased section is still served")
	}
}

func TestParseOfNothingIsEmpty(t *testing.T) {
	if got := Parse("", nil).Releases(); len(got) != 0 {
		t.Fatalf("Releases() = %+v, want none", got)
	}
}

func TestAnchorIsGitHubs(t *testing.T) {
	for heading, want := range map[string]string{
		"[Unreleased]":         "unreleased",
		"[0.8.0] — 2026-09-30": "080--2026-09-30",
		"[0.1.0] - 2026-08-11": "010---2026-08-11",
	} {
		if got := anchor(heading); got != want {
			t.Errorf("anchor(%q) = %q, want %q", heading, got, want)
		}
	}
}

// TestParseReadsTheHeadlineShapesTheChangelogUses: a headline need not end in a
// stop, and bold the sentence runs on from is emphasis rather than a headline.
func TestParseReadsTheHeadlineShapesTheChangelogUses(t *testing.T) {
	r, _ := Parse("## [0.1.0] — 2026-08-11\n\n### Added\n\n"+
		"- **A loop keeps its result**. Taking a flow is one operation.\n"+
		"- **Receive tasks**, and **boundary events**. Both wait.\n", nil).Release("0.1.0")
	want := []Change{
		{Category: "Added", Title: "A loop keeps its result", Text: "Taking a flow is one operation."},
		{Category: "Added", Title: "**Receive tasks**, and **boundary events**.", Text: "Both wait."},
	}
	if !reflect.DeepEqual(r.Changes, want) {
		t.Fatalf("changes =\n  %+v\nwant\n  %+v", r.Changes, want)
	}
}

// TestParseReadsTheMarkdownAroundABullet: a wrapped line may start at column 0, a code
// block's lines are neither bullets nor headings, a bullet with no sentence end is
// its own headline, and a second-level heading that is not a version ends the notes.
func TestParseReadsTheMarkdownAroundABullet(t *testing.T) {
	n := Parse("## [0.1.0] — 2026-08-11\n\n### Added\n\n"+
		"- **Lazy.** A line that wraps\nat column zero.\n\n"+
		"  ```yaml\n- not: a bullet\n## [9.9.9] not a release\n  ```\n\n"+
		"- No sentence end here\n\n"+
		"## Appendix\n\n- **Not a change.** It is past the releases.\n", nil)
	if got := n.Releases(); len(got) != 1 || got[0].ChangeCount != 2 {
		t.Fatalf("Releases() = %+v, want 0.1.0 with two changes", got)
	}
	r, _ := n.Release("0.1.0")
	want := []Change{
		{Category: "Added", Title: "Lazy.", Text: "A line that wraps at column zero."},
		{Category: "Added", Title: "No sentence end here"},
	}
	if !reflect.DeepEqual(r.Changes, want) {
		t.Fatalf("changes =\n  %+v\nwant\n  %+v", r.Changes, want)
	}
}

// TestALinkNamesTheRecordTheEntryIsAbout: an entry closes by citing its own record and
// cites the ones it builds on along the way, so the closing citation wins over the
// first one — and of a closing group, its first.
func TestALinkNamesTheRecordTheEntryIsAbout(t *testing.T) {
	r, _ := Parse("## [0.1.0] — 2026-08-11\n\n### Added\n\n"+
		"- **Mail.** It runs ADR-0205's claim it had\n  skipped. ADR-0438.\n"+
		"- **Classes.** Drawn from the class\n  ([ADR-0352](docs/adr/0352-draw.md),\n  [ADR-0237](docs/adr/0237-canvas.md)).\n"+
		"- **Lead-in** ([ADR-0101](docs/adr/0101-first.md)): text that mentions ADR-0237 and goes on.\n", nil).Release("0.1.0")
	want := []string{
		repoURL + "/tree/main/docs/adr",               // ADR-0438: nothing names its file
		repoURL + "/blob/main/docs/adr/0352-draw.md",  // the first of the closing group
		repoURL + "/blob/main/docs/adr/0101-first.md", // no closing citation: the first
	}
	for i, c := range r.Changes {
		if c.Link == nil || c.Link.URL != want[i] {
			t.Errorf("%q links to %+v, want %s", c.Title, c.Link, want[i])
		}
	}
	if r.Changes[0].Link.Label != "ADR-0438" {
		t.Errorf("label = %q, want ADR-0438", r.Changes[0].Link.Label)
	}
}

// TestTheIndexNamesARecordsFile: most entries cite a record by its number alone, and
// the index is where its file name is read from.
func TestTheIndexNamesARecordsFile(t *testing.T) {
	index := "| ADR | Title | Status | Implementation |\n|---|---|---|---|\n" +
		"| [0438](0438-mailbox-worker.md) | The mail Worker reads its mailbox | Accepted | Landed |\n" +
		"See [the draft](draft-x.md) for one in flight.\n"
	records := Records(index)
	if want := map[string]string{"0438": "docs/adr/0438-mailbox-worker.md"}; !reflect.DeepEqual(records, want) {
		t.Fatalf("Records() = %v, want %v", records, want)
	}
	r, _ := Parse("## [0.1.0] — 2026-08-11\n\n### Added\n\n- **Mail.** It reads. ADR-0438.\n", records).Release("0.1.0")
	if got := r.Changes[0].Link; got == nil || got.URL != repoURL+"/blob/main/docs/adr/0438-mailbox-worker.md" {
		t.Errorf("link = %+v, want the record's file", got)
	}
}
