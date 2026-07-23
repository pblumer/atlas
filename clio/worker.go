package clio

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
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
// retry de-duplicates (ADR-0026). Returning an error leaves the job pending,
// exactly as for any worker; the runner completes it only on success.
func Handler(store *state.Store, lookup ProcessLookup, reg *Registry) job.Handler {
	return func(j job.Job) error {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return err
		}
		if !ok {
			return nil // element instance gone (e.g. already completed); nothing to do
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return fmt.Errorf("clio: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail := cp.ConnectorTask(cp.Node(ei.ElementId).Detail)
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return fmt.Errorf("clio: no connector registered as %q", name)
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

// instanceData reads the instance's variables into a JSON-ready map — the event
// body a connector task sends. Until output mappings exist (Milestone 1) the
// whole variable scope is the payload, exactly as a message throw publishes its
// instance's variables (ADR-0025/0026).
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
// float, so large or high-precision numbers survive intact.
func varToAny(v *model.VariableValue) any {
	switch v.Kind {
	case model.VarBool:
		return v.Bool
	case model.VarNumber:
		return json.Number(v.Text)
	case model.VarString:
		return v.Text
	default:
		return nil
	}
}
