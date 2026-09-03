package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// hasScrapeFieldType reports whether the moddle declares the child element a
// structured scrape's fields are written as.
func hasScrapeFieldType(types []struct {
	Name       string `json:"name"`
	Properties []struct {
		Name   string `json:"name"`
		IsAttr bool   `json:"isAttr"`
	} `json:"properties"`
}) bool {
	for _, typ := range types {
		if typ.Name == "ScrapeField" {
			return true
		}
	}
	return false
}

func TestWebScrapeModelerDeclaresFeedAuthoringFields(t *testing.T) {
	raw, err := os.ReadFile("web/atlas-moddle.json")
	if err != nil {
		t.Fatalf("read atlas-moddle.json: %v", err)
	}
	var doc struct {
		Types []struct {
			Name       string `json:"name"`
			Properties []struct {
				Name   string `json:"name"`
				IsAttr bool   `json:"isAttr"`
			} `json:"properties"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode atlas-moddle.json: %v", err)
	}

	properties := map[string]bool{}
	for _, typ := range doc.Types {
		if typ.Name != "WebscrapeConnector" {
			continue
		}
		for _, property := range typ.Properties {
			properties[property.Name] = property.IsAttr
		}
	}
	for _, name := range []string{"format", "maxItems", "absoluteLinks", "plainText"} {
		if !properties[name] {
			t.Errorf("WebscrapeConnector moddle property %q is missing or is not an XML attribute", name)
		}
	}
	// The field list is a child element, not an attribute — and without the
	// ScrapeField type bpmn-js drops the children on the first Save, which is silent
	// data loss rather than a cosmetic gap (see moddle_drift_test.go).
	if _, declared := properties["fields"]; !declared {
		t.Error("WebscrapeConnector has no fields property: scrapeField children would be dropped on a Modeler round trip")
	} else if properties["fields"] {
		t.Error("WebscrapeConnector fields must be a child element, not an XML attribute")
	}
	if !hasScrapeFieldType(doc.Types) {
		t.Error("the moddle declares no ScrapeField type, so <atlas:scrapeField> cannot round-trip")
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	catalog := string(editor)
	start := strings.Index(catalog, `id: "webscrape"`)
	if start < 0 {
		t.Fatal("webscrape connector is missing from SERVICE_TASK_KINDS")
	}
	endRel := strings.Index(catalog[start:], "\n  },\n].map(withRetries);")
	if endRel < 0 {
		t.Fatal("could not isolate the webscrape SERVICE_TASK_KINDS entry")
	}
	entry := catalog[start : start+endRel]

	for _, fragment := range []string{
		`key: "format"`,
		`type: "select"`,
		`{ v: "rss", l: "RSS" }`,
		`{ v: "atom", l: "Atom" }`,
		`key: "maxItems"`,
		`showIf: (v) => !v.format || v.format === "html"`,
		// The structured-HTML half: the panel must offer the field rows, the two
		// mode flags, and the child type the moddle declares
		// (ADR-draft-webscrape-structured-extraction).
		`key: "fields"`,
		`childType: "atlas:ScrapeField"`,
		`valueKey: "selector"`,
		`extraKey: "attribute"`,
		`key: "absoluteLinks"`,
		`key: "plainText"`,
	} {
		if !strings.Contains(entry, fragment) {
			t.Errorf("webscrape modeler entry does not contain %q", fragment)
		}
	}
}
