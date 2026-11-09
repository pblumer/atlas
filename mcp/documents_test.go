package mcp

import (
	"strings"
	"testing"
)

func TestDocumentText(t *testing.T) {
	if got := documentText("doc_a", "text/csv; charset=utf-8", []byte("a,b\n1,2\n"), 100); !strings.Contains(got, "a,b\n1,2") {
		t.Errorf("whole text = %q", got)
	}
	got := documentText("doc_a", "text/plain", []byte("äöü"), 3)
	if !strings.Contains(got, "the first 2 bytes") || strings.Contains(got, "ö") {
		t.Errorf("a cut inside a character = %q", got)
	}
	if got := documentText("doc_b", "application/pdf", []byte("%PDF"), 100); strings.Contains(got, "%PDF") || !strings.Contains(got, "not text") {
		t.Errorf("binary = %q", got)
	}
}
