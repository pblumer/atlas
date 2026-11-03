package entra

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/lookup"
)

// TestValidLookupValueAsHoldsEachPropertyToItsShape: a user is identified by its
// object id or its user principal name, a group by its alias; a property the resource
// does not admit, an unknown resource and text no lookup carries hold no valid value.
func TestValidLookupValueAsHoldsEachPropertyToItsShape(t *testing.T) {
	for _, c := range []struct {
		resource, property, value string
		want                      bool
	}{
		{"users", "id", "00000000-0000-0000-0000-000000000001", true},
		{"users", "id", "anna@example.com", false},
		{"users", "userPrincipalName", "anna@example.com", true},
		{"users", "userPrincipalName", "anna", false},
		{"users", "mail", "anna@example.com", false},
		{"groups", "mailNickname", "dev-huda", true},
		{"groups", "mailNickname", "-dev", false},
		{"groups", "id", "00000000-0000-0000-0000-000000000001", false},
		{"nope", "id", "00000000-0000-0000-0000-000000000001", false},
		{"users", "userPrincipalName", strings.Repeat("a", lookup.MaxValueLength) + "@example.com", false},
		{"users", "userPrincipalName", "anna\u0000@example.com", false},
	} {
		if got := ValidLookupValueAs(c.resource, c.property, c.value); got != c.want {
			t.Errorf("ValidLookupValueAs(%q, %q, %q) = %v, want %v", c.resource, c.property, c.value, got, c.want)
		}
	}
}

// TestAQueryNamesTheValuePropertyItsResourceAdmits: a query that names no value
// property identifies records by the resource's own; one that names an admitted
// property keeps it; any other is refused as "".
func TestAQueryNamesTheValuePropertyItsResourceAdmits(t *testing.T) {
	users := LookupResources["users"]
	for _, c := range []struct{ asked, want string }{
		{"", "id"},
		{"id", "id"},
		{"userPrincipalName", "userPrincipalName"},
		{"mail", ""},
	} {
		if got := users.valueProperty(lookup.Query{ValueProperty: c.asked}); got != c.want {
			t.Errorf("users.valueProperty(%q) = %q, want %q", c.asked, got, c.want)
		}
	}
}
