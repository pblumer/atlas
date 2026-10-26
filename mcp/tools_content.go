package mcp

import (
	"encoding/json"
	"net/url"
)

// The content binding of a product
// (ADR-draft-service-content-from-an-external-source): an agent maintaining a catalogue as a product manager (ADR-0376) reads
// what a product is bound to, searches and previews pages, links, refreshes and
// unlinks — the same routes and the same gate a person meets. Configuring a source
// is an administrator's and names a credential, so it has no tool.

func productContent(id, suffix string) string {
	return "/api/v1/catalog-products/" + url.PathEscape(id) + "/content" + suffix
}

func contentTools() []Tool {
	return markCatalogue([]Tool{
		{
			Name: "atlas_catalog_product_content",
			Description: "What a product's description, keywords and picture are bound to in an external " +
				"content source (a wiki): the configured sources to choose from, the binding (one page per " +
				"language, the primary one naming the picture), the recheck record — a quarantine with its " +
				"reason, when a recheck last passed — and whether the shop shows the content now. `sources` " +
				"empty means no source is configured here and products are written by hand as before.",
			InputSchema: map[string]any{"type": "object",
				"properties": map[string]any{"id": stringProp("The product id.")}, "required": []any{"id"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get(productContent(id, "")))
			},
		},
		{
			Name: "atlas_search_content_source",
			Description: "Search a content source for pages to bind a product to: id, key, title, space, state " +
				"and address of each hit. Nothing is written.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"id":     stringProp("The product the pages are for (the search is gated on maintaining it)."),
				"source": stringProp("The content source id, from atlas_catalog_product_content."),
				"q":      stringProp("What to search for."),
			}, "required": []any{"id", "source", "q"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				q := url.Values{"source": {optString(args, "source")}, "q": {optString(args, "q")}}
				return asText(c.get(productContent(id, "/search?"+q.Encode())))
			},
		},
		{
			Name: "atlas_preview_content_page",
			Description: "Read one page as linking would take it, without writing anything: its stable id, title, " +
				"state and version, the plain text it would supply (its body, then every readable child page " +
				"depth first), the keywords, every protected passage and child page the security rules drop, " +
				"every reason the page is refused, and `boundTo` when another product holds it. Preview before " +
				"atlas_link_catalog_product_content.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"id":     stringProp("The product the page is for."),
				"source": stringProp("The content source id."),
				"page":   stringProp("The page's address on the source's host, its key or its id."),
			}, "required": []any{"id", "source", "page"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				q := url.Values{"source": {optString(args, "source")}, "page": {optString(args, "page")}}
				return asText(c.get(productContent(id, "/preview?"+q.Encode())))
			},
		},
		{
			Name: "atlas_link_catalog_product_content",
			Description: "Bind a product to one page per language its home catalogue declares and write the " +
				"descriptions, keywords and picture fetched from them in one draft save. All or nothing: every " +
				"refusal is answered at once and nothing is written. While bound, those fields are read-only " +
				"through atlas_save_catalog_product. State `revision` to make it a safe read-modify-write.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"id":       stringProp("The product id."),
				"source":   stringProp("The content source id."),
				"primary":  stringProp("The language whose page names the picture; may be left out with one page."),
				"pages":    objectProp("One page per language tag: {\"de\": \"SVC-12\", \"fr\": \"https://wiki.example.org/#/item/SVC-13\"}."),
				"revision": integerProp("The product's revision you read; the link is refused with 409 when it moved."),
			}, "required": []any{"id", "source", "pages"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				body, err := json.Marshal(map[string]any{"source": args["source"], "primary": args["primary"],
					"pages": args["pages"], "revision": args["revision"]})
				if err != nil {
					return "", err
				}
				return asText(c.post(productContent(id, ""), "application/json", body))
			},
		},
		{
			Name: "atlas_refresh_catalog_product_content",
			Description: "Recheck a bound product, then fetch its pages again and write the result into the draft, " +
				"all or nothing. It is the only act that ends a quarantine, and publishing needs a passed " +
				"recheck within the last fifteen minutes.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"id":       stringProp("The product id."),
				"revision": integerProp("The product's revision you read."),
			}, "required": []any{"id"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				body, err := json.Marshal(map[string]any{"revision": args["revision"]})
				if err != nil {
					return "", err
				}
				return asText(c.post(productContent(id, "/refresh"), "application/json", body))
			},
		},
		{
			Name: "atlas_unlink_catalog_product_content",
			Description: "Remove one language page (`lang`) of a product's binding, or the whole binding (no " +
				"`lang`). `reason` is REQUIRED and recorded in the audit log: the product owner's page stops " +
				"supplying the shop. The fields become editable again with their last values, unless the " +
				"product is quarantined, when the content in question is dropped.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"id":       stringProp("The product id."),
				"lang":     stringProp("The one language page to remove; leave out to remove the binding."),
				"reason":   stringProp("Why the page no longer supplies the product."),
				"revision": integerProp("The product's revision you read."),
			}, "required": []any{"id", "reason"}},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				body, err := json.Marshal(map[string]any{"lang": args["lang"], "reason": args["reason"],
					"revision": args["revision"]})
				if err != nil {
					return "", err
				}
				return asText(c.post(productContent(id, "/unlink"), "application/json", body))
			},
		},
		{
			Name: "atlas_refresh_catalog_content",
			Description: "Refresh every bound product a catalogue offers, each all or nothing on its own; one " +
				"homed in a catalogue you do not maintain is skipped. Answers refreshed, refused (with the " +
				"problems) or skipped per product.",
			InputSchema: catalogIDArg("The catalogue whose bound products to refresh."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.post(withID(id, "/content/refresh"), "", nil))
			},
		},
	})
}
