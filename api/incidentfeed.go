package api

import (
	"strconv"

	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/state"
)

// incidentEvent is the type, subject and data of an incident row on the feed
// (ADR-0435): the keys that find the incident, in the names GET /api/v1/incidents uses,
// and its cause — definition, element and type, the triple ADR-0337 reads a flood by —
// so a receiver can tell a new cause from the thousandth incident of a known one.
//
// Never the message. It is whatever text a worker, a connector or an expression
// produced, and may carry anything the instance held: a name, an address, a fragment of
// a response with a credential in it. A receiver that needs it asks Atlas, as the person
// it acts for.
func incidentEvent(e state.FeedEntry, defs defIndex) (typ, subject string, data map[string]any) {
	inc := e.Incident
	typ = eventcatalog.IncidentRaised
	if e.Kind == state.FeedIncidentResolved {
		typ = eventcatalog.IncidentResolved
	}
	data = map[string]any{
		"elementInstanceKey": inc.ElementInstanceKey,
		"processInstanceKey": inc.ProcessInstanceKey,
		"elementIndex":       inc.ElementId,
		"incidentType":       incidentType(inc),
		"raisedAt":           feedTime(inc.RaisedAt),
	}
	if e.Kind == state.FeedIncidentResolved {
		data["resolvedAt"] = feedTime(e.At)
	}
	if inc.JobKey != 0 {
		data["jobKey"] = inc.JobKey
	}
	// The definition was read when the row was folded; what it is called is read now,
	// from what is deployed, and is left out for a definition that has since gone.
	if e.Definition != 0 {
		data["processDefKey"] = e.Definition
		if d, ok := defs[e.Definition]; ok {
			data["processId"] = d.ProcessID
			data["version"] = d.Version
			if d.cp != nil {
				if id := d.cp.ElementBpmnId(inc.ElementId); id != "" {
					data["elementId"] = id
				}
			}
		}
	}
	return typ, "instances/" + strconv.FormatUint(inc.ProcessInstanceKey, 10), data
}
