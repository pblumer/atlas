package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/pblumer/atlas/api/httpapi"
)

// Confidential projects (ADR-draft-confidential-projects).
//
// ADR-0275 made an instance's data its project's members' to read, and left
// operators and admins reading everything: they could already list every instance,
// so the per-instance endpoint was not where that would be narrowed. On a shared
// installation that is exactly the gap. The operator of one team reads the
// variables, the timeline and the incidents of another team's instances, because
// "operator" is a role on the server and not a relationship to the work.
//
// A project its owner marks confidential closes that gap for that project. Its
// instances are visible to whoever the project grants viewer or better — its
// owner, its members, an admin — and to nobody else, whatever role they hold on the
// server. "Visible" covers reading (lists, counts, search, the per-instance views)
// and acting (cancel, terminate, resolve an incident, complete or fail a job,
// claim or complete a task the caller does not hold).
//
// It is opt-in per project and changes nothing for a project that is not marked:
// the operator role keeps meaning what it meant. That is the whole reason it is a
// flag rather than a new default — a server whose operators run every team's
// incidents is a legitimate way to run Atlas, and this must not break it.
//
// What it is not, stated where the next reader looks: it is a visibility rule in
// the API, not encryption. An admin reads everything; a backup holds everything;
// the OpenSearch exporter mirrors the log, and with it every confidential
// instance, into an index this rule does not reach. The handbook says so, and
// marking a project confidential on a server that exports says so in the answer.
//
// The rule is one predicate, [veil]: the definition keys whose instances a caller
// may not see. It is computed per request on the run loop, from the projects marked
// confidential ([confidentialIndex]) and the deployment map, and it is empty — no
// work, no allocation beyond the check — on a server where no project is marked,
// and for an admin.

// confidentialExportWarning is what marking a project confidential answers on a
// server that exports its log: the mark governs this API, and the exporter is not
// this API.
const confidentialExportWarning = "this server exports its event log to OpenSearch (ADR-0114): " +
	"the instances of a confidential application are exported like every other, and whoever " +
	"can read that index reads them. The mark governs this server's API, not the index."

// confidentialIndex is the projects marked confidential, by id, kept in memory so
// that a request on a server without any costs nothing. The project store tells it
// about every save and delete ([sidecar.Observe]), so the index cannot drift from
// the records whatever path wrote them; it is seeded from the store at startup.
//
// It carries its own lock rather than leaning on the run loop: the project store
// is loop-owned today, but an index that silently assumed so would be the first
// thing to break when a writer moved, and the cost is one uncontended lock.
type confidentialIndex struct {
	mu   sync.RWMutex
	byID map[string]project
}

func newConfidentialIndex() *confidentialIndex {
	return &confidentialIndex{byID: map[string]project{}}
}

// observe is the project store's change hook.
func (c *confidentialIndex) observe(id string, rec project, present bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if present && rec.Confidential {
		c.byID[id] = rec
		return
	}
	delete(c.byID, id)
}

// seed loads the marked projects from a full listing.
func (c *confidentialIndex) seed(all []project) {
	for _, p := range all {
		c.observe(p.ID, p, true)
	}
}

// snapshot copies the marked projects out. Nil-safe: a server built without an
// index has no confidential project.
func (c *confidentialIndex) snapshot() []project {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.byID) == 0 {
		return nil
	}
	out := make([]project, 0, len(c.byID))
	for _, p := range c.byID {
		out = append(out, p)
	}
	return out
}

// none reports whether no project is marked — the check every request makes first,
// so it copies nothing. Nil-safe like snapshot.
func (c *confidentialIndex) none() bool {
	if c == nil {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID) == 0
}

// veil is the set of definitions whose instances one caller may not see. The zero
// value hides nothing.
type veil struct {
	hidden map[uint64]struct{}
}

// hides reports whether instances of a definition are hidden from the caller.
func (v veil) hides(defKey uint64) bool {
	if v.hidden == nil {
		return false
	}
	_, ok := v.hidden[defKey]
	return ok
}

// active reports whether anything is hidden at all — the check every caller makes
// before paying for a lookup.
func (v veil) active() bool { return len(v.hidden) > 0 }

// veilOnLoop computes what pr may not see. Must run on the run loop: it reads the
// deployment map.
//
// A project hides its instances from pr when it is marked confidential and grants
// pr less than viewer — the same effectiveRole every artifact route asks, so a
// member, a group member, the owner, an admin and a credential whose reach names
// the project all see it, and nobody else does. A nil principal on an
// authenticated server sees nothing confidential, which is the direction to fail.
//
// Hidden are the definitions deployed from such a project now, and the ones that
// were deployed from it and deleted while it was marked ([project.RetiredDefinitions]):
// a deleted definition's finished instances stay in the store, and without that
// record they would have no project left to be hidden by.
func (s *Server) veilOnLoop(pr *httpapi.Principal) veil {
	if !s.authEnabled {
		return veil{}
	}
	marked := s.confidential.snapshot()
	if len(marked) == 0 {
		return veil{}
	}
	var closed map[string]bool
	hidden := map[uint64]struct{}{}
	for _, p := range marked {
		if scopeRank(p.effectiveRole(pr, true)) >= scopeRank(ScopeRoleViewer) {
			continue
		}
		if closed == nil {
			closed = map[string]bool{}
		}
		closed[p.ID] = true
		for _, k := range p.RetiredDefinitions {
			hidden[k] = struct{}{}
		}
	}
	if closed == nil {
		return veil{}
	}
	for key, d := range s.deployments {
		if d.ProjectID != "" && closed[d.ProjectID] {
			hidden[key] = struct{}{}
		}
	}
	if len(hidden) == 0 {
		return veil{}
	}
	return veil{hidden: hidden}
}

// veilFor is veilOnLoop for a request, from off the loop.
func (s *Server) veilFor(r *http.Request) veil {
	return s.veilForPrincipal(httpapi.PrincipalFrom(r.Context()))
}

// veilForPrincipal is veilOnLoop from off the loop. It must not be called on it.
func (s *Server) veilForPrincipal(pr *httpapi.Principal) veil {
	if !s.authEnabled || s.confidential.none() {
		return veil{} // the common case answers without a turn on the loop
	}
	var v veil
	s.do(func() { v = s.veilOnLoop(pr) })
	return v
}

// isMachinePrincipal reports whether a principal is a credential rather than a
// person: the server's own service identity, a deploy agent, an API token. They are
// the identities the job protocol is spoken by, and a veil on that protocol would
// stop a worker from serving the very process it was deployed for.
func isMachinePrincipal(pr *httpapi.Principal) bool {
	return pr != nil && strings.HasPrefix(pr.UserID, "system:")
}

// veilKind says what a route's {key} names, so the route table can declare which
// of its routes open one instance and the mount can put the check in front of them
// (see apiOp.veil). Declaring it beside the role is what makes forgetting it a
// failing test rather than a leak: TestEveryInstanceRouteIsVeiled walks the table.
type veilKind uint8

const (
	veilNone veilKind = iota
	// veilInstance: {key} is a process instance, or one of its element instances.
	veilInstance
	// veilElement: {key} is an element instance holding an incident.
	veilElement
	// veilJob: {key} is a job. Machine credentials pass: see isMachinePrincipal.
	veilJob
	// veilDefinition: {key} is a deployed definition.
	veilDefinition
)

// notFound is what a hidden key answers: the route's own answer for a key that
// names nothing, so a 404 here reads like every other 404 on it.
func (k veilKind) notFound() string {
	switch k {
	case veilElement:
		return "no incident on that element instance"
	case veilJob:
		return "no job with that key"
	case veilDefinition:
		return "no deployment with that key"
	}
	return "no instance with that key"
}

// veiled puts the check in front of a route whose {key} the kind names. A key
// that does not parse, or names nothing, passes through to the handler, which
// answers it as it always did.
func (s *Server) veiled(kind veilKind, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled || s.confidential.none() {
			h(w, r)
			return
		}
		pr := httpapi.PrincipalFrom(r.Context())
		if kind == veilJob && isMachinePrincipal(pr) {
			h(w, r)
			return
		}
		key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
		if err != nil {
			h(w, r)
			return
		}
		var hidden bool
		var readErr error
		s.do(func() {
			v := s.veilOnLoop(pr)
			if !v.active() {
				return
			}
			var def uint64
			var ok bool
			if def, ok, readErr = s.definitionOfOnLoop(kind, key); readErr == nil && ok {
				hidden = v.hides(def)
			}
		})
		switch {
		case readErr != nil:
			httpapi.Error(w, http.StatusInternalServerError, "read: "+readErr.Error())
		case hidden:
			httpapi.Error(w, http.StatusNotFound, kind.notFound())
		default:
			h(w, r)
		}
	}
}

// definitionOfOnLoop answers the definition a key's instance runs, or ok=false
// when the key names nothing of its kind. Must run on the run loop.
func (s *Server) definitionOfOnLoop(kind veilKind, key uint64) (uint64, bool, error) {
	switch kind {
	case veilDefinition:
		return key, true, nil
	case veilJob:
		j, ok, err := s.store.GetJob(key)
		if err != nil || !ok {
			return 0, false, err
		}
		return s.definitionOfInstanceOnLoop(j.ProcessInstanceKey)
	}
	return s.definitionOfInstanceOnLoop(key)
}

// definitionOfInstanceOnLoop resolves a process instance key, or an element
// instance key, to the definition its instance runs. A finished instance resolves
// too: its history record keeps the definition.
func (s *Server) definitionOfInstanceOnLoop(key uint64) (uint64, bool, error) {
	if ei, ok, err := s.store.GetElementInstance(key); err != nil {
		return 0, false, err
	} else if ok {
		return ei.ProcessDefKey, true, nil
	}
	pi, ok, err := s.store.ProcessInstance(key)
	if err != nil || !ok {
		return 0, false, err
	}
	return pi.ProcessDefKey, true, nil
}

// instanceHiddenOnLoop reports whether v hides the instance a key belongs to. A
// key that names nothing is not hidden — the caller answers it as missing anyway.
func (s *Server) instanceHiddenOnLoop(v veil, key uint64) (bool, error) {
	if !v.active() {
		return false, nil
	}
	def, ok, err := s.definitionOfInstanceOnLoop(key)
	if err != nil || !ok {
		return false, err
	}
	return v.hides(def), nil
}

// retireConfidentialOnLoop records, on a confidential project, that one of its
// definitions is being deleted (see [project.RetiredDefinitions]). A project that is
// not marked, or not there, records nothing. Must run on the run loop.
func (s *Server) retireConfidentialOnLoop(projectID string, defKey uint64) error {
	if projectID == "" {
		return nil
	}
	p, ok, err := s.projects.Get(projectID)
	if err != nil || !ok || !p.Confidential {
		return err
	}
	if slices.Contains(p.RetiredDefinitions, defKey) {
		return nil
	}
	p.RetiredDefinitions = append(p.RetiredDefinitions, defKey)
	return s.projects.Save(p)
}
