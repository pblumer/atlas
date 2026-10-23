package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The event log: the signals and messages that were sent, and where each arrived
// (ADR-0472).
//
// applyToState folds a row from every send, every reception, every start a send
// caused, and every message the buffer kept, handed over or let expire. This route
// reads those rows, newest first, from a snapshot off the run loop (ADR-0239), and
// hands each one through the rule that governs reading the instance it is about
// (ADR-0275): an operator or an administrator reads every row, a project member the
// rows of the definitions deployed from a project they may view, and a row that names
// no definition — a publish through the route, a watch, the buffer — only the first.
// A caller who may read nothing reads an empty log; the route is open to every
// signed-in caller because the rows, not the route, decide.

// defaultEventLogTTL is how long a row of the event log is kept when the operator set
// nothing.
const defaultEventLogTTL = 30 * 24 * time.Hour

const (
	// eventLogPage is how many rows a page holds when the caller names no limit, and
	// eventLogMaxPage the most it may name.
	eventLogPage    = 100
	eventLogMaxPage = 500
)

// eventLogScan is how many rows one page reads at most, so a narrow filter or a
// reader who may see few rows answers a short page with `more` instead of walking the
// whole table. A var so a test can reach the cut; nothing outside a test writes it.
var eventLogScan = 10_000

// WithEventLogRetention sets how long a row of the event log is kept
// (ADR-0472 §4); the default is 30 days.
func WithEventLogRetention(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.eventLogTTL = d
		}
	}
}

// pruneEventLog drops the event-log rows older than their retention, at most once an
// hour. It runs on the run loop, inside the retention sweep's turn, and enqueues a
// prune through the last row recorded before the cutoff, so the cut is a fact replay
// repeats — as the event feed's is.
func (s *Server) pruneEventLog(now int64) {
	if now-s.lastEventLogPrune < int64(time.Hour) {
		return
	}
	s.lastEventLogPrune = now
	ttl := s.eventLogTTL
	if ttl <= 0 {
		ttl = defaultEventLogTTL
	}
	part := s.proc.Partition()
	through, found, err := s.store.EventLogThroughBefore(part, now-int64(ttl))
	if err != nil || !found {
		return
	}
	if done, err := s.store.EventLogPrunedThrough(part); err != nil || through <= done {
		return
	}
	s.proc.PruneEventLog(through)
	_ = s.proc.RunUntilIdle()
}

// eventLogParty is an instance a row names: who sent, who received, or whom a
// directed delivery was addressed to. A reception names its sender by instance key
// alone, because the sender may be in a project the reader cannot see.
type eventLogParty struct {
	InstanceKey   uint64 `json:"instanceKey,omitempty"`
	DefinitionKey uint64 `json:"definitionKey,omitempty"`
	ProcessID     string `json:"processId,omitempty"`
	ProcessName   string `json:"processName,omitempty"`
	Version       int32  `json:"version,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
	ElementID     string `json:"elementId,omitempty"`
}

// eventLogRow is one row as the route answers it. Position and cause are decimal
// strings, like the feed's cursors; rows that share a cause were written while one
// command was processed — a send and what it reached.
type eventLogRow struct {
	Position       string         `json:"position"`
	Cause          string         `json:"cause"`
	At             string         `json:"at"`
	Kind           string         `json:"kind"`
	Channel        string         `json:"channel"`
	Name           string         `json:"name"`
	CorrelationKey string         `json:"correlationKey,omitempty"`
	Source         string         `json:"source,omitempty"`
	Sender         *eventLogParty `json:"sender,omitempty"`
	Receiver       *eventLogParty `json:"receiver,omitempty"`
	Target         *eventLogParty `json:"target,omitempty"`
	Reached        *uint32        `json:"reached,omitempty"`
	Started        *uint32        `json:"started,omitempty"`
	ExpiresAt      string         `json:"expiresAt,omitempty"`
	MessageID      string         `json:"messageId,omitempty"`
}

// eventLogPage is one page: its rows newest first, the cursor for older rows
// (`next`, the oldest position read), the cursor for newer ones (`latest`, the newest
// position read, or the caller's own), whether the read was cut (`more`), and how far
// the log was pruned.
type eventLogResp struct {
	Rows          []eventLogRow `json:"rows"`
	Next          string        `json:"next"`
	Latest        string        `json:"latest"`
	More          bool          `json:"more"`
	PrunedThrough string        `json:"prunedThrough"`
}

// eventLogDef is what a row needs of a deployed definition, copied on the loop.
type eventLogDef struct {
	processID, name, projectID string
	version                    int32
	cp                         *compiler.CompiledProcess
}

// eventLogFilter is what a caller narrowed the page to.
type eventLogFilter struct {
	channel  string
	name     string
	instance uint64
}

var errEventLogPageFull = errors.New("event log page full")

// handleEventLog serves GET /api/v1/event-log.
func (s *Server) handleEventLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cursor := func(name string) (uint64, bool) {
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			return 0, true
		}
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, name+" must be a cursor this log answered: a decimal position")
			return 0, false
		}
		return v, true
	}
	before, ok := cursor("before")
	if !ok {
		return
	}
	after, ok := cursor("after")
	if !ok {
		return
	}
	if before != 0 && after != 0 {
		httpapi.Error(w, http.StatusBadRequest, "name before or after, not both")
		return
	}
	limit := eventLogPage
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > eventLogMaxPage {
			httpapi.Error(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(eventLogMaxPage))
			return
		}
		limit = v
	}
	f := eventLogFilter{channel: strings.TrimSpace(q.Get("channel")), name: q.Get("name")}
	if f.channel != "" && f.channel != "signal" && f.channel != "message" {
		httpapi.Error(w, http.StatusBadRequest, "channel must be signal or message")
		return
	}
	if raw := strings.TrimSpace(q.Get("instance")); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "instance must be a process instance key")
			return
		}
		f.instance = v
	}

	pr := httpapi.PrincipalFrom(r.Context())
	var (
		view    *state.ReadView
		part    uint16
		defs    map[uint64]eventLogDef
		all     bool
		visible map[uint64]bool
	)
	s.do(func() {
		part = s.proc.Partition()
		view = s.store.ReadView()
		defs = make(map[uint64]eventLogDef, len(s.deployments))
		all = !s.authEnabled || (pr != nil && (pr.HasRole(RoleOperator) || pr.HasRole(RoleAdmin)))
		visible = map[uint64]bool{}
		grants := map[string]bool{} // per project, asked once however many versions it holds
		for key, d := range s.deployments {
			if d == nil {
				continue
			}
			defs[key] = eventLogDef{processID: d.ProcessID, name: d.Name, projectID: d.ProjectID, version: d.Version, cp: d.cp}
			if all || pr == nil || d.ProjectID == "" || !definitionReadable(pr, d) {
				continue
			}
			g, asked := grants[d.ProjectID]
			if !asked {
				g = s.instanceProjectGrantsView(pr, key)
				grants[d.ProjectID] = g
			}
			visible[key] = g
		}
	})
	if view == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "event log: the server is stopping")
		return
	}
	defer view.Close()

	pruned, err := view.EventLogPrunedThrough(part)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "event log: "+err.Error())
		return
	}
	resp := eventLogResp{Rows: []eventLogRow{}, PrunedThrough: strconv.FormatUint(pruned, 10)}
	var (
		scanned          int
		oldest, youngest uint64
	)
	take := func(e state.EventLogEntry) error {
		scanned++
		if oldest == 0 || e.Position < oldest {
			oldest = e.Position
		}
		if e.Position > youngest {
			youngest = e.Position
		}
		if (all || rowVisible(e, visible)) && f.matches(e) {
			resp.Rows = append(resp.Rows, renderEventLogRow(e, defs))
		}
		if len(resp.Rows) >= limit || scanned >= eventLogScan {
			return errEventLogPageFull
		}
		return nil
	}
	if after != 0 {
		err = view.EventLogAfter(part, after, take)
		slices.Reverse(resp.Rows) // read oldest first, answered newest first
	} else {
		err = view.EventLogBefore(part, before, take)
	}
	switch {
	case errors.Is(err, errEventLogPageFull):
		resp.More = true
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "event log: "+err.Error())
		return
	}
	resp.Next = strconv.FormatUint(oldest, 10)
	if oldest == 0 {
		resp.Next = strconv.FormatUint(before, 10)
	}
	resp.Latest = strconv.FormatUint(max(youngest, after), 10)
	httpapi.JSON(w, http.StatusOK, resp)
}

// rowDefinition is the definition a row is about, for the rule of who may read it: the
// sender's for a send, the addressed instance's for a directed delivery, the
// receiver's for a reception. Zero for a row that names none.
func rowDefinition(e state.EventLogEntry) uint64 {
	switch {
	case e.Dispatch != nil:
		if e.Dispatch.Source == model.DispatchDirected {
			return e.Dispatch.TargetProcessDefKey
		}
		return e.Dispatch.SenderProcessDefKey
	case e.Signal != nil:
		return e.Signal.ProcessDefKey
	case e.Flow != nil:
		return e.Flow.ReceiverProcessDefKey
	}
	return 0
}

// rowVisible reports whether a caller who is neither operator nor administrator may
// read a row: its definition is one of a project they may view.
func rowVisible(e state.EventLogEntry, visible map[uint64]bool) bool {
	def := rowDefinition(e)
	return def != 0 && visible[def]
}

// matches reports whether a row is one the caller narrowed the page to.
func (f eventLogFilter) matches(e state.EventLogEntry) bool {
	ch, name := rowChannelAndName(e)
	if f.channel != "" && f.channel != ch {
		return false
	}
	if f.name != "" && f.name != name {
		return false
	}
	if f.instance == 0 {
		return true
	}
	switch {
	case e.Dispatch != nil:
		return e.Dispatch.SenderProcessInstanceKey == f.instance || e.Dispatch.TargetProcessInstanceKey == f.instance
	case e.Signal != nil:
		return e.Signal.ProcessInstanceKey == f.instance || e.Signal.SenderProcessInstanceKey == f.instance
	case e.Flow != nil:
		return e.Flow.ReceiverProcessInstanceKey == f.instance || e.Flow.SenderProcessInstanceKey == f.instance
	}
	return false
}

func rowChannelAndName(e state.EventLogEntry) (string, string) {
	switch {
	case e.Dispatch != nil:
		if e.Dispatch.Channel == model.DispatchSignal {
			return "signal", e.Dispatch.Name
		}
		return "message", e.Dispatch.Name
	case e.Signal != nil:
		return "signal", e.Signal.SignalName
	case e.Flow != nil:
		return "message", e.Flow.MessageName
	case e.Message != nil:
		return "message", e.Message.MessageName
	}
	return "", ""
}

// party names an instance on a definition, with the element's BPMN id when the
// definition is still deployed.
func party(defs map[uint64]eventLogDef, instance, def uint64, element int32) *eventLogParty {
	p := &eventLogParty{InstanceKey: instance, DefinitionKey: def}
	if d, ok := defs[def]; ok {
		p.ProcessID, p.ProcessName, p.Version, p.ProjectID = d.processID, d.name, d.version, d.projectID
		if d.cp != nil {
			p.ElementID = d.cp.ElementBpmnId(element) // "" for an index the definition does not have
		}
	}
	return p
}

var dispatchSources = map[model.DispatchSource]string{
	model.DispatchFromProcess: "process",
	model.DispatchFromPublish: "publish",
	model.DispatchFromInbound: "inbound",
	model.DispatchDirected:    "directed",
}

// renderEventLogRow is a row as the route answers it.
func renderEventLogRow(e state.EventLogEntry, defs map[uint64]eventLogDef) eventLogRow {
	ch, name := rowChannelAndName(e)
	row := eventLogRow{
		Position: strconv.FormatUint(e.Position, 10), Cause: strconv.FormatUint(e.Cause, 10),
		At: time.Unix(0, e.At).UTC().Format(time.RFC3339Nano), Channel: ch, Name: name,
	}
	switch e.Kind {
	case state.EventLogDispatched:
		d := e.Dispatch
		row.Kind = "message.sent"
		if d.Channel == model.DispatchSignal {
			row.Kind = "signal.thrown"
		}
		row.CorrelationKey, row.Source = d.CorrelationKey, dispatchSources[d.Source]
		if d.Source == model.DispatchFromProcess {
			row.Sender = party(defs, d.SenderProcessInstanceKey, d.SenderProcessDefKey, d.SenderElementId)
		}
		if d.Source == model.DispatchDirected {
			row.Target = party(defs, d.TargetProcessInstanceKey, d.TargetProcessDefKey, -1)
		}
		reached, started := d.Reached, d.Started
		row.Reached, row.Started = &reached, &started
	case state.EventLogSignalReceived, state.EventLogSignalStarted:
		sig := e.Signal
		row.Kind = "signal.received"
		if e.Kind == state.EventLogSignalStarted {
			row.Kind = "signal.started"
		}
		row.Receiver = party(defs, sig.ProcessInstanceKey, sig.ProcessDefKey, sig.ElementId)
		if sig.SenderProcessInstanceKey != 0 {
			row.Sender = &eventLogParty{InstanceKey: sig.SenderProcessInstanceKey}
		}
	case state.EventLogMessageDelivered:
		fl := e.Flow
		row.Kind = "message.received"
		if fl.ReceiverProcessInstanceKey == 0 {
			row.Kind = "message.started"
		}
		row.CorrelationKey = fl.CorrelationKey
		row.Receiver = party(defs, fl.ReceiverProcessInstanceKey, fl.ReceiverProcessDefKey, fl.ReceiverElementId)
		if fl.SenderProcessInstanceKey != 0 {
			row.Sender = &eventLogParty{InstanceKey: fl.SenderProcessInstanceKey}
		}
	case state.EventLogMessageKept, state.EventLogMessageTaken, state.EventLogMessageExpired:
		m := e.Message
		row.Kind = map[state.EventLogKind]string{
			state.EventLogMessageKept: "message.kept", state.EventLogMessageTaken: "message.taken",
			state.EventLogMessageExpired: "message.expired",
		}[e.Kind]
		row.CorrelationKey, row.MessageID = m.CorrelationKey, m.MessageId
		if m.ExpiresAt != 0 {
			row.ExpiresAt = time.Unix(0, m.ExpiresAt).UTC().Format(time.RFC3339Nano)
		}
	}
	return row
}
