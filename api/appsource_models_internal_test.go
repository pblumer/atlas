package api

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/importplan"
	"github.com/pblumer/atlas/dmn"
)

// Decision models in the source tree (ADR-0467). A
// reference names a model by its handle, and the handle is this server's: a tree
// carrying only the reference installs on another server as a decision whose model
// is not there, and its publish fails. A model this server keeps in its own model
// folder therefore travels as a file beside the references that point at it. A
// model behind a remote temis service is temis's and stays a reference.

// sourceModelXML stands in for a model in tests that never compile it.
const sourceModelXML = `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="freigabe" name="Freigabe"/>`

func TestBuildSourceTreeCarriesLocalDecisionModels(t *testing.T) {
	app := project{ID: "a", Name: "App", Key: "app"}
	files, err := buildSourceTree(app, nil, nil,
		[]dmnRef{
			{ID: "r1", Name: "Freigabestufe", ModelRef: "freigabe"},
			{ID: "r2", Name: "Betragsgrenze", ModelRef: "freigabe"},
			{ID: "r3", Name: "Risiko", ModelRef: "temis://risk"},
		},
		map[string][]byte{"freigabe": []byte(sourceModelXML)}, nil)
	if err != nil {
		t.Fatalf("buildSourceTree: %v", err)
	}
	// One file per model, however many of its decisions the application references.
	if got := pathsOf(files); len(got) != 2 || got[1] != "decisions/freigabe.dmn" {
		t.Fatalf("paths = %v, want the manifest and one model file", got)
	}
	// Verbatim, without the newline the other files are given: the bytes are what a
	// re-import compares against the model already stored under the handle, and a
	// newline added on the way out would read as a change on the way back in.
	if string(files[1].Data) != sourceModelXML {
		t.Errorf("model content = %q", files[1].Data)
	}
	man, _, err := parseSourceTree(files)
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	if man.FormatVersion != sourceFormatVersionModels {
		t.Errorf("formatVersion = %d, want %d for a tree carrying a model", man.FormatVersion, sourceFormatVersionModels)
	}
	want := map[string]string{"r1": "decisions/freigabe.dmn", "r2": "decisions/freigabe.dmn", "r3": ""}
	for _, d := range man.Decisions {
		if d.Path != want[d.ID] {
			t.Errorf("decision %s path = %q, want %q", d.ID, d.Path, want[d.ID])
		}
	}
}

func TestParseSourceTreeDecisionModelRefusals(t *testing.T) {
	manifest := func(body string) sourceFile {
		return sourceFile{Path: manifestPath, Data: []byte(`{"formatVersion":2,"key":"a","name":"A","decisions":` + body + `}`)}
	}
	model := sourceFile{Path: "decisions/m.dmn", Data: []byte(sourceModelXML)}
	other := sourceFile{Path: "decisions/n.dmn", Data: []byte(sourceModelXML)}
	cases := []struct {
		name  string
		files []sourceFile
		want  string
	}{
		{"model file missing", []sourceFile{manifest(`[{"id":"d","modelRef":"m","path":"decisions/m.dmn"}]`)},
			`decision "d" names "decisions/m.dmn", which is not in the tree`},
		// Two files for one handle would leave the import to pick which one the
		// handle holds afterwards.
		{"one model in two files", []sourceFile{
			manifest(`[{"id":"d1","modelRef":"m","path":"decisions/m.dmn"},{"id":"d2","modelRef":"m","path":"decisions/n.dmn"}]`),
			model, other}, `decision model "m" is carried twice`},
		// The handle is the name the model is written under, so it must name one
		// file in the model folder and nothing outside it.
		{"handle climbs out", []sourceFile{manifest(`[{"id":"d","modelRef":"../m","path":"decisions/m.dmn"}]`), model},
			"not a model handle"},
		{"handle has a separator", []sourceFile{manifest(`[{"id":"d","modelRef":"a/m","path":"decisions/m.dmn"}]`), model},
			"not a model handle"},
		{"handle a drive could read", []sourceFile{manifest(`[{"id":"d","modelRef":"c:m","path":"decisions/m.dmn"}]`), model},
			"not a model handle"},
		{"handle empty", []sourceFile{manifest(`[{"id":"d","path":"decisions/m.dmn"}]`), model},
			"not a model handle"},
		{"newer format", []sourceFile{{Path: manifestPath, Data: []byte(`{"formatVersion":5,"key":"a","name":"A"}`)}},
			"unsupported format version 5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := parseSourceTree(c.files)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}

	// Two decisions of one model name one file, which is the shape an export writes.
	man, byPath, err := parseSourceTree([]sourceFile{
		manifest(`[{"id":"d1","modelRef":"m","path":"decisions/m.dmn"},{"id":"d2","modelRef":"m","path":"decisions/m.dmn"}]`),
		model})
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	if len(man.Decisions) != 2 || string(byPath["decisions/m.dmn"]) != sourceModelXML {
		t.Fatalf("manifest = %+v", man)
	}
}

// modelStoresFor is storesFor with a local model folder behind the resolver, the
// shape a server without ATLAS_DMN_RESOLVER_URL has.
func modelStoresFor(t *testing.T) (*Server, string) {
	t.Helper()
	s := storesFor(t)
	dir := filepath.Join(t.TempDir(), "dmn-models")
	s.dmnResolver = dmn.DirResolver{Dir: dir}
	return s, dir
}

func writeModelFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
}

func TestExportApplicationSourceReadsLocalModels(t *testing.T) {
	s, dir := modelStoresFor(t)
	app := project{ID: "a1", Name: "App", Key: "app"}
	if err := s.projects.Save(app); err != nil {
		t.Fatalf("save project: %v", err)
	}
	for _, rec := range []dmnRef{
		{ID: "r1", Name: "Freigabestufe", ModelRef: "freigabe", ProjectID: "a1"},
		// A model the folder holds as .xml, which the resolver reads too.
		{ID: "r2", Name: "Rabatt", ModelRef: "rabatt", ProjectID: "a1"},
		// Broken here already: it travels as the reference it is, and is broken the
		// same way on the server the tree goes to.
		{ID: "r3", Name: "Verloren", ModelRef: "verloren", ProjectID: "a1"},
	} {
		if err := s.dmnrefs.Save(rec); err != nil {
			t.Fatalf("save ref: %v", err)
		}
	}
	writeModelFile(t, dir, "freigabe.dmn", sourceModelXML)
	writeModelFile(t, dir, "rabatt.xml", "<rabatt/>")

	files, err := s.exportApplicationSource(app)
	if err != nil {
		t.Fatalf("exportApplicationSource: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Data)
	}
	if got["decisions/freigabe.dmn"] != sourceModelXML || got["decisions/rabatt.dmn"] != "<rabatt/>" {
		t.Errorf("model files = %v", pathsOf(files))
	}
	man, _, err := parseSourceTree(files)
	if err != nil {
		t.Fatalf("parseSourceTree: %v", err)
	}
	for _, d := range man.Decisions {
		if d.ID == "r3" && d.Path != "" {
			t.Errorf("a model the folder does not hold was given a file: %+v", d)
		}
	}
}

// TestExportApplicationSourceLeavesRemoteModelsReferenced: with a temis service
// behind the resolver the model is temis's, and the export neither copies it nor
// reaches out to the service to try.
func TestExportApplicationSourceLeavesRemoteModelsReferenced(t *testing.T) {
	s := storesFor(t)
	s.dmnResolver = dmn.ServiceResolver{BaseURL: "http://127.0.0.1:1"}
	app := project{ID: "a1", Name: "App", Key: "app"}
	if err := s.projects.Save(app); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.dmnrefs.Save(dmnRef{ID: "r1", Name: "Freigabe", ModelRef: "freigabe", ProjectID: "a1"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	files, err := s.exportApplicationSource(app)
	if err != nil {
		t.Fatalf("exportApplicationSource: %v", err)
	}
	if got := pathsOf(files); len(got) != 1 {
		t.Errorf("paths = %v, want the manifest alone", got)
	}
}

// TestExportApplicationSourceReportsUnreadableModel: a model folder that cannot be
// read is a failure, not a model that is absent — the tree would otherwise leave the
// model out without anybody being told.
func TestExportApplicationSourceReportsUnreadableModel(t *testing.T) {
	s, dir := modelStoresFor(t)
	app := project{ID: "a1", Name: "App", Key: "app"}
	if err := s.projects.Save(app); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.dmnrefs.Save(dmnRef{ID: "r1", Name: "Freigabe", ModelRef: "freigabe", ProjectID: "a1"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	// A directory where the model file should be reads as an I/O failure.
	if err := os.MkdirAll(filepath.Join(dir, "freigabe.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := s.exportApplicationSource(app); err == nil {
		t.Fatal("an unreadable model was left out silently")
	}
}

// modelTree is a tree whose decisions r1 and r2 live in one model, carried as a file.
func modelTree(content string) (sourceManifest, map[string][]byte) {
	man := sourceManifest{FormatVersion: sourceFormatVersion, Key: "app", Name: "App", Decisions: []sourceDecision{
		{ID: "r1", Name: "Freigabestufe", ModelRef: "freigabe", Path: "decisions/freigabe.dmn"},
		{ID: "r2", Name: "Betragsgrenze", ModelRef: "freigabe", Path: "decisions/freigabe.dmn"},
	}}
	return man, map[string][]byte{"decisions/freigabe.dmn": []byte(content)}
}

func TestApplySourceTreeWritesDecisionModels(t *testing.T) {
	s, dir := modelStoresFor(t)
	man, byPath := modelTree(sourceModelXML)
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{})
	if err != nil {
		t.Fatalf("applySourceTree: %v", err)
	}
	if res.Decisions != 2 || res.Models != 1 {
		t.Errorf("result = %+v, want two decisions and one model", res)
	}
	data, err := os.ReadFile(filepath.Join(dir, "freigabe.dmn"))
	if err != nil || string(data) != sourceModelXML {
		t.Fatalf("model on disk = %q, %v", data, err)
	}

	// Another application's decision on the same model is no obstacle while the
	// model stays as it is: importing the same tree again changes nothing.
	if err := s.dmnrefs.Save(dmnRef{ID: "theirs", Name: "Fremd", ModelRef: "freigabe", ProjectID: "other"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	if res, err = s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{}); err != nil {
		t.Fatalf("second applySourceTree: %v", err)
	}
	if res.Models != 1 {
		t.Errorf("an unchanged model was not counted: %+v", res)
	}
}

// TestASharedModelIsReplacedOnlyWhenNamed: a handle is resolved by every reference
// on the server. Replacing the model under one that a decision outside the
// application points at changes that decision too, so the plan names that decision
// and the model is written only when the caller names it under overwrite — never by
// * (ADR-0467 amendment 2026-11-05).
func TestASharedModelIsReplacedOnlyWhenNamed(t *testing.T) {
	for _, theirs := range []dmnRef{
		{ID: "theirs", Name: "Fremd", ModelRef: "freigabe", ProjectID: "other"},
		{ID: "loose", Name: "Ungrouped", ModelRef: "freigabe"},
	} {
		t.Run(theirs.ID, func(t *testing.T) {
			s, dir := modelStoresFor(t)
			if err := s.projects.Save(project{ID: "other", Name: "Andere", Key: "andere"}); err != nil {
				t.Fatal(err)
			}
			writeModelFile(t, dir, "freigabe.dmn", "<old/>")
			if err := s.dmnrefs.Save(theirs); err != nil {
				t.Fatalf("save ref: %v", err)
			}
			man, byPath := modelTree(sourceModelXML)
			sees := func(project) bool { return true }
			res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow, sees: sees}, importplan.Decisions{DryRun: true})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			got := res.Artifacts[len(res.Artifacts)-1]
			if got.Ref != "model:freigabe" || len(got.SharedWith) != 1 || got.SharedWith[0].Decision != theirs.ID {
				t.Fatalf("the model = %+v, want it shared with %s", got, theirs.ID)
			}
			if theirs.ProjectID != "" && (got.SharedWith[0].Application == nil || got.SharedWith[0].Application.Name != "Andere") {
				t.Errorf("the plan does not name the application the decision belongs to: %+v", got.SharedWith[0])
			}

			_, err = s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{OverwriteAll: true})
			var undecided sourceUndecided
			if !errors.As(err, &undecided) {
				t.Fatalf("overwrite=* = %v, want the shared model left undecided", err)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "freigabe.dmn")); string(data) != "<old/>" {
				t.Errorf("overwrite=* replaced a shared model: %q", data)
			}

			if _, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow},
				importplan.Decisions{Overwrite: map[string]bool{"model:freigabe": true}}); err != nil {
				t.Fatalf("named overwrite: %v", err)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "freigabe.dmn")); string(data) != sourceModelXML {
				t.Errorf("the named model was not replaced: %q", data)
			}
		})
	}
}

// Replacing a shared model needs the editor role on every application whose decision
// changes with it, and never touches the protected system application's.
func TestASharedModelNeedsTheOtherApplicationsEditorRole(t *testing.T) {
	for _, tc := range []struct {
		holder project
		status int
	}{
		{project{ID: "other", Name: "Andere", Key: "andere"}, http.StatusNotFound},
		{project{ID: "system", Name: "Atlas System", Key: "atlas-system", Protected: true}, http.StatusForbidden},
	} {
		s, dir := modelStoresFor(t)
		if err := s.projects.Save(tc.holder); err != nil {
			t.Fatal(err)
		}
		writeModelFile(t, dir, "freigabe.dmn", "<old/>")
		if err := s.dmnrefs.Save(dmnRef{ID: "theirs", Name: "Fremd", ModelRef: "freigabe", ProjectID: tc.holder.ID}); err != nil {
			t.Fatal(err)
		}
		notTheHolder := func(p project) (int, string) {
			if p.ID == tc.holder.ID {
				return http.StatusNotFound, "no such project"
			}
			return 0, ""
		}
		man, byPath := modelTree(sourceModelXML)
		_, err := s.applySourceTree(man, byPath, sourceCaller{authorize: notTheHolder},
			importplan.Decisions{Overwrite: map[string]bool{"model:freigabe": true}})
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != tc.status || !strings.Contains(refusal.msg, `decision model "freigabe"`) {
			t.Fatalf("%s: err = %v, want %d naming the model", tc.holder.ID, err, tc.status)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, "freigabe.dmn")); string(data) != "<old/>" {
			t.Errorf("%s: the model was replaced anyway: %q", tc.holder.ID, data)
		}
		if _, err := s.applySourceTree(man, byPath, sourceCaller{authorize: notTheHolder},
			importplan.Decisions{Keep: map[string]bool{"model:freigabe": true}}); err != nil {
			t.Errorf("%s: keeping the model: %v", tc.holder.ID, err)
		}
	}
}

// TestApplySourceTreeOverwritesItsOwnModel: a decision of the application itself
// that the tree no longer names still shares the model; the tree is the
// application's source, so the model follows it.
func TestApplySourceTreeOverwritesItsOwnModel(t *testing.T) {
	s, dir := modelStoresFor(t)
	if err := s.projects.Save(project{ID: "a1", Name: "App", Key: "app"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.dmnrefs.Save(dmnRef{ID: "mine", Name: "Alt", ModelRef: "freigabe", ProjectID: "a1"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	writeModelFile(t, dir, "freigabe.dmn", "<old/>")
	man, byPath := modelTree(sourceModelXML)
	if _, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{Overwrite: map[string]bool{"model:freigabe": true}}); err != nil {
		t.Fatalf("applySourceTree: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "freigabe.dmn")); string(data) != sourceModelXML {
		t.Errorf("model on disk = %q", data)
	}
}

// TestApplySourceTreeLeavesRemoteModelsAlone: a server whose models come from a
// temis service does not write models. The references go in, and the answer
// names the models it did not write, so nobody takes the import for complete.
func TestApplySourceTreeLeavesRemoteModelsAlone(t *testing.T) {
	s := storesFor(t)
	s.dmnResolver = dmn.ServiceResolver{BaseURL: "http://127.0.0.1:1"}
	man, byPath := modelTree(sourceModelXML)
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{})
	if err != nil {
		t.Fatalf("applySourceTree: %v", err)
	}
	if res.Decisions != 2 || res.Models != 0 || len(res.ModelsSkipped) != 1 || res.ModelsSkipped[0] != "freigabe" {
		t.Errorf("result = %+v, want the references in and the model named as skipped", res)
	}
}

// TestApplySourceTreeReportsModelFolderFailures: a model that cannot be read or
// written is a failure the caller reports, before any artifact of the tree is
// written.
func TestApplySourceTreeReportsModelFolderFailures(t *testing.T) {
	for name, blocked := range map[string]string{
		// A directory where the model is stored cannot be read as one.
		"unreadable": "freigabe.dmn",
		// A directory where the write stages its file cannot be written.
		"unwritable": "freigabe.dmn.tmp",
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := modelStoresFor(t)
			if err := os.MkdirAll(filepath.Join(dir, blocked), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			man, byPath := modelTree(sourceModelXML)
			_, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{})
			var refusal sourceRefusal
			if err == nil || errors.As(err, &refusal) {
				t.Fatalf("err = %v, want a plain failure", err)
			}
			if all, _ := s.dmnrefs.LoadAll(); len(all) != 0 {
				t.Errorf("references were written beside a model that was not: %+v", all)
			}
		})
	}
}

// TestCheckSourceModelsRefusesWhatDoesNotCompile: a model is compiled before it is
// written, once however many decisions name its file, and one that does not compile
// refuses the tree.
func TestCheckSourceModelsRefusesWhatDoesNotCompile(t *testing.T) {
	s := &Server{dmnValidator: dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})}
	man, byPath := modelTree("not a model")
	err := s.checkSourceModels(t.Context(), man, byPath)
	if err == nil || !strings.Contains(err.Error(), `decision model "freigabe"`) {
		t.Fatalf("err = %v, want a refusal naming the model", err)
	}
	if err := s.checkSourceModels(t.Context(), sourceManifest{}, nil); err != nil {
		t.Fatalf("a tree without models: %v", err)
	}
	if !bytes.Equal(byPath["decisions/freigabe.dmn"], []byte("not a model")) {
		t.Fatal("the check changed the tree")
	}
}

// TestABehindModelOthersUseIsAsked: a model the last import wrote and a newer tree
// brings is taken without a question — unless a decision outside the application
// resolves it: then it waits for a decision, and is replaced only when named.
func TestABehindModelOthersUseIsAsked(t *testing.T) {
	s, dir := modelStoresFor(t)
	man, byPath := modelTree("<v1/>")
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allow}, importplan.Decisions{})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	newer, newerPath := modelTree("<v2/>")
	if res, err = s.applySourceTree(newer, newerPath, sourceCaller{authorize: allow}, importplan.Decisions{DryRun: true}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := res.Artifacts[len(res.Artifacts)-1]; got.Ref != "model:freigabe" || got.Status != importplan.Behind || got.Outcome != importplan.Updated {
		t.Fatalf("the model = %+v, want behind and taken", got)
	}
	if err := s.dmnrefs.Save(dmnRef{ID: "theirs", Name: "Fremd", ModelRef: "freigabe", ProjectID: "other"}); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	res, err = s.applySourceTree(newer, newerPath, sourceCaller{authorize: allow}, importplan.Decisions{DryRun: true})
	if err != nil {
		t.Fatalf("plan with a shared model: %v", err)
	}
	if got := res.Artifacts[len(res.Artifacts)-1]; got.Status != importplan.Behind || got.Outcome != importplan.Undecided || len(got.SharedWith) != 1 {
		t.Fatalf("the shared model = %+v, want behind, undecided and shared", got)
	}
	_, err = s.applySourceTree(newer, newerPath, sourceCaller{authorize: allow}, importplan.Decisions{})
	var undecided sourceUndecided
	if !errors.As(err, &undecided) {
		t.Fatalf("err = %v, want the shared model undecided", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "freigabe.dmn")); string(data) != "<v1/>" {
		t.Errorf("the model was replaced anyway: %q", data)
	}
	// Keeping this server's model is the way through.
	if _, err := s.applySourceTree(newer, newerPath, sourceCaller{authorize: allow}, importplan.Decisions{Keep: map[string]bool{"model:freigabe": true}}); err != nil {
		t.Fatalf("keeping the model: %v", err)
	}
}
