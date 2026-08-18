package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ADR-0134 Phase 4: the curated source layout. These tests pin the serialization
// itself — the half that has no server, no clock, and no store — because that is
// where a second serialization drifts from the records it mirrors.

func TestBuildSourceTreeLayout(t *testing.T) {
	app := project{ID: "app1", Name: "Mitarbeiter Onboarding", Key: "mitarbeiter-onboarding"}
	files, err := buildSourceTree(app,
		[]draft{{ProcessID: "onboarding", Name: "Onboarding", XML: "<definitions/>"}},
		[]form{{ID: "neuer-mitarbeiter", Name: "Neuer Mitarbeiter", Schema: `{"components":[]}`}},
		[]dmnRef{{ID: "d1", Name: "Freigabestufe", ModelRef: "temis://m/1"}},
	)
	if err != nil {
		t.Fatalf("buildSourceTree: %v", err)
	}
	want := []string{manifestPath, "processes/onboarding.bpmn", "forms/neuer-mitarbeiter.form.json"}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), pathsOf(files))
	}
	for i, p := range want {
		if files[i].Path != p {
			t.Errorf("file %d path=%q, want %q", i, files[i].Path, p)
		}
	}
	// Native content, verbatim but newline-terminated.
	if got := string(files[1].Data); got != "<definitions/>\n" {
		t.Errorf("bpmn content=%q", got)
	}
	if got := string(files[2].Data); got != `{"components":[]}`+"\n" {
		t.Errorf("form content=%q", got)
	}

	man, byPath, err := parseSourceTree(files)
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	if man.FormatVersion != sourceFormatVersion || man.Key != "mitarbeiter-onboarding" || man.Name != "Mitarbeiter Onboarding" {
		t.Errorf("manifest header = %+v", man)
	}
	if len(man.Processes) != 1 || man.Processes[0].ID != "onboarding" {
		t.Errorf("manifest processes = %+v", man.Processes)
	}
	// A DMN reference is a name plus a temis handle, so it lives entirely in the
	// manifest: Atlas never owns the model and must not write a .dmn file for it.
	if len(man.Decisions) != 1 || man.Decisions[0].ModelRef != "temis://m/1" {
		t.Errorf("manifest decisions = %+v", man.Decisions)
	}
	for _, p := range pathsOf(files) {
		if strings.HasPrefix(p, "decisions/") {
			t.Errorf("tree carries %q; DMN references have no file", p)
		}
	}
	if _, ok := byPath["processes/onboarding.bpmn"]; !ok {
		t.Errorf("byPath is missing the process file: %v", byPath)
	}
	// The manifest is formatted for a diff, not for the wire.
	if !bytes.Contains(files[0].Data, []byte("\n  \"key\": \"mitarbeiter-onboarding\"")) {
		t.Errorf("manifest is not indented:\n%s", files[0].Data)
	}
}

// TestBuildSourceTreeIsDeterministic is the property that makes the layout usable
// in git at all: the same records must produce the same bytes, whatever order they
// come out of the stores in. A tree that reshuffled would show up as a diff nobody
// can act on.
func TestBuildSourceTreeIsDeterministic(t *testing.T) {
	app := project{ID: "app1", Name: "Onboarding", Key: "onboarding"}
	drafts := []draft{
		{ProcessID: "zeta", Name: "Zeta", XML: "<z/>"},
		{ProcessID: "alpha", Name: "Alpha", XML: "<a/>"},
	}
	forms := []form{{ID: "f2", Name: "Zwei", Schema: "{}"}, {ID: "f1", Name: "Eins", Schema: "{}"}}
	refs := []dmnRef{{ID: "r2", Name: "B", ModelRef: "m2"}, {ID: "r1", Name: "A", ModelRef: "m1"}}

	first, err := buildSourceTree(app, drafts, forms, refs)
	if err != nil {
		t.Fatalf("buildSourceTree: %v", err)
	}
	// Reversed input, identical output.
	second, err := buildSourceTree(app,
		[]draft{drafts[1], drafts[0]}, []form{forms[1], forms[0]}, []dmnRef{refs[1], refs[0]})
	if err != nil {
		t.Fatalf("buildSourceTree (reversed): %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("file counts differ: %v vs %v", pathsOf(first), pathsOf(second))
	}
	for i := range first {
		if first[i].Path != second[i].Path || !bytes.Equal(first[i].Data, second[i].Data) {
			t.Fatalf("file %d differs: %q vs %q", i, first[i].Path, second[i].Path)
		}
	}
}

// TestBuildSourceTreeNeedsKey: the key is the identity a clone survives on, so a
// tree without one would be unimportable. Refuse to write it rather than emit a
// manifest nobody can read back.
func TestBuildSourceTreeNeedsKey(t *testing.T) {
	_, err := buildSourceTree(project{ID: "a", Name: "No Key"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "portable key") {
		t.Fatalf("err = %v, want a missing-key error", err)
	}
}

// TestSourcePathsDisambiguate: two artifacts may legitimately carry the same name,
// and two files may not carry the same path.
func TestSourcePathsDisambiguate(t *testing.T) {
	app := project{ID: "a", Name: "App", Key: "app"}
	files, err := buildSourceTree(app,
		[]draft{{ProcessID: "p1", Name: "Antrag"}, {ProcessID: "p2", Name: "Antrag"}},
		[]form{{ID: "f1", Name: "Antrag", Schema: "{}"}, {ID: "f2", Name: "Antrag", Schema: "{}"}},
		nil)
	if err != nil {
		t.Fatalf("buildSourceTree: %v", err)
	}
	got := pathsOf(files)
	want := []string{
		manifestPath,
		"processes/antrag.bpmn", "processes/antrag-2.bpmn",
		// The counter lands before the whole compound extension, so the second file
		// still reads as a form.
		"forms/antrag.form.json", "forms/antrag-2.form.json",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, fallback, want string }{
		{"Mitarbeiter Onboarding", "x", "mitarbeiter-onboarding"},
		{"Prüfung Änderung Groß", "x", "pruefung-aenderung-gross"},
		{"  Trim  --  Me  ", "x", "trim-me"},
		{"Order/To:Cash", "x", "order-to-cash"},
		{"Öffnung Grösse", "x", "oeffnung-groesse"},
		{"", "Process_1", "process-1"},
		// Nothing usable in either input: a file still needs a name.
		{"→→→", "✂", "artifact"},
		// A name that is only punctuation falls back to the id.
		{"...", "p1", "p1"},
	}
	for _, c := range cases {
		if got := slugify(c.in, c.fallback); got != c.want {
			t.Errorf("slugify(%q, %q) = %q, want %q", c.in, c.fallback, got, c.want)
		}
	}
}

func TestNewlineTerminatedIsIdempotent(t *testing.T) {
	once := newlineTerminated([]byte("x"))
	if string(once) != "x\n" {
		t.Fatalf("first = %q", once)
	}
	if twice := newlineTerminated(once); string(twice) != "x\n" {
		t.Fatalf("second = %q, want the same bytes", twice)
	}
	if got := newlineTerminated(nil); len(got) != 1 || got[0] != '\n' {
		t.Fatalf("empty input = %q", got)
	}
}

func TestParseSourceTreeRefusals(t *testing.T) {
	good := func() []sourceFile {
		files, err := buildSourceTree(
			project{ID: "a", Name: "App", Key: "app"},
			[]draft{{ProcessID: "p1", Name: "P", XML: "<x/>"}},
			[]form{{ID: "f1", Name: "F", Schema: "{}"}},
			nil)
		if err != nil {
			t.Fatalf("buildSourceTree: %v", err)
		}
		return files
	}

	cases := []struct {
		name  string
		files []sourceFile
		want  string
	}{
		{"no manifest", good()[1:], "no atlas.json"},
		{"manifest is not JSON", []sourceFile{{Path: manifestPath, Data: []byte("nope")}}, "invalid atlas.json"},
		{"future format", []sourceFile{{Path: manifestPath, Data: []byte(`{"formatVersion":99,"key":"a","name":"A"}`)}}, "unsupported format version"},
		{"no key", []sourceFile{{Path: manifestPath, Data: []byte(`{"formatVersion":1,"name":"A"}`)}}, "no application key"},
		{"no name", []sourceFile{{Path: manifestPath, Data: []byte(`{"formatVersion":1,"key":"a"}`)}}, "no application name"},
		{"process without id", []sourceFile{{Path: manifestPath, Data: []byte(
			`{"formatVersion":1,"key":"a","name":"A","processes":[{"path":"processes/x.bpmn"}]}`)}}, "process entry has no id"},
		{"form without id", []sourceFile{{Path: manifestPath, Data: []byte(
			`{"formatVersion":1,"key":"a","name":"A","forms":[{"path":"forms/x.form.json"}]}`)}}, "form entry has no id"},
		{"decision without id", []sourceFile{{Path: manifestPath, Data: []byte(
			`{"formatVersion":1,"key":"a","name":"A","decisions":[{"name":"D"}]}`)}}, "decision entry has no id"},
		// A manifest that names a file the tree does not carry is a half tree; a
		// partial import is exactly what the all-or-nothing posture exists to avoid.
		{"process file missing", good()[:1], `process "p1" names`},
		{"form file missing", good()[:2], `form "f1" names`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := parseSourceTree(c.files)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

// TestParseSourceTreeAcceptsHandWrittenManifest: the point of the layout is that a
// human can edit it. A minimal, hand-written manifest with no artifacts is valid.
func TestParseSourceTreeAcceptsHandWrittenManifest(t *testing.T) {
	man, _, err := parseSourceTree([]sourceFile{{
		Path: manifestPath,
		Data: []byte(`{"formatVersion": 1, "key": "onboarding", "name": "Onboarding"}`),
	}})
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	if man.Key != "onboarding" || len(man.Processes) != 0 {
		t.Fatalf("manifest = %+v", man)
	}
}

func TestCleanSourcePath(t *testing.T) {
	ok := map[string]string{
		"processes/x.bpmn":    "processes/x.bpmn",
		"./atlas.json":        "atlas.json",
		"a//b.bpmn":           "a/b.bpmn",
		"processes/./x.bpmn":  "processes/x.bpmn",
		`processes\x.bpmn`:    "processes/x.bpmn",
		"processes/y/../x.js": "processes/x.js",
	}
	for in, want := range ok {
		got, valid := cleanSourcePath(in)
		if !valid || got != want {
			t.Errorf("cleanSourcePath(%q) = %q, %v; want %q, true", in, got, valid, want)
		}
	}
	for _, in := range []string{"/etc/passwd", "../escape.bpmn", "..", ".", "", "a/../../b"} {
		if got, valid := cleanSourcePath(in); valid {
			t.Errorf("cleanSourcePath(%q) = %q, true; want rejected", in, got)
		}
	}
}

// TestSourceArchiveNameFallsBack: the download is named after the application's
// key, and after nothing in particular when the tree has no readable manifest —
// a download must still have a filename.
func TestSourceArchiveNameFallsBack(t *testing.T) {
	files, err := buildSourceTree(project{ID: "a", Name: "Onboarding", Key: "onboarding"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildSourceTree: %v", err)
	}
	if got := sourceArchiveName(files); got != "onboarding-source.tar.gz" {
		t.Errorf("name = %q", got)
	}
	if got := sourceArchiveName(nil); got != "atlas-application-source.tar.gz" {
		t.Errorf("fallback name = %q", got)
	}
}

// TestSourceArchiveRoundTrip covers the transport on its own: what the tar writer
// emits is what the tar reader gives back, paths and bytes alike.
func TestSourceArchiveRoundTrip(t *testing.T) {
	in := []sourceFile{
		{Path: manifestPath, Data: []byte("{}\n")},
		{Path: "processes/x.bpmn", Data: []byte("<x/>\n")},
	}
	var buf bytes.Buffer
	if err := writeSourceArchive(&buf, in); err != nil {
		t.Fatalf("writeSourceArchive: %v", err)
	}
	// No timestamps in the headers, so an unchanged application archives to the
	// same bytes every time.
	var again bytes.Buffer
	if err := writeSourceArchive(&again, in); err != nil {
		t.Fatalf("writeSourceArchive (again): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Errorf("archiving the same tree twice produced different bytes")
	}

	out, err := readSourceArchive(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("readSourceArchive: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d files, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Path != in[i].Path || !bytes.Equal(out[i].Data, in[i].Data) {
			t.Errorf("file %d = %q/%q, want %q/%q", i, out[i].Path, out[i].Data, in[i].Path, in[i].Data)
		}
	}
}

func TestReadSourceArchiveRefusals(t *testing.T) {
	if _, err := readSourceArchive(strings.NewReader("not gzip")); err == nil ||
		!strings.Contains(err.Error(), "gzip") {
		t.Errorf("plain text: err = %v, want a gzip complaint", err)
	}

	var buf bytes.Buffer
	if err := writeSourceArchive(&buf, []sourceFile{{Path: manifestPath, Data: []byte("{}")}}); err != nil {
		t.Fatalf("writeSourceArchive: %v", err)
	}
	truncated := buf.Bytes()[:len(buf.Bytes())-20]
	if _, err := readSourceArchive(bytes.NewReader(truncated)); err == nil {
		t.Errorf("truncated archive was accepted")
	}
}

// TestSourceManifestJSONShape pins the wire names, since the manifest is the part
// of the layout a human writes by hand and a later server reads back.
func TestSourceManifestJSONShape(t *testing.T) {
	data, err := json.Marshal(sourceManifest{
		FormatVersion: 1, Key: "k", Name: "N",
		Processes: []sourceProcess{{ID: "p", Name: "P", Path: "processes/p.bpmn"}},
		Forms:     []sourceForm{{ID: "f", Name: "F", Path: "forms/f.form.json"}},
		Decisions: []sourceDecision{{ID: "d", Name: "D", ModelRef: "m"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"formatVersion"`, `"key"`, `"name"`, `"processes"`, `"forms"`, `"decisions"`, `"path"`, `"modelRef"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("manifest JSON is missing %s: %s", key, data)
		}
	}
	// The random per-server application id must never appear in a manifest: it is
	// the thing the portable key exists to replace.
	if strings.Contains(string(data), `"id":"app`) {
		t.Errorf("manifest carries a server-local id: %s", data)
	}
}

func pathsOf(files []sourceFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// The paths a passing export or import never reaches: the lazy key backfill for
// an application that predates ADR-0134, and the store failures the handlers can
// only report. These use a bare Server holding just the stores under test — the
// functions run inside the caller's do() and touch nothing else.

// storesFor builds the four design-time stores over a temp dir.
func storesFor(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	projects, err := newProjectStore(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	drafts, err := newDraftStore(filepath.Join(dir, "drafts"))
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	forms, err := newFormStore(filepath.Join(dir, "forms"))
	if err != nil {
		t.Fatalf("newFormStore: %v", err)
	}
	refs, err := newDmnRefStore(filepath.Join(dir, "dmnrefs"))
	if err != nil {
		t.Fatalf("newDmnRefStore: %v", err)
	}
	return &Server{projects: projects, drafts: drafts, forms: forms, dmnrefs: refs}
}

// allow is the authorize callback for a caller who may write the application.
func allow(project) (int, string) { return 0, "" }

// TestApplicationKeyBackfill: an application saved before ADR-0134 carries no key.
// The first export derives one, saves it, and every later call returns the same —
// the key must not move once something has been matched on it.
func TestApplicationKeyBackfill(t *testing.T) {
	s := storesFor(t)
	legacy := project{ID: "old1", Name: "Alte Applikation", CreatedAt: 1, UpdatedAt: 1}
	if err := s.projects.save(legacy); err != nil {
		t.Fatalf("save: %v", err)
	}
	key, err := s.applicationKeyFor(legacy)
	if err != nil {
		t.Fatalf("applicationKeyFor: %v", err)
	}
	if key != "alte-applikation" {
		t.Fatalf("key = %q", key)
	}
	stored, ok, err := s.projects.get("old1")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if stored.Key != key {
		t.Errorf("key was not persisted: %q", stored.Key)
	}
	// Already keyed: returned as-is, and no second derivation.
	again, err := s.applicationKeyFor(stored)
	if err != nil || again != key {
		t.Errorf("second call = %q, %v; want %q", again, err, key)
	}
}

// TestExportApplicationSourceFiltersOtherApplications: the exporter ships this
// application's artifacts and no one else's.
func TestExportApplicationSourceFiltersOtherApplications(t *testing.T) {
	s := storesFor(t)
	mine := project{ID: "app1", Name: "Meine", Key: "meine"}
	if err := s.projects.save(mine); err != nil {
		t.Fatalf("save project: %v", err)
	}
	for _, d := range []draft{
		{ProcessID: "mine", Name: "Meins", ProjectID: "app1", XML: "<a/>"},
		{ProcessID: "theirs", Name: "Fremd", ProjectID: "app2", XML: "<b/>"},
		{ProcessID: "loose", Name: "Ungrouped", XML: "<c/>"},
	} {
		if err := s.drafts.save(d); err != nil {
			t.Fatalf("save draft: %v", err)
		}
	}
	if err := s.forms.save(form{ID: "f-theirs", Name: "Fremd", ProjectID: "app2", Schema: "{}"}); err != nil {
		t.Fatalf("save form: %v", err)
	}
	if err := s.dmnrefs.save(dmnRef{ID: "r-theirs", Name: "Fremd", ModelRef: "m", ProjectID: "app2"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	files, err := s.exportApplicationSource(mine)
	if err != nil {
		t.Fatalf("exportApplicationSource: %v", err)
	}
	man, _, err := parseSourceTree(files)
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	if len(man.Processes) != 1 || man.Processes[0].ID != "mine" {
		t.Errorf("processes = %+v, want only the application's own", man.Processes)
	}
	if len(man.Forms) != 0 || len(man.Decisions) != 0 {
		t.Errorf("another application's artifacts travelled: %+v / %+v", man.Forms, man.Decisions)
	}
}

// TestApplySourceTreeRefusals covers the four refusals, each of which means the
// tree and this server disagree about who owns something.
func TestApplySourceTreeRefusals(t *testing.T) {
	tree := func(key string, procs []sourceProcess, forms []sourceForm, decs []sourceDecision) (sourceManifest, map[string][]byte) {
		man := sourceManifest{FormatVersion: 1, Key: key, Name: "App", Processes: procs, Forms: forms, Decisions: decs}
		byPath := map[string][]byte{}
		for _, p := range procs {
			byPath[p.Path] = []byte("<x/>\n")
		}
		for _, f := range forms {
			byPath[f.Path] = []byte("{}\n")
		}
		return man, byPath
	}

	t.Run("caller may not write the application", func(t *testing.T) {
		s := storesFor(t)
		if err := s.projects.save(project{ID: "a1", Name: "App", Key: "app"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		man, byPath := tree("app", nil, nil, nil)
		_, err := s.applySourceTree(man, byPath, "", func(project) (int, string) {
			return http.StatusForbidden, "insufficient project access"
		})
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != http.StatusForbidden {
			t.Fatalf("err = %v, want a 403 refusal", err)
		}
		if refusal.Error() != "insufficient project access" {
			t.Errorf("message = %q", refusal.Error())
		}
	})

	t.Run("protected system project", func(t *testing.T) {
		s := storesFor(t)
		if err := s.projects.save(project{ID: "sys", Name: "Atlas System", Key: "atlas-system", Protected: true}); err != nil {
			t.Fatalf("save: %v", err)
		}
		man, byPath := tree("atlas-system", nil, nil, nil)
		_, err := s.applySourceTree(man, byPath, "", allow)
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != http.StatusForbidden {
			t.Fatalf("err = %v, want a 403 refusal", err)
		}
	})

	t.Run("form owned by another application", func(t *testing.T) {
		s := storesFor(t)
		if err := s.forms.save(form{ID: "f1", Name: "Fremd", ProjectID: "other", Schema: "{}"}); err != nil {
			t.Fatalf("save form: %v", err)
		}
		man, byPath := tree("app", nil, []sourceForm{{ID: "f1", Name: "F", Path: "forms/f.form.json"}}, nil)
		_, err := s.applySourceTree(man, byPath, "", allow)
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != http.StatusConflict {
			t.Fatalf("err = %v, want a 409 refusal", err)
		}
	})

	t.Run("decision owned by another application", func(t *testing.T) {
		s := storesFor(t)
		if err := s.dmnrefs.save(dmnRef{ID: "r1", Name: "Fremd", ModelRef: "m", ProjectID: "other"}); err != nil {
			t.Fatalf("save ref: %v", err)
		}
		man, byPath := tree("app", nil, nil, []sourceDecision{{ID: "r1", Name: "D", ModelRef: "m2"}})
		_, err := s.applySourceTree(man, byPath, "", allow)
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != http.StatusConflict {
			t.Fatalf("err = %v, want a 409 refusal", err)
		}
	})
}

// TestApplySourceTreeKeepsDecisionCreatedAt: re-importing must not reshuffle the
// Modeler's ordering, so an existing reference keeps its creation time.
func TestApplySourceTreeKeepsDecisionCreatedAt(t *testing.T) {
	s := storesFor(t)
	if err := s.projects.save(project{ID: "a1", Name: "App", Key: "app"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.dmnrefs.save(dmnRef{ID: "r1", Name: "Alt", ModelRef: "m", ProjectID: "a1", CreatedAt: 4242}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	man := sourceManifest{
		FormatVersion: 1, Key: "app", Name: "App",
		Decisions: []sourceDecision{{ID: "r1", Name: "Neu", ModelRef: "m2"}},
	}
	if _, err := s.applySourceTree(man, map[string][]byte{}, "", allow); err != nil {
		t.Fatalf("applySourceTree: %v", err)
	}
	got, ok, err := s.dmnrefs.get("r1")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if got.CreatedAt != 4242 {
		t.Errorf("createdAt = %d, want the original 4242", got.CreatedAt)
	}
	if got.Name != "Neu" || got.ModelRef != "m2" {
		t.Errorf("the reference was not updated: %+v", got)
	}
}

// TestApplySourceTreeReportsStoreFailure: a store that cannot be read is a plain
// error, not a refusal — the caller reports 500 rather than blaming the tree.
func TestApplySourceTreeReportsStoreFailure(t *testing.T) {
	s := storesFor(t)
	// A directory that does not exist and cannot be created under a file.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.projects = &projectStore{dir: filepath.Join(blocker, "projects")}
	man := sourceManifest{FormatVersion: 1, Key: "app", Name: "App"}
	_, err := s.applySourceTree(man, map[string][]byte{}, "", allow)
	if err == nil {
		t.Fatal("a broken project store was not reported")
	}
	var refusal sourceRefusal
	if errors.As(err, &refusal) {
		t.Errorf("a store failure was reported as a refusal: %v", err)
	}

	broken := storesFor(t)
	broken.drafts = &draftStore{dir: filepath.Join(blocker, "drafts")}
	if _, err := broken.exportApplicationSource(project{ID: "a", Name: "A", Key: "a"}); err == nil {
		t.Error("a broken draft store was not reported by the exporter")
	}
}

// TestApplySourceTreeReportsUntrackedOfEveryKind: a tree that omits a local form
// or decision reports it too, not only omitted processes — the operator has to be
// able to see everything the repository does not know about.
func TestApplySourceTreeReportsUntrackedOfEveryKind(t *testing.T) {
	s := storesFor(t)
	if err := s.projects.save(project{ID: "a1", Name: "App", Key: "app"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.drafts.save(draft{ProcessID: "p1", Name: "P", ProjectID: "a1", XML: "<x/>"}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := s.forms.save(form{ID: "f1", Name: "F", ProjectID: "a1", Schema: "{}"}); err != nil {
		t.Fatalf("save form: %v", err)
	}
	if err := s.dmnrefs.save(dmnRef{ID: "r1", Name: "R", ModelRef: "m", ProjectID: "a1"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	// An empty tree for the same application: everything local is untracked.
	man := sourceManifest{FormatVersion: 1, Key: "app", Name: "App"}
	res, err := s.applySourceTree(man, map[string][]byte{}, "", allow)
	if err != nil {
		t.Fatalf("applySourceTree: %v", err)
	}
	want := "decision:r1,form:f1,process:p1" // sorted
	if got := strings.Join(res.Untracked, ","); got != want {
		t.Errorf("untracked = %q, want %q", got, want)
	}
	// And none of them was removed.
	if _, ok, _ := s.forms.get("f1"); !ok {
		t.Error("the untracked form was deleted")
	}
	if _, ok, _ := s.dmnrefs.get("r1"); !ok {
		t.Error("the untracked decision was deleted")
	}
}

// TestApplySourceTreeReportsWriteFailure: a store that cannot be written is
// reported as an error, not swallowed into a success with a smaller count.
func TestApplySourceTreeReportsWriteFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name   string
		break_ func(*Server)
		man    sourceManifest
		files  map[string][]byte
	}{
		{
			name:   "draft store",
			break_: func(s *Server) { s.drafts = &draftStore{dir: filepath.Join(blocker, "drafts")} },
			man: sourceManifest{FormatVersion: 1, Key: "app", Name: "App",
				Processes: []sourceProcess{{ID: "p1", Name: "P", Path: "processes/p.bpmn"}}},
			files: map[string][]byte{"processes/p.bpmn": []byte("<x/>\n")},
		},
		{
			name:   "form store",
			break_: func(s *Server) { s.forms = &formStore{dir: filepath.Join(blocker, "forms")} },
			man: sourceManifest{FormatVersion: 1, Key: "app", Name: "App",
				Forms: []sourceForm{{ID: "f1", Name: "F", Path: "forms/f.form.json"}}},
			files: map[string][]byte{"forms/f.form.json": []byte("{}\n")},
		},
		{
			name:   "dmn reference store",
			break_: func(s *Server) { s.dmnrefs = &dmnRefStore{dir: filepath.Join(blocker, "dmnrefs")} },
			man: sourceManifest{FormatVersion: 1, Key: "app", Name: "App",
				Decisions: []sourceDecision{{ID: "r1", Name: "R", ModelRef: "m"}}},
			files: map[string][]byte{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := storesFor(t)
			if err := s.projects.save(project{ID: "a1", Name: "App", Key: "app"}); err != nil {
				t.Fatalf("save project: %v", err)
			}
			c.break_(s)
			if _, err := s.applySourceTree(c.man, c.files, "", allow); err == nil {
				t.Fatal("a failed write was reported as success")
			}
		})
	}
}

// TestApplySourceTreeReportsCreateFailure: the same for creating the application
// itself, which is the first write an import into a fresh server makes.
func TestApplySourceTreeReportsCreateFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := storesFor(t)
	// loadAll succeeds (the dir exists and is empty); the save then fails.
	dir := s.projects.dir
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
	s.projects = &projectStore{dir: dir}
	if _, err := s.applySourceTree(
		sourceManifest{FormatVersion: 1, Key: "app", Name: "App"}, map[string][]byte{}, "", allow); err == nil {
		t.Fatal("a failed application create was reported as success")
	}
}

// TestExportApplicationSourceReportsStoreFailures: each of the three artifact
// stores is a separate read, and each failure has to surface.
func TestExportApplicationSourceReportsStoreFailures(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	app := project{ID: "a1", Name: "App", Key: "app"}
	cases := map[string]func(*Server){
		"forms":    func(s *Server) { s.forms = &formStore{dir: filepath.Join(blocker, "forms")} },
		"dmnrefs":  func(s *Server) { s.dmnrefs = &dmnRefStore{dir: filepath.Join(blocker, "dmnrefs")} },
		"projects": func(s *Server) { s.projects = &projectStore{dir: filepath.Join(blocker, "projects")} },
	}
	for name, brk := range cases {
		t.Run(name, func(t *testing.T) {
			s := storesFor(t)
			brk(s)
			// The projects case needs an unkeyed application, so the exporter has to
			// derive a key and therefore read the project store at all.
			a := app
			if name == "projects" {
				a.Key = ""
			}
			if _, err := s.exportApplicationSource(a); err == nil {
				t.Fatalf("a broken %s store was not reported", name)
			}
		})
	}
}

// TestApplicationKeyForReportsSaveFailure: the backfill persists the key it
// derived, and a failure there must not return a key the store does not hold.
func TestApplicationKeyForReportsSaveFailure(t *testing.T) {
	s := storesFor(t)
	dir := s.projects.dir
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
	s.projects = &projectStore{dir: dir}
	if _, err := s.applicationKeyFor(project{ID: "a1", Name: "App"}); err == nil {
		t.Fatal("a failed key save was reported as success")
	}
}

// TestWriteSourceArchiveReportsWriterFailure: the export streams, so a client that
// disappears mid-download surfaces as an error rather than a silent truncation.
func TestWriteSourceArchiveReportsWriterFailure(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 64<<10)
	err := writeSourceArchive(failingWriter{after: 16}, []sourceFile{
		{Path: manifestPath, Data: big},
		{Path: "processes/p.bpmn", Data: big},
	})
	if err == nil {
		t.Fatal("a broken writer was not reported")
	}
}

// failingWriter accepts a few bytes and then fails, standing in for a client that
// hangs up mid-download.
type failingWriter struct{ after int }

func (w failingWriter) Write(p []byte) (int, error) {
	if len(p) > w.after {
		return 0, errors.New("connection reset")
	}
	return len(p), nil
}

// TestReadSourceArchiveSkipsAndRefuses covers the entries an archive may carry
// that are not files of the tree.
func TestReadSourceArchiveSkipsAndRefuses(t *testing.T) {
	// A directory entry is skipped, not an error: tar writers commonly emit them.
	var withDir bytes.Buffer
	gz := gzip.NewWriter(&withDir)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "processes/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestPath, Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("{}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatalf("close: %v", err)
	}
	files, err := readSourceArchive(bytes.NewReader(withDir.Bytes()))
	if err != nil {
		t.Fatalf("readSourceArchive: %v", err)
	}
	if len(files) != 1 || files[0].Path != manifestPath {
		t.Errorf("files = %v, want just the manifest", pathsOf(files))
	}

	// A path that climbs out of the root is refused outright.
	var escaping bytes.Buffer
	gz = gzip.NewWriter(&escaping)
	tw = tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.bpmn", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := readSourceArchive(bytes.NewReader(escaping.Bytes())); err == nil ||
		!strings.Contains(err.Error(), "illegal path") {
		t.Errorf("escaping path: err = %v, want an illegal-path refusal", err)
	}

	// More entries than the cap.
	var many bytes.Buffer
	gz = gzip.NewWriter(&many)
	tw = tar.NewWriter(gz)
	for i := 0; i <= maxSourceEntries; i++ {
		name := "processes/p" + strconv.Itoa(i) + ".bpmn"
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := readSourceArchive(bytes.NewReader(many.Bytes())); err == nil ||
		!strings.Contains(err.Error(), "too many entries") {
		t.Errorf("oversized archive: err = %v, want a too-many-entries refusal", err)
	}
}
