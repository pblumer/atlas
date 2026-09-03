package clio

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process. The
// worker uses it to find the worker, subject, and event type a clio job
// belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a clio "write-events" worker
// task. Register it with a [job.Runner] for the reserved ClioWriteJobType index;
// the runner then pulls activatable clio jobs, and for each the handler resolves
// the worker task's connector/subject/event-type from the compiled process,
// resolves the worker's client from reg, and appends an event whose body the
// task's input mappings define — or, with none, the variables it sees (see
// [eventBody]) — keyed by the job key so an at-least-once retry de-duplicates
// (ADR-0036). Returning an error leaves the job pending, exactly as for any
// worker; the runner completes it only on success.
func Handler(store state.Reader, lookup ProcessLookup, reg *Registry) job.Handler {
	return func(j job.Job) error {
		task, client, ok, err := resolveTask(store, lookup, reg, j)
		if err != nil || !ok {
			return err
		}
		_, err = Run(context.Background(), task, client)
		return err
	}
}

// QueryHandler builds a job handler for a clio "query" task: it reads
// projected state (get_state) or runs a stored query (run_query) on the task's
// worker and writes the result back into the task's result variable. Register it
// with a [job.Runner] for the reserved ClioQueryJobType index via HandleWithOutput,
// like the REST worker (ADR-0036/0067): when the task carries a query the handler
// runs it, otherwise it reads get_state for the task's subject (with the optional
// reduce spec). Returning an error leaves the job pending (retry, then an incident),
// exactly as for the write handler.
func QueryHandler(store state.Reader, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return runningHandler(store, lookup, reg)
}

// ReadHandler builds a job handler for a clio "read" task: it reads the
// task's subject events (up to the task's limit) from the worker and writes them
// back into the task's result variable as a JSON array. Register it for the reserved
// ClioReadJobType index via HandleWithOutput (ADR-0036).
func ReadHandler(store state.Reader, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return runningHandler(store, lookup, reg)
}

// runningHandler is the read/query half of every in-process clio handler: resolve the
// task, run it, and hand back the variables the result becomes. Query and read differ
// only in what [Run] does with the resolved job, so they share this rather than each
// spelling out a call the offloaded path would then have to match a second time.
func runningHandler(store state.Reader, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		task, client, ok, err := resolveTask(store, lookup, reg, j)
		if err != nil || !ok {
			return nil, err
		}
		res, err := Run(context.Background(), task, client)
		if err != nil {
			return nil, err
		}
		return res.Variables(), nil
	}
}

// resolveTask is the in-process path's whole engine half: the guards, the compiled
// detail, the client, and [Resolve] — the same resolution the API performs when it
// puts a clio job's payload on the wire, so an offloaded task and an in-engine one
// act on the identical values. ok is false with a nil error only when the element
// instance is already gone.
func resolveTask(store state.Reader, lookup ProcessLookup, reg *Registry, j job.Job) (Job, Client, bool, error) {
	cp, detail, client, ei, ok, err := resolveConnector(store, lookup, reg, j)
	if err != nil || !ok {
		return Job{}, nil, false, err
	}
	task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
	if err != nil {
		return Job{}, nil, false, err
	}
	return task, client, true, nil
}

// idempotencyKey is the de-duplication key a write carries: the job key, which is
// stable across the retries of one job and different for every other (ADR-0036).
func idempotencyKey(jobKey uint64) string {
	return strconv.FormatUint(jobKey, 10)
}

// resolveConnector performs the guards shared by every clio handler: it loads the
// job's element instance (a vanished one is a no-op), finds its compiled process and
// connector-task detail, and resolves the named worker's client. ok is false with
// a nil error only when the element instance is already gone; any other failure
// returns an error so the job stays pending.
func resolveConnector(store state.Reader, lookup ProcessLookup, reg *Registry, j job.Job) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail, Client, *model.ElementInstanceValue, bool, error) {
	ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	if !ok {
		return nil, nil, nil, nil, false, nil // element instance gone; nothing to do
	}
	cp := lookup(ei.ProcessDefKey)
	if cp == nil {
		return nil, nil, nil, nil, false, fmt.Errorf("clio: no compiled process for def %d", ei.ProcessDefKey)
	}
	detail, err := cp.ConnectorTaskOf(ei.ElementId)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("clio: %w", err)
	}
	name := cp.Intern(detail.Connector)
	client, ok := reg.Client(name)
	if !ok {
		return nil, nil, nil, nil, false, reg.Unresolved("clio", name)
	}
	return cp, detail, client, ei, true, nil
}

// resultVariable canonicalizes a clio read/query result into the process variable
// named by the task's result variable, through the same expr path REST uses so it
// round-trips on replay (a scalar stays a scalar, an object/array becomes a
// structured VarJSON).
func resultVariable(name string, value any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(value))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind (mirrors the REST
// worker's mapping so the two enums evolve independently).
func toVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}

// eventBody builds the JSON-ready body a write-events task appends.
//
// When the task carries zeebe:ioMapping inputs, those mappings *are* the payload:
// the body is exactly its activity-local scope, which at this point holds the mapped
// values and nothing else (a job's result is written there only on completion). That
// is the "payload mapping" ADR-0036 planned, expressed with the ADR-0068 mappings
// that arrived later (ADR-0174) — a model
// states what leaves it rather than spilling every process variable, including
// scratch and internal ones, into an external event store.
//
// With no input mappings the body stays every variable the task *sees*, resolved up
// its scope chain (nearest scope wins), so a task inside a subprocess also carries
// that subprocess's variables; for a top-level task the chain is just the process
// instance, exactly the previous behaviour (ADR-0035/0036).
func eventBody(store state.Reader, cp *compiler.CompiledProcess, ei *model.ElementInstanceValue, elementInstanceKey uint64) (map[string]any, error) {
	data := map[string]any{}
	collect := func(v *model.VariableValue) error {
		data[v.Name] = varToAny(v)
		return nil
	}
	var err error
	if len(cp.IOInputs(ei.ElementId)) > 0 {
		err = store.VariablesOfScope(elementInstanceKey, collect) // the mappings, inheriting nothing
	} else {
		err = state.VisibleVariables(store, elementInstanceKey, collect)
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// varToAny maps a stored variable to its JSON-ready Go value. A number keeps its
// exact canonical decimal text via json.Number rather than being routed through a
// float, so large or high-precision numbers survive intact. A structured value
// (VarJSON) is re-parsed from its stored JSON so the worker payload nests it
// as a real object/array rather than a JSON-in-a-string blob.
func varToAny(v *model.VariableValue) any {
	switch v.Kind {
	case model.VarBool:
		return v.Bool
	case model.VarNumber:
		return json.Number(v.Text)
	case model.VarString:
		return v.Text
	case model.VarJSON:
		dec := json.NewDecoder(strings.NewReader(v.Text))
		dec.UseNumber()
		var out any
		if err := dec.Decode(&out); err != nil {
			return nil
		}
		return out
	default:
		return nil
	}
}
