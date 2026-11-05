package mcp_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The purge tools (ADR-0469): the preview answers on any server,
// the delete on every server not started with --catalogue-purge=false, and only
// with the id repeated.

func TestThePurgeToolsPreviewAndThenDelete(t *testing.T) {
	ts := newAtlas(t)
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

func TestThePurgeToolsAreRefusedWhereTheServerSwitchedItOff(t *testing.T) {
	ts := newAtlasWith(t, api.WithoutCataloguePurge())
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
	if !isErr || !strings.Contains(text, "--catalogue-purge=false") {
		t.Fatalf("delete = (%q, isErr=%v), want a refusal naming the switch", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_get_catalog", map[string]any{"id": cat}); isErr {
		t.Fatalf("the refused delete removed the catalogue: %q", text)
	}
}

// The prune tools (catalogpurge.go): the preview names the releases that would go,
// and the prune deletes them only with the id repeated, keeping the newest.
func TestThePruneToolsPreviewAndThenPrune(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Test"}, "rank": 1, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)
	if text, isErr := callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "state": "active", "texts": map[string]any{"de": "VPN"},
		"approval": map[string]any{"kind": "none"}, "provisionProcess": "p", "deprovisionProcess": "d",
	}); isErr {
		t.Fatalf("save = %q", text)
	}
	if text, isErr := callText(t, ts, "atlas_update_catalog", map[string]any{"id": cat, "items": []any{"vpn"}}); isErr {
		t.Fatalf("offer = %q", text)
	}
	for i := 0; i < 3; i++ {
		if text, isErr := callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat}); isErr {
			t.Fatalf("publish = %q", text)
		}
	}
	text, isErr = callText(t, ts, "atlas_release_pruning", map[string]any{"id": cat})
	if isErr || strings.Count(text, `"rel_`) < 3 || !strings.Contains(text, `"why":"newest"`) {
		t.Fatalf("preview = (%q, isErr=%v)", text, isErr)
	}
	if text, isErr = callText(t, ts, "atlas_prune_releases", map[string]any{"id": cat, "confirm": "other"}); !isErr {
		t.Fatalf("a prune confirming another id = %q, want refused", text)
	}
	if text, isErr = callText(t, ts, "atlas_prune_releases", map[string]any{"id": cat, "confirm": cat}); isErr || !strings.Contains(text, `"done":true`) {
		t.Fatalf("prune = (%q, isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat})
	if isErr || strings.Count(text, `"catalogId"`) != 1 {
		t.Fatalf("after the prune = (%q, isErr=%v), want one release", text, isErr)
	}
}
