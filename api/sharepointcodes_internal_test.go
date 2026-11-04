package api

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepoint"
)

// The handbook's error recipe names every code a SharePoint task can throw, in both
// languages, so a modeller drawing the boundary for set-site-quota finds what to catch.
func TestHandbookNamesEverySharePointErrorCode(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	for _, code := range sharepoint.Codes {
		if n := strings.Count(string(body), "<code>"+code+"</code>"); n < 2 {
			t.Errorf("handbuch.html names <code>%s</code> %d time(s); want it in the German and the English text", code, n)
		}
	}
}
