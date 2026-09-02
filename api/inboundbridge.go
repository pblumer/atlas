package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// defaultInboundBatch caps how many clio events a single poll of one subscription
// reads and republishes as Atlas messages (ADR-0075). It exists so a watch pointed
// at a subject with a large backlog cannot hand the single-writer run loop one
// unbounded publish storm — every matching event starts a process, so an
// N-event backlog is N instances in one batch without this bound. With the cap a
// backlog drains as bounded catch-up: each tick advances the resume cursor by at
// most this many events and the next tick continues. Overridable per server with
// WithInboundBatchLimit (tests use a tiny value).
const defaultInboundBatch = 256

// inboundBridge polls the configured clio inbound subscriptions and republishes new
// clio events as Atlas messages, so an external event both starts message-start
// processes and wakes waiting message-catch instances (ADR-0075). It mirrors the
// timer scheduler: a ticker goroutine that does its network I/O off the run loop and
// hands only the resulting publish onto it via s.do (invariant I3). Stopped by the
// shared quit channel.
func (s *Server) inboundBridge(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			s.pollInbound(context.Background())
		}
	}
}

// pendingSub is one enabled subscription resolved for a poll: its record, the reader
// for its connector's kind, that kind's name (which composes the engine's source id),
// and its compiled correlation-key expression (nil when the subscription is keyless).
type pendingSub struct {
	rec    inboundSubscription
	kind   string
	source inboundSource
	key    *expr.Compiled
}

// pollInbound runs one poll cycle. It resolves the enabled subscriptions and their
// clients on the run loop (a read), then for each reads new clio events OFF the run
// loop (the only network call), computes each event's correlation key and payload
// off the loop, and hands a single publish batch per subscription back onto the run
// loop. The publish is made durable (Drive → fsync) before the best-effort resume
// cursor advances; a crash in that window re-reads on restart, where the engine's
// durable high-water mark dedupes the replay (ADR-0075).
func (s *Server) pollInbound(ctx context.Context) {
	var subs []pendingSub
	s.do(func() { subs = s.resolveInboundSubs() })

	now := s.inboundNow()
	for _, sb := range subs {
		if !inboundDue(sb.rec, now) {
			continue // this watch has its own cadence and it has not elapsed
		}
		subID := sb.rec.ID
		s.do(func() { s.markInboundPolled(subID, now.Unix()) })
		if sb.rec.StartFromTip && !sb.rec.Primed {
			s.primeInbound(ctx, sb) // skip the backlog to the tip, publishing nothing
			continue
		}
		events, cursor, err := sb.source.Read(ctx, sb.rec, s.inboundBatch)
		if err != nil || len(events) == 0 {
			continue // transient read failure or nothing new; retry next tick
		}
		// Compute correlation keys and payloads off the run loop.
		type pub struct {
			sourceID string
			seq      uint64
			key      string
			vars     []model.VariableValue
		}
		pubs := make([]pub, len(events))
		for i, ev := range events {
			pubs[i] = pub{
				sourceID: inboundSourceID(sb.kind, sb.rec, ev.MarkKey),
				seq:      ev.Seq,
				key:      correlationKeyOf(sb.key, ev.Fields),
				vars:     eventVars(ev.Fields),
			}
		}
		name := sb.rec.MessageName
		published := false
		s.do(func() {
			// The ceiling is charged in the same run-loop hop as the publish, so a watch
			// cannot be read as inside its budget and publish outside it.
			if !s.chargeInboundBudget(subID, len(pubs), now) {
				return
			}
			for _, p := range pubs {
				s.proc.PublishInbound(p.sourceID, p.seq, name, p.key, p.vars...)
			}
			published = true
		})
		if !published {
			continue // over the ceiling: the watch is off and the cursor stays put
		}
		// A correlated message can start or advance an instance straight into a
		// connector task, so the driving happens off the run loop (ADR-0157 step 6).
		// The cursor only advances once that succeeded; otherwise it is left where it
		// was and the events are re-read next tick, which the engine dedupes.
		if err := s.drive(); err != nil {
			continue
		}
		if cursor != "" {
			s.do(func() { s.advanceInboundCursor(subID, cursor) })
		}
	}
}

// inboundNow is the clock the bridge paces watches by, injectable so a test does not
// have to wait out a cadence.
func (s *Server) inboundNow() time.Time {
	if s.inboundClock != nil {
		return s.inboundClock()
	}
	return time.Now()
}

// inboundDue reports whether a watch's own cadence has elapsed. A watch with no cadence
// of its own is read on every tick, which is what every clio subscription did before
// this existed and still does.
//
// The cadence is per watch rather than per bridge because the two sources want opposite
// things from it: a clio read is local and cheap, and a two-second tick is the latency
// budget it was chosen for; a Jira site rate-limits per site, and spending that budget
// on empty answers every two seconds is not what it is for.
func inboundDue(rec inboundSubscription, now time.Time) bool {
	if rec.PollSeconds <= 0 {
		return true
	}
	return now.Unix()-rec.LastPolledAt >= int64(rec.PollSeconds)
}

// markInboundPolled records when a watch was last read, so its cadence advances even
// when the read fails or finds nothing. It runs on the run loop (the store's owner) and
// a save failure is ignored: the value only paces, and losing it re-reads early.
func (s *Server) markInboundPolled(subID string, at int64) {
	rec, ok, err := s.inboundSubs.Get(subID)
	if err != nil || !ok {
		return
	}
	rec.LastPolledAt = at
	_ = s.inboundSubs.Save(rec)
}

// defaultInboundPerHour is the ceiling a watch gets when it names none: how many events
// it may publish in one hour before the guard switches it off.
//
// Sixty is one a minute sustained, which is generous for a watch whose every event
// starts a process and cheap to raise per watch when a project really is that busy. The
// number it is protecting against is not close: the reported Jira loop published one
// every ten seconds and would have run until somebody noticed.
const defaultInboundPerHour = 60

// inboundBudgetWindow is the span the ceiling is counted over. An hour is long enough
// that a burst of real work does not trip it and short enough that a watch switched off
// yesterday is not still paying for it.
const inboundBudgetWindow = time.Hour

// inboundBudget is a watch's ceiling: its own, or the default when it names none.
func inboundBudget(rec inboundSubscription) int {
	if rec.MaxPerHour > 0 {
		return rec.MaxPerHour
	}
	return defaultInboundPerHour
}

// chargeInboundBudget books n events against a watch's hourly ceiling and reports
// whether they may be published.
//
// A watch can feed itself: a process started by an event writes to the system the watch
// reads, the watch matches what it wrote, and the loop has no natural end. That is not
// a hypothetical — a Jira watch published jira.ticket.created, the started instance
// created a Jira issue, the watch matched it, and it ran until the watch was deleted by
// hand (ADR-draft-inbound-watch-budget). The engine fix for that particular loop
// (ADR-draft-start-events-are-triggers) closed the shape it took; this closes the class,
// including the one two processes can build between them, where no single model is
// wrong.
//
// A batch that would cross the ceiling is refused whole and the watch is switched off,
// rather than published up to the line. Publishing part of a batch would advance nothing
// an operator can reason about, and a runaway that is merely throttled is still a
// runaway. The cursor does not advance either, so re-enabling re-reads what was refused.
//
// It runs on the run-loop goroutine, which owns the store (invariant I3).
func (s *Server) chargeInboundBudget(subID string, n int, now time.Time) bool {
	rec, ok, err := s.inboundSubs.Get(subID)
	if err != nil || !ok {
		return false
	}
	if now.Unix()-rec.WindowStart >= int64(inboundBudgetWindow.Seconds()) {
		rec.WindowStart = now.Unix()
		rec.PublishedInWindow = 0
	}
	ceiling := inboundBudget(rec)
	if rec.PublishedInWindow+n > ceiling {
		rec.Enabled = false
		rec.DisabledReason = fmt.Sprintf(
			"switched off by the loop guard: this watch tried to publish more than %d events in an hour. "+
				"That is usually a watch matching what its own processes write. Check the query, then raise "+
				"the hourly limit and enable it again.", ceiling)
		_ = s.inboundSubs.Save(rec)
		return false
	}
	rec.PublishedInWindow += n
	_ = s.inboundSubs.Save(rec)
	return true
}

// inboundPrimeBatch is the page size the priming path reads while skipping a
// forward-only subscription's backlog. It is larger than the live cap because
// priming publishes nothing (no run-loop work per event), so a big page just
// advances the cursor toward the tip faster; a backlog larger than one page is
// primed across several polls, one page each.
const inboundPrimeBatch = 4096

// primeInbound advances a forward-only (StartFromTip) subscription's resume cursor
// past the subject's existing backlog WITHOUT republishing it, so enabling a watch
// on a subject that already has history does not start a process per historical
// event (the reported /employees flood, ADR-0075). It reads one bounded page off the
// run loop; a short page means the tip is reached and the subscription is marked
// primed, after which pollInbound publishes new events normally. Publishing nothing
// here means a lost cursor update is harmless — a re-prime simply skips again.
func (s *Server) primeInbound(ctx context.Context, sb pendingSub) {
	cursor, done, err := sb.source.Prime(ctx, sb.rec)
	if err != nil {
		return // transient read failure; retry next tick
	}
	subID := sb.rec.ID
	s.do(func() { s.markInboundPrimed(subID, cursor, done) })
}

// markInboundPrimed persists one priming step: it advances the resume cursor to the
// last skipped event (when the page carried any) and sets Primed once the backlog is
// exhausted. It runs on the run loop (the store's owner).
func (s *Server) markInboundPrimed(subID, lastEventID string, primed bool) {
	rec, ok, err := s.inboundSubs.Get(subID)
	if err != nil || !ok {
		return
	}
	if lastEventID != "" {
		rec.LastEventID = lastEventID
	}
	if primed {
		rec.Primed = true
	}
	_ = s.inboundSubs.Save(rec)
}

// resolveInboundSubs loads the enabled subscriptions whose connector is an enabled
// clio connector with a live client, compiling each correlation key. It reads the
// stores, so it runs on the run-loop goroutine.
func (s *Server) resolveInboundSubs() []pendingSub {
	recs, err := s.inboundSubs.LoadAll()
	if err != nil {
		return nil
	}
	conns, err := s.connectors.LoadAll()
	if err != nil {
		return nil
	}
	byID := make(map[string]connector, len(conns))
	for _, c := range conns {
		byID[c.ID] = c
	}
	var out []pendingSub
	for _, r := range recs {
		if !r.Enabled {
			continue
		}
		c, ok := byID[r.ConnectorID]
		if !ok || !c.Enabled {
			continue
		}
		// The connector's kind is the discriminator: a watch record carries no kind of
		// its own, so a clio subscription written before jira watches existed needs no
		// migration to keep meaning what it meant (ADR-0214).
		var src inboundSource
		switch c.Kind {
		case connectorKindClio:
			client, ok := s.clioRegistry.Client(c.Name)
			if !ok {
				continue
			}
			src = clioSource{client: client}
		case connectorKindJira:
			client, ok := s.jiraRegistry.Client(c.Name)
			if !ok {
				continue
			}
			src = jiraSource{client: client, now: s.inboundNow}
		default:
			continue // a kind with no inbound half; its connector is outbound only
		}
		var compiled *expr.Compiled
		if strings.TrimSpace(r.CorrelationKey) != "" {
			// Compiled once here rather than per event; the config endpoint already
			// rejected an uncompilable key, so an error here is not expected.
			if compiled, err = expr.CompileAuto(r.CorrelationKey); err != nil {
				continue
			}
		}
		out = append(out, pendingSub{rec: r, kind: c.Kind, source: src, key: compiled})
	}
	return out
}

// advanceInboundCursor persists a subscription's best-effort resume cursor. It runs
// on the run loop (the store's owner). A save failure is ignored: the cursor is only
// an optimization — the engine high-water mark, not this cursor, guarantees no
// duplicate delivery (ADR-0075).
func (s *Server) advanceInboundCursor(subID, lastEventID string) {
	rec, ok, err := s.inboundSubs.Get(subID)
	if err != nil || !ok {
		return
	}
	rec.LastEventID = lastEventID
	_ = s.inboundSubs.Save(rec)
}

// inboundSourceID is the opaque key the engine deduplicates on. The engine never
// interprets it (ADR-0075); what varies is how wide a scope one key covers.
//
// A clio watch keeps one key for the whole subject — byte-identical to what it has
// always been, which is not cosmetic: reshaping that string would reset every existing
// watch's high-water mark and replay its backlog as new process starts.
//
// A source that hands over an event-level mark key gets one key per *event subject*
// instead — for Jira, per issue. That is what lets a source whose order is a query's be
// correct: two issues never share a mark, so no delivery order can make one suppress
// the other, and the cursor is then free to lag and re-read (ADR-0214).
func inboundSourceID(kind string, r inboundSubscription, markKey string) string {
	if markKey == "" {
		return kind + ":" + r.ConnectorID + ":" + r.WatchedSubject
	}
	return kind + ":" + r.ConnectorID + ":" + r.ID + ":" + markKey
}

// correlationKeyOf evaluates a subscription's compiled correlation key over an event's
// fields, returning the key as a string. A nil expression (keyless subscription) yields
// ""; a failed evaluation (e.g. a missing field) also yields "" so the message publishes
// keyless rather than being dropped.
func correlationKeyOf(compiled *expr.Compiled, fields map[string]any) string {
	if compiled == nil {
		return ""
	}
	binds := make(map[string]expr.Value, len(compiled.Inputs()))
	for _, name := range compiled.Inputs() {
		if v, ok := fields[name]; ok {
			binds[name] = expr.FromJSON(v)
		}
	}
	v, err := compiled.Eval(binds)
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// eventVars turns an event's fields into the payload variables carried into the
// woken/started instances, canonicalized through expr so each round-trips on replay
// exactly like any other variable. The envelope fields each source adds (subject and
// subjectTail for clio, issueKey and the issue itself for Jira) are what let a process
// read what the event was about and derive keys from it — e.g. a message-start
// correlation key of subjectTail, or of issueKey.
func eventVars(fields map[string]any) []model.VariableValue {
	out := make([]model.VariableValue, 0, len(fields))
	for name, raw := range fields {
		kind, b, text := expr.Classify(expr.FromJSON(raw))
		out = append(out, model.VariableValue{Name: name, Kind: inboundVarKind(kind), Bool: b, Text: text})
	}
	return out
}

// inboundVarKind maps an expr value kind to the stored variable kind (mirrors the
// connector workers' mapping so the enums evolve independently).
func inboundVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}
