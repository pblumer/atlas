// Package mihistory serves the bounded multi-instance history of a process
// instance (ADR-0517): the body executions of its multi-instance activities,
// cursor-paged iterations of one body execution with exact lookup and indexed
// filters, and one iteration's attributed incidents.
//
// Every read runs on a state.ReadView taken off the single-writer loop
// (invariant I3, ADR-0080): the service is handed a View that opens one, and has
// no other way to reach engine state. Every read is a bounded seek in the
// event-derived MI history index the engine's applyToState maintains; nothing
// here scans the instance's timeline, builds replay frames or reads variable
// snapshots, so the cost of a request is independent of how many iterations a
// body has. The legacy full timeline (GET /api/v1/instances/{key}/timeline) is
// unchanged.
//
// When the index of this installation is not proven complete (a store that
// predates it, ADR-0517 "readiness"), every route answers 409 with code
// "history_unavailable" — never an empty list, which would claim that no
// iterations ran.
package mihistory

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ErrLoopClosing is what a View returns when the server is shutting down and no
// read view could be opened.
var ErrLoopClosing = errors.New("server is shutting down")

// Definitions resolves a process-definition key to its compiled process, nil
// when the definition is no longer deployed. It must be safe off the loop.
type Definitions func(processDefKey uint64) *compiler.CompiledProcess

// View opens a read view off the single-writer loop, calls fn with it and the
// deployed definitions, and closes it. fn must not retain either.
type View func(fn func(rv *state.ReadView, defs Definitions) error) error

const (
	defaultLimit = 100
	// maxConcurrentReads bounds how many MI history reads run at once. Each is
	// bounded on its own; this keeps a burst of them from taking every CPU.
	maxConcurrentReads = 16
	// iterationIncidentCap bounds the incident records read per iteration row.
	iterationIncidentCap = 8
	// detailIncidentCap bounds the incident records one detail request returns.
	detailIncidentCap = 50
	// nestedBodyCap bounds the nested bodies one iteration detail lists.
	nestedBodyCap = 20
	// bodyOpenIncidentRounds bounds the iterations whose incidents a body
	// detail verifies when it counts open incidents.
	bodyOpenIncidentRounds = 100
	// detailVariableCap and detailDecisionCap bound one iteration detail's
	// variable changes and decision evaluations.
	detailVariableCap = 100
	detailDecisionCap = 20
	defaultCursorTTL  = time.Hour
)

// RenderVariable turns a stored variable into the kind and value an operator
// reads, exactly as the instance's other variable views render it — an
// enciphered personal value is named as such, never shown as ciphertext. The
// server supplies its own renderer so there is one definition of that rule.
type RenderVariable func(v *model.VariableValue) (kind, value string)

// Service serves the MI history routes. Build it with [New].
type Service struct {
	view      View
	key       []byte
	now       func() time.Time
	render    RenderVariable
	cursorTTL time.Duration
	sem       chan struct{}
}

// New builds the service with a fresh random cursor key. now is the wall clock
// cursor expiry is measured against; it never reaches engine state.
func New(view View, now func() time.Time, render RenderVariable) (*Service, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("mihistory: cursor key: %w", err)
	}
	s := newService(view, key, now)
	s.render = render
	return s, nil
}

func newService(view View, key []byte, now func() time.Time) *Service {
	return &Service{view: view, key: key, now: now, render: plainVariable, cursorTTL: defaultCursorTTL, sem: make(chan struct{}, maxConcurrentReads)}
}

// plainVariable is the renderer a test service uses when none is supplied.
func plainVariable(v *model.VariableValue) (string, string) {
	switch v.Kind {
	case model.VarBool:
		return "boolean", strconv.FormatBool(v.Bool)
	case model.VarNumber:
		return "number", v.Text
	case model.VarString:
		return "string", v.Text
	case model.VarJSON:
		return "json", v.Text
	}
	return "null", "null"
}

// BodyView is one multi-instance body execution.
type BodyView struct {
	BodyElementInstanceKey uint64 `json:"bodyElementInstanceKey"`
	ElementID              string `json:"elementId"`
	ProcessDefinitionKey   uint64 `json:"processDefinitionKey"`
	// Mode is "parallel", "sequential" or "loop" (a standard loop) as the
	// definition version that ran the body declares, "unknown" when that
	// version is no longer deployed.
	Mode               string `json:"mode"`
	State              string `json:"state"`
	ActivationPosition uint64 `json:"activationPosition"`
	EndPosition        uint64 `json:"endPosition,omitempty"`
	ParentIterationKey uint64 `json:"parentIterationKey,omitempty"`
	TokenID            uint64 `json:"tokenId"`
	ParentTokenID      uint64 `json:"parentTokenId"`
	// Iterations counts the iterations seeded so far. A sequential body seeds
	// one at a time, so while it runs this is not its planned total; the
	// planned total is not a recorded fact and is not reported.
	Iterations              uint64 `json:"iterations"`
	Active                  uint64 `json:"active"`
	Completed               uint64 `json:"completed"`
	Terminated              uint64 `json:"terminated"`
	IncidentsRaised         uint64 `json:"incidentsRaised"`
	IterationsWithIncidents uint64 `json:"iterationsWithIncidents"`
	// OpenIncidents is set on the single-body route only: incidents attributed
	// to this body that are open now. OpenIncidentsExact is false when more
	// were attributed than one request verifies.
	OpenIncidents      *int  `json:"openIncidents,omitempty"`
	OpenIncidentsExact *bool `json:"openIncidentsExact,omitempty"`
}

// IterationView is one iteration of a body execution.
type IterationView struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	// Iteration is the iteration's loopCounter, null when none was recorded.
	Iteration          *uint64 `json:"iteration"`
	State              string  `json:"state"`
	ActivationPosition uint64  `json:"activationPosition"`
	EndPosition        uint64  `json:"endPosition,omitempty"`
	IncidentsRaised    uint64  `json:"incidentsRaised"`
	OpenIncidents      int     `json:"openIncidents"`
	OpenIncidentsExact bool    `json:"openIncidentsExact"`
}

// IncidentView is one incident attributed to an iteration or body.
type IncidentView struct {
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	RaisedPosition     uint64 `json:"raisedPosition"`
	EndPosition        uint64 `json:"endPosition,omitempty"`
	// Status: "open" (recorded now), "resolved" (an operator resolved it),
	// "superseded" (a later incident replaced it on the same element), or
	// "cleared" (its element ended while it was open; no resolution recorded).
	Status string `json:"status"`
}

// BodiesResponse answers GET /api/v1/instances/{key}/multi-instances.
type BodiesResponse struct {
	InstanceKey uint64 `json:"instanceKey"`
	// ProcessDefinitionKey and InstanceState describe the instance itself, so a
	// client can draw its diagram and state without the full timeline.
	ProcessDefinitionKey uint64     `json:"processDefinitionKey"`
	InstanceState        string     `json:"instanceState"`
	ParentIterationKey   uint64     `json:"parentIterationKey,omitempty"`
	HighWaterPosition    uint64     `json:"highWaterPosition"`
	Bodies               []BodyView `json:"bodies"`
	NextCursor           string     `json:"nextCursor,omitempty"`
}

// BodyResponse answers GET /api/v1/instances/{key}/multi-instances/{bodyKey}.
type BodyResponse struct {
	InstanceKey uint64   `json:"instanceKey"`
	Body        BodyView `json:"body"`
}

// IterationsResponse answers .../multi-instances/{bodyKey}/iterations.
type IterationsResponse struct {
	InstanceKey       uint64          `json:"instanceKey"`
	Body              BodyView        `json:"body"`
	HighWaterPosition uint64          `json:"highWaterPosition"`
	Iterations        []IterationView `json:"iterations"`
	// NextCursor is set while more matching iterations may follow. A page cut
	// short by the server's per-request work bound can hold fewer rows than
	// the limit, even none, and still carry a cursor.
	NextCursor string `json:"nextCursor,omitempty"`
}

// IterationResponse answers .../iterations/{iterationKey}.
type IterationResponse struct {
	InstanceKey        uint64         `json:"instanceKey"`
	Body               BodyView       `json:"body"`
	Iteration          IterationView  `json:"iteration"`
	Incidents          []IncidentView `json:"incidents"`
	IncidentsTruncated bool           `json:"incidentsTruncated"`
	NestedBodies       []BodyView     `json:"nestedBodies"`
	NestedTruncated    bool           `json:"nestedBodiesTruncated"`
	// Variables are the recorded changes of the iteration's own scope, oldest
	// first: its loopCounter and input item as the engine bound them, and what it
	// wrote locally. A contribution to the body's output collection is recorded
	// on the body's collection, not per iteration, and does not appear here.
	Variables          []VariableChange `json:"variables"`
	VariablesTruncated bool             `json:"variablesTruncated"`
	// Decisions are the DMN evaluations attributed to the iteration, with the
	// inputs and outputs the worker froze into the log — never re-evaluated.
	// DecisionsAvailable is false when this store's history predates decision
	// attribution: then an empty list means "not known", not "none".
	Decisions          []DecisionView `json:"decisions"`
	DecisionsAvailable bool           `json:"decisionsAvailable"`
	DecisionsTruncated bool           `json:"decisionsTruncated"`
}

// VariableChange is one recorded change of a variable in an iteration's scope.
type VariableChange struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	Position    uint64 `json:"position"`
	At          int64  `json:"at"`
	ProducerKey uint64 `json:"producerKey,omitempty"`
}

// DecisionView is one decision evaluation attributed to an iteration.
type DecisionView struct {
	DecisionID         string          `json:"decisionId"`
	ElementInstanceKey uint64          `json:"elementInstanceKey"`
	Position           uint64          `json:"position"`
	At                 int64           `json:"at"`
	DecisionKey        uint64          `json:"decisionKey,omitempty"`
	Inputs             json.RawMessage `json:"inputs"`
	Outputs            json.RawMessage `json:"outputs"`
	// Trace is the rule trace, only with ?technical=true: it is detail for the
	// person who asks for it, not the default reading.
	Trace json.RawMessage `json:"trace,omitempty"`
}

// rawJSON passes a stored canonical JSON text through, and quotes anything that
// is not valid JSON so the response stays well-formed.
func rawJSON(text string) json.RawMessage {
	if text == "" {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	quoted, _ := json.Marshal(text)
	return quoted
}

// apiError is a request the service answers with a non-2xx status. The body
// keeps the API's {"error": "..."} envelope and adds a stable "code".
type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(code, msg string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: code, msg: msg}
}

var (
	errUnavailable = &apiError{status: http.StatusConflict, code: "history_unavailable",
		msg: "The multi-instance history of this installation was recorded before the bounded history index existed and has not been verified as complete. " +
			"Iterations are not listed, because an incomplete list would look like a complete one; this does not mean no iterations ran. " +
			"The full instance timeline is still available."}
	errInstanceNotFound = &apiError{status: http.StatusNotFound, code: "not_found", msg: "process instance not found (it may never have existed, or its history was purged)"}
	errBodyNotFound     = &apiError{status: http.StatusNotFound, code: "not_found", msg: "multi-instance body execution not found in this instance"}
	errIterNotFound     = &apiError{status: http.StatusNotFound, code: "not_found", msg: "iteration not found in this multi-instance body execution"}
	errStale            = &apiError{status: http.StatusConflict, code: "cursor_stale",
		msg: "the iterations changed state since the first page was read, so a state-filtered page sequence cannot continue without skipping or repeating rows; reload from the first page"}
	errBusy = &apiError{status: http.StatusServiceUnavailable, code: "busy", msg: "too many multi-instance history reads are running; retry shortly"}
)

func writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		httpapi.JSON(w, ae.status, map[string]string{"error": ae.msg, "code": ae.code})
	case errors.Is(err, state.ErrMIHistoryIndexUnavailable), errors.Is(err, state.ErrOutlineIndexUnavailable):
		writeError(w, errUnavailable)
	case errors.Is(err, ErrLoopClosing):
		httpapi.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error(), "code": "shutting_down"})
	default:
		// Corruption or I/O: say so without echoing internals beyond the cause.
		httpapi.JSON(w, http.StatusInternalServerError, map[string]string{"error": "read multi-instance history: " + err.Error(), "code": "internal"})
	}
}

// read runs fn on a fresh read view, bounded by the concurrency limit.
func (s *Service) read(r *http.Request, fn func(rv *state.ReadView, defs Definitions) error) error {
	select {
	case s.sem <- struct{}{}:
	default:
		return errBusy
	}
	defer func() { <-s.sem }()
	if err := r.Context().Err(); err != nil {
		return err
	}
	return s.view(func(rv *state.ReadView, defs Definitions) error {
		ready, err := rv.MIHistoryIndexReady()
		if err != nil {
			return err
		}
		if !ready {
			return errUnavailable
		}
		return fn(rv, defs)
	})
}

func pathUint(r *http.Request, name string) (uint64, error) {
	v, err := strconv.ParseUint(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, badRequest("invalid_parameter", "invalid "+name)
	}
	return v, nil
}

func queryUint(r *http.Request, name string) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		return 0, badRequest("invalid_parameter", name+" must be a positive integer")
	}
	return v, nil
}

func queryLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > state.MIMaxPageLimit {
		return 0, badRequest("invalid_parameter", fmt.Sprintf("limit must be an integer between 1 and %d", state.MIMaxPageLimit))
	}
	return n, nil
}

// requireInstance answers not-found for an instance that does not exist or
// whose history was purged; purge also removes its MI index ranges.
func requireInstance(rv *state.ReadView, key uint64) error {
	_, err := readInstance(rv, key)
	return err
}

func readInstance(rv *state.ReadView, key uint64) (*model.ProcessInstanceValue, error) {
	pi, ok, err := rv.ProcessInstance(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errInstanceNotFound
	}
	return pi, nil
}

func bodyView(e state.MIHistoryEntry, defs Definitions) BodyView {
	v := BodyView{
		BodyElementInstanceKey: e.ElementInstanceKey, ProcessDefinitionKey: e.ProcessDefKey,
		Mode: "unknown", State: e.State.String(), ActivationPosition: e.ActivationPosition,
		EndPosition: e.EndPosition, ParentIterationKey: e.ParentIterationKey,
		TokenID: e.TokenID, ParentTokenID: e.ParentTokenID,
		Iterations: e.Iterations, Active: e.ActiveIterations(), Completed: e.Completed,
		Terminated: e.Terminated, IncidentsRaised: e.IncidentsRaised,
		IterationsWithIncidents: e.IterationsWithIncidents,
	}
	// The body's own definition key is the version that ran it, recorded on its
	// activation event — after a migration it is not the instance's current one.
	if cp := defs(e.ProcessDefKey); cp != nil && e.ElementID >= 0 && int(e.ElementID) < cp.NodeCount() {
		v.ElementID = cp.ElementBpmnId(e.ElementID)
		if node := cp.Node(e.ElementID); node.MultiInstance >= 0 {
			d := cp.MultiInstance(node.MultiInstance)
			switch {
			case d.Standard:
				v.Mode = "loop"
			case d.Sequential:
				v.Mode = "sequential"
			default:
				v.Mode = "parallel"
			}
		}
	}
	return v
}

func iterationView(e state.MIHistoryEntry) IterationView {
	v := IterationView{
		ElementInstanceKey: e.ElementInstanceKey, State: e.State.String(),
		ActivationPosition: e.ActivationPosition, EndPosition: e.EndPosition,
		IncidentsRaised: e.IncidentsRaised, OpenIncidentsExact: true,
	}
	if e.LoopCounter != 0 {
		n := e.LoopCounter
		v.Iteration = &n
	}
	return v
}

// incidentStatus reads whether an unresolved attribution is still open: the
// current-incident table is authoritative, because an element that ends with
// an open incident clears it without an event (ADR-0061).
func incidentStatus(rv *state.ReadView, rec state.MIIncidentRecord) (string, error) {
	switch rec.Outcome {
	case state.MIIncidentResolved:
		return "resolved", nil
	case state.MIIncidentSuperseded:
		return "superseded", nil
	}
	open, err := rv.IncidentOpen(rec.ElementInstanceKey)
	if err != nil {
		return "", err
	}
	if open {
		return "open", nil
	}
	return "cleared", nil
}

// openIncidents counts the open incidents attributed to one owner, reading at
// most limit records. exact is false when more records exist.
func openIncidents(rv *state.ReadView, pi, owner uint64, limit int) (n int, exact bool, err error) {
	more, err := rv.MIIncidents(pi, owner, limit, func(rec state.MIIncidentRecord) error {
		st, err := incidentStatus(rv, rec)
		if st == "open" {
			n++
		}
		return err
	})
	return n, !more, err
}

// HandleBodies serves GET /api/v1/instances/{key}/multi-instances.
func (s *Service) HandleBodies(w http.ResponseWriter, r *http.Request) {
	resp, err := s.bodies(r)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

func (s *Service) bodies(r *http.Request) (*BodiesResponse, error) {
	key, err := pathUint(r, "key")
	if err != nil {
		return nil, err
	}
	limit, err := queryLimit(r)
	if err != nil {
		return nil, err
	}
	parent, err := queryUint(r, "parentIteration")
	if err != nil {
		return nil, err
	}
	digest := filterDigest("bodies|parent=" + strconv.FormatUint(parent, 10))
	var cur *cursor
	if text := r.URL.Query().Get("cursor"); text != "" {
		c, err := s.checkCursor(text, kindBodies, key, 0, digest)
		if err != nil {
			return nil, err
		}
		cur = &c
	}
	resp := &BodiesResponse{InstanceKey: key, ParentIterationKey: parent, Bodies: []BodyView{}}
	err = s.read(r, func(rv *state.ReadView, defs Definitions) error {
		pi, err := readInstance(rv, key)
		if err != nil {
			return err
		}
		resp.ProcessDefinitionKey, resp.InstanceState = pi.ProcessDefKey, pi.State.String()
		q := state.MIBodyPageQuery{ParentIteration: parent, Limit: limit}
		if cur != nil {
			q.HighWater = cur.highWater
			q.After = &state.MIBodyPosition{ActivationPosition: cur.afterA, BodyKey: cur.afterB}
		} else if q.HighWater, err = rv.LastAppliedPosition(); err != nil {
			return err
		}
		resp.HighWaterPosition = q.HighWater
		next, err := rv.MIBodyPage(key, q, func(e state.MIHistoryEntry) error {
			resp.Bodies = append(resp.Bodies, bodyView(e, defs))
			return nil
		})
		if err != nil || next == nil {
			return err
		}
		resp.NextCursor = s.encodeCursor(cursor{
			kind: kindBodies, instance: key, filter: digest, highWater: q.HighWater,
			afterA: next.ActivationPosition, afterB: next.BodyKey, issuedAt: s.now().Unix(),
		})
		return nil
	})
	return resp, err
}

// checkCursor decodes a cursor and holds it to the request it is presented with.
func (s *Service) checkCursor(text string, kind byte, instance, body, digest uint64) (cursor, error) {
	c, err := s.decodeCursor(text, s.now())
	switch {
	case errors.Is(err, errCursorExpired):
		return c, badRequest("cursor_expired", "cursor expired; reload from the first page")
	case err != nil:
		return c, badRequest("invalid_cursor", "invalid cursor: "+err.Error())
	case c.kind != kind || c.instance != instance || c.body != body:
		return c, badRequest("cursor_mismatch", "cursor was issued for a different instance, body execution or route")
	case c.filter != digest:
		return c, badRequest("cursor_mismatch", "cursor was issued for different filters; keep the filters of the first page or start again")
	}
	return c, nil
}

// HandleBody serves GET /api/v1/instances/{key}/multi-instances/{bodyKey}.
func (s *Service) HandleBody(w http.ResponseWriter, r *http.Request) {
	key, err := pathUint(r, "key")
	if err == nil {
		var bodyKey uint64
		if bodyKey, err = pathUint(r, "bodyKey"); err == nil {
			var resp *BodyResponse
			if resp, err = s.body(r, key, bodyKey); err == nil {
				httpapi.JSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	writeError(w, err)
}

func (s *Service) body(r *http.Request, key, bodyKey uint64) (*BodyResponse, error) {
	resp := &BodyResponse{InstanceKey: key}
	err := s.read(r, func(rv *state.ReadView, defs Definitions) error {
		body, err := loadBody(rv, key, bodyKey)
		if err != nil {
			return err
		}
		resp.Body = bodyView(*body, defs)
		open, exact, err := openIncidents(rv, key, bodyKey, detailIncidentCap)
		if err != nil {
			return err
		}
		if body.IterationsWithIncidents > 0 {
			page, err := rv.MIIterationPage(key, bodyKey, body.ActivationPosition, state.MIIterationQuery{
				IncidentsOnly: true, Limit: bodyOpenIncidentRounds, ScanBudget: bodyOpenIncidentRounds,
			}, func(e state.MIHistoryEntry) error {
				n, ex, err := openIncidents(rv, key, e.ElementInstanceKey, iterationIncidentCap)
				open += n
				exact = exact && ex
				return err
			})
			if err != nil {
				return err
			}
			exact = exact && page.Next == nil
		}
		resp.Body.OpenIncidents, resp.Body.OpenIncidentsExact = &open, &exact
		return nil
	})
	return resp, err
}

func loadBody(rv *state.ReadView, key, bodyKey uint64) (*state.MIHistoryEntry, error) {
	if err := requireInstance(rv, key); err != nil {
		return nil, err
	}
	body, ok, err := rv.MIBody(key, bodyKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errBodyNotFound
	}
	return body, nil
}

// iterationFilter is the parsed, canonical filter set of an iterations page.
type iterationFilter struct {
	state     state.MIHistoryState
	incidents bool
	iteration uint64
	from      uint64
}

func (f iterationFilter) canonical() string {
	return fmt.Sprintf("iterations|state=%d|incidents=%t|iteration=%d|from=%d", f.state, f.incidents, f.iteration, f.from)
}

func parseIterationFilter(r *http.Request) (iterationFilter, error) {
	var f iterationFilter
	q := r.URL.Query()
	if raw := q.Get("state"); raw != "" {
		st, ok := state.ParseMIHistoryState(raw)
		if !ok {
			return f, badRequest("invalid_parameter", "state must be one of active, completed, terminated")
		}
		f.state = st
	}
	switch q.Get("incidents") {
	case "", "false":
	case "true":
		f.incidents = true
	default:
		return f, badRequest("invalid_parameter", "incidents must be true or false")
	}
	var err error
	if f.iteration, err = queryUint(r, "iteration"); err != nil {
		return f, err
	}
	if f.from, err = queryUint(r, "from"); err != nil {
		return f, err
	}
	if f.iteration != 0 && f.from != 0 {
		return f, badRequest("invalid_parameter", "iteration and from cannot be combined")
	}
	return f, nil
}

// HandleIterations serves GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations.
func (s *Service) HandleIterations(w http.ResponseWriter, r *http.Request) {
	resp, err := s.iterations(r)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

func (s *Service) iterations(r *http.Request) (*IterationsResponse, error) {
	key, err := pathUint(r, "key")
	if err != nil {
		return nil, err
	}
	bodyKey, err := pathUint(r, "bodyKey")
	if err != nil {
		return nil, err
	}
	limit, err := queryLimit(r)
	if err != nil {
		return nil, err
	}
	filter, err := parseIterationFilter(r)
	if err != nil {
		return nil, err
	}
	exactKey, err := queryUint(r, "elementInstanceKey")
	if err != nil {
		return nil, err
	}
	cursorText := r.URL.Query().Get("cursor")
	if exactKey != 0 && (cursorText != "" || filter != (iterationFilter{})) {
		return nil, badRequest("invalid_parameter", "elementInstanceKey is an exact lookup and cannot be combined with filters or a cursor")
	}
	digest := filterDigest(filter.canonical())
	var cur *cursor
	if cursorText != "" {
		c, err := s.checkCursor(cursorText, kindIterations, key, bodyKey, digest)
		if err != nil {
			return nil, err
		}
		cur = &c
	}
	resp := &IterationsResponse{InstanceKey: key, Iterations: []IterationView{}}
	err = s.read(r, func(rv *state.ReadView, defs Definitions) error {
		body, err := loadBody(rv, key, bodyKey)
		if err != nil {
			return err
		}
		resp.Body = bodyView(*body, defs)
		add := func(e state.MIHistoryEntry) error {
			v := iterationView(e)
			if e.IncidentsRaised > 0 {
				if v.OpenIncidents, v.OpenIncidentsExact, err = openIncidents(rv, key, e.ElementInstanceKey, iterationIncidentCap); err != nil {
					return err
				}
			}
			resp.Iterations = append(resp.Iterations, v)
			return nil
		}
		if exactKey != 0 {
			resp.HighWaterPosition, err = rv.LastAppliedPosition()
			if err != nil {
				return err
			}
			e, ok, err := rv.MIIteration(key, exactKey)
			if err != nil || !ok || e.BodyElementKey != bodyKey {
				return err
			}
			return add(*e)
		}
		q := state.MIIterationQuery{
			State: filter.state, IncidentsOnly: filter.incidents, LoopCounter: filter.iteration, Limit: limit,
		}
		bodyVersion := body.Changes
		if cur != nil {
			if cur.bodyPos != body.ActivationPosition {
				return badRequest("cursor_mismatch", "cursor was issued for a different body execution")
			}
			if filter.state != 0 && body.Changes != cur.bodyVersion {
				return errStale
			}
			q.HighWater, bodyVersion = cur.highWater, cur.bodyVersion
			q.After = &state.MIIterationPosition{LoopCounter: cur.afterA, RoundKey: cur.afterB}
		} else {
			if q.HighWater, err = rv.LastAppliedPosition(); err != nil {
				return err
			}
			q.From = filter.from
		}
		resp.HighWaterPosition = q.HighWater
		page, err := rv.MIIterationPage(key, bodyKey, body.ActivationPosition, q, add)
		if err != nil || page.Next == nil {
			return err
		}
		resp.NextCursor = s.encodeCursor(cursor{
			kind: kindIterations, instance: key, body: bodyKey, bodyPos: body.ActivationPosition,
			filter: digest, highWater: q.HighWater, bodyVersion: bodyVersion,
			afterA: page.Next.LoopCounter, afterB: page.Next.RoundKey, issuedAt: s.now().Unix(),
		})
		return nil
	})
	return resp, err
}

// HandleIteration serves GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations/{iterationKey}.
func (s *Service) HandleIteration(w http.ResponseWriter, r *http.Request) {
	resp, err := s.iteration(r)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

func (s *Service) iteration(r *http.Request) (*IterationResponse, error) {
	key, err := pathUint(r, "key")
	if err != nil {
		return nil, err
	}
	bodyKey, err := pathUint(r, "bodyKey")
	if err != nil {
		return nil, err
	}
	iterKey, err := pathUint(r, "iterationKey")
	if err != nil {
		return nil, err
	}
	technical := r.URL.Query().Get("technical") == "true"
	resp := &IterationResponse{InstanceKey: key, Incidents: []IncidentView{}, NestedBodies: []BodyView{},
		Variables: []VariableChange{}, Decisions: []DecisionView{}}
	err = s.read(r, func(rv *state.ReadView, defs Definitions) error {
		body, err := loadBody(rv, key, bodyKey)
		if err != nil {
			return err
		}
		resp.Body = bodyView(*body, defs)
		e, ok, err := rv.MIIteration(key, iterKey)
		if err != nil {
			return err
		}
		if !ok || e.BodyElementKey != bodyKey {
			return errIterNotFound
		}
		resp.Iteration = iterationView(*e)
		open := 0
		more, err := rv.MIIncidents(key, iterKey, detailIncidentCap, func(rec state.MIIncidentRecord) error {
			st, err := incidentStatus(rv, rec)
			if st == "open" {
				open++
			}
			resp.Incidents = append(resp.Incidents, IncidentView{
				ElementInstanceKey: rec.ElementInstanceKey, RaisedPosition: rec.RaisedPosition,
				EndPosition: rec.EndPosition, Status: st,
			})
			return err
		})
		if err != nil {
			return err
		}
		resp.IncidentsTruncated = more
		resp.Iteration.OpenIncidents, resp.Iteration.OpenIncidentsExact = open, !more
		next, err := rv.MIBodyPage(key, state.MIBodyPageQuery{ParentIteration: iterKey, Limit: nestedBodyCap}, func(b state.MIHistoryEntry) error {
			resp.NestedBodies = append(resp.NestedBodies, bodyView(b, defs))
			return nil
		})
		resp.NestedTruncated = next != nil
		if err != nil {
			return err
		}
		if resp.VariablesTruncated, err = rv.VariableSnapshotPage(iterKey, detailVariableCap, func(at int64, pos uint64, v *model.VariableValue) error {
			kind, value := s.render(v)
			resp.Variables = append(resp.Variables, VariableChange{
				Name: v.Name, Kind: kind, Value: value, Position: pos, At: at, ProducerKey: v.ProducerKey,
			})
			return nil
		}); err != nil {
			return err
		}
		resp.DecisionsAvailable = true
		resp.DecisionsTruncated, err = rv.MIDecisions(key, iterKey, detailDecisionCap, func(at int64, pos uint64, d *model.DecisionEvaluationValue) error {
			dv := DecisionView{
				DecisionID: d.DecisionId, ElementInstanceKey: d.ElementInstanceKey, Position: pos, At: at,
				DecisionKey: d.DecisionKey, Inputs: rawJSON(d.InputsJSON), Outputs: rawJSON(d.OutputsJSON),
			}
			if technical && d.TraceJSON != "" {
				dv.Trace = rawJSON(d.TraceJSON)
			}
			resp.Decisions = append(resp.Decisions, dv)
			return nil
		})
		if errors.Is(err, state.ErrMIDecisionIndexUnavailable) {
			resp.DecisionsAvailable, err = false, nil
		}
		return err
	})
	return resp, err
}

// OutlineStepView is one step of an instance's outline: an element activation
// or end outside every multi-instance iteration.
type OutlineStepView struct {
	Position           uint64 `json:"position"`
	At                 int64  `json:"at"`
	Action             string `json:"action"` // activated, completed, terminated
	ElementID          string `json:"elementId"`
	Type               string `json:"type,omitempty"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	TokenID            uint64 `json:"tokenId"`
	ParentTokenID      uint64 `json:"parentTokenId"`
	// ProcessDefinitionKey is the version the element ran under, as its event
	// recorded it — after a migration not necessarily the instance's current one.
	ProcessDefinitionKey uint64 `json:"processDefinitionKey"`
	// MultiInstanceBody marks a body execution: its iterations are behind it,
	// in the iterations route.
	MultiInstanceBody bool `json:"multiInstanceBody,omitempty"`
}

// OutlineResponse answers GET /api/v1/instances/{key}/outline.
type OutlineResponse struct {
	InstanceKey          uint64            `json:"instanceKey"`
	ProcessDefinitionKey uint64            `json:"processDefinitionKey"`
	InstanceState        string            `json:"instanceState"`
	HighWaterPosition    uint64            `json:"highWaterPosition"`
	Steps                []OutlineStepView `json:"steps"`
	NextCursor           string            `json:"nextCursor,omitempty"`
}

var replayActions = map[byte]string{
	state.ReplayActivated:  "activated",
	state.ReplayCompleted:  "completed",
	state.ReplayTerminated: "terminated",
}

// HandleOutline serves GET /api/v1/instances/{key}/outline: the instance's steps
// in the order they happened, without the steps inside multi-instance
// iterations, cursor-paged. It is how a client draws the history of an instance
// whose full timeline is too large to load.
func (s *Service) HandleOutline(w http.ResponseWriter, r *http.Request) {
	resp, err := s.outline(r)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

func (s *Service) outline(r *http.Request) (*OutlineResponse, error) {
	key, err := pathUint(r, "key")
	if err != nil {
		return nil, err
	}
	limit, err := queryLimit(r)
	if err != nil {
		return nil, err
	}
	digest := filterDigest("outline")
	var cur *cursor
	if text := r.URL.Query().Get("cursor"); text != "" {
		c, err := s.checkCursor(text, kindOutline, key, 0, digest)
		if err != nil {
			return nil, err
		}
		cur = &c
	}
	resp := &OutlineResponse{InstanceKey: key, Steps: []OutlineStepView{}}
	err = s.read(r, func(rv *state.ReadView, defs Definitions) error {
		pi, err := readInstance(rv, key)
		if err != nil {
			return err
		}
		resp.ProcessDefinitionKey, resp.InstanceState = pi.ProcessDefKey, pi.State.String()
		var after *state.OutlinePosition
		if cur != nil {
			resp.HighWaterPosition = cur.highWater
			after = &state.OutlinePosition{At: int64(cur.afterA), Position: cur.afterB}
		} else if resp.HighWaterPosition, err = rv.LastAppliedPosition(); err != nil {
			return err
		}
		next, err := rv.OutlinePage(key, after, resp.HighWaterPosition, limit, func(o state.OutlineStep) error {
			v := OutlineStepView{
				Position: o.Position, At: o.At, Action: replayActions[o.Action],
				ElementInstanceKey: o.ElementInstanceKey, TokenID: o.TokenID, ParentTokenID: o.ParentTokenID,
				ProcessDefinitionKey: o.ProcessDefKey, MultiInstanceBody: o.MultiInstance == 1,
			}
			if cp := defs(o.ProcessDefKey); cp != nil && o.ElementID >= 0 && int(o.ElementID) < cp.NodeCount() {
				v.ElementID = cp.ElementBpmnId(o.ElementID)
				v.Type = cp.Node(o.ElementID).Type.String()
			}
			resp.Steps = append(resp.Steps, v)
			return nil
		})
		if err != nil || next == nil {
			return err
		}
		resp.NextCursor = s.encodeCursor(cursor{
			kind: kindOutline, instance: key, filter: digest, highWater: resp.HighWaterPosition,
			afterA: uint64(next.At), afterB: next.Position, issuedAt: s.now().Unix(),
		})
		return nil
	})
	return resp, err
}
