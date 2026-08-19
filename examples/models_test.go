// Package examples carries no code — it exists so the shipped BPMN models can
// guard their own authoring conventions with a Go test that runs in the
// mandatory `go test ./...` sweep.
//
// The failure this catches is one the engine cannot see. `compiler.Parse` decodes
// with encoding/xml, which matches elements by **local name and ignores the
// namespace**: `<script expression="…"/>` inside `<extensionElements>` binds to
// the same field as a correctly written `<zeebe:script …>`, so an unprefixed
// model parses, deploys and runs perfectly. bpmn-js does not — it is
// namespace-aware, resolves the unprefixed element into the default BPMN
// namespace, finds no such type there, and **silently drops it**. The Modeler
// then imports the task with an empty `<extensionElements/>`, its Problems panel
// reports "script task has no expression", and exporting the model writes the
// expression out of existence.
//
// order-fulfillment.bpmn, pruefung.bpmn, reisebuchung.bpmn and
// pruefe-datensaetze.bpmn all shipped that way: runnable by the engine,
// unreadable by the Modeler that is supposed to author them. Prefixing is the
// whole fix, and it is invisible to every test that only deploys a model — hence
// this one, which reads the XML as the browser does.
package examples

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelDirs are the directories whose .bpmn files ship with Atlas and are opened
// in the Modeler: the examples, the conformance fixtures, and the onboarding kit.
var modelDirs = []string{".", "../conformance/models", "../postman"}

// bpmnNamespace is the BPMN 2.0 MODEL namespace. An extension element resolved
// into it is by definition not an extension — it is a BPMN type that does not
// exist, which is exactly the mistake this test exists to catch.
const bpmnNamespace = "http://www.omg.org/spec/BPMN/20100524/MODEL"

// TestExtensionElementsAreNamespaced walks every shipped model and asserts that
// each child of an <extensionElements> lives in a real extension namespace
// (zeebe: or atlas:), never in the BPMN namespace it would inherit from an
// unprefixed tag. Reading with xml.Decoder gives us the browser's view: the
// decoder resolves prefixes, so an unprefixed child reports the default xmlns.
func TestExtensionElementsAreNamespaced(t *testing.T) {
	for _, path := range shippedModels(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			for _, bad := range unnamespacedExtensions(t, path) {
				t.Errorf("<%s> inside <extensionElements> resolves to the BPMN namespace — "+
					"the engine accepts it (encoding/xml matches on local name) but bpmn-js "+
					"drops it on import, so the Modeler loses the property. Write it as "+
					"<zeebe:%s> or <atlas:%s> and declare the namespace on <definitions>.",
					bad, bad, bad)
			}
		})
	}
}

// shippedModels collects the .bpmn files under modelDirs, recursing so that the
// per-scenario subdirectories (pruefung/, reisebuchung/, …) are covered too.
func shippedModels(t *testing.T) []string {
	t.Helper()
	var models []string
	for _, dir := range modelDirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".bpmn") {
				models = append(models, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(models) == 0 {
		t.Fatal("no .bpmn models found — the directory list is wrong, not the models")
	}
	return models
}

// unnamespacedExtensions returns the local names of any <extensionElements>
// children that sit in the BPMN namespace. Depth tracking keeps it to direct and
// nested children of an extensionElements block (a <zeebe:ioMapping> holds
// <zeebe:input> children, which must be prefixed too) without inspecting the
// ordinary process elements around it.
func unnamespacedExtensions(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var bad []string
	seen := map[string]bool{}
	depth := 0 // >0 while inside an <extensionElements> block
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			if depth > 0 {
				if e.Name.Space == bpmnNamespace && !seen[e.Name.Local] {
					seen[e.Name.Local] = true
					bad = append(bad, e.Name.Local)
				}
				depth++
			} else if e.Name.Local == "extensionElements" {
				depth = 1
			}
		case xml.EndElement:
			if depth > 0 {
				depth--
			}
		}
	}
	return bad
}
