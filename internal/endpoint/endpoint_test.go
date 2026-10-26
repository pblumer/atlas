package endpoint

import "testing"

// TestReduceKeepsSchemeHostAndPortOnly: whatever shape a Worker Type stores its
// endpoint in, what comes out is scheme, host and port — never the user information,
// path, query or fragment the configured value carried — and what cannot be an address
// is refused rather than passed on.
func TestReduceKeepsSchemeHostAndPortOnly(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://svc:geheim@ERP.example.com:8443/api?token=x#f", "https://erp.example.com:8443", true},
		{"ldaps://dc1.corp.local", "ldaps://dc1.corp.local", true},
		{"mail.example.com:587", "mail.example.com:587", true},
		{"svc@relay.example.com:25/x", "relay.example.com:25", true},
		{"[::1]:9000", "[::1]:9000", true},
		{"", "", false},
		{"   ", "", false},
		{"not a host", "", false},
		{"https://", "", false},
	} {
		got, ok := Reduce(tc.raw)
		if ok != tc.ok || (ok && got.String() != tc.want) {
			t.Errorf("Reduce(%q) = %q, %v; want %q, %v", tc.raw, got.String(), ok, tc.want, tc.ok)
		}
	}
}
