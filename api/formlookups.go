package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/formdecision"
	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/compiler"
)

// What a task form's remote data source may reach (ADR-0473).
//
// The lookup service holds no state and no store; these closures are its whole reach
// into this server, written here so the single writer (I3), the task's object question
// (ADR-0317) and the Worker store stay in the package that owns them. Nothing here is on
// the job path: a lookup is a query put to a Worker Instance through the broker, which
// lives beside the engine and never in it.

// lookupRatePerSecond and lookupBurst are the per-person budget for directory searches.
// A person typing with the form's debounce makes about three requests a second at the
// very most; twenty in a burst and five a second sustained leaves that untouched and
// stops a script from walking the directory through somebody's task.
const (
	lookupBurst         = 20
	lookupRatePerSecond = 5
)

// newFormLookups builds the broker, the worker half of the query protocol and the lookup
// service. It runs after the options, so the budgets are the ones this server was
// configured with.
func (s *Server) newFormLookups() {
	s.queryBroker = workerquery.NewBroker(workerquery.DefaultLimits())
	s.workerQueries = workerquery.NewService(s.queryBroker, s.budgets().Request)
	s.lookupRate = newRateLimiter(lookupBurst, lookupRatePerSecond)
	s.formLookup = formlookup.New(formlookup.Config{
		Task:    s.lookupTask,
		Worker:  s.lookupWorker,
		Workers: s.lookupWorkers,
		Ask:     s.queryBroker.Ask,
		Allow:   s.lookupRate.allow,
		Principal: func(r *http.Request) string {
			if p := httpapi.PrincipalFrom(r.Context()); p != nil {
				return p.UserID
			}
			return ""
		},
		MaxBody: s.budgets().Request,
	})
}

// lookupTask asks the question completing the task asks — may this caller work on it —
// and returns the task's form as the form store holds it now.
func (s *Server) lookupTask(r *http.Request, key uint64) (formlookup.Task, error) {
	auth, err := s.mayWorkTask(r, key)
	switch {
	case err != nil:
		return formlookup.Task{}, err
	case !auth.known:
		return formlookup.Task{}, formlookup.ErrNoTask
	case !auth.allowed:
		return formlookup.Task{}, formlookup.ErrForbidden
	}
	schema, found, err := s.taskFormSchema(key)
	if err != nil {
		return formlookup.Task{}, err
	}
	if !found {
		// A task without a form has no directory field to search; to the caller this
		// is the same as a field that does not exist.
		return formlookup.Task{Schema: []byte(`{}`)}, nil
	}
	return formlookup.Task{Schema: schema}, nil
}

// taskFormSchema reads the form a user task binds, by the id its compiled model names.
// found is false for a task without a form, or one whose form was deleted.
func (s *Server) taskFormSchema(key uint64) (schema []byte, found bool, err error) {
	s.do(func() {
		jv, ok, gErr := s.store.GetJob(key)
		if gErr != nil {
			err = gErr
			return
		}
		if !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
			return
		}
		formID := strings.TrimSpace(s.enrichTask(key, jv).FormID)
		if formID == "" {
			return
		}
		rec, ok, fErr := s.forms.Get(formID)
		if fErr != nil {
			err = fErr
			return
		}
		if ok {
			schema, found = []byte(rec.Schema), true
		}
	})
	return schema, found, err
}

// lookupWorker resolves a configured Worker by name. Names are unique per store, which
// the create handler enforces.
func (s *Server) lookupWorker(name string) (formlookup.Worker, bool, error) {
	var (
		out     formlookup.Worker
		found   bool
		loadErr error
	)
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			loadErr = err
			return
		}
		for _, c := range recs {
			if c.Name == name {
				out, found = workerForLookup(c), true
				return
			}
		}
	})
	return out, found, loadErr
}

// lookupWorkers lists the configured Workers for the form editor. Like the Worker picker
// on a service task it lists catalog entries for everyone who may author — existence is
// not configuration (ADR-0205) — and what travels is a name, a type and two switches.
func (s *Server) lookupWorkers(*http.Request) ([]formlookup.Worker, error) {
	var (
		recs    []connector
		loadErr error
	)
	s.do(func() { recs, loadErr = s.connectors.LoadAll() })
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]formlookup.Worker, 0, len(recs))
	for _, c := range recs {
		out = append(out, workerForLookup(c))
	}
	return out, nil
}

func workerForLookup(c connector) formlookup.Worker {
	return formlookup.Worker{Name: c.Name, WorkerType: c.Kind, Enabled: c.Enabled, FormLookups: c.FormLookups}
}

// formLookupKinds are the Worker Types a Worker's editors may open for form lookups:
// those a remote data source is offered for.
func formLookupKind(kind string) bool {
	for _, p := range formlookup.Providers() {
		if p.WorkerType == kind {
			return true
		}
	}
	return false
}

// refuseFormLookups is the message for a Worker of a kind no form can search.
func refuseFormLookups(kind string) string {
	return fmt.Sprintf("formLookups can only be switched on for a Worker a form can search (%s), not for a %s Worker",
		strings.Join(lookupKinds(), ", "), kind)
}

func lookupKinds() []string {
	var out []string
	for _, p := range formlookup.Providers() {
		out = append(out, p.WorkerType)
	}
	return out
}

// checkTaskSubmission holds a completion's variables to the task form's directory
// fields, and to the lists of its fields whose options come from a decision
// (formdecisions.go), before the processor is told anything. A form without either is
// not looked at. It answers true when it wrote a refusal.
func (s *Server) checkTaskSubmission(w http.ResponseWriter, r *http.Request, key uint64, body []byte) bool {
	schema, found, err := s.taskFormSchema(key)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read task form: "+err.Error())
		return true
	}
	if !found {
		return false
	}
	lookups := strings.Contains(string(schema), formlookup.DataSourceMember)
	decisions := strings.Contains(string(schema), formdecision.Member)
	if !lookups && !decisions {
		return false
	}
	vars, err := submittedVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return true
	}
	if lookups {
		if refusal := s.formLookup.CheckSubmission(r.Context(), schema, vars); refusal != nil {
			refusal.Write(w)
			return true
		}
	}
	if decisions {
		if refusal := s.formDecision.CheckSubmission(r.Context(), schema, vars); refusal != nil {
			refusal.Write(w)
			return true
		}
	}
	return false
}

// submittedVariables reads a completion body as the plain values it submits, before
// anything is enciphered: the check is of what the person chose, and it runs before
// the command exists. An empty body submits nothing.
func submittedVariables(body []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var payload struct {
		Variables map[string]any `json:"variables"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	if payload.Variables == nil {
		payload.Variables = map[string]any{}
	}
	return payload.Variables, nil
}

// refuseDirectoryFieldInPublic refuses a public form — a start link or a public wizard
// step — whose form carries a directory field. Nobody is signed in there, so nobody can
// search, and the value cannot be checked either: a check that answered "no such person"
// to an anonymous visitor would make the public door an oracle for who is in the
// directory. The field is for task forms; on a public form it fails closed. It answers
// true when it wrote the refusal.
func refuseDirectoryFieldInPublic(w http.ResponseWriter, schema string) bool {
	if !strings.Contains(schema, formlookup.DataSourceMember) {
		return false
	}
	form, err := formlookup.Parse([]byte(schema))
	if err != nil || len(form.Fields) > 0 {
		httpapi.Error(w, http.StatusBadRequest,
			"this form has a directory field, which only a signed-in person can answer in a task; it cannot be submitted through a public link")
		return true
	}
	return false
}
