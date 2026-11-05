package importplan

import (
	"net/url"
	"testing"
)

func TestGapsAreAdoptedMappedOrOmitted(t *testing.T) {
	d, err := Parse(url.Values{
		"adopt": {"capability:a"},
		"map":   {"capability:b=identitaet, capability:c=konten"},
		"omit":  {"valuestream:v"},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for ref, want := range map[string]string{
		"capability:a":  Created,
		"capability:b":  Mapped,
		"valuestream:v": Omitted,
		"capability:z":  Undecided,
	} {
		if got, _ := d.GapOutcome(ref); got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
	if _, to := d.GapOutcome("capability:c"); to != "konten" {
		t.Fatalf("mapped onto %q", to)
	}
	all, err := Parse(url.Values{"omit": {"*"}, "adopt": {"capability:a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := all.GapOutcome("capability:x"); got != Omitted {
		t.Fatalf("omit=* left %s", got)
	}
	if got, _ := all.GapOutcome("capability:a"); got != Created {
		t.Fatalf("omit=* overrode an adopted gap: %s", got)
	}
}

func TestContradictoryGapDecisionsAreRefused(t *testing.T) {
	for name, q := range map[string]url.Values{
		"no key":              {"map": {"capability:a"}},
		"mapped twice":        {"map": {"capability:a=x", "capability:a=y"}},
		"mapped and omitted":  {"map": {"capability:a=x"}, "omit": {"capability:a"}},
		"adopted and omitted": {"adopt": {"capability:a"}, "omit": {"capability:a"}},
		"map all":             {"map": {"*=x"}},
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
