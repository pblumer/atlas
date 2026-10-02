package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/eventcatalog"
)

// The event catalogue's two routes (ADR-0435 §7).
//
// What Atlas can say and who listens to it here are not equally sensitive. The
// entries — name, meaning, moment, payload, guarantees, access — describe Atlas, not
// this installation, and are in the repository and the handbook as well; choosing an
// event to listen to is modelling, so a modeler reads them. Who listens now is a map of
// where personal data flows in this installation, across every project, and is an
// administrator's: it is a route of its own rather than a column a page hides, so a
// modeler's answer never contains it.
//
// Neither belongs to the shop: a platform fact must not fall silent, nor its
// description vanish, because the service catalogue is switched off (ADR-0434).

// eventCatalogResp is the catalogue as GET /api/v1/event-catalog answers it.
type eventCatalogResp struct {
	Entries []eventcatalog.Entry `json:"entries"`
}

func (s *Server) handleEventCatalog(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, eventCatalogResp{Entries: eventcatalog.Entries})
}

// eventListener is one deployed element that receives an atlas.* event: a signal
// start or catch, a signal boundary or event subprocess, or a message receiver.
type eventListener struct {
	Type          string               `json:"type"`
	Channel       eventcatalog.Channel `json:"channel"`
	ProcessID     string               `json:"processId"`
	ProcessName   string               `json:"processName,omitempty"`
	Version       int32                `json:"version"`
	DefinitionKey uint64               `json:"definitionKey"`
	ProjectID     string               `json:"projectId,omitempty"`
	// System marks Atlas's own processes, which a message entry names as its
	// receiver; everything else is a model of this installation.
	System   bool   `json:"system,omitempty"`
	Element  string `json:"element"`
	Role     string `json:"role"`
	Inactive bool   `json:"inactive,omitempty"`
	// Personal names the payload fields marked personal data that this listener
	// receives, as the catalogue declares them; empty for a name the catalogue does
	// not know.
	Personal []string `json:"personal,omitempty"`
	// Catalogued is false for an atlas.* name the catalogue has no entry for: a model
	// waiting for something Atlas never emits.
	Catalogued bool `json:"catalogued"`
}

// feedListener is a feed subscription: a system beyond Atlas that is sent the feed.
type feedListener struct {
	SubscriptionID string   `json:"subscriptionId"`
	WorkerID       string   `json:"workerId"`
	WorkerName     string   `json:"workerName,omitempty"`
	Reach          []string `json:"reach,omitempty"`
	Enabled        bool     `json:"enabled"`
}

// eventListenersResp is who listens now, as GET /api/v1/event-catalog/listeners
// answers it. Every feed subscription receives every feed type, narrowed only by its
// reach, so the feed's types are listed once beside the subscriptions.
type eventListenersResp struct {
	Processes []eventListener `json:"processes"`
	Feed      []feedListener  `json:"feed"`
	FeedTypes []string        `json:"feedTypes"`
	// FeedDelivered is false on a server whose service catalogue is switched off:
	// the feed is neither served nor pushed there, and the subscriptions wait.
	FeedDelivered bool `json:"feedDelivered"`
}

func (s *Server) handleEventListeners(w http.ResponseWriter, _ *http.Request) {
	out := eventListenersResp{Processes: []eventListener{}, Feed: []feedListener{}, FeedTypes: []string{}, FeedDelivered: !s.catalogueOff}
	for _, e := range eventcatalog.Entries {
		if e.Has(eventcatalog.Feed) {
			out.FeedTypes = append(out.FeedTypes, e.Type)
		}
	}
	var loadErr error
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			if d == nil || d.cp == nil {
				continue
			}
			add := func(name string, ch eventcatalog.Channel, element, role string) {
				l := eventListener{
					Type: name, Channel: ch, ProcessID: d.ProcessID, ProcessName: d.Name,
					Version: d.Version, DefinitionKey: d.Key, ProjectID: d.ProjectID,
					System: s.systemPIDs[d.ProcessID], Element: element, Role: role, Inactive: d.inactive,
				}
				if e, ok := eventcatalog.Lookup(name); ok {
					l.Catalogued, l.Personal = true, e.PersonalFields()
				}
				out.Processes = append(out.Processes, l)
			}
			for _, sp := range d.cp.SignalPoints() {
				if sp.Receives() && eventcatalog.IsAtlasName(sp.SignalName) {
					add(sp.SignalName, eventcatalog.Signal, sp.Element, string(sp.Role))
				}
			}
			for _, mp := range d.cp.MessageReceivers() {
				if eventcatalog.IsAtlasName(mp.MessageName) {
					role := "catch"
					if mp.Start {
						role = "start"
					}
					add(mp.MessageName, eventcatalog.Message, mp.Element, role)
				}
			}
		}
		var subs []feedSubscription
		if subs, loadErr = s.feedSubs.LoadAll(); loadErr != nil {
			return
		}
		for _, sub := range subs {
			f := feedListener{SubscriptionID: sub.ID, WorkerID: sub.WorkerID, Reach: sub.Reach, Enabled: sub.Enabled}
			if wk, ok, err := s.connectors.Get(sub.WorkerID); err == nil && ok {
				f.WorkerName = wk.Name
			}
			out.Feed = append(out.Feed, f)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list feed subscriptions: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
