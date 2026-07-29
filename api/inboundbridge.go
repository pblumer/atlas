package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/clio"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

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

// pendingSub is one enabled subscription resolved for a poll: its record, the clio
// client for its connector, and its compiled correlation-key expression (nil when
// the subscription is keyless).
type pendingSub struct {
	rec    inboundSubscription
	client clio.Client
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

	for _, sb := range subs {
		events, err := sb.client.ReadEvents(ctx, clio.ReadEventsRequest{
			Subject:   sb.rec.WatchedSubject,
			AfterID:   sb.rec.LastEventID,
			Recursive: sb.rec.Recursive,
		})
		if err != nil || len(events) == 0 {
			continue // transient read failure or nothing new; retry next tick
		}
		// Compute correlation keys and payloads off the run loop.
		type pub struct {
			seq  uint64
			key  string
			vars []model.VariableValue
		}
		pubs := make([]pub, len(events))
		for i, ev := range events {
			pubs[i] = pub{seq: inboundSeq(ev.ID), key: correlationKeyOf(sb.key, ev), vars: eventVars(ev)}
		}
		sourceID := inboundSourceID(sb.rec)
		lastID := events[len(events)-1].ID
		name := sb.rec.MessageName
		subID := sb.rec.ID
		s.do(func() {
			for _, p := range pubs {
				s.proc.PublishInbound(sourceID, p.seq, name, p.key, p.vars...)
			}
			if err := s.jobRunner.Drive(); err != nil {
				return // leave the cursor un-advanced; re-read next tick (engine dedupes)
			}
			s.advanceInboundCursor(subID, lastID)
		})
	}
}

// resolveInboundSubs loads the enabled subscriptions whose connector is an enabled
// clio connector with a live client, compiling each correlation key. It reads the
// stores, so it runs on the run-loop goroutine.
func (s *Server) resolveInboundSubs() []pendingSub {
	recs, err := s.inboundSubs.loadAll()
	if err != nil {
		return nil
	}
	conns, err := s.connectors.loadAll()
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
		if !ok || !c.Enabled || c.Kind != connectorKindClio {
			continue
		}
		client, ok := s.clioRegistry.Client(c.Name)
		if !ok {
			continue
		}
		var compiled *expr.Compiled
		if strings.TrimSpace(r.CorrelationKey) != "" {
			// Compiled once here rather than per event; the config endpoint already
			// rejected an uncompilable key, so an error here is not expected.
			if compiled, err = expr.CompileAuto(r.CorrelationKey); err != nil {
				continue
			}
		}
		out = append(out, pendingSub{rec: r, client: client, key: compiled})
	}
	return out
}

// advanceInboundCursor persists a subscription's best-effort resume cursor. It runs
// on the run loop (the store's owner). A save failure is ignored: the cursor is only
// an optimization — the engine high-water mark, not this cursor, guarantees no
// duplicate delivery (ADR-0075).
func (s *Server) advanceInboundCursor(subID, lastEventID string) {
	rec, ok, err := s.inboundSubs.get(subID)
	if err != nil || !ok {
		return
	}
	rec.LastEventID = lastEventID
	_ = s.inboundSubs.save(rec)
}

// inboundSourceID is the opaque per-source key the engine deduplicates on: a clio
// connector plus the watched subject. The engine never interprets it (ADR-0075).
func inboundSourceID(r inboundSubscription) string {
	return "clio:" + r.ConnectorID + ":" + r.WatchedSubject
}

// inboundSeq parses a clio event id — a per-partition monotonic sequence rendered
// as a decimal string — into the uint64 the engine deduplicates on (ADR-0075).
// clio events carry no separate `seq` field; the id itself is the order. A
// non-numeric id (not a real clio event) yields 0, which the engine treats as
// already-applied and skips, so a garbled line can never double-start a process.
func inboundSeq(id string) uint64 {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// eventFields is the binding environment a clio event exposes to correlation-key
// expressions and as seeded process variables: the event body, plus four reserved
// envelope fields the body cannot see on its own. subjectTail is the last
// '/'-segment of the subject — "E-123456" for "/employees/E-123456" — so a watch
// on a parent subject can key on the child id. The envelope fields take precedence
// over a body field of the same name, so a subscription can always rely on them.
func eventFields(ev clio.InboundEvent) map[string]any {
	fields := make(map[string]any, len(ev.Data)+4)
	for k, v := range ev.Data {
		fields[k] = v
	}
	fields["subject"] = ev.Subject
	fields["subjectTail"] = subjectTail(ev.Subject)
	fields["eventType"] = ev.Type
	fields["eventId"] = ev.ID
	return fields
}

// subjectTail returns the last '/'-separated segment of a clio subject (trailing
// slashes ignored): the leaf id an event is scoped to.
func subjectTail(subject string) string {
	trimmed := strings.TrimRight(subject, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// correlationKeyOf evaluates a subscription's compiled correlation key over a clio
// event's fields (body plus reserved envelope fields), returning the key as a
// string. A nil expression (keyless subscription) yields ""; a failed evaluation
// (e.g. a missing field) also yields "" so the message publishes keyless rather
// than being dropped.
func correlationKeyOf(compiled *expr.Compiled, ev clio.InboundEvent) string {
	if compiled == nil {
		return ""
	}
	fields := eventFields(ev)
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

// eventVars turns a clio event's fields (body plus reserved envelope fields) into
// the payload variables carried into the woken/started instances, canonicalized
// through expr so each round-trips on replay exactly like any other variable. The
// envelope fields (subject, subjectTail, eventType, eventId) let a process read the
// event's subject and derive keys from it — e.g. a message-start correlation key of
// subjectTail.
func eventVars(ev clio.InboundEvent) []model.VariableValue {
	fields := eventFields(ev)
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
