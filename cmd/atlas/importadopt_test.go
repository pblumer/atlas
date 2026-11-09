package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/importplan"
)

const heldBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d" targetNamespace="http://atlas/test">
  <process id="p_held" name="Gehalten" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <userTask id="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>
`

// `atlas import --adopt` takes an artifact another application holds
// (ADR-0467 amendment 2026-11-03).

// heldElsewhere is a server where application "halter" holds process p_held, and a
// package of application "nehmer" that carries the same process with the given
// comment appended.
func heldElsewhere(t *testing.T, tail string) (*httptest.Server, string, []string) {
	t.Helper()
	ts, _ := liveServer(t)
	model := heldBPMN
	pkg := func(key, xml string) string {
		dir := t.TempDir()
		writeFile(t, dir, packageManifest, `{"formatVersion":1,"key":"`+key+`","name":"`+key+`",`+
			`"processes":[{"id":"p_held","name":"Gehalten","path":"p.bpmn"}],"forms":[],"decisions":[]}`)
		writeFile(t, dir, "p.bpmn", xml)
		return dir
	}
	args := []string{"--server", ts.URL}
	if err := runImport(append(args, pkg("halter", model)), &bytes.Buffer{}); err != nil {
		t.Fatalf("install the holder: %v", err)
	}
	return ts, pkg("nehmer", model+tail), args
}

// holds reports whether the application of that key lists process p_held.
func holds(t *testing.T, ts *httptest.Server, key string) bool {
	t.Helper()
	code, raw := apiCall(t, ts, http.MethodGet, "/api/v1/drafts?projectId="+applicationID(t, ts, key), "")
	if code != http.StatusOK {
		t.Fatalf("drafts = %d %s", code, raw)
	}
	return strings.Contains(string(raw), "p_held")
}

func TestImportTakesWhatAnotherApplicationHoldsOnlyWhenAdopted(t *testing.T) {
	ts, pkg, args := heldElsewhere(t, "")

	var out bytes.Buffer
	if err := runImport(append(args, "--dry-run", pkg), &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{"1 held by another application",
		`elsewhere process:p_held "Gehalten", held by application "halter" — not taken; --adopt process:p_held`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the dry run lacks %q:\n%s", want, out.String())
		}
	}

	err := runImport(append(args, pkg), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "belong to another application and were not taken") ||
		!strings.Contains(err.Error(), "--adopt REF") {
		t.Fatalf("an import without --adopt = %v", err)
	}
	if !holds(t, ts, "halter") {
		t.Fatal("a refused import moved the process")
	}
	if err := runImport(append(args, "--adopt", "all", pkg), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "all is not accepted") {
		t.Fatalf("--adopt all = %v", err)
	}

	out.Reset()
	if err := runImport(append(args, "--adopt", "process:p_held", pkg), &out); err != nil {
		t.Fatalf("adopting import: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `application "nehmer" (nehmer) created`) {
		t.Errorf("output:\n%s", out.String())
	}
	if holds(t, ts, "halter") || !holds(t, ts, "nehmer") {
		t.Fatal("the adopted process did not move into the importing application")
	}
}

// At a terminal, the move is asked first, and a changed content after it.
func TestImportAsksToAdoptAtATerminal(t *testing.T) {
	ts, pkg, args := heldElsewhere(t, "<!-- im Paket geändert -->\n")
	answering(t, "n\n")
	err := runImport(append(args, pkg), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `stays in application "halter"`) {
		t.Fatalf("a declined adopt = %v", err)
	}

	answering(t, "y\n\n")
	var out bytes.Buffer
	if err := runImport(append(args, pkg), &out); err != nil {
		t.Fatalf("adopt at a terminal: %v\n%s", err, out.String())
	}
	for _, want := range []string{`belongs to application "halter". Move it into this application? [y/N]`,
		"differs from the server's. Replace it with the package's? [y/N]", "kept as the server has them: process:p_held"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if !holds(t, ts, "nehmer") {
		t.Fatal("the process adopted at the terminal did not move")
	}
}

// A model other applications' decisions use is replaced only when --overwrite names
// it: --overwrite all leaves it undecided, and the plan says which decision changes
// with it (ADR-0467 amendment 2026-11-05).
func TestASharedModelIsNotReplacedByOverwriteAll(t *testing.T) {
	shared := importplan.Artifact{Ref: "model:noten", Name: "noten", Status: importplan.Changed,
		SharedWith: []importplan.Use{{Decision: "d9", Name: "Notenschluessel", Application: &importplan.Holder{ID: "x", Name: "Pruefung"}}}}
	other := importplan.Artifact{Ref: "form:f1", Status: importplan.Changed}
	plan := func() importPlan {
		return importPlan{artifacts: []planned{{Artifact: shared, detail: sharedDetail(shared)}, {Artifact: other}}}
	}

	p := plan()
	p.decide(importplan.Decisions{OverwriteAll: true})
	if p.artifacts[0].Outcome != importplan.Undecided || p.artifacts[1].Outcome != importplan.Updated {
		t.Fatalf("--overwrite all = %+v", p.artifacts)
	}
	if q := p.query(); strings.Contains(q, "model") {
		t.Fatalf("--overwrite all sent the shared model: %s", q)
	}
	if !strings.Contains(p.artifacts[0].detail, `decision "Notenschluessel" in application "Pruefung"`) {
		t.Errorf("detail = %q", p.artifacts[0].detail)
	}

	p = plan()
	p.decide(importplan.Decisions{Overwrite: map[string]bool{"model:noten": true}})
	if p.artifacts[0].Outcome != importplan.Updated || !strings.Contains(p.query(), "model%3Anoten") {
		t.Fatalf("--overwrite model:noten = %+v, %s", p.artifacts[0], p.query())
	}

	behind := shared
	behind.Status = importplan.Behind
	p = importPlan{artifacts: []planned{{Artifact: behind}}}
	p.decide(importplan.Decisions{})
	if p.artifacts[0].Outcome != importplan.Undecided {
		t.Fatalf("a behind shared model = %+v, want it asked", p.artifacts[0])
	}
}
