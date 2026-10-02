package api

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
)

// The kinds of source a message name has (ADR-0429 §6). The listing used to hold the
// first alone; a modeller now picks a name from all three, grouped by where it comes
// from.
const (
	// sourceInboundWatch is a Worker's event: an inbound watch publishes the name.
	sourceInboundWatch = "inbound-watch"
	// sourceProductAction is a product's action: the order starts or delivers the name
	// when the action is asked for.
	sourceProductAction = "product-action"
	// sourceWaitingProcess is a deployed process waiting for the name: a message start event,
	// or a catch — an intermediate catch, a boundary event, a receive task.
	sourceWaitingProcess = "process"
)

// messageSourceView is one source of a message name. It is the Modeler's answer to a
// question the model cannot answer itself.
//
// A message start event names a message and nothing else — deliberately: what feeds a
// message is an operational fact, not a property of the process (ADR-0075/0214). The
// same name can arrive from a Google or Jira watch, a clio subscription, POST /api/v1/messages,
// or another process's send task, and a model that named its source could be started by
// only one of them and would need a redeploy to change which.
//
// What the model therefore cannot show is whether anything feeds the name at all. A
// message name typed one character differently in the model and in the Events panel is
// two working halves that never meet: no error anywhere, and a process that simply never
// starts. This is the view that closes that gap without closing the seam.
type messageSourceView struct {
	MessageName string `json:"messageName"`
	// SourceKind says which of the three sources this is; the fields below it are each
	// filled for the kind that has them.
	SourceKind  string `json:"sourceKind"`
	ConnectorID string `json:"connectorId,omitempty"`
	// ConnectorName and Kind name the watch's worker. They are catalog facts —
	// existence, not configuration (see connectorscope.go) — so they are answered to any
	// modeller, like the worker picker's own listing.
	ConnectorName string `json:"connectorName,omitempty"`
	Kind          string `json:"kind,omitempty"`
	// Enabled is whether the source sends the name now: a watch that is on, a product
	// that is active, a process that is deployed and not deactivated.
	Enabled bool `json:"enabled"`
	// Description says *which* watch, e.g. the JQL a jira watch follows, the spreadsheet a
	// Google row watch reads, or the subject a
	// clio one does. That is the worker's configuration, so it is filled only for a
	// caller with viewer access to that worker and is empty otherwise: knowing a name
	// is fed is what a modeller needs; knowing the query behind it is not.
	Description string `json:"description,omitempty"`
	// CorrelationKey is the FEEL the watch correlates on, under the same rule as
	// Description.
	CorrelationKey string `json:"correlationKey,omitempty"`

	// ProductID, ProductName, Action, Effect and Triggers describe a product action:
	// the product, its name in the catalogue's first language, the action's key and
	// effect, and who may ask for it.
	ProductID   string   `json:"productId,omitempty"`
	ProductName string   `json:"productName,omitempty"`
	Action      string   `json:"action,omitempty"`
	Effect      string   `json:"effect,omitempty"`
	Triggers    []string `json:"triggers,omitempty"`
	// ProcessID is, for a product action, the process the product binds the action to,
	// so a Modeler can tell that the model it shows is one a product binds; for a
	// process, the process that waits.
	ProcessID string `json:"processId,omitempty"`
	// ElementID and Element name where a process waits: its BPMN id, and "start" or
	// "catch".
	ElementID string `json:"elementId,omitempty"`
	Element   string `json:"element,omitempty"`
}

// handleListMessageSources lists every inbound watch on this server by the message name
// it publishes, so the Modeler can say whether a message start event has anything
// feeding it — and name what.
//
// It is one listing rather than a per-worker query because the question is asked
// about a *name*, and a name is not owned by a worker: two watches on two different
// workers may publish the same one, which is exactly the case an author most wants to
// see. Filtering to one name server-side would also make the panel ask again on every
// keystroke of a rename, for a listing small enough to hold.
func (s *Server) handleListMessageSources(w http.ResponseWriter, r *http.Request) {
	var (
		out     = []messageSourceView{}
		loadErr error
	)
	// The subscription store, the worker store and the role check happen in one
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
				// A watch whose worker is gone publishes nothing, so it would be a
				// misleading answer to "is this name fed".
				continue
			}
			v := messageSourceView{
				MessageName:   sub.MessageName,
				SourceKind:    sourceInboundWatch,
				ConnectorID:   c.ID,
				ConnectorName: c.Name,
				Kind:          c.Kind,
				// A watch on a disabled worker is as inert as a disabled watch, and
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
		var actions []messageSourceView
		if actions, loadErr = s.productActionSources(httpapi.PrincipalFrom(r.Context())); loadErr != nil {
			return
		}
		out = append(out, actions...)
		out = append(out, s.processSources()...)
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list message sources: "+loadErr.Error())
		return
	}
	// Stable order: by message name, then by kind, then by who sends or waits, so the
	// panel's line does not reshuffle between two renders of the same state.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.MessageName != b.MessageName {
			return a.MessageName < b.MessageName
		}
		if a.SourceKind != b.SourceKind {
			return a.SourceKind < b.SourceKind
		}
		if a.ConnectorName != b.ConnectorName {
			return a.ConnectorName < b.ConnectorName
		}
		if a.ProductID != b.ProductID {
			return a.ProductID < b.ProductID
		}
		if a.ProcessID != b.ProcessID {
			return a.ProcessID < b.ProcessID
		}
		return a.ElementID < b.ElementID
	})
	httpapi.JSON(w, http.StatusOK, out)
}

// productActionSources lists every product action's message, for the products of the
// catalogues p maintains or was shared (ADR-0211 §3): the picture of which process a
// product drives is a maintainer's, not the audience's. A product that still carries
// the operation map is listed as the actions it means. Runs on the loop.
func (s *Server) productActionSources(p *httpapi.Principal) ([]messageSourceView, error) {
	// A server that switched the catalogue off lists none of its products' actions:
	// the store is still on disk and read here directly, not through a route the
	// switch removed (ADR-0434).
	if s.catalogStore == nil || s.catalogs == nil || s.catalogueOff {
		return nil, nil
	}
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		return nil, err
	}
	maintained := map[string]catalog.Catalog{}
	for _, c := range cats {
		if s.catalogs.MayMaintain(c, p) {
			maintained[c.ID] = c
		}
	}
	items, err := s.catalogStore.Items()
	if err != nil {
		return nil, err
	}
	var out []messageSourceView
	for _, it := range items {
		c, ok := maintained[it.HomeCatalog]
		if !ok {
			continue
		}
		for _, a := range it.ActionList() {
			msg := strings.TrimSpace(a.Message)
			if msg == "" {
				continue
			}
			b := it.BindingFor(a.Key)
			out = append(out, messageSourceView{
				MessageName: msg, SourceKind: sourceProductAction,
				Enabled:   it.State == catalog.StateActive,
				ProductID: it.ID, ProductName: productNameIn(it, c.Languages),
				Action: a.Key, Effect: a.Effect, Triggers: slices.Clone(a.Triggers),
				ProcessID: b.Process,
			})
		}
	}
	return out, nil
}

// productNameIn is a product's name in the first of langs it has one in, or in the
// first language it has one in at all.
func productNameIn(it catalog.Item, langs []string) string {
	for _, l := range langs {
		if t := strings.TrimSpace(it.Texts[l]); t != "" {
			return t
		}
	}
	keys := make([]string, 0, len(it.Texts))
	for k := range it.Texts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if t := strings.TrimSpace(it.Texts[k]); t != "" {
			return t
		}
	}
	return it.ID
}

// processSources lists where the newest deployed version of every process waits for a
// message: its message start events and its catches. A send task or a throw event
// names one of these to reach a process that is already modelled. Runs on the loop.
func (s *Server) processSources() []messageSourceView {
	newest := map[string]*deployment{}
	for _, d := range s.deployments {
		if d == nil || d.cp == nil {
			continue
		}
		if have, ok := newest[d.ProcessID]; !ok || d.Version > have.Version {
			newest[d.ProcessID] = d
		}
	}
	var out []messageSourceView
	for id, d := range newest {
		on := !d.inactive
		for _, ms := range d.cp.MessageStartEvents() {
			out = append(out, messageSourceView{MessageName: ms.MessageName, SourceKind: sourceWaitingProcess,
				Enabled: on, ProcessID: id, ElementID: d.cp.ElementBpmnId(ms.ElementId), Element: "start"})
		}
		for _, cp := range d.cp.MessageCatchPoints() {
			out = append(out, messageSourceView{MessageName: cp.MessageName, SourceKind: sourceWaitingProcess,
				Enabled: on, ProcessID: id, ElementID: cp.Element, Element: "catch"})
		}
	}
	return out
}

// describeInboundWatch renders one watch in the words of its own kind: a jira watch is
// its JQL and the timestamp it follows, a clio one its subject and whether the subtree
// counts, a discord one the channel it reads. The kind comes from the worker record, which is the discriminator
// everywhere else too (see inboundSubscription).
func describeInboundWatch(kind string, sub inboundSubscription) string {
	if kind == connectorKindGoogleSheets {
		// A Google watch is one of two things, and which it is follows from the target
		// it names — the same discriminator the bridge and the validator use.
		if folder := strings.TrimSpace(sub.FolderID); folder != "" {
			field := strings.TrimSpace(sub.CursorField)
			if field == "" {
				field = "created"
			}
			return fmt.Sprintf("files %s in Drive folder %s", field, folder)
		}
		rng := strings.TrimSpace(sub.WatchRange)
		if rng == "" {
			rng = sheetsDefaultRange
		}
		return fmt.Sprintf("new rows in spreadsheet %s (%s)", sub.SpreadsheetID, rng)
	}
	if kind == connectorKindDiscord {
		return fmt.Sprintf("new messages in channel %s", sub.ChannelID)
	}
	if kind == connectorKindMail {
		folder := strings.TrimSpace(sub.MailFolder)
		if folder == "" {
			folder = "INBOX"
		}
		if len(sub.AllowedSenders) > 0 {
			return fmt.Sprintf("new mail in folder %s from %s", folder, strings.Join(sub.AllowedSenders, ", "))
		}
		return fmt.Sprintf("new mail in folder %s", folder)
	}
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
