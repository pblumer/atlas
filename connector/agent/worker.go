package agent

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ProcessLookup resolves a process-definition key to its compiled process, so one
// handler serves every deployed process — the shape every other Worker Type uses.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// resultsVariable is where a round's tool results accumulate on the container's scope
// when the model names a result collection. The worker reads it to tell the model what
// its earlier calls returned; the engine writes it (ADR-0253).
const resultsVariable = "toolCallResults"

// Handler builds the job handler for an agent-driven ad-hoc's round job. Register it
// under [compiler.AgentJobTypeIndex] via HandleCompleting — the widest handler shape,
// because a round's answer is either tool calls or output variables and only that shape
// carries both.
//
// One job is one round. The handler does not loop: the loop is the process itself, where
// each round is a durable job and each tool call an ordinary activity (ADR-0253). That is
// what keeps an agent run inspectable, replayable and interruptible, and it is why this
// worker holds no state between rounds — everything it knows on the second round it reads
// from the instance.
func Handler(store state.Reader, lookup ProcessLookup, m Model) job.CompletingHandler {
	return func(j job.Job) (job.Completion, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return job.Completion{}, err
		}
		if !ok {
			// The container is gone — cancelled, or its instance terminated while this
			// round was in flight. Nothing to decide and nothing to fail.
			return job.Completion{}, nil
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return job.Completion{}, fmt.Errorf("agent: no compiled process for def %d", ei.ProcessDefKey)
		}
		tools, err := Toolbox(cp, ei.ElementId)
		if err != nil {
			return job.Completion{}, err
		}

		req := Request{
			Goal:    cp.ElementDocumentation(ei.ElementId),
			Tools:   tools,
			Results: collectedResults(store, j.ElementInstanceKey),
		}
		req.Round = len(req.Results) + 1

		decision, err := m.Decide(context.Background(), req)
		if err != nil {
			// A model that cannot be reached is a job failure like any other: retried
			// while retries remain, then an incident (ADR-0061). It is deliberately not
			// an empty decision, which would silently end the agent's run as if it had
			// answered.
			return job.Completion{}, fmt.Errorf("agent: %w", err)
		}
		return job.Completion{ToolCalls: decision.ToolCalls, Outputs: decision.Outputs}, nil
	}
}

// collectedResults reads what this container's earlier tool calls returned, as the model
// needs to see them. Absent (no result collection configured, or the first round) it is
// empty, which is the honest answer: nothing has been called yet.
func collectedResults(store state.Reader, containerKey uint64) []string {
	var out []string
	_ = state.VisibleVariables(store, containerKey, func(v *model.VariableValue) error {
		if v.Name == resultsVariable && v.Text != "" {
			out = append(out, v.Text)
		}
		return nil
	})
	return out
}
