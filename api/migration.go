package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// migrationBatchDefault / migrationBatchMax bound one call of the batch form, the way
// the bulk cancel does: a definition with a hundred thousand running instances must not
// hold the run loop for the whole set, and the caller repeats while `remaining` is true.
const (
	migrationBatchDefault = 500
	migrationBatchMax     = 5000
)

// migrationPair is one element correspondence as the API speaks it: BPMN element ids,
// never compiled indices. The ids are what a modeler wrote and what an operator can
// recognise on a diagram; indices are an internal encoding that changes between
// versions for reasons no operator should have to know about (ADR-0162, I5).
type migrationPair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// migrationPlanResp is the answer to "what would this migration do?" — the derived
// mapping and every reason it would be refused, written nowhere. It is also exactly
// what the migrate endpoint returns when it refuses, so the two surfaces cannot
// disagree about what is wrong.
type migrationPlanResp struct {
	InstanceKey       uint64                    `json:"instanceKey"`
	FromProcessDefKey uint64                    `json:"fromProcessDefKey"`
	FromVersion       int32                     `json:"fromVersion"`
	ToProcessDefKey   uint64                    `json:"toProcessDefKey"`
	ToVersion         int32                     `json:"toVersion"`
	Mapping           []migrationPair           `json:"mapping"`
	Problems          []engine.MigrationProblem `json:"problems,omitempty"`
	// Migratable is the plain answer, so a caller does not have to interpret an empty
	// problems array itself.
	Migratable bool `json:"migratable"`
}

// migrationRequest is the body both endpoints take. Mapping overrides are optional and
// name elements by id on both sides; a reason is required by the migrate endpoint for
// the same purpose ADR-0159 requires one for a manual completion — an unexplained
// intervention on live process state is precisely what the audit record exists to
// prevent.
type migrationRequest struct {
	TargetProcessDefKey uint64          `json:"targetProcessDefKey"`
	Reason              string          `json:"reason"`
	Mapping             []migrationPair `json:"mapping"`
}

// deriveMigrationMapping builds the element mapping a migration will carry: every
// element whose BPMN id exists in both versions, then the operator's overrides on top.
//
// Matching by id is the default because the id is the only element identity a human
// controls and the only one stable across an ordinary edit — a modeler fixing a FEEL
// expression does not move ids, while every index after an inserted element shifts.
// Overrides exist for the case ids *did* move (a rename, a redraw), and are resolved to
// indices here so no model string ever reaches the log (I5).
//
// Returns the mapping and one message per override that names an element neither
// version has — refusing a typo rather than silently ignoring it, since an ignored
// override is an operator believing they mapped something they did not.
func deriveMigrationMapping(from, to *compiler.CompiledProcess, overrides []migrationPair) (map[int32]int32, []string) {
	byID := map[string]int32{}
	for i := int32(0); i < to.NodeCount(); i++ {
		if id := to.ElementBpmnId(i); id != "" {
			byID[id] = i
		}
	}
	fromByID := map[string]int32{}
	mapping := map[int32]int32{}
	for i := int32(0); i < from.NodeCount(); i++ {
		id := from.ElementBpmnId(i)
		if id == "" {
			continue
		}
		fromByID[id] = i
		if target, ok := byID[id]; ok {
			mapping[i] = target
		}
	}

	var bad []string
	for _, p := range overrides {
		src, okSrc := fromByID[strings.TrimSpace(p.From)]
		dst, okDst := byID[strings.TrimSpace(p.To)]
		switch {
		case !okSrc:
			bad = append(bad, fmt.Sprintf("the source version has no element %q", p.From))
		case !okDst:
			bad = append(bad, fmt.Sprintf("the target version has no element %q", p.To))
		default:
			mapping[src] = dst
		}
	}
	return mapping, bad
}

// migrationPairsOf renders a mapping back into the API's element-id vocabulary, ordered
// by source id so two plans of the same migration read identically.
func migrationPairsOf(from, to *compiler.CompiledProcess, mapping map[int32]int32) []migrationPair {
	pairs := make([]migrationPair, 0, len(mapping))
	for src, dst := range mapping {
		pairs = append(pairs, migrationPair{From: from.ElementBpmnId(src), To: to.ElementBpmnId(dst)})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].From < pairs[j].From })
	return pairs
}

// errMigrationBatchFull stops the batch scan once a call's limit is reached.
var errMigrationBatchFull = errors.New("migration batch full")

// planMigration answers what migrating one instance to a target definition would do.
// It reads the instance, both compiled processes and the instance's live element
// instances, derives the mapping and runs the shared validator over it — the *same*
// validator the run loop runs again when the command lands (ADR-0162), so a plan that
// says yes and a migration that then refuses can only mean the instance moved in
// between.
//
// Reads the deployment map and the store, so callers must be on the run-loop goroutine
// (invariant I3). Returns ok=false when the instance is not a running one.
func (s *Server) planMigration(piKey, targetDefKey uint64, overrides []migrationPair) (migrationPlanResp, bool, error) {
	pi, found, err := s.store.ProcessInstance(piKey)
	if err != nil {
		return migrationPlanResp{}, false, err
	}
	if !found || pi.State != model.PIActive {
		return migrationPlanResp{}, false, nil
	}
	plan := migrationPlanResp{
		InstanceKey:       piKey,
		FromProcessDefKey: pi.ProcessDefKey,
		ToProcessDefKey:   targetDefKey,
	}
	src, srcOK := s.deployments[pi.ProcessDefKey]
	dst, dstOK := s.deployments[targetDefKey]
	if !srcOK || src.cp == nil {
		plan.Problems = []engine.MigrationProblem{{Reason: "the version this instance is running is no longer deployed, so its elements cannot be mapped"}}
		return plan, true, nil
	}
	plan.FromVersion = src.Version
	if !dstOK || dst.cp == nil {
		plan.Problems = []engine.MigrationProblem{{Reason: "no deployed definition with that key"}}
		return plan, true, nil
	}
	plan.ToVersion = dst.Version
	if src.ProcessID != dst.ProcessID {
		// Migration moves an instance between *versions of its own process*. Anything
		// else is a different process with a different meaning for every element.
		plan.Problems = []engine.MigrationProblem{{Reason: fmt.Sprintf(
			"the target is process %q, but this instance runs %q — an instance can only move between versions of its own process",
			dst.ProcessID, src.ProcessID)}}
		return plan, true, nil
	}
	if pi.ProcessDefKey == targetDefKey {
		plan.Problems = []engine.MigrationProblem{{Reason: "the instance is already running this version"}}
		return plan, true, nil
	}

	mapping, bad := deriveMigrationMapping(src.cp, dst.cp, overrides)
	for _, msg := range bad {
		plan.Problems = append(plan.Problems, engine.MigrationProblem{Reason: msg})
	}
	var live []engine.MigrationElement
	if err := s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
		ei, ok, err := s.store.GetElementInstance(elKey)
		if err != nil {
			return err
		}
		if ok {
			live = append(live, engine.MigrationElementOf(elKey, ei))
		}
		return nil
	}); err != nil {
		return migrationPlanResp{}, false, err
	}
	plan.Problems = append(plan.Problems, engine.ValidateMigration(src.cp, dst.cp, live, mapping)...)
	plan.Mapping = migrationPairsOf(src.cp, dst.cp, mapping)
	plan.Migratable = len(plan.Problems) == 0
	return plan, true, nil
}

// readMigrationRequest decodes and sanity-checks the shared body.
func readMigrationRequest(w http.ResponseWriter, r *http.Request) (migrationRequest, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return migrationRequest{}, false
	}
	var req migrationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return migrationRequest{}, false
	}
	if req.TargetProcessDefKey == 0 {
		httpapi.Error(w, http.StatusBadRequest, "targetProcessDefKey is required")
		return migrationRequest{}, false
	}
	return req, true
}

// handleMigrationPlan answers what a migration would do, and writes nothing. It exists
// because the alternative is finding out by attempting it: the mapping is derived from
// two graphs an operator cannot diff by eye, and "which of my elements would be
// stranded" is the question they actually have (ADR-0162).
func (s *Server) handleMigrationPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	req, ok := readMigrationRequest(w, r)
	if !ok {
		return
	}
	var (
		plan    migrationPlanResp
		found   bool
		planErr error
	)
	s.do(func() { plan, found, planErr = s.planMigration(key, req.TargetProcessDefKey, req.Mapping) })
	switch {
	case planErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "plan migration: "+planErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no running instance with that key")
	default:
		httpapi.JSON(w, http.StatusOK, plan)
	}
}

// handleMigrateInstance rebinds one running instance to another version of its process
// (ADR-0162). It plans first and refuses with the plan itself — 409 carrying every
// problem — so a rejection is as informative as a dry run, and an operator never has to
// ask twice to find out why.
func (s *Server) handleMigrateInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	req, ok := readMigrationRequest(w, r)
	if !ok {
		return
	}
	// A migration forces a change to live process state that the engine would not have
	// made on its own, so it is explained — the same gate ADR-0159 put on a manual
	// completion, for the same reason.
	if strings.TrimSpace(req.Reason) == "" {
		httpapi.Error(w, http.StatusBadRequest, "reason is required: a migration is recorded as an operator action and must say why")
		return
	}
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	var (
		plan    migrationPlanResp
		found   bool
		planErr error
		runErr  error
	)
	s.do(func() {
		plan, found, planErr = s.planMigration(key, req.TargetProcessDefKey, req.Mapping)
		if planErr != nil || !found || !plan.Migratable {
			return
		}
		s.proc.MigrateInstance(migrationValueOf(s, plan), actor, strings.TrimSpace(req.Reason))
		runErr = s.jobRunner.Drive()
	})
	switch {
	case planErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "plan migration: "+planErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no running instance with that key")
	case !plan.Migratable:
		httpapi.JSON(w, http.StatusConflict, plan)
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "migrate: "+runErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, plan)
	}
}

// migrationValueOf turns a validated plan back into the command payload, resolving the
// element ids to the compiled indices the log carries (I5). Runs on the run loop, with
// the same deployments the plan was built from.
func migrationValueOf(s *Server, plan migrationPlanResp) model.ProcessMigrationValue {
	src, dst := s.deployments[plan.FromProcessDefKey], s.deployments[plan.ToProcessDefKey]
	byIDFrom, byIDTo := map[string]int32{}, map[string]int32{}
	for i := int32(0); i < src.cp.NodeCount(); i++ {
		byIDFrom[src.cp.ElementBpmnId(i)] = i
	}
	for i := int32(0); i < dst.cp.NodeCount(); i++ {
		byIDTo[dst.cp.ElementBpmnId(i)] = i
	}
	mapping := make(map[int32]int32, len(plan.Mapping))
	for _, p := range plan.Mapping {
		mapping[byIDFrom[p.From]] = byIDTo[p.To]
	}
	return model.NewProcessMigration(plan.InstanceKey, plan.FromProcessDefKey, plan.ToProcessDefKey, mapping)
}

// migrateBatchResp is the answer to migrating a whole version's instances: what moved,
// what was refused and why, and whether this call hit its cap.
type migrateBatchResp struct {
	ToProcessDefKey uint64              `json:"toProcessDefKey"`
	Migrated        int                 `json:"migrated"`
	Refused         []migrationPlanResp `json:"refused,omitempty"`
	Remaining       bool                `json:"remaining"`
}

// handleMigrateInstancesOfProcess migrates a bounded batch of one definition's running
// instances to another version. Each instance is its own command and its own event, so
// a refusal on the four-hundredth does not roll back the three hundred and ninety-nine
// that were fine (ADR-0162) — the batch is an API convenience, never one durable
// transaction. The caller repeats while `remaining` is true, as the bulk cancel does.
func (s *Server) handleMigrateInstancesOfProcess(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	fromKey, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	req, ok := readMigrationRequest(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		httpapi.Error(w, http.StatusBadRequest, "reason is required: a migration is recorded as an operator action and must say why")
		return
	}
	limit := migrationBatchDefault
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = n
	}
	if limit > migrationBatchMax {
		limit = migrationBatchMax
	}
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	var (
		found bool
		resp  = migrateBatchResp{ToProcessDefKey: req.TargetProcessDefKey}
		opErr error
	)
	s.do(func() {
		if _, ok := s.deployments[fromKey]; !ok {
			return
		}
		found = true
		var keys []uint64
		err := s.store.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
			if v.ProcessDefKey != fromKey {
				return nil
			}
			keys = append(keys, k)
			if len(keys) >= limit {
				return errMigrationBatchFull
			}
			return nil
		})
		if err != nil && !errors.Is(err, errMigrationBatchFull) {
			opErr = err
			return
		}
		resp.Remaining = errors.Is(err, errMigrationBatchFull)
		for _, k := range keys {
			plan, ok, err := s.planMigration(k, req.TargetProcessDefKey, req.Mapping)
			if err != nil {
				opErr = err
				return
			}
			if !ok {
				continue // finished while this batch was being scanned
			}
			if !plan.Migratable {
				resp.Refused = append(resp.Refused, plan)
				continue
			}
			s.proc.MigrateInstance(migrationValueOf(s, plan), actor, strings.TrimSpace(req.Reason))
			resp.Migrated++
		}
		if resp.Migrated > 0 {
			opErr = s.jobRunner.Drive()
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "migrate instances: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployed definition with that key")
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}
