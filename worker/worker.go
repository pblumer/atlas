// Package worker is Atlas's out-of-process job worker: the built-in Worker Types,
// served by a process that runs as a client of atlas's HTTP API, leasing jobs of named
// types and reporting what happened (ADR-0157 step 5, over the protocol of ADR-0007).
//
// It exists so that a service task's work does not have to run on the engine's
// single writer. An in-process worker's outbound call is charged to the whole
// engine for its duration; a job this worker takes is charged to the worker.
//
// The protocol client itself — leasing, completing, failing, throwing, answering
// queries, asking before acting — lives in package workersdk, which imports nothing of
// the engine so that a Worker Type built outside this repository can depend on it
// (ADR-0535). This package holds what is
// specific to the Worker Types atlas ships ([BuiltinConnectors] and the handlers behind
// it) and keeps the client's names as aliases, so code written against worker.New,
// worker.Options or worker.BPMNError is unchanged.
package worker

import (
	"context"

	"github.com/pblumer/atlas/workersdk"
)

// The protocol client's types, under the names this package has always given them.
// Each is the workersdk type itself, not a copy: a value of one is a value of the other.
type (
	// Job is one leased job as the engine hands it over.
	Job = workersdk.Job
	// ConnectorPayload is a resolved task: what to do, with no engine concepts in it.
	ConnectorPayload = workersdk.ConnectorPayload
	// Exec does one job's work.
	Exec = workersdk.Exec
	// ExecFunc adapts a function to [Exec].
	ExecFunc = workersdk.ExecFunc
	// CompletingExec is work that completes with more than variables.
	CompletingExec = workersdk.CompletingExec
	// CompletingExecFunc adapts a function to [CompletingExec].
	CompletingExecFunc = workersdk.CompletingExecFunc
	// Outcome is everything a job's work produced.
	Outcome = workersdk.Outcome
	// DecisionReport is a worker's account of a decision it evaluated.
	DecisionReport = workersdk.DecisionReport
	// ToolCallReport is a worker's account of one tool an agent chose.
	ToolCallReport = workersdk.ToolCallReport
	// BPMNError ends a job in a BPMN error instead of a result (ADR-0089).
	BPMNError = workersdk.BPMNError
	// Options configures a [Worker].
	Options = workersdk.Options
	// Worker leases jobs of the types it handles and reports what happened.
	Worker = workersdk.Worker
	// CmdExec runs an operating-system command per job.
	CmdExec = workersdk.CmdExec
	// QueryFunc answers one form lookup (ADR-0473).
	QueryFunc = workersdk.QueryFunc
	// Authorization is the server's answer to a handler asking before it acts (ADR-0475).
	Authorization = workersdk.Authorization
)

// The protocol client's defaults.
const (
	DefaultLease      = workersdk.DefaultLease
	DefaultWait       = workersdk.DefaultWait
	DefaultMaxJobs    = workersdk.DefaultMaxJobs
	DefaultRetry      = workersdk.DefaultRetry
	DefaultMaxQueries = workersdk.DefaultMaxQueries
)

// New builds a worker, filling in defaults. See [workersdk.New].
func New(opts Options) *Worker { return workersdk.New(opts) }

// Authorize asks the server whether the job in ctx may act. See [workersdk.Authorize].
func Authorize(ctx context.Context) (*Authorization, error) { return workersdk.Authorize(ctx) }

// Refused is the error a handler returns for a refused action. See [workersdk.Refused].
func Refused() error { return workersdk.Refused() }
