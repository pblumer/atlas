package api

import (
	"errors"
	"net/http"
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
	q := r.URL.Query()
	processID := strings.TrimSpace(q.Get("processId"))
	if processID == "" {
		httpapi.Error(w, http.StatusBadRequest, "processId is required: the process to compute the graph around")
		return
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
			return
		}
		window = d
	}

	pr := httpapi.PrincipalFrom(r.Context())
	var sources []interaction.Source
	if !s.authEnabled || pr == nil || mayReach(pr, RoleModeler) {
		rows, err := s.messageSources(r)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "process interactions: "+err.Error())
			return
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
		return
	}
	defer view.Close()

	now := s.now()
	g, err := interaction.Build(interaction.Input{
		ProcessID:   processID,
		Definitions: interactionDefinitions(defs),
		Sources:     sources,
		Scan: func(fn func(state.EventLogEntry) error) error {
			return view.EventLogBefore(part, 0, fn)
		},
		ReadsAll:  all,
		Visible:   func(key uint64) bool { return visible[key] },
		Now:       now,
		From:      now - int64(window),
		ScanLimit: interactionScan,
	})
	switch {
	case errors.Is(err, interaction.ErrUnknownProcess):
		httpapi.Error(w, http.StatusNotFound, "no deployed process "+processID)
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "process interactions: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, interactionGraph{Graph: g, Retention: retention.String()})
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
