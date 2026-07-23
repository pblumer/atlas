package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// TestListTasksSkipsOrphanedIndexEntry covers handleListTasks' robustness path:
// an activatable-index entry whose backing job record is gone (a torn write, or
// a record removed out from under a lingering index key) is skipped rather than
// listed or errored. The scenario is seeded directly on the run-loop goroutine
// through the store's transaction API (respecting the single-writer invariant):
// write a user-task job, then delete only its record — passing a mismatched job
// type to DeleteJob so its activatable-index key survives.
func TestListTasksSkipsOrphanedIndexEntry(t *testing.T) {
	srv := newServerForErrors(t)

	const orphanKey uint64 = 4242
	srv.do(func() {
		tx := srv.store.NewTransaction()
		if err := tx.PutJob(orphanKey, &model.JobValue{JobType: compiler.UserTaskJobTypeIndex}); err != nil {
			t.Fatalf("put job: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit put: %v", err)
		}
		// Delete with a job type that does NOT match the one indexed above, so the
		// record is removed but the user-task activatable-index key is left behind.
		tx = srv.store.NewTransaction()
		if err := tx.DeleteJob(orphanKey, &model.JobValue{JobType: compiler.UserTaskJobTypeIndex + 999}); err != nil {
			t.Fatalf("delete job record: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit delete: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list tasks status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var tasks []taskResp
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, rec.Body.String())
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (the orphaned index entry must be skipped)", len(tasks))
	}
}
