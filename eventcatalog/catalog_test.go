package eventcatalog_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/logging"
)

// The catalogue is held to itself here: every entry says everything ADR-0435 §1 asks
// of it, in both languages, names a test that exists and a version that does, and a
// stable entry never loses a field. Whether the code emits what the entries say is held
// in the api package, where the system processes and the feed are.

var update = flag.Bool("update", false, "rewrite the stable-entries golden file and the generated documentation")

// typeName is the form of a name (ADR-0435 §2): atlas.<subject>.<fact>, lower case,
// a hyphen inside a word.
var typeName = regexp.MustCompile(`^atlas\.[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$`)

func TestEveryEntrySaysWhatTheRecordAsks(t *testing.T) {
	versions := releasedVersions(t)
	tests := testFunctions(t)
	seen := map[string]bool{}
	for _, e := range eventcatalog.Entries {
		at := e.Type
		switch {
		case seen[e.Type]:
			t.Errorf("%s: listed twice", at)
		case !e.Shaped && !typeName.MatchString(e.Type):
			t.Errorf("%s: not atlas.<subject>.<fact>", at)
		}
		seen[e.Type] = true
		if e.Kind != eventcatalog.Domain && e.Kind != eventcatalog.Platform {
			t.Errorf("%s: kind %q", at, e.Kind)
		}
		if e.Meaning.EN == "" || e.Meaning.DE == "" {
			t.Errorf("%s: its meaning is not in both languages", at)
		}
		if e.Moment.Producer == "" || (e.Kind == eventcatalog.Domain && !e.Shaped && (e.Moment.Process == "" || e.Moment.Element == "")) {
			t.Errorf("%s: its moment does not say where it is emitted: %+v", at, e.Moment)
		}
		if len(e.Channels) == 0 {
			t.Errorf("%s: no channel", at)
		}
		for _, c := range e.Channels {
			switch c {
			case eventcatalog.Signal, eventcatalog.Message, eventcatalog.Feed, eventcatalog.Log:
			default:
				t.Errorf("%s: channel %q", at, c)
			}
			if e.Access[c] == "" {
				t.Errorf("%s: no access rule for its %s channel", at, c)
			}
			if e.Kind == eventcatalog.Platform && c == eventcatalog.Signal {
				t.Errorf("%s: a platform fact is not thrown as a BPMN signal (ADR-0435 §3)", at)
			}
		}
		if e.Has(eventcatalog.Log) && !slices.Contains(logging.Events(), e.LogEvent) {
			t.Errorf("%s: logs %q, which logging's catalogue does not have", at, e.LogEvent)
		}
		if !e.Has(eventcatalog.Log) && e.LogEvent != "" {
			t.Errorf("%s: names a log event without the log channel", at)
		}
		if !tests[e.NeverSecret] {
			t.Errorf("%s: its never-secret guard %q is not a test in this repository", at, e.NeverSecret)
		}
		if e.Since != eventcatalog.Unreleased && !versions[e.Since] {
			t.Errorf("%s: since %q, which is no release in CHANGELOG.md", at, e.Since)
		}
		if e.Stability != eventcatalog.Stable && e.Stability != eventcatalog.Experimental {
			t.Errorf("%s: stability %q", at, e.Stability)
		}
		if e.Listenable && !e.Has(eventcatalog.Signal) {
			t.Errorf("%s: listenable, but a model listens to a signal and this is none", at)
		}
		if e.Payload == nil {
			t.Errorf("%s: no payload list; an event without fields says so with an empty one", at)
		}
		names := map[string]bool{}
		for _, f := range e.Payload {
			if names[f.Name] {
				t.Errorf("%s: field %s twice", at, f.Name)
			}
			names[f.Name] = true
			switch f.Type {
			case "string", "number", "boolean", "object":
			default:
				t.Errorf("%s: field %s has type %q", at, f.Name, f.Type)
			}
			if f.Meaning.EN == "" || f.Meaning.DE == "" {
				t.Errorf("%s: field %s is not explained in both languages", at, f.Name)
			}
			// The access rule reads this marking, so an unmarked field is no answer
			// rather than "not personal" (ADR-0435 §5).
			if f.Data != eventcatalog.PersonalData && f.Data != eventcatalog.NotPersonal {
				t.Errorf("%s: field %s does not say whether it is personal data (%q)", at, f.Name, f.Data)
			}
			if secretLike.MatchString(f.Name) {
				t.Errorf("%s: field %s is named like a secret; an event never carries one", at, f.Name)
			}
		}
	}
	requested, ok := eventcatalog.Lookup(eventcatalog.UserRequested)
	if !ok {
		t.Fatal("Lookup does not find a catalogued name")
	}
	if got, want := requested.PersonalFields(), []string{"vorname", "nachname", "email", "benutzername", "begruendung"}; !slices.Equal(got, want) {
		t.Errorf("PersonalFields of %s = %v, want %v", eventcatalog.UserRequested, got, want)
	}
	if placed, _ := eventcatalog.Lookup(eventcatalog.OrderPlaced); placed.PersonalFields() != nil {
		t.Errorf("PersonalFields of %s = %v, want none", eventcatalog.OrderPlaced, placed.PersonalFields())
	}
	if _, ok := eventcatalog.Lookup("<message>.<outcome>"); ok {
		t.Error("Lookup found a shape by its placeholder")
	}
	if !eventcatalog.IsAtlasName(" atlas.user.requested") || eventcatalog.IsAtlasName("kunde.bestellt") {
		t.Error("IsAtlasName misreads the namespace")
	}
}

// secretLike is a field name that would carry a secret.
var secretLike = regexp.MustCompile(`(?i)pass(word)?|secret|token|credential|apikey`)

// stableGolden is the golden file of the stable entries: each with its fields, so a
// field removed from a stable entry fails here rather than in a receiver's parser.
const stableGolden = "testdata/stable.json"

func TestAStableEntryNeverLosesAField(t *testing.T) {
	current := map[string][]string{}
	for _, e := range eventcatalog.Entries {
		if e.Stability != eventcatalog.Stable {
			continue
		}
		fields := []string{}
		for _, f := range e.Payload {
			fields = append(fields, f.Name)
		}
		current[e.Type] = fields
	}
	if *update {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(current); err != nil {
			t.Fatalf("encode golden: %v", err)
		}
		if err := os.WriteFile(stableGolden, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	raw, err := os.ReadFile(stableGolden)
	if err != nil {
		t.Fatalf("read %s (go test ./eventcatalog -update writes it): %v", stableGolden, err)
	}
	var golden map[string][]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("%s: %v", stableGolden, err)
	}
	for typ, fields := range golden {
		have, ok := current[typ]
		if !ok {
			t.Errorf("%s was a stable entry and is gone or no longer stable; a stable event is not withdrawn", typ)
			continue
		}
		for _, f := range fields {
			if !slices.Contains(have, f) {
				t.Errorf("%s lost its field %s; a stable entry only ever gains fields", typ, f)
			}
		}
	}
	for typ := range current {
		if _, ok := golden[typ]; !ok {
			t.Errorf("%s is stable and not in %s; run go test ./eventcatalog -update to record it", typ, stableGolden)
		}
	}
}

// TestTheCatalogueImportsNothingOfTheEngine: every one of the engine, the server and
// the state store may import the catalogue, so it imports none of them — nothing of
// this module at all.
func TestTheCatalogueImportsNothingOfTheEngine(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "github.com/pblumer/atlas") {
				t.Errorf("%s imports %s; the catalogue depends on nothing of atlas", f, path)
			}
		}
	}
}

// releasedVersions are the versions CHANGELOG.md has a heading for.
func releasedVersions(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindAllStringSubmatch(string(raw), -1) {
		out[m[1]] = true
	}
	return out
}

// testFunctions are the names of every test function in the repository.
func testFunctions(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)`)
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range decl.FindAllSubmatch(raw, -1) {
			out[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// TestTheDocumentationIsTheCatalogue: the handbook's chapter and the runtime
// contract's table are the entries rendered; a document that has fallen behind fails
// here, and go test ./eventcatalog -update brings it level.
func TestTheDocumentationIsTheCatalogue(t *testing.T) {
	for _, doc := range []struct {
		path, begin, end, generated string
	}{
		{"../api/web/handbuch.html", eventcatalog.HandbookBegin, eventcatalog.HandbookEnd, eventcatalog.HandbookHTML() + "      "},
		{"../docs/runtime-contract.md", eventcatalog.ContractBegin, eventcatalog.ContractEnd, eventcatalog.ContractTable()},
	} {
		raw, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("read %s: %v", doc.path, err)
		}
		want, ok := eventcatalog.Splice(string(raw), doc.begin, doc.end, doc.generated)
		if !ok {
			t.Errorf("%s has no generated block between %q and %q", doc.path, doc.begin, doc.end)
			continue
		}
		if *update && want != string(raw) {
			if err := os.WriteFile(doc.path, []byte(want), 0o644); err != nil {
				t.Fatalf("write %s: %v", doc.path, err)
			}
			continue
		}
		if want != string(raw) {
			t.Errorf("%s no longer matches the event catalogue; run go test ./eventcatalog -update", doc.path)
		}
	}
	if _, ok := eventcatalog.Splice("no markers", eventcatalog.HandbookBegin, eventcatalog.HandbookEnd, "x"); ok {
		t.Error("Splice found markers that are not there")
	}
}
