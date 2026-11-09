package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/blueprintregistry"
)

// ADR-draft-blueprint-architecture-preview: an atlas hub release shows, before it is
// installed, the architecture sketch of its application as its package's process
// models draw it, and the models themselves. These tests pin what the sketch reads
// from a real package, that it claims nothing about an installation, that every read
// is bound to the previewed digest, and that nothing is written.

// sketchedHubBPMN carries one of each fact a package fixes: a message that starts an
// instance, a worker named in the model, a user task for a group, a data object and
// a call into a process the package does not carry.
const sketchedHubBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_auftrag" name="auftrag"/>
  <bpmn:process id="rechnung" name="Rechnung" isExecutable="true">
    <bpmn:startEvent id="Start_Auftrag"><bpmn:messageEventDefinition messageRef="Msg_auftrag"/></bpmn:startEvent>
    <bpmn:userTask id="Pruefen" name="Prüfen">
      <bpmn:extensionElements><zeebe:assignmentDefinition candidateGroups="buchhaltung"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:serviceTask id="Send_Rechnung">
      <bpmn:extensionElements><atlas:mailConnector connector="Postausgang" to="a@b.ch" subject="Rechnung" body="Anbei."/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:callActivity id="Call_Mahnung">
      <bpmn:extensionElements><zeebe:calledElement processId="mahnung" bindingType="latest"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start_Auftrag" targetRef="Pruefen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Pruefen" targetRef="Send_Rechnung"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Send_Rechnung" targetRef="Call_Mahnung"/>
    <bpmn:sequenceFlow id="f4" sourceRef="Call_Mahnung" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`

// sketchedHubSource offers one release whose package carries sketchedHubBPMN and,
// beside it, a process file that does not compile.
func sketchedHubSource(t *testing.T) *fakeBlueprintSource {
	t.Helper()
	var archive bytes.Buffer
	if err := writeSourceArchive(&archive, []sourceFile{
		{Path: "atlas.json", Data: []byte(`{"formatVersion":1,"key":"rechnungswesen","name":"Rechnungswesen","processes":[` +
			`{"id":"rechnung","name":"Rechnung","path":"processes/rechnung.bpmn"},` +
			`{"id":"kaputt","name":"Kaputt","path":"processes/kaputt.bpmn"}],"forms":[],"decisions":[]}`)},
		{Path: "processes/rechnung.bpmn", Data: []byte(sketchedHubBPMN)},
		{Path: "processes/kaputt.bpmn", Data: []byte(`<definitions>not a model`)},
	}); err != nil {
		t.Fatal(err)
	}
	payload := archive.Bytes()
	sum := sha256.Sum256(payload)
	release := blueprintregistry.Release{
		BlueprintID: "org.example.rechnung", PublisherID: "org.example", Version: "1.2.0",
		Title: "Rechnungswesen", Description: "Example", License: "Apache-2.0",
		Atlas: blueprintregistry.Compatibility{MinVersion: "0.1.0"},
		Artifact: blueprintregistry.Artifact{Path: "packages/rechnung.tar.gz", SizeBytes: int64(len(payload)),
			Digest: "sha256:" + hex.EncodeToString(sum[:])},
	}
	return &fakeBlueprintSource{
		index:   blueprintregistry.Index{SchemaVersion: 1, RegistryID: "org.example.catalog", Releases: []blueprintregistry.Release{release}},
		release: release, packageBytes: payload,
	}
}

func blueprintSketchRequest(s *Server, suffix, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/org.example.rechnung/releases/1.2.0/"+suffix+query, nil)
	req.SetPathValue("id", "org.example.rechnung")
	req.SetPathValue("version", "1.2.0")
	w := httptest.NewRecorder()
	switch suffix {
	case "architecture":
		s.handleBlueprintArchitecture(w, req)
	case "architecture.svg":
		s.handleBlueprintArchitectureSVG(w, req)
	case "diagrams":
		s.handleBlueprintDiagrams(w, req)
	}
	return w
}

func TestBlueprintArchitectureReadsThePackagesModels(t *testing.T) {
	f := sketchedHubSource(t)
	s := &Server{blueprints: f}
	w := blueprintSketchRequest(s, "architecture", "?layers=application,business,data&digest="+f.release.Artifact.Digest)
	if w.Code != http.StatusOK {
		t.Fatalf("architecture = %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		BlueprintID    string `json:"blueprintId"`
		Version        string `json:"version"`
		Digest         string `json:"digest"`
		ApplicationKey string `json:"applicationKey"`
		Application    string `json:"application"`
		Layers         struct {
			Recorded []string `json:"recorded"`
		} `json:"layers"`
		Architecture archsketch.Snapshot `json:"architecture"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	a := got.Architecture
	if got.BlueprintID != "org.example.rechnung" || got.Version != "1.2.0" || got.Digest != f.release.Artifact.Digest ||
		got.ApplicationKey != "rechnungswesen" || got.Application != "Rechnungswesen" {
		t.Fatalf("identity = %+v", got)
	}
	if !a.Packaged || a.Physical != nil || a.Strategy != nil || a.TakenAt != 0 {
		t.Errorf("a package preview must be marked packaged and carry no installation facts: %+v", a)
	}
	if len(a.Processes) != 2 || a.Processes[0].ProcessID != "kaputt" || !a.Processes[0].Unread ||
		a.Processes[1].ProcessID != "rechnung" || a.Processes[1].Name != "Rechnung" {
		t.Errorf("processes = %+v, want the broken file unread and the other read", a.Processes)
	}
	if len(a.EntryPoints) != 1 || a.EntryPoints[0].Name != "auftrag" || !a.EntryPoints[0].Starts {
		t.Errorf("entry points = %+v", a.EntryPoints)
	}
	deps := map[string]archsketch.Dependency{}
	for _, d := range a.Dependencies {
		deps[d.Kind+":"+d.Name] = d
	}
	if d, ok := deps["worker:Postausgang"]; !ok || d.WorkerType != "mail" {
		t.Errorf("dependencies = %+v, want the mail worker", a.Dependencies)
	}
	if d, ok := deps["call:mahnung"]; !ok || d.TargetApplicationID != "" || d.TargetApplication != "" {
		t.Errorf("a call out of the package names no application, none being read: %+v", d)
	}
	if a.Business == nil || len(a.Business.Roles) != 1 || a.Business.Roles[0].Name != "buchhaltung" {
		t.Errorf("business = %+v, want the candidate group", a.Business)
	}
	if a.Data == nil {
		t.Error("the data layer is recorded from the models")
	}
}

func TestBlueprintArchitectureSVGNamesThePackage(t *testing.T) {
	f := sketchedHubSource(t)
	s := &Server{blueprints: f}
	w := blueprintSketchRequest(s, "architecture.svg", "?digest="+f.release.Artifact.Digest)
	if w.Code != http.StatusOK {
		t.Fatalf("architecture.svg = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("content type = %q", ct)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("the sketch must be served inert, got CSP %q", csp)
	}
	text := w.Body.String()
	for _, want := range []string{"Rechnungswesen 1.2.0 (atlas hub)", "Preview of atlas hub package org.example.rechnung 1.2.0",
		"Message auftrag", "Postausgang", "not in this package"} {
		if !strings.Contains(text, want) {
			t.Errorf("SVG lacks %q", want)
		}
	}
	if strings.Contains(text, "Release v") {
		t.Error("a package preview names a release nobody published")
	}
}

func TestBlueprintDiagramsReturnTheModels(t *testing.T) {
	f := sketchedHubSource(t)
	s := &Server{blueprints: f}
	w := blueprintSketchRequest(s, "diagrams", "?digest="+f.release.Artifact.Digest)
	if w.Code != http.StatusOK {
		t.Fatalf("diagrams = %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Digest    string             `json:"digest"`
		Processes []blueprintDiagram `json:"processes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Digest != f.release.Artifact.Digest || len(got.Processes) != 2 ||
		got.Processes[0].ProcessID != "rechnung" || got.Processes[0].XML != sketchedHubBPMN ||
		got.Processes[1].ProcessID != "kaputt" {
		t.Fatalf("diagrams = %+v", got)
	}
}

// TestBlueprintSketchReadsAreBoundToThePreviewedDigest: a sketch or a model of bytes
// the reader did not preview describes a package nobody reviewed.
func TestBlueprintSketchReadsAreBoundToThePreviewedDigest(t *testing.T) {
	for _, suffix := range []string{"architecture", "architecture.svg", "diagrams"} {
		f := sketchedHubSource(t)
		s := &Server{blueprints: f}
		if w := blueprintSketchRequest(s, suffix, ""); w.Code != http.StatusPreconditionRequired || f.fetches != 0 {
			t.Errorf("%s without a digest = %d (%d fetches)", suffix, w.Code, f.fetches)
		}
		if w := blueprintSketchRequest(s, suffix, "?digest=sha256:"+strings.Repeat("0", 64)); w.Code != http.StatusPreconditionFailed {
			t.Errorf("%s with another digest = %d", suffix, w.Code)
		}
		f.packageBytes = append([]byte{}, f.packageBytes...)
		f.packageBytes[len(f.packageBytes)-1] ^= 0xff
		if w := blueprintSketchRequest(s, suffix, "?digest="+f.release.Artifact.Digest); w.Code != http.StatusBadGateway {
			t.Errorf("%s of tampered bytes = %d", suffix, w.Code)
		}
		if w := blueprintSketchRequest(&Server{}, suffix, "?digest="+f.release.Artifact.Digest); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without a registry = %d", suffix, w.Code)
		}
	}
}

func TestBlueprintArchitectureRefusesAnUnrecordedOrUnknownLayer(t *testing.T) {
	f := sketchedHubSource(t)
	s := &Server{blueprints: f}
	if w := blueprintSketchRequest(s, "architecture", "?layers=bogus&digest="+f.release.Artifact.Digest); w.Code != http.StatusBadRequest {
		t.Errorf("unknown layer = %d", w.Code)
	}
	w := blueprintSketchRequest(s, "architecture.svg", "?layers=technology&digest="+f.release.Artifact.Digest)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "records none of the layers") {
		t.Errorf("technology layer of a package = %d: %s", w.Code, w.Body.String())
	}
}
