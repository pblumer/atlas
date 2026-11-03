package api

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/state"
)

// The task views narrowed to a reach of projects, and a task that ended told from one
// out of reach (ADR-0512 §2).
//
// A credential that states a reach — a `tasklist` token minted with one, or the
// `tasklist` capability of a connected system's grant — sees the tasks of processes
// deployed from those projects and no others: the list skips the rest, and one task by
// key answers 404 outside them, as it does for any task the caller may not see. A task
// of a process deployed into no project is outside every reach.
//
// A connected system that keeps copies of tasks has one more question: whether a task
// it no longer finds ended or left its view. So for a system grant one task by key that
// ended — completed or cancelled — while the feed still holds its end (state.FeedTaskEnd)
// answers 410 Gone with how and when, under the same reach and visibility a read of it
// while open had. A task whose end the feed's retention has dropped, or that ended
// before that index existed, is not known as ended and answers 404: the system then
// detaches its copy, which is what the contract with it asks for.

// taskReachOf is the projects the request's credential narrows the task views to, or
// nil when it narrows nothing.
func taskReachOf(r *http.Request) []string {
	if p := httpapi.PrincipalFrom(r.Context()); p != nil && len(p.Reach) > 0 {
		return p.Reach
	}
	return nil
}

// taskProjectFilter is the test a task's definition must pass to be in the request's
// reach, or nil when the credential states none. The definitions' projects are read
// in one loop turn and kept in the closure, so the test runs off the loop too.
func (s *Server) taskProjectFilter(r *http.Request) func(defKey uint64) bool {
	reach := taskReachOf(r)
	if reach == nil {
		return nil
	}
	projectOf := map[uint64]string{}
	s.do(func() {
		for key, d := range s.deployments {
			projectOf[key] = d.ProjectID
		}
	})
	return func(defKey uint64) bool {
		p := projectOf[defKey]
		return p != "" && slices.Contains(reach, p)
	}
}

// taskInReach applies a filter that may be nil, which keeps everything.
func taskInReach(inReach func(uint64) bool, tr taskResp) bool {
	return inReach == nil || inReach(tr.ProcessDefKey)
}

// inProjectsOnLoop is taskProjectFilter for a caller already on the loop.
func (s *Server) inProjectsOnLoop(reach []string, defKey uint64) bool {
	if reach == nil {
		return true
	}
	d, ok := s.deployments[defKey]
	return ok && d.ProjectID != "" && slices.Contains(reach, d.ProjectID)
}

// taskEnded is the body of a 410 for a task that ended.
type taskEnded struct {
	Key     string `json:"key"`
	Ended   string `json:"ended"` // "completed" or "cancelled"
	EndedAt string `json:"endedAt"`
}

// endedTaskFor is how the task with this key ended, if the feed still holds its end
// and the viewer, with this reach, could have read it while it was open. It runs on the
// loop: one point read and the deployments map.
func (s *Server) endedTaskFor(viewer taskfolder.User, reach []string, key uint64) (*taskEnded, error) {
	e, ok, err := s.store.FeedTaskEnd(s.proc.Partition(), key)
	if err != nil || !ok {
		return nil, err
	}
	// Visible as it was when it ended: who it was addressed to, and in which reach.
	was := taskResp{Key: key, ProcessDefKey: e.Definition, Assignee: e.Task.Assignee, CandidateGroups: e.Task.CandidateGroups}
	if !s.taskVisibleTo(viewer, was) || !s.inProjectsOnLoop(reach, e.Definition) {
		return nil, nil
	}
	how := "completed"
	if e.Kind == state.FeedTaskCancelled {
		how = "cancelled"
	}
	return &taskEnded{
		Key:     strconv.FormatUint(key, 10),
		Ended:   how,
		EndedAt: time.Unix(0, e.At).UTC().Format(time.RFC3339),
	}, nil
}
