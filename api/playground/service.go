// Package playground serves the Modeler's Playground area: a caller opens a
// session on a model, feeds it cases, and drives it — free-running, or one
// occurrence at a time with a person answering the human tasks.
//
// The engine work is in [github.com/pblumer/atlas/playground]; this package is
// the HTTP half. It is a per-area service (ADR-0147) with an unusual property:
// it holds no run loop of its own, because it touches no state the server's loop
// owns. Every sandbox carries its own single-writer goroutine, and the only way
// into one is [playground.Session.With] — the boundary travels with the session
// rather than with this service.
//
// Nothing here reaches the durable engine, the deployment registry or any
// design-time store. Resolving *which* model a session runs is the one thing that
// does, and that is injected as [ModelSource] so the server keeps its own
// authorization rules over drafts and deployments.
package playground

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// maxModelBytes caps an inline model in an open request. A BPMN diagram is text;
// this is a sanity bound on the request body, not a tuning knob.
const maxModelBytes = 8 << 20 // 8 MiB

// maxBodyBytes caps every other request body. They are small: variables, a stub
// policy, a duration.
const maxBodyBytes = 1 << 20 // 1 MiB

// ModelSource resolves the model a session is asked to run and decides whether
// this request may read it.
//
// It returns the BPMN XML on success, or an HTTP status and message to answer
// with. Both halves belong to the server: it owns the draft store, the deployment
// registry, and the per-artifact authorization rules that say who may open a
// draft (ADR-0071). Putting them behind one function keeps this service free of
// design-time state and of policy alike.
type ModelSource func(r *http.Request, kind, ref string) (xml []byte, status int, message string)

// VarsFromMap converts a name→value map in encoding/json shape into engine
// variables. Injected rather than reimplemented so a playground case is seeded
// exactly as a real start is.
type VarsFromMap func(map[string]any) ([]model.VariableValue, error)

// Service serves the Playground area. Build it with [New].
type Service struct {
	sessions *playground.Registry
	source   ModelSource
	vars     VarsFromMap
	// budget bounds every run this service starts. A caller does not get to ask
	// for an unbounded one: a sandbox is a live engine on somebody's server.
	budget playground.Budget
}

// New builds the Playground service over a session registry.
func New(sessions *playground.Registry, source ModelSource, vars VarsFromMap) *Service {
	return &Service{sessions: sessions, source: source, vars: vars, budget: playground.DefaultBudget()}
}

// --- wire shapes -------------------------------------------------------------

// stubReq is one element's answer in a run's policy.
type stubReq struct {
	MinMillis      int64          `json:"minMillis,omitempty"`
	MaxMillis      int64          `json:"maxMillis,omitempty"`
	Outputs        map[string]any `json:"outputs,omitempty"`
	FailPerMillion int32          `json:"failPerMillion,omitempty"`
	FailMessage    string         `json:"failMessage,omitempty"`
	ErrorCode      string         `json:"errorCode,omitempty"`
}

// openReq opens a session. Source is "draft", "process" or "xml"; Ref names the
// draft's process id or the deployment key, and XML carries the model itself for
// "xml" — the shape the Modeler uses for an unsaved diagram.
type openReq struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	XML    string `json:"xml,omitempty"`
	// Root names the process to run in a model that holds several.
	Root string `json:"root,omitempty"`
	// StartTime is the simulated instant the run begins at, RFC 3339. Empty starts
	// at the request's wall-clock time, so a report reads like today.
	StartTime string `json:"startTime,omitempty"`
	// Seed makes the run reproducible. Zero seeds it from the clock, and the
	// response says which seed was used so the run can be repeated.
	Seed int64 `json:"seed,omitempty"`
	// Stubs is the answering policy; omitted fields park the jobs they cover. It is
	// fixed for the sandbox's life: a run is only comparable with another run if
	// the policy behind them is the same, so changing it means a new session.
	Stubs struct {
		Default   *stubReq           `json:"default,omitempty"`
		Human     *stubReq           `json:"human,omitempty"`
		ByElement map[string]stubReq `json:"byElement,omitempty"`
		// Pools are the sets of workers elements compete for, and PoolOf assigns an
		// element to one by name. Without them every case is worked the instant it
		// arrives and the report's waiting column is zero by construction.
		Pools  map[string]poolReq `json:"pools,omitempty"`
		PoolOf map[string]string  `json:"poolOf,omitempty"`
	} `json:"stubs"`
}

// poolReq is a pool on the wire.
type poolReq struct {
	Capacity int         `json:"capacity"`
	Calendar calendarReq `json:"calendar,omitempty"`
}

// sessionResp is a session's state.
type sessionResp struct {
	ID        string `json:"id"`
	ProcessID string `json:"processId"`
	Seed      int64  `json:"seed"`
	// SimTime is where simulated time stands, RFC 3339.
	SimTime   string `json:"simTime"`
	CreatedAt string `json:"createdAt"`
	LastUsed  string `json:"lastUsed"`
	Paused    bool   `json:"paused"`
	// OpenTasks is how many jobs are waiting for a person.
	OpenTasks int `json:"openTasks"`
}

type caseResp struct {
	InstanceKey string            `json:"instanceKey"`
	State       string            `json:"state"`
	Path        []string          `json:"path"`
	Variables   map[string]string `json:"variables"`
	Incidents   int               `json:"incidents"`
}

type taskResp struct {
	JobKey      string `json:"jobKey"`
	InstanceKey string `json:"instanceKey"`
	Element     string `json:"element"`
	Human       bool   `json:"human"`
}

type progressResp struct {
	Occurrences int    `json:"occurrences"`
	Quiescent   bool   `json:"quiescent"`
	SimTime     string `json:"simTime"`
	Paused      bool   `json:"paused"`
}

type occurrenceResp struct {
	Happened bool   `json:"happened"`
	Kind     string `json:"kind,omitempty"`
	Element  string `json:"element,omitempty"`
	At       string `json:"at,omitempty"`
	SimTime  string `json:"simTime"`
}

// --- handlers ----------------------------------------------------------------

// HandleOpen opens a session on a draft, a deployed definition, or an inline
// model.
func (s *Service) HandleOpen(w http.ResponseWriter, r *http.Request) {
	var req openReq
	if !decode(w, r, maxModelBytes, &req) {
		return
	}

	var xml []byte
	switch req.Source {
	case "xml":
		if req.XML == "" {
			httpapi.Error(w, http.StatusBadRequest, "source \"xml\" needs a model in xml")
			return
		}
		xml = []byte(req.XML)
	case "draft", "process":
		if req.Ref == "" {
			httpapi.Error(w, http.StatusBadRequest, "source "+strconv.Quote(req.Source)+" needs a ref")
			return
		}
		var status int
		var msg string
		xml, status, msg = s.source(r, req.Source, req.Ref)
		if status != 0 {
			httpapi.Error(w, status, msg)
			return
		}
	default:
		httpapi.Error(w, http.StatusBadRequest, "source must be \"draft\", \"process\" or \"xml\"")
		return
	}

	start := time.Now().UTC()
	if req.StartTime != "" {
		t, err := time.Parse(time.RFC3339, req.StartTime)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "startTime: "+err.Error())
			return
		}
		start = t
	}
	seed := req.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	stubs, err := s.stubSet(req)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	sess, err := s.sessions.Open(principal(r), playground.Options{
		ModelXML: xml, Root: req.Root, StartTime: start, Seed: seed, Stubs: stubs,
	})
	if err != nil {
		// A model that does not compile is the caller's problem; a full registry is
		// a capacity problem. Both are refusals, not server faults.
		httpapi.Error(w, http.StatusConflict, err.Error())
		return
	}
	s.respondSession(w, sess, seed)
}

// stubSet converts the wire policy into an engine one.
func (s *Service) stubSet(req openReq) (playground.StubSet, error) {
	out := playground.StubSet{}
	var err error
	if req.Stubs.Default != nil {
		if out.Default, err = s.stub(*req.Stubs.Default); err != nil {
			return out, fmt.Errorf("stubs.default: %w", err)
		}
	}
	if req.Stubs.Human != nil {
		if out.Human, err = s.stub(*req.Stubs.Human); err != nil {
			return out, fmt.Errorf("stubs.human: %w", err)
		}
	}
	if len(req.Stubs.Pools) > 0 {
		out.Pools = make(map[string]playground.Pool, len(req.Stubs.Pools))
		for name, p := range req.Stubs.Pools {
			out.Pools[name] = playground.Pool{Capacity: p.Capacity, Calendar: p.Calendar.toCalendar()}
		}
	}
	if len(req.Stubs.PoolOf) > 0 {
		out.PoolOf = make(map[string]string, len(req.Stubs.PoolOf))
		for element, name := range req.Stubs.PoolOf {
			out.PoolOf[element] = name
		}
	}
	if len(req.Stubs.ByElement) > 0 {
		out.ByElement = make(map[string]playground.Stub, len(req.Stubs.ByElement))
		for id, sr := range req.Stubs.ByElement {
			st, e := s.stub(sr)
			if e != nil {
				return out, fmt.Errorf("stubs.byElement[%s]: %w", id, e)
			}
			out.ByElement[id] = *st
		}
	}
	return out, nil
}

func (s *Service) stub(sr stubReq) (*playground.Stub, error) {
	if sr.MinMillis < 0 || sr.MaxMillis < 0 {
		return nil, fmt.Errorf("a duration cannot be negative")
	}
	if sr.FailPerMillion < 0 || sr.FailPerMillion > 1_000_000 {
		return nil, fmt.Errorf("failPerMillion must be between 0 and 1000000")
	}
	outputs, err := s.vars(sr.Outputs)
	if err != nil {
		return nil, err
	}
	return &playground.Stub{
		Min:            time.Duration(sr.MinMillis) * time.Millisecond,
		Max:            time.Duration(sr.MaxMillis) * time.Millisecond,
		Outputs:        outputs,
		FailPerMillion: sr.FailPerMillion,
		FailMessage:    sr.FailMessage,
		ErrorCode:      sr.ErrorCode,
	}, nil
}

// HandleStatus reports a session's state.
func (s *Service) HandleStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	s.respondSession(w, sess, 0)
}

// HandleClose ends a session and discards its sandbox.
func (s *Service) HandleClose(w http.ResponseWriter, r *http.Request) {
	// Resolve through session() first: closing somebody else's sandbox is as much
	// theirs to decide as reading it.
	if _, ok := s.session(w, r); !ok {
		return
	}
	// A session that went away between the lookup and here — the reaper got it, or
	// another tab closed it — is gone, which is what the caller asked for. Reporting
	// that race as a failure would be a worse answer than the truth.
	_ = s.sessions.Close(r.PathValue("id"))
	httpapi.JSON(w, http.StatusOK, map[string]bool{"closed": true})
}

// HandleStartCase starts one case, seeded with the given variables.
func (s *Service) HandleStartCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Variables map[string]any `json:"variables"`
	}
	if !decode(w, r, maxBodyBytes, &body) {
		return
	}
	vars, err := s.vars(body.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	var out caseResp
	s.run(w, r, func(sb *playground.Sandbox) error {
		key, err := sb.StartCase(vars...)
		if err != nil {
			return err
		}
		c, err := sb.Case(key)
		if err != nil {
			return err
		}
		out = renderCase(c)
		return nil
	}, &out)
}

// HandleRun runs the session until the model comes to rest, the service's budget
// stops it, or somebody pauses it.
func (s *Service) HandleRun(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	var out progressResp
	err := sess.With(func(sb *playground.Sandbox) error {
		prog, err := sb.Run(sess.Budget(s.budget))
		if err != nil {
			return err
		}
		out = progressResp{
			Occurrences: prog.Occurrences, Quiescent: prog.Quiescent,
			SimTime: rfc3339(prog.SimTime), Paused: sess.Paused(),
		}
		return nil
	})
	if !s.ok(w, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleStep carries out exactly one occurrence.
func (s *Service) HandleStep(w http.ResponseWriter, r *http.Request) {
	var out occurrenceResp
	s.run(w, r, func(sb *playground.Sandbox) error {
		occ, happened, err := sb.Step()
		if err != nil {
			return err
		}
		out = occurrenceResp{Happened: happened, SimTime: rfc3339(sb.Now())}
		if happened {
			out.Kind, out.Element, out.At = occurrenceKind(occ.Kind), occ.Element, rfc3339(occ.At)
		}
		return nil
	}, &out)
}

// HandlePause holds a run in flight at its next occurrence.
func (s *Service) HandlePause(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	sess.Pause()
	httpapi.JSON(w, http.StatusOK, map[string]bool{"paused": true})
}

// HandleResume lets a paused session run again.
func (s *Service) HandleResume(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	sess.Resume()
	httpapi.JSON(w, http.StatusOK, map[string]bool{"paused": false})
}

// HandleAdvanceClock jumps simulated time and fires whatever came due.
func (s *Service) HandleAdvanceClock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Millis int64 `json:"millis"`
	}
	if !decode(w, r, maxBodyBytes, &body) {
		return
	}
	if body.Millis <= 0 {
		httpapi.Error(w, http.StatusBadRequest, "millis must be positive: simulated time does not run backwards")
		return
	}
	var out struct {
		SimTime string `json:"simTime"`
	}
	s.run(w, r, func(sb *playground.Sandbox) error {
		if err := sb.Advance(time.Duration(body.Millis) * time.Millisecond); err != nil {
			return err
		}
		out.SimTime = rfc3339(sb.Now())
		return nil
	}, &out)
}

// HandlePublishMessage delivers a message into the sandbox — the author standing
// in for the outside world.
func (s *Service) HandlePublishMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string         `json:"name"`
		CorrelationKey string         `json:"correlationKey"`
		Variables      map[string]any `json:"variables"`
	}
	if !decode(w, r, maxBodyBytes, &body) {
		return
	}
	vars, err := s.vars(body.Variables)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Name == "" {
		httpapi.Error(w, http.StatusBadRequest, "a message needs a name")
		return
	}
	var out struct {
		Published bool `json:"published"`
	}
	s.run(w, r, func(sb *playground.Sandbox) error {
		if err := sb.PublishMessage(body.Name, body.CorrelationKey, vars...); err != nil {
			return err
		}
		out.Published = true
		return nil
	}, &out)
}

// HandleTasks lists the jobs waiting for a person.
func (s *Service) HandleTasks(w http.ResponseWriter, r *http.Request) {
	out := []taskResp{}
	s.run(w, r, func(sb *playground.Sandbox) error {
		tasks, err := sb.OpenTasks()
		if err != nil {
			return err
		}
		for _, t := range tasks {
			out = append(out, taskResp{
				JobKey:      strconv.FormatUint(t.JobKey, 10),
				InstanceKey: strconv.FormatUint(t.InstanceKey, 10),
				Element:     t.Element, Human: t.Human,
			})
		}
		return nil
	}, &out)
}

// HandleCompleteTask completes a parked job the way the person would have.
func (s *Service) HandleCompleteTask(w http.ResponseWriter, r *http.Request) {
	jobKey, err := strconv.ParseUint(r.PathValue("jobKey"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid job key")
		return
	}
	var body struct {
		Variables map[string]any `json:"variables"`
	}
	if !decode(w, r, maxBodyBytes, &body) {
		return
	}
	vars, verr := s.vars(body.Variables)
	if verr != nil {
		httpapi.Error(w, http.StatusBadRequest, verr.Error())
		return
	}

	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	var completeErr error
	withErr := sess.With(func(sb *playground.Sandbox) error {
		completeErr = sb.CompleteTask(jobKey, vars...)
		return nil
	})
	if !s.ok(w, withErr) {
		return
	}
	if completeErr != nil {
		// The job is not waiting: the author is looking at a stale task list, which
		// is a conflict rather than a fault.
		httpapi.Error(w, http.StatusConflict, completeErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]bool{"completed": true})
}

// HandleCase reports what became of one case.
func (s *Service) HandleCase(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("caseKey"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid case key")
		return
	}
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	var (
		out     caseResp
		readErr error
	)
	withErr := sess.With(func(sb *playground.Sandbox) error {
		c, e := sb.Case(key)
		if e != nil {
			readErr = e
			return nil
		}
		out = renderCase(c)
		return nil
	})
	if !s.ok(w, withErr) {
		return
	}
	if readErr != nil {
		httpapi.Error(w, http.StatusNotFound, readErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleOverlay reports how many tokens have passed through each element — the
// heat map's raw material, in the shape the runtime overlay already uses.
func (s *Service) HandleOverlay(w http.ResponseWriter, r *http.Request) {
	out := map[string]int64{}
	s.run(w, r, func(sb *playground.Sandbox) error {
		visits, err := sb.ElementVisits()
		if err != nil {
			return err
		}
		out = visits
		return nil
	}, &out)
}

// --- plumbing ----------------------------------------------------------------

// principal names the authenticated caller, or "" when authentication is off.
func principal(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.Username
	}
	return ""
}

// session resolves the session named in the path, answering 404 when it is gone
// — or when it belongs to somebody else.
//
// Somebody else's session is "not found" rather than "forbidden" on purpose: the
// id is a 128-bit secret handed only to its opener, and a different answer for an
// id that exists would turn it into an oracle. A session can hold the variables
// of a draft its owner alone may read, so this is a read boundary, not tidiness.
func (s *Service) session(w http.ResponseWriter, r *http.Request) (*playground.Session, bool) {
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok || !sess.OwnedBy(principal(r)) {
		httpapi.Error(w, http.StatusNotFound, "no playground session with that id")
		return nil, false
	}
	return sess, true
}

// run resolves the session, executes fn inside it, and writes out on success.
func (s *Service) run(w http.ResponseWriter, r *http.Request, fn func(*playground.Sandbox) error, out any) {
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.ok(w, sess.With(fn)) {
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// ok reports whether the call succeeded, writing the error response when it did
// not. A session that closed under the caller is a 404, not a 500: it is gone,
// and retrying the same id will not bring it back.
func (s *Service) ok(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case playground.ErrClosedSession(err):
		httpapi.Error(w, http.StatusNotFound, "the playground session has been closed")
	default:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	}
	return false
}

// respondSession writes a session's state. seed is echoed on open, where the
// caller has to learn which seed the run got; it is 0 elsewhere.
func (s *Service) respondSession(w http.ResponseWriter, sess *playground.Session, seed int64) {
	out := sessionResp{
		ID: sess.ID(), Seed: seed, Paused: sess.Paused(),
		CreatedAt: rfc3339(sess.CreatedAt()), LastUsed: rfc3339(sess.LastUsed()),
	}
	err := sess.With(func(sb *playground.Sandbox) error {
		out.ProcessID = sb.ProcessID()
		out.SimTime = rfc3339(sb.Now())
		tasks, err := sb.OpenTasks()
		if err != nil {
			return err
		}
		out.OpenTasks = len(tasks)
		return nil
	})
	if !s.ok(w, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// decode reads a JSON body with UseNumber, so a variable's exact decimal form
// survives into FEEL. An empty body decodes to the zero value: every request here
// has sensible defaults.
func decode(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func renderCase(c playground.CaseResult) caseResp {
	out := caseResp{
		InstanceKey: strconv.FormatUint(c.InstanceKey, 10),
		State:       c.State.String(),
		Path:        c.Path,
		Variables:   c.Variables,
		Incidents:   c.Incidents,
	}
	if out.Path == nil {
		out.Path = []string{}
	}
	return out
}

func occurrenceKind(k playground.OccurrenceKind) string {
	switch k {
	case playground.OccJobFailed:
		return "jobFailed"
	case playground.OccJobError:
		return "jobError"
	case playground.OccTimer:
		return "timer"
	default:
		return "jobCompleted"
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
