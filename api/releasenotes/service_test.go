package releasenotes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas"
)

func serve(t *testing.T, s *Service, path string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /release-notes", s.HandleList)
	mux.HandleFunc("GET /release-notes/{version}", s.HandleGet)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: body is not JSON: %v\n%s", path, err, rec.Body)
	}
	return rec.Code, body
}

func TestHandleListListsTheReleases(t *testing.T) {
	code, body := serve(t, New(fixture, ""), "/release-notes")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	rels, _ := body["releases"].([]any)
	if len(rels) != 3 {
		t.Fatalf("releases = %v, want three", body["releases"])
	}
	first, _ := rels[0].(map[string]any)
	if first["version"] != "Unreleased" || first["changeCount"] != float64(2) {
		t.Errorf("first release = %v", first)
	}
	if _, has := first["date"]; has {
		t.Errorf("Unreleased carries a date: %v", first)
	}
}

func TestHandleGetAnswersOneRelease(t *testing.T) {
	code, body := serve(t, New(fixture, ""), "/release-notes/0.8.0")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if body["version"] != "0.8.0" || body["date"] != "2026-09-30" {
		t.Errorf("release = %v", body)
	}
	if changes, _ := body["changes"].([]any); len(changes) != 2 {
		t.Errorf("changes = %v", body["changes"])
	}
}

func TestHandleGetRefusesAnUnknownVersion(t *testing.T) {
	code, body := serve(t, New(fixture, ""), "/release-notes/9.9.9")
	if code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "9.9.9") {
		t.Errorf("error = %q, want it to name the version", msg)
	}
}

// TestAReleaseWithNothingInItAnswersEmptyLists: the Console iterates both lists, so
// they are [] rather than null.
func TestAReleaseWithNothingInItAnswersEmptyLists(t *testing.T) {
	_, body := serve(t, New("## [0.1.0] — 2026-08-11\n\nOnly an intro.\n", ""), "/release-notes/0.1.0")
	if changes, ok := body["changes"].([]any); !ok || len(changes) != 0 {
		t.Errorf("changes = %#v, want []", body["changes"])
	}
	_, body = serve(t, New("## [0.1.0] — 2026-08-11\n\n### Added\n\n- **One.** Two.\n", ""), "/release-notes/0.1.0")
	if intro, ok := body["intro"].([]any); !ok || len(intro) != 0 {
		t.Errorf("intro = %#v, want []", body["intro"])
	}
}

// TestTheEmbeddedChangelogReadsWhole holds the parser to the file it is actually
// given. The What's New generator had a guard of this kind on its output; the release
// notes have no output to guard, so the guard reads the source the binary carries and
// asks whether everything in it reached the notes.
func TestTheEmbeddedChangelogReadsWhole(t *testing.T) {
	src := atlas.Changelog
	if conflict := regexp.MustCompile(`(?m)^(<{7}|={7}|>{7})( |$)`); conflict.MatchString(src) {
		t.Fatal("CHANGELOG.md still carries merge-conflict markers; the Console would show both sides as entries")
	}

	// Counted independently of the parser: every version heading, and every top-level
	// bullet under a category heading, outside code blocks.
	wantVersions := []string{}
	wantChanges := map[string]int{}
	version, inCategory, fence := "", false, false
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		switch {
		case fence:
		case strings.HasPrefix(line, "## ["):
			version = line[len("## ["):strings.Index(line, "]")]
			wantVersions = append(wantVersions, version)
			inCategory = false
		case strings.HasPrefix(line, "### "):
			inCategory = true
		case inCategory && strings.HasPrefix(line, "- "):
			wantChanges[version]++
		}
	}

	notes := Parse(src, Records(atlas.ADRIndex))
	got := notes.Releases()
	if len(got) == 0 {
		t.Fatal("the embedded CHANGELOG yields no release notes")
	}
	byVersion := map[string]Summary{}
	for _, r := range got {
		byVersion[r.Version] = r
	}
	for _, v := range wantVersions {
		r, ok := byVersion[v]
		switch {
		case !ok && v == "Unreleased" && wantChanges[v] == 0:
			// An empty Unreleased section is left out on purpose.
		case !ok:
			t.Errorf("release %s is in CHANGELOG.md but not in the release notes", v)
		case r.ChangeCount != wantChanges[v]:
			t.Errorf("release %s: %d changes read, CHANGELOG.md has %d bullets — one was swallowed or split",
				v, r.ChangeCount, wantChanges[v])
		}
	}

	// Newest first, which is the order the Console shows them in.
	last := "9999-99-99"
	for _, r := range got {
		if r.Date == "" {
			continue
		}
		if r.Date > last {
			t.Errorf("release %s (%s) is listed after an older one (%s)", r.Version, r.Date, last)
		}
		last = r.Date
	}

	// A headline or a text that starts with punctuation is the signature of a parse
	// artefact rather than prose: a lead-in the parser failed to strip. And a record an
	// entry cites resolves to its file through the embedded index — a citation the
	// index does not hold would link to the directory instead.
	artefact := regexp.MustCompile(`^[):;,.—]`)
	for _, s := range got {
		rel, _ := notes.Release(s.Version)
		for _, c := range rel.Changes {
			if c.Title == "" || artefact.MatchString(c.Title) || artefact.MatchString(c.Text) {
				t.Errorf("release %s: change %q / %q reads as a parse artefact", s.Version, c.Title, c.Text)
			}
			if c.Link != nil && strings.HasSuffix(c.Link.URL, "/tree/main/docs/adr") {
				t.Errorf("release %s: %q cites %s, which the embedded index does not name", s.Version, c.Title, c.Link.Label)
			}
		}
	}
}
