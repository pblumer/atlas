package api

import (
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

// The server half of the Tasks app's folders (ADR-draft-task-folders-are-saved-filters).
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
const maxFolderScan = 20000

// taskDefLookup resolves a deployment key to what enriching a task needs. It
// exists so the same enrichment serves the loop-owned registry and an off-loop
// snapshot's copy of it, rather than the two growing separate copies of the walk.
type taskDefLookup func(defKey uint64) (processID, processName string, cp *compiler.CompiledProcess, ok bool)

// elementReader is the one read enriching a task makes beyond the job itself.
// Both *state.Store and *state.ReadView satisfy it.
type elementReader interface {
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
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
			if !visit(jobKey, tr, ft) {
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
func (s *Server) countTaskFolders(matchers []*taskfolder.Matcher, u taskfolder.User) ([]int, int, bool, error) {
	needInstance := false
	for _, m := range matchers {
		if m.NeedsInstance() {
			needInstance = true
			break
		}
	}
	counts := make([]int, len(matchers))
	total := 0
	// One instant for the whole scan, so "overdue" cannot mean two different
	// moments within a single answer.
	now := time.Now()
	budgetHit, err := s.visitOpenTasks(0, needInstance, func(_ uint64, _ taskResp, ft taskfolder.Task) bool {
		total++
		for i, m := range matchers {
			if m.Match(ft, u, now) {
				counts[i]++
			}
		}
		return true
	})
	if err != nil {
		return nil, 0, false, err
	}
	return counts, total, budgetHit, nil
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
	tasks := []taskResp{}
	var nextCursor uint64
	now := time.Now()
	full := false
	budgetHit, scanErr := s.visitOpenTasks(before, matcher.NeedsInstance(), func(jobKey uint64, tr taskResp, ft taskfolder.Task) bool {
		if !matcher.Match(ft, viewer, now) {
			// A skipped task still advances the cursor: the next page must resume
			// after everything this one looked at, not after the last row it kept.
			nextCursor = jobKey
			return true
		}
		if len(tasks) >= limit {
			full = true
			return false
		}
		tasks = append(tasks, tr)
		nextCursor = jobKey
		return true
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list tasks: "+scanErr.Error())
		return
	}
	if full || budgetHit {
		w.Header().Set("X-Tasks-Truncated", "true")
		w.Header().Set("X-Tasks-Next-Cursor", strconv.FormatUint(nextCursor, 10))
	}
	httpapi.JSON(w, http.StatusOK, tasks)
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
