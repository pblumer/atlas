package api

import (
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/state"
)

// taskEvent is the type, subject and data of a turn in a user task's life on the feed
// (ADR-draft-tasks-for-a-connected-system §1): the keys that find the task, in the
// names GET /api/v1/tasks uses, the element and process it belongs to, the groups it is
// offered to and when it is due — what a copy of the open tasks elsewhere needs to open,
// route and close a card of its own.
//
// Never who holds it. A model writes an assignee as a username or an expression's
// value, not a principal id, and the feed names people by id only (ADR-0314):
// `assigned` says whether somebody does, and a receiver that needs who asks
// GET /api/v1/tasks/{key} with a credential that may see the task. Nor the form, the
// variables or the content: those are the person's, and read as the person.
func taskEvent(e state.FeedEntry, defs defIndex) (typ, subject string, data map[string]any) {
	job := e.Task
	switch e.Kind {
	case state.FeedTaskAssigned:
		typ = eventcatalog.TaskAssigned
	case state.FeedTaskUnassigned:
		typ = eventcatalog.TaskUnassigned
	case state.FeedTaskCompleted:
		typ = eventcatalog.TaskCompleted
	case state.FeedTaskCancelled:
		typ = eventcatalog.TaskCancelled
	default:
		typ = eventcatalog.TaskCreated
	}
	data = map[string]any{
		"taskKey":            e.TaskKey,
		"processInstanceKey": job.ProcessInstanceKey,
		"elementInstanceKey": job.ElementInstanceKey,
		"elementIndex":       e.TaskElement,
		"assigned":           job.Assignee != "",
	}
	if job.CreatedAt != 0 {
		data["createdAt"] = feedTime(job.CreatedAt)
	}
	if job.Deadline != 0 {
		data["dueDate"] = feedTime(job.Deadline)
	}
	groups := job.CandidateGroups
	// The definition was read when the row was folded; what it and its task are called
	// is read now, from what is deployed, and left out for a definition that has gone.
	if e.Definition != 0 {
		data["processDefKey"] = e.Definition
		if d, ok := defs[e.Definition]; ok {
			data["processId"] = d.ProcessID
			data["version"] = d.Version
			// The element is the definition's own, so it is in range; checked all the
			// same, because a read of the feed must not panic the reader.
			if d.cp != nil && e.TaskElement >= 0 && int(e.TaskElement) < d.cp.NodeCount() {
				if id := d.cp.ElementBpmnId(e.TaskElement); id != "" {
					data["elementId"] = id
				}
				if n := d.cp.Node(e.TaskElement); n.Type == compiler.TypeUserTask {
					detail := d.cp.UserTask(n.Detail)
					if name := d.cp.Intern(detail.Name); name != "" {
						data["name"] = name
					}
					// A job written before it carried its groups reads the model's, as
					// the task list does (enrichTaskWith).
					if groups == "" {
						groups = d.cp.Intern(detail.CandidateGroups)
					}
				}
			}
		}
	}
	if groups != "" {
		data["candidateGroups"] = groups
	}
	return typ, "tasks/" + strconv.FormatUint(e.TaskKey, 10), data
}
