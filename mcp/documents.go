package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// documentText renders a document for an agent: a text document's content, cut at max
// bytes on a character boundary and saying so; for anything else, what it is and how big
// (ADR-0543).
func documentText(id, contentType string, body []byte, max int) string {
	if !textual(contentType) || !utf8.Valid(body) {
		return fmt.Sprintf("document %s is %s, %d bytes; it is not text, so its content is not returned", id, contentType, len(body))
	}
	if len(body) <= max {
		return fmt.Sprintf("document %s (%s, %d bytes):\n%s", id, contentType, len(body), body)
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return fmt.Sprintf("document %s (%s, %d bytes) — the first %d bytes; ask with a larger maxBytes for more:\n%s",
		id, contentType, len(body), cut, body[:cut])
}

// textual reports whether a content type is one an agent can read.
func textual(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	for _, p := range []string{"application/json", "application/xml", "application/csv", "application/x-ndjson", "image/svg+xml"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}
