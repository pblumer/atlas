package mail

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Default submission ports for an SMTP endpoint written without one: 587 is the
// RFC 6409 message submission port every modern provider serves with STARTTLS, and
// 465 is implicit TLS, which an "smtps://" endpoint asks for by name.
const (
	defaultSubmissionPort  = "587"
	defaultImplicitTLSPort = "465"
)

// NormalizeSMTPEndpoint canonicalizes an operator-written SMTP submission endpoint
// into the "host:port" form [net/smtp] requires, or explains why it cannot.
//
// This exists because the shapes a human writes for a mail server — "mx1.example.ch",
// "smtp://mx1.example.ch", "smtps://mx1.example.ch/" — are all obviously *meant* as a
// submission endpoint, but only one of them dials. Passing an endpoint without a port
// straight through fails deep inside the send with "missing port in address", which
// surfaces as an incident on a parked token hours after someone configured the
// worker — the failure is real, but it arrives at the wrong time, to the wrong
// person, in the wrong words. Normalizing at the boundary turns three of those into a
// working worker and the rest into a message at the moment of typing.
//
// The rules, in order: an optional "smtp://" or "smtps://" scheme is consumed (smtps
// selects implicit TLS, hence port 465); a path, query or fragment is dropped, so a
// pasted URL works; a bare IPv6 address is bracketed; and a missing port becomes the
// submission default. Host and port are then checked, so what comes back either dials
// or is rejected here. The returned error is a complete sentence, suitable for showing
// to whoever typed the endpoint.
func NormalizeSMTPEndpoint(endpoint string) (string, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return "", errors.New("smtp endpoint is required (e.g. \"mail.example.com\" or \"mail.example.com:587\")")
	}

	port := defaultSubmissionPort
	if i := strings.Index(raw, "://"); i >= 0 {
		switch scheme := strings.ToLower(raw[:i]); scheme {
		case "smtp":
		case "smtps":
			port = defaultImplicitTLSPort // implicit TLS asks for the submissions port by name
		default:
			return "", fmt.Errorf("smtp endpoint %q uses the %q scheme (want a bare host, \"smtp://\" or \"smtps://\")", endpoint, scheme)
		}
		raw = raw[i+3:]
	}
	// A pasted URL keeps a trailing path, query or fragment; none of it addresses a
	// submission server, so it is dropped rather than made part of the host.
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)

	host := raw
	switch h, p, err := net.SplitHostPort(raw); {
	case err == nil:
		host = h
		if p = strings.TrimSpace(p); p != "" {
			port = p // "host:" keeps the default rather than dialing port zero
		}
	case net.ParseIP(raw) != nil:
		// A bare IPv6 literal ("::1") is "too many colons" to SplitHostPort but a
		// perfectly good host; JoinHostPort brackets it below.
	default:
		var addrErr *net.AddrError
		if !errors.As(err, &addrErr) || addrErr.Err != "missing port in address" {
			return "", fmt.Errorf("smtp endpoint %q is not a host or host:port (%w)", endpoint, err)
		}
	}

	if err := checkSMTPHost(host, endpoint); err != nil {
		return "", err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("smtp endpoint %q has an invalid port %q (want a number from 1 to 65535)", endpoint, port)
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// checkSMTPHost rejects the host shapes that would dial nowhere, naming what was
// actually typed. The "@" case is its own message because pasting the mailbox address
// into the server field is the mistake people actually make.
func checkSMTPHost(host, endpoint string) error {
	switch {
	case host == "":
		return fmt.Errorf("smtp endpoint %q names no host", endpoint)
	case strings.ContainsAny(host, " \t"):
		return fmt.Errorf("smtp endpoint %q has whitespace in its host", endpoint)
	case strings.Contains(host, "@"):
		return fmt.Errorf("smtp endpoint %q looks like an e-mail address — this field wants the mail *server* (e.g. \"smtp.example.com:587\"); the address belongs in the sender field", endpoint)
	}
	return nil
}
