package api

import (
	"net/url"
	"testing"
)

// TestOneHostIsOneThrottleKeyHoweverItIsSpelt: the budget for reading metadata
// documents is per host, so that one host cannot be read at the speed requests
// arrive. Counted under the host as the sender spelt it, the same host was a new key
// — with a full budget — in every other case, with a trailing dot, and with the port
// the scheme already implies.
func TestOneHostIsOneThrottleKeyHoweverItIsSpelt(t *testing.T) {
	want := "client.example.com:443"
	for _, raw := range []string{
		"https://client.example.com/doc.json",
		"https://CLIENT.Example.COM/doc.json",
		"https://client.example.com./doc.json",
		"https://client.example.com:443/doc.json",
		"https://Client.Example.com.:443/other.json",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got := metadataHostKey(u); got != want {
			t.Errorf("%s is counted under %q, want %q", raw, got, want)
		}
	}
	// Another port is another service, and stays another key.
	for raw, want := range map[string]string{
		"https://client.example.com:8443/doc.json": "client.example.com:8443",
		"http://localhost/doc.json":                "localhost:80",
		"https://[::1]:9000/doc.json":              "[::1]:9000",
	} {
		u, _ := url.Parse(raw)
		if got := metadataHostKey(u); got != want {
			t.Errorf("%s is counted under %q, want %q", raw, got, want)
		}
	}
}
