// Package logging gives Atlas's operational logs a stable contract (ADR-0142).
//
// Every line Atlas wrote used to be a prose sentence with values interpolated into it:
//
//	log.Printf("checkpoint: published at log position %d (recovery replays only past it)", pos)
//
// An operator can read that. Nothing can *alert* on it, except by matching a regular
// expression against wording that changes the moment someone rewords the sentence, and
// nothing can chart the position without parsing it back out of English.
//
// The fix is not to delete the prose. "will retry next tick" is real guidance that a bare
// event name loses, and the console is a first-class audience — an operator watching
// `atlas serve` should not be handed JSON. So each line now carries three things:
//
//   - an **event name**, the stable identifier an alert matches on;
//   - the **sentence**, unchanged in spirit, as the human explanation;
//   - the **values**, moved out of the sentence into typed attributes.
//
// Two properties are structural rather than conventional. Event is a struct with an
// unexported field, so a caller outside this package cannot invent a name — only the
// constants declared here exist, and a duplicate or malformed one panics at init rather
// than reaching production. And the standard logger ends up pointed at the same handler,
// so a line from a dependency arrives in one stream and one format instead of alongside
// it — see Setup for why that costs no code.
//
// It is built on log/slog, which is to say on nothing: no dependency is added (ADR-0010),
// and the engine still does not log at all, so the single writer's hot path is untouched
// (invariants I1 and I3).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// Format is how records are rendered.
type Format string

const (
	// FormatText is logfmt-style key=value output: what a person reads in a terminal.
	FormatText Format = "text"
	// FormatJSON is one JSON object per line: what a log shipper ingests without a
	// parsing rule of its own.
	FormatJSON Format = "json"
)

// DefaultFormat is text. The console audience is the one Atlas has always had, and a
// default that turns their terminal into JSON would be a regression dressed as progress;
// JSON is one flag away for the deployment that wants it.
const DefaultFormat = FormatText

// eventKey is the attribute the event name is carried under. Fixed here so it cannot
// drift per call site — it is the field alerts are written against.
const eventKey = "event"

// unregisteredEvent is what a zero Event logs as. A caller outside this package cannot
// construct a name, but they can pass Event{}; reporting that as an empty field would
// read as "this line has no event", which is a different and wrong statement.
const unregisteredEvent = "unregistered"

// Setup points the default logger at w in the given format. Everything the process
// emits — including lines from dependencies that log through the standard library —
// then arrives as one stream in one shape.
//
// w is the caller's to compose: the server tees stderr into the bounded buffer behind
// GET /api/v1/logs, and that keeps working because this writes to the same place.
func Setup(w io.Writer, f Format) error {
	var h slog.Handler
	switch f {
	case FormatText:
		h = slog.NewTextHandler(w, nil)
	case FormatJSON:
		h = slog.NewJSONHandler(w, nil)
	default:
		return fmt.Errorf("logging: unknown format %q (want %q or %q)", f, FormatText, FormatJSON)
	}
	// SetDefault does more than name a default logger: it also points the standard
	// library's logger at this handler and clears its flags. So a dependency that logs
	// through log.Printf arrives as a record like any other, in one stream and one
	// format, and without the second timestamp its own prefix would add. That is worth
	// stating because it is not obvious, and because it is the reason no explicit
	// log.SetOutput is needed here — an earlier draft of this function had one, and the
	// test that was supposed to prove it necessary passed without it.
	slog.SetDefault(slog.New(h))
	return nil
}

// Info records routine progress: work that got done.
func Info(e Event, msg string, attrs ...slog.Attr) { emit(slog.LevelInfo, e, msg, attrs) }

// Warn records something an operator should look at that the server is handling on its
// own — a tick that failed and will be retried, a configuration that will not do what it
// looks like it does.
func Warn(e Event, msg string, attrs ...slog.Attr) { emit(slog.LevelWarn, e, msg, attrs) }

// Error records something that failed and is not being retried.
func Error(e Event, msg string, attrs ...slog.Attr) { emit(slog.LevelError, e, msg, attrs) }

// emit puts the event name first so it reads before the payload in text output. The
// slice it builds is one allocation per line on paths that run once a tick or once at
// startup; the engine's batch loop does not log at all, so invariant I1 is not in play
// here (see the package comment).
func emit(level slog.Level, e Event, msg string, attrs []slog.Attr) {
	slog.Default().LogAttrs(context.Background(), level, msg, append([]slog.Attr{e.Attr()}, attrs...)...)
}
