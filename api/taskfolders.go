package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The server half of the Tasks app's folders (ADR-0268).
// The taskfolder service owns what a folder *is* — the rule, the generated FEEL,
// the store; this file is the part that only the server can do: fill the editor's
// value lists from the deployments and the directory, and walk the open user tasks
// a rule is evaluated against.
//
// Every scan here runs through [Server.readOffLoop]. That is not a preference: a
// folder's badge is a question about the whole open-task population, and a walk of
// it inside s.do would hold the engine's single writer for its duration (ADR-0239,
// and the note in AGENTS.md that a query which grows with the population does not
// belong on the loop).

// maxFolderScan bounds one folder scan. A count is worth a bounded walk, not an
// unbounded one: past this many open tasks the scan stops and says so, and the
// number it reports is a floor rather than a total — which the sidebar shows as
// such instead of a confident wrong figure. It sits well above the list page cap
// (500) because counting a task is far cheaper than enriching and shipping one,
// and this is the number a person actually reads.
// It is a var rather than a const for one reason: the behaviour at the bound —
// the count coming back as a floor and the sidebar saying so — is the half of this
// that a reader has to trust, and a bound only reachable by parking twenty
// thousand tasks in a test is a bound nothing checks.
var maxFolderScan = 20000

// taskDefLookup resolves a deployment key to what enriching a task needs. It
// exists so the same enrichment serves the loop-owned registry and an off-loop
// snapshot's copy of it, rather than the two growing separate copies of the walk.
type taskDefLookup func(defKey uint64) (processID, processName string, cp *compiler.CompiledProcess, ok bool)

// elementReader is what enriching a task reads beyond the job itself.
// Both *state.Store and *state.ReadView satisfy it.
type elementReader interface {
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
	// ElementReplayHistory answers when a job written before it carried its own
	// creation stamp was opened (see activatedAt).
	ElementReplayHistory(piKey uint64, fn func(ts int64, pos uint64, v state.ElementReplayValue) error) error
	// VisibleVariablesOfScope is read only when a caller asks for a task's content.
	VisibleVariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
}

// wantsTaskContent reports whether a task listing was asked to carry each task's
// content (?content=1). Off by default: the rows every other caller reads stay the
// size and the cost they were.
func wantsTaskContent(r *http.Request) bool {
	v := strings.TrimSpace(r.URL.Query().Get("content"))
	return v == "1" || v == "true"
}

// errFound stops a scan that has what it came for.
var errFound = errors.New("found")

// activatedAt is when the element a job waits on was activated, in unix nanos, or 0
// when the instance's history does not say. It is the fallback for a job written
// before the job recorded its own creation time: a user task's job is created in the
// same batch its element activates, so the two instants are the same moment. The
// scan is over one instance's token history and stops at the match, and it is only
// taken for those older jobs, so it fades out as they are completed.
func activatedAt(r elementReader, jv *model.JobValue) int64 {
	var at int64
	err := r.ElementReplayHistory(jv.ProcessInstanceKey, func(ts int64, _ uint64, v state.ElementReplayValue) error {
		if v.ElementInstanceKey == jv.ElementInstanceKey && v.Action == state.ReplayActivated {
			at = ts
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return 0
	}
	return at
}

// The bounds on what taskContent returns per task. A task's content is for finding
// it, not for reading it — the form does that — so a long value is cut and a scope
// with many variables contributes its first few.
const (
	maxTaskContentValues = 40
	maxTaskContentLen    = 200
)

// taskContent is the short text and number values visible at a task's scope: the
// task's own variables and those of every enclosing scope up to the instance, the
// same set its form is filled from. The inbox's filter matches against them, so a
// person can find "every task about this recipient" by the recipient rather than by
// a task name that is the same on every row.
//
// It returns what the viewer's form would already show them for the tasks they may
// see, so it widens what a list row says, not who may read it.
func taskContent(r elementReader, tr taskResp) []string {
	scope := tr.ElementInstanceKey
	if scope == 0 {
		scope = tr.ProcessInstanceKey
	}
	var out []string
	err := r.VisibleVariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Kind != model.VarString && v.Kind != model.VarNumber {
			return nil
		}
		text := strings.TrimSpace(v.Text)
		if text == "" {
			return nil
		}
		if len(text) > maxTaskContentLen {
			text = strings.ToValidUTF8(text[:maxTaskContentLen], "")
		}
		out = append(out, text)
		if len(out) >= maxTaskContentValues {
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return nil
	}
	return out
}

// deploymentMeta reads the loop-owned deployment registry. Only call it on the
// loop; an off-loop scan uses the defIndex copy readOffLoop hands it.
func (s *Server) deploymentMeta(defKey uint64) (string, string, *compiler.CompiledProcess, bool) {
	d, ok := s.deployments[defKey]
	if !ok {
		return "", "", nil, false
	}
	return d.ProcessID, d.Name, d.cp, true
}

// defsMeta is the same lookup over an off-loop snapshot of the registry.
func defsMeta(defs defIndex) taskDefLookup {
	return func(defKey uint64) (string, string, *compiler.CompiledProcess, bool) {
		d, ok := defs[defKey]
		if !ok {
			return "", "", nil, false
		}
		return d.ProcessID, d.Name, d.cp, true
	}
}

// folderTask projects an enriched task row into the evaluation context a rule
// sees. Keeping the projection in one place is what makes the filter, the badge
// count and the editor's preview answer the same question.
func folderTask(tr taskResp, processName string) taskfolder.Task {
	return taskfolder.Task{
		ProcessID: tr.ProcessID, ProcessName: processName,
		TaskName: taskTitleOf(tr), ElementID: tr.ElementID,
		Assignee: tr.Assignee, CandidateGroups: tr.CandidateGroups,
		Lane: tr.Lane, LanePath: tr.LanePath,
		Priority: taskPriorityOf(tr), DueDate: tr.DueDate,
		HasForm: tr.FormID != "",
	}
}

// taskTitleOf is the name a rule matches on, and the one the inbox shows: the
// element's name, falling back to its BPMN id for a task authored without one.
func taskTitleOf(tr taskResp) string {
	if tr.Name != "" {
		return tr.Name
	}
	return tr.ElementID
}

// taskPriorityOf defaults an absent priority to the model default of 50, so a
// "priority is at least 50" folder holds the tasks a person would expect it to
// rather than only those whose model states the number explicitly.
func taskPriorityOf(tr taskResp) int32 {
	if tr.Priority > 0 {
		return tr.Priority
	}
	return 50
}

// visitOpenTasks walks open user tasks newest-first, off the run loop, calling
// visit for each until it returns false or the scan budget runs out. before is
// the pagination cursor (0 to start at the newest).
//
// needInstance asks for the process instance's start time, which costs one more
// store read per task — so it is taken only when a rule actually reads it
// ([taskfolder.Matcher.NeedsInstance]).
func (s *Server) visitOpenTasks(before uint64, needInstance bool,
	visit func(jobKey uint64, tr taskResp, ft taskfolder.Task) bool) (budgetHit bool, err error) {
	return s.visitOpenTasksIn(before, needInstance, func(_ *state.ReadView, jobKey uint64, tr taskResp, ft taskfolder.Task) bool {
		return visit(jobKey, tr, ft)
	})
}

// visitOpenTasksIn is [Server.visitOpenTasks] with the snapshot the walk reads, for a
// visitor that reads more about the tasks it keeps (a page asking for their content)
// and should not pay for that on the tasks it skips.
func (s *Server) visitOpenTasksIn(before uint64, needInstance bool,
	visit func(rv *state.ReadView, jobKey uint64, tr taskResp, ft taskfolder.Task) bool) (budgetHit bool, err error) {
	scanned := 0
	err = s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		def := defsMeta(defs)
		return unlessTruncated(rv.ActivatableJobsDesc(compiler.UserTaskJobTypeIndex, before, func(jobKey uint64) error {
			if scanned >= maxFolderScan {
				budgetHit = true
				return errListTruncated
			}
			jv, ok, jerr := rv.GetJob(jobKey)
			if jerr != nil || !ok {
				return jerr
			}
			scanned++
			tr := enrichTaskWith(rv, def, jobKey, jv)
			_, processName, _, _ := def(tr.ProcessDefKey)
			ft := folderTask(tr, processName)
			if needInstance {
				if pi, found, perr := rv.ActiveProcessInstance(tr.ProcessInstanceKey); perr == nil && found {
					ft.InstanceCreatedAt = pi.CreatedAt / int64(time.Millisecond)
				}
			}
			if !visit(rv, jobKey, tr, ft) {
				return errListTruncated
			}
			return nil
		}))
	})
	return budgetHit, err
}

// countTaskFolders answers every visible folder's badge from one walk of the open
// tasks. It is the [taskfolder.CountFunc] the service is built with — the service
// knows how to decide membership, the server knows where the tasks are.
//
// The fixed inbox folders are counted on the same walk. They used to be counted in
// the console instead, off the rows of the newest-first page it had already loaded,
// so each badge was the size of that page rather than of the inbox: a task assigned
// to somebody and sitting outside the newest 500 left their "Assigned to me" reading
// 0 (ADR-0365). One walk, one set of
// numbers, one truncation flag over all of them.
func (s *Server) countTaskFolders(matchers []*taskfolder.Matcher, u taskfolder.User) (taskfolder.Tally, error) {
	needInstance := false
	for _, m := range matchers {
		if m.NeedsInstance() {
			needInstance = true
			break
		}
	}
	tally := taskfolder.Tally{
		PerMatcher: make([]int, len(matchers)),
		Builtin:    make(map[string]int, len(taskfolder.BuiltinFolders)),
	}
	// Every fixed folder is present in the map from the start, zero included: a badge
	// with no entry would be "not counted" and a badge with a zero is "counted, none",
	// and the console draws those two differently.
	for _, b := range taskfolder.BuiltinFolders {
		tally.Builtin[b.ID] = 0
	}
	// One instant for the whole scan, so "overdue" cannot mean two different
	// moments within a single answer.
	now := time.Now()
	v := s.veilForPrincipal(u.Principal)
	budgetHit, err := s.visitOpenTasks(0, needInstance, func(_ uint64, tr taskResp, ft taskfolder.Task) bool {
		// A badge counts what the viewer's list would show, not what exists: a count
		// that includes tasks the list withholds is the withheld inbox by another name.
		if !s.taskVisibleTo(u, v, tr) {
			return true
		}
		tally.Total++
		for i, m := range matchers {
			if m.Match(ft, u, now) {
				tally.PerMatcher[i]++
			}
		}
		for _, b := range taskfolder.BuiltinFolders {
			if b.Match(ft, u) {
				tally.Builtin[b.ID]++
			}
		}
		return true
	})
	if err != nil {
		return taskfolder.Tally{}, err
	}
	tally.Truncated = budgetHit
	return tally, nil
}

// listTasksForFolder writes the open user tasks one folder selects. It keeps the
// page cap, the truncation header and the cursor the unfiltered listing uses, so
// a folder pages exactly like "All tasks" does — the only difference is that the
// scan skips what the rule does not select.
func (s *Server) listTasksForFolder(w http.ResponseWriter, r *http.Request, folderID string, limit int, before uint64) {
	viewer := taskfolder.Viewer(r)
	_, matcher, ok, err := s.taskFolders.MatcherFor(folderID, viewer)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read folder: "+err.Error())
		return
	}
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no folder with that id")
		return
	}
	now := time.Now()
	v := s.veilFor(r)
	s.pageOpenTasks(w, limit, before, matcher.NeedsInstance(), wantsTaskContent(r), func(tr taskResp, ft taskfolder.Task) bool {
		return s.taskVisibleTo(viewer, v, tr) && matcher.Match(ft, viewer, now)
	})
}

// listVisibleTasks is the unfiltered listing for a viewer who does not see every
// task: the same page, cap and cursor, walked off the loop because it has to skip
// what [Server.taskVisibleTo] withholds, exactly as a folder skips what its rule
// does not select.
func (s *Server) listVisibleTasks(w http.ResponseWriter, viewer taskfolder.User, limit int, before uint64, content bool) {
	v := s.veilForPrincipal(viewer.Principal)
	s.pageOpenTasks(w, limit, before, false, content, func(tr taskResp, _ taskfolder.Task) bool {
		return s.taskVisibleTo(viewer, v, tr)
	})
}

// pageOpenTasks writes one newest-first page of the open user tasks keep selects.
// It keeps the page cap, the truncation flag and the cursor the unfiltered listing
// uses, so a filtered list pages exactly like "All tasks" does.
func (s *Server) pageOpenTasks(w http.ResponseWriter, limit int, before uint64, needInstance, content bool,
	keep func(tr taskResp, ft taskfolder.Task) bool) {
	tasks := []taskResp{}
	var nextCursor uint64
	full := false
	budgetHit, scanErr := s.visitOpenTasksIn(before, needInstance, func(rv *state.ReadView, jobKey uint64, tr taskResp, ft taskfolder.Task) bool {
		if !keep(tr, ft) {
			// A skipped task still advances the cursor: the next page must resume
			// after everything this one looked at, not after the last row it kept.
			nextCursor = jobKey
			return true
		}
		if len(tasks) >= limit {
			full = true
			return false
		}
		if content {
			tr.Content = taskContent(rv, tr)
		}
		tasks = append(tasks, tr)
		nextCursor = jobKey
		return true
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list tasks: "+scanErr.Error())
		return
	}
	// Same shape and same reasoning as the unfiltered listing
	// (ADR-0378). The exact
	// count for a folder is what GET /api/v1/task-folders/counts answers; here the
	// total is the page unless the page is everything.
	capped := full || budgetHit
	page := httpapi.PageOf(tasks, capped)
	if capped {
		page = page.WithCursor(strconv.FormatUint(nextCursor, 10))
	}
	httpapi.JSON(w, http.StatusOK, page)
}

// taskFolderOptions fills the editor's value listboxes from what is actually
// deployed and who actually exists — which is the point of the whole design: a
// person picks a process from a list, so a folder cannot be built around a
// process id that was mistyped and matches nothing.
//
// It reads the deployment registry and the user store, so it runs on the loop.
func (s *Server) taskFolderOptions(viewer taskfolder.User) taskfolder.Options {
	// One entry per process id, from its newest deployed version: an older version's
	// task names are not what somebody filtering today's work is looking for.
	latest := map[string]*deployment{}
	for _, d := range s.deployments {
		if cur, ok := latest[d.ProcessID]; !ok || d.Version > cur.Version {
			latest[d.ProcessID] = d
		}
	}
	var processes []taskfolder.Option
	names, groups, lanes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for id, d := range latest {
		processes = append(processes, taskfolder.Option{Value: id, Label: d.Name})
		cp := d.cp
		if cp == nil {
			continue
		}
		for i := 0; i < cp.NodeCount(); i++ {
			n := cp.Node(int32(i))
			for _, lane := range cp.LanePath(int32(i)) {
				if lane != "" {
					lanes[lane] = true
				}
			}
			if n.Type != compiler.TypeUserTask {
				continue
			}
			detail := cp.UserTask(n.Detail)
			if name := cp.Intern(detail.Name); name != "" {
				names[name] = true
			}
			if g := cp.Intern(detail.CandidateGroups); g != "" {
				groups[g] = true
			}
		}
	}
	sort.Slice(processes, func(i, j int) bool {
		if processes[i].Label != processes[j].Label {
			return processes[i].Label < processes[j].Label
		}
		return processes[i].Value < processes[j].Value
	})

	var users []taskfolder.Option
	if recs, err := s.users.LoadAll(); err == nil {
		for _, u := range recs {
			if u.Disabled {
				continue
			}
			users = append(users, taskfolder.Option{Value: u.Username, Label: u.DisplayName})
		}
		sort.Slice(users, func(i, j int) bool { return users[i].Value < users[j].Value })
	}

	return taskfolder.Options{
		Processes: processes,
		TaskNames: plainOptions(names),
		Users:     users,
		Groups:    plainOptions(groups),
		Lanes:     plainOptions(lanes),
		MyGroups:  s.viewerGroups(viewer),
	}
}

// viewerGroups names the identity groups the caller belongs to, so the editor can
// offer "share with my team" without a route that lets any signed-in account read
// the whole group roster — the caller's own membership is already in their session
// (ADR-0180), and this only puts a name to each id.
func (s *Server) viewerGroups(viewer taskfolder.User) []taskfolder.Option {
	if len(viewer.Groups) == 0 {
		return nil
	}
	out := make([]taskfolder.Option, 0, len(viewer.Groups))
	for _, id := range viewer.Groups {
		label := id
		if g, ok, err := s.groups.Get(id); err == nil && ok {
			label = g.Name
		}
		out = append(out, taskfolder.Option{Value: id, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// plainOptions turns a set of values into a sorted option list with no separate
// label — the value *is* what a person reads for a lane, a candidate group or a
// task name.
func plainOptions(set map[string]bool) []taskfolder.Option {
	out := make([]taskfolder.Option, 0, len(set))
	for v := range set {
		out = append(out, taskfolder.Option{Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// folderQuery reads the ?folder= selector off a task listing request.
func folderQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("folder"))
}

// SetMaxFolderScanForTest lowers the folder scan budget and returns a function
// that puts it back. It is exported for the api_test package, which drives the
// bound through the real routes rather than reaching into this one — the
// behaviour at the budget is what a person sees, so it is tested from outside.
func SetMaxFolderScanForTest(n int) func() {
	prev := maxFolderScan
	maxFolderScan = n
	return func() { maxFolderScan = prev }
}
