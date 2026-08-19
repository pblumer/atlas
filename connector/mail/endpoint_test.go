package mail

import (
	"strings"
	"testing"
)

// TestNormalizeSMTPEndpoint covers the shapes an operator actually types. The first
// case is the outage this function was written for: a submission host written without
// a port dialed nowhere and surfaced, much later, as "missing port in address" on a
// parked token (ADR-0149).
func TestNormalizeSMTPEndpoint(t *testing.T) {
	ok := map[string]string{
		"mail.example.com":             "mail.example.com:587",
		"  mail.example.com  ":         "mail.example.com:587",
		"mail.example.com:2525":        "mail.example.com:2525",
		"mail.example.com:":            "mail.example.com:587",
		"smtp://mail.example.com":      "mail.example.com:587",
		"smtp://mail.example.com/":     "mail.example.com:587",
		"SMTP://mail.example.com:2525": "mail.example.com:2525",
		"smtps://mail.example.com":     "mail.example.com:465",
		"smtps://mail.example.com:587": "mail.example.com:587",
		"localhost:1025":               "localhost:1025",
		"127.0.0.1":                    "127.0.0.1:587",
		"::1":                          "[::1]:587",
		"[::1]:25":                     "[::1]:25",
	}
	for in, want := range ok {
		got, err := NormalizeSMTPEndpoint(in)
		if err != nil {
			t.Errorf("NormalizeSMTPEndpoint(%q) = error %v, want %q", in, err, want)
			continue
		}
		if got != want {
			t.Errorf("NormalizeSMTPEndpoint(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"",
		"   ",
		":587",                        // no host
		"https://mail.example.com",    // not a submission scheme
		"mail.example.com:0",          // port out of range
		"mail.example.com:70000",      // port out of range
		"mail.example.com:submission", // a service name, not a port number
		"mail example.com",            // whitespace in the host
		"bot@example.com",             // the mailbox, not the server
	}
	for _, in := range bad {
		if got, err := NormalizeSMTPEndpoint(in); err == nil {
			t.Errorf("NormalizeSMTPEndpoint(%q) = %q, want an error", in, got)
		}
	}
}

// TestNormalizeSMTPEndpointIsIdempotent: a normalized endpoint is stored and
// re-normalized on every later read and update, so it has to be a fixed point.
func TestNormalizeSMTPEndpointIsIdempotent(t *testing.T) {
	for _, in := range []string{"mail.example.com", "smtps://mail.example.com", "[::1]:25"} {
		once, err := NormalizeSMTPEndpoint(in)
		if err != nil {
			t.Fatalf("NormalizeSMTPEndpoint(%q): %v", in, err)
		}
		twice, err := NormalizeSMTPEndpoint(once)
		if err != nil {
			t.Fatalf("NormalizeSMTPEndpoint(%q): %v", once, err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q → %q → %q", in, once, twice)
		}
	}
}

// TestNormalizeSMTPEndpointErrorsNameTheInput: these messages are shown to whoever
// typed the endpoint, so they have to quote what was actually typed rather than some
// internal remainder of it.
func TestNormalizeSMTPEndpointErrorsNameTheInput(t *testing.T) {
	_, err := NormalizeSMTPEndpoint("smtp://mail.example.com:99999")
	if err == nil {
		t.Fatal("want an error for an out-of-range port")
	}
	if want := "smtp://mail.example.com:99999"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not quote the input %q", err, want)
	}
}
