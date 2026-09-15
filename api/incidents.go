package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The incident surfaces that are about a *flood* rather than about one parked token
// (ADR-0337).
//
// Everything built before this answered "what is stuck here", one incident at a time:
// the list, the diagram badge, the replay's details, and the four ways out of a single
// incident (ADR-0061/0158/0160/0169). A worker whose endpoint stops answering parks
// every instance that reaches the task, and then a few thousand incidents are one cause
// with one fix — while every surface treats them as a few thousand problems. Two things
// are missing, and they are both here: a reading whose size is the number of *causes*,
// and an action that clears a whole cause.
//
// Neither needs new durable state. An incident leaves state two ways — resolved, or
// dropped with the element instance it sits on — which is why the count is a scan and
// not a maintained counter (ADR-0061); a per-element counter would carry the same drift
// per element. So both read the incident family off the run loop against a snapshot,
// exactly as the listing does (ADR-0266).

const (
	// bulkResolveBatchDefault / bulkResolveBatchMax bound one filter-mode resolve.
	// The bound is not about the loop turn — queueing a command per incident is cheap
	// — but about what follows it: every re-activated job becomes work an in-process
	// worker may pick up in the same request (s.drive), so an unbounded call would be
	// an unbounded outbound-call storm. Repeat while the response says remaining.
	bulkResolveBatchDefault = 500
	bulkResolveBatchMax     = 5000
)

// maxIncidentSummaryGroups bounds the summary's *response*, not its scan: the walk is
// the incident family either way, and `total` counts every incident it sees. A flood is
// a handful of causes, so the cap only bites on a store whose incidents are spread over
// hundreds of elements — where the tail is not what an operator acts on first. What the
// cap left out is reported as `ungrouped`.
//
// A var rather than a const so a test can reach the overflow without standing up five
// hundred causes; nothing outside a test writes it.
var maxIncidentSummaryGroups = 500

// errResolveBatchFull stops the selection scan once a bulk resolve has collected a
// full batch. Like errCancelBatchFull it is control flow, not a failure.
var errResolveBatchFull = errors.New("resolve batch full")

// incidentCtx is the deployment context of one parked token, resolved once per
// instance for a whole walk: a flood is usually many incidents over few instances,
// and each attribution is a point read.
type incidentCtx struct {
	defKey    uint64
	processID string
	version   int32
	cp        *compiler.CompiledProcess
}

// incidentSelector is what an operator means by "these incidents". It is written
// twice — as the listing's query parameters and as the bulk resolve's body — and
// evaluated once, here, so that what a page shows and what an action touches cannot
// disagree.
//
// Every field is optional and they AND together. An empty selector matches
// everything, which is right for a listing and deliberately refused by the bulk
// resolve: resolving the whole server is not something to reach by leaving a field
// out (see [Server.handleResolveIncidents]).
type incidentSelector struct {
	instance  uint64
	process   uint64
	elementID string
	// elementIndex is the *compiled* element index, the one the incident actually
	// stores. It exists beside elementID for the case the id cannot answer: an
	// instance whose definition is no longer deployed has no compiled process, so its
	// incidents carry no BPMN id at all, and a scope that simply left the id out would
	// silently widen to the whole definition. Only meaningful together with a
	// definition — an index is not a thing outside its own compiled graph.
	elementIndex *int32
	incType      string
	message      string // lower-cased substring; matched against the incident's message
}

func (sel incidentSelector) empty() bool {
	return sel.instance == 0 && sel.process == 0 && sel.elementID == "" &&
		sel.elementIndex == nil && sel.incType == "" && sel.message == ""
}

// match reports whether one incident is in scope, given the context [walkIncidents]
// has resolved for it. elementID is the BPMN id — empty when the instance outlived its
// deployment, which is why an element-scoped selector cannot match such an incident:
// there is nothing to compare, and guessing would resolve a token on an unrelated
// shape.
//
// The instance scope is not here: the walk answers it before the point read that fills
// this context in, because an instance-scoped read over a flood must not pay a lookup
// per foreign incident. One filter, one place.
func (sel incidentSelector) match(v *model.IncidentValue, ctx incidentCtx, elementID string) bool {
	switch {
	case sel.process != 0 && ctx.defKey != sel.process:
		return false
	case sel.elementID != "" && elementID != sel.elementID:
		return false
	case sel.elementIndex != nil && v.ElementId != *sel.elementIndex:
		return false
	case sel.incType != "" && incidentType(v) != sel.incType:
		return false
	case sel.message != "" && !strings.Contains(strings.ToLower(v.Message), sel.message):
		return false
	}
	return true
}

// incidentTypes are the values [incidentType] can report, and so the only ones a
// selector may name. Naming another is a client error rather than an empty page: a
// typo that silently matches nothing reads as "nothing is stuck".
var incidentTypes = map[string]bool{"job": true, "timer": true, "budget": true}

// incidentSelectorFromQuery reads a selector off the listing's query string,
// reporting the first malformed parameter rather than ignoring it.
func incidentSelectorFromQuery(r *http.Request) (incidentSelector, error) {
	var sel incidentSelector
	for _, f := range []struct {
		name string
		dst  *uint64
	}{{"instance", &sel.instance}, {"process", &sel.process}} {
		q := strings.TrimSpace(r.URL.Query().Get(f.name))
		if q == "" {
			continue
		}
		n, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			return sel, errors.New("invalid " + f.name + " (want a key)")
		}
		*f.dst = n
	}
	sel.elementID = strings.TrimSpace(r.URL.Query().Get("element"))
	if q := strings.TrimSpace(r.URL.Query().Get("elementIndex")); q != "" {
		n, err := strconv.ParseInt(q, 10, 32)
		if err != nil || n < 0 {
			return sel, errors.New("invalid elementIndex (want a non-negative compiled element index)")
		}
		idx := int32(n)
		sel.elementIndex = &idx
	}
	sel.message = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("message")))
	if t := strings.TrimSpace(r.URL.Query().Get("type")); t != "" {
		if !incidentTypes[t] {
			return sel, errors.New("invalid type (want job, timer, or budget)")
		}
		sel.incType = t
	}
	return sel, nil
}

// walkIncidents calls fn for every unresolved incident the selector admits, with the
// deployment context and BPMN element id already resolved.
//
// It is the one place that attributes an incident to its definition. A compiled
// element index is only meaningful inside its own definition, so the attribution is
// not optional decoration: it is what makes "element `review`" mean anything at all.
// Instance contexts are memoized because a flood is typically many parked tokens over
// comparatively few instances.
//
// fn may return [errListTruncated] or [errResolveBatchFull] to stop the walk; both
// reach the caller unchanged, so a bounded reader keeps its own sentinel.
func walkIncidents(rv *state.ReadView, defs defIndex, sel incidentSelector,
	fn func(elKey uint64, v *model.IncidentValue, ctx incidentCtx, elementID string) error) error {
	resolved := map[uint64]incidentCtx{}
	lookup := func(piKey uint64) (incidentCtx, error) {
		if ctx, ok := resolved[piKey]; ok {
			return ctx, nil
		}
		var ctx incidentCtx
		pi, ok, err := rv.ProcessInstance(piKey)
		if err != nil {
			return ctx, err
		}
		if ok {
			ctx.defKey = pi.ProcessDefKey
			if d, ok := defs[pi.ProcessDefKey]; ok {
				ctx.processID, ctx.version, ctx.cp = d.ProcessID, d.Version, d.cp
			}
		}
		resolved[piKey] = ctx
		return ctx, nil
	}
	return rv.Incidents(func(elKey uint64, v *model.IncidentValue) error {
		// The instance filter is answered before the point read: an instance-scoped
		// walk over a flood must not pay a lookup per foreign incident.
		if sel.instance != 0 && v.ProcessInstanceKey != sel.instance {
			return nil
		}
		ctx, err := lookup(v.ProcessInstanceKey)
		if err != nil {
			return err
		}
		elementID := ""
		if ctx.cp != nil {
			elementID = ctx.cp.ElementBpmnId(v.ElementId)
		}
		if !sel.match(v, ctx, elementID) {
			return nil
		}
		return fn(elKey, v, ctx, elementID)
	})
}

// incidentGroupView is one *cause*: the element of the definition that parked, why,
// and how many tokens are behind it. The triple (definition, element, type) is the
// unit an operator can act on as one thing — the fix for all of them is the same fix,
// and [Server.handleResolveIncidents] takes exactly this scope.
type incidentGroupView struct {
	ProcessDefKey uint64 `json:"processDefKey,omitempty"`
	ProcessID     string `json:"processId,omitempty"`
	Version       int32  `json:"version,omitempty"`
	ElementID     string `json:"elementId,omitempty"`
	ElementIndex  int32  `json:"elementIndex"`
	Type          string `json:"type"`
	Count         int    `json:"count"`
	// The window the cause has been running: the first token parked, and the last.
	// A cause whose newest incident is minutes old is still happening; one whose
	// window closed hours ago is a backlog somebody has already stopped feeding.
	OldestRaisedAt int64 `json:"oldestRaisedAt"`
	NewestRaisedAt int64 `json:"newestRaisedAt"`
	// Message is the oldest incident's — a representative that does not change
	// between two polls of the same flood. MessageVaries says the group holds more
	// than one distinct wording, so a reader knows the line is a sample rather than
	// the whole story (a per-instance detail in the text, or two failure modes on
	// one element).
	Message       string `json:"message"`
	MessageVaries bool   `json:"messageVaries,omitempty"`
	// The worker and repair-form context the single incident already carries
	// (ADR-0160/0287/0169), resolved once per group rather than once per incident.
	// It is what makes the group actionable: the fix for a flood is a field on a
	// worker, and this says which one.
	Connector     string `json:"connector,omitempty"`
	ConnectorKind string `json:"connectorKind,omitempty"`
	ConnectorID   string `json:"connectorId,omitempty"`
	RepairForm    string `json:"repairForm,omitempty"`
}

type incidentSummaryResp struct {
	// Total counts every incident the walk saw, including any the group cap left
	// out — a number that is honest whatever the response had room for.
	Total  int                 `json:"total"`
	Groups []incidentGroupView `json:"groups"`
	// GroupsTruncated marks a response whose group list hit the cap; Ungrouped is
	// how many incidents are behind that edge.
	GroupsTruncated bool `json:"groupsTruncated,omitempty"`
	Ungrouped       int  `json:"ungrouped,omitempty"`
}

// groupKey identifies a cause. The compiled element index rather than the BPMN id:
// the id is empty for an instance whose definition is gone, and two definitions may
// use the same id for different elements.
type groupKey struct {
	defKey  uint64
	element int32
	incType string
}

// handleIncidentSummary answers "what is stuck, by cause" in constant size.
//
// This is the reading a flood needs and the row list cannot be. Under a broken worker
// the listing returns thousands of near-identical rows — megabytes per refresh, tens
// of thousands of DOM nodes, and no statement anywhere that they are one problem.
// Here the same walk produces one line per (definition, element, type), biggest first,
// so the flood is the first thing on the page and the thing to fix is named on it.
//
// Off the run loop against a snapshot (ADR-0266): it is a walk of the whole incident
// family, which is exactly what /stats already does for the nav badge, and precisely
// the endpoint somebody reaches for when the engine is busy.
func (s *Server) handleIncidentSummary(w http.ResponseWriter, r *http.Request) {
	sel, selErr := incidentSelectorFromQuery(r)
	if selErr != nil {
		httpapi.Error(w, http.StatusBadRequest, selErr.Error())
		return
	}
	var (
		resp   = incidentSummaryResp{Groups: []incidentGroupView{}}
		groups = map[groupKey]*incidentGroupView{}
		// firstMessage is the message first seen in each group, against which every
		// later one is compared: "more than one distinct wording" cannot be decided
		// from the representative alone, because the representative moves whenever an
		// older incident turns up.
		firstMessage = map[groupKey]string{}
	)
	scanErr := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		connectorFor := s.incidentConnectorLookup()
		return walkIncidents(rv, defs, sel, func(_ uint64, v *model.IncidentValue, ctx incidentCtx, elementID string) error {
			resp.Total++
			key := groupKey{defKey: ctx.defKey, element: v.ElementId, incType: incidentType(v)}
			g, ok := groups[key]
			if !ok {
				if len(groups) >= maxIncidentSummaryGroups {
					resp.GroupsTruncated = true
					resp.Ungrouped++
					return nil
				}
				g = &incidentGroupView{
					ProcessDefKey: ctx.defKey, ProcessID: ctx.processID, Version: ctx.version,
					ElementID: elementID, ElementIndex: v.ElementId, Type: key.incType, Count: 1,
					OldestRaisedAt: v.RaisedAt, NewestRaisedAt: v.RaisedAt, Message: v.Message,
				}
				// Once per cause, not once per parked token: the worker store is read
				// at most once for the whole page (ADR-0159), and the lookup itself is
				// a scan of the configured records.
				if ctx.cp != nil {
					g.Connector, g.ConnectorKind, g.ConnectorID = connectorFor(ctx.cp, v.ElementId)
					g.RepairForm = ctx.cp.RepairForm(v.ElementId)
				}
				groups[key] = g
				firstMessage[key] = v.Message
				return nil
			}
			g.Count++
			if v.RaisedAt < g.OldestRaisedAt {
				g.OldestRaisedAt, g.Message = v.RaisedAt, v.Message
			}
			if v.RaisedAt > g.NewestRaisedAt {
				g.NewestRaisedAt = v.RaisedAt
			}
			if v.Message != firstMessage[key] {
				g.MessageVaries = true
			}
			return nil
		})
	})
	switch {
	case errors.Is(scanErr, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, scanErr.Error())
		return
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "incident summary: "+scanErr.Error())
		return
	}
	for _, g := range groups {
		resp.Groups = append(resp.Groups, *g)
	}
	// Biggest cause first — the flood is what the operator came for. The rest of the
	// ordering only has to be stable, so two polls of an unchanged store agree.
	sort.Slice(resp.Groups, func(i, j int) bool {
		a, b := resp.Groups[i], resp.Groups[j]
		switch {
		case a.Count != b.Count:
			return a.Count > b.Count
		case a.OldestRaisedAt != b.OldestRaisedAt:
			return a.OldestRaisedAt < b.OldestRaisedAt
		case a.ProcessDefKey != b.ProcessDefKey:
			return a.ProcessDefKey < b.ProcessDefKey
		case a.ElementIndex != b.ElementIndex:
			return a.ElementIndex < b.ElementIndex
		default:
			return a.Type < b.Type
		}
	})
	httpapi.JSON(w, http.StatusOK, resp)
}

// resolveIncidentsReq selects which incidents to resolve. Exactly one mode is used
// per call: Keys picks a hand-selected set (the rows an operator ticked), and the
// selector fields resolve every incident matching a scope — the "clear this whole
// cause" shape a flood needs. Retries is the budget each re-activated job gets
// (default 1); Limit bounds a filter-mode call the way every other bulk drain does
// (repeat while the response reports remaining=true) and is ignored in keys mode,
// where the request is already the bound.
type resolveIncidentsReq struct {
	Keys               []uint64 `json:"keys"`
	ProcessDefKey      uint64   `json:"processDefKey"`
	ProcessInstanceKey uint64   `json:"processInstanceKey"`
	ElementID          string   `json:"elementId"`
	// ElementIndex names the element by its compiled index instead of its BPMN id —
	// the only way to say "this element" for an instance whose definition is no longer
	// deployed, where nothing can resolve an id. A pointer because 0 is a valid index.
	ElementIndex *int32 `json:"elementIndex"`
	Type         string `json:"type"`
	Message      string `json:"message"`
	Retries      int    `json:"retries"`
	Limit        int    `json:"limit"`
}

// resolveIncidentsResp reports what a bulk resolve did: how many incidents it
// cleared, how many requested keys held no incident (keys mode only), and — for a
// filter-mode call that hit its per-call cap — whether more may match.
type resolveIncidentsResp struct {
	Resolved  int       `json:"resolved"`
	NotFound  int       `json:"notFound"`
	Remaining bool      `json:"remaining"`
	Stats     statsResp `json:"stats"`
}

// handleResolveIncidents resolves a selected set of incidents in one call — the
// action a flood needs, and the one thing the per-incident endpoint cannot become.
//
// Two mutually exclusive modes, deliberately the shape ADR-0090 settled for bulk
// termination: an explicit key set (point lookups, cheap however many incidents
// exist), or a matching scope evaluated by the same [incidentSelector] the listing
// filters by — so an operator can read exactly what they are about to resolve.
//
// A scope must name at least one selector. Resolving is not destructive (a retry
// against an unfixed cause parks the token again with the new reason), so this is not
// a blast-radius gate; it is that "everything on this server" should be a thing
// somebody asks for — `{"type":"job"}` says it — rather than what an empty body does.
func (s *Server) handleResolveIncidents(w http.ResponseWriter, r *http.Request) {
	var req resolveIncidentsReq
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	sel := incidentSelector{
		instance:     req.ProcessInstanceKey,
		process:      req.ProcessDefKey,
		elementID:    strings.TrimSpace(req.ElementID),
		elementIndex: req.ElementIndex,
		message:      strings.ToLower(strings.TrimSpace(req.Message)),
	}
	if req.ElementIndex != nil && *req.ElementIndex < 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid elementIndex (want a non-negative compiled element index)")
		return
	}
	if t := strings.TrimSpace(req.Type); t != "" {
		if !incidentTypes[t] {
			httpapi.Error(w, http.StatusBadRequest, "invalid type (want job, timer, or budget)")
			return
		}
		sel.incType = t
	}
	switch {
	case len(req.Keys) > 0 && !sel.empty():
		httpapi.Error(w, http.StatusBadRequest, "specify either keys or a scope (processDefKey / processInstanceKey / elementId / elementIndex / type / message), not both")
		return
	case len(req.Keys) == 0 && sel.empty():
		httpapi.Error(w, http.StatusBadRequest,
			"name what to resolve: keys, or a scope (processDefKey / processInstanceKey / elementId / elementIndex / type / message). Resolving every incident on the server is asked for with {\"type\":\"job\"}")
		return
	case req.Retries < 0:
		httpapi.Error(w, http.StatusBadRequest, "invalid retries (want a positive integer)")
		return
	case req.Limit < 0:
		httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
		return
	}
	retries := int32(req.Retries)
	if retries < 1 {
		retries = 1
	}

	var (
		keys      []uint64 // the incidents this call will resolve
		notFound  int
		remaining bool
		opErr     error
	)
	if len(req.Keys) > 0 {
		keys, notFound, opErr = s.selectIncidentsByKey(req.Keys)
	} else {
		limit := req.Limit
		if limit == 0 {
			limit = bulkResolveBatchDefault
		}
		if limit > bulkResolveBatchMax {
			limit = bulkResolveBatchMax
		}
		keys, remaining, opErr = s.selectIncidentsByScope(sel, limit)
	}
	if opErr == nil && len(keys) > 0 {
		s.do(func() {
			for _, k := range keys {
				s.proc.ResolveIncident(k, retries)
			}
		})
		// Drive what this call unblocked OUTSIDE the run loop: a bulk resolve can hand
		// hundreds of jobs back to the workers at once, and every one of them is an
		// outbound call. Holding the single writer for that is the stall ADR-0157
		// step 6 removed.
		opErr = s.drive()
	}
	var stats statsResp
	if opErr == nil {
		stats, opErr = s.statsOffLoop()
	}
	switch {
	case errors.Is(opErr, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, opErr.Error())
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "resolve incidents: "+opErr.Error())
	default:
		// A scope that matched nothing is an honest zero, not a 404: the operator asked
		// what is in scope, and the answer is nothing — which is also what the call
		// after a successful drain says.
		httpapi.JSON(w, http.StatusOK, resolveIncidentsResp{
			Resolved: len(keys), NotFound: notFound, Remaining: remaining, Stats: stats,
		})
	}
}

// selectIncidentsByKey verifies a hand-picked set with one point read each, on the
// run loop. Duplicates collapse; a key that holds no incident is counted as not
// found rather than failing the call — it is usually an incident somebody else
// resolved while the page was open.
func (s *Server) selectIncidentsByKey(requested []uint64) (keys []uint64, notFound int, err error) {
	uniq := make(map[uint64]struct{}, len(requested))
	ordered := make([]uint64, 0, len(requested))
	for _, k := range requested {
		if _, seen := uniq[k]; seen {
			continue
		}
		uniq[k] = struct{}{}
		ordered = append(ordered, k)
	}
	s.do(func() {
		for _, k := range ordered {
			inc, getErr := s.store.GetIncident(k)
			if getErr != nil {
				err = getErr
				return
			}
			if inc == nil {
				notFound++
				continue
			}
			keys = append(keys, k)
		}
	})
	return keys, notFound, err
}

// selectIncidentsByScope collects up to limit incidents matching the selector, off
// the run loop against a snapshot. Hitting the limit reports remaining=true: more may
// match, call again.
//
// Selection and resolution are two phases, like the bulk terminate's (ADR-0090). An
// incident that disappears between them — resolved by somebody else, or dropped with
// a cancelled instance — is harmless: resolving a key that holds no incident is the
// same no-op the keys mode already relies on.
func (s *Server) selectIncidentsByScope(sel incidentSelector, limit int) (keys []uint64, remaining bool, err error) {
	err = s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		walkErr := walkIncidents(rv, defs, sel, func(elKey uint64, _ *model.IncidentValue, _ incidentCtx, _ string) error {
			keys = append(keys, elKey)
			if len(keys) >= limit {
				return errResolveBatchFull // batch full: more may match, stop here
			}
			return nil
		})
		if errors.Is(walkErr, errResolveBatchFull) {
			remaining = true
			return nil
		}
		return walkErr
	})
	return keys, remaining, err
}
