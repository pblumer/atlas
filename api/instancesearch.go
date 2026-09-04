package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// maxInstanceSearchResults caps a variable search so a single query can't return
// an unbounded response on a large deployment. There is no value index yet, so a
// content search is a walk — of one version when scoped with ?process=, of every
// instance when not — and the cap bounds both the answer's size and, on the scoped
// path, the walk itself: those indexes are newest-first, so it stops here. Active
// instances come first, then finished ones most-recently-completed first, so the
// newest matches are what survives truncation either way. An exact-key lookup is
// not a walk and is not subject to the cap: it returns the one instance it found.
const maxInstanceSearchResults = 200

// varQuery is a parsed instance-variable search. Two shapes: a structured
// name=value match (variable name exact, value substring) when the query
// contains "="; otherwise a free-text substring over variable names and values.
// All comparisons are case-insensitive.
type varQuery struct {
	structured bool
	name       string // lower-cased variable name; structured only
	needle     string // lower-cased substring (value in structured, term in free-text)
	// rawName and rawValue are the two halves as typed. The walk above is
	// case-insensitive; the value index is a byte-ordered index, so its seek needs the
	// case the operator wrote and the value the writer stored.
	rawName  string
	rawValue string
	// prefix says the value ended in "*": ask the index for every value starting with
	// rawValue rather than equal to it — the other question an ordered index answers.
	prefix bool
}

// parseVarQuery interprets a raw ?q= value. ok is false for a blank query (the
// caller returns an empty result set rather than scanning everything). A query
// like "=value" with no name degrades to free text — an empty exact name can
// never match, so treating it structurally would be a silent dead end.
func parseVarQuery(q string) (varQuery, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return varQuery{}, false
	}
	if i := strings.IndexByte(q, '='); i >= 0 {
		if name := strings.TrimSpace(q[:i]); name != "" {
			value := strings.TrimSpace(q[i+1:])
			prefix := strings.HasSuffix(value, "*")
			return varQuery{
				structured: true,
				name:       strings.ToLower(name),
				needle:     strings.ToLower(strings.TrimSuffix(value, "*")),
				rawName:    name,
				rawValue:   strings.TrimSuffix(value, "*"),
				prefix:     prefix,
			}, true
		}
	}
	return varQuery{needle: strings.ToLower(q)}, true
}

// match reports whether a variable satisfies the query.
func (p varQuery) match(v variableView) bool {
	if p.structured {
		return strings.ToLower(v.Name) == p.name && strings.Contains(strings.ToLower(v.Value), p.needle)
	}
	return strings.Contains(strings.ToLower(v.Name), p.needle) || strings.Contains(strings.ToLower(v.Value), p.needle)
}

// instanceKeyQuery reports the process instance key a query names, when the query
// is nothing but digits that fit one. It is deliberately narrow: only a bare
// number is treated as a key, so "customerType=1998" and "MT-1998" stay content
// searches. A number that is not a live or finished instance falls through to the
// content search — which is what keeps a query like "3098" finding zip=3098.
func instanceKeyQuery(q string) (uint64, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return 0, false
	}
	// Digits only, checked before parsing: ParseUint would otherwise accept a leading
	// sign, and "+123" is not something an operator pasted out of an instance list.
	// ParseUint itself rejects anything too large to be a key.
	for i := 0; i < len(q); i++ {
		if q[i] < '0' || q[i] > '9' {
			return 0, false
		}
	}
	key, err := strconv.ParseUint(q, 10, 64)
	if err != nil {
		return 0, false
	}
	return key, true
}

// newInstanceRow builds the response row for an instance, without its variables.
func newInstanceRow(key uint64, v *model.ProcessInstanceValue, defs defIndex) instanceResp {
	r := instanceResp{
		Key:           key,
		ProcessDefKey: v.ProcessDefKey,
		// ProcessInstanceState.String() reads "active" for a live record and names the
		// terminal state on a history one, so one row builder serves both families.
		State:          v.State.String(),
		CreatedAt:      v.CreatedAt,
		CompletedAt:    v.CompletedAt,
		CorrelationKey: v.CorrelationKey,
		Variables:      []variableView{},
	}
	// A row whose definition is gone (deleted after the instance ran) keeps its keys
	// and simply carries no labels.
	if d, ok := defs[r.ProcessDefKey]; ok {
		r.ProcessID, r.Version, r.VersionTag = d.ProcessID, d.Version, d.VersionTag()
	}
	return r
}

// lookupInstanceByKey answers a bare-key query with that one instance and its
// whole variable set — the operator asked for the instance, not for whichever
// variables a needle happened to match. Two point reads and one prefix scan of a
// single scope, so its cost is the instance's size and not the store's.
func lookupInstanceByKey(rv *state.ReadView, defs defIndex, key uint64) (instanceResp, bool, error) {
	pi, ok, err := rv.ProcessInstance(key)
	if err != nil || !ok {
		return instanceResp{}, false, err
	}
	row := newInstanceRow(key, pi, defs)
	err = rv.VariablesOfScope(key, func(vv *model.VariableValue) error {
		row.Variables = append(row.Variables, toVariableView(vv))
		return nil
	})
	return row, err == nil, err
}

// searchInstances resolves a parsed query against a consistent read view: an
// exact-key hit first (a point read), otherwise a walk of the live instances and
// then the history, keeping only instances with a matching variable and, on each
// row, only the variables that matched — so the UI can highlight them.
//
// Scoped to a definition (defKey != 0) the walk reads that definition's own
// indexes, so a search on a busy engine costs the version being looked at rather
// than every instance in it. Unscoped it is still a full scan: without a value
// index there is nothing to seek to, which is why the result is capped.
//
// It touches no loop-owned state: [Server.readOffLoop] took the view and copied
// the definition labels on the loop and handed both here, which is what lets the
// walk — the expensive part, and the part whose cost grows with the store — run
// off the loop without breaking the single-writer invariant (I3, ADR-0239).
func searchInstances(rv *state.ReadView, defs defIndex, defKey uint64, raw string, pred varQuery) ([]instanceResp, error) {
	// A declared name is answered by the value index: a seek to the instances holding
	// that value, rather than a walk that reads every instance's variables. It is
	// available only under a scope, because the declaration is per definition — the
	// same reason the instance list's paged path is (ADR-0241).
	if defKey != 0 && pred.structured {
		if def, ok := defs[defKey]; ok && def.cp != nil && def.cp.IsSearchableVariable(pred.rawName) {
			return searchByIndex(rv, defs, defKey, pred)
		}
	}

	// A bare instance key is answered without scanning anything. It only wins when
	// the key actually resolves — and, under a scope, when it resolves to *this*
	// definition; an ordinary number falls through to the content search below.
	if key, isKey := instanceKeyQuery(raw); isKey {
		row, found, err := lookupInstanceByKey(rv, defs, key)
		if err != nil {
			return nil, err
		}
		if found && (defKey == 0 || row.ProcessDefKey == defKey) {
			return []instanceResp{row}, nil
		}
	}

	// matchingVars returns the scope's variables that satisfy the query, or nil if
	// none do (so the caller can drop the instance).
	matchingVars := func(key uint64) ([]variableView, error) {
		var hits []variableView
		err := rv.VariablesOfScope(key, func(vv *model.VariableValue) error {
			if view := toVariableView(vv); pred.match(view) {
				hits = append(hits, view)
			}
			return nil
		})
		return hits, err
	}

	active := []instanceResp{}
	done := []instanceResp{}
	// Scoped, both indexes are newest-first, so the walk can stop the moment the cap
	// is met and still return the newest matches — the same rows reading the rest
	// would have left standing. Unscoped, the history family is in key order and
	// which matches are newest is only known once all of it has been read, so that
	// walk runs to the end and the cap is applied to the sorted result below.
	bounded := defKey != 0
	collect := func(into *[]instanceResp) func(uint64, *model.ProcessInstanceValue) error {
		return func(key uint64, v *model.ProcessInstanceValue) error {
			if bounded && len(active)+len(done) >= maxInstanceSearchResults {
				return errListTruncated
			}
			hits, err := matchingVars(key)
			if err != nil || len(hits) == 0 {
				return err
			}
			row := newInstanceRow(key, v, defs)
			row.Variables = hits
			*into = append(*into, row)
			return nil
		}
	}
	walkActive, walkDone := rv.ActiveProcessInstances, rv.CompletedProcessInstances
	if defKey != 0 {
		walkActive = func(fn func(uint64, *model.ProcessInstanceValue) error) error {
			return rv.ActiveInstancesOfDefDesc(defKey, 0, fn)
		}
		walkDone = func(fn func(uint64, *model.ProcessInstanceValue) error) error {
			return rv.FinishedInstancesOfDefDesc(defKey, 0, 0, fn)
		}
	}
	if err := unlessTruncated(walkActive(collect(&active))); err != nil {
		return nil, err
	}
	if err := unlessTruncated(walkDone(collect(&done))); err != nil {
		return nil, err
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	out := append(active, done...)
	if len(out) > maxInstanceSearchResults {
		out = out[:maxInstanceSearchResults]
	}
	return out, nil
}

// searchByIndex answers a declared name from the variable value index. The rows it
// builds carry only the variable that matched, exactly as the content walk's do, so
// the two paths are indistinguishable to the caller apart from what they cost.
//
// A hit names an instance; the instance's own record says whether it is still running
// and which definition it belongs to. The definition check is not redundant: the
// index is keyed by name and value, not by definition, so another version declaring
// the same name would land in the same range.
func searchByIndex(rv *state.ReadView, defs defIndex, defKey uint64, pred varQuery) ([]instanceResp, error) {
	out := []instanceResp{}
	err := rv.InstancesByVariable(pred.rawName, pred.rawValue, pred.prefix, func(piKey uint64) error {
		if len(out) >= maxInstanceSearchResults {
			return errListTruncated
		}
		pi, ok, err := rv.ProcessInstance(piKey)
		if err != nil || !ok {
			// An entry whose instance is gone is skipped rather than reported: the two
			// are written in one batch, so this is only reachable mid-purge.
			return err
		}
		if pi.ProcessDefKey != defKey {
			return nil
		}
		row := newInstanceRow(piKey, pi, defs)
		if err := rv.VariablesOfScope(piKey, func(vv *model.VariableValue) error {
			if vv.Name == pred.rawName {
				row.Variables = append(row.Variables, toVariableView(vv))
			}
			return nil
		}); err != nil {
			return err
		}
		out = append(out, row)
		return nil
	})
	if err = unlessTruncated(err); err != nil {
		return nil, err
	}
	return out, nil
}

// handleSearchInstances finds process instances by key or by the content of their
// variables — the operator "which instance had customerType=Business?" surface,
// and the "I have this instance key, take me to it" one.
//
// A blank query returns an empty list, not every instance. ?process=<key> narrows
// the search to one definition, which is also what makes it read an index rather
// than every instance. Finished instances are searchable only while their scope's
// variables are retained, same as the instances list. Content results are capped at
// maxInstanceSearchResults.
//
// The reading runs off the run loop through [Server.readOffLoop], so a search no
// longer stalls the processor for the length of a walk and what it reports is one
// consistent state rather than a state that moved underneath it.
func (s *Server) handleSearchInstances(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var defKey uint64
	if v := strings.TrimSpace(query.Get("process")); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid process key")
			return
		}
		defKey = n
	}
	raw := query.Get("q")
	pred, ok := parseVarQuery(raw)
	if !ok {
		httpapi.JSON(w, http.StatusOK, []instanceResp{})
		return
	}
	out := []instanceResp{}
	scanErr := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		var err error
		out, err = searchInstances(rv, defs, defKey, raw, pred)
		return err
	})
	switch {
	case errors.Is(scanErr, errLoopClosing):
		httpapi.Error(w, http.StatusServiceUnavailable, scanErr.Error())
		return
	case scanErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "search instances: "+scanErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}
