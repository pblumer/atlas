package ad

import (
	"errors"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/workersdk/lookup"
)

// lookupFailure hands on nothing but a code: a lookup error as it is, a refusal by the
// directory as provider_refused, anything else as provider_unavailable.
func TestLookupFailureKeepsOnlyTheCode(t *testing.T) {
	own := lookup.Fail(lookup.CodeRefused)
	if got := lookupFailure(own); got != own {
		t.Errorf("a lookup error came back as %v", got)
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"invalid credentials", goldap.NewError(goldap.LDAPResultInvalidCredentials, errors.New("(sAMAccountName=anna*)")), lookup.CodeRefused},
		{"filter error", goldap.NewError(goldap.LDAPResultFilterError, errors.New("bad filter")), lookup.CodeRefused},
		{"busy", goldap.NewError(goldap.LDAPResultBusy, errors.New("busy")), lookup.CodeUnavailable},
		{"network", errors.New("dial tcp: connection refused"), lookup.CodeUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lookupFailure(tc.err)
			var le *lookup.Error
			if !errors.As(got, &le) || got.Error() != lookup.Fail(tc.want).Error() {
				t.Fatalf("lookupFailure(%v) = %v, want %s", tc.err, got, tc.want)
			}
			if got.Error() == tc.err.Error() {
				t.Errorf("the directory's message leaked: %v", got)
			}
		})
	}
}
