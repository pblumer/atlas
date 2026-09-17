package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// forkParked is one element the instance currently holds a token on, and where that
// work would pick up again in the target version — empty when the target has no element
// of that id, which is exactly the case a fork is being considered for.
type forkParked struct {
	ElementID string `json:"elementId"`
	ResumeAt  string `json:"resumeAt,omitempty"`
}

// forkPlanResp answers "if this instance cannot be rebound, where would it continue?"
// (ADR-draft-forked-instance-migration). Like the migration plan it writes nothing, and
// it is also what the fork endpoint returns when it refuses — so the two surfaces cannot
// disagree about what is wrong.
type forkPlanResp struct {
	InstanceKey       uint64 `json:"instanceKey"`
	FromProcessDefKey uint64 `json:"fromProcessDefKey"`
	FromVersion       int32  `json:"fromVersion"`
	ToProcessDefKey   uint64 `json:"toProcessDefKey"`
	ToVersion         int32  `json:"toVersion"`
	// Resume is where the successor will start, by element id in the target version:
	// what the caller asked for, or the proposal derived from where the tokens are now.
	Resume []string `json:"resume"`
	// Parked is where this instance's tokens are, and which of them paired with an
	// element of the same id in the target version. It is the reason a fork is or is not
	// one click: an operator reads it to see what the rename or the redraw cost them.
	Parked []forkParked `json:"parked,omitempty"`
	// Candidates is every element of the target version work could legitimately resume
	// at, so choosing one does not mean guessing which choices the validator will take.
	Candidates []string `json:"candidates,omitempty"`
	// Variables and DataObjects cross with the successor; Jobs is what ends with the
	// predecessor — the in-flight work a fork cannot carry, stated before it is lost.
	Variables   int `json:"variables"`
	DataObjects int `json:"dataObjects"`
	Jobs        int `json:"jobs"`

	Problems []engine.MigrationProblem `json:"problems,omitempty"`
	Forkable bool                      `json:"forkable"`
	// SuccessorInstanceKey is filled only by a fork that went through: the instance the
	// work continues in, so the caller can follow it without guessing which instance is
	// new.
	SuccessorInstanceKey uint64 `json:"successorInstanceKey,omitempty"`
}

// forkCandidates is every element of a version an execution could legitimately resume
// at: the ones ValidateFork accepts, in document order. It is a small O(nodes) walk on
// an operator's request, never on the token path.
func forkCandidates(from, to *compiler.CompiledProcess) []string {
	var out []string
	for i := int32(0); int(i) < to.NodeCount(); i++ {
		id := to.ElementBpmnId(i)
		if id == "" {
			continue
		}
		if len(engine.ValidateFork(from, to, 0, []int32{i})) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// forkParkedTokens is where an instance's tokens are, by element id in the version it is
// running, deduplicated and ordered.
//
// A boundary event and an event subprocess's armed trigger are element instances too,
// and they are deliberately left out: neither is work an operator would resume at — each
// is armed *by* something else — so listing them would tell somebody their instance is
// parked in places it is not.
func (s *Server) forkParkedTokens(piKey uint64, src *compiler.CompiledProcess) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	err := s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
		ei, ok, err := s.store.GetElementInstance(elKey)
		if err != nil || !ok {
			return err
		}
		if ei.AttachedToKey != 0 || ei.BpmnElementType == uint8(compiler.TypeEventSubProcessStart) {
			return nil
		}
		id := src.ElementBpmnId(ei.ElementId)
		if id == "" {
			return nil
		}
		if _, dup := seen[id]; dup {
			return nil
		}
		seen[id] = struct{}{}
		out = append(out, id)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// planFork answers what forking one instance onto a target version would do: where the
// successor would start, what crosses with it, what ends with the predecessor, and every
// reason it would be refused.
//
// Reads the deployment map and the store, so callers must be on the run-loop goroutine
// (invariant I3). Returns ok=false when the instance is not a running one. resume names
// elements by BPMN id in the *target* version; empty means "propose them", which is what
// the dialog opens with.
func (s *Server) planFork(piKey, targetDefKey uint64, resume []string) (forkPlanResp, bool, error) {
	pi, found, err := s.store.ProcessInstance(piKey)
	if err != nil {
		return forkPlanResp{}, false, err
	}
	if !found || pi.State != model.PIActive {
		return forkPlanResp{}, false, nil
	}
	plan := forkPlanResp{
		InstanceKey:       piKey,
		FromProcessDefKey: pi.ProcessDefKey,
		ToProcessDefKey:   targetDefKey,
		Resume:            []string{},
	}
	src, srcOK := s.deployments[pi.ProcessDefKey]
	dst, dstOK := s.deployments[targetDefKey]
	if !srcOK || src.cp == nil {
		plan.Problems = []engine.MigrationProblem{{Reason: "the version this instance is running is no longer deployed, so its tokens cannot be read"}}
		return plan, true, nil
	}
	plan.FromVersion = src.Version
	if !dstOK || dst.cp == nil {
		plan.Problems = []engine.MigrationProblem{{Reason: "no deployed definition with that key"}}
		return plan, true, nil
	}
	plan.ToVersion = dst.Version

	parked, err := s.forkParkedTokens(piKey, src.cp)
	if err != nil {
		return forkPlanResp{}, false, err
	}
	byIDTo := map[string]int32{}
	for i := int32(0); int(i) < dst.cp.NodeCount(); i++ {
		if id := dst.cp.ElementBpmnId(i); id != "" {
			byIDTo[id] = i
		}
	}
	// The proposal: each parked token's own element id, where the target version has one
	// that work can resume at. The id is the only element identity a human controls and
	// the only one stable across an ordinary edit, which is the same reason the
	// in-place mapping pairs by it (ADR-0162).
	var proposed []string
	for _, id := range parked {
		row := forkParked{ElementID: id}
		if idx, ok := byIDTo[id]; ok && len(engine.ValidateFork(src.cp, dst.cp, pi.ParentElementInstanceKey, []int32{idx})) == 0 {
			row.ResumeAt = id
			proposed = append(proposed, id)
		}
		plan.Parked = append(plan.Parked, row)
	}
	plan.Candidates = forkCandidates(src.cp, dst.cp)

	chosen := resume
	if len(chosen) == 0 {
		chosen = proposed
	}
	var idx []int32
	for _, id := range chosen {
		name := strings.TrimSpace(id)
		i, ok := byIDTo[name]
		if !ok {
			// A resume point nobody can find is refused rather than dropped: an ignored
			// one is an operator believing the work will pick up somewhere it will not.
			plan.Problems = append(plan.Problems, engine.MigrationProblem{
				Reason: fmt.Sprintf("the target version has no element %q", id)})
			continue
		}
		plan.Resume = append(plan.Resume, name)
		idx = append(idx, i)
	}
	plan.Problems = append(plan.Problems,
		engine.ValidateFork(src.cp, dst.cp, pi.ParentElementInstanceKey, idx)...)

	// What crosses, and what does not — counted here so the dialog can say it before the
	// operator confirms rather than after the predecessor is gone.
	if err := s.store.VariablesOfScope(piKey, func(*model.VariableValue) error {
		plan.Variables++
		return nil
	}); err != nil {
		return forkPlanResp{}, false, err
	}
	declared := map[string]struct{}{}
	for _, d := range dst.cp.DataObjects() {
		declared[dst.cp.Intern(d.Name)] = struct{}{}
	}
	if err := s.store.DataObjectsOfScope(piKey, func(v *model.DataObjectValue) error {
		// Only an object the target version still declares crosses: one it dropped has
		// nowhere to live in the successor, and counting it would promise otherwise.
		if _, ok := declared[v.Name]; ok {
			plan.DataObjects++
		}
		return nil
	}); err != nil {
		return forkPlanResp{}, false, err
	}
	if err := s.store.ElementInstancesOfProcess(piKey, func(elKey uint64) error {
		if _, ok, err := s.store.JobOfElement(elKey); err != nil {
			return err
		} else if ok {
			plan.Jobs++
		}
		return nil
	}); err != nil {
		return forkPlanResp{}, false, err
	}

	plan.Forkable = len(plan.Problems) == 0
	return plan, true, nil
}

// handleForkInstance ends a running instance and continues its work in a new instance of
// another version of its process, at the resume points the request names
// (ADR-draft-forked-instance-migration).
//
// It is the operator's next step after [Server.handleMigrateInstance] has refused, and
// it is a different operation rather than a fallback: in-flight jobs end with the
// predecessor, so it is never something the server decides on its own. Like the
// migration it plans first and refuses with the plan itself — 409 carrying every problem
// — so a rejection is as informative as a dry run.
func (s *Server) handleForkInstance(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid instance key")
		return
	}
	req, ok := s.readMigrationRequest(w, r)
	if !ok {
		return
	}
	// A fork ends a running instance. That is not something to do unexplained — the same
	// gate ADR-0159 put on a manual completion, and for a heavier act.
	if strings.TrimSpace(req.Reason) == "" {
		httpapi.Error(w, http.StatusBadRequest, "reason is required: a fork ends the instance it continues and is recorded as an operator action")
		return
	}
	actor := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		actor = p.Username
	}

	var (
		plan    forkPlanResp
		found   bool
		planErr error
		runErr  error
	)
	s.do(func() {
		plan, found, planErr = s.planFork(key, req.TargetProcessDefKey, req.Resume)
		if planErr != nil || !found || !plan.Forkable {
			return
		}
		dst := s.deployments[req.TargetProcessDefKey]
		resume := make([]int32, 0, len(plan.Resume))
		byIDTo := map[string]int32{}
		for i := int32(0); int(i) < dst.cp.NodeCount(); i++ {
			byIDTo[dst.cp.ElementBpmnId(i)] = i
		}
		for _, id := range plan.Resume {
			resume = append(resume, byIDTo[id])
		}
		s.proc.ForkInstance(key, req.TargetProcessDefKey, resume, actor, strings.TrimSpace(req.Reason))
		if runErr = s.jobRunner.Drive(); runErr != nil {
			return
		}
		// The successor's key is minted by the handler, not by this request, so it is
		// read back from the predecessor's own record — which now names it, durably,
		// because Drive returned only after the batch was committed (invariant I2).
		//
		// Its absence is the answer to the one case the plan cannot rule out: the
		// instance was free to move between the plan above and the command's turn on the
		// loop, and the handler re-validates and drops a fork that no longer holds.
		// Reporting that as a success would tell an operator their work is running
		// somewhere it is not.
		pi, found, err := s.store.ProcessInstance(key)
		switch {
		case err != nil:
			runErr = err
		case !found || pi.SuccessorInstanceKey == 0:
			plan.Forkable = false
			plan.Problems = append(plan.Problems, engine.MigrationProblem{
				Reason: "the instance moved while this request was in flight, so the fork no longer held and nothing was written — read the plan again"})
		default:
			plan.SuccessorInstanceKey = pi.SuccessorInstanceKey
		}
	})
	switch {
	case planErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "plan fork: "+planErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no running instance with that key")
	case !plan.Forkable:
		httpapi.JSON(w, http.StatusConflict, plan)
	case runErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "fork: "+runErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, plan)
	}
}
