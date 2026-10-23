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
	// entitlement rides only on the two inventory commands and the events they
	// emit (ADR-0312). Like migration it never
	// rides token movement, so it costs the hot path the field and nothing else.
	entitlement model.EntitlementValue
	// entitlementEnd rides only on the revocation command and the event it emits
	// (ADR-0346). Like entitlement it never rides token
	// movement, so it costs the hot path the field and nothing else.
	entitlementEnd model.EntitlementHistoryValue
	// migration rides only on the operator-initiated migrate command and the event it
	// emits (ADR-0162). Its mapping is a slice, so — like the decision a job completion
	// carries — it is a non-hot-path payload: no token movement ever populates it. It is
	// also what makes inflightValue non-comparable, so compare one with reflect.DeepEqual
	// rather than ==.
	migration model.ProcessMigrationValue
	// variableIndex rides only on the reindex command and the events it emits: a
	// membership correction after a migration or an operator's repair (ADR-0244). Like
	// migration it never rides token movement, so it costs the hot path nothing but the
	// field.
	variableIndex model.VariableIndexValue
	// trigger rides only on the directed-trigger command and the receipt events it
	// and the prune command emit (ADR-0425). Never on token movement.
	trigger model.TriggerReceiptValue
	// actionOutcome rides on the outcome-report command and the event it emits, and
	// beside an entitlement on a grant or revocation that ends a provision or a
	// return, so both commit in one batch (ADR-0429 §3). Never on token movement.
	actionOutcome model.ActionOutcomeValue
	// feedRetention rides only on the feed's prune command and the event it emits
	// (ADR-0429 §5). Never on token movement.
	feedRetention model.FeedRetentionValue
	// message rides on the message buffer's events and the sweep's expiry command
	// (ADR-0370). Its payload is a slice, so like migration it is a non-hot-path
	// field: no token movement populates it.
	message model.MessageValue
	// dispatch rides on the record of a send (ADR-0472 §1). It is written on
	// token movement — a throw — but holds only strings that already exist and fixed
	// fields, so filling it allocates nothing.
	dispatch model.DispatchValue
	// authorization and approval ride only on an enforcement point's decision, an
	// approver's answer and the events they emit
	// (ADR-0475). They are pointers rather than inline
	// values, unlike the rest of this struct, because the two records are wide — a dozen
	// strings each — and every command and event the processor copies carries this
	// struct: inline they would widen every token movement's copy for a record most
	// installations write a few times an hour. Behind a pointer they cost the hot path
	// sixteen bytes and nothing else, and the allocation falls on the governed action,
	// which has just made a network round trip to get here.
	authorization *model.AuthorizationValue
	approval      *model.ActionApprovalValue
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
	case model.VTVariableIndex:
		return &v.variableIndex
	case model.VTEntitlement:
		return &v.entitlement
	case model.VTEntitlementHistory:
		return &v.entitlementEnd
	case model.VTTriggerReceipt:
		return &v.trigger
	case model.VTActionOutcome:
		return &v.actionOutcome
	case model.VTFeedRetention:
		return &v.feedRetention
	case model.VTMessage:
		return &v.message
	case model.VTDispatch:
		return &v.dispatch
	case model.VTAuthorization:
		if v.authorization != nil {
			return v.authorization
		}
	case model.VTActionApproval:
		if v.approval != nil {
			return v.approval
		}
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
	case model.VTVariableIndex:
		if v, ok := rec.Value.(*model.VariableIndexValue); ok {
			iv.variableIndex = *v
		}
	case model.VTEntitlement:
		if v, ok := rec.Value.(*model.EntitlementValue); ok {
			iv.entitlement = *v
		}
	case model.VTEntitlementHistory:
		if v, ok := rec.Value.(*model.EntitlementHistoryValue); ok {
			iv.entitlementEnd = *v
		}
	case model.VTTriggerReceipt:
		if v, ok := rec.Value.(*model.TriggerReceiptValue); ok {
			iv.trigger = *v
		}
	case model.VTActionOutcome:
		if v, ok := rec.Value.(*model.ActionOutcomeValue); ok {
			iv.actionOutcome = *v
		}
	case model.VTFeedRetention:
		if v, ok := rec.Value.(*model.FeedRetentionValue); ok {
			iv.feedRetention = *v
		}
	case model.VTMessage:
		if v, ok := rec.Value.(*model.MessageValue); ok {
			iv.message = *v
		}
	case model.VTDispatch:
		if v, ok := rec.Value.(*model.DispatchValue); ok {
			iv.dispatch = *v
		}
	case model.VTAuthorization:
		if v, ok := rec.Value.(*model.AuthorizationValue); ok {
			iv.authorization = v
		}
	case model.VTActionApproval:
		if v, ok := rec.Value.(*model.ActionApprovalValue); ok {
			iv.approval = v
		}
	}
	return iv
}
