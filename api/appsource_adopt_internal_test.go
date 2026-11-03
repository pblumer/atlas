package api

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/pblumer/atlas/api/importplan"
)

// adoptFixture is a server whose application "holder" holds process p1, and a tree
// for application "taker" that carries p1 with the given XML.
func adoptFixture(t *testing.T, held, carried string, holder project) (*Server, sourceManifest, map[string][]byte) {
	t.Helper()
	s := storesFor(t)
	if err := s.projects.Save(holder); err != nil {
		t.Fatal(err)
	}
	if err := s.drafts.Save(draft{ProcessID: "p1", Name: "P", ProjectID: holder.ID, XML: held}); err != nil {
		t.Fatal(err)
	}
	man := sourceManifest{FormatVersion: 1, Key: "taker", Name: "Taker",
		Processes: []sourceProcess{{ID: "p1", Name: "P", Path: "p.bpmn"}}}
	return s, man, map[string][]byte{"p.bpmn": []byte(carried)}
}

func decisions(t *testing.T, query string) importplan.Decisions {
	t.Helper()
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	d, err := importplan.Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func artifactOf(res sourceImportResult, ref string) importplan.Artifact {
	for _, a := range res.Artifacts {
		if a.Ref == ref {
			return a
		}
	}
	return importplan.Artifact{}
}

func allowEverything(project) (int, string) { return 0, "" }

var holderApp = project{ID: "h1", Name: "Patrick's Stuff", Key: "patricks-stuff"}

// Another application's process is planned, not refused: elsewhere, with the
// application that holds it (for a caller who may see it) and how its content
// compares — and nothing moves until the caller adopts it.
func TestAnImportPlansAnotherApplicationsArtifactAsElsewhere(t *testing.T) {
	s, man, byPath := adoptFixture(t, "<x/>\n", "<x/>\n", holderApp)
	sees := func(project) bool { return true }
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything, sees: sees}, decisions(t, "dryRun=true"))
	if err != nil {
		t.Fatal(err)
	}
	a := artifactOf(res, "process:p1")
	if a.Status != importplan.Elsewhere || a.Outcome != importplan.Undecided || a.Compared != importplan.Unchanged ||
		a.HeldBy == nil || a.HeldBy.Name != "Patrick's Stuff" {
		t.Fatalf("plan = %+v", a)
	}

	// A caller who may not see the holder learns that something holds it, not what.
	res, _ = s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "dryRun=true"))
	if a := artifactOf(res, "process:p1"); a.Status != importplan.Elsewhere || a.HeldBy != nil {
		t.Fatalf("plan for a caller who cannot see the holder = %+v", a)
	}

	_, err = s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, ""))
	var undecided sourceUndecided
	if !errors.As(err, &undecided) {
		t.Fatalf("import without adopt = %v, want undecided", err)
	}
	if d, _, _ := s.drafts.Get("p1"); d.ProjectID != "h1" {
		t.Fatalf("an unadopted process moved to %q", d.ProjectID)
	}
}

// Adopted with the same content, the process moves into the importing application.
func TestAnAdoptedArtifactMovesIntoTheImportingApplication(t *testing.T) {
	s, man, byPath := adoptFixture(t, "<x/>\n", "<x/>\n", holderApp)
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt=process:p1"))
	if err != nil {
		t.Fatal(err)
	}
	if a := artifactOf(res, "process:p1"); !a.Adopted || a.Status != importplan.Unchanged {
		t.Fatalf("plan = %+v", a)
	}
	if d, _, _ := s.drafts.Get("p1"); d.ProjectID != res.ApplicationID || d.ProjectID == "h1" {
		t.Fatalf("adopted process is in %q, want %q", d.ProjectID, res.ApplicationID)
	}
}

// Adopted with other content, the content is a separate decision: undecided without
// one, kept moves it with this server's content, overwrite takes the tree's.
func TestAnAdoptedArtifactWithOtherContentStillAsksAboutTheContent(t *testing.T) {
	s, man, byPath := adoptFixture(t, "<local/>\n", "<tree/>\n", holderApp)
	_, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt=process:p1"))
	var undecided sourceUndecided
	if !errors.As(err, &undecided) {
		t.Fatalf("adopt of changed content without a content decision = %v", err)
	}

	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt=process:p1&keep=process:p1"))
	if err != nil {
		t.Fatal(err)
	}
	d, _, _ := s.drafts.Get("p1")
	if d.ProjectID != res.ApplicationID || d.XML != "<local/>\n" {
		t.Fatalf("adopt+keep = %+v, want the local content in the new application", d)
	}

	s, man, byPath = adoptFixture(t, "<local/>\n", "<tree/>\n", holderApp)
	res, err = s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt=process:p1&overwrite=process:p1"))
	if err != nil {
		t.Fatal(err)
	}
	if d, _, _ := s.drafts.Get("p1"); d.ProjectID != res.ApplicationID || d.XML != "<tree/>\n" {
		t.Fatalf("adopt+overwrite = %+v", d)
	}
}

// Adopting needs the editor role on the application the artifact leaves, and a
// protected system application gives nothing up.
func TestAdoptingNeedsTheHoldersEditorRoleAndNeverTakesFromTheSystem(t *testing.T) {
	s, man, byPath := adoptFixture(t, "<x/>\n", "<x/>\n", holderApp)
	onlyTaker := func(p project) (int, string) {
		if p.ID == "h1" {
			return http.StatusNotFound, "no such project"
		}
		return 0, ""
	}
	_, err := s.applySourceTree(man, byPath, sourceCaller{authorize: onlyTaker}, decisions(t, "adopt=process:p1"))
	var refusal sourceRefusal
	if !errors.As(err, &refusal) || refusal.status != http.StatusNotFound {
		t.Fatalf("adopt without a role on the holder = %v", err)
	}
	if d, _, _ := s.drafts.Get("p1"); d.ProjectID != "h1" {
		t.Fatalf("refused adopt moved the process to %q", d.ProjectID)
	}

	system := project{ID: "system", Name: "Atlas System", Key: "atlas-system", Protected: true}
	s, man, byPath = adoptFixture(t, "<x/>\n", "<x/>\n", system)
	_, err = s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt=process:p1"))
	if !errors.As(err, &refusal) || refusal.status != http.StatusForbidden {
		t.Fatalf("adopt from the system application = %v", err)
	}
}

func TestAdoptIsNamedArtifactByArtifact(t *testing.T) {
	if _, err := importplan.Parse(url.Values{"adopt": {"*"}}); err == nil {
		t.Fatal("adopt=* was accepted")
	}
	d, err := importplan.Parse(url.Values{"adopt": {"process:a,form:b"}})
	if err != nil || !d.Adopt["process:a"] || !d.Adopt["form:b"] {
		t.Fatalf("adopt = %+v, %v", d.Adopt, err)
	}
}

// A form and a decision move like a process: adopted and kept, each joins the
// importing application with this server's content; from a protected application
// neither is given up.
func TestAdoptMovesFormsAndDecisionsAndRefusesTheSystemsToo(t *testing.T) {
	setup := func(t *testing.T, holder project) (*Server, sourceManifest, map[string][]byte) {
		t.Helper()
		s := storesFor(t)
		if err := s.projects.Save(holder); err != nil {
			t.Fatal(err)
		}
		if err := s.forms.Save(form{ID: "f1", Name: "F", ProjectID: holder.ID, Schema: `{"local":true}`}); err != nil {
			t.Fatal(err)
		}
		if err := s.dmnrefs.Save(dmnRef{ID: "d1", Name: "Here", ModelRef: "m1", ProjectID: holder.ID}); err != nil {
			t.Fatal(err)
		}
		man := sourceManifest{FormatVersion: 1, Key: "taker", Name: "Taker",
			Forms:     []sourceForm{{ID: "f1", Name: "F", Path: "f.json"}},
			Decisions: []sourceDecision{{ID: "d1", Name: "There", ModelRef: "m1"}}}
		return s, man, map[string][]byte{"f.json": []byte(`{"tree":true}`)}
	}

	s, man, byPath := setup(t, holderApp)
	res, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything},
		decisions(t, "adopt=form:f1,decision:d1&keep=form:f1,decision:d1"))
	if err != nil {
		t.Fatal(err)
	}
	if f, _, _ := s.forms.Get("f1"); f.ProjectID != res.ApplicationID || f.Schema != `{"local":true}` {
		t.Fatalf("adopted, kept form = %+v", f)
	}
	if d, _, _ := s.dmnrefs.Get("d1"); d.ProjectID != res.ApplicationID || d.Name != "Here" {
		t.Fatalf("adopted, kept decision = %+v", d)
	}

	system := project{ID: "system", Name: "Atlas System", Key: "atlas-system", Protected: true}
	for _, ref := range []string{"form:f1", "decision:d1"} {
		s, man, byPath := setup(t, system)
		_, err := s.applySourceTree(man, byPath, sourceCaller{authorize: allowEverything}, decisions(t, "adopt="+ref))
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != http.StatusForbidden {
			t.Fatalf("adopt %s from the system application = %v", ref, err)
		}
	}
}
