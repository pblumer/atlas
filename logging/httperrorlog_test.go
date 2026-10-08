package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-draft-trusted-proxies: what net/http says about a connection.
//
// A load balancer's TCP health check opens a connection to the TLS port and closes it
// before sending a ClientHello. net/http reports every one of them as
// "http: TLS handshake error from <peer>: EOF", and before this change that arrived as an
// INFO line with no event name — 8640 a day at a ten-second interval, burying the TLS
// failures that do matter under the ones that are the health check working.

// records parses one JSON object per line.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("not one JSON object per line: %v\n%s", err, buf.String())
		}
		out = append(out, rec)
	}
	return out
}

func setupAt(t *testing.T, l Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := Setup(&buf, FormatJSON, l); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return &buf
}

// TestAHandshakeNobodyStartedIsDebug: the health-check shape is demoted, not dropped. At
// the default level it is not written at all; at debug it is, under a name an operator
// can count.
func TestAHandshakeNobodyStartedIsDebug(t *testing.T) {
	const line = "http: TLS handshake error from 10.179.2.139:42658: EOF\n"

	buf := setupAt(t, DefaultLevel)
	HTTPServerErrorLog().Print(line)
	if buf.Len() != 0 {
		t.Fatalf("at the default level the health-check line is still written:\n%s", buf.String())
	}

	buf = setupAt(t, LevelDebug)
	HTTPServerErrorLog().Print(line)
	recs := records(t, buf)
	if len(recs) != 1 {
		t.Fatalf("got %d lines at debug, want 1:\n%s", len(recs), buf.String())
	}
	rec := recs[0]
	if rec["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", rec["level"])
	}
	if rec["event"] != "server.tls_handshake_aborted" {
		t.Errorf("event = %v, want server.tls_handshake_aborted", rec["event"])
	}
	if rec["remote"] != "10.179.2.139:42658" {
		t.Errorf("remote = %v, want the peer as a typed field", rec["remote"])
	}
}

// TestEveryOtherServerErrorKeepsItsLevel: only the one shape is demoted. A handshake that
// failed for a reason — a client that cannot speak TLS 1.3, a certificate it refused — is
// exactly what the demotion is meant to make visible, so it stays where it was: INFO,
// wording untouched, as the standard logger would have written it.
func TestEveryOtherServerErrorKeepsItsLevel(t *testing.T) {
	for _, line := range []string{
		"http: TLS handshake error from 192.0.2.7:51234: tls: client offered only unsupported versions: [303 302 301]",
		"http: TLS handshake error from 192.0.2.7:51234: remote error: tls: bad certificate",
		"http: TLS handshake error from 192.0.2.7:51234: unexpected EOF",
		"http: panic serving 192.0.2.7:51234: boom",
		"http: Accept error: too many open files; retrying in 5ms",
	} {
		buf := setupAt(t, DefaultLevel)
		HTTPServerErrorLog().Print(line)
		recs := records(t, buf)
		if len(recs) != 1 {
			t.Fatalf("%q: got %d lines, want 1:\n%s", line, len(recs), buf.String())
		}
		if recs[0]["level"] != "INFO" {
			t.Errorf("%q: level = %v, want INFO", line, recs[0]["level"])
		}
		if recs[0]["msg"] != line {
			t.Errorf("%q: msg = %q, want the line verbatim", line, recs[0]["msg"])
		}
	}
}

// TestLevelHidesWhatIsBelowIt: --log-level is a floor, and the floor is honoured for the
// catalogue's own events too, not only for net/http's.
func TestLevelHidesWhatIsBelowIt(t *testing.T) {
	buf := setupAt(t, LevelWarn)
	Info(RetentionPurged, "purged finished instances")
	Warn(ExporterTickFailed, "export tick failed")
	recs := records(t, buf)
	if len(recs) != 1 || recs[0]["level"] != "WARN" {
		t.Fatalf("at warn, want only the WARN line:\n%s", buf.String())
	}
}

// TestInfoIsTheDefaultLevel: a server started without --log-level writes what it always
// wrote. Changing that would be a regression nobody asked for.
func TestInfoIsTheDefaultLevel(t *testing.T) {
	if DefaultLevel != LevelInfo {
		t.Fatalf("DefaultLevel = %q, want %q", DefaultLevel, LevelInfo)
	}
}

// TestUnknownLevelIsRefused: like a bad --log-format, a typo must stop the boot rather
// than quietly log at a level nobody chose.
func TestUnknownLevelIsRefused(t *testing.T) {
	var buf bytes.Buffer
	err := Setup(&buf, FormatText, Level("verbose"))
	if err == nil {
		t.Fatal("Setup accepted an unknown level")
	}
	if !strings.Contains(err.Error(), "verbose") || !strings.Contains(err.Error(), string(LevelDebug)) {
		t.Errorf("error %q should name both the bad value and the valid ones", err)
	}
}
