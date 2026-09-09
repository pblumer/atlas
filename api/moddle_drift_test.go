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

// This file guards the Modeler's understanding of the worker extensions the
// engine executes. The two are wired through separate files that nothing forces to
// agree:
//
//   - compiler/parse.go decides which <atlas:*Worker> elements the engine reads.
//   - api/web/atlas-moddle.json decides which of them bpmn-js can parse and
//     re-serialize.
//   - api/web/editor.js (SERVICE_TASK_KINDS) decides which of them the properties
//     panel can display and edit.
//
// A worker missing from the moddle is not a cosmetic gap — it is silent data
// loss. bpmn-js drops an extension element it cannot parse, so merely opening a
// process in the Modeler and pressing Save (or Deploy, or Auto-layout) strips the
// worker's whole configuration and leaves an empty <extensionElements/>. That
// happened to the user-provisioning worker: it shipped with a compiler branch
// and a worker, but no moddle type, so the intake process's "Konto anlegen" task
// would lose its configuration on any Modeler round trip — and the panel showed it
// as an unconfigured job worker, because the extension was gone before the panel
// ever looked.

// connectorExtRe matches the worker extension elements compiler/parse.go reads,
// e.g. `xml:"extensionElements>userConnector"`.
var connectorExtRe = regexp.MustCompile(`xml:"extensionElements>([a-zA-Z]+Connector)"`)

// nonServiceTaskConnectors are worker extensions that are deliberately absent
// from the Modeler's service-task catalog, with the reason. They are still required
// to be in the moddle — the round-trip rule has no exceptions.
var nonServiceTaskConnectors = map[string]string{
	"temisConnector": "a business rule task's central-DMN binding, configured in the decision panel rather than the service-task worker picker (ADR-0050)",
}

// agentConnector used to be listed above, because ADR-0253 put it only on an ad-hoc
// container. ADR-0256 gave it a second host — a service task that asks a model once — so
// it is a service-task kind now and the catalog must carry it. The exemption came off
// rather than being reworded, which is the point of the check below: an excuse that has
// stopped being true is worse than none, because it reads like a decision.

// compilerConnectorTags returns the worker extension tags the compiler parses.
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
		t.Fatal("found no worker extensions in compiler/parse.go; the pattern must have changed")
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

// moddleConnectorTypes returns the worker type names the atlas moddle declares.
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
// it is not enough that the moddle declares a worker — the tag bpmn-js
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
		t.Fatalf("the Modeler writes %d worker tag(s) the compiler ignores:\n\t%s\n\n"+
			"Go's XML matching is case-sensitive, so such a task compiles as an unconfigured "+
			"service task with its configuration silently ignored. Make compiler/parse.go read the tag.",
			len(unread), strings.Join(unread, "\n\t"))
	}
}

// TestModdleDeclaresEveryCompilerConnector is the data-loss guard: every worker
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
	// Match case-insensitively: this test asks whether the moddle knows the worker
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
		t.Fatalf("api/web/atlas-moddle.json does not declare %d worker extension(s) the compiler executes:\n\t%s\n\n"+
			"bpmn-js drops an extension it cannot parse, so opening such a process in the Modeler and saving it "+
			"silently strips the worker's configuration. Add the type to atlas-moddle.json.",
			len(missing), strings.Join(missing, "\n\t"))
	}
}

// TestModelerPanelKnowsEveryConnector guards the second half: a worker the
// Modeler can round-trip but not display shows up as an unconfigured job worker,
// which is how the user-provisioning worker looked before it was catalogued. A
// worker that is genuinely not a service-task kind belongs in
// nonServiceTaskConnectors with its reason.
func TestModelerPanelKnowsEveryConnector(t *testing.T) {
	catalog := serviceTaskKindsSource(t)

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
		t.Fatalf("api/web/editor.js (SERVICE_TASK_KINDS) has no entry for %d worker(s): %s\n\n"+
			"Without a catalog entry the properties panel falls back to \"Job worker\" and shows an empty Job type, "+
			"hiding a fully configured worker. Add a kind, or record an exemption in nonServiceTaskConnectors.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestNonServiceTaskConnectorsAreReal keeps the exemption list honest: an entry
// that no longer matches a compiler extension is stale and must be removed, so the
// list cannot silently excuse a worker that was renamed away.
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

// TestNoConnectorIsExemptedAndCatalogued is the other half of keeping that list honest.
// The staleness check above catches an entry the compiler no longer parses; this one
// catches an entry the Modeler *does* now offer, which is how an exemption outlives the
// reason it was written for — and an exemption still standing is a claim that the picker
// is the wrong panel for that worker, which would then be false.
func TestNoConnectorIsExemptedAndCatalogued(t *testing.T) {
	catalog := strings.ToLower(serviceTaskKindsSource(t))
	for tag, reason := range nonServiceTaskConnectors {
		if strings.Contains(catalog, strings.ToLower(`"atlas:`+moddleTypeFor(tag)+`"`)) {
			t.Errorf("nonServiceTaskConnectors excuses %q (%s), but SERVICE_TASK_KINDS now offers it; drop the exemption", tag, reason)
		}
	}
}

// serviceTaskKindsSource returns the SERVICE_TASK_KINDS array's own source text.
//
// Reading the whole of editor.js would be wrong in both directions. A worker named
// anywhere in the file — in the decision panel, in a comment, in the agent container's
// own section — would look catalogued when the service-task picker has no entry for it,
// and an exemption for such a worker would look stale when it is exactly right. The
// question both callers ask is about the picker, so the text they ask it of is the
// picker's.
func serviceTaskKindsSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	const opening = "const SERVICE_TASK_KINDS = ["
	i := strings.Index(string(src), opening)
	if i < 0 {
		t.Fatalf("editor.js has no %s; the catalog must have been renamed", opening)
	}
	rest := string(src)[i+len(opening):]
	// The array's own closing bracket is the first one at column zero: every entry inside
	// is indented, so nothing nested can look like the end. What follows it varies (today
	// a .map), which is why the bracket rather than the statement's end is the anchor.
	j := strings.Index(rest, "\n]")
	if j < 0 {
		t.Fatal("SERVICE_TASK_KINDS is not closed by a bracket at column zero; the catalog's shape must have changed")
	}
	return rest[:j]
}

// connectorAttrRe matches the attributes one xml*Worker struct parses, e.g.
// `xml:"baseDN,attr"`.
var connectorAttrRe = regexp.MustCompile(`xml:"([a-zA-Z]+),attr"`)

// TestModdleKnowsEveryConnectorAttribute guards the same round trip one level down: a
// worker *type* the moddle declares but an *attribute* it does not is the same
// silent data loss, in a smaller box. bpmn-js drops an attribute it has no property
// for, so opening a task and pressing Save strips exactly that one setting — and the
// task keeps working, addressing whatever the missing attribute no longer says.
//
// It happened here: <atlas:adConnector connector="…">, the way a task names a
// Console-configured directory (ADR-0206), was parsed by the compiler and declared
// nowhere in the moddle.
//
// The list is the extensions whose attributes have since changed under this check
// rather than every worker — widening it the rest of the way is worth doing on its
// own. Jira joined it with the account search (ADR-0223), which
// added the `query` attribute: an operation that adds an attribute is exactly the change
// this guards, so covering it is the guard for that change and not a drive-by.
func TestModdleKnowsEveryConnectorAttribute(t *testing.T) {
	src, err := os.ReadFile("../compiler/parse.go")
	if err != nil {
		t.Fatalf("read compiler/parse.go: %v", err)
	}
	body := string(src)

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

	for _, tc := range []struct{ structName, moddleType string }{
		{"xmlAdConnector", "AdConnector"},
		{"xmlJiraConnector", "JiraConnector"},
	} {
		t.Run(tc.moddleType, func(t *testing.T) {
			// The struct's own body, so the pattern reads this extension's attributes only.
			start := strings.Index(body, "type "+tc.structName+" struct {")
			if start < 0 {
				t.Fatalf("%s is no longer declared in compiler/parse.go", tc.structName)
			}
			end := strings.Index(body[start:], "\n}")
			if end < 0 {
				t.Fatalf("%s's declaration does not end", tc.structName)
			}
			var want []string
			for _, m := range connectorAttrRe.FindAllStringSubmatch(body[start:start+end], -1) {
				want = append(want, m[1])
			}
			if len(want) == 0 {
				t.Fatalf("found no attributes on %s; the pattern must have changed", tc.structName)
			}

			declared := map[string]bool{}
			var found bool
			for _, ty := range moddle.Types {
				if ty.Name != tc.moddleType {
					continue
				}
				found = true
				for _, p := range ty.Properties {
					declared[p.Name] = true
				}
			}
			if !found {
				t.Fatalf("atlas-moddle.json declares no %s type", tc.moddleType)
			}
			var missing []string
			for _, attr := range want {
				if !declared[attr] {
					missing = append(missing, attr)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf("atlas-moddle.json's %s is missing %d attribute(s) the compiler reads: %s\n\n"+
					"bpmn-js drops an attribute it has no property for, so a Modeler round trip silently strips it.",
					tc.moddleType, len(missing), strings.Join(missing, ", "))
			}
		})
	}
}

// bpmnOwnProcessAttrs are the <bpmn:process> attributes the BPMN moddle itself
// declares, so bpmn-js round-trips them without anything from our package. Every
// other attribute compiler/parse.go reads off a process is ours and has to be
// declared in ProcessMeta.
var bpmnOwnProcessAttrs = map[string]string{
	"id":           "bpmn:BaseElement's own id",
	"name":         "bpmn:CallableElement's own name",
	"isExecutable": "bpmn:Process's own isExecutable",
}

// TestModdleKnowsEveryProcessAttribute asks a different question at the process root,
// and the difference is worth stating because it is not the data-loss one above. An
// attribute in a namespace the document declares survives a round trip either way:
// moddle keeps what it has no property for in $attrs and writes it back out. What it
// cannot do without a property is let anything *read or write* the setting — the
// properties panel reads rootBo.<attr> and writes it through updateProperties, and
// both of those go through moddle's properties, not $attrs. So an undeclared process
// attribute is not lost, it is unauthorable: the Modeler cannot show it, and a field
// offered for it would silently write nothing.
//
// That is exactly what happened to atlas:searchable (ADR-0244). It shipped as a
// compiler attribute with no moddle property, so declaring a searchable variable
// meant editing the XML by hand outside the Modeler.
func TestModdleKnowsEveryProcessAttribute(t *testing.T) {
	src, err := os.ReadFile("../compiler/parse.go")
	if err != nil {
		t.Fatalf("read compiler/parse.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "type xmlProcess struct {")
	if start < 0 {
		t.Fatal("xmlProcess is no longer declared in compiler/parse.go")
	}
	end := strings.Index(body[start:], "\n}")
	if end < 0 {
		t.Fatal("xmlProcess's declaration does not end")
	}
	var want []string
	for _, m := range connectorAttrRe.FindAllStringSubmatch(body[start:start+end], -1) {
		if _, own := bpmnOwnProcessAttrs[m[1]]; !own {
			want = append(want, m[1])
		}
	}
	if len(want) == 0 {
		t.Fatal("found no Atlas attributes on xmlProcess; the pattern must have changed")
	}

	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var moddle struct {
		Types []struct {
			Name       string   `json:"name"`
			Extends    []string `json:"extends"`
			Properties []struct {
				Name string `json:"name"`
			} `json:"properties"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &moddle); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}
	declared := map[string]bool{}
	for _, ty := range moddle.Types {
		for _, ext := range ty.Extends {
			if ext != "bpmn:Process" {
				continue
			}
			for _, p := range ty.Properties {
				declared[p.Name] = true
			}
		}
	}

	var missing []string
	for _, attr := range want {
		if !declared[attr] {
			missing = append(missing, attr)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("atlas-moddle.json declares no bpmn:Process property for %d attribute(s) the compiler reads: %s\n\n"+
			"bpmn-js drops an attribute it has no property for, so a Modeler round trip silently strips it — and "+
			"nothing in the properties panel can offer a setting the moddle does not carry.",
			len(missing), strings.Join(missing, ", "))
	}
}
