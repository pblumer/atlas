package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

// ADR-0142 slice 8a: logs as a contract.
//
// Every operational line Atlas wrote was a prose sentence with values interpolated into
// it. An operator could read one; nothing could *alert* on one, except by matching a
// regular expression against wording that changes the moment someone rewords a sentence.
//
// The fix is not to delete the prose — it carries real guidance ("will retry next tick")
// that a bare event name loses — but to put a stable name beside it. The name is the
// contract, the sentence is the explanation, and the values move out of the sentence into
// typed attributes.

// capture runs Setup against a buffer and returns it.
func capture(t *testing.T, f Format) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := Setup(&buf, f); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return &buf
}

// TestEventIsCarriedAsAStableAttribute is the whole point of the slice: the name an
// alert matches on is a field, not the wording of the sentence.
func TestEventIsCarriedAsAStableAttribute(t *testing.T) {
	buf := capture(t, FormatJSON)
	Info(CheckpointPublished, "checkpoint published; recovery replays only past it",
		slog.Uint64("position", 4711))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not one JSON object per line: %v\n%s", err, buf.String())
	}
	if got := rec["event"]; got != "checkpoint.published" {
		t.Errorf("event = %v, want checkpoint.published", got)
	}
	if got, ok := rec["msg"].(string); !ok || !strings.Contains(got, "recovery replays only past it") {
		t.Errorf("msg = %v, want the human-readable explanation to survive", rec["msg"])
	}
	if got := rec["position"]; got != float64(4711) {
		t.Errorf("position = %v, want 4711 as a typed field rather than text in the message", got)
	}
	if got := rec["level"]; got != "INFO" {
		t.Errorf("level = %v, want INFO", got)
	}
}

// TestTextIsTheDefaultAndStaysReadable: an operator watching `atlas serve` in a terminal
// is the other audience, and they are the one who has always been served. The default
// must not turn their console into JSON.
func TestTextIsTheDefaultAndStaysReadable(t *testing.T) {
	if DefaultFormat != FormatText {
		t.Fatalf("DefaultFormat = %q, want %q — the console audience is the default one", DefaultFormat, FormatText)
	}
	buf := capture(t, DefaultFormat)
	Warn(WALCompactionFailed, "wal compaction failed; will retry next tick",
		slog.String("error", "disk full"))

	line := buf.String()
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		t.Fatalf("the default format is JSON:\n%s", line)
	}
	for _, want := range []string{"event=wal_compaction.failed", "level=WARN", "will retry next tick", `error="disk full"`} {
		if !strings.Contains(line, want) {
			t.Errorf("text output is missing %q:\n%s", want, line)
		}
	}
}

// TestLevelsSeparateRoutineFromWrong: a line that reports work getting done and a line
// that reports work failing must be distinguishable without reading them.
func TestLevelsSeparateRoutineFromWrong(t *testing.T) {
	buf := capture(t, FormatJSON)
	Info(RetentionPurged, "purged finished instances", slog.Int("instances", 3))
	Warn(ExporterTickFailed, "export tick failed", slog.String("error", "timeout"))
	Error(CommandFailed, "atlas failed", slog.String("error", "bad flag"))

	want := []string{"INFO", "WARN", "ERROR"}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if rec["level"] != want[i] {
			t.Errorf("line %d level = %v, want %v", i, rec["level"], want[i])
		}
	}
}

// TestUnknownFormatIsRefused: a typo in --log-format must fail the boot rather than
// silently picking a format the operator did not ask for. They would find out from the
// shape of the logs, which is exactly when it is too late to matter.
func TestUnknownFormatIsRefused(t *testing.T) {
	var buf bytes.Buffer
	err := Setup(&buf, Format("logfmt"))
	if err == nil {
		t.Fatal("Setup accepted an unknown format")
	}
	if !strings.Contains(err.Error(), "logfmt") || !strings.Contains(err.Error(), string(FormatText)) {
		t.Errorf("error %q should name both the bad value and the valid ones", err)
	}
}

// TestStdlibLogJoinsTheSameStream: a line from a dependency that logs through the
// standard logger must not arrive in a second format with its own timestamp. There is
// one stream, and /api/v1/logs shows all of it.
//
// slog.SetDefault is what provides this — it repoints the standard logger at the handler
// and clears its flags — so the test pins behavior the package depends on rather than
// code it contains. That is worth a test precisely because it is not visible at the call
// site: nothing in Setup says "and now the log package works too".
func TestStdlibLogJoinsTheSameStream(t *testing.T) {
	buf := capture(t, FormatJSON)
	log.Printf("something from a dependency: %d", 7)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("a stdlib log line did not arrive as a record: %v\n%s", err, buf.String())
	}
	if got, ok := rec["msg"].(string); !ok || !strings.Contains(got, "something from a dependency: 7") {
		t.Errorf("msg = %v, want the stdlib line", rec["msg"])
	}
	// It is not one of our events, and must not pretend to be one.
	if _, ok := rec["event"]; ok {
		t.Errorf("a stdlib line carries an event name: %v", rec["event"])
	}
	// The stdlib prefix must be gone, or every line would carry two timestamps.
	if regexp.MustCompile(`\d{4}/\d{2}/\d{2}`).MatchString(rec["msg"].(string)) {
		t.Errorf("the stdlib timestamp prefix survived into the message: %v", rec["msg"])
	}
}

// TestEventNamesAreUniqueAndWellFormed. The names are the API an operator's alerts are
// written against, so they get the same treatment as the metric names in ADR-0142: a
// fixed shape, checked, rather than a convention that erodes.
func TestEventNamesAreUniqueAndWellFormed(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	seen := map[string]bool{}
	events := Events()
	if len(events) == 0 {
		t.Fatal("no events are registered")
	}
	for _, name := range events {
		if !shape.MatchString(name) {
			t.Errorf("event %q does not match subject.verb in lower_snake_case", name)
		}
		if seen[name] {
			t.Errorf("event %q is registered twice", name)
		}
		seen[name] = true
	}
}

// TestAnUnregisteredEventCannotBeLoggedSilently. Event holds an unexported field, so a
// caller outside this package cannot compose a name of their own — the only value they
// can forge is the zero one. That must be loud rather than an empty field that reads as
// "no event".
func TestAnUnregisteredEventCannotBeLoggedSilently(t *testing.T) {
	buf := capture(t, FormatJSON)
	Info(Event{}, "logged with a zero event")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := rec["event"]; got != unregisteredEvent {
		t.Errorf("event = %v, want %q", got, unregisteredEvent)
	}
}

// TestDeclaringABadEventNameFailsTheBoot. Both rules are enforced at package init rather
// than in review, so neither can reach production: a malformed name would break the
// shape alerts are written against, and two subsystems sharing one name would make an
// operator's alert fire for the wrong thing — the kind of defect that is discovered
// during the incident it was supposed to catch.
//
// Neither call registers anything, so the catalogue is unchanged by this test.
func TestDeclaringABadEventNameFailsTheBoot(t *testing.T) {
	for _, tc := range []struct{ name, event, want string }{
		{"malformed", "NotAnEventName", "malformed"},
		{"no subject", "published", "malformed"},
		{"duplicate", CheckpointPublished.String(), "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil {
					t.Fatalf("newEvent(%q) was accepted", tc.event)
				}
				if msg, ok := p.(string); !ok || !strings.Contains(msg, tc.want) {
					t.Errorf("panic = %v, want it to say %q", p, tc.want)
				}
			}()
			newEvent(tc.event)
		})
	}
}

// TestSetupIsRequiredBeforeLogging: a call before Setup must still reach stderr through
// slog's default handler rather than vanishing. Nothing asserts the destination here —
// only that it does not panic and does not lose the event.
func TestEventAttrIsAvailableWithoutSetup(t *testing.T) {
	if got := CheckpointPublished.Attr(); got.Key != "event" || got.Value.String() != "checkpoint.published" {
		t.Errorf("Attr() = %v, want event=checkpoint.published", got)
	}
	if got := CheckpointPublished.String(); got != "checkpoint.published" {
		t.Errorf("String() = %q, want checkpoint.published", got)
	}
}
