package order

import "testing"

// Atlas's own approval processes are told apart from a model the installation binds by
// name, because only the latter is given the line's answers (ADR-0441).
func TestOnlyAtlasOwnApprovalKindsCountAsAtlasApproval(t *testing.T) {
	cases := map[string]bool{
		"fixed":                   true,
		"role":                    true,
		"superior":                true,
		"none":                    false,
		"":                        false,
		"proc_custom_genehmigung": false,
	}
	for kind, want := range cases {
		l := Line{Approval: Approval{Kind: kind}}
		if got := l.AtlasApproval(); got != want {
			t.Errorf("kind %q: AtlasApproval() = %v, want %v", kind, got, want)
		}
	}
}
