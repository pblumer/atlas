package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/formlookup"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/startgrant"
	"github.com/pblumer/atlas/model"
)

// The self-service half of ADR-0516: the routes a signed-in person reaches to see,
// and later start, the processes an administrator granted them. They are a second
// door to the same engine start the operator routes use, which is why they are
// Server methods beside it rather than an area service of their own: the start
// path, the deployment registry and the form checks are the server's, and a copy
// of any of them would be a second start implementation (ADR-0516, I4).
//
// The user role is the coarse gate the route table enforces. It is never enough:
// every request also needs a person's own session and a current grant, checked on
// the run loop against the newest deployed version, and a server without
// authentication offers none of it.

// maxStartableProcesses bounds the self-service list. A person is granted a few
// processes, not hundreds; a list longer than this says so with truncated.
const maxStartableProcesses = 200

// startableProcess is one process the signed-in person may start now, as the
// self-service list answers it. Nothing here names a grant, an audience or another
// person: what the list says is that this person may start this version.
type startableProcess struct {
	// Key is the definition key of the newest deployed version — the key the
	// self-service start names. It is not a permission: the start checks again.
	Key             uint64             `json:"key"`
	ProcessID       string             `json:"processId"`
	Name            string             `json:"name"`
	Version         int32              `json:"version"`
	ApplicationID   string             `json:"applicationId"`
	ApplicationName string             `json:"applicationName"`
	StartForm       startableStartForm `json:"startForm"`
}

// startableStartForm says whether the version starts from a form and which: a
// process without one starts with no values at all.
type startableStartForm struct {
	Available bool   `json:"available"`
	FormID    string `json:"formId,omitempty"`
}

// selfServicePrincipal is the gate in front of every self-service route. An open
// server answers 404: there is no such resource there, and its operators keep the
// Start view they have (ADR-0516). A credential that is not a person's own session
// — an API token, the internal token, an application a person connected — is
// refused even when it carries the user role: the right to start is the person's,
// and delegating it needs a design of its own.
func (s *Server) selfServicePrincipal(w http.ResponseWriter, r *http.Request) (*httpapi.Principal, bool) {
	if !s.authEnabled {
		httpapi.Error(w, http.StatusNotFound,
			"starting a process yourself needs authentication; on this server processes are started from the Start view")
		return nil, false
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	if !p.Session || strings.TrimSpace(p.UserID) == "" {
		httpapi.Error(w, http.StatusForbidden,
			"starting a process yourself needs your own signed-in session; API tokens and connected applications cannot")
		return nil, false
	}
	return p, true
}

// grantPrincipal is the request's identity as the grant store reads it: the user id
// and the groups the session carries now, which every membership change updates in
// place (ADR-0185).
func grantPrincipal(p *httpapi.Principal) startgrant.Principal {
	return startgrant.Principal{UserID: p.UserID, GroupIDs: append([]string(nil), p.GroupIDs...)}
}

// handleMyStartableProcesses answers the processes the signed-in person may start
// now: GET /api/v1/me/startable-processes.
func (s *Server) handleMyStartableProcesses(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfServicePrincipal(w, r)
	if !ok {
		return
	}
	principal := grantPrincipal(p)
	var (
		items     []startableProcess
		err       error
		uncertain bool
	)
	s.do(func() {
		if uncertain = s.startGrants.Uncertain(); uncertain {
			return
		}
		items, err = s.startableOnLoop(s.startGrants.GrantedTo(principal))
	})
	switch {
	case uncertain:
		// Fail closed, and say so: an empty list would read as "nothing granted".
		httpapi.Error(w, http.StatusServiceUnavailable, "the processes you may start cannot be read right now; try again later")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "startable processes: "+err.Error())
	default:
		page, total, truncated := pageStartable(items, maxStartableProcesses)
		httpapi.JSON(w, http.StatusOK, map[string]any{
			"items": page, "total": total, "totalExact": true, "truncated": truncated,
		})
	}
}

// startableOnLoop resolves granted scopes to the versions the person may start
// now. One pass over the registry finds the newest version of every granted
// process — the version latestDeploymentByProcessID answers, so the list, the
// administrator's preview and the start agree — and each application is read once.
// Runs on the loop.
func (s *Server) startableOnLoop(grants []startgrant.Grant) ([]startableProcess, error) {
	out := []startableProcess{}
	if len(grants) == 0 {
		return out, nil
	}
	want := make(map[string]bool, len(grants))
	for _, g := range grants {
		want[g.ProcessID] = true
	}
	newest := make(map[string]*deployment, len(want))
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || !want[d.ProcessID] || d.Version != s.versions[d.ProcessID] {
			continue
		}
		newest[d.ProcessID] = d
	}
	type appRecord struct {
		p     project
		found bool
	}
	apps := map[string]appRecord{}
	for _, g := range grants {
		a, seen := apps[g.ApplicationID]
		if !seen {
			p, found, err := s.projects.Get(g.ApplicationID)
			if err != nil {
				return nil, err
			}
			a = appRecord{p: p, found: found}
			apps[g.ApplicationID] = a
		}
		d := newest[g.ProcessID]
		if t := s.targetFor(g.ApplicationID, a.p, a.found, d); !t.ProcessInApplication || len(t.Blockers) > 0 {
			continue
		}
		name := d.Name
		if name == "" {
			name = d.ProcessID
		}
		form := d.cp.StartFormId()
		out = append(out, startableProcess{
			Key: d.Key, ProcessID: d.ProcessID, Name: name, Version: d.Version,
			ApplicationID: g.ApplicationID, ApplicationName: a.p.Name,
			StartForm: startableStartForm{Available: form != "", FormID: form},
		})
	}
	return out, nil
}

// pageStartable orders the list the way a person scans it — by application, then by
// process name — with ids as tie-breakers so the order is total, and cuts it at
// limit.
func pageStartable(items []startableProcess, limit int) ([]startableProcess, int, bool) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if x, y := strings.ToLower(a.ApplicationName), strings.ToLower(b.ApplicationName); x != y {
			return x < y
		}
		if a.ApplicationID != b.ApplicationID {
			return a.ApplicationID < b.ApplicationID
		}
		if x, y := strings.ToLower(a.Name), strings.ToLower(b.Name); x != y {
			return x < y
		}
		if a.ProcessID != b.ProcessID {
			return a.ProcessID < b.ProcessID
		}
		return a.Key < b.Key
	})
	total := len(items)
	if total > limit {
		return items[:limit], total, true
	}
	return items, total, false
}

// noStartableKey is the one answer for a key that names no definition this person
// can see: unknown, malformed, or not readable. It says nothing about which.
const noStartableKey = "no process you may start has that key"

// selfStartDecisionOnLoop is the authorization of one self-service start of the
// definition d. It runs on the loop — once to refuse early and read the start form,
// and again as the start guard, in the turn that submits the command, which is the
// check that counts. The answers:
//
//   - 503 while the grant store is uncertain after a failed write: fail closed;
//   - 403 when the person holds no current grant for d's application and process.
//     The definition itself is no secret — every signed-in identity lists the
//     deployed definitions (GET /api/v1/processes, definitionReadable) — so saying
//     "not yours to start" tells them nothing the listing does not;
//   - 409 when they hold one but d is not what it would start now: a superseded
//     version, or a version the shared blocker rule refuses (deactivated, not
//     executable, trigger-only, missing constants, application gone or protected,
//     newest version filed under another application). Nothing is created, and the
//     list they reload offers the version that is current.
func (s *Server) selfStartDecisionOnLoop(d *deployment, p startgrant.Principal) (int, string) {
	if s.startGrants.Uncertain() {
		return http.StatusServiceUnavailable, "the processes you may start cannot be checked right now; try again later"
	}
	if !s.startGrants.Allowed(d.ProjectID, d.ProcessID, p) {
		return http.StatusForbidden, "you may not start this process"
	}
	app, found, err := s.projects.Get(d.ProjectID)
	if err != nil {
		return http.StatusInternalServerError, "read application: " + err.Error()
	}
	newest := s.latestDeploymentByProcessID(d.ProcessID)
	if newest == nil || newest.Key != d.Key {
		return http.StatusConflict, "this version of the process has been replaced; reload the list of processes you may start"
	}
	if t := s.targetFor(d.ProjectID, app, found, newest); !t.ProcessInApplication || len(t.Blockers) > 0 {
		return http.StatusConflict, "this process cannot be started right now"
	}
	return 0, ""
}

// selfStartBody reads a self-service start body strictly: nothing, or one JSON
// object holding variables and nothing else, bounded by the Settings budget the
// public form start reads too.
func (s *Server) selfStartBody(r *http.Request) ([]model.VariableValue, []byte, int, error) {
	limit := s.budgets().Settings
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, nil, http.StatusBadRequest, errors.New("read body: " + err.Error())
	}
	if int64(len(body)) > limit {
		return nil, nil, http.StatusRequestEntityTooLarge, errors.New("a start is at most " + strconv.FormatInt(limit, 10) + " bytes")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, body, 0, nil
	}
	var payload struct {
		Variables map[string]any `json:"variables"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return nil, nil, http.StatusBadRequest, errors.New("invalid JSON body: " + err.Error())
	}
	if dec.More() {
		return nil, nil, http.StatusBadRequest, errors.New("invalid JSON body: trailing data after the JSON object")
	}
	vars, err := startVarsFromMap(payload.Variables)
	if err != nil {
		return nil, nil, http.StatusBadRequest, err
	}
	return vars, body, 0, nil
}

// handleMyProcessStart starts one instance of a granted process for the signed-in
// person: POST /api/v1/me/processes/{key}/instances (ADR-0516).
//
// Nothing a previous listing said is taken for granted. The key is resolved, the
// grant and the version checked, the values held to the start form — and then the
// one start path every door uses runs with the same authorization as its guard, in
// the turn that submits the command. Success is answered only after that path has
// driven the command through the log and its fsync, and the instance records the
// person as startedBy (ADR-0475 §3).
func (s *Server) handleMyProcessStart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfServicePrincipal(w, r)
	if !ok {
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusNotFound, noStartableKey)
		return
	}
	vars, body, code, err := s.selfStartBody(r)
	if err != nil {
		httpapi.Error(w, code, err.Error())
		return
	}
	principal := grantPrincipal(p)
	var (
		msg    string
		formID string
		schema string
	)
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok || !definitionReadable(p, d) {
			code, msg = http.StatusNotFound, noStartableKey
			return
		}
		if code, msg = s.selfStartDecisionOnLoop(d, principal); code != 0 {
			return
		}
		if formID = d.cp.StartFormId(); formID == "" {
			return
		}
		f, found, err := s.forms.Get(formID)
		switch {
		case err != nil:
			code, msg = http.StatusInternalServerError, "read start form: "+err.Error()
		case !found:
			// The model names a form that is gone. Starting anyway would take whatever
			// the caller sent with nothing to hold it to.
			code, msg = http.StatusConflict, "this process's start form is missing, so it cannot be started"
		default:
			schema = f.Schema
		}
	})
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if formID == "" {
		if len(vars) > 0 {
			httpapi.Error(w, http.StatusBadRequest, "this process starts without values; send none")
			return
		}
	} else if !s.checkSelfStartForm(w, r, schema, body, vars) {
		return
	}
	s.startInstanceGuarded(w, r, key, vars, func(d *deployment) (int, string) {
		return s.selfStartDecisionOnLoop(d, principal)
	})
}

// checkSelfStartForm holds a submission to its start form: the form's own fields
// and no other path (the shape check every public form uses), a decision field's
// value among the decision's options, and the constraints the form declares. It
// writes the refusal and answers false when one fails.
func (s *Server) checkSelfStartForm(w http.ResponseWriter, r *http.Request, schema string, body []byte, vars []model.VariableValue) bool {
	if strings.Contains(schema, formlookup.DataSourceMember) {
		if form, err := formlookup.Parse([]byte(schema)); err != nil || len(form.Fields) > 0 {
			// A directory field is answered through a task's own lookup route, which a
			// start has not got; its value could not be checked here.
			httpapi.Error(w, http.StatusConflict,
				"this process's start form has a directory field, which can only be answered in a task; it cannot be started from here")
			return false
		}
	}
	if s.refuseDecisionValues(w, r, schema, body) {
		return false
	}
	if bad := formShapeOf([]byte(schema)).notOnForm(vars); len(bad) > 0 {
		httpapi.Error(w, http.StatusBadRequest, "not fields of this form: "+strings.Join(bad, ", "))
		return false
	}
	if problems := startFormProblems(startFormRules([]byte(schema)), vars); len(problems) > 0 {
		httpapi.Error(w, http.StatusBadRequest, "the start form refuses these values: "+strings.Join(problems, "; "))
		return false
	}
	return true
}
