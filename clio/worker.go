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
// worker uses it to find the connector, subject, and event type a clio job
// belongs to, so one handler serves every deployed process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Handler builds a job handler that performs a clio "write-events" connector
// task. Register it with a [job.Runner] for the reserved ClioWriteJobType index;
// the runner then pulls activatable clio jobs, and for each the handler resolves
// the connector task's connector/subject/event-type from the compiled process,
// resolves the connector's client from reg, and appends an event carrying the
// instance's variables as its body — keyed by the job key so an at-least-once
// retry de-duplicates (ADR-0036). Returning an error leaves the job pending,
// exactly as for any worker; the runner completes it only on success.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry) job.Handler {
	return func(j job.Job) error {
		cp, detail, client, ei, ok, err := resolveConnector(store, lookup, reg, j)
		if err != nil || !ok {
			return err
		}
		data, err := instanceData(store, ei.ProcessInstanceKey)
		if err != nil {
			return fmt.Errorf("clio: read variables for element %d: %w", j.ElementInstanceKey, err)
		}
		return client.WriteEvent(context.Background(), Event{
			Subject:        cp.Intern(detail.Subject),
			Type:           cp.Intern(detail.EventType),
			Data:           data,
			IdempotencyKey: strconv.FormatUint(j.Key, 10),
		})
	}
}

// QueryHandler builds a job handler for a clio "query" connector task: it reads
// projected state (get_state) or runs a stored query (run_query) on the task's
// connector and writes the result back into the task's result variable. Register it
// with a [job.Runner] for the reserved ClioQueryJobType index via HandleWithOutput,
// like the REST worker (ADR-0036/0067): when the task carries a query the handler
// runs it, otherwise it reads get_state for the task's subject (with the optional
// reduce spec). Returning an error leaves the job pending (retry, then an incident),
// exactly as for the write handler.
func QueryHandler(store *state.Store, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		cp, detail, client, _, ok, err := resolveConnector(store, lookup, reg, j)
		if err != nil || !ok {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		var result any
		if query := cp.Intern(detail.ClioQuery); query != "" {
			if result, err = client.Query(context.Background(), query); err != nil {
				return nil, err
			}
		} else {
			var state map[string]any
			if state, err = client.GetState(context.Background(), cp.Intern(detail.Subject), cp.Intern(detail.ReduceSpec)); err != nil {
				return nil, err
			}
			result = state
		}
		if resultVar == "" {
			return nil, nil // the model discards the result
		}
		return []model.VariableValue{resultVariable(resultVar, result)}, nil
	}
}

// ReadHandler builds a job handler for a clio "read" connector task: it reads the
// task's subject events (up to the task's limit) from the connector and writes them
// back into the task's result variable as a JSON array. Register it for the reserved
// ClioReadJobType index via HandleWithOutput (ADR-0036).
func ReadHandler(store *state.Store, lookup ProcessLookup, reg *Registry) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		cp, detail, client, _, ok, err := resolveConnector(store, lookup, reg, j)
		if err != nil || !ok {
			return nil, err
		}
		events, err := client.ReadEvents(context.Background(), ReadEventsRequest{
			Subject: cp.Intern(detail.Subject),
			Limit:   int(detail.Limit),
		})
		if err != nil {
			return nil, err
		}
		resultVar := cp.Intern(detail.ResultVar)
		if resultVar == "" {
			return nil, nil
		}
		rows := make([]any, len(events))
		for i, e := range events {
			rows[i] = map[string]any{"id": e.ID, "type": e.Type, "subject": e.Subject, "data": e.Data}
		}
		return []model.VariableValue{resultVariable(resultVar, rows)}, nil
	}
}

// resolveConnector performs the guards shared by every clio handler: it loads the
// job's element instance (a vanished one is a no-op), finds its compiled process and
// connector-task detail, and resolves the named connector's client. ok is false with
// a nil error only when the element instance is already gone; any other failure
// returns an error so the job stays pending.
func resolveConnector(store *state.Store, lookup ProcessLookup, reg *Registry, j job.Job) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail, Client, *model.ElementInstanceValue, bool, error) {
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
	detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
	name := cp.Intern(detail.Connector)
	client, ok := reg.Client(name)
	if !ok {
		return nil, nil, nil, nil, false, fmt.Errorf("clio: no connector registered as %q", name)
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

// instanceData reads the instance's variables into a JSON-ready map — the event
// body a connector task sends. Until output mappings exist (Milestone 1) the
// whole variable scope is the payload, exactly as a message throw publishes its
// instance's variables (ADR-0035/0036).
func instanceData(store *state.Store, scope uint64) (map[string]any, error) {
	data := map[string]any{}
	err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
		data[v.Name] = varToAny(v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// varToAny maps a stored variable to its JSON-ready Go value. A number keeps its
// exact canonical decimal text via json.Number rather than being routed through a
// float, so large or high-precision numbers survive intact. A structured value
// (VarJSON) is re-parsed from its stored JSON so the connector payload nests it
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
