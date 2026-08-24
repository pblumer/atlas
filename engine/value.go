package engine

import "github.com/pblumer/atlas/model"

// inflightValue carries a record's payload by value through the hot path. Only
// the field selected by the accompanying ValueType is meaningful. Holding the
// payload inline — rather than a *model.XxxValue behind the model.Value
// interface — means commands and events never box a value or allocate one per
// record on the processor path (invariant I1).
type inflightValue struct {
	process       model.ProcessInstanceValue
	element       model.ElementInstanceValue
	job           model.JobValue
	variable      model.VariableValue
	timer         model.TimerValue
	subscription  model.MessageSubscriptionValue
	signalSub     model.SignalSubscriptionValue
	messageFlow   model.MessageFlowValue
	dataObject    model.DataObjectValue
	incident      model.IncidentValue
	decisionEval  model.DecisionEvaluationValue
	inbound       model.InboundDeliveryValue
	variableAudit model.VariableAuditValue
	compensable   model.CompensableValue
	operatorAct   model.OperatorActionValue
	// migration rides only on the operator-initiated migrate command and the event it
	// emits (ADR-0162). Its mapping is a slice, so — like the decision a job completion
	// carries — it is a non-hot-path payload: no token movement ever populates it. It is
	// also what makes inflightValue non-comparable, so compare one with reflect.DeepEqual
	// rather than ==.
	migration model.ProcessMigrationValue
}

// asValue returns a model.Value pointing at the active field, for encoding. The
// returned interface wraps an interior pointer into existing memory, so it does
// not allocate.
func (v *inflightValue) asValue(vt model.ValueType) model.Value {
	switch vt {
	case model.VTProcessInstance:
		return &v.process
	case model.VTElementInstance:
		return &v.element
	case model.VTJob:
		return &v.job
	case model.VTVariable:
		return &v.variable
	case model.VTTimer:
		return &v.timer
	case model.VTMessageSubscription:
		return &v.subscription
	case model.VTSignal:
		return &v.signalSub
	case model.VTMessageFlow:
		return &v.messageFlow
	case model.VTDataObject:
		return &v.dataObject
	case model.VTIncident:
		return &v.incident
	case model.VTDecisionEvaluation:
		return &v.decisionEval
	case model.VTInboundDelivery:
		return &v.inbound
	case model.VTVariableAudit:
		return &v.variableAudit
	case model.VTCompensable:
		return &v.compensable
	case model.VTOperatorAction:
		return &v.operatorAct
	case model.VTProcessMigration:
		return &v.migration
	}
	return nil
}

// eventRecord is an event accumulated during a batch: its header plus its
// by-value payload. Stored in a slice the processor reuses across batches.
type eventRecord struct {
	header model.RecordHeader
	value  inflightValue
}

// inflightFromRecord copies a decoded record's payload into an inflightValue.
// Used only on recovery, where decoding has already allocated, so an extra copy
// is harmless and keeps the live and replay applyToState identical (invariant I4).
func inflightFromRecord(rec model.Record) inflightValue {
	var iv inflightValue
	switch rec.Header.ValueType {
	case model.VTProcessInstance:
		if v, ok := rec.Value.(*model.ProcessInstanceValue); ok {
			iv.process = *v
		}
	case model.VTElementInstance:
		if v, ok := rec.Value.(*model.ElementInstanceValue); ok {
			iv.element = *v
		}
	case model.VTJob:
		if v, ok := rec.Value.(*model.JobValue); ok {
			iv.job = *v
		}
	case model.VTVariable:
		if v, ok := rec.Value.(*model.VariableValue); ok {
			iv.variable = *v
		}
	case model.VTTimer:
		if v, ok := rec.Value.(*model.TimerValue); ok {
			iv.timer = *v
		}
	case model.VTMessageSubscription:
		if v, ok := rec.Value.(*model.MessageSubscriptionValue); ok {
			iv.subscription = *v
		}
	case model.VTSignal:
		if v, ok := rec.Value.(*model.SignalSubscriptionValue); ok {
			iv.signalSub = *v
		}
	case model.VTMessageFlow:
		if v, ok := rec.Value.(*model.MessageFlowValue); ok {
			iv.messageFlow = *v
		}
	case model.VTDataObject:
		if v, ok := rec.Value.(*model.DataObjectValue); ok {
			iv.dataObject = *v
		}
	case model.VTIncident:
		if v, ok := rec.Value.(*model.IncidentValue); ok {
			iv.incident = *v
		}
	case model.VTDecisionEvaluation:
		if v, ok := rec.Value.(*model.DecisionEvaluationValue); ok {
			iv.decisionEval = *v
		}
	case model.VTInboundDelivery:
		if v, ok := rec.Value.(*model.InboundDeliveryValue); ok {
			iv.inbound = *v
		}
	case model.VTVariableAudit:
		if v, ok := rec.Value.(*model.VariableAuditValue); ok {
			iv.variableAudit = *v
		}
	case model.VTCompensable:
		if v, ok := rec.Value.(*model.CompensableValue); ok {
			iv.compensable = *v
		}
	case model.VTOperatorAction:
		if v, ok := rec.Value.(*model.OperatorActionValue); ok {
			iv.operatorAct = *v
		}
	case model.VTProcessMigration:
		if v, ok := rec.Value.(*model.ProcessMigrationValue); ok {
			iv.migration = *v
		}
	}
	return iv
}
