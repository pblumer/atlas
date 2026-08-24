package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// Where a repair form came from. ADR-0169 gave a task its own binding; this adds two
// more sources so the common case needs no authoring at all
// (ADR-draft-repair-forms-without-authoring). The names are part of the HTTP contract —
// the dialog shows the operator which of the three it is looking at, because a form
// written for *this* task and one a machine derived deserve different confidence.
const (
	repairSourceTask      = "task"      // the modeler bound it to this element (ADR-0169)
	repairSourceConnector = "connector" // an operator bound it to the task's connector kind
	repairSourceDerived   = "derived"   // built from the task's input mappings, unauthored
)

// repairFormResp is the answer to "what form do I show for this incident?": the schema to
// render, what to call it, and which of the three sources it came from.
//
// FormID is empty for a derived form — there is no stored record behind it. That is not a
// gap in the response: a derived form is computed from the model on every read, which is
// what keeps it current when the model changes underneath it.
type repairFormResp struct {
	Source string `json:"source"`
	FormID string `json:"formId,omitempty"`
	Name   string `json:"name"`
	Schema any    `json:"schema"`
}

// derivedRepairSchema builds a form-js schema from the process variables a task's input
// mappings read (ADR-0068): each mapping's FEEL source already reports the variables it
// references, and those are the values the task was handed — so they are the values a
// retry depends on. Returns nil when the task has no input mappings, which is the honest
// answer rather than an empty form.
//
// Every field is a plain text input, deliberately. The derivation knows the *names* a
// task reads and nothing else: not their types, not their valid ranges, not which of them
// is the problem. A derived number field that refused "0042" would be a constraint nobody
// authored, and the write path takes the value as typed either way. The leading text is
// part of the honesty — it says the form was derived and that the raw editor holds
// everything this one does not.
func derivedRepairSchema(cp *compiler.CompiledProcess, elementID int32) any {
	if cp == nil {
		return nil
	}
	ins := cp.IOInputs(elementID)
	if len(ins) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, m := range ins {
		if m.Source == nil {
			continue
		}
		for _, v := range m.Source.Inputs() {
			if v != "" && !seen[v] {
				seen[v] = true
				names = append(names, v)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	// The mappings' own order is the modeler's order, which carries no meaning for a
	// reader scanning a list of fields; sorting makes the form stable across deploys and
	// predictable to look at.
	sort.Strings(names)

	components := []any{map[string]any{
		"type": "text",
		"text": "These are the variables this task reads. Nobody authored a form for it, so " +
			"they were derived from its input mappings — correct what is wrong and retry. " +
			"Use **Fix variables…** for anything not listed here.",
	}}
	for _, n := range names {
		components = append(components, map[string]any{
			"type":  "textfield",
			"key":   n,
			"label": n,
		})
	}
	return map[string]any{"type": "default", "components": components}
}

// repairFormSourceOf names which of the three sources would answer for this element, or ""
// when none would. It is the precedence itself, stated once; resolveRepairForm below adds
// the schema, and the incident listings use this alone.
//
// Separate from resolveRepairForm because the listings loop over up to thousands of
// incidents on the run-loop goroutine and only need to know *whether* a form exists, to
// decide whether the row offers the action. Building a derived schema per row to answer
// that would allocate a form nobody asked for on a path that must not block writers
// (invariant I3), so the derived arm here only asks whether any input mapping references a
// variable at all — the same condition, without the document.
func repairFormSourceOf(cp *compiler.CompiledProcess, elementID int32, byKind map[string]string) string {
	if cp == nil {
		return ""
	}
	if cp.RepairForm(elementID) != "" {
		return repairSourceTask
	}
	if _, kind := elementConnectorRef(cp, elementID); kind != "" && byKind[kind] != "" {
		return repairSourceConnector
	}
	for _, m := range cp.IOInputs(elementID) {
		if m.Source != nil && len(m.Source.Inputs()) > 0 {
			return repairSourceDerived
		}
	}
	return ""
}

// resolveRepairForm answers which repair form an incident on this element should show,
// most specific first (ADR-draft-repair-forms-without-authoring):
//
//  1. the form the modeler bound to this task (ADR-0169) — written for *this* element;
//  2. the form an operator bound to the task's connector kind — written once for how this
//     integration fails, and applying to every model that uses it;
//  3. a form derived from the task's input mappings — no authoring at all.
//
// The order is the whole point: each step knows strictly less than the one above it, so a
// general answer can never shadow a specific one, and authoring a form for one troublesome
// task is enough to make it win without unbinding anything.
//
// Returns ok=false when none of the three applies, which is a complete answer and means
// the raw JSON editor is the way. The two stored sources are resolved to their schema by
// the caller's loader, so this function stays a pure statement of the precedence.
func resolveRepairForm(cp *compiler.CompiledProcess, elementID int32, byKind map[string]string) (source, formID string, derived any, ok bool) {
	switch repairFormSourceOf(cp, elementID, byKind) {
	case repairSourceTask:
		return repairSourceTask, cp.RepairForm(elementID), nil, true
	case repairSourceConnector:
		_, kind := elementConnectorRef(cp, elementID)
		return repairSourceConnector, byKind[kind], nil, true
	case repairSourceDerived:
		return repairSourceDerived, "", derivedRepairSchema(cp, elementID), true
	}
	return "", "", nil, false
}

// handleIncidentRepairForm answers "what form do I show for this incident?" for one parked
// element instance. One endpoint rather than three rules in three surfaces: the live view,
// the replay panel and the incidents table all ask the same question, and a precedence
// implemented separately in each is one that eventually differs.
//
// 404 means no form applies — a complete answer, not a failure, and the surface falls back
// to the raw editor exactly as it did before any of this existed.
func (s *Server) handleIncidentRepairForm(w http.ResponseWriter, r *http.Request) {
	elKey, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid element instance key")
		return
	}
	var (
		resp    repairFormResp
		found   bool
		loadErr error
		scanErr error
	)
	s.do(func() {
		ei, ok, err := s.store.GetElementInstance(elKey)
		if err != nil {
			scanErr = err
			return
		}
		if !ok {
			return
		}
		d, ok := s.deployments[ei.ProcessDefKey]
		if !ok || d.cp == nil {
			return // the instance outlived its deployment; nothing can say what it reads
		}
		byKind, err := s.settings.getRepairForms()
		if err != nil {
			scanErr = err
			return
		}
		source, id, derived, ok := resolveRepairForm(d.cp, ei.ElementId, byKind)
		if !ok {
			return
		}
		found = true
		resp.Source = source
		if derived != nil {
			resp.Schema = derived
			resp.Name = "Derived from this task's inputs"
			return
		}
		rec, ok, err := s.forms.Get(id)
		if err != nil {
			loadErr = err
			return
		}
		if !ok {
			// A binding pointing at a form that has since been deleted: stale, not broken.
			// Reported as "no form" so the surface falls back to the raw editor rather
			// than offering something it cannot open.
			found = false
			return
		}
		resp.FormID = rec.ID
		resp.Name = rec.Name
		if resp.Name == "" {
			resp.Name = rec.ID
		}
		// The stored schema is a raw JSON document; emit it as JSON rather than as a
		// re-escaped string, so the client gets the form-js form directly.
		resp.Schema = json.RawMessage(rec.Schema)
	})
	switch {
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "resolve repair form: "+scanErr.Error())
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read form: "+loadErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no repair form applies to this incident")
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}

// repairFormsResp is the operator-managed connector-kind → form-id table, together with
// the kinds that exist to bind one to.
//
// Kinds ride along because the Console renders a row per kind, and a list hardcoded in the
// browser would be a second copy of managedConnectorKinds — one that silently omits every
// kind added after it was written, so the new integration would be the one nobody could
// give guidance for. Omitted from the request body: the server owns the set.
type repairFormsResp struct {
	ByKind map[string]string `json:"byKind"`
	Kinds  []string          `json:"kinds,omitempty"`
}

// handleGetRepairForms reports which connector kinds have a repair form bound. Readable by
// anyone who can see incidents: it says which integrations have guidance, not what any
// instance holds.
func (s *Server) handleGetRepairForms(w http.ResponseWriter, r *http.Request) {
	var (
		byKind map[string]string
		err    error
	)
	s.do(func() { byKind, err = s.settings.getRepairForms() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read repair forms: "+err.Error())
		return
	}
	kinds := make([]string, 0, len(managedConnectorKinds))
	for _, k := range managedConnectorKinds {
		kinds = append(kinds, k.name)
	}
	httpapi.JSON(w, http.StatusOK, repairFormsResp{ByKind: byKind, Kinds: kinds})
}

// handlePutRepairForms replaces the whole connector-kind → form-id table. Admin-only when
// auth is on, like the other org-wide settings: it changes what every operator is shown on
// every incident of that kind.
//
// The whole table rather than one kind at a time, because that is what the Console edits —
// a panel of kinds saved together — and a whole-table write cannot leave two callers'
// partial updates interleaved.
func (s *Server) handlePutRepairForms(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body repairFormsResp
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	var err error
	s.do(func() { err = s.settings.saveRepairForms(body.ByKind) })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save repair forms: "+err.Error())
		return
	}
	// Echo the stored table back, so a caller sees exactly what was kept: the save drops
	// empty entries, and a client that assumed otherwise would show a binding the server
	// does not have.
	s.handleGetRepairForms(w, r)
}

// repairKindsLookup returns a memoizing reader for the connector-kind → form-id table.
//
// A closure rather than a plain read for the same reason incidentConnectorLookup is one:
// both incident listings loop over up to thousands of rows on the run-loop goroutine, and
// a per-row read of the settings sidecar would put a file read in front of every writer
// waiting on that loop (invariant I3). The table is read at most once per listing, and a
// read failure answers "nothing bound" rather than failing the whole listing — an incident
// an operator cannot see is worse than one whose Repair action is missing.
func (s *Server) repairKindsLookup() func() map[string]string {
	var (
		byKind map[string]string
		loaded bool
	)
	return func() map[string]string {
		if !loaded {
			loaded = true
			byKind, _ = s.settings.getRepairForms()
		}
		return byKind
	}
}
