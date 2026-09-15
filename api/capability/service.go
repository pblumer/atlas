// Package capability serves the business-architecture area: the business
// capabilities an organisation must be able to perform, and the value streams whose
// stages they perform.
//
// It is the registry Atlas owns. A capability record says what has to be done, who
// owns it in the business, what it has promised, and how it is currently done — by an
// executable process here, by a Worker, by a purchased system, or by a person. The
// last two are why the realization edge lives on this record rather than in a BPMN
// model: a capability nobody has automated has no model to carry the statement, and
// that is the normal state of most of a map on the day it is started.
//
// Everything mutable about a realization is resolved when a record is read and stored
// nowhere. A record holding "deployed, version 7, healthy" would be a second, stale
// copy of the deployment registry within a week, which is the rule ADR-0189 §4 set
// for Panorama's bindings and the reason it set it.
//
// The single-writer boundary is the [runloop.Loop] this service holds: every read and
// write of either store goes through it, and there is no other way from here to shared
// state (I3, ADR-0002/0147). Design-time only — nothing here reaches the event log,
// the processor, or recovery.
package capability

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// LandscapeResolver builds the picture of the installation this area compares the map
// against, filtered for the requesting principal. It is called from inside a loop
// turn — it reads the deployment registry, the application store and the worker
// registry, which only the loop may touch — and must not dispatch onto the loop again.
type LandscapeResolver func(r *http.Request) (Landscape, error)

// Clock supplies timestamps, injected so tests are deterministic.
type Clock func() time.Time

// HorizonResolver reads how many months a confirmation stays fresh for, from the
// installation's settings. Called from inside a loop turn, like the landscape resolver.
//
// It is a function rather than a field because an operator can change it while the
// server runs, and a read that answered from a value captured at construction would
// keep applying yesterday's policy.
type HorizonResolver func() (int, error)

// DefaultHorizonMonths is how long a confirmation stays fresh when an installation has
// not said otherwise. Twelve, matching the two places this repository already dates
// something it cannot verify: a Worker Type's setup steps (ADR-0289) and a decision
// record's open question (ADR-0293). Inherited rather than invented.
const DefaultHorizonMonths = 12

// Service serves the business-architecture area. Build it with [New].
type Service struct {
	// loop is the single-writer boundary. Every store access below runs on it.
	loop *runloop.Loop
	// caps and streams are the two records. Owned by the loop.
	caps    *Store
	streams *StreamStore
	// landscape is what the installation currently runs, resolved on the loop for the
	// requesting principal. Nothing it returns is ever written down.
	landscape LandscapeResolver
	// horizon reads the confirmation horizon from the installation's settings.
	horizon HorizonResolver
	now     Clock
	// measure reads what the engine recorded, off the run loop. It is settable after
	// construction rather than a constructor argument because it is the one resolver
	// this service can do without: a server that wires none answers the measurement
	// route with "this server cannot measure" and every other route unchanged.
	measure MeasurementResolver

	// Limits are the installation's resource budgets. New sets them to
	// [limits.Default]; the server overwrites them with its own once it has read the
	// environment (ADR-0291).
	Limits limits.Limits
}

// New builds the service over its two store directories.
func New(loop *runloop.Loop, caps *Store, streams *StreamStore, landscape LandscapeResolver,
	horizon HorizonResolver, now Clock) *Service {
	return &Service{loop: loop, caps: caps, streams: streams, landscape: landscape,
		horizon: horizon, now: now, Limits: limits.Default()}
}

// horizonMonths reads the configured horizon, falling back to the default when the
// service was built without a resolver or the settings cannot be read.
//
// A failure here must not be fatal to a listing or a report. The horizon decides how
// *loudly* a record is reported, never whether the answer is correct — so an unreadable
// setting degrades to the default rather than refusing the read, and the answer says
// which horizon it applied so a reader is never guessing.
func (s *Service) horizonMonths() int {
	if s.horizon == nil {
		return DefaultHorizonMonths
	}
	months, err := s.horizon()
	if err != nil || months == 0 {
		return DefaultHorizonMonths
	}
	return months
}

// budgets is how this service reads a ceiling. It defaults a Service built as a
// struct literal to [limits.Default], because the zero Limits is every ceiling at
// zero and a ceiling of zero admits nothing.
func (s *Service) budgets() limits.Limits {
	if s.Limits == (limits.Limits{}) {
		return limits.Default()
	}
	return s.Limits
}

// dispatch runs fn on the run loop and reports whether it ran at all.
//
// [runloop.Loop.Do] abandons the closure when the loop is shutting down, leaving every
// result variable at its zero value — and its own doc says a caller must read that as
// "not produced" rather than as an answer. For this area the difference is not
// academic. An abandoned listing is an empty list, an abandoned gap report reads as
// "nothing is wrong", and an abandoned create answers 201 for a record that was never
// written. Each of those is the most reassuring possible answer from a server that did
// nothing, which is the one answer a report like this must never give.
//
// So every closure says it ran, and a closure that did not becomes a refusal.
func (s *Service) dispatch(fn func()) bool {
	ran := false
	s.loop.Do(func() {
		ran = true
		fn()
	})
	return ran
}

// writeShuttingDown answers a request the run loop refused because the server is
// stopping.
func writeShuttingDown(w http.ResponseWriter) {
	httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
}

var errStale = errors.New("stale revision")

const (
	notFoundCapability  = "no such capability"
	notFoundValueStream = "no such value stream"
	conflictMessage     = "this record changed since you opened it; reload before saving so the other edit is not lost"
)

// HandleSubset serves every vocabulary this build accepts, plus the statement that
// there is no hierarchy. Served rather than duplicated in the browser: a picker
// offering a word the write path refuses is a promise the server breaks.
func (s *Service) HandleSubset(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, Subset())
}

// --- capabilities ----------------------------------------------------------

// HandleListCapabilities lists the map, filtered.
//
// The filters are the ones the method actually asks of a flat list: by tag (the only
// classification there is), by state, by whether anything realizes it, and by a
// substring of the name or key. `realized=false` is the adoption backlog, which is the
// query this list mostly exists to answer.
func (s *Service) HandleListCapabilities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tag := strings.TrimSpace(q.Get("tag"))
	state := strings.TrimSpace(q.Get("state"))
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	realized, hasRealized := boolParam(q.Get("realized"))
	stale, hasStale := boolParam(q.Get("stale"))

	out := []CapabilitySummary{}
	var opErr error
	if !s.dispatch(func() {
		now, horizon := s.now(), s.horizonMonths()
		all, err := s.caps.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, c := range all {
			if tag != "" && !hasTag(c.Tags, tag) {
				continue
			}
			if state != "" && c.State != state {
				continue
			}
			if hasRealized && (len(c.Realizations) > 0) != realized {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(c.Name), search) &&
				!strings.Contains(strings.ToLower(c.Key), search) {
				continue
			}
			row := summarizeCapability(c, now, horizon)
			if hasStale && row.Stale != stale {
				continue
			}
			out = append(out, row)
		}
	}) {
		writeShuttingDown(w)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list capabilities: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleCreateCapability files a new capability.
func (s *Service) HandleCreateCapability(w http.ResponseWriter, r *http.Request) {
	var c Capability
	if !decodeJSONLimit(w, r, &c, s.budgets().Request) {
		return
	}
	NormalizeCapability(&c)
	if findings := ValidateCapability(c); len(findings) > 0 {
		writeFindings(w, "the capability is not valid", findings)
		return
	}
	now := s.now().Unix()
	actor := requestActor(r)
	c.Revision, c.CreatedAt, c.CreatedBy, c.UpdatedAt, c.UpdatedBy = 1, now, actor, now, actor
	// Creating a record confirms it: somebody just wrote it down, which is an
	// assertion, and today is the honest date for it. Nothing is carried from the
	// request — a client cannot post a confirmation date it did not make.
	c.Confirmation = Confirmation{At: now, By: actor}

	var exists bool
	var opErr error
	if !s.dispatch(func() {
		if _, found, err := s.caps.Get(c.Key); err != nil {
			opErr = err
			return
		} else if found {
			exists = true
			return
		}
		opErr = s.caps.Save(c)
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case exists:
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"a capability with the key %q already exists — the key is the identity, so two "+
				"capabilities cannot share one", c.Key))
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save capability: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusCreated, c)
	}
}

// HandleGetCapability returns one whole capability.
func (s *Service) HandleGetCapability(w http.ResponseWriter, r *http.Request) {
	var c Capability
	var found bool
	var opErr error
	if !s.dispatch(func() { c, found, opErr = s.caps.Get(r.PathValue("key")) }) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read capability: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFoundCapability)
	default:
		httpapi.JSON(w, http.StatusOK, c)
	}
}

// HandleUpdateCapability replaces a capability's content.
//
// The whole record is sent rather than a patch, for the reason the information model
// takes a whole document: a patch language would be a second way to say everything the
// record already says. A stale revision is refused as a conflict.
//
// The key cannot change. It is the identity in the URL, on disk, in every `requires`
// and in every value-stream stage, so renaming it in place would silently break every
// reference to it. Renaming is a deliberate gap, not an oversight: export, edit,
// import.
func (s *Service) HandleUpdateCapability(w http.ResponseWriter, r *http.Request) {
	var next Capability
	if !decodeJSONLimit(w, r, &next, s.budgets().Request) {
		return
	}
	key := r.PathValue("key")
	NormalizeCapability(&next)
	if next.Key != "" && next.Key != key {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"this capability's key is %q and cannot be changed to %q: the key is the identity, and "+
				"every requires and value-stream stage naming it would be left pointing at nothing",
			key, next.Key))
		return
	}
	next.Key = key
	if findings := ValidateCapability(next); len(findings) > 0 {
		writeFindings(w, "the capability is not valid", findings)
		return
	}

	var found bool
	var opErr error
	var saved Capability
	if !s.dispatch(func() {
		current, exists, err := s.caps.Get(key)
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			return
		}
		found = true
		if next.Revision != 0 && next.Revision != current.Revision {
			opErr = errStale
			return
		}
		next.Revision = current.Revision + 1
		next.CreatedAt, next.CreatedBy = current.CreatedAt, current.CreatedBy
		// The confirmation is carried over untouched, and a client cannot set it here.
		// If a save refreshed the date, fixing a typo in the summary would assert that
		// the owner, the scope and every SLA had been re-read — the lie ADR-0289 names,
		// made automatic and therefore invisible. Confirming is its own call.
		next.Confirmation = current.Confirmation
		next.UpdatedAt, next.UpdatedBy = s.now().Unix(), requestActor(r)
		if opErr = s.caps.Save(next); opErr == nil {
			saved = next
		}
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case !found && opErr == nil:
		httpapi.Error(w, http.StatusNotFound, notFoundCapability)
	case errors.Is(opErr, errStale):
		httpapi.Error(w, http.StatusConflict, conflictMessage)
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save capability: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, saved)
	}
}

// DeletionResult is what a delete says about what it left behind.
type DeletionResult struct {
	Deleted string `json:"deleted"`
	// RequiredBy and Stages are the references now pointing at nothing.
	RequiredBy []string   `json:"requiredBy,omitempty"`
	Stages     []StageRef `json:"stages,omitempty"`
	// ValueStreams names the streams those stages belong to, in the same order.
	ValueStreams []string `json:"valueStreams,omitempty"`
}

// HandleDeleteCapability removes a capability and says what now dangles.
//
// It does not refuse a capability others reference, and it does not cascade. Refusing
// would mean editing every referrer before retiring anything, which makes a map that
// cannot be evolved; cascading would silently rewrite records the caller never
// mentioned. So it deletes the one thing asked for and answers with the references it
// has just broken — which the gap report will also carry, but a caller learning it a
// day later has already moved on.
//
// This is why the route answers 200 with a body rather than 204: there is something to
// say.
func (s *Service) HandleDeleteCapability(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	result := DeletionResult{Deleted: key}
	var found bool
	var opErr error
	if !s.dispatch(func() {
		_, exists, err := s.caps.Get(key)
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			return
		}
		found = true
		all, err := s.caps.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, other := range all {
			if other.Key == key {
				continue
			}
			for _, req := range other.Requires {
				if req == key {
					result.RequiredBy = append(result.RequiredBy, other.Key)
					break
				}
			}
		}
		streams, err := s.streams.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, v := range streams {
			for _, st := range v.Stages {
				for _, name := range st.Capabilities {
					if name == key {
						result.Stages = append(result.Stages, StageRef{Key: st.Key, Name: st.Name})
						result.ValueStreams = append(result.ValueStreams, v.Key)
						break
					}
				}
			}
		}
		opErr = s.caps.Delete(key)
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete capability: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFoundCapability)
	default:
		sort.Strings(result.RequiredBy)
		httpapi.JSON(w, http.StatusOK, result)
	}
}

// HandleCoverage resolves one capability against the installation: what actually does
// it, what it depends on and what those have promised, who depends on it, and which
// value-stream stages it performs.
func (s *Service) HandleCoverage(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var (
		rep    CoverageReport
		found  bool
		opErr  error
		target Capability
	)
	if !s.dispatch(func() {
		var exists bool
		target, exists, opErr = s.caps.Get(key)
		if opErr != nil || !exists {
			return
		}
		found = true
		all, err := s.caps.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		streams, err := s.streams.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		land, err := s.landscape(r)
		if err != nil {
			opErr = err
			return
		}
		rep = Coverage(target, all, streams, land, s.now(), s.horizonMonths())
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "resolve coverage: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFoundCapability)
	default:
		httpapi.JSON(w, http.StatusOK, rep)
	}
}

// HandleGaps compares the whole map against the installation.
//
// Everything it reads is design-time size — the map, the deployment registry, the
// call-activity graph — plus one O(1) maintained counter per definition (ADR-0080).
// Nothing here grows with the instance population, which is why it may run on the run
// loop at all (ADR-0239).
func (s *Service) HandleGaps(w http.ResponseWriter, r *http.Request) {
	var rep GapReport
	var opErr error
	if !s.dispatch(func() {
		caps, err := s.caps.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		streams, err := s.streams.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		land, err := s.landscape(r)
		if err != nil {
			opErr = err
			return
		}
		rep = Gaps(caps, streams, land, s.now(), s.horizonMonths())
	}) {
		writeShuttingDown(w)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "compare the map against this server: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

// --- value streams ---------------------------------------------------------

// HandleListValueStreams lists the value streams, optionally by tag.
func (s *Service) HandleListValueStreams(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	stale, hasStale := boolParam(r.URL.Query().Get("stale"))
	out := []ValueStreamSummary{}
	var opErr error
	if !s.dispatch(func() {
		now, horizon := s.now(), s.horizonMonths()
		all, err := s.streams.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, v := range all {
			if tag != "" && !hasTag(v.Tags, tag) {
				continue
			}
			row := summarizeValueStream(v, now, horizon)
			if hasStale && row.Stale != stale {
				continue
			}
			out = append(out, row)
		}
	}) {
		writeShuttingDown(w)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list value streams: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleCreateValueStream files a new value stream.
func (s *Service) HandleCreateValueStream(w http.ResponseWriter, r *http.Request) {
	var v ValueStream
	if !decodeJSONLimit(w, r, &v, s.budgets().Request) {
		return
	}
	NormalizeValueStream(&v)
	if findings := ValidateValueStream(v); len(findings) > 0 {
		writeFindings(w, "the value stream is not valid", findings)
		return
	}
	now := s.now().Unix()
	actor := requestActor(r)
	v.Revision, v.CreatedAt, v.CreatedBy, v.UpdatedAt, v.UpdatedBy = 1, now, actor, now, actor
	v.Confirmation = Confirmation{At: now, By: actor}

	var exists bool
	var opErr error
	if !s.dispatch(func() {
		if _, found, err := s.streams.Get(v.Key); err != nil {
			opErr = err
			return
		} else if found {
			exists = true
			return
		}
		opErr = s.streams.Save(v)
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case exists:
		httpapi.Error(w, http.StatusConflict,
			fmt.Sprintf("a value stream with the key %q already exists", v.Key))
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save value stream: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusCreated, v)
	}
}

// HandleGetValueStream returns one whole value stream.
func (s *Service) HandleGetValueStream(w http.ResponseWriter, r *http.Request) {
	var v ValueStream
	var found bool
	var opErr error
	if !s.dispatch(func() { v, found, opErr = s.streams.Get(r.PathValue("key")) }) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read value stream: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFoundValueStream)
	default:
		httpapi.JSON(w, http.StatusOK, v)
	}
}

// HandleUpdateValueStream replaces a value stream's content. The key cannot change,
// for the reason a capability's cannot.
func (s *Service) HandleUpdateValueStream(w http.ResponseWriter, r *http.Request) {
	var next ValueStream
	if !decodeJSONLimit(w, r, &next, s.budgets().Request) {
		return
	}
	key := r.PathValue("key")
	NormalizeValueStream(&next)
	if next.Key != "" && next.Key != key {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"this value stream's key is %q and cannot be changed to %q: the key is the identity", key, next.Key))
		return
	}
	next.Key = key
	if findings := ValidateValueStream(next); len(findings) > 0 {
		writeFindings(w, "the value stream is not valid", findings)
		return
	}

	var found bool
	var opErr error
	var saved ValueStream
	if !s.dispatch(func() {
		current, exists, err := s.streams.Get(key)
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			return
		}
		found = true
		if next.Revision != 0 && next.Revision != current.Revision {
			opErr = errStale
			return
		}
		next.Revision = current.Revision + 1
		next.CreatedAt, next.CreatedBy = current.CreatedAt, current.CreatedBy
		next.Confirmation = current.Confirmation
		next.UpdatedAt, next.UpdatedBy = s.now().Unix(), requestActor(r)
		if opErr = s.streams.Save(next); opErr == nil {
			saved = next
		}
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case !found && opErr == nil:
		httpapi.Error(w, http.StatusNotFound, notFoundValueStream)
	case errors.Is(opErr, errStale):
		httpapi.Error(w, http.StatusConflict, conflictMessage)
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save value stream: "+opErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, saved)
	}
}

// HandleDeleteValueStream removes a value stream. Nothing references a stream, so
// unlike a capability's delete there is nothing to report and this answers 204.
func (s *Service) HandleDeleteValueStream(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var found bool
	var opErr error
	if !s.dispatch(func() {
		_, exists, err := s.streams.Get(key)
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			return
		}
		found = true
		opErr = s.streams.Delete(key)
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete value stream: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFoundValueStream)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- helpers ---------------------------------------------------------------

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// boolParam reads an optional boolean query parameter, reporting whether it was given
// at all — "no filter" and "filter to false" are different questions, and for
// `realized` the second one is the adoption backlog.
func boolParam(raw string) (value, present bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, false
	case "false", "0", "no":
		return false, true
	default:
		return true, true
	}
}

// writeFindings answers an invalid record with every finding at once. An author
// fixing one field at a time through a form would otherwise make one round trip per
// mistake.
func writeFindings(w http.ResponseWriter, message string, findings []string) {
	httpapi.JSON(w, http.StatusBadRequest, map[string]any{
		"error":    message,
		"findings": findings,
	})
}

func requestActor(r *http.Request) string {
	if principal := httpapi.PrincipalFrom(r.Context()); principal != nil {
		return principal.Username
	}
	return ""
}

// SetMeasurementResolver wires the off-loop runtime read (ADR-0239). Called once at
// start-up, before the service serves anything.
func (s *Service) SetMeasurementResolver(m MeasurementResolver) { s.measure = m }
