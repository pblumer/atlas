package engine

import (
	"github.com/pblumer/chrampfer/compiler"
	"github.com/pblumer/chrampfer/model"
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
	}
}

func (p *Processor) registerBehaviors() {
	p.behaviors[compiler.TypeStartEvent] = startEventBehavior{}
	p.behaviors[compiler.TypeEndEvent] = endEventBehavior{}
	p.behaviors[compiler.TypeServiceTask] = serviceTaskBehavior{}
}

// --- command handlers ---

// handleProcessInstanceActivating creates the process instance and activates
// each start event.
func handleProcessInstanceActivating(c *ProcessingContext) {
	pv := c.cmd.Value.(*model.ProcessInstanceValue)
	piKey := c.NewKey()
	c.AppendFollowupEvent(piKey, model.IntentActivated, pv)

	cp := c.process(pv.ProcessDefKey)
	for _, startID := range cp.StartEvents() {
		node := cp.Node(startID)
		c.AppendFollowupCommand(c.NewKey(), model.IntentActivating, &model.ElementInstanceValue{
			ProcessInstanceKey: piKey,
			ProcessDefKey:      pv.ProcessDefKey,
			ElementId:          startID,
			FlowScopeKey:       piKey, // root elements are scoped by the instance
			BpmnElementType:    uint8(node.Type),
		})
	}
}

// handleElementActivating emits the Activated lifecycle event, then runs the
// element-type behavior.
func handleElementActivating(c *ProcessingContext) {
	ei := c.cmd.Value.(*model.ElementInstanceValue)
	c.AppendFollowupEvent(c.cmd.Key, model.IntentActivated, ei)
	c.p.behavior(ei).OnActivated(c, c.cmd.Key, ei)
}

// handleElementCompleting runs the element-type completion behavior.
func handleElementCompleting(c *ProcessingContext) {
	ei := c.cmd.Value.(*model.ElementInstanceValue)
	c.p.behavior(ei).OnCompleting(c, c.cmd.Key, ei)
}

// handleJobCompleted retires the job and tells its element to complete.
func handleJobCompleted(c *ProcessingContext) {
	job := c.GetJob(c.cmd.Key)
	if job == nil {
		return // already gone or never existed; nothing to do
	}
	c.AppendFollowupEvent(c.cmd.Key, model.IntentJobCompleted, job)

	ei := c.GetElementInstance(job.ElementInstanceKey)
	if ei != nil {
		c.AppendFollowupCommand(job.ElementInstanceKey, model.IntentCompleting, ei)
	}
}

// behavior resolves the behavior for an element instance's type.
func (p *Processor) behavior(ei *model.ElementInstanceValue) bpmnBehavior {
	return p.behaviors[ei.BpmnElementType]
}

// --- element behaviors ---

// completeAndTakeFlows is the default OnCompleting: emit Completed, then
// activate the targets of every outgoing flow.
func completeAndTakeFlows(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendFollowupEvent(key, model.IntentCompleted, ei)
	takeOutgoingFlows(c, ei)
}

// takeOutgoingFlows activates a fresh element instance for each outgoing flow's
// target. (Sequence-flow-taken audit events are deferred; the lifecycle events
// are enough to drive and recover state.)
func takeOutgoingFlows(c *ProcessingContext, ei *model.ElementInstanceValue) {
	cp := c.process(ei.ProcessDefKey)
	for _, flowID := range cp.Outgoing(ei.ElementId) {
		target := cp.Node(cp.Flow(flowID).Target)
		c.AppendFollowupCommand(c.NewKey(), model.IntentActivating, &model.ElementInstanceValue{
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
	c.AppendFollowupCommand(key, model.IntentCompleting, ei)
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
	job := &model.JobValue{
		ProcessInstanceKey: ei.ProcessInstanceKey,
		ElementInstanceKey: key,
		JobType:            detail.JobType,
		Retries:            detail.Retries,
	}
	jobKey := c.NewKey()
	c.AppendFollowupEvent(jobKey, model.IntentJobCreated, job)
	c.SideEffect(func() { c.p.notifyJobAvailable(detail.JobType) })
	// Stays Activated: no Completing until a worker completes the job.
}

func (serviceTaskBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	completeAndTakeFlows(c, key, ei)
}

// endEventBehavior: a none end event completes and, if it was the last active
// element in its scope, completes the process instance.
type endEventBehavior struct{}

func (endEventBehavior) OnActivated(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendFollowupCommand(key, model.IntentCompleting, ei)
}

func (endEventBehavior) OnCompleting(c *ProcessingContext, key uint64, ei *model.ElementInstanceValue) {
	c.AppendFollowupEvent(key, model.IntentCompleted, ei) // decrements scope's active children
	if c.ActiveChildren(ei.FlowScopeKey) == 0 {
		if pi := c.GetProcessInstance(ei.ProcessInstanceKey); pi != nil {
			c.AppendFollowupEvent(ei.ProcessInstanceKey, model.IntentCompleted, pi)
		}
	}
}
