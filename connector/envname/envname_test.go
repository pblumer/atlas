package envname_test

import (
	"testing"

	"github.com/pblumer/atlas/connector/envname"
)

// The fold, on the shapes a worker name and a secret reference actually take.
//
// It is one function in one place precisely so these cases have one answer. The
// engine renders a variable from a name, a worker reads a variable from the same
// name, and a worker quotes the variable it could not resolve — three packages
// that never see each other, agreeing only because they all call this.
func TestKey(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"already a variable name", "AD_DEMO_BIND", "AD_DEMO_BIND"},
		{"lower case", "ad_demo_bind", "AD_DEMO_BIND"},
		{"spaces", "ad demo bind", "AD_DEMO_BIND"},
		{"hyphens and dots", "ad-demo.bind", "AD_DEMO_BIND"},
		{"a run of separators collapses", "ad -- demo", "AD_DEMO"},
		{"leading separators do not open with an underscore", "  ad", "AD"},
		{"trailing separators add nothing", "ad  ", "AD"},
		{"digits survive", "dc1", "DC1"},
		{"non-ASCII is a separator", "büro bind", "B_RO_BIND"},
		{"nothing usable", " - ", ""},
		{"empty", "", ""},
	} {
		if got := envname.Key(tc.in); got != tc.want {
			t.Errorf("%s: Key(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// ConnectorToken is what an error message quotes, so the whole variable — prefix and
// suffix included — has to come out of one place too. An operator copying it must be
// able to paste it.
func TestConnectorToken(t *testing.T) {
	if got, want := envname.ConnectorToken("AD_DEMO_BIND"), "ATLAS_CONNECTOR_AD_DEMO_BIND_TOKEN"; got != want {
		t.Errorf("ConnectorToken = %q, want %q", got, want)
	}
	if got, want := envname.ConnectorToken("ad-demo bind"), "ATLAS_CONNECTOR_AD_DEMO_BIND_TOKEN"; got != want {
		t.Errorf("a reference with punctuation folded to %q, want %q", got, want)
	}
}
