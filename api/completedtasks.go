package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A completed task stays readable to the person who completed it, in the form they
// answered (ADR-0554).
//
// The completion half is here too: completerOf names who answers, and
// answeredRevisionOnLoop which revision of the task's form they answered through. The
// handlers that complete a task on a person's behalf hand both to
// Processor.CompleteUserTask, which freezes them into a TaskAnswered event; the list
// and the view below read the index folded from it, off the run loop.

const (
	completedTasksDefaultLimit = 50
	completedTasksMaxLimit     = 200
)

// completerOf is the principal id a person's completion is recorded under, or "" when
// nobody is signed in (auth off, a public link) and there is nobody to list it for.
func completerOf(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

// offeredFormRevision is the form revision a completion body says the person answered
// through ({"formRevision": "sha256:…"}), or "" when it says none. A body that does
// not parse says none; parseStartVariables reports that body's own fault.
func offeredFormRevision(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var payload struct {
		FormRevision string `json:"formRevision"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.FormRevision)
}

// taskFormIDOnLoop is the form the user task this job waits on binds, or "" for none.
//
// Must be called on the run-loop goroutine.
func (s *Server) taskFormIDOnLoop(jv *model.JobValue) string {
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return ""
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok || d.cp == nil {
		return ""
	}
	return userTaskFormID(d.cp, ei.ElementId)
}

// userTaskFormID is the form a compiled user task binds, or "" when the element is not
// a user task of this graph or binds none.
func userTaskFormID(cp *compiler.CompiledProcess, el int32) string {
	if el < 0 || int(el) >= cp.NodeCount() {
		return ""
	}
	n := cp.Node(el)
	if n.Type != compiler.TypeUserTask {
		return ""
	}
	return cp.Intern(cp.UserTask(n.Detail).FormId)
}

// answeredRevisionOnLoop is the revision of formID a completion is recorded with
// (ADR-0554 §2): the one the person's browser says it rendered, when that is a kept
// revision of this form, and otherwise the form's current schema, recorded as a
// revision now if it is not one yet. "" when the task has no form or its form is gone.
// A revision that cannot be recorded is not a reason to refuse the answer; the task is
// then shown without its form.
//
// Must be called on the run-loop goroutine.
func (s *Server) answeredRevisionOnLoop(formID, offered string) string {
	if formID == "" {
		return ""
	}
	if offered != "" {
		if _, ok, err := s.forms.Revision(formID, offered); err == nil && ok {
			return offered
		}
	}
	rec, ok, err := s.forms.Get(formID)
	if err != nil || !ok {
		return ""
	}
	digest, err := s.forms.RecordRevision(rec)
	if err != nil {
		return ""
	}
	return digest
}

// completedTask is one row of the list, and the head of the view.
type completedTask struct {
	Key                string `json:"key"`
	ProcessInstanceKey string `json:"processInstanceKey"`
	ElementInstanceKey string `json:"elementInstanceKey"`
	ProcessDefKey      string `json:"processDefKey,omitempty"`
	ProcessID          string `json:"processId,omitempty"`
	ProcessName        string `json:"processName,omitempty"`
	Version            int32  `json:"version,omitempty"`
	ElementID          string `json:"elementId,omitempty"`
	Name               string `json:"name,omitempty"`
	FormID             string `json:"formId,omitempty"`
	FormRevision       string `json:"formRevision,omitempty"`
	CompletedAt        string `json:"completedAt"`
	CompletedBy        string `json:"completedBy"`
}

func completedTaskOf(a state.TaskAnswer, defs defIndex) completedTask {
	v := a.Value
	ct := completedTask{
		Key:                strconv.FormatUint(v.JobKey, 10),
		ProcessInstanceKey: strconv.FormatUint(v.ProcessInstanceKey, 10),
		ElementInstanceKey: strconv.FormatUint(v.ElementInstanceKey, 10),
		FormRevision:       v.FormRevision,
		CompletedAt:        time.Unix(0, a.At).UTC().Format(time.RFC3339),
		CompletedBy:        v.Completer,
	}
	if v.ProcessDefKey != 0 {
		ct.ProcessDefKey = strconv.FormatUint(v.ProcessDefKey, 10)
	}
	d, ok := defs[v.ProcessDefKey]
	if !ok {
		return ct
	}
	ct.ProcessID, ct.ProcessName, ct.Version = d.ProcessID, d.Name, d.Version
	if cp := d.cp; cp != nil && v.ElementId >= 0 && int(v.ElementId) < cp.NodeCount() {
		ct.ElementID = cp.ElementBpmnId(v.ElementId)
		if n := cp.Node(v.ElementId); n.Type == compiler.TypeUserTask {
			ct.Name = cp.Intern(cp.UserTask(n.Detail).Name)
		}
		ct.FormID = userTaskFormID(cp, v.ElementId)
	}
	return ct
}

// inReach reports whether a definition is in a credential's reach of projects; a
// credential that states none reaches every project (ADR-0512 §2).
func inReach(reach []string, defs defIndex, defKey uint64) bool {
	if reach == nil {
		return true
	}
	d, ok := defs[defKey]
	return ok && d.ProjectID != "" && slices.Contains(reach, d.ProjectID)
}

// errCompletedPageFull stops a page once it holds one row more than it returns, which
// is how a page knows another follows.
var errCompletedPageFull = errors.New("page full")

// parseCompletedCursor reads a cursor written by formatCompletedCursor: "<at>.<jobKey>".
func parseCompletedCursor(raw string) (state.TaskAnswerCursor, bool) {
	at, key, ok := strings.Cut(raw, ".")
	if !ok {
		return state.TaskAnswerCursor{}, false
	}
	a, err1 := strconv.ParseInt(at, 10, 64)
	k, err2 := strconv.ParseUint(key, 10, 64)
	if err1 != nil || err2 != nil || k == 0 {
		return state.TaskAnswerCursor{}, false
	}
	return state.TaskAnswerCursor{At: a, JobKey: k}, true
}

func formatCompletedCursor(a state.TaskAnswer) string {
	return strconv.FormatInt(a.At, 10) + "." + strconv.FormatUint(a.Value.JobKey, 10)
}

// The instants unix nanoseconds can hold. Outside them UnixNano overflows silently, and a
// bound in the year 2999 would read as one in the past.
var (
	earliestInstant = time.Unix(0, -1<<63)
	latestInstant   = time.Unix(0, 1<<63-1)
)

// parseInstant reads an RFC 3339 instant or a date (YYYY-MM-DD, midnight UTC) as unix
// nanoseconds; "" is 0, an open bound. An instant nanoseconds cannot hold is refused.
func parseInstant(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		if t, err = time.Parse(time.DateOnly, raw); err != nil {
			return 0, fmt.Errorf("%q is neither an RFC 3339 instant nor a date", raw)
		}
	}
	if t.Before(earliestInstant) || t.After(latestInstant) {
		return 0, fmt.Errorf("%q is outside the years 1678 to 2262", raw)
	}
	return t.UnixNano(), nil
}

// handleListCompletedTasks lists the tasks the caller completed, newest first
// (ADR-0554 §5): GET /api/v1/tasks/completed?process=&from=&to=&limit=&cursor=.
// Read off the run loop; a caller with nobody signed in has completed nothing.
func (s *Server) handleListCompletedTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := completedTasksDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > completedTasksMaxLimit {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", completedTasksMaxLimit))
			return
		}
		limit = n
	}
	var cursor state.TaskAnswerCursor
	if raw := q.Get("cursor"); raw != "" {
		c, ok := parseCompletedCursor(raw)
		if !ok {
			httpapi.Error(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		cursor = c
	}
	from, err := parseInstant(q.Get("from"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	to, err := parseInstant(q.Get("to"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	process := strings.TrimSpace(q.Get("process"))
	completer := completerOf(r)
	reach := taskReachOf(r)

	resp := struct {
		Items      []completedTask `json:"items"`
		NextCursor string          `json:"nextCursor,omitempty"`
	}{Items: []completedTask{}}
	var last state.TaskAnswer
	more := false
	readErr := s.readOffLoop(func(v *state.ReadView, defs defIndex) error {
		return v.TaskAnswersBy(completer, cursor, from, to, func(a state.TaskAnswer) error {
			if !inReach(reach, defs, a.Value.ProcessDefKey) {
				return nil
			}
			if process != "" && defs[a.Value.ProcessDefKey].ProcessID != process {
				return nil
			}
			if len(resp.Items) == limit {
				more = true
				return errCompletedPageFull
			}
			resp.Items = append(resp.Items, completedTaskOf(a, defs))
			last = a
			return nil
		})
	})
	if readErr != nil && !errors.Is(readErr, errCompletedPageFull) {
		httpapi.Error(w, http.StatusInternalServerError, "list completed tasks: "+readErr.Error())
		return
	}
	if more {
		resp.NextCursor = formatCompletedCursor(last)
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// completedTaskView is a completed task as the Tasks app shows it again: the task, the
// form revision it was answered through, and the variables the form binds as they stood
// once it was answered.
type completedTaskView struct {
	Task completedTask `json:"task"`
	// Form is the revision the task was answered through; nil when the task had no form
	// or the revision is no longer kept, and then FormMissing says which.
	Form *completedForm `json:"form"`
	// FormMissing is set when the task was answered through a form that is no longer
	// kept (it was deleted): Values then holds the task's own answers only.
	FormMissing bool `json:"formMissing,omitempty"`
	// Values are the variables the form binds — the task's own scope over the process's
	// — as they stood at the answer's log position.
	Values map[string]any `json:"values"`
	// Answered names the variables the completion wrote: the person's answer.
	Answered []string `json:"answered"`
	// Erased names the variables whose data subject was erased since (ADR-0314); they
	// are absent from Values and can no longer be read.
	Erased []string `json:"erased,omitempty"`
}

type completedForm struct {
	ID       string          `json:"id"`
	Revision string          `json:"revision"`
	SavedAt  int64           `json:"savedAt"`
	Schema   json.RawMessage `json:"schema"`
}

// errSnapshotPast stops a history fold at the first change past the answer.
var errSnapshotPast = errors.New("past the answer")

// handleGetCompletedTask shows one completed task again to the person who completed it
// (ADR-0554 §3, §4): GET /api/v1/tasks/completed/{key}. Anybody else, a task still open
// and one whose history is gone are answered 404 alike, so the route is no oracle for
// which tasks exist.
func (s *Server) handleGetCompletedTask(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid task key")
		return
	}
	completer := completerOf(r)
	reach := taskReachOf(r)
	const notFound = "no completed task of yours with that key"

	var (
		answer state.TaskAnswer
		found  bool
		head   completedTask
		// scopes are the task's own scope over the process's, folded in that order so
		// the inner value wins, as a form's prefill reads them.
		changes map[string]*model.VariableValue
	)
	readErr := s.readOffLoop(func(v *state.ReadView, defs defIndex) error {
		a, ok, err := v.TaskAnswer(key)
		if err != nil || !ok {
			return err
		}
		if completer == "" || a.Value.Completer != completer || !inReach(reach, defs, a.Value.ProcessDefKey) {
			return nil
		}
		answer, found, head = a, true, completedTaskOf(a, defs)
		changes = map[string]*model.VariableValue{}
		for _, scope := range []uint64{a.Value.ProcessInstanceKey, a.Value.ElementInstanceKey} {
			if scope == 0 {
				continue
			}
			err := v.VariableSnapshotHistory(scope, func(_ int64, pos uint64, val *model.VariableValue) error {
				if pos > a.Position {
					return errSnapshotPast
				}
				c := *val
				changes[c.Name] = &c
				return nil
			})
			if err != nil && !errors.Is(err, errSnapshotPast) {
				return err
			}
		}
		return nil
	})
	switch {
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read completed task: "+readErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFound)
		return
	}

	view := completedTaskView{Task: head, Values: map[string]any{}, Answered: []string{}}
	// The form revision answered through. Revisions are design-time data the run loop
	// owns, like the forms.
	var (
		rev    formRevision
		hasRev bool
		revErr error
	)
	if head.FormID != "" && answer.Value.FormRevision != "" {
		s.do(func() { rev, hasRev, revErr = s.forms.Revision(head.FormID, answer.Value.FormRevision) })
	}
	if revErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read form revision: "+revErr.Error())
		return
	}
	var bound map[string]bool
	switch {
	case hasRev:
		view.Form = &completedForm{ID: head.FormID, Revision: rev.Digest, SavedAt: rev.SavedAt, Schema: json.RawMessage(rev.Schema)}
		bound = map[string]bool{}
		collectFormFieldKeys([]byte(rev.Schema), bound)
	case head.FormID != "":
		view.FormMissing = true
	}
	acc := instanceAccess{fields: bound}

	opener := personalReader{vault: s.vault}
	for name, val := range changes {
		answered := val.ProducerKey != 0 && val.ProducerKey == answer.Value.ElementInstanceKey
		// With its form, a task shows what the form binds; without it, only the answer.
		if hasRev && !acc.allows(name) || !hasRev && !answered {
			continue
		}
		// The completion's own marker of who answered is the task's head, not a field.
		if name == taskCompleterVar && !bound[name] {
			continue
		}
		shown := val
		if s.vault != nil {
			opened, err := opener.open(val)
			switch {
			case errors.Is(err, vault.ErrErased):
				view.Erased = append(view.Erased, name)
				continue
			case err != nil:
				httpapi.Error(w, http.StatusInternalServerError, "open "+name+": "+err.Error())
				return
			}
			shown = opened
		}
		view.Values[name] = nativeVar(shown)
		if answered {
			view.Answered = append(view.Answered, name)
		}
	}
	slices.Sort(view.Answered)
	slices.Sort(view.Erased)
	httpapi.JSON(w, http.StatusOK, view)
}
