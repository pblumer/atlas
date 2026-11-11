package examples

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestAPositionInAnAtlasURLIsEncoded keeps every REST connector task that addresses an
// order position by its id encoding the '#' in it. A position of a product with variants
// is called "<product>#<variant>" (ADR-0384), and an unencoded '#' makes the rest of the
// address a fragment: the request reaches /lines/<product> without its last segment, the
// route that records an outcome answers 400, and the line never settles. The shop's own
// fulfilment process encodes it the same way (api/systemprocesses/auftrag-erfuellung.bpmn).
func TestAPositionInAnAtlasURLIsEncoded(t *testing.T) {
	url := regexp.MustCompile(`(?s)\burl=("[^"]*"|'[^']*')`)
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".bpmn") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range url.FindAllStringSubmatch(string(b), -1) {
			u := html.UnescapeString(m[1])
			if strings.Contains(u, "positionId") && !strings.Contains(u, `"%23"`) {
				t.Errorf("%s: a REST URL carries positionId without encoding '#': %.140s", path, u)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTheTransferDecisionURLAddressesTheVariantPosition evaluates the address the
// Informationstransfer refuses a position with, for a position with a variant: it must
// end in /lines/<product>%23<variant>/decision.
func TestTheTransferDecisionURLAddressesTheVariantPosition(t *testing.T) {
	b, err := os.ReadFile("informationstransfer/informationstransfer.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`url='(=[^']*/decision")'`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no /decision URL in informationstransfer.bpmn")
	}
	c, err := expr.CompileAuto(html.UnescapeString(m[1])[1:])
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Eval(map[string]expr.Value{
		"atlasApiBase": expr.FromJSON("http://127.0.0.1:8080"), "orderId": expr.FromJSON("ord_1"),
		"positionId": expr.FromJSON("it-informationstransfer#amtswechsel"), "itemId": expr.FromJSON("it-informationstransfer"),
		"application": expr.FromJSON(map[string]any{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := expr.ToJSON(v)
	if want := `"http://127.0.0.1:8080/api/v1/orders/ord_1/lines/it-informationstransfer%23amtswechsel/decision"`; got != want {
		t.Errorf("url = %s, want %s", got, want)
	}
}
