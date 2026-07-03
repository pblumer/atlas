package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// bpmnBehavior is the per-element-type logic invoked at lifecycle transitions.
// The element instance lifecycle (Activating → Activated → Completing →
// Completed) is shared; the behavior fills in what each element type does.
type bpmnBehavior interface {
	// OnActivated runs after the element's Activated event. The element may do
	// work (create a job), finish immediately (events), or wait.
	OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue)
	// OnCompleting runs when the element is told to complete. By default it
	// emits Completed and takes the outgoing flows.
	OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue)
}

// handlerKey packs a (ValueType, Intent) pair into a dispatch key.
func handlerKey(vt model.ValueType, intent model.Intent) uint16 {
	return uint16(vt)<<8 | uint16(intent)
}

func (p *Processor) registerHandlers() {
	p.handlers = map[uint16]func(*ProcessingContext){
		handlerKey(model.VTProcessInstance, model.IntentActivating): handleProcessInstanceActivating,
		handlerKey(model.VTElementInstance, model.IntentActivating): handleElementActivating,
		handlerKey(model.VTElementInstance, model.IntentCompleting): handleElementCompleting,
		handlerKey(model.VTJob, model.IntentJobCompleted):           handleJobCompleted,
		handlerKey(model.VTUserTask, model.IntentUserTaskClaimed):   handleUserTaskClaim,
		handlerKey(model.VTUserTask, model.IntentUserTaskCompleted): handleUserTaskComplete,
	}
}

func (p *Processor) registerBehaviors() {
	p.behaviors[compiler.TypeStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeEndEvent] = endEventBehavior{}
	p.behaviors[compiler.TypeServiceTask] = serviceTaskBehavior{}
	p.behaviors[compiler.TypeUserTask] = userTaskBehavior{}
}

// --- command handlers ---

// handleProcessInstanceActivating creates the process instance and activates
// each start event.
func handleProcessInstanceActivating(c *ProcessingContext) {
	defKey := c.cmd.Value.process.ProcessDefKey
	piKey := c.NewKey()
	c.AppendProcessInstanceEvent(piKey, model.IntentActivated, model.ProcessInstanceValue{ProcessDefKey: defKey})

	cp := c.process(defKey)
	for _, startID := range cp.StartEvents() {
		node := cp.Node(startID)
		c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      defKey,
			ElementId:          startID,
			FlowScopeKey:       piKey, // root elements are scoped by the instance
			BpmnElementType:    uint8(node.Type),
		})
	}
}

// handleElementActivating emits the Activated lifecycle event, then runs the
// element-type behavior.
func handleElementActivating(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	c.AppendElementEvent(c.cmd.Key, model.IntentActivated, *ei)
	c.p.behavior(ei.BpmnElementType).OnActivated(c, c.cmd.Key, ei)
}

// handleElementCompleting runs the element-type completion behavior.
func handleElementCompleting(c *ProcessingContext) {
	ei := &c.cmd.Value.element
	c.p.behavior(ei.BpmnElementType).OnCompleting(c, c.cmd.Key, ei)
}

// handleJobCompleted retires the job and tells its element to complete.
func handleJobCompleted(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return // already gone or never existed; nothing to do
	}
	c.AppendJobEvent(c.cmd.Key, model.IntentJobCompleted, *job)

	if ei := c.GetElementInstance(job.ElementInstanceKey); ei != nil {
		c.AppendElementCommand(job.ElementInstanceKey, model.IntentCompleting, *ei)
	}
}

// handleUserTaskClaim assigns a claimant to an open task. It is a no-op if the
// task is gone or already claimed, so a duplicate or racing claim is rejected
// without persisting anything.
func handleUserTaskClaim(c *ProcessingContext) {
	ut := c.GetUserTask(c.cmd.Key)
	if ut == nil || ut.State == model.UserTaskClaimed {
		return
	}
	ut.Assignee = c.cmd.Value.userTask.Assignee
	ut.State = model.UserTaskClaimed
	c.AppendUserTaskEvent(c.cmd.Key, model.IntentUserTaskClaimed, *ut)
}

// handleUserTaskComplete retires the task and tells its element to complete.
func handleUserTaskComplete(c *ProcessingContext) {
	ut := c.GetUserTask(c.cmd.Key)
	if ut == nil {
		return // already gone or never existed; nothing to do
	}
	c.AppendUserTaskEvent(c.cmd.Key, model.IntentUserTaskCompleted, *ut)

	if ei := c.GetElementInstance(ut.ElementInstanceKey); ei != nil {
		c.AppendElementCommand(ut.ElementInstanceKey, model.IntentCompleting, *ei)
	}
}

// behavior resolves the behavior for a BPMN element type.
func (p *Processor) behavior(bpmnType uint8) bpmnBehavior {
	return p.behaviors[bpmnType]
}

// --- element behaviors ---

// completeAndTakeFlows is the default OnCompleting: emit Completed, then
// activate the targets of every outgoing flow.
func completeAndTakeFlows(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei)
	takeOutgoingFlows(c, ei)
}

// takeOutgoingFlows activates a fresh element instance for each outgoing flow's
// target. (Sequence-flow-taken audit events are deferred; the lifecycle events
// are enough to drive and recover state.)
func takeOutgoingFlows(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		target := cp.Node(cp.Flow(flowID).Target)
		c.AppendElementCommand(c.NewKey(), model.IntentActivating, model.ElementInstanceValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ProcessDefKey:      ei.ProcessDefKey,
			ElementId:          target.ElementId,
			FlowScopeKey:       ei.FlowScopeKey,
			BpmnElementType:    uint8(target.Type),
		})
	}
}

// startEventBehavior: a none start event has no work; it completes at once.
type startEventBehavior struct{}

func (startEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (startEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// serviceTaskBehavior: create a job on activation and wait; complete when the
// job is completed by a worker.
type serviceTaskBehavior struct{}

func (serviceTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.ServiceTask(cp.Node(ei.ElementId).Detail)
	jobKey := c.NewKey()
	c.AppendJobEvent(jobKey, model.IntentJobCreated, model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	})
	c.NotifyJobAvailable(detail.JobType)
	// Stays Activated: no Completing until a worker completes the job.
}

func (serviceTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// userTaskBehavior: create a human task on activation and wait; complete when a
// person completes the task through the tasklist (ADR-0013). The mechanism
// mirrors the service task (hand out work, wait, complete via command), but the
// task is offered to a candidate group and claimed rather than leased.
type userTaskBehavior struct{}

func (userTaskBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	detail := cp.UserTask(cp.Node(ei.ElementId).Detail)
	taskKey := c.NewKey()
	c.AppendUserTaskEvent(taskKey, model.IntentUserTaskCreated, model.UserTaskValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		CandidateGroup:     detail.CandidateGroup,
		Assignee:           -1, // unassigned until claimed
		FormRef:            detail.FormRef,
		State:              model.UserTaskCreated,
	})
	c.NotifyUserTaskAvailable(detail.CandidateGroup)
	// Stays Activated: no Completing until a human completes the task.
}

func (userTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// endEventBehavior: a none end event completes and, if it was the last active
// element in its scope, completes the process instance.
type endEventBehavior struct{}

func (endEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementCommand(key, model.IntentCompleting, *ei)
}

func (endEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendElementEvent(key, model.IntentCompleted, *ei) // decrements scope's active children
	if c.ActiveChildren(ei.FlowScopeKey) == 0 {
		if pi := c.GetProcessInstance(ei.ProcessInstanceKey); pi != nil {
			c.AppendProcessInstanceEvent(ei.ProcessInstanceKey, model.IntentCompleted, *pi)
		}
	}
}
