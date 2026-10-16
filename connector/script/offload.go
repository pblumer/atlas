package script

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// A script task resolved into plain values, and the function that runs one.
//
// The fourth shape of ADR-0168's split, and the one where moving the work is the
// most obviously right. A script task needs an *interpreter on the machine* — pwsh,
// python3, node — and until now that meant installing all of them beside the engine,
// where a runaway script competes with the run loop for the same host. A worker puts
// each interpreter where it belongs, on a machine chosen for it, and a script that
// pegs a core pegs that one.
//
// The division is the usual one: the engine walks the scope chain and hands over the
// source and the variables the script may see; the worker has the interpreter and
// runs it. Which language is not part of the resolved job — it is the *job type*,
// and a worker subscribes per type, so a Python worker and a PowerShell worker are
// simply two workers.

// Job is a script task with everything already looked up: the source to run and the
// variables it sees. It is what travels with a leased job.
type Job struct {
	Source string `json:"source"`
	// Input is the scope chain flattened for the script, nearest scope winning
	// (ADR-0068) — resolved by the engine, which is the only one that can walk it.
	Input map[string]any `json:"input,omitempty"`
	// Result names the process variable the script's output is written to; empty
	// means the task writes nothing back.
	Result string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the
	// task's process catches. A script that fails with one of them threw a BPMN error;
	// any other failure is a failure (ADR-0461). They travel with
	// the job because a worker has no compiled process to ask.
	ErrorCodes []string `json:"errorCodes,omitempty"`
}

// Result is what running a Job produces.
type Result struct {
	ResultVariable string
	Output         any
}

// Resolve turns a compiled script task into a [Job]. Engine work by necessity: the
// source lives in the compiled process and the variables are reached by walking the
// element instance's scope chain in the store.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ScriptJobTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("script: task has no detail")
	}
	input, err := scopeVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("script: read variables for element %d: %w", elementInstanceKey, err)
	}
	return Job{
		Source:     cp.Intern(detail.Source),
		Input:      input,
		Result:     cp.Intern(detail.ResultVar),
		ErrorCodes: cp.CaughtErrorCodes(),
	}, nil
}

// Run executes a resolved job through the caller's own interpreter. The in-process
// path calls it too, so there is one definition of what running a script task means
// rather than two that drift — only the machine the interpreter sits on differs.
func Run(ctx context.Context, j Job, exec Exec) (Result, error) {
	out, err := exec.Run(ctx, j.Source, j.Input)
	if err != nil {
		// A code the process catches makes the failure an answer the model has a path
		// for. Any other code is only an identifier on an error, and the failure keeps
		// the retries a transient fault is owed.
		var coded *CodedError
		if errors.As(err, &coded) && slices.Contains(j.ErrorCodes, coded.Code) {
			return Result{}, &job.BPMNError{Code: coded.Code, Message: coded.Message}
		}
		return Result{}, fmt.Errorf("script: %w", err)
	}
	return Result{ResultVariable: j.Result, Output: out}, nil
}
