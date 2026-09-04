package compiler

import (
	"reflect"
	"strings"
	"testing"
)

// A process states which of its variables are worth finding an instance by:
// atlas:searchable="identityId,item". The names are resolved at deploy time
// (invariant I5) so the runtime never parses this attribute, and the engine can
// ask a compiled process one question per variable write.
//
// It is a declaration, not a hint: indexing every value would double the write
// path and index JSON blobs, so a process that declares nothing pays nothing.
func TestProcessSearchableVariables(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr string
		want []string
	}{
		{"one", ` searchable="identityId"`, []string{"identityId"}},
		{"several", ` searchable="identityId,item"`, []string{"identityId", "item"}},
		{"namespaced", ` atlas:searchable="identityId"`, []string{"identityId"}},
		// Authored by hand, so the separator is forgiving about spacing.
		{"spaced", ` searchable=" identityId , item "`, []string{"identityId", "item"}},
		{"absent", "", nil},
		{"empty", ` searchable=""`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cp.SearchableVariables(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SearchableVariables() = %v, want %v", got, tc.want)
			}
			for _, n := range tc.want {
				if !cp.IsSearchableVariable(n) {
					t.Errorf("IsSearchableVariable(%q) = false, want true", n)
				}
			}
			if cp.IsSearchableVariable("somethingElse") {
				t.Error("IsSearchableVariable of an undeclared name = true, want false")
			}
		})
	}
}

// A declaration that cannot mean anything fails the deploy rather than silently
// indexing nothing — the same reasoning the two TTLs are validated under. A
// nameless entry is the typo this catches ("a,,b", or a trailing comma).
func TestProcessSearchableVariablesInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr string
	}{
		{"empty-entry", ` searchable="identityId,,item"`},
		{"trailing-comma", ` searchable="identityId,"`},
		{"only-spaces", ` searchable="   ,  "`},
		{"duplicate", ` searchable="identityId,identityId"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(10, 1, strings.NewReader(ttlXML(tc.attr))); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", tc.attr)
			}
		})
	}
}

// A process that declares nothing must be able to say so in one check: the engine
// consults this on every variable write, and a process with no declaration has to
// pay nothing for the feature.
func TestProcessHasNoSearchableVariablesByDefault(t *testing.T) {
	cp, err := Parse(10, 1, strings.NewReader(ttlXML("")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cp.HasSearchableVariables() {
		t.Error("HasSearchableVariables() = true on a process that declares none")
	}
	cp2, err := Parse(10, 1, strings.NewReader(ttlXML(` searchable="identityId"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cp2.HasSearchableVariables() {
		t.Error("HasSearchableVariables() = false on a process that declares one")
	}
}
