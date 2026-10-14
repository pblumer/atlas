package order

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// TestEveryUnresolvedVariantIsNamed: each way a basket can name its shapes wrongly
// is refused with its own sentence, and a basket that names them rightly passes.
// The HTTP tests reach this through the order route; coverage is counted per
// package, and the rule is this package's.
func TestEveryUnresolvedVariantIsNamed(t *testing.T) {
	shapes := []catalog.Variant{{ID: "black"}, {ID: "white"}}
	rel := catalog.Release{Items: []catalog.Item{
		{ID: "phone", Variants: shapes},
		{ID: "sim", Variants: shapes, MultipleAllowed: true},
		{ID: "vpn"},
	}}
	for _, c := range []struct {
		name    string
		ordered []string
		chosen  map[string][]string
		want    string
	}{
		{"resolved", []string{"phone", "sim", "vpn"},
			map[string][]string{"phone": {"black"}, "sim": {"black", "white"}}, ""},
		{"not in the order", []string{"vpn"}, map[string][]string{"phone": {"black"}}, "which is not in it"},
		{"one shape only", []string{"vpn"}, map[string][]string{"vpn": {"black"}}, "comes in one shape"},
		{"held once", []string{"phone"}, map[string][]string{"phone": {"black", "white"}}, "may be held once"},
		{"unknown shape", []string{"phone"}, map[string][]string{"phone": {"red"}}, `does not come in "red"`},
		{"same shape twice", []string{"sim"}, map[string][]string{"sim": {"black", "black"}}, "twice"},
		{"shape missing", []string{"phone", "vpn"}, nil, "does not say which"},
	} {
		got := unresolvedVariant(rel, c.ordered, c.chosen)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
