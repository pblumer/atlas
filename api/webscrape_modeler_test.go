package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

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
	for _, name := range []string{"format", "maxItems"} {
		if !properties[name] {
			t.Errorf("WebscrapeConnector moddle property %q is missing or is not an XML attribute", name)
		}
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	catalog := string(editor)
	start := strings.Index(catalog, `id: "webscrape"`)
	if start < 0 {
		t.Fatal("webscrape worker is missing from SERVICE_TASK_KINDS")
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
	} {
		if !strings.Contains(entry, fragment) {
			t.Errorf("webscrape modeler entry does not contain %q", fragment)
		}
	}
}
