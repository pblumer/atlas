package mcp_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The purge tools (ADR-draft-catalogue-purge): the preview answers on any server,
// the delete only on one started to allow it, and only with the id repeated.

func TestThePurgeToolsPreviewAndThenDelete(t *testing.T) {
	ts := newAtlasWith(t, api.WithCataloguePurge())
	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Test"}, "rank": 1, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)
	for _, id := range []string{"vpn", "laptop"} {
		if text, isErr := callText(t, ts, "atlas_save_catalog_product", map[string]any{
			"id": id, "homeCatalog": cat, "state": "draft", "texts": map[string]any{"de": id},
		}); isErr {
			t.Fatalf("save %s = %q", id, text)
		}
	}

	text, isErr = callText(t, ts, "atlas_catalog_product_deletion", map[string]any{"id": "laptop"})
	if isErr || !strings.Contains(text, `"enabled":true`) || !strings.Contains(text, `"laptop"`) {
		t.Fatalf("product preview = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_delete_catalog_product",
		map[string]any{"id": "laptop", "confirm": "vpn"}); !isErr {
		t.Fatalf("a delete confirming another id = %q, want refused", text)
	}
	if text, isErr = callText(t, ts, "atlas_delete_catalog_product",
		map[string]any{"id": "laptop", "confirm": "laptop"}); isErr || !strings.Contains(text, `"done":true`) {
		t.Fatalf("delete the product = (%q, isErr=%v)", text, isErr)
	}

	text, isErr = callText(t, ts, "atlas_catalog_deletion", map[string]any{"id": cat})
	if isErr || !strings.Contains(text, `"vpn"`) || strings.Contains(text, `"laptop"`) {
		t.Fatalf("catalogue preview = (%q, isErr=%v), want vpn and no laptop", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_delete_catalog",
		map[string]any{"id": cat, "confirm": cat}); isErr || !strings.Contains(text, `"done":true`) {
		t.Fatalf("delete the catalogue = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_get_catalog", map[string]any{"id": cat}); !isErr {
		t.Fatalf("the deleted catalogue still reads: %q", text)
	}
}

func TestThePurgeToolsAreRefusedWhereTheServerDoesNotAllowIt(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Test"}, "rank": 1, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)

	text, isErr = callText(t, ts, "atlas_catalog_deletion", map[string]any{"id": cat})
	if isErr || !strings.Contains(text, `"enabled":false`) {
		t.Fatalf("preview = (%q, isErr=%v), want enabled false", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_delete_catalog", map[string]any{"id": cat, "confirm": cat})
	if !isErr || !strings.Contains(text, "--catalogue-purge") {
		t.Fatalf("delete = (%q, isErr=%v), want a refusal naming the switch", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_get_catalog", map[string]any{"id": cat}); isErr {
		t.Fatalf("the refused delete removed the catalogue: %q", text)
	}
}
