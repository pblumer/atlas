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

// errListTruncated stops a bounded list scan once the page cap is reached. It is a
// sentinel to break the scan early, not a failure.
var errListTruncated = errors.New("list page full")

// unlessTruncated maps a bounded-scan result to a real error: the page-full sentinel
// (a deliberate early stop) becomes nil, any other error passes through. It lets the
// capped list handlers report a genuine scan failure without treating truncation as one.
func unlessTruncated(err error) error {
	if errors.Is(err, errListTruncated) {
		return nil
	}
	return err
}

const (
	// maxInstanceListDefault and maxInstanceListMax bound how many active and how many
	// completed instances GET /api/v1/instances returns (each capped independently),
	// so the endpoint can never try to enrich and serialize hundreds of thousands of
	// rows — the shape that made the operations page unreachable during the reported
	// flood. Raise per request with ?limit= (up to the max); narrow to one definition
	// with ?process=. The overview reads per-definition counts from
	// /api/v1/instances/summary instead, so the cap does not skew its tallies.
	maxInstanceListDefault = 1000
	maxInstanceListMax     = 10000
)

// instanceListQuery is a parsed GET /api/v1/instances request.
//
// Two shapes, and the difference is where the work happens. Unscoped, the endpoint
// walks the whole instance family and keeps the first `limit` of each half: bounded
// in what it returns, but not in what it reads. Scoped to a definition with
// ?process=, it reads that definition's own index instead, so the cost is the page
// and not the store — which is the shape that survives a few hundred thousand
// instances, and the only shape ?state= and ?before= paging is offered in.
type instanceListQuery struct {
	limit  int
	defKey uint64
	hasDef bool
	// state is "" (both halves), "active" or "finished". A single half is what a
	// cursor can page, since the two halves are ordered by different things.
	state string
	// before is the exclusive cursor: an instance key for the active half, and the
	// (completion time, instance key) pair for the finished half — completion order
	// is not key order, so the key alone cannot name a position in it.
	beforeKey    uint64
	beforeDoneAt int64
	hasBefore    bool
}

// parseInstanceListQuery reads the query string, reporting the first thing wrong
// with it rather than quietly serving something else.
func parseInstanceListQuery(q map[string][]string) (instanceListQuery, error) {
	get := func(name string) string {
		if v, ok := q[name]; ok && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	out := instanceListQuery{limit: maxInstanceListDefault}
	if v := get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return out, errors.New("invalid limit (want a positive integer)")
		}
		out.limit = n
	}
	if out.limit > maxInstanceListMax {
		out.limit = maxInstanceListMax
	}
	if v := get("process"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return out, errors.New("invalid process key")
		}
		out.defKey, out.hasDef = n, true
	}
	// "completed" and "all" are spellings that predate this parameter meaning
	// anything: callers wrote them and the handler ignored them. They keep working,
	// and now do what those callers were asking for — "completed" is "finished"
	// (which is the canonical name because it covers termination as well as
	// completion), and "all" is the default, both halves. Anything else is refused
	// rather than ignored: a filter silently dropped is how a caller comes to
	// believe a partial answer is the whole one.
	switch v := get("state"); v {
	case "", "all":
		out.state = ""
	case "active", "finished":
		out.state = v
	case "completed":
		out.state = "finished"
	default:
		return out, errors.New(`invalid state (want "active", "finished" or "all")`)
	}
	before := get("before")
	if before != "" {
		if out.state == "" {
			return out, errors.New("before requires state=active or state=finished (the two halves are ordered differently, so one cursor cannot address both)")
		}
		var err error
		if out.beforeDoneAt, out.beforeKey, err = parseInstanceCursor(out.state, before); err != nil {
			return out, err
		}
		out.hasBefore = true
	}
	if out.hasBefore && !out.hasDef {
		return out, errors.New("before requires process=<definition key>: the cursor addresses a position in one definition's index, and that index is what makes the page cost the page rather than the store")
	}
	return out, nil
}

// parseInstanceCursor reads a cursor produced by formatInstanceCursor: a bare
// instance key for the active half, "<completedAt>.<key>" for the finished half.
func parseInstanceCursor(state, raw string) (completedAt int64, key uint64, err error) {
	bad := errors.New("invalid before cursor (use the X-Instances-Next-Cursor value from the previous page)")
	if state == "active" {
		key, perr := strconv.ParseUint(raw, 10, 64)
		if perr != nil {
			return 0, 0, bad
		}
		return 0, key, nil
	}
	at, k, ok := strings.Cut(raw, ".")
	if !ok {
		return 0, 0, bad
	}
	completedAt, perr := strconv.ParseInt(at, 10, 64)
	if perr != nil {
		return 0, 0, bad
	}
	key, perr = strconv.ParseUint(k, 10, 64)
	if perr != nil {
		return 0, 0, bad
	}
	return completedAt, key, nil
}

// formatInstanceCursor renders the position of the last row on a page, so the next
// call resumes strictly below it.
func formatInstanceCursor(state string, r instanceResp) string {
	if state == "active" {
		return strconv.FormatUint(r.Key, 10)
	}
	return strconv.FormatInt(r.CompletedAt, 10) + "." + strconv.FormatUint(r.Key, 10)
}

// instanceLister is the read surface the listing needs. Both *state.Store and
// *state.ReadView satisfy it; the handler passes a view, so the reading happens off
// the run loop against one consistent state (I3, ADR-0157).
type instanceLister interface {
	ActiveInstancesOfDefDesc(procDefKey, before uint64, fn func(key uint64, pi *model.ProcessInstanceValue) error) error
	FinishedInstancesOfDefDesc(procDefKey uint64, beforeCompletedAt int64, beforeKey uint64, fn func(key uint64, pi *model.ProcessInstanceValue) error) error
	ActiveProcessInstances(fn func(key uint64, pi *model.ProcessInstanceValue) error) error
	CompletedProcessInstances(fn func(key uint64, pi *model.ProcessInstanceValue) error) error
	ElementInstancesOfProcess(procKey uint64, fn func(elKey uint64) error) error
	VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
}

// instancePage is one answer: the rows, whether the page was capped, and where the
// next page resumes.
type instancePage struct {
	rows       []instanceResp
	truncated  bool
	nextCursor string
}

// listInstances builds a page against a read view. It never reads loop-owned state
// — the definition labels were copied on the loop and handed in — so the scan runs
// on the request goroutine.
func listInstances(rv instanceLister, defs instanceDefs, q instanceListQuery) (instancePage, error) {
	var page instancePage

	// fill turns one instance record into a row: its definition labels, its live
	// element instances (running rows only), and its variables.
	fill := func(key uint64, v *model.ProcessInstanceValue) (instanceResp, error) {
		r := newInstanceRow(key, v, defs)
		if v.State == model.PIActive {
			if err := rv.ElementInstancesOfProcess(key, func(uint64) error {
				r.ElementInstances++
				return nil
			}); err != nil {
				return r, err
			}
		}
		err := rv.VariablesOfScope(key, func(vv *model.VariableValue) error {
			r.Variables = append(r.Variables, toVariableView(vv))
			return nil
		})
		return r, err
	}
	// collect appends up to limit rows, stopping the scan with the page-full
	// sentinel rather than enriching rows it will not return.
	collect := func(into *[]instanceResp) func(uint64, *model.ProcessInstanceValue) error {
		return func(key uint64, v *model.ProcessInstanceValue) error {
			if len(*into) >= q.limit {
				page.truncated = true
				return errListTruncated
			}
			r, err := fill(key, v)
			if err != nil {
				return err
			}
			*into = append(*into, r)
			return nil
		}
	}

	// One half. Scoped to a definition this pages that definition's own index —
	// the shape whose cost is the page rather than the store, and the only one a
	// cursor can address. Unscoped it is the capped family scan, filtered to the
	// half that was asked for.
	if q.state != "" {
		rows := []instanceResp{}
		var err error
		switch {
		case q.hasDef && q.state == "active":
			err = rv.ActiveInstancesOfDefDesc(q.defKey, q.beforeKey, collect(&rows))
		case q.hasDef:
			err = rv.FinishedInstancesOfDefDesc(q.defKey, q.beforeDoneAt, q.beforeKey, collect(&rows))
		case q.state == "active":
			err = rv.ActiveProcessInstances(collect(&rows))
		default:
			err = rv.CompletedProcessInstances(collect(&rows))
			sort.Slice(rows, func(i, j int) bool { return rows[i].CompletedAt > rows[j].CompletedAt })
		}
		if err = unlessTruncated(err); err != nil {
			return page, err
		}
		page.rows = rows
		// Only the index-backed halves have a position a cursor can name; the capped
		// family scan has none, so a truncated page there is flagged without one.
		if q.hasDef && page.truncated && len(rows) > 0 {
			page.nextCursor = formatInstanceCursor(q.state, rows[len(rows)-1])
		}
		return page, nil
	}

	active := []instanceResp{}
	done := []instanceResp{}
	if q.hasDef {
		// Both halves of one definition, newest first, each off its own index.
		if err := unlessTruncated(rv.ActiveInstancesOfDefDesc(q.defKey, 0, collect(&active))); err != nil {
			return page, err
		}
		if err := unlessTruncated(rv.FinishedInstancesOfDefDesc(q.defKey, 0, 0, collect(&done))); err != nil {
			return page, err
		}
		page.rows = append(active, done...)
		return page, nil
	}

	// Unscoped: the whole family, capped. Bounded in what it returns, not in what it
	// reads — narrow with ?process= to get the index-backed path instead.
	if err := unlessTruncated(rv.ActiveProcessInstances(collect(&active))); err != nil {
		return page, err
	}
	if err := unlessTruncated(rv.CompletedProcessInstances(collect(&done))); err != nil {
		return page, err
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CompletedAt > done[j].CompletedAt })
	page.rows = append(active, done...)
	return page, nil
}

// handleListInstances lists process instances — live ones (with their current
// token count) followed by finished ones, most recently completed first (ADR-0017).
// It is the operator "instances" view.
//
// Narrowed to one definition with ?process=, it reads that definition's own
// indexes, so a version's instances cost what the page costs instead of a walk of
// every instance in the engine. ?state=active|finished then returns a single half
// and, when the page is capped, hands back X-Instances-Next-Cursor for the next
// (older) page — the paging the live view needs to stay usable at a few hundred
// thousand instances.
func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	q, err := parseInstanceListQuery(r.URL.Query())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		rv   *state.ReadView
		defs instanceDefs
	)
	s.do(func() {
		rv = s.store.ReadView()
		defs = s.snapshotInstanceDefs()
	})
	if rv == nil { // the run loop is closing: nothing ran, and there is no answer
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	defer func() { _ = rv.Close() }()

	page, err := listInstances(rv, defs, q)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list instances: "+err.Error())
		return
	}
	if page.truncated {
		// Signal that the page was capped so a client can page with ?before=/?limit=
		// rather than assume it received every instance.
		w.Header().Set("X-Instances-Truncated", "true")
		if page.nextCursor != "" {
			w.Header().Set("X-Instances-Next-Cursor", page.nextCursor)
		}
	}
	httpapi.JSON(w, http.StatusOK, page.rows)
}
