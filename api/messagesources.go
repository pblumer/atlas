package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// messageSourceView is one inbound watch, described by the message name it publishes
// under. It is the Modeler's answer to a question the model cannot answer itself.
//
// A message start event names a message and nothing else — deliberately: what feeds a
// message is an operational fact, not a property of the process (ADR-0075/0214). The
// same name can arrive from a Jira watch, a clio subscription, POST /api/v1/messages,
// or another process's send task, and a model that named its source could be started by
// only one of them and would need a redeploy to change which.
//
// What the model therefore cannot show is whether anything feeds the name at all. A
// message name typed one character differently in the model and in the Events panel is
// two working halves that never meet: no error anywhere, and a process that simply never
// starts. This is the view that closes that gap without closing the seam.
type messageSourceView struct {
	MessageName string `json:"messageName"`
	ConnectorID string `json:"connectorId"`
	// ConnectorName and Kind name the watch's connector. They are catalog facts —
	// existence, not configuration (see connectorscope.go) — so they are answered to any
	// modeller, like the connector picker's own listing.
	ConnectorName string `json:"connectorName"`
	Kind          string `json:"kind"`
	Enabled       bool   `json:"enabled"`
	// Description says *which* watch, e.g. the JQL a jira watch follows or the subject a
	// clio one does. That is the connector's configuration, so it is filled only for a
	// caller with viewer access to that connector and is empty otherwise: knowing a name
	// is fed is what a modeller needs; knowing the query behind it is not.
	Description string `json:"description,omitempty"`
	// CorrelationKey is the FEEL the watch correlates on, under the same rule as
	// Description.
	CorrelationKey string `json:"correlationKey,omitempty"`
}

// handleListMessageSources lists every inbound watch on this server by the message name
// it publishes, so the Modeler can say whether a message start event has anything
// feeding it — and name what.
//
// It is one listing rather than a per-connector query because the question is asked
// about a *name*, and a name is not owned by a connector: two watches on two different
// connectors may publish the same one, which is exactly the case an author most wants to
// see. Filtering to one name server-side would also make the panel ask again on every
// keystroke of a rename, for a listing small enough to hold.
func (s *Server) handleListMessageSources(w http.ResponseWriter, r *http.Request) {
	var (
		out     = []messageSourceView{}
		loadErr error
	)
	// The subscription store, the connector store and the role check happen in one
	// closure on the run-loop goroutine, which owns them (invariant I3).
	s.do(func() {
		var subs []inboundSubscription
		if subs, loadErr = s.inboundSubs.LoadAll(); loadErr != nil {
			return
		}
		var conns []connector
		if conns, loadErr = s.connectors.LoadAll(); loadErr != nil {
			return
		}
		byID := make(map[string]connector, len(conns))
		for _, c := range conns {
			byID[c.ID] = c
		}
		for _, sub := range subs {
			c, ok := byID[sub.ConnectorID]
			if !ok {
				// A watch whose connector is gone publishes nothing, so it would be a
				// misleading answer to "is this name fed".
				continue
			}
			v := messageSourceView{
				MessageName:   sub.MessageName,
				ConnectorID:   c.ID,
				ConnectorName: c.Name,
				Kind:          c.Kind,
				// A watch on a disabled connector is as inert as a disabled watch, and
				// the author asking "is this name fed" is asking about the outcome, not
				// about which of the two switches is off.
				Enabled: sub.Enabled && c.Enabled,
			}
			if code, _ := s.checkConnectorRole(r, c, ScopeRoleViewer); code == 0 {
				v.Description = describeInboundWatch(c.Kind, sub)
				v.CorrelationKey = sub.CorrelationKey
			}
			out = append(out, v)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list message sources: "+loadErr.Error())
		return
	}
	// Stable order: by message name, then by connector, so the panel's line does not
	// reshuffle between two renders of the same state.
	sort.Slice(out, func(i, j int) bool {
		if out[i].MessageName != out[j].MessageName {
			return out[i].MessageName < out[j].MessageName
		}
		return out[i].ConnectorName < out[j].ConnectorName
	})
	httpapi.JSON(w, http.StatusOK, out)
}

// describeInboundWatch renders one watch in the words of its own kind: a jira watch is
// its JQL and the timestamp it follows, a clio one its subject and whether the subtree
// counts. The kind comes from the connector record, which is the discriminator
// everywhere else too (see inboundSubscription).
func describeInboundWatch(kind string, sub inboundSubscription) string {
	if kind == connectorKindJira {
		field := strings.TrimSpace(sub.CursorField)
		if field == "" {
			field = "created"
		}
		return fmt.Sprintf("JQL %s (on %s)", sub.JQL, field)
	}
	if sub.Recursive {
		return sub.WatchedSubject + " (and its subtree)"
	}
	return sub.WatchedSubject
}
