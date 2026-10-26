package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/token"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The pass: how an anonymous visitor walks a public wizard (ADR-0449, its fourth step).
//
// A public start link starts an instance for somebody who has no account. When the
// process holds a public wizard, the start draws 32 random bytes and hands the visitor
// a pass, "<instanceKey>.<hex>"; the instance keeps only the SHA-256 of the hex, frozen
// on its activation event (model.ProcessInstanceValue.PublicPassHash), so the proof is
// durable in the same fsync as the instance it opens and nothing has to be kept beside
// it.
//
// The pass opens the open user tasks of that one instance that lie inside a public
// wizard, and nothing else: no /api/v1 route reads it, no other task answers to it, no
// other instance. It travels in an Authorization header, never in a path or a query,
// so it stays out of access logs and Referer. The instance key in front of the dot is
// an identifier, not a capability — every /instances route asks who the caller is, and
// an anonymous caller is nobody there. Every failure to open answers 404 alike.

// publicStep is one screen of a public sitting, as the public page renders it.
type publicStep struct {
	TaskKey uint64 `json:"taskKey"`
	// Title is the task's name, or its element id when the model gave it none.
	Title string `json:"title,omitempty"`
	// Documentation is the work instruction the modeller wrote for the screen.
	Documentation string          `json:"documentation,omitempty"`
	Schema        json.RawMessage `json:"schema,omitempty"`
	// Data prefills the screen: the values the instance holds under the variables the
	// form binds, and nothing else — the rule ADR-0275 applies to the holder of a
	// task, with the pass holder as the holder.
	Data map[string]any `json:"data,omitempty"`
}

// publicWizardResp is where the visitor's sittings stand.
type publicWizardResp struct {
	// Pass is set only on the start that minted it; the visitor keeps it from there.
	Pass  string      `json:"pass,omitempty"`
	State string      `json:"state"`
	Step  *publicStep `json:"step,omitempty"`
}

// errNoPublicStep is a step key the pass does not open; it answers 404 like any other.
var errNoPublicStep = errors.New("no open step with that key")

// passLength is the hex length of a pass's secret: token.New's 32 bytes.
const passLength = 64

// mintPass draws a pass's secret and the hash an instance keeps of it.
func mintPass() (secret, hash string, err error) {
	secret, err = token.New()
	if err != nil {
		return "", "", err
	}
	return secret, passHash(secret), nil
}

// passHash is what an instance keeps of a pass: the SHA-256 of its secret, hex.
func passHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// passOf reads a pass from the Authorization header: "Bearer <instanceKey>.<secret>".
// The shape is checked before anything is read, so nothing request-supplied reaches the
// store but a number.
func passOf(r *http.Request) (piKey uint64, hash string, ok bool) {
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return 0, "", false
	}
	k, secret, found := strings.Cut(strings.TrimSpace(raw), ".")
	if !found || len(secret) != passLength || !token.IsHex(secret) {
		return 0, "", false
	}
	piKey, err := strconv.ParseUint(k, 10, 64)
	if err != nil || piKey == 0 {
		return 0, "", false
	}
	return piKey, passHash(secret), true
}

// passOpens reports whether hash is the pass the instance was started with. On the
// loop. The comparison takes the same time whatever the hash, and an instance no
// public start handed a pass for opens to nothing.
func (s *Server) passOpens(piKey uint64, hash string) (bool, error) {
	pi, ok, err := s.store.ProcessInstance(piKey)
	if err != nil || !ok || pi.PublicPassHash == "" {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(pi.PublicPassHash), []byte(hash)) == 1, nil
}

// publicWizardAnswer says where the instance's public sittings stand: the earliest open
// screen of any of them, pending while one is live with nothing open, done otherwise.
func (s *Server) publicWizardAnswer(piKey uint64) (*publicWizardResp, error) {
	var (
		best  uint64
		found bool
		live  bool
		tr    taskResp
		err   error
	)
	s.do(func() {
		err = s.openStepsOf(piKey, func(elKey, jobKey uint64, jv *model.JobValue, scope uint64) {
			if scope == 0 || (found && elKey >= best) {
				return
			}
			if t := s.enrichTask(jobKey, jv); t.Wizard == compiler.WizardPublic.String() {
				best, found, tr = elKey, true, t
			}
		})
		if err != nil || found {
			return
		}
		err = s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
			ei, ok, gErr := s.store.GetElementInstance(elKey)
			if gErr != nil || !ok {
				return gErr
			}
			if d, ok := s.deployments[ei.ProcessDefKey]; ok && d.cp != nil && d.cp.Wizard(ei.ElementId) == compiler.WizardPublic {
				live = true
			}
			return nil
		})
	})
	switch {
	case err != nil:
		return nil, err
	case !found && live:
		return &publicWizardResp{State: wizardPending}, nil
	case !found:
		return &publicWizardResp{State: wizardDone}, nil
	}
	step, err := s.publicStepOf(tr)
	if err != nil {
		return nil, err
	}
	return &publicWizardResp{State: wizardStep, Step: step}, nil
}

// publicStepOf renders one open screen for the visitor: its form, and the values the
// instance holds under the variables that form binds.
func (s *Server) publicStepOf(tr taskResp) (*publicStep, error) {
	step := &publicStep{TaskKey: tr.Key, Title: tr.Name, Documentation: tr.Documentation}
	if step.Title == "" {
		step.Title = tr.ElementID
	}
	if tr.FormID == "" {
		return step, nil
	}
	var err error
	s.do(func() {
		f, ok, gErr := s.forms.Get(tr.FormID)
		if gErr != nil || !ok {
			err = gErr
			return
		}
		step.Schema = json.RawMessage(f.Schema)
		fields := map[string]bool{}
		collectFormFieldKeys([]byte(f.Schema), fields)
		data := map[string]any{}
		err = state.VisibleVariables(s.personalReader(s.store), tr.ElementInstanceKey, func(v *model.VariableValue) error {
			if fields[v.Name] {
				data[v.Name] = nativeVar(v)
			}
			return nil
		})
		if len(data) > 0 {
			step.Data = data
		}
	})
	return step, err
}

// publicStepForm returns the form a step the pass opens renders, and errNoPublicStep for a
// key that is not an open screen of a public sitting of the instance. On the loop.
func (s *Server) publicStepForm(piKey, taskKey uint64) (string, error) {
	jv, ok, err := s.store.GetJob(taskKey)
	if err != nil {
		return "", err
	}
	if !ok || jv.JobType != compiler.UserTaskJobTypeIndex || jv.Retries <= 0 || jv.ProcessInstanceKey != piKey {
		return "", errNoPublicStep
	}
	tr := s.enrichTask(taskKey, jv)
	if tr.Wizard != compiler.WizardPublic.String() {
		return "", errNoPublicStep
	}
	if tr.FormID == "" {
		return "", nil
	}
	f, _, err := s.forms.Get(tr.FormID)
	return f.Schema, err
}

// publicPassOrRefuse opens the request's pass, writing the refusal when it does not. The
// rate limit comes first, so trying passes costs a visitor's budget like anything else.
func (s *Server) publicPassOrRefuse(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	s.setPublicCORS(w, r)
	if !s.publicRate.allow(httpapi.ClientIP(r)) {
		httpapi.Error(w, http.StatusTooManyRequests, "too many requests")
		return 0, false
	}
	piKey, hash, ok := passOf(r)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "unknown or expired pass")
		return 0, false
	}
	var (
		opens bool
		err   error
	)
	s.do(func() { opens, err = s.passOpens(piKey, hash) })
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read instance: "+err.Error())
		return 0, false
	case !opens:
		httpapi.Error(w, http.StatusNotFound, "unknown or expired pass")
		return 0, false
	}
	return piKey, true
}

// writePublicWizard answers with where the sittings stand, or the reason it cannot say.
func writePublicWizard(w http.ResponseWriter, ans *publicWizardResp, err error) {
	switch {
	case errors.Is(err, vault.ErrErased):
		httpapi.Error(w, http.StatusConflict, "this form holds personal data that has been erased, so it can no longer be continued")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, ans)
	}
}

// handlePublicWizardStep answers GET /public/wizard/step: where the visitor's sittings
// stand, with the screen to fill when one is open. It is what the public page asks on
// a reload and while a sitting is pending.
func (s *Server) handlePublicWizardStep(w http.ResponseWriter, r *http.Request) {
	piKey, ok := s.publicPassOrRefuse(w, r)
	if !ok {
		return
	}
	ans, err := s.publicWizardAnswer(piKey)
	writePublicWizard(w, ans, err)
}

// handlePublicWizardComplete answers POST /public/wizard/steps/{taskKey}/complete: the
// visitor submits one screen. Only the fields its form has are accepted, as at the start
// (publicstartfields.go); personal values are sealed at this in-edge as at every other
// (ADR-0314); and the answer is where the sittings stand afterwards.
func (s *Server) handlePublicWizardComplete(w http.ResponseWriter, r *http.Request) {
	piKey, ok := s.publicPassOrRefuse(w, r)
	if !ok {
		return
	}
	taskKey, err := strconv.ParseUint(r.PathValue("taskKey"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusNotFound, errNoPublicStep.Error())
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
	var schema string
	s.do(func() { schema, err = s.publicStepForm(piKey, taskKey) })
	switch {
	case errors.Is(err, errNoPublicStep):
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read step: "+err.Error())
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
		// Still an open screen of this sitting: two submissions of one screen race, and
		// the second finds it gone.
		if _, err = s.publicStepForm(piKey, taskKey); err == nil {
			s.proc.CompleteJob(taskKey, vars...)
			completed = true
		}
	})
	switch {
	case errors.Is(err, errNoPublicStep):
		httpapi.Error(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read step: "+err.Error())
		return
	}
	if completed {
		if err := s.drive(); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "complete step: "+err.Error())
			return
		}
	}
	ans, err := s.publicWizardAnswer(piKey)
	writePublicWizard(w, ans, err)
}
