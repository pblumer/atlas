package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The link that answers a user task (ADR-draft-user-task-answered-by-link).
//
// A user task marked atlas:answeredBy="link" may be completed by whoever holds its link,
// and a mail task naming it with answerTask carries that link as answerLink. The link is
//
//	{external URL}/public/answer#{instance key}.{scope key}.{task id, base64url}.{mac}
//
// where the scope key is the scope instance the mail was sent from — the instance itself
// at the root, one run of an event subprocess inside one — and mac is an HMAC of the
// three under a key the vault derives for this purpose alone. Nothing per link is stored:
// a check recomputes it. The token is in the fragment, which a browser never sends, so it
// is in no access log and no Referer; the page sends it in an Authorization header, as a
// wizard's pass travels.
//
// A link opens one thing — the open job of that user task in that scope of that instance,
// while the task is answered by link in the version the instance runs — and every other
// case answers 404 alike. Opening it never answers: only a POST of the task's form does, so a
// link scanner that fetches every link in a mail completes nothing.

// answerPagePath is the static page an answer link opens.
const answerPagePath = "/public/answer"

// answerLinkPurpose names the vault-derived key answer links are signed with. The
// version is in it so a change to what is signed can never accept an old link by
// accident.
const answerLinkPurpose = "atlas/answer-link/v1"

// answerMACLength is how many bytes of the HMAC a link carries: 128 bits, hex.
const answerMACLength = 16

// maxAnswerTaskID bounds the task id a token may carry before anything is decoded.
const maxAnswerTaskID = 256

// errNoAnswer is a token that opens nothing; it answers 404 like every other.
var errNoAnswer = errors.New("nothing to answer under this link")

// answerMAC is the signature of the link to task in scope scopeKey of instance piKey.
func (s *Server) answerMAC(piKey, scopeKey uint64, task string) ([]byte, error) {
	if s.vault == nil {
		return nil, errors.New("answer links are signed with a key the vault derives, and this server runs without a vault (--vault=false)")
	}
	m := hmac.New(sha256.New, s.vault.DerivedKey(answerLinkPurpose))
	m.Write([]byte(strconv.FormatUint(piKey, 10)))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatUint(scopeKey, 10)))
	m.Write([]byte{0})
	m.Write([]byte(task))
	return m.Sum(nil)[:answerMACLength], nil
}

// answerToken is the token of the link to task in scope scopeKey of instance piKey.
func (s *Server) answerToken(piKey, scopeKey uint64, task string) (string, error) {
	mac, err := s.answerMAC(piKey, scopeKey, task)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(piKey, 10) + "." + strconv.FormatUint(scopeKey, 10) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(task)) + "." + hex.EncodeToString(mac), nil
}

// AnswerLink mints the link a mail task sent from scope scopeKey of instance piKey carries
// for the user task with BPMN id task ([mail.AnswerLinker]). It is built on the operator's configured origin
// and on nothing else: a link built on whichever host a request came in on could send a
// customer to an address they cannot reach — the rule the approval notifications follow.
func (d mailDirectory) AnswerLink(piKey, scopeKey uint64, task string) (string, error) {
	base := strings.TrimRight(d.s.externalURL, "/")
	if base == "" {
		return "", errors.New("answer links are built on the server's external URL, and none is configured (--external-url or ATLAS_EXTERNAL_URL)")
	}
	tok, err := d.s.answerToken(piKey, scopeKey, task)
	if err != nil {
		return "", err
	}
	return base + answerPagePath + "#" + tok, nil
}

// answerLink is what a checked token names: a task in one scope of one instance.
type answerLink struct {
	piKey, scopeKey uint64
	task            string
}

// answerOf reads and checks the token in the request's Authorization header, "Bearer
// {token}". The shape is checked before anything is decoded, and the signature before
// anything is read from the store.
func (s *Server) answerOf(r *http.Request) (answerLink, bool) {
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return answerLink{}, false
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 4 || len(parts[2]) == 0 || len(parts[2]) > maxAnswerTaskID*4/3+4 || len(parts[3]) != 2*answerMACLength {
		return answerLink{}, false
	}
	piKey, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || piKey == 0 {
		return answerLink{}, false
	}
	scopeKey, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || scopeKey == 0 {
		return answerLink{}, false
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(id) == 0 || len(id) > maxAnswerTaskID {
		return answerLink{}, false
	}
	got, err := hex.DecodeString(parts[3])
	if err != nil {
		return answerLink{}, false
	}
	want, err := s.answerMAC(piKey, scopeKey, string(id))
	if err != nil || !hmac.Equal(got, want) {
		return answerLink{}, false
	}
	return answerLink{piKey: piKey, scopeKey: scopeKey, task: string(id)}, true
}

// openAnswerTask finds the open job of the link's user task, when that task is answered
// by link in the version the instance runs and lies in the scope the mail was sent from.
// Two open ones in that scope — two tokens reached the task — answer the one activated
// first. On the loop.
func (s *Server) openAnswerTask(l answerLink) (uint64, *model.JobValue, error) {
	var (
		jobKey uint64
		job    *model.JobValue
		first  uint64
	)
	err := s.store.ElementInstancesOfProcess(l.piKey, func(elKey uint64) error {
		if job != nil && elKey > first {
			return nil
		}
		ei, ok, err := s.store.GetElementInstance(elKey)
		if err != nil || !ok {
			return err
		}
		task := l.task
		d, ok := s.deployments[ei.ProcessDefKey]
		if !ok || d.cp == nil || d.cp.ElementBpmnId(ei.ElementId) != task {
			return nil
		}
		n := d.cp.Node(ei.ElementId)
		if n.Type != compiler.TypeUserTask || !d.cp.UserTask(n.Detail).AnsweredByLink {
			return nil
		}
		k, ok, err := s.store.JobOfElement(elKey)
		if err != nil || !ok {
			return err
		}
		jv, ok, err := s.store.GetJob(k)
		if err != nil || !ok {
			return err
		}
		// Open only: retries 0 is a task parked behind an incident.
		if jv.JobType != compiler.UserTaskJobTypeIndex || jv.Retries <= 0 {
			return nil
		}
		in, err := s.withinScope(ei, l.scopeKey)
		if err != nil || !in {
			return err
		}
		jobKey, job, first = k, jv, elKey
		return nil
	})
	if err == nil && job == nil {
		err = errNoAnswer
	}
	return jobKey, job, err
}

// withinScope reports whether element instance ei lies in scope instance scopeKey: the
// scope is ei's flow scope or one enclosing it. The instance itself encloses everything.
func (s *Server) withinScope(ei *model.ElementInstanceValue, scopeKey uint64) (bool, error) {
	if scopeKey == ei.ProcessInstanceKey {
		return true, nil
	}
	for k, depth := ei.FlowScopeKey, 0; k != 0 && depth < 64; depth++ {
		if k == scopeKey {
			return true, nil
		}
		if k == ei.ProcessInstanceKey {
			return false, nil
		}
		sc, ok, err := s.store.GetElementInstance(k)
		if err != nil || !ok {
			return false, err
		}
		k = sc.FlowScopeKey
	}
	return false, nil
}

// answerResp is what the page is told: the task to answer, or that it was answered.
type answerResp struct {
	State string      `json:"state"` // "open" or "done"
	Step  *publicStep `json:"step,omitempty"`
}

// answerOrRefuse opens the request's link, writing the refusal when it opens nothing. The
// rate limit comes first, so trying links costs a visitor's budget like anything else.
func (s *Server) answerOrRefuse(w http.ResponseWriter, r *http.Request) (answerLink, bool) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return answerLink{}, false
	}
	l, ok := s.answerOf(r)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, errNoAnswer.Error())
		return answerLink{}, false
	}
	return l, true
}

// handlePublicAnswerPage serves the page an answer link opens. It is static: the token
// is in the fragment, which never reaches this handler, and the page asks for the task
// with it.
func (s *Server) handlePublicAnswerPage(w http.ResponseWriter, r *http.Request) {
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	page, err := webFS.ReadFile("web/public-answer.html")
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "public page missing")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The page must not hand its address on: the token is in the fragment, which a
	// browser does not send, and no-referrer keeps even the path at home.
	h.Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(page)
}

// handlePublicAnswerTask answers GET /public/answer/task: the task the link opens, with
// its form and the values the instance holds under the form's own fields. It answers
// and changes nothing, so a scanner that opens the link changes nothing either.
func (s *Server) handlePublicAnswerTask(w http.ResponseWriter, r *http.Request) {
	l, ok := s.answerOrRefuse(w, r)
	if !ok {
		return
	}
	var (
		tr  taskResp
		err error
	)
	s.do(func() {
		var jobKey uint64
		var jv *model.JobValue
		if jobKey, jv, err = s.openAnswerTask(l); err == nil {
			tr = s.enrichTask(jobKey, jv)
		}
	})
	if errors.Is(err, errNoAnswer) {
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
		return
	}
	step, err := s.publicStepOf(tr)
	if err != nil {
		writePublicWizard(w, nil, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, answerResp{State: "open", Step: step})
}

// handlePublicAnswerComplete answers POST /public/answer/task/{taskKey}/complete: the
// holder of the link answers the task. Only the fields of its form are accepted, as on a
// public start (publicstartfields.go); personal values are sealed at this in-edge as at
// every other (ADR-0314); and the job is completed as the Tasks app completes it.
func (s *Server) handlePublicAnswerComplete(w http.ResponseWriter, r *http.Request) {
	l, ok := s.answerOrRefuse(w, r)
	if !ok {
		return
	}
	taskKey, err := strconv.ParseUint(r.PathValue("taskKey"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusNotFound, errNoAnswer.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Settings))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	vars, err := parseStartVariables(body)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// The form the task renders, read while the task is still the one the link opens.
	formOf := func() (string, error) {
		k, jv, err := s.openAnswerTask(l)
		if err != nil {
			return "", err
		}
		if k != taskKey {
			return "", errNoAnswer
		}
		tr := s.enrichTask(k, jv)
		if tr.FormID == "" {
			return "", nil
		}
		f, _, err := s.forms.Get(tr.FormID)
		return f.Schema, err
	}
	var schema string
	s.do(func() { schema, err = formOf() })
	switch {
	case errors.Is(err, errNoAnswer):
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
		return
	}
	if bad := formShapeOf([]byte(schema)).notOnForm(vars); len(bad) > 0 {
		httpapi.Error(w, http.StatusBadRequest, "not fields of this form: "+strings.Join(bad, ", "))
		return
	}
	if refuseDirectoryFieldInPublic(w, schema) {
		return
	}
	if err := s.encipherJobVars(taskKey, vars); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	completed := false
	s.do(func() {
		// Still the open task of this link: two answers race, and the second finds it
		// gone — as does an answer given meanwhile in the Tasks app.
		if _, err = formOf(); err == nil {
			s.proc.CompleteJob(taskKey, vars...)
			completed = true
		}
	})
	switch {
	case errors.Is(err, errNoAnswer):
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
		return
	}
	if completed {
		if err := s.drive(); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "complete task: "+err.Error())
			return
		}
	}
	httpapi.JSON(w, http.StatusOK, answerResp{State: "done"})
}
