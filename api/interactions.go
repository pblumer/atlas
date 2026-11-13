package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/interaction"
	"github.com/pblumer/atlas/state"
)

// The interaction graph of ADR-0559: around one process, who exchanges which message
// names with it — what the deployed definitions make possible, and what the event log
// shows happened in a window. The graph is computed by api/interaction; this file
// gathers its inputs under the rules every other door to the same facts already
// applies:
//
//   - the definitions are the ones readableDefinitions copies, as message-sources and
//     the message overview read them (ADR-0446);
//   - the inbound watches and product actions are message-sources' own rows, under its
//     filters, and only for a caller who may read message-sources (a modeller);
//   - the event-log rows are the ones the event log shows the caller (ADR-0472 §5),
//     through eventLogReach.
//
// The loop turn is bounded by design-time size: copying the definitions and taking a
// read view. The scan, the only part that grows with the traffic, runs off the loop on
// that view and is bounded by interactionScan (ADR-0559 §4).

// interactionScan is how many event-log rows one graph reads at most. A var so a test
// can reach the cut; nothing outside a test writes it.
var interactionScan = 100_000

// interactionGraph is the route's answer: the graph and how long this server keeps the
// event log, so a client can say where its history begins.
type interactionGraph struct {
	interaction.Graph
	Retention string `json:"retention"`
}

// handleProcessInteractions serves GET /api/v1/process-interactions.
func (s *Server) handleProcessInteractions(w http.ResponseWriter, r *http.Request) {
	in, view, part, retention, ok := s.interactionInput(w, r)
	if !ok {
		return
	}
	defer view.Close()
	in.Scan = func(fn func(state.EventLogEntry) error) error { return view.EventLogBefore(part, 0, fn) }
	g, err := interaction.Build(in)
	if !interactionAnswered(w, in.ProcessID, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, interactionGraph{Graph: g, Retention: retention.String()})
}

// The replay's page sizes (ADR-0559 §8): how many events a page holds when the caller
// names no limit, and the most it may name.
const (
	interactionReplayPage    = 500
	interactionReplayMaxPage = 2000
)

// handleProcessInteractionReplay serves GET /api/v1/process-interactions/replay: the
// graph's arrivals one by one, oldest first, a page at a time. `before` reads the
// history backwards from a page's `next`, `after` what came since its `latest`; with
// neither, the newest page of the window.
func (s *Server) handleProcessInteractionReplay(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cursor := func(name string) (uint64, bool) {
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			return 0, true
		}
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, name+" must be a cursor the replay answered: a decimal position")
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
	limit := interactionReplayPage
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > interactionReplayMaxPage {
			httpapi.Error(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(interactionReplayMaxPage))
			return
		}
		limit = v
	}
	in, view, part, _, ok := s.interactionInput(w, r)
	if !ok {
		return
	}
	defer view.Close()
	rin := interaction.ReplayInput{Input: in, Limit: limit, After: after}
	if after != 0 {
		rin.Direction = interaction.Newer
		rin.Scan = func(fn func(state.EventLogEntry) error) error { return view.EventLogAfter(part, after, fn) }
	} else {
		rin.Scan = func(fn func(state.EventLogEntry) error) error { return view.EventLogBefore(part, before, fn) }
	}
	page, err := interaction.Replay(rin)
	if !interactionAnswered(w, in.ProcessID, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, page)
}

// interactionInput reads the query both routes share — processId and window — and
// gathers what the graph reads under the rules the other doors to the same facts
// apply. It answers the request itself when it cannot proceed. The view it returns is
// open and the caller closes it; the scan is the caller's to choose.
func (s *Server) interactionInput(w http.ResponseWriter, r *http.Request) (interaction.Input, *state.ReadView, uint16, time.Duration, bool) {
	q := r.URL.Query()
	processID := strings.TrimSpace(q.Get("processId"))
	if processID == "" {
		httpapi.Error(w, http.StatusBadRequest, "processId is required: the process to compute the graph around")
		return interaction.Input{}, nil, 0, 0, false
	}
	retention := s.eventLogTTL
	if retention <= 0 {
		retention = defaultEventLogTTL
	}
	window := retention
	if raw := strings.TrimSpace(q.Get("window")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "window must be a positive duration, such as 24h")
			return interaction.Input{}, nil, 0, 0, false
		}
		window = d
	}

	pr := httpapi.PrincipalFrom(r.Context())
	var sources []interaction.Source
	if !s.authEnabled || pr == nil || mayReach(pr, RoleModeler) {
		rows, err := s.messageSources(r)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "process interactions: "+err.Error())
			return interaction.Input{}, nil, 0, 0, false
		}
		sources = interactionSources(rows)
	}

	var (
		view    *state.ReadView
		part    uint16
		defs    []deployedDefinition
		all     bool
		visible map[uint64]bool
	)
	s.do(func() {
		part = s.proc.Partition()
		view = s.store.ReadView()
		defs = s.readableDefinitions(pr)
		all, visible = s.eventLogReach(pr)
	})
	if view == nil {
		httpapi.Error(w, http.StatusServiceUnavailable, "process interactions: the server is stopping")
		return interaction.Input{}, nil, 0, 0, false
	}
	now := s.now()
	return interaction.Input{
		ProcessID:   processID,
		Definitions: interactionDefinitions(defs),
		Sources:     sources,
		ReadsAll:    all,
		Visible:     func(key uint64) bool { return visible[key] },
		Now:         now,
		From:        now - int64(window),
		ScanLimit:   interactionScan,
	}, view, part, retention, true
}

// interactionAnswered answers an error of the graph or the replay, and reports
// whether there was none.
func interactionAnswered(w http.ResponseWriter, processID string, err error) bool {
	switch {
	case errors.Is(err, interaction.ErrUnknownProcess):
		httpapi.Error(w, http.StatusNotFound, "no deployed process "+processID)
		return false
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "process interactions: "+err.Error())
		return false
	}
	return true
}

// interactionDefinitions is the definitions as the graph reads them, with their
// message points. The compiled processes are immutable, so this runs off the loop.
func interactionDefinitions(defs []deployedDefinition) []interaction.Definition {
	out := make([]interaction.Definition, 0, len(defs))
	for _, d := range defs {
		def := interaction.Definition{
			Key: d.key, ProcessID: d.processID, Name: d.name, Version: d.version, ProjectID: d.projectID,
			System: d.system, Inactive: d.inactive, Newest: d.newest,
		}
		for _, mp := range d.cp.MessageReceivers() {
			def.Points = append(def.Points, interaction.Point{Element: mp.Element, Message: mp.MessageName, Role: string(mp.Role)})
		}
		for _, mp := range d.cp.MessageSenders() {
			def.Points = append(def.Points, interaction.Point{Element: mp.Element, Message: mp.MessageName, Role: string(mp.Role)})
		}
		out = append(out, def)
	}
	return out
}

// interactionSources is message-sources' watches and product actions, the senders
// that are not processes.
func interactionSources(rows []messageSourceView) []interaction.Source {
	var out []interaction.Source
	for _, v := range rows {
		switch v.SourceKind {
		case sourceInboundWatch:
			out = append(out, interaction.Source{Kind: interaction.KindInboundWatch, Message: v.MessageName, Enabled: v.Enabled,
				ConnectorID: v.ConnectorID, ConnectorName: v.ConnectorName, WorkerKind: v.Kind})
		case sourceProductAction:
			out = append(out, interaction.Source{Kind: interaction.KindProductAction, Message: v.MessageName, Enabled: v.Enabled,
				ProductID: v.ProductID, ProductName: v.ProductName, Action: v.Action})
		}
	}
	return out
}
