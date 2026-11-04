package contentsource

import "testing"

// TestACopiedRichListSharesNoMemoryWithItsOriginal: a copy whose table cells or child
// blocks are edited leaves the original as it was, at every depth.
func TestACopiedRichListSharesNoMemoryWithItsOriginal(t *testing.T) {
	if CopyRich(nil) != nil {
		t.Fatal("a nil list copied to something")
	}
	orig := []Rich{
		{Kind: "table", Rows: [][]string{{"a", "b"}}},
		{Kind: "page", Title: "child", Blocks: []Rich{{Kind: "table", Rows: [][]string{{"x"}}}}},
	}
	cp := CopyRich(orig)
	cp[0].Rows[0][0] = "changed"
	cp[1].Blocks[0].Rows[0][0] = "changed"
	cp[1].Title = "renamed"
	if orig[0].Rows[0][0] != "a" || orig[1].Blocks[0].Rows[0][0] != "x" || orig[1].Title != "child" {
		t.Fatalf("editing the copy changed the original: %+v", orig)
	}
}
