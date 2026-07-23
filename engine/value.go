package engine

import "github.com/pblumer/atlas/model"

// inflightValue carries a record's payload by value through the hot path. Only
// the field selected by the accompanying ValueType is meaningful. Holding the
// payload inline — rather than a *model.XxxValue behind the model.Value
// interface — means commands and events never box a value or allocate one per
// record on the processor path (invariant I1).
type inflightValue struct {
	process      model.ProcessInstanceValue
	element      model.ElementInstanceValue
	job          model.JobValue
	variable     model.VariableValue
	timer        model.TimerValue
	subscription model.MessageSubscriptionValue
	messageFlow  model.MessageFlowValue
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
	case model.VTMessageFlow:
		return &v.messageFlow
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
	case model.VTMessageFlow:
		if v, ok := rec.Value.(*model.MessageFlowValue); ok {
			iv.messageFlow = *v
		}
	}
	return iv
}
