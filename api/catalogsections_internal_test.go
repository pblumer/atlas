package api

import (
	"strings"
	"testing"
)

// The catalogue page in sections (catalog-admin.js pageSection): one per step, in the
// order a catalogue is filled — products, descriptions, assemble, publish — then the
// catalogue's own settings. Each folds, folded by default, and the browser remembers
// which ones a maintainer keeps open. The fold itself is proven in the browser
// (e2e/catalog-sections.spec.mjs); what is held here is the order and the contract.

func TestTheCataloguePageIsInStepsInTheOrderACatalogueIsFilled(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	page := webRegion(t, src, "export async function viewCatalogDetail(", "\n}\n")
	order := []string{
		`pageSection("products", "1"`,
		`pageSection("content", "2"`,
		`pageSection("relations", "3"`,
		`pageSection("publish", "4"`,
		`pageSection("settings"`,
		`pageSection("appearance"`,
		`pageSection("sharing"`,
		`pageSection("transfer"`,
		`pageSection("purge"`,
	}
	at := -1
	for _, frag := range order {
		i := strings.Index(page, frag)
		if i < 0 {
			t.Fatalf("the page no longer draws the section %s", frag)
		}
		if i < at {
			t.Errorf("the section %s is drawn before the one it follows", frag)
		}
		at = i
	}
}

func TestACataloguePageSectionIsFoldedUntilItIsOpenedAndRemembered(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	sec := webRegion(t, src, "export function pageSection(", "\n}\n")
	if !strings.Contains(sec, `openPageSections()[key] === true`) {
		t.Error("a section is no longer folded unless this browser opened it")
	}
	if !strings.Contains(sec, `<aside class="cat-sec-about">`) || !strings.Contains(sec, `<div class="cat-sec-main">`) {
		t.Error("a section no longer keeps its explanation in a column of its own beside the controls")
	}
	wire := webRegion(t, src, "function wirePageSections(", "\n}\n")
	if !strings.Contains(wire, `"toggle"`) || !strings.Contains(wire, "rememberPageSection(") {
		t.Error("opening or folding a section is no longer remembered")
	}
	if !strings.Contains(src, `const PAGE_SECTIONS_KEY = "atlas.catalog.pageSections";`) {
		t.Error("the sections are remembered under another key, so every browser forgets what it had open")
	}
	css := readWeb(t, "app.css")
	if !strings.Contains(css, `grid-template-areas: "main about"`) {
		t.Error("on a wide screen the explanation is no longer beside the controls")
	}
}
