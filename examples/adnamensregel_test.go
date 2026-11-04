package examples

import (
	"context"
	"os"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestADNamingRuleCoversTheNonPersonalAccounts evaluates AdNamensregel of ad-dienste for
// the account types the product ad-unpersoenliches-konto offers (F, R, E): each gets its
// prefix, the length limit of sAMAccountName still refuses, and a type without a rule —
// the null the process passes for an unknown variant — is refused (fail-closed).
func TestADNamingRuleCoversTheNonPersonalAccounts(t *testing.T) {
	src, err := os.ReadFile("ad-dienste/ad-namensregel.dmn")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule, err := dmn.CompileStandalone(ctx, src, "AdNamensregel")
	if err != nil {
		t.Fatal(err)
	}
	eval := func(objekttyp any, bezeichnung string) map[string]any {
		t.Helper()
		got, err := rule.Evaluate(ctx, map[string]any{"objekttyp": objekttyp, "bezeichnung": bezeichnung, "bereich": nil})
		if err != nil {
			t.Fatal(err)
		}
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("result %T %v, want a context", got, got)
		}
		return m
	}

	for _, c := range []struct{ typ, want, display string }{
		{"funktion", "f-kasse", "Funktionskonto kasse"},
		{"rolle", "r-kasse", "Rollenkonto kasse"},
		{"schulung", "e-kasse", "Schulungskonto kasse"},
		{"service", "svc-kasse", "Servicekonto kasse"},
	} {
		m := eval(c.typ, "kasse")
		if m["erlaubt"] != true || m["sAMAccountName"] != c.want || m["cn"] != c.want || m["displayName"] != c.display {
			t.Errorf("%s: got %v, want %s / %s", c.typ, m, c.want, c.display)
		}
	}

	if m := eval("funktion", "abcdefghijklmnopq"); m["erlaubt"] != false {
		t.Errorf("a name over 20 characters was allowed: %v", m)
	}
	if m := eval("rolle", "admin"); m["erlaubt"] != false {
		t.Errorf("a reserved name was allowed: %v", m)
	}
	if m := eval(nil, "kasse"); m["erlaubt"] != false {
		t.Errorf("an unknown account type was allowed: %v", m)
	}
}
