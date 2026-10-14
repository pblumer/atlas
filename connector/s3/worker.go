package s3

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The worker uses
// it to find the Worker name, operation and authored values an S3 job belongs to, so one
// handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs an S3 task. Register it with a [job.Runner]
// under the reserved [compiler.S3JobTypeIndex] via HandleWithOutput; the runner then pulls
// activatable S3 jobs, and for each the handler resolves the task's Worker and authored
// values from the compiled process — evaluating any FEEL value over the variables the task
// sees, up its scope chain (the fx toggle, ADR-0067/0068) — resolves the named Worker's
// client from reg, performs the one operation, and (when the task names a result variable
// and the store returned something) returns what it answered as that variable to be
// written back on completion.
//
// Returning an error leaves the job pending (retry, then an incident, ADR-0061); the
// runner completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil // element instance gone (e.g. already completed); nothing to do
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return nil, fmt.Errorf("s3: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("s3: %w", err)
		}
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
		if err != nil {
			return nil, err
		}
		result, err := Run(context.Background(), task, reg)
		if err != nil {
			return nil, err
		}
		if task.ResultVariable == "" || result == nil {
			// Either the model discards the answer, or the operation is the one that
			// answers with nothing (a delete). Writing a null variable for the second
			// would make "deleted" indistinguishable from "read something that was empty".
			return nil, nil
		}
		return []model.VariableValue{resultVariable(task.ResultVariable, result)}, nil
	}
}
