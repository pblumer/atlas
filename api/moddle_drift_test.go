package api

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// This file guards the Modeler's understanding of the connector extensions the
// engine executes. The two are wired through separate files that nothing forces to
// agree:
//
//   - compiler/parse.go decides which <atlas:*Connector> elements the engine reads.
//   - api/web/atlas-moddle.json decides which of them bpmn-js can parse and
//     re-serialize.
//   - api/web/editor.js (SERVICE_TASK_KINDS) decides which of them the properties
//     panel can display and edit.
//
// A connector missing from the moddle is not a cosmetic gap — it is silent data
// loss. bpmn-js drops an extension element it cannot parse, so merely opening a
// process in the Modeler and pressing Save (or Deploy, or Auto-layout) strips the
// connector's whole configuration and leaves an empty <extensionElements/>. That
// happened to the user-provisioning connector: it shipped with a compiler branch
// and a worker, but no moddle type, so the intake process's "Konto anlegen" task
// would lose its configuration on any Modeler round trip — and the panel showed it
// as an unconfigured job worker, because the extension was gone before the panel
// ever looked.

// connectorExtRe matches the connector extension elements compiler/parse.go reads,
// e.g. `xml:"extensionElements>userConnector"`.
var connectorExtRe = regexp.MustCompile(`xml:"extensionElements>([a-zA-Z]+Connector)"`)

// nonServiceTaskConnectors are connector extensions that are deliberately absent
// from the Modeler's service-task catalog, with the reason. They are still required
// to be in the moddle — the round-trip rule has no exceptions.
var nonServiceTaskConnectors = map[string]string{
	"temisConnector": "a business rule task's central-DMN binding, configured in the decision panel rather than the service-task connector picker (ADR-0050)",
}

// compilerConnectorTags returns the connector extension tags the compiler parses.
func compilerConnectorTags(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("../compiler/parse.go")
	if err != nil {
		t.Fatalf("read compiler/parse.go: %v", err)
	}
	seen := map[string]bool{}
	var tags []string
	for _, m := range connectorExtRe.FindAllStringSubmatch(string(src), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			tags = append(tags, m[1])
		}
	}
	if len(tags) == 0 {
		t.Fatal("found no connector extensions in compiler/parse.go; the pattern must have changed")
	}
	sort.Strings(tags)
	return tags
}

// moddleTypeFor maps an XML tag to its moddle type name. The atlas moddle declares
// `"xml": {"tagAlias": "lowerCase"}`, so a type is written with its first letter
// lowercased — UserConnector serializes as <atlas:userConnector>.
func moddleTypeFor(tag string) string {
	r := []rune(tag)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// serializedTagFor is the inverse: the XML tag bpmn-js writes for a moddle type.
// moddle's "lowerCase" tagAlias lowercases only the FIRST letter, so
// SharePointConnector becomes <atlas:sharePointConnector> — not all-lowercase. Go's
// encoding/xml matches element names case-sensitively, so a compiler tag that
// differs by even one letter silently ignores the extension.
func serializedTagFor(typeName string) string {
	r := []rune(typeName)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// moddleConnectorTypes returns the connector type names the atlas moddle declares.
func moddleConnectorTypes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var doc struct {
		Types []struct {
			Name string `json:"name"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}
	var out []string
	for _, ty := range doc.Types {
		if strings.HasSuffix(ty.Name, "Connector") {
			out = append(out, ty.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestCompilerReadsWhatTheModelerWrites is the sharper half of the round-trip rule:
// it is not enough that the moddle declares a connector — the tag bpmn-js
// *serializes* must be a tag the compiler *parses*, byte for byte.
//
// This caught a real defect: the moddle type SharePointConnector serializes as
// <atlas:sharePointConnector> (capital P, because the tagAlias lowercases only the
// first letter), while compiler/parse.go read only <atlas:sharepointConnector>.
// Every compiler test hand-authored the all-lowercase spelling, so nothing noticed
// that a SharePoint task built in the Modeler compiled as an unconfigured service
// task — its configuration present in the XML and never read.
func TestCompilerReadsWhatTheModelerWrites(t *testing.T) {
	parsed := map[string]bool{}
	for _, tag := range compilerConnectorTags(t) {
		parsed[tag] = true
	}
	var unread []string
	for _, typeName := range moddleConnectorTypes(t) {
		if tag := serializedTagFor(typeName); !parsed[tag] {
			unread = append(unread, typeName+" serializes as <atlas:"+tag+">, which compiler/parse.go does not read")
		}
	}
	if len(unread) > 0 {
		t.Fatalf("the Modeler writes %d connector tag(s) the compiler ignores:\n\t%s\n\n"+
			"Go's XML matching is case-sensitive, so such a task compiles as an unconfigured "+
			"service task with its configuration silently ignored. Make compiler/parse.go read the tag.",
			len(unread), strings.Join(unread, "\n\t"))
	}
}

// TestModdleDeclaresEveryCompilerConnector is the data-loss guard: every connector
// extension the engine executes must be declared in the Modeler's moddle, or
// opening and saving a process that uses it silently destroys its configuration.
func TestModdleDeclaresEveryCompilerConnector(t *testing.T) {
	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var doc struct {
		Types []struct {
			Name string `json:"name"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}
	// Match case-insensitively: this test asks whether the moddle knows the connector
	// at all (the data-loss question). Exact tag agreement is the separate, stricter
	// concern of TestCompilerReadsWhatTheModelerWrites, so a deliberate spelling
	// alias does not trip this one twice.
	declared := map[string]bool{}
	for _, ty := range doc.Types {
		declared[strings.ToLower(ty.Name)] = true
	}

	var missing []string
	for _, tag := range compilerConnectorTags(t) {
		if want := moddleTypeFor(tag); !declared[strings.ToLower(want)] {
			missing = append(missing, tag+" (needs moddle type \""+want+"\")")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("api/web/atlas-moddle.json does not declare %d connector extension(s) the compiler executes:\n\t%s\n\n"+
			"bpmn-js drops an extension it cannot parse, so opening such a process in the Modeler and saving it "+
			"silently strips the connector's configuration. Add the type to atlas-moddle.json.",
			len(missing), strings.Join(missing, "\n\t"))
	}
}

// TestModelerPanelKnowsEveryConnector guards the second half: a connector the
// Modeler can round-trip but not display shows up as an unconfigured job worker,
// which is how the user-provisioning connector looked before it was catalogued. A
// connector that is genuinely not a service-task kind belongs in
// nonServiceTaskConnectors with its reason.
func TestModelerPanelKnowsEveryConnector(t *testing.T) {
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	catalog := string(src)

	var missing []string
	for _, tag := range compilerConnectorTags(t) {
		if _, exempt := nonServiceTaskConnectors[tag]; exempt {
			continue
		}
		// SERVICE_TASK_KINDS entries reference the moddle type, e.g. ext: "atlas:UserConnector".
		// Match the catalog entry case-insensitively for the same reason as above:
		// the exact tag is TestCompilerReadsWhatTheModelerWrites's concern.
		if !strings.Contains(strings.ToLower(catalog), strings.ToLower(`"atlas:`+moddleTypeFor(tag)+`"`)) {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("api/web/editor.js (SERVICE_TASK_KINDS) has no entry for %d connector(s): %s\n\n"+
			"Without a catalog entry the properties panel falls back to \"Job worker\" and shows an empty Job type, "+
			"hiding a fully configured connector. Add a kind, or record an exemption in nonServiceTaskConnectors.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestNonServiceTaskConnectorsAreReal keeps the exemption list honest: an entry
// that no longer matches a compiler extension is stale and must be removed, so the
// list cannot silently excuse a connector that was renamed away.
func TestNonServiceTaskConnectorsAreReal(t *testing.T) {
	known := map[string]bool{}
	for _, tag := range compilerConnectorTags(t) {
		known[tag] = true
	}
	for tag, reason := range nonServiceTaskConnectors {
		if !known[tag] {
			t.Errorf("nonServiceTaskConnectors lists %q (%s), which the compiler no longer parses", tag, reason)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("nonServiceTaskConnectors[%q] has no reason recorded", tag)
		}
	}
}

// adAttrRe matches the attributes xmlAdConnector parses, e.g. `xml:"baseDN,attr"`.
var adAttrRe = regexp.MustCompile(`xml:"([a-zA-Z]+),attr"`)

// TestModdleKnowsEveryADAttribute guards the same round trip one level down: a
// connector *type* the moddle declares but an *attribute* it does not is the same
// silent data loss, in a smaller box. bpmn-js drops an attribute it has no property
// for, so opening a task and pressing Save strips exactly that one setting — and the
// task keeps working, addressing whatever the missing attribute no longer says.
//
// It happened here: <atlas:adConnector connector="…">, the way a task names a
// Console-configured directory (ADR-0206), was parsed by the compiler and declared
// nowhere in the moddle. The check is scoped to this one extension rather than every
// connector because that is what this change touched; widening it is worth doing and
// is not a side effect of adding an operation.
func TestModdleKnowsEveryADAttribute(t *testing.T) {
	src, err := os.ReadFile("../compiler/parse.go")
	if err != nil {
		t.Fatalf("read compiler/parse.go: %v", err)
	}
	// The struct's own body, so the pattern reads this extension's attributes only.
	body := string(src)
	start := strings.Index(body, "type xmlAdConnector struct {")
	if start < 0 {
		t.Fatal("xmlAdConnector is no longer declared in compiler/parse.go")
	}
	end := strings.Index(body[start:], "\n}")
	if end < 0 {
		t.Fatal("xmlAdConnector's declaration does not end")
	}
	var want []string
	for _, m := range adAttrRe.FindAllStringSubmatch(body[start:start+end], -1) {
		want = append(want, m[1])
	}
	if len(want) == 0 {
		t.Fatal("found no attributes on xmlAdConnector; the pattern must have changed")
	}

	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var moddle struct {
		Types []struct {
			Name       string `json:"name"`
			Properties []struct {
				Name string `json:"name"`
			} `json:"properties"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &moddle); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}
	declared := map[string]bool{}
	var found bool
	for _, ty := range moddle.Types {
		if ty.Name != "AdConnector" {
			continue
		}
		found = true
		for _, p := range ty.Properties {
			declared[p.Name] = true
		}
	}
	if !found {
		t.Fatal("atlas-moddle.json declares no AdConnector type")
	}
	var missing []string
	for _, attr := range want {
		if !declared[attr] {
			missing = append(missing, attr)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("atlas-moddle.json's AdConnector is missing %d attribute(s) the compiler reads: %s\n\n"+
			"bpmn-js drops an attribute it has no property for, so a Modeler round trip silently strips it.",
			len(missing), strings.Join(missing, ", "))
	}
}
