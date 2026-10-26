package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// The content tools, driven as an agent maintaining a catalogue would, and the
// quarantine read back through MCP: the release an agent reads withholds what the
// shop withholds (ADR-0491, § 9).

func put(t *testing.T, ts string, method, path, body string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: %v %v", method, path, err, resp)
	}
	_ = resp.Body.Close()
}

func TestContentToolsAndTheQuarantineOverMCP(t *testing.T) {
	f := wikifake.New()
	defer f.Close()
	f.Put(wikifake.Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	f.Put(wikifake.Page{ID: "it_de", Key: "SVC-12", Space: "SVC", Parent: "it_root", Title: "VPN",
		Labels: []string{"fernzugriff"}, Body: []wikifake.Block{{Kind: "paragraph", Text: "Sicherer Zugang."}}})
	ts := newAtlasWith(t, api.WithContentTransport(f.TS.Client().Transport))
	put(t, ts.URL, "PUT", "/api/v1/secrets/wiki-token", `{"value":"`+wikifake.Token+`"}`)
	put(t, ts.URL, "POST", "/api/v1/content-sources", `{"id":"wiki","name":"Wiki","kind":"wikiwikibangbang","baseURL":"`+
		f.URL()+`","credentialsRef":"wiki-token"}`)

	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{"rank": 1, "languages": []any{"de"}})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)
	if text, isErr = callText(t, ts, "atlas_save_catalog_product", map[string]any{"id": "vpn", "homeCatalog": cat,
		"state": "active", "texts": map[string]any{"de": "VPN"}, "approval": map[string]any{"kind": "none"},
		"provisionProcess": "p", "deprovisionProcess": "d"}); isErr {
		t.Fatalf("save = %q", text)
	}
	if text, isErr = callText(t, ts, "atlas_update_catalog", map[string]any{"id": cat, "items": []any{"vpn"}}); isErr {
		t.Fatalf("offer = %q", text)
	}
	if text, _ = callText(t, ts, "atlas_catalog_product_content", map[string]any{"id": "vpn"}); !strings.Contains(text, `"id":"wiki"`) {
		t.Fatalf("status = %q", text)
	}
	if text, _ = callText(t, ts, "atlas_search_content_source", map[string]any{"id": "vpn", "source": "wiki", "q": "vpn"}); !strings.Contains(text, "SVC-12") {
		t.Fatalf("search = %q", text)
	}
	if text, _ = callText(t, ts, "atlas_preview_content_page", map[string]any{"id": "vpn", "source": "wiki", "page": "SVC-12"}); !strings.Contains(text, "Sicherer Zugang") {
		t.Fatalf("preview = %q", text)
	}
	if text, isErr = callText(t, ts, "atlas_link_catalog_product_content", map[string]any{"id": "vpn", "source": "wiki",
		"pages": map[string]any{"de": "SVC-12"}}); isErr {
		t.Fatalf("link = %q", text)
	}
	if text, isErr = callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat}); isErr {
		t.Fatalf("publish = %q", text)
	}
	if text, _ = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat}); !strings.Contains(text, "Sicherer Zugang") {
		t.Fatalf("release before = %q", text)
	}
	if text, isErr = callText(t, ts, "atlas_refresh_catalog_content", map[string]any{"id": cat}); isErr || !strings.Contains(text, "refreshed") {
		t.Fatalf("catalogue refresh = %q", text)
	}

	f.Edit("it_de", func(p *wikifake.Page) { p.Held = true })
	if text, isErr = callText(t, ts, "atlas_refresh_catalog_product_content", map[string]any{"id": "vpn"}); !isErr {
		t.Fatalf("a refresh of a held page passed: %q", text)
	}
	text, _ = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat})
	if strings.Contains(text, "Sicherer Zugang") || strings.Contains(text, "fernzugriff") {
		t.Fatalf("MCP reads quarantined content: %q", text)
	}
	for _, tool := range []string{"atlas_export_catalog"} {
		if text, _ = callText(t, ts, tool, map[string]any{"id": cat}); strings.Contains(text, "Sicherer Zugang") {
			t.Fatalf("%s carries quarantined content: %q", tool, text)
		}
	}
	if text, isErr = callText(t, ts, "atlas_unlink_catalog_product_content", map[string]any{"id": "vpn", "reason": "page held"}); isErr ||
		strings.Contains(text, "Sicherer Zugang") {
		t.Fatalf("unlink = %q", text)
	}
}
