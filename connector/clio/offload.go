package clio

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A clio task resolved into plain values, and the function that runs one.
//
// The division follows ADR-0168: the engine owns the compiled process and the scope
// chain, so it resolves the connector name, the subject, the event type and — for a
// write — the event *body*, which is the variables the task sees or its input
// mappings and exists nowhere but in engine state. The worker owns the endpoint, the
// token and the call. ADR-0231's successor moves the execution;
// what a clio task means is decided here once, by both halves.

// Operations a clio task performs. They are the three reserved job types spelled as
// the payload's own word, so a worker dispatches on the resolved job rather than on
// a job-type index it would have to keep in step with the compiler.
const (
	OpWrite = "write"
	OpQuery = "query"
	OpRead  = "read"
)

// Job is a clio task with everything already evaluated. It is what travels with a
// leased job (ADR-0036/0168).
type Job struct {
	// Connector names the clio instance in the worker's registry — the endpoint and
	// token live there, never here.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	Subject   string `json:"subject,omitempty"`
	// EventType is the CloudEvents type a write appends under.
	EventType string `json:"eventType,omitempty"`
	// Query is a run_query predicate; when empty a query task reads get_state for
	// Subject with the optional ReduceSpec projection instead.
	Query      string `json:"query,omitempty"`
	ReduceSpec string `json:"reduceSpec,omitempty"`
	// Limit bounds a read; 0 means the connector's own default.
	Limit int32 `json:"limit,omitempty"`
	// Data is the event body a write appends: the task's input mappings, or every
	// variable it sees when it maps none (ADR-0174). It is resolved in the engine
	// because it *is* engine state — a worker has no scope chain to read it from.
	Data map[string]any `json:"data,omitempty"`
	// IdempotencyKey de-duplicates an at-least-once retry (ADR-0036). It is the job
	// key, frozen here so a retry of the same job writes the same event once.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// Result names the process variable a read or query writes into; empty discards
	// the result, and a write never has one.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what running a Job produces: a write has nothing to write back, a read
// or query has the value its result variable takes.
type Result struct {
	ResultVariable string
	Value          any
	// HasValue separates "the model discards this" from "the value is null": only a
	// read or query with a result variable writes anything at all.
	HasValue bool
}

// Variables renders a run's result as the process variables it completes with —
// none for a write, one for a read or query that named a result variable. Both
// halves call it, so the two cannot disagree about what a clio task returns.
func (r Result) Variables() []model.VariableValue {
	if !r.HasValue || r.ResultVariable == "" {
		return nil
	}
	return []model.VariableValue{resultVariable(r.ResultVariable, r.Value)}
}

// operationOf maps a compiled clio task's reserved job type to the operation word
// the payload carries. An unknown type is an error rather than a default: silently
// treating a read as a write would append to somebody's event store.
func operationOf(jobType int32) (string, error) {
	switch jobType {
	case compiler.ClioWriteJobTypeIndex:
		return OpWrite, nil
	case compiler.ClioQueryJobTypeIndex:
		return OpQuery, nil
	case compiler.ClioReadJobTypeIndex:
		return OpRead, nil
	default:
		return "", fmt.Errorf("clio: job type %d is not a clio operation", jobType)
	}
}

// Resolve turns a compiled clio task into a [Job] by reading everything the engine
// owns: the interned connector/subject/type, and for a write the event body built
// from the task's mappings or its visible variables.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("clio: connector task has no detail")
	}
	op, err := operationOf(detail.JobType)
	if err != nil {
		return Job{}, err
	}
	j := Job{
		Connector:  cp.Intern(detail.Connector),
		Operation:  op,
		Subject:    cp.Intern(detail.Subject),
		EventType:  cp.Intern(detail.EventType),
		Query:      cp.Intern(detail.ClioQuery),
		ReduceSpec: cp.Intern(detail.ReduceSpec),
		Limit:      detail.Limit,
		Result:     cp.Intern(detail.ResultVar),
	}
	if op == OpWrite {
		// The body is only meaningful for a write, and reading it costs a scope-chain
		// walk — so a read or query does not pay for one.
		data, err := eventBody(store, cp, ei, elementInstanceKey)
		if err != nil {
			return Job{}, fmt.Errorf("clio: read variables for element %d: %w", elementInstanceKey, err)
		}
		j.Data = data
		j.IdempotencyKey = idempotencyKey(jobKey)
		j.Result = "" // a write writes nothing back; a result variable on one is meaningless
	}
	return j, nil
}

// Run performs a resolved clio task against a client the caller resolved. The
// in-process path calls it too, so there is one definition of what a clio task does
// rather than two that drift.
func Run(ctx context.Context, j Job, client Client) (Result, error) {
	switch j.Operation {
	case OpWrite:
		err := client.WriteEvent(ctx, Event{
			Source:         DefaultEventSource,
			Subject:        j.Subject,
			Type:           j.EventType,
			Data:           j.Data,
			IdempotencyKey: j.IdempotencyKey,
		})
		return Result{}, err
	case OpQuery:
		if j.Query != "" {
			// run_query: filter the subject's events with a CEL predicate.
			rows, err := client.Query(ctx, j.Subject, j.Query)
			if err != nil {
				return Result{}, err
			}
			return Result{ResultVariable: j.Result, Value: rows, HasValue: true}, nil
		}
		// get_state: the subject's folded projection.
		projected, err := client.GetState(ctx, j.Subject, j.ReduceSpec)
		if err != nil {
			return Result{}, err
		}
		return Result{ResultVariable: j.Result, Value: projected, HasValue: true}, nil
	case OpRead:
		events, err := client.ReadEvents(ctx, ReadEventsRequest{Subject: j.Subject, Limit: int(j.Limit)})
		if err != nil {
			return Result{}, err
		}
		rows := make([]any, len(events))
		for i, e := range events {
			// Spelled out rather than marshalled from the struct, so the four keys a
			// model reads are a decision here and not a consequence of field tags.
			rows[i] = map[string]any{"id": e.ID, "type": e.Type, "subject": e.Subject, "data": e.Data}
		}
		return Result{ResultVariable: j.Result, Value: rows, HasValue: true}, nil
	default:
		return Result{}, fmt.Errorf("clio: unsupported operation %q", j.Operation)
	}
}
