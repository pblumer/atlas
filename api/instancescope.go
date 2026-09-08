package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// Who may read a running instance's data.
//
// ADR-0071 put a project's membership on the artifacts filed into it, and F09 put
// it on deployment. This file is the runtime half of the same axis: an instance is
// a *thing*, and being signed in is not a relationship to it.
//
// The endpoint it governs was deliberately left open, and the route table said so:
// a task form is prefilled from the variables of the instance the task belongs to,
// so any role narrower than "signed in" would hand a task worker an empty form.
// That reasoning was right about roles and wrong about the conclusion — the answer
// is not a role, it is the object question, and until now nobody was asking it. An
// external review walked in as an unrelated account and read the confidential
// variables of somebody else's instance (audit F11, ISDS point O-02).
//
// The rule, in order:
//
//   - Authentication off: everything, as everywhere else — single-user mode is a
//     single user.
//   - Operator or admin: everything. They can already list every instance; this
//     endpoint is not where that is narrowed.
//   - A member of the project the instance's definition was deployed from:
//     everything. This is ADR-0071's inheritance, followed one step further —
//     from the project to the draft to the deployment to the instance it runs.
//   - Somebody holding a task on the instance: only the fields that task's form
//     asks for. This is what keeps the Tasks app working without handing a task
//     worker the whole instance.
//   - Anyone else: nothing, reported as 404 so the endpoint does not confirm that
//     an instance with that key exists.

// instanceAccess is how much of an instance's data a request may read.
type instanceAccess struct {
	// full grants every variable in the requested scope.
	full bool
	// fields is the allowlist a task holder gets instead: the keys the form of a
	// task they hold asks for. Nil when full. Empty and not full means no access.
	fields map[string]bool
}

// allows reports whether one variable name may be returned.
func (a instanceAccess) allows(name string) bool {
	if a.full {
		return true
	}
	if a.fields[name] {
		return true
	}
	// A form field addresses a nested value by path ("customer.name"); the variable
	// it reads is the root of that path.
	if i := strings.IndexByte(name, '.'); i > 0 {
		return a.fields[name[:i]]
	}
	return false
}

// any reports whether the caller may read anything at all.
func (a instanceAccess) any() bool { return a.full || len(a.fields) > 0 }

// instanceAccessFor decides what the caller may read of the instance that scopeKey
// belongs to. scopeKey is either a process instance or one of its live element
// instances — the Tasks app asks for its task's element scope so a task nested in a
// subprocess prefills from its own fields (ADR-0084), so both have to resolve here.
//
// It returns a non-zero HTTP status and message when the answer is no.
func (s *Server) instanceAccessFor(r *http.Request, scopeKey uint64) (instanceAccess, int, string) {
	if !s.authEnabled {
		return instanceAccess{full: true}, 0, ""
	}
	pr := httpapi.PrincipalFrom(r.Context())
	if pr == nil {
		return instanceAccess{}, http.StatusUnauthorized, "sign in to read instance data"
	}
	if pr.HasRole(RoleOperator) || pr.HasRole(RoleAdmin) {
		return instanceAccess{full: true}, 0, ""
	}
	var (
		acc   instanceAccess
		known bool
		err   error
	)
	s.do(func() { acc, known, err = s.instanceAccessOnLoop(pr, scopeKey) })
	switch {
	case err != nil:
		return instanceAccess{}, http.StatusInternalServerError, "read instance: " + err.Error()
	case !known || !acc.any():
		// One answer for "no such instance" and "not yours", so the endpoint is not an
		// oracle for which instance keys exist — the same choice authorizeArtifact makes.
		return instanceAccess{}, http.StatusNotFound, "no instance with that key"
	}
	return acc, 0, ""
}

// instanceAccessOnLoop is the part that reads state, and must run on the run loop.
// known is false when nothing answers to scopeKey at all.
func (s *Server) instanceAccessOnLoop(pr *httpapi.Principal, scopeKey uint64) (instanceAccess, bool, error) {
	piKey := scopeKey
	if ei, ok, err := s.store.GetElementInstance(scopeKey); err != nil {
		return instanceAccess{}, false, err
	} else if ok {
		piKey = ei.ProcessInstanceKey
	}
	pi, ok, err := s.store.ProcessInstance(piKey)
	if err != nil || !ok {
		return instanceAccess{}, false, err
	}
	if s.instanceProjectGrantsView(pr, pi.ProcessDefKey) {
		return instanceAccess{full: true}, true, nil
	}
	fields, err := s.taskFieldsFor(pr, piKey)
	return instanceAccess{fields: fields}, true, err
}

// instanceProjectGrantsView reports whether the caller is a member — viewer or
// better — of the project the definition was deployed from. A deployment with no
// project grants nothing: an ungrouped deployment has no membership to inherit, and
// treating "no project" as "everyone" is the hole this closes.
func (s *Server) instanceProjectGrantsView(pr *httpapi.Principal, defKey uint64) bool {
	d, ok := s.deployments[defKey]
	if !ok || d.ProjectID == "" {
		return false
	}
	proj, ok, err := s.projects.Get(d.ProjectID)
	if err != nil || !ok {
		return false
	}
	return scopeRank(proj.effectiveRole(pr, s.authEnabled)) >= scopeRank(ScopeRoleViewer)
}

// taskFieldsFor collects the variable names the caller may read because of the user
// tasks they hold on this instance: the field keys of each such task's form. A task
// with no form contributes nothing — there is no declared set of fields to allow,
// and guessing one is how an allowlist becomes a formality.
//
// The scan walks the instance's own live element instances and asks the job index
// for each, so it costs the instance's token count rather than the server's whole
// job population.
func (s *Server) taskFieldsFor(pr *httpapi.Principal, piKey uint64) (map[string]bool, error) {
	var fields map[string]bool
	err := s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
		jobKey, ok, err := s.store.JobOfElement(elKey)
		if err != nil || !ok {
			return err
		}
		jv, ok, err := s.store.GetJob(jobKey)
		if err != nil || !ok || jv.JobType != compiler.UserTaskJobTypeIndex {
			return err
		}
		ei, ok, err := s.store.GetElementInstance(elKey)
		if err != nil || !ok {
			return err
		}
		d, ok := s.deployments[ei.ProcessDefKey]
		if !ok || d.cp == nil {
			return nil
		}
		n := d.cp.Node(ei.ElementId)
		if n.Type != compiler.TypeUserTask {
			return nil
		}
		detail := d.cp.UserTask(n.Detail)
		if !s.holdsTask(pr, jv.Assignee, d.cp.Intern(detail.CandidateGroups)) {
			return nil
		}
		formID := d.cp.Intern(detail.FormId)
		if formID == "" {
			return nil
		}
		rec, ok, err := s.forms.Get(formID)
		if err != nil || !ok {
			return err
		}
		if fields == nil {
			fields = map[string]bool{}
		}
		collectFormFieldKeys([]byte(rec.Schema), fields)
		return nil
	})
	return fields, err
}

// holdsTask reports whether the caller holds a user task: they are its assignee, or
// it is unclaimed and names a candidate group they belong to.
//
// **The candidate-group rule is a decision this change had to make.** A BPMN
// candidate group is free text in the model, and Atlas has never defined its
// relationship to an identity group — ADR-0042 listed exactly this ("authorization
// once the server has identity, mutable candidate groups") as a follow-up, and the
// Tasks app only ever used the attribute to say "this is a group task". Matching by
// group *name*, and by id for a modeller who wrote one, is the least surprising
// reading, and it is what keeps a group task's form prefilling before anyone claims
// it. It is deliberately spelt out here rather than buried: a stricter installation
// may want claimed-only, and that is one line.
//
// A claimed task grants nothing to the group: once somebody holds it, it is theirs.
func (s *Server) holdsTask(pr *httpapi.Principal, assignee, candidateGroups string) bool {
	if assignee != "" {
		return pr.Username != "" && strings.EqualFold(assignee, pr.Username)
	}
	wanted := strings.Split(candidateGroups, ",")
	// Ids first, and all of them, before any store read: a modeller who wrote group
	// ids never pays for the name lookup, and the lookup then happens once rather
	// than once per candidate entry.
	for _, want := range wanted {
		for _, id := range pr.GroupIDs {
			if strings.EqualFold(strings.TrimSpace(want), id) {
				return true
			}
		}
	}
	for _, id := range pr.GroupIDs {
		g, ok, err := s.groups.Get(id)
		if err != nil || !ok {
			continue
		}
		for _, want := range wanted {
			if want = strings.TrimSpace(want); want != "" && strings.EqualFold(want, g.Name) {
				return true
			}
		}
	}
	return false
}

// collectFormFieldKeys walks a form-js schema and adds every component's key to out.
// Components nest (a group holds components of its own), so it recurses; anything
// that is not an object with a string key is skipped rather than guessed at. A
// schema that will not parse contributes nothing, which fails closed.
func collectFormFieldKeys(schema []byte, out map[string]bool) {
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if k, ok := t["key"].(string); ok && k != "" {
				// A key addresses a nested value by path; the variable it reads is the
				// root of the path, and that is what an instance scope holds.
				if i := strings.IndexByte(k, '.'); i > 0 {
					k = k[:i]
				}
				out[k] = true
			}
			walk(t["components"])
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(doc)
}
