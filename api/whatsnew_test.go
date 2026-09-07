package api

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Console "What's New" feed is a static asset (web/whats-new.json) generated
// from CHANGELOG.md by scripts/whats-new/gen.mjs. It is served straight off the
// embedded FS with no Go code in the path, so nothing else would notice if it were
// regenerated into garbage, emptied, or hand-edited into an invalid shape. This test
// guards the served file: it must parse, be non-empty, and every entry must carry
// the fields the UI relies on (an English title and summary, an http(s) link), with
// any "Try it" route pointing inside the app and any dates in newest-first order.
func TestWhatsNewJSONIsValid(t *testing.T) {
	raw, err := webFS.ReadFile("web/whats-new.json")
	if err != nil {
		t.Fatalf("read embedded whats-new.json: %v", err)
	}

	type bilingual struct {
		En string `json:"en"`
		De string `json:"de"`
	}
	type link struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	type tryLink struct {
		Label bilingual `json:"label"`
		Route string    `json:"route"`
	}
	type entry struct {
		ID       string    `json:"id"`
		Date     string    `json:"date"`
		Version  string    `json:"version"`
		Title    bilingual `json:"title"`
		Summary  bilingual `json:"summary"`
		Link     link      `json:"link"`
		Tags     []string  `json:"tags"`
		Tutorial *struct {
			En []string `json:"en"`
			De []string `json:"de"`
		} `json:"tutorial"`
		Try *tryLink `json:"try"`
	}
	var doc struct {
		GeneratedAt string  `json:"generatedAt"`
		Entries     []entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("whats-new.json is not valid JSON: %v", err)
	}
	if len(doc.Entries) == 0 {
		t.Fatal("whats-new.json has no entries; the section would render empty")
	}

	seen := map[string]bool{}
	prevDate := "￿" // sorts after any real date, so the first entry always passes
	for i, e := range doc.Entries {
		if e.ID == "" {
			t.Errorf("entry %d: empty id", i)
		}
		if seen[e.ID] {
			t.Errorf("entry %d: duplicate id %q", i, e.ID)
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.Title.En) == "" {
			t.Errorf("entry %d (%s): empty English title", i, e.ID)
		}
		if strings.TrimSpace(e.Summary.En) == "" {
			t.Errorf("entry %d (%s): empty English summary", i, e.ID)
		}
		// A summary opening with closing punctuation is a generator parse artifact
		// (e.g. the leftover "): …" of a half-stripped "(ADR-nnnn):" lead-in), not prose.
		if s := strings.TrimSpace(e.Summary.En); s != "" && strings.ContainsRune("):;,.—-", []rune(s)[0]) {
			t.Errorf("entry %d (%s): summary %q starts with punctuation; looks like a generator parse artifact", i, e.ID, s)
		}
		if !strings.HasPrefix(e.Link.URL, "http://") && !strings.HasPrefix(e.Link.URL, "https://") {
			t.Errorf("entry %d (%s): link url %q is not http(s)", i, e.ID, e.Link.URL)
		}
		if e.Try != nil && !strings.HasPrefix(e.Try.Route, "#/") {
			t.Errorf("entry %d (%s): try route %q does not point inside the app", i, e.ID, e.Try.Route)
		}
		// Newest-first: dated entries must not grow later down the list. Undated
		// (Unreleased) entries sit at the top and are skipped by this check.
		if e.Date != "" {
			if e.Date > prevDate {
				t.Errorf("entry %d (%s): date %q is newer than the entry above (%q); list must be newest-first", i, e.ID, e.Date, prevDate)
			}
			prevDate = e.Date
		}
	}
}

// TestWhatsNewGeneratorRefusesAConflictedChangelog covers the one input the feed's
// generator must not accept.
//
// The feed is derived from CHANGELOG.md, and .gitattributes marks the *output*
// unmergeable so git raises a conflict rather than interleaving two generated
// files. The documented resolution is to take the merged CHANGELOG and re-run the
// generator — but the two files change together, so at that moment the CHANGELOG
// is usually conflicted too, and the generator used to read straight past the
// markers: it looks for `- **bullets**`, and `<<<<<<< HEAD` is not one. Both sides
// then became two entries in a feed that looked perfectly well-formed, and CI's
// staleness check passed, because the committed file really was what the generator
// produced from that source. Only a reader would ever find out.
//
// The test drives the real script against a throwaway tree, because a guard that is
// only asserted in the language it is written in is a guard nobody runs.
func TestWhatsNewGeneratorRefusesAConflictedChangelog(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the generator is a JS script (CI has it)")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(root, "scripts", "whats-new", "gen.mjs")
	if _, err := os.Stat(gen); err != nil {
		t.Skipf("generator not present: %v", err)
	}

	// A throwaway tree with the layout the generator reads, so nothing here can
	// touch the repository's own CHANGELOG or feed.
	tmp := t.TempDir()
	for _, dir := range []string{
		filepath.Join(tmp, "api", "web"),
		filepath.Join(tmp, "docs", "adr"),
		filepath.Join(tmp, "scripts", "whats-new", "overrides"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	changelog := filepath.Join(tmp, "CHANGELOG.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(changelog, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() (string, error) {
		t.Helper()
		cmd := exec.Command(node, gen, "--root", tmp)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	const clean = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n" +
		"- **A thing happened.** And here is what it means.\n"
	write(clean)
	if out, err := run(); err != nil {
		t.Fatalf("the generator refused a clean CHANGELOG: %v\n%s", err, out)
	}

	const conflicted = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n" +
		"<<<<<<< HEAD\n- **One side.** A.\n=======\n- **The other side.** B.\n>>>>>>> origin/main\n"
	write(conflicted)
	out, err := run()
	if err == nil {
		t.Fatalf("the generator accepted a conflicted CHANGELOG:\n%s", out)
	}
	if !strings.Contains(out, "unresolved merge conflicts") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if !strings.Contains(out, "CHANGELOG.md") {
		t.Errorf("the refusal does not name the file:\n%s", out)
	}
}
