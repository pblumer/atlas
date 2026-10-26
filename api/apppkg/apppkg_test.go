package apppkg_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/apppkg"
)

// TestAnAnswerCannotBreakTheDocumentItIsPutIn: every answer goes in as an escaped
// JSON string, so quotes and backslashes in a name stay inside its value, and a
// document that is not JSON once filled is refused rather than written.
func TestAnAnswerCannotBreakTheDocumentItIsPutIn(t *testing.T) {
	filled, err := apppkg.Fill([]byte(shop), map[string]string{"bau": `g-"bau"\`, "alle": "g-alle", "fm": "fmm"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Catalogs []struct {
			Groups []string `json:"groups"`
		} `json:"catalogs"`
	}
	if err := json.Unmarshal(filled, &doc); err != nil {
		t.Fatalf("filled document is not JSON: %v\n%s", err, filled)
	}
	if got := doc.Catalogs[0].Groups; len(got) != 2 || got[0] != `g-"bau"\` || got[1] != "g-alle" {
		t.Errorf("groups = %q, want the answers verbatim inside their strings", got)
	}
	if strings.Contains(string(filled), "{{") {
		t.Errorf("a question is left in %s", filled)
	}
	if _, err := apppkg.Fill([]byte(`{"groups":[{{bau}}]}`), map[string]string{"bau": "g-bau"}); err == nil ||
		!strings.Contains(err.Error(), apppkg.CatalogueFile) {
		t.Errorf("a question outside a string: err %v, want the file named as not JSON", err)
	}
}

// TestAPackageFileStaysReadableAndExact: indented, newline-terminated, a BPMN model
// left as XML rather than \u003c-escaped, and numbers read back as written.
func TestAPackageFileStaysReadableAndExact(t *testing.T) {
	out, err := apppkg.EncodeJSON(map[string]any{"xml": "<bpmn:process id=\"p\"/>", "rank": json.Number("1.50")})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, `<bpmn:process id=\"p\"/>`) || strings.Contains(text, `\u003c`) {
		t.Errorf("encoded = %s, want the XML unescaped", text)
	}
	if !strings.HasSuffix(text, "}\n") || !strings.Contains(text, "\n  \"rank\": 1.50") {
		t.Errorf("encoded = %q, want two-space indent and a final newline", text)
	}
	var back map[string]any
	if err := apppkg.Decode(out, &back); err != nil {
		t.Fatal(err)
	}
	if back["rank"] != json.Number("1.50") {
		t.Errorf("rank read back as %#v, want the number as written", back["rank"])
	}
	if _, err := apppkg.EncodeJSON(map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("a value JSON cannot hold was encoded")
	}
	if err := apppkg.Decode([]byte("{"), &back); err == nil {
		t.Error("a truncated document was decoded")
	}
}
