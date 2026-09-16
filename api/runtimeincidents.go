package api

import (
	"sync"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The live diagram's aggregate incident overlay
// (ADR-0366).
//
// ADR-0337 gave the operations overview and the incidents table a reading whose size
// is the number of *causes*, and moved both onto it. The live diagram was not moved,
// and it is the surface an operator opens after the overview has told them a process
// has work parked — so the two disagreed in the worst direction available: the
// overview counted a flood, and the diagram it links to reported nothing at all.
//
// The disagreement was not a display bug. The overlay's incidents were collected on
// the run loop, so the collection was bounded twice — how far it read, and what it
// returned — and it walked the incident family in key order, attributing each entry to
// its definition only *after* reading it. Incidents are keyed by element instance and
// those keys ascend, so the budget was spent oldest-first: a definition deployed after
// a flood was reached only once the flood was resolved. Its own parked tokens were
// invisible on its own diagram, and an empty overlay under a truncated scan could not
// be told apart from a healthy one.
//
// So the overlay reads what the summary reads, the way the summary reads it: one walk
// of the incident family off the run loop against a snapshot (ADR-0266), through the
// same [walkIncidents] that attributes an incident to its definition everywhere else.
// Three surfaces that disagreed now cannot, because there is one attribution and one
// walk behind all of them.
//
// What that costs is measured rather than assumed. At 5 000 incidents over 5 000
// instances one walk is ~70 ms, and scoping it to a definition saves nothing: the
// family is read whole either way, because model.IncidentValue carries its process
// instance and not its definition, so attribution is a point lookup per instance. That
// is affordable at the nav badge's five seconds and not at the overlay's 1.5 — which
// is what the cache below is for, and why it is keyed by nothing. One walk answers for
// every definition at once, so a two-version process pays for one rather than two, and
// twenty tabs on the same flood pay for none of the other nineteen.
//
// It is held off the run loop under its own mutex, like [jobWaiters] and unlike the
// Starmap's structural cache: the handlers already run off the loop (ADR-0157 step 6)
// and the walk itself must, so routing the result back onto the loop would buy two
// dispatches and nothing else. The mutex is held across the walk, which makes it
// single-flight for free — readers arriving together do not each pay for one.

// runtimeIncidentTTL is how long one walk of the incident family answers the aggregate
// overlay for.
//
// Five seconds, for the same reason the nav badge polls at five (ADR-0337): it is the
// cadence at which walking the family is a cost an operations team can leave running,
// and the live view polls at 1.5s, so a definition under a flood pays for one walk per
// four polls however many browsers are watching it. Shorter would not buy accuracy
// worth having — a flood an operator is working through does not change meaning inside
// a second — and would put the walk back on nearly every poll. What must *not* wait out
// the TTL is a resolve, and that does not: see [Server.forgetIncidentCounts].
const runtimeIncidentTTL = 5 * time.Second

// defIncidents is one definition's parked tokens: the exact total, the exact split
// over the compiled element indices holding them, and a bounded page of the details
// behind them.
//
// The counts and the details are deliberately different sizes, and keeping them apart
// is the correction this file exists for. A diagram needs a number per element and the
// resolve panel needs rows, and reading the number off the rows made it the size of the
// page rather than the size of the problem.
//
// The element key is the compiled index rather than the BPMN id because that is what
// the incident stores; the id it maps to is only meaningful inside its own definition,
// and every caller holds that definition's compiled process anyway.
type defIncidents struct {
	total            int
	byElement        map[int32]int
	details          []runtimeIncident
	detailsTruncated bool
}

// runtimeIncidentCache holds the last reading, for every definition at once.
//
// A reading is a whole snapshot of the incident family, so a definition *absent* from
// it has no parked tokens — a fact, not a cache miss. That is the case this exists for:
// a healthy definition must be able to say so, and under the bounded scan it could not
// be told apart from an unreached one. Which is why freshness sits on the collection
// rather than per entry, and why the zero value of a lookup is a real answer.
//
// Its size is bounded by how many definitions are *deployed* and hold incidents, times
// the detail page — design-time size, the same bound [defIndex] is content with, and
// unrelated to how many instances or incidents exist.
type runtimeIncidentCache struct {
	mu     sync.Mutex
	at     time.Time
	held   map[uint64]*defIncidents
	filled bool // a reading has been taken; `held` is authoritative, empty or not
}

// incidentOverlay returns one definition's parked tokens, walking the incident family
// only when what it holds has aged out.
//
// The bool reports whether a reading could be produced at all. A walk that fails
// answers false rather than zero: "nothing is stuck" is a claim, and a store that could
// not be read has not earned it.
func (s *Server) incidentOverlay(defKey uint64) (*defIncidents, bool) {
	c := &s.runtimeIncidents
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.filled && time.Since(c.at) < runtimeIncidentTTL {
		return c.lookup(defKey), true
	}
	fresh := map[uint64]*defIncidents{}
	err := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		// One resolver for the whole walk, so the worker store is read once rather
		// than once per parked token (ADR-0160) — the same lookup the summary builds
		// for the same reason.
		connectorFor := s.incidentConnectorLookup()
		// No selector: one walk counts every definition, and a scoped walk would cost
		// the same. walkIncidents is the one place an incident is attributed to its
		// definition (ADR-0337), so what the overlay reports cannot drift from what the
		// overview and the incidents table do.
		return walkIncidents(rv, defs, incidentSelector{}, func(elKey uint64, v *model.IncidentValue, ctx incidentCtx, elementID string) error {
			if ctx.defKey == 0 {
				// An incident whose instance has no resolvable definition belongs to no
				// diagram. It is still stuck and the incidents table still lists it; it
				// simply cannot be drawn, so no overlay may claim it.
				return nil
			}
			d := fresh[ctx.defKey]
			if d == nil {
				d = &defIncidents{byElement: map[int32]int{}}
				fresh[ctx.defKey] = d
			}
			d.total++
			d.byElement[v.ElementId]++
			if len(d.details) >= maxRuntimeIncidents {
				d.detailsTruncated = true
				return nil
			}
			inc := runtimeIncident{
				ElementInstanceKey: elKey,
				ProcessInstanceKey: v.ProcessInstanceKey,
				JobKey:             v.JobKey,
				ElementID:          elementID,
				RaisedAt:           v.RaisedAt,
				Message:            v.Message,
			}
			if ctx.cp != nil {
				inc.Connector, inc.ConnectorKind, inc.ConnectorID = connectorFor(ctx.cp, v.ElementId)
				inc.RepairForm = ctx.cp.RepairForm(v.ElementId)
			}
			d.details = append(d.details, inc)
			return nil
		})
	})
	if err != nil {
		return nil, false
	}
	c.held, c.at, c.filled = fresh, time.Now(), true
	return c.lookup(defKey), true
}

// lookup answers for one definition inside a held reading. Caller holds the mutex.
func (c *runtimeIncidentCache) lookup(defKey uint64) *defIncidents {
	if d := c.held[defKey]; d != nil {
		return d
	}
	// Counted, and the count is none.
	return &defIncidents{byElement: map[int32]int{}}
}

// forgetIncidentCounts drops the held reading, so the next overlay walks afresh.
//
// It is called where an operator has just changed what is parked and will immediately
// look at a diagram to see it gone — a resolve, single or bulk. Everything else waits
// out the TTL, and an incident *arriving* deliberately gets no hook: nobody is watching
// for a failure they do not yet know about, and five seconds is not a delay anyone can
// perceive against a 1.5-second poll. What would be perceived is resolving a flood and
// watching the diagram insist it is still there.
func (s *Server) forgetIncidentCounts() {
	c := &s.runtimeIncidents
	c.mu.Lock()
	c.held, c.at, c.filled = nil, time.Time{}, false
	c.mu.Unlock()
}
