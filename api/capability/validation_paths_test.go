package capability

import (
	"slices"
	"testing"
)

// TestEveryUnnamedPartIsNamedByPosition: the refusals TestValidateCapabilityRefusals
// matches by keyword are checked here by sentence, for the parts it never left
// unnamed — a resource, an SLA's name and metric. The position is what lets somebody
// find the row in a long list.
func TestEveryUnnamedPartIsNamedByPosition(t *testing.T) {
	c := Capability{Key: "onboarding", Name: "Customer Onboarding", State: StateActive,
		Resources: []Resource{{Kind: ResourceTeam, Name: "Clerks"}, {Kind: ResourceTeam}},
		SLAs:      []SLA{{Threshold: "5d", Scope: SLAInternal}},
	}
	findings := ValidateCapability(c)
	for _, want := range []string{
		"resource 2: name is required",
		"sla 1: name is required",
		"sla 1: metric is required",
	} {
		if !slices.Contains(findings, want) {
			t.Errorf("findings %q do not include %q", findings, want)
		}
	}
	if len(findings) != 3 {
		t.Errorf("findings = %q, want exactly the three", findings)
	}
}

// TestAValueStreamKeyMustBeAKey: the key is the record's identity and its file name,
// so a display name typed into it is refused with the rule spelled out.
func TestAValueStreamKeyMustBeAKey(t *testing.T) {
	findings := ValidateValueStream(ValueStream{Key: "Consumer Loan", Name: "Consumer Loan"})
	want := `key "Consumer Loan" is not a valid key: lower-case letters, digits and dashes, 1 to 64 characters, ` +
		"starting and ending with a letter or digit"
	if len(findings) != 1 || findings[0] != want {
		t.Fatalf("findings = %q, want only %q", findings, want)
	}
}
