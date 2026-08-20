package mimimport

import "testing"

// TestTranslateCondition pins the WAL→FEEL translation for the idioms a MIMWAL
// ActivityExecutionCondition uses, and the fallback (ok=false) for anything the
// first-pass translator does not confidently understand.
func TestTranslateCondition(t *testing.T) {
	ok := []struct{ in, want string }{
		{
			"ConvertToBoolean(//WorkflowData/HasStructureResponsibilities) Or ConvertToBoolean(//WorkflowData/HasDisableInheritanceResponsibilities)",
			"(HasStructureResponsibilities) or (HasDisableInheritanceResponsibilities)",
		},
		{
			"ConvertToBoolean(//WorkflowData/HasResponsibilities) And ConvertToBoolean(//WorkflowData/PersonTransitionInGracePeriod)",
			"(HasResponsibilities) and (PersonTransitionInGracePeriod)",
		},
		{"ConvertToBoolean(//Target/PersonType/UseSuffixAccountName)", "(Target.PersonType.UseSuffixAccountName)"},
		{`//WorkflowData/Department = "Finance"`, `Department = "Finance"`},
		{`//WorkflowData["HasResponsibilities"]`, "HasResponsibilities"},
		{"//WorkflowData/Count > 0", "Count > 0"},
		{"Not(//WorkflowData/Flag)", "not(Flag)"},
		{"//WorkflowData/A <> //WorkflowData/B", "A != B"},
	}
	for _, tc := range ok {
		got, ok := translateCondition(tc.in)
		if !ok {
			t.Errorf("translateCondition(%q) unexpectedly failed", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("translateCondition(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Constructs outside the recognised set must fall back rather than mistranslate.
	bad := []string{
		"IIF(//WorkflowData/X, 1, 0) = 1", // unknown function
		"//WorkflowData/X + 1",            // arithmetic not modelled
		"CustomFunc(//WorkflowData/X)",    // unknown function
		"",                                // empty
		"//WorkflowData/",                 // dangling reference
	}
	for _, in := range bad {
		if got, ok := translateCondition(in); ok {
			t.Errorf("translateCondition(%q) = %q, want fallback", in, got)
		}
	}
}
