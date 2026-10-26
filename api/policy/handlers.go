package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/authz"
)

// The policy routes. All of them are an administrator's (api/openapi.go): a policy
// decides what every Worker may do as anybody, so writing one is administering the
// installation.

// readBody reads a request body within the Definition budget: a policy document is a
// modelled artefact, and one may carry a whole DMN decision.
func (s *Service) readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, s.budgets().Definition))
}

func who(r *http.Request) string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

// HandleList lists every policy.
func (s *Service) HandleList(w http.ResponseWriter, _ *http.Request) {
	out, err := s.List()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleGet returns one policy with every version it has published.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	d, ok, err := s.get(r.PathValue("id"))
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, ErrNotFound.Error())
	default:
		for i := range d.Versions {
			d.Versions[i].Document = compact(d.Versions[i].Document)
		}
		httpapi.JSON(w, http.StatusOK, d)
	}
}

// HandleSaveDraft saves the body as the policy's draft, with the warnings it raises.
func (s *Service) HandleSaveDraft(w http.ResponseWriter, r *http.Request) {
	body, err := s.readBody(r)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.SaveDraft(r.PathValue("id"), body, who(r))
	switch {
	case errors.Is(err, ErrInvalid):
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandlePublish publishes the draft as the next version.
func (s *Service) HandlePublish(w http.ResponseWriter, r *http.Request) {
	out, err := s.Publish(r.PathValue("id"), who(r))
	switch {
	case errors.Is(err, ErrNoDraft):
		httpapi.Error(w, http.StatusConflict, err.Error())
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// HandleSetEnabled switches a policy on or off.
func (s *Service) HandleSetEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	raw, err := s.readBody(r)
	if err == nil {
		err = json.Unmarshal(raw, &body)
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	out, err := s.SetEnabled(r.PathValue("id"), body.Enabled, who(r))
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.Error(w, http.StatusNotFound, err.Error())
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}

// simulation is a request as an administrator imagines it. Every attribute is taken
// as given — that is what makes it a simulation — except a subject named by id alone,
// whose roles and groups are read from the user store.
type simulation struct {
	Subject struct {
		ID       string   `json:"id"`
		Known    *bool    `json:"known"`
		Disabled bool     `json:"disabled"`
		Roles    []string `json:"roles"`
		Groups   []string `json:"groups"`
	} `json:"subject"`
	IdentityMode   string            `json:"identityMode"`
	Worker         string            `json:"worker"`
	WorkerType     string            `json:"workerType"`
	WorkerInstance string            `json:"workerInstance"`
	ExecutedAs     string            `json:"executedAs"`
	Connection     *authz.Connection `json:"connection"`
	Operation      string            `json:"operation"`
	Resource       string            `json:"resource"`
	Digest         string            `json:"digest"`
	Parameters     map[string]any    `json:"parameters"`
	ProcessID      string            `json:"processId"`
	InitiatedBy    string            `json:"initiatedBy"`
	Time           string            `json:"time"`
	Approvals      []authz.Approval  `json:"approvals"`
	Policy         json.RawMessage   `json:"policy"`
}

// known is the simulation's vocabulary; anything else in a request is reported back
// as ignored rather than silently dropped, because a claim like "approved": true is
// exactly what an administrator might expect to matter, and it does not.
var known = map[string]bool{"subject": true, "identityMode": true, "worker": true, "workerType": true,
	"workerInstance": true, "executedAs": true, "connection": true, "operation": true, "resource": true,
	"digest": true, "parameters": true, "processId": true, "initiatedBy": true, "time": true,
	"approvals": true, "policy": true}

// HandleSimulate evaluates a simulated request against the published set — or, with
// "policy", against that unpublished document in place of its published version —
// and explains the answer. Nothing is recorded and nothing is enforced, and the answer
// says so.
func (s *Service) HandleSimulate(w http.ResponseWriter, r *http.Request) {
	raw, err := s.readBody(r)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var sim simulation
	if err := json.Unmarshal(raw, &sim); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	ignored := []string{}
	for k := range fields {
		if !known[k] {
			ignored = append(ignored, k)
		}
	}
	sort.Strings(ignored)

	at := s.now()
	if sim.Time != "" {
		t, err := time.Parse(time.RFC3339, sim.Time)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "time must be RFC 3339: "+err.Error())
			return
		}
		at = t
	}
	req := &authz.Request{
		Subject: authz.Subject{ID: sim.Subject.ID, Disabled: sim.Subject.Disabled,
			Roles: sim.Subject.Roles, Groups: sim.Subject.Groups, Known: sim.Subject.ID != ""},
		Actor: authz.Actor{IdentityMode: sim.IdentityMode, Worker: sim.Worker, WorkerType: sim.WorkerType,
			WorkerInstance: sim.WorkerInstance, ExecutedAs: sim.ExecutedAs, Connection: sim.Connection},
		Action:   authz.Action{Operation: sim.Operation, Digest: sim.Digest},
		Resource: authz.Resource{Type: sim.WorkerType, ID: sim.Resource},
		Context: authz.Context{Time: at.UnixNano(), ProcessID: sim.ProcessID, InitiatedBy: sim.InitiatedBy,
			Approvals: sim.Approvals},
	}
	if sim.Subject.Known != nil {
		req.Subject.Known = *sim.Subject.Known
	}
	resolved := false
	if sim.Subject.ID != "" && sim.Subject.Roles == nil && sim.Subject.Groups == nil && s.resolve != nil {
		if subj, ok := s.resolve(sim.Subject.ID); ok {
			req.Subject, resolved = subj, true
		} else {
			req.Subject.Known = false
		}
	}
	if req.Action.Digest == "" {
		if d, err := authz.Digest(sim.WorkerType, sim.Worker, sim.Operation, sim.Resource, sim.Parameters); err == nil {
			req.Action.Digest = d
		}
	}
	set := s.Set()
	if len(sim.Policy) > 0 {
		set, err = s.withCandidate(sim.Policy)
		switch {
		case errors.Is(err, errBadCandidate):
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			httpapi.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	d := set.Evaluate(ctx, req)
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"simulation":      true,
		"note":            "A simulation: nothing was recorded and nothing was enforced. Attributes were taken as given" + resolvedNote(resolved) + ".",
		"effect":          d.Effect.String(),
		"permitted":       d.Permitted(),
		"reasonCode":      d.ReasonCode,
		"policyId":        d.PolicyID,
		"policyVersion":   d.PolicyVersion,
		"obligations":     d.Obligations,
		"trace":           traceView(d.Trace),
		"digest":          req.Action.Digest,
		"ignored":         ignored,
		"ignoredBecause":  ignoredBecause(ignored),
		"subjectResolved": resolved,
	})
}

func resolvedNote(resolved bool) string {
	if resolved {
		return ", except the subject's roles and groups, read from the user store"
	}
	return ""
}

func ignoredBecause(ignored []string) string {
	if len(ignored) == 0 {
		return ""
	}
	return "These fields are not attributes a decision reads. An approval, in particular, is never a claim in a request: it is a fact an approver records, and a simulation states it under \"approvals\"."
}

// withCandidate is the published set with one document compiled in place of its
// published version, as version 0 so nobody mistakes it for a published one.
//
// A published policy that cannot be read fails the simulation rather than being left
// out: enforcement has no set at all then and refuses every governed action, and a
// simulation without the unreadable policy could answer Permit where enforcement
// refuses.
func (s *Service) withCandidate(raw json.RawMessage) (*authz.Set, error) {
	doc, err := authz.ParseDocument(bytes.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadCandidate, err)
	}
	var published []authz.Published
	var lerr error
	s.loop.Do(func() {
		recs, err := s.policies.LoadAll()
		if err != nil {
			lerr = err
			return
		}
		for _, r := range recs {
			if !r.Enabled || r.Current == 0 || r.ID == doc.ID {
				continue
			}
			v, ok, err := s.versions.Get(versionKey(r.ID, r.Current))
			if err == nil && !ok {
				err = fmt.Errorf("version %d is missing", r.Current)
			}
			var d authz.Document
			if err == nil {
				d, err = authz.ParseDocument(v.Document)
			}
			if err != nil {
				lerr = fmt.Errorf("published policy %s cannot be read, so enforcement refuses every governed action and a simulation would mislead: %w", r.ID, err)
				return
			}
			published = append(published, authz.Published{Document: d, Version: v.Version})
		}
	})
	if lerr != nil {
		return nil, lerr
	}
	// A candidate carries the version after its current one, which is what it would be
	// published as; the answer is labeled a simulation either way.
	published = append(published, authz.Published{Document: doc, Version: 1 + s.currentOf(doc.ID)})
	set, err := authz.CompileSet(context.Background(), published)
	if err != nil {
		// Every published document compiled when it was published; what does not
		// compile now is the candidate.
		return nil, fmt.Errorf("%w: %w", errBadCandidate, err)
	}
	return set, nil
}

// errBadCandidate marks a simulated candidate the caller got wrong, as against a
// published set the server cannot read.
var errBadCandidate = errors.New("the candidate policy is invalid")

func (s *Service) currentOf(id string) int {
	cur := 0
	s.loop.Do(func() {
		if r, ok, err := s.policies.Get(id); err == nil && ok {
			cur = r.Current
		}
	})
	return cur
}

type stepView struct {
	PolicyID      string `json:"policyId"`
	PolicyVersion int    `json:"policyVersion"`
	RuleID        string `json:"ruleId,omitempty"`
	Effect        string `json:"effect"`
	Reason        string `json:"reasonCode,omitempty"`
	Note          string `json:"note,omitempty"`
}

func traceView(steps []authz.Step) []stepView {
	out := make([]stepView, 0, len(steps))
	for _, st := range steps {
		out = append(out, stepView{PolicyID: st.PolicyID, PolicyVersion: st.PolicyVersion, RuleID: st.RuleID,
			Effect: st.Effect.String(), Reason: st.Reason, Note: st.Note})
	}
	return out
}

// TraceView renders a trace for a response or an evidence record.
func TraceView(steps []authz.Step) any { return traceView(steps) }
