package trustedproxy

import (
	"net/netip"
	"strings"
	"testing"
)

// TestParseReadsAddressesAndPrefixes: the list an operator writes is the load balancer's
// own addresses, or the subnet it lives in, separated however a person separates them.
func TestParseReadsAddressesAndPrefixes(t *testing.T) {
	s, err := Parse(" 10.179.2.139, 192.168.10.0/24 2001:db8::/48,::ffff:172.16.0.0/108 ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"10.179.2.139", true},
		{"10.179.2.140", false},
		{"192.168.10.77", true},
		{"192.168.11.1", false},
		{"2001:db8::1", true},
		{"2001:db9::1", false},
		// A 4-in-6 prefix is the IPv4 prefix it spells, and a 4-in-6 peer is the IPv4
		// address it carries: a dual-stack socket must not decide who is trusted.
		{"172.16.0.9", true},
		{"::ffff:10.179.2.139", true},
		{"fe80::1%eth0", false},
	} {
		if got := s.Contains(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("Contains(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
	if s.Empty() {
		t.Error("a parsed list reports itself empty")
	}
	want := "10.179.2.139/32, 192.168.10.0/24, 2001:db8::/48, 172.16.0.0/12"
	if got := s.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestNothingIsTrustedByDefault: an empty list is a server that believes only the
// connection, which is what every deployment had before this existed.
func TestNothingIsTrustedByDefault(t *testing.T) {
	for _, spec := range []string{"", "   ", ",,"} {
		s, err := Parse(spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", spec, err)
		}
		if !s.Empty() {
			t.Errorf("Parse(%q) trusts something", spec)
		}
		if s.Contains(netip.MustParseAddr("127.0.0.1")) {
			t.Errorf("Parse(%q) trusts loopback", spec)
		}
	}
}

// TestParseRefusesWhatItCannotMean: a typo must stop the boot. A list that silently
// lost an entry trusts less than the operator believes, and one that silently grew
// trusts more.
func TestParseRefusesWhatItCannotMean(t *testing.T) {
	for _, tc := range []struct{ spec, mention string }{
		{"10.179.2.300", "10.179.2.300"},
		{"lb.internal", "lb.internal"},
		{"10.0.0.0/33", "10.0.0.0/33"},
		{"10.0.0.1, nonsense", "nonsense"},
		// Trusting every address is trusting the client to name itself — the one
		// thing this list exists to prevent.
		{"0.0.0.0/0", "every address"},
		{"::/0", "every address"},
	} {
		_, err := Parse(tc.spec)
		if err == nil {
			t.Errorf("Parse(%q) accepted it", tc.spec)
			continue
		}
		if !strings.Contains(err.Error(), tc.mention) {
			t.Errorf("Parse(%q) = %q, want it to mention %q", tc.spec, err, tc.mention)
		}
	}
}

func mustParse(spec string) Set {
	s, err := Parse(spec)
	if err != nil {
		panic(err)
	}
	return s
}
