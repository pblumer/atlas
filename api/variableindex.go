package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// reindexBatchDefault / reindexBatchMax bound one call of the repair, the way the
// migration batch and the bulk cancel are bounded: a definition can hold hundreds of
// thousands of instances, and one request must not become an unbounded run of commands
// on the single writer.
const (
	reindexBatchDefault = 500
	reindexBatchMax     = 5000
)

// errReindexBatchFull stops the walk once a page is full, the way the migration batch
// stops its own.
var errReindexBatchFull = errors.New("reindex batch full")

// reindexBatchResp is what one repair round reports. Submitted counts the instances the
// command was queued for, not the memberships that changed: a command whose instance is
// already in step emits no events at all, which is what makes repeating the repair free
// (ADR-0244). Searchable echoes what the definition declares, so an operator can see
// what the index will answer for before reading the count.
type reindexBatchResp struct {
	ProcessDefKey uint64   `json:"processDefKey"`
	Searchable    []string `json:"searchable"`
	Submitted     int      `json:"submitted"`
	Remaining     bool     `json:"remaining"`
}

// handleReindexInstancesOfProcess brings a bounded batch of one definition's running
// instances back in line with what that definition declares searchable (ADR-0244).
//
// It exists for one situation and says so: a variable's membership in the value index
// is stamped by the version that wrote the value, so an instance migrated onto a
// version with a different declaration carries the old answer. Migration corrects that
// as it happens; this is the repair for the instances that were migrated before it did,
// and for an operator who wants to be sure rather than to reason about when a version
// was deployed.
//
// Running instances only. A finished instance's membership can no longer change through
// any normal path, and reaching into the history family from a command handler would
// widen the fold's reach for a one-off — see the record for why that trade was taken.
func (s *Server) handleReindexInstancesOfProcess(w http.ResponseWriter, r *http.Request) {
	defKey, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	limit := reindexBatchDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > reindexBatchMax {
		limit = reindexBatchMax
	}

	var (
		found bool
		resp  = reindexBatchResp{ProcessDefKey: defKey}
		opErr error
		keys  []uint64
	)
	// Selecting the batch reads the definition's own instance index; queueing the
	// repairs is a write. Only the second half needs the run loop, so the walk runs off
	// it — the same split the bulk migration and the bulk cancel take (ADR-0239).
	opErr = s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		def, ok := defs[defKey]
		if !ok {
			return nil
		}
		found = true
		if def.cp != nil {
			resp.Searchable = def.cp.SearchableVariables()
		}
		err := rv.ActiveInstancesOfDefDesc(defKey, 0, func(k uint64, _ *model.ProcessInstanceValue) error {
			keys = append(keys, k)
			if len(keys) >= limit {
				return errReindexBatchFull
			}
			return nil
		})
		if err != nil && !errors.Is(err, errReindexBatchFull) {
			return err
		}
		resp.Remaining = errors.Is(err, errReindexBatchFull)
		return nil
	})
	if opErr != nil {
		found = found && !errors.Is(opErr, errLoopClosing)
	}
	s.do(func() {
		if opErr != nil || !found {
			return
		}
		for _, k := range keys {
			s.proc.ReindexInstanceVariables(k)
		}
		resp.Submitted = len(keys)
		// RunUntilIdle rather than the job runner's Drive: a membership correction
		// creates no jobs, so there is nothing for a worker to pick up afterwards.
		opErr = s.proc.RunUntilIdle()
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "reindex instances: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployed definition with that key")
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}
