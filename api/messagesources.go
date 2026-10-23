package api

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// The kinds of source a message name has (ADR-0429 §6, ADR-0446 §1). The listing used
// to hold the first alone; a modeller now picks a name from all four, grouped by where
// it comes from.
const (
	// sourceInboundWatch is a Worker's event: an inbound watch publishes the name.
	sourceInboundWatch = "inbound-watch"
	// sourceProductAction is a product's action: the order starts or delivers the name
	// when the action is asked for.
	sourceProductAction = "product-action"
	// sourceWaitingProcess is a deployed process waiting for the name: a message start
	// event, or a catch — an intermediate catch, a boundary event, a receive task, an
	// event subprocess's message start.
	sourceWaitingProcess = "process"
	// sourceProcessThrow is a deployed process sending the name: a message throw event,
	// a message end event, or a send task naming a message, which compiles to a throw.
	sourceProcessThrow = "process-throw"
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
	// LastError and FailingSince say, while a watch cannot read its source, why and
	// since when (unix seconds) — under the same rule as Description, since the reason
	// names the Worker's configuration. An enabled watch that is failing feeds its name
	// as little as a disabled one, and only this tells the two apart from a quiet source.
	LastError    string `json:"lastError,omitempty"`
	FailingSince int64  `json:"failingSince,omitempty"`

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
	// process, the process that waits or sends.
	ProcessID string `json:"processId,omitempty"`
	// ElementID and Element name where a process waits or sends: its BPMN id, and
	// "start", "catch" or "throw". Element is the coarse word a Modeler older than Role
	// reads; every receiver that is not a start is a "catch" to it.
	ElementID string `json:"elementId,omitempty"`
	Element   string `json:"element,omitempty"`

	// The fields below describe a process row, a waiting or a sending one (ADR-0446
	// §1): which deployed version it is and whether it is the newest of its process,
	// its project, whether it is one of atlas's system processes, and the element's
	// role — start, catch, boundary, event-subprocess or throw. Newest is absent, that
	// is false, for an older version. Interrupting is set for a boundary or an event
	// subprocess only: whether firing cancels the work it guards.
	ProcessName   string `json:"processName,omitempty"`
	Version       int32  `json:"version,omitempty"`
	Newest        bool   `json:"newest,omitempty"`
	DefinitionKey uint64 `json:"definitionKey,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
	System        bool   `json:"system,omitempty"`
	Role          string `json:"role,omitempty"`
	Interrupting  *bool  `json:"interrupting,omitempty"`
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
	out, err := s.messageSources(r)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list message sources: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// messageSources is every source of every message name the caller may see, in a
// stable order: the one function behind the flat listing the Modeler's picker reads
// and the grouped one the Console's overview reads (ADR-0446 §2), so the two cannot
// disagree about a row or about who may see it.
func (s *Server) messageSources(r *http.Request) ([]messageSourceView, error) {
	var (
		out     = []messageSourceView{}
		defs    []deployedDefinition
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
				if f, failing := s.inboundHealth.failure(sub.ID); failing {
					v.LastError, v.FailingSince = f.LastError, f.FailingSince
				}
			}
			out = append(out, v)
		}
		var actions []messageSourceView
		if actions, loadErr = s.productActionSources(httpapi.PrincipalFrom(r.Context())); loadErr != nil {
			return
		}
		out = append(out, actions...)
		defs = s.readableDefinitions(httpapi.PrincipalFrom(r.Context()))
	})
	if loadErr != nil {
		return nil, loadErr
	}
	out = append(out, processSources(defs)...)
	// Stable order: by message name, then by kind, then by who sends or waits, newest
	// version first, so the panel's line does not reshuffle between two renders of the
	// same state.
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
		if a.Version != b.Version {
			return a.Version > b.Version
		}
		return a.ElementID < b.ElementID
	})
	return out, nil
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

// processSources lists where every deployed version of every process the caller may
// read waits for a message — its message starts, catches, receive tasks, boundaries
// and event subprocesses — and where it sends one (ADR-0446 §1). Each row says which
// version it is and whether it is the newest: a Modeler asking who waits now reads the
// newest, the overview lists every version, because an older version's running
// instances still wait and send. It reads the copies readableDefinitions made, off
// the loop.
func processSources(defs []deployedDefinition) []messageSourceView {
	var out []messageSourceView
	for _, d := range defs {
		row := func(mp compiler.MessagePoint, kind string) messageSourceView {
			element := "catch"
			switch mp.Role {
			case compiler.MessageStarts:
				element = "start"
			case compiler.MessageSends:
				element = "throw"
			}
			v := messageSourceView{
				MessageName: mp.MessageName, SourceKind: kind, Enabled: !d.inactive,
				ProcessID: d.processID, ElementID: mp.Element, Element: element,
				ProcessName: d.name, Version: d.version, Newest: d.newest,
				DefinitionKey: d.key, ProjectID: d.projectID, System: d.system,
				Role: string(mp.Role),
			}
			if mp.Role == compiler.MessageBoundary || mp.Role == compiler.MessageEventSubProcess {
				v.Interrupting = &mp.Interrupting
			}
			return v
		}
		for _, mp := range d.cp.MessageReceivers() {
			out = append(out, row(mp, sourceWaitingProcess))
		}
		for _, mp := range d.cp.MessageSenders() {
			out = append(out, row(mp, sourceProcessThrow))
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
