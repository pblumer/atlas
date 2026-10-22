package api

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/entra"
)

// The handbook says which workers throw BPMN errors and under which codes, in the error
// recipe a modeller reads before drawing a boundary. An Entra code missing there is one
// nobody draws a boundary for, and the unknown person it stands for keeps ending in an
// incident. Each is named in both languages.
func TestHandbookNamesEveryEntraErrorCode(t *testing.T) {
	body, err := fs.ReadFile(webFS, "web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	for _, code := range entra.Codes {
		if n := strings.Count(string(body), "<code>"+code+"</code>"); n < 2 {
			t.Errorf("handbuch.html names <code>%s</code> %d time(s); want it in the German and the English text", code, n)
		}
	}
}
