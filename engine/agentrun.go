package engine

import (
	"encoding/json"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// --- An agent's run on the record (ADR-0117, as built by ADR-0566) ---
//
// Every completed call to a language model leaves one AgentRun fact beside the job's
// completion: which element, which round, which model answered, what it read and wrote,
// and whether the call reached a bound the model states. The worker reports the part
// only it can know; everything that decides anything is stated here, from the job this
// completion names and from the totals the earlier facts were folded into.
//
// It is written for every such completion, reported or not. A worker that says nothing
// about its call has still made one, and a round that went unrecorded would be a round
// the ceiling does not count — which is the one thing a ceiling must not allow.

// AgentRunTotal reads what an element instance's model calls add up to so far, through
// the in-flight transaction.
func (c *ProcessingContext) AgentRunTotal(piKey, elKey uint64) model.AgentRunTotal {
	t, err := c.tx.AgentRunTotal(piKey, elKey)
	c.p.fail(err)
	return t
}

// isAgentJob reports whether a job is a call to a language model: an agent-driven
// container's round, or an ai task's one call.
func isAgentJob(jobType int32) bool {
	return jobType == compiler.AgentJobTypeIndex || jobType == compiler.AiTaskJobTypeIndex
}

// recordAgentRun appends the fact of one model call in the batch that completes its job
// (I2: the two are durable together). The allocation when a worker reported nothing is
// deliberate and off the hot path: a model was just asked a question, which took
// seconds, and this path runs once per answer.
func recordAgentRun(c *ProcessingContext, jobKey uint64, job *model.JobValue) {
	if !isAgentJob(job.JobType) || c.cmd.Manual {
		// An operator forcing the job through asked no model anything (ADR-0159); what
		// they did is on the record as an operator action, and it is not a round.
		return
	}
	run := c.cmd.AgentRun
	if run == nil {
		run = &model.AgentRunValue{}
	}
	// Never believe a worker about which task it holds, or about anything the engine
	// can state itself: the keys are the leased job's, and the fields below are
	// overwritten whatever the report carried.
	run.Kind = model.AgentRunModelCall
	run.ProcessInstanceKey = job.ProcessInstanceKey
	run.ElementInstanceKey = job.ElementInstanceKey
	run.JobKey = jobKey
	run.CallId = ""
	var limits *compiler.AgentLimits
	if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
		run.ProcessDefKey, run.ElementId = ei.ProcessDefKey, ei.ElementId
		if d := agentContainer(c, ei); d != nil {
			limits = &d.AgentLimits
		}
	}
	prev := c.AgentRunTotal(job.ProcessInstanceKey, job.ElementInstanceKey)
	run.Round = prev.Rounds + 1
	run.SumInputTokens = prev.InputTokens + run.InputTokens
	run.SumOutputTokens = prev.OutputTokens + run.OutputTokens
	run.ToolCalls = agentToolCallsJSON(c.cmd.ToolCalls)
	run.Limit = model.AgentRunLimitNone
	if limits != nil {
		run.Limit = agentLimitReached(*limits, prev, run)
	}
	c.appendEvent(job.ProcessInstanceKey, model.VTAgentRun, model.IntentAgentRunRecorded, inflightValue{agentRun: run})
}

// agentContainer returns the detail of an agent-driven ad-hoc subprocess, or nil when
// the element instance is anything else.
func agentContainer(c *ProcessingContext, ei *model.ElementInstanceValue) *compiler.AdHocDetail {
	if ei.BpmnElementType != uint8(compiler.TypeAdHocSubProcess) {
		return nil
	}
	cp := c.process(ei.ProcessDefKey)
	if cp == nil {
		return nil
	}
	if d := cp.AdHoc(cp.Node(ei.ElementId).Detail); d.AgentDriven {
		return d
	}
	return nil
}

// agentLimitReached says which bound, if any, the call that was just recorded reached.
//
// Both tests are stateless on purpose, in the shape parkRunawayLoop uses for a standard
// loop: the round bound is reached every time the count is a multiple of the allowance,
// and the token budget every time the running sum crosses a multiple of it. So resolving
// the incident needs to write nothing — the next round simply is not a multiple, and the
// run carries on until the next one is.
func agentLimitReached(l compiler.AgentLimits, prev model.AgentRunTotal, run *model.AgentRunValue) model.AgentRunLimit {
	if run.Round%l.Rounds() == 0 {
		return model.AgentRunLimitRounds
	}
	if budget := uint64(l.TokenBudget); budget > 0 {
		before := prev.InputTokens + prev.OutputTokens
		after := run.SumInputTokens + run.SumOutputTokens
		if before/budget < after/budget {
			return model.AgentRunLimitTokens
		}
	}
	return model.AgentRunLimitNone
}

// agentToolCallsJSON names the tools a round activated, without their arguments: the
// names are element ids and the call ids are the model's, and neither is process data.
// The arguments are — they are on the record as the variables of the activity each call
// started, and in the model's own turn.
func agentToolCallsJSON(calls []model.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	type call struct {
		Tool   string `json:"tool"`
		CallId string `json:"callId,omitempty"`
	}
	out := make([]call, len(calls))
	for i, c := range calls {
		out[i] = call{Tool: c.Tool, CallId: c.CallId}
	}
	raw, _ := json.Marshal(out) // two strings per entry: nothing here can fail to encode
	return string(raw)
}

// parkAgentAtLimit stops an agent-driven container that would start another round past
// a bound, and raises an incident on it. The container stays active and takes no
// outgoing flow: it is parked, not finished, so nothing downstream runs on an answer the
// agent never gave. Resolving the incident asks for the next round (resumeParkedElement).
func parkAgentAtLimit(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue,
	l compiler.AgentLimits, total model.AgentRunTotal) {
	var msg string
	switch total.Limit {
	case model.AgentRunLimitTokens:
		used := total.InputTokens + total.OutputTokens
		budget := uint64(l.TokenBudget)
		msg = "the agent has used " + strconv.FormatUint(used, 10) + " tokens in " +
			strconv.FormatUint(uint64(total.Rounds), 10) + " rounds, past its tokenBudget of " +
			strconv.FormatUint(budget, 10) + "; resolve to let it continue up to " +
			strconv.FormatUint((used/budget+1)*budget, 10)
	default:
		stated := "its maxIterations"
		if l.MaxRounds == 0 {
			stated = "the ceiling for an agent that states no maxIterations"
		}
		msg = "the agent ran " + strconv.FormatUint(uint64(total.Rounds), 10) +
			" rounds without finishing, which is " + stated + "; resolve to run " +
			strconv.FormatUint(uint64(l.Rounds()), 10) + " more"
	}
	c.AppendIncidentEvent(model.IntentIncidentCreated, model.IncidentValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		ElementId:          ei.ElementId,
		RaisedAt:           c.Now(),
		Reason:             model.IncidentAgentLimit,
		Message:            msg,
	})
}

// recordAgentToolResult appends what one tool returned to the run's record, paired with
// the call that asked for it by the model's own call id
// (ADR-0567). With the model calls it makes the history a
// transcript: the next round can be shown its own turn and the answer to each call it
// made, instead of a list of results it has to match up itself.
//
// Only an activity the agent called has a call id in its own scope. One reached by a
// sequence flow from a tool does not, and adds no fact: what the call "returned" is what
// the called activity itself finished with.
//
// The text is what the engine evaluated, so it holds nothing the engine does not already
// hold: a personal value read here is its envelope, never its plaintext (ADR-0314).
func recordAgentToolResult(c *ProcessingContext, containerKey, toolKey uint64, tool *model.ElementInstanceValue, val expr.Value) {
	callID := c.GetVariable(toolKey, AgentCallIdVariable)
	if callID == nil || callID.Text == "" {
		return
	}
	kind, b, text := expr.Classify(val)
	if kind == expr.KindBool {
		text = strconv.FormatBool(b)
	}
	c.appendEvent(tool.ProcessInstanceKey, model.VTAgentRun, model.IntentAgentRunRecorded, inflightValue{agentRun: &model.AgentRunValue{
		Kind:               model.AgentRunToolResult,
		ProcessInstanceKey: tool.ProcessInstanceKey,
		ElementInstanceKey: containerKey,
		ProcessDefKey:      tool.ProcessDefKey,
		ElementId:          tool.ElementId,
		Round:              c.AgentRunTotal(tool.ProcessInstanceKey, containerKey).Rounds,
		CallId:             callID.Text,
		Answer:             text,
	}})
}
