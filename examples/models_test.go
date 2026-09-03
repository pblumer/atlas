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
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/pblumer/atlas/compiler"
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

// TestShippedModelsCompile deploys every shipped model through the real compiler.
//
// The namespace test above catches a model the Modeler cannot read. This one catches
// the other half: a model *nothing* can run. Until now no test parsed the shipped
// BPMN at all, so a typo in an extension attribute — a filter that is not FEEL, an
// operation the worker does not have — shipped as a file that looks like an
// example and fails the moment someone deploys it.
//
// It walks the same tree as the namespace test, so the per-scenario subdirectories
// (bewerbermanagement/, pruefung/, reisebuchung/, …) are covered too — a top-level
// glob left every multi-file example, which is exactly where a model is most likely
// to reference something that is not there.
//
// The conformance suite's neg-* fixtures are excluded because failing to compile is
// exactly what they are for.
func TestShippedModelsCompile(t *testing.T) {
	for _, path := range shippedModels(t) {
		if strings.HasPrefix(filepath.Base(path), "neg-") {
			continue // a fixture whose whole purpose is to be rejected
		}
		t.Run(path, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()
			if _, err := compiler.Parse(1, 1, f); err != nil {
				t.Errorf("does not compile: %v", err)
			}
		})
	}
}

// atlasModdlePath is the Modeler's own descriptor for the atlas extensions. It is the
// authority on which namespace an <atlas:*> element must live in, because it is what
// bpmn-js resolves the prefix against.
const atlasModdlePath = "../api/web/atlas-moddle.json"

// atlasModdle reads the descriptor's namespace URI and the set of element names it
// declares, in the spelling they are serialized with (moddle's "lowerCase" tagAlias
// lowercases the first letter, so MockupConnector is written <atlas:mockupConnector>).
func atlasModdle(t *testing.T) (uri string, elements map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(atlasModdlePath)
	if err != nil {
		t.Fatalf("read %s: %v", atlasModdlePath, err)
	}
	var doc struct {
		URI   string `json:"uri"`
		Types []struct {
			Name string `json:"name"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", atlasModdlePath, err)
	}
	if strings.TrimSpace(doc.URI) == "" {
		t.Fatalf("%s declares no uri — the descriptor shape must have changed", atlasModdlePath)
	}
	elements = map[string]bool{}
	for _, ty := range doc.Types {
		if ty.Name == "" {
			continue
		}
		r := []rune(ty.Name)
		r[0] = unicode.ToLower(r[0])
		elements[string(r)] = true
	}
	return doc.URI, elements
}

// TestAtlasExtensionsUseTheModdleNamespace is the third namespace guard, and it closes
// the hole the first one leaves open.
//
// TestExtensionElementsAreNamespaced asks whether an extension is in the BPMN
// namespace — i.e. not prefixed at all. It says nothing about an extension prefixed to
// the *wrong* URI, and that is not a hypothetical: bonitaet-mockup.bpmn and both
// bewerbermanagement models shipped bound to "http://atlas.dev/schema/1.0" while
// atlas-moddle.json declares "http://atlas/schema/1.0".
//
// The consequence is worse than the silent drop the first test guards against.
// bpmn-js is namespace-aware: it parses such an element into an unknown namespace it
// has no prefix for, and *serializing* then throws outright —
// "no namespace uri given for prefix <ns0>". Importing appears to work, and then
// every Save, Deploy and Auto-layout of that model fails. Verified against the
// vendored bpmn-js by round-tripping all three before and after the fix.
//
// encoding/xml gives us the same view bpmn-js has, because the decoder resolves
// prefixes to URIs — which is precisely why the engine cannot see this:
// compiler.Parse matches on local name and ignores the namespace, so a wrongly bound
// model deploys and runs perfectly.
func TestAtlasExtensionsUseTheModdleNamespace(t *testing.T) {
	uri, elements := atlasModdle(t)
	for _, path := range shippedModels(t) {
		t.Run(path, func(t *testing.T) {
			for _, m := range misboundAtlasExtensions(t, path, uri, elements) {
				t.Errorf("<%s> is bound to %q, but the Modeler resolves atlas extensions against %q.\n"+
					"bpmn-js imports it into an unknown namespace and then fails to serialize it, so every "+
					"Save/Deploy/Auto-layout of this model errors. The engine cannot see this — it matches "+
					"on local name and ignores the namespace. Fix the xmlns:atlas binding.",
					m.local, m.space, uri)
			}
		})
	}
}

// misbound is one extension element written in the wrong namespace.
type misbound struct{ local, space string }

// misboundAtlasExtensions returns the <extensionElements> children whose local name is
// one the atlas moddle declares but whose namespace is not the moddle's.
//
// Keying on the moddle's own element names rather than on "anything not zeebe" is what
// keeps this from firing on a legitimate third-party extension a model may carry.
func misboundAtlasExtensions(t *testing.T, path, uri string, elements map[string]bool) []misbound {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var bad []misbound
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
				if elements[e.Name.Local] && e.Name.Space != uri && !seen[e.Name.Local] {
					seen[e.Name.Local] = true
					bad = append(bad, misbound{local: e.Name.Local, space: e.Name.Space})
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
