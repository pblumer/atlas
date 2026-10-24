package api

import (
	"log/slog"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/logging"
)

// Why a watch is not reading. The bridge used to know it and keep it to itself: a watch
// whose Worker had no mailbox, whose credential was refused, or whose source did not
// answer was skipped or retried every tick without a word, and from outside it looked
// exactly like a quiet source. The only trace was a mail that never started a process.
//
// The state lives in memory, like a feed subscription's hold (ADR-0433) and a breaker's
// (ADR-0340), and not in the watch's record. A failure is a fact about now, not
// configuration: a restart learns it again at the watch's next read, which for the
// slowest kind is a minute. Writing it into the record would put two fsyncs on the
// single writer for every change of a value nothing replays, which is the cost
// advanceInboundCursor and pollInbound go out of their way to avoid.

// maxInboundError bounds the reason kept for one watch. A provider can answer an
// HTML error page; the reason is a line for a person, not a transcript.
const maxInboundError = 1024

// inboundFailure is why one watch last failed to read, and since when it has been
// failing without a read in between, in unix seconds like the record's own times.
type inboundFailure struct {
	LastError    string
	FailingSince int64
}

// inboundHealth is every failing watch's failure, behind a lock: the bridge goroutine
// writes it off the run loop, and the listing reads it on the loop.
type inboundHealth struct {
	mu      sync.Mutex
	failing map[string]inboundFailure
}

func newInboundHealth() *inboundHealth {
	return &inboundHealth{failing: map[string]inboundFailure{}}
}

// failed records why a watch did not read and answers whether this began a streak,
// which is when it is worth a log line. The reason is always the newest; the start of
// the streak is kept.
func (h *inboundHealth) failed(id string, now time.Time, why string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, was := h.failing[id]
	if !was {
		f.FailingSince = now.Unix()
	}
	f.LastError = clipInboundError(why)
	h.failing[id] = f
	return !was
}

// succeeded ends a watch's streak and answers whether it had one.
func (h *inboundHealth) succeeded(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, was := h.failing[id]
	delete(h.failing, id)
	return was
}

// failure answers a watch's failure and whether it is failing.
func (h *inboundHealth) failure(id string) (inboundFailure, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.failing[id]
	return f, ok
}

// retain forgets every watch that is not in live: one deleted, or switched off, reads
// nothing on purpose, and a failure it had before is no longer news.
func (h *inboundHealth) retain(live map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.failing {
		if !live[id] {
			delete(h.failing, id)
		}
	}
}

// clipInboundError bounds a reason on a rune boundary and says that it was cut, so a
// shortened error is never read as the whole one.
func clipInboundError(why string) string {
	if len(why) <= maxInboundError {
		return why
	}
	cut := maxInboundError
	for cut > 0 && !utf8.RuneStart(why[cut]) {
		cut--
	}
	return why[:cut] + "…"
}

// noteInboundFailure records that a watch could not read, and logs it once per streak:
// a reason repeated every tick would bury the line that matters.
func (s *Server) noteInboundFailure(rec inboundSubscription, why string) {
	if s.inboundHealth.failed(rec.ID, s.inboundNow(), why) {
		logging.Warn(logging.InboundWatchFailing, "an inbound watch cannot read its source: "+clipInboundError(why),
			slog.String("subscription", rec.ID), slog.String("worker", rec.ConnectorID),
			slog.String("messageName", rec.MessageName))
	}
}

// noteInboundRead records that a watch read its source, ending a streak it was in.
func (s *Server) noteInboundRead(rec inboundSubscription) {
	if s.inboundHealth.succeeded(rec.ID) {
		logging.Info(logging.InboundWatchRecovered, "an inbound watch reads its source again",
			slog.String("subscription", rec.ID), slog.String("worker", rec.ConnectorID),
			slog.String("messageName", rec.MessageName))
	}
}

// inboundSubView is a watch as the listing answers it: the record and, while it cannot
// read its source, why and since when.
type inboundSubView struct {
	inboundSubscription
	LastError    string `json:"lastError,omitempty"`
	FailingSince int64  `json:"failingSince,omitempty"`
}

// viewInboundSub joins a watch's record with its failure. Safe on and off the run loop.
func (s *Server) viewInboundSub(sub inboundSubscription) inboundSubView {
	v := inboundSubView{inboundSubscription: sub}
	if f, failing := s.inboundHealth.failure(sub.ID); failing {
		v.LastError, v.FailingSince = f.LastError, f.FailingSince
	}
	return v
}

// inboundClientMissing says why a watch's Worker has no client to read with: the reason
// its registry gave when it could not build one, which is what the Worker's own row
// shows, or — for a Worker the registry has not heard of — that it has none. It runs on
// the run loop, which owns the registries (invariant I3).
func (s *Server) inboundClientMissing(c connector) string {
	if why := s.connectorProblem(c.Kind, c.Name); why != "" {
		return "its Worker " + strconv.Quote(c.Name) + " cannot be used: " + why
	}
	return "its Worker " + strconv.Quote(c.Name) + " has no client on this server"
}
