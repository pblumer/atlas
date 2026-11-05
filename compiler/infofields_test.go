package compiler

import (
	"slices"
	"testing"
)

// A namespace's info fields are listed in a stable order; a namespace without any
// lists none.
func TestInfoFields(t *testing.T) {
	if got := InfoFields(ApplicationNamespace); !slices.Equal(got, []string{"id", "key", "name"}) {
		t.Errorf("InfoFields(application) = %v", got)
	}
	if got := InfoFields("nobody"); got != nil {
		t.Errorf("InfoFields(nobody) = %v, want none", got)
	}
}
