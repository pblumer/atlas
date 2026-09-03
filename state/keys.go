package state

import "encoding/binary"

// Column families partition the key space into indexes (data-model.md). The
// first byte of every key is its column family. All multi-byte numbers in keys
// are big-endian so lexicographic byte order matches numeric order — this is
// what makes the timer due-date range scan and the job/element prefix scans
// work.
type columnFamily byte

const (
	cfMeta                   columnFamily = 0x00 // meta:<name> → bytes
	cfElementInstance        columnFamily = 0x01 // el:<elKey> → ElementInstanceValue
	cfElByProc               columnFamily = 0x02 // elByProc:<procKey>:<elKey> → nil
	cfJob                    columnFamily = 0x03 // job:<jobKey> → JobValue
	cfJobActivatable         columnFamily = 0x04 // jobActivatable:<jobType>:<jobKey> → nil
	cfTimer                  columnFamily = 0x05 // timer:<dueDate>:<timerKey> → TimerValue
	cfProcessInstance        columnFamily = 0x06 // pi:<piKey> → ProcessInstanceValue
	cfActiveChildren         columnFamily = 0x07 // activeChildren:<scopeKey> → int32 count
	cfVariable               columnFamily = 0x08 // var:<scopeKey>:<name> → VariableValue
	cfProcessInstanceHistory columnFamily = 0x09 // piHist:<piKey> → ProcessInstanceValue (terminal)
	cfMessageSubscription    columnFamily = 0x0A // msgSub:<name>:<corrKey>:<elKey> → MessageSubscriptionValue
	cfElementVisit           columnFamily = 0x0B // elVisit:<procDefKey>:<piKey>:<elementId> → int64 count
	cfMessageFlow            columnFamily = 0x0C // msgFlow:<receiverDefKey>:<ts>:<pos> → MessageFlowValue
	cfJobByElement           columnFamily = 0x0D // jobByEl:<elKey> → jobKey (reverse lookup for boundary cancel)
	cfElementStep            columnFamily = 0x0E // elStep:<piKey>:<ts>:<pos> → int32 elementId
	cfElementReplay          columnFamily = 0x13 // elReplay:<piKey>:<ts>:<pos> → causal lifecycle value
	cfVariableSnapshot       columnFamily = 0x0F // varSnap:<scopeKey>:<ts>:<pos> → VariableValue
	cfDataObject             columnFamily = 0x10 // do:<scopeKey>:<name> → DataObjectValue
	cfDataObjectSnapshot     columnFamily = 0x11 // doSnap:<scopeKey>:<ts>:<pos> → DataObjectValue
	cfIncident               columnFamily = 0x12 // incident:<elKey> → IncidentValue (ADR-0061)
	cfDecisionEvaluation     columnFamily = 0x14 // decEval:<scopeKey>:<ts>:<pos> → DecisionEvaluationValue (ADR-0066)
	cfInboundHighWater       columnFamily = 0x15 // inboundHW:<sourceID> → uint64 last-applied sequence (ADR-0075)
	cfActiveStartKey         columnFamily = 0x16 // activeStartKey:<defKey>:<corrKey> → int32 live message-start instances (ADR-0094)
	cfDefInstanceCount       columnFamily = 0x17 // defInst:<procDefKey> → int64 active-instance count (merge, ADR-0080)
	cfElementTokenCount      columnFamily = 0x18 // elTok:<procDefKey>:<elementId> → int64 live-token count (merge, ADR-0080)
	cfElementVisitAgg        columnFamily = 0x19 // elVisAgg:<procDefKey>:<elementId> → int64 cumulative visits (merge, ADR-0080)
	cfDefCompletedCount      columnFamily = 0x1A // defDone:<procDefKey> → int64 finished-instance count (merge, ADR-0083)
	cfDefLastActivity        columnFamily = 0x1B // defAct:<procDefKey> → int64 unix-nano of the latest instance event (set, ADR-0083)
	cfSignalSubscription     columnFamily = 0x1C // sigSub:<name>:<elKey> → SignalSubscriptionValue (ADR-0088)
	cfVariableAudit          columnFamily = 0x1D // varAudit:<piKey>:<ts>:<pos> → VariableAuditValue (ADR-0098)
	cfCompensable            columnFamily = 0x1E // comp:<scopeKey>:<seq> → CompensableValue (ADR-0103)
	cfCanceling              columnFamily = 0x1F // canceling:<txScopeKey> → 1: a transaction being cancelled (ADR-0108)
	cfHistoryExpiry          columnFamily = 0x20 // histExp:<purgeDueDate>:<piKey> → nil (ADR-0146)
	cfRuntimeTotal           columnFamily = 0x21 // rtTotal:<kind> → int64 engine-wide live count (merge, ADR-0142)
	cfOperatorAction         columnFamily = 0x22 // opAct:<piKey>:<ts>:<pos> → OperatorActionValue (ADR-0159)
	cfChildByParent          columnFamily = 0x23 // childByParent:<callElKey>:<childPiKey> → nil (ADR-draft-child-instance-index)
)

// keyDefInstanceCount keys a definition's active-instance counter. A point key
// (get/merge only), so the definition key follows the column-family byte directly.
// Maintained as a signed merge so create (+1) and terminate (−1) are write-only
// (invariant I1), and read in O(1) instead of scanning every instance (ADR-0080).
func keyDefInstanceCount(procDefKey uint64) []byte {
	return appendBE64([]byte{byte(cfDefInstanceCount)}, procDefKey)
}

// keyDefCompletedCount keys a definition's finished-instance counter (completed or
// terminated). A point key, maintained as a monotonic merge (+1 on each terminal
// event, never decremented — there is no un-finishing), so the instances-summary
// "finished" column reads in O(1) instead of scanning the whole history, which does
// not shrink when active instances are drained into it (ADR-0083, extending ADR-0080).
func keyDefCompletedCount(procDefKey uint64) []byte {
	return appendBE64([]byte{byte(cfDefCompletedCount)}, procDefKey)
}

// keyDefLastActivity keys a definition's last-activity timestamp: the unix-nano of
// the most recent instance lifecycle event (start or finish) of any of its instances.
// A point key written by overwrite — the processor's event timestamps are non-
// decreasing in log order, so the last write is the latest and replay rebuilds it
// identically (invariant I4); read in O(1) for the summary's "last activity" column
// (ADR-0083).
func keyDefLastActivity(procDefKey uint64) []byte {
	return appendBE64([]byte{byte(cfDefLastActivity)}, procDefKey)
}

// runtimeCountPrefix scans every per-element counter of one definition (its live
// tokens, or its cumulative visits) — O(elements), not O(instances).
// runtimeTotalKind names one engine-wide live-entity counter. The values are part of
// the on-disk key, so they are append-only: a new kind takes the next number and an
// obsolete one is never reused (ADR-0142).
type runtimeTotalKind byte

const (
	rtOpenJobs             runtimeTotalKind = 1
	rtPendingTimers        runtimeTotalKind = 2
	rtMessageSubscriptions runtimeTotalKind = 3
)

// keyRuntimeTotal keys an engine-wide live-entity counter. One key per kind, so the
// count reads in O(1) instead of scanning the family it summarises — the difference
// between a metric a scrape can take every fifteen seconds and one it cannot
// (ADR-0142, following the per-definition counters of ADR-0080).
func keyRuntimeTotal(kind runtimeTotalKind) []byte {
	return []byte{byte(cfRuntimeTotal), byte(kind)}
}

func runtimeCountPrefix(cf columnFamily, procDefKey uint64) []byte {
	return appendBE64([]byte{byte(cf)}, procDefKey)
}

// keyElementTokenCount keys a definition-element live-token counter: the
// definition leads so one definition's per-element token counts are a single
// prefix scan; the element index is the trailing component. Incremented when an
// element instance activates, decremented when it completes/terminates (ADR-0080).
func keyElementTokenCount(procDefKey uint64, elementId int32) []byte {
	return appendBE32(runtimeCountPrefix(cfElementTokenCount, procDefKey), uint32(elementId))
}

// keyElementVisitAgg keys a definition-element cumulative-visit counter — the
// aggregate of the per-instance visit history (ADR-0022) so the heatmap reads in
// O(elements). Incremented on activation, never decremented (ADR-0080).
func keyElementVisitAgg(procDefKey uint64, elementId int32) []byte {
	return appendBE32(runtimeCountPrefix(cfElementVisitAgg, procDefKey), uint32(elementId))
}

// elementIdFromCountKey extracts the trailing element index from a per-element
// runtime counter key (token or visit).
func elementIdFromCountKey(k []byte) int32 {
	return int32(binary.BigEndian.Uint32(k[len(k)-4:]))
}

// keyInboundHighWater keys an external event source's inbound high-water mark by
// its opaque source id (ADR-0075). It is a point key (get/put only, never scanned),
// so the id bytes follow the column-family byte directly.
func keyInboundHighWater(sourceID string) []byte {
	return append([]byte{byte(cfInboundHighWater)}, sourceID...)
}

func appendBE64(dst []byte, v uint64) []byte { return binary.BigEndian.AppendUint64(dst, v) }
func appendBE32(dst []byte, v uint32) []byte { return binary.BigEndian.AppendUint32(dst, v) }

// appendOrderedInt64 encodes v so big-endian bytes sort in numeric order across
// the whole int64 range, by flipping the sign bit (otherwise negatives, with
// their high bit set, would sort after positives).
func appendOrderedInt64(dst []byte, v int64) []byte {
	return appendBE64(dst, uint64(v)^(1<<63))
}

func keyElementInstance(key uint64) []byte {
	return appendBE64([]byte{byte(cfElementInstance)}, key)
}

func keyElByProc(procKey, elKey uint64) []byte {
	return appendBE64(elByProcPrefix(procKey), elKey)
}

// keyChildByParent keys the reverse call-activity link: the child process instance
// a call-activity element instance started (ADR-0076). The child records its parent
// on its own record; this index records the other direction, so "which instance did
// this call activity start?" is a prefix scan of one element's children instead of a
// walk of every live instance.
//
// The child key is part of the key rather than the value so the entry is
// self-describing and the write needs no read-modify-write.
func keyChildByParent(callElKey, childPiKey uint64) []byte {
	return appendBE64(childByParentPrefix(callElKey), childPiKey)
}

// childByParentPrefix scans one call-activity element instance's children.
func childByParentPrefix(callElKey uint64) []byte {
	return appendBE64([]byte{byte(cfChildByParent)}, callElKey)
}

// keyIncident keys an incident by the element instance it is attached to — one
// activity holds at most one job, so at most one incident (ADR-0061).
func keyIncident(elKey uint64) []byte {
	return appendBE64([]byte{byte(cfIncident)}, elKey)
}

func elByProcPrefix(procKey uint64) []byte {
	return appendBE64([]byte{byte(cfElByProc)}, procKey)
}

func keyJob(key uint64) []byte {
	return appendBE64([]byte{byte(cfJob)}, key)
}

func keyJobActivatable(jobType int32, key uint64) []byte {
	return appendBE64(jobActivatablePrefix(jobType), key)
}

// keyJobByElement keys the reverse lookup from an element instance to its job, so
// an interrupting boundary event can find and cancel the host activity's job. An
// activity holds at most one job at a time, so the element key alone identifies it.
func keyJobByElement(elKey uint64) []byte {
	return appendBE64([]byte{byte(cfJobByElement)}, elKey)
}

func jobActivatablePrefix(jobType int32) []byte {
	return appendBE32([]byte{byte(cfJobActivatable)}, uint32(jobType))
}

func keyTimer(dueDate int64, key uint64) []byte {
	return appendBE64(appendOrderedInt64([]byte{byte(cfTimer)}, dueDate), key)
}

func keyProcessInstance(key uint64) []byte {
	return appendBE64([]byte{byte(cfProcessInstance)}, key)
}

func keyProcessInstanceHistory(key uint64) []byte {
	return appendBE64([]byte{byte(cfProcessInstanceHistory)}, key)
}

// keyHistoryExpiry keys a finished instance's scheduled hard delete by its purge due
// date, so the retention sweep finds what is due with a range scan up to now instead of
// walking the whole history (ADR-0146). The same shape as keyTimer, for the same reason:
// the ordered due date leads, the instance key disambiguates entries sharing one date.
func keyHistoryExpiry(dueDate int64, piKey uint64) []byte {
	return appendBE64(appendOrderedInt64([]byte{byte(cfHistoryExpiry)}, dueDate), piKey)
}

// orderedInt64At decodes an int64 written by appendOrderedInt64 at the given offset —
// the inverse sign-bit flip. Used to read a history-expiry entry's due date back out of
// its key, which is where that date lives (the entry has no value).
func orderedInt64At(k []byte, off int) int64 {
	return int64(binary.BigEndian.Uint64(k[off:off+8]) ^ (1 << 63))
}

func keyActiveChildren(scope uint64) []byte {
	return appendBE64([]byte{byte(cfActiveChildren)}, scope)
}

// keyActiveStartKey keys the count of live message-start instances of one definition
// that began with a given correlation key (ADR-0094). The definition key is fixed-
// width big-endian so a variable-length correlation key can follow unambiguously.
func keyActiveStartKey(defKey uint64, correlationKey string) []byte {
	return append(appendBE64([]byte{byte(cfActiveStartKey)}, defKey), correlationKey...)
}

func keyMeta(name string) []byte {
	return append([]byte{byte(cfMeta)}, name...)
}

// keyElementVisit keys the per-instance visit counter for one BPMN element. The
// process-definition key leads, so every visit across a definition's instances
// is one prefix scan (the aggregate heatmap); the process-instance key follows,
// so a single instance's visits are a narrower prefix scan; the element index is
// the trailing component the scan folds by. All big-endian so lexicographic byte
// order matches numeric order.
func keyElementVisit(procDefKey, piKey uint64, elementId int32) []byte {
	return appendBE32(elementVisitInstancePrefix(procDefKey, piKey), uint32(elementId))
}

// elementVisitDefPrefix scans every element visit recorded for a definition,
// across all of its instances.
func elementVisitDefPrefix(procDefKey uint64) []byte {
	return appendBE64([]byte{byte(cfElementVisit)}, procDefKey)
}

// elementVisitInstancePrefix scans the element visits of a single instance.
func elementVisitInstancePrefix(procDefKey, piKey uint64) []byte {
	return appendBE64(elementVisitDefPrefix(procDefKey), piKey)
}

// elementIdFromVisitKey extracts the trailing element index from a visit key.
func elementIdFromVisitKey(k []byte) int32 {
	return int32(binary.BigEndian.Uint32(k[len(k)-4:]))
}

// keyMessageFlow keys a retained message-flow history record. The receiver
// definition leads, so every message a pool received is one prefix scan; the
// event timestamp follows so the scan yields them in the order they occurred
// (the replay timeline), and the log position is the trailing disambiguator so
// two flows in the same nanosecond keep distinct keys. All big-endian / sign-
// flipped so lexicographic byte order matches numeric (and thus time) order.
func keyMessageFlow(receiverDefKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(messageFlowDefPrefix(receiverDefKey), ts)
	return appendBE64(b, pos)
}

// messageFlowDefPrefix scans every message flow a definition received, in time
// order.
func messageFlowDefPrefix(receiverDefKey uint64) []byte {
	return appendBE64([]byte{byte(cfMessageFlow)}, receiverDefKey)
}

// timestampFromFlowKey extracts the event timestamp from a message-flow key,
// inverting the sign-flip appendOrderedInt64 applied.
func timestampFromFlowKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromFlowKey extracts the trailing log position from a message-flow key.
func positionFromFlowKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// keyElementStep keys one retained element-activation step of a single process
// instance. The process-instance key leads, so every step of one instance is one
// prefix scan; the event timestamp follows so the scan yields them in the order
// they occurred (the step-by-step replay timeline), and the log position is the
// trailing disambiguator so two steps in the same nanosecond keep distinct keys.
// All big-endian / sign-flipped so lexicographic byte order matches numeric (and
// thus time) order. Unlike the element-visit counter (ADR-0022) this is keyed by
// instance, ordered in time, and never aggregated across instances (ADR-0046).
func keyElementStep(piKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(elementStepInstancePrefix(piKey), ts)
	return appendBE64(b, pos)
}

// elementStepInstancePrefix scans every step recorded for one process instance,
// in time order.
func elementStepInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfElementStep)}, piKey)
}

func keyElementReplay(piKey uint64, ts int64, pos uint64) []byte {
	k := appendBE64([]byte{byte(cfElementReplay)}, piKey)
	k = appendOrderedInt64(k, ts)
	return appendBE64(k, pos)
}

func elementReplayInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfElementReplay)}, piKey)
}

// timestampFromStepKey extracts the event timestamp from an element-step key,
// inverting the sign-flip appendOrderedInt64 applied.
func timestampFromStepKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromStepKey extracts the trailing log position from an element-step key.
func positionFromStepKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// keyVariableSnapshot keys one retained variable change of a scope (a process
// instance today). The scope key leads, so every change under one scope is one
// prefix scan; the event timestamp follows so the scan yields them in change
// order, and the log position is the trailing disambiguator. Same shape as the
// element-step key, so a single instance's step and variable timelines fold
// together by position for step-by-step replay (ADR-0048).
func keyVariableSnapshot(scopeKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(variableSnapshotScopePrefix(scopeKey), ts)
	return appendBE64(b, pos)
}

// variableSnapshotScopePrefix scans every variable change recorded under one
// scope, in change order.
func variableSnapshotScopePrefix(scopeKey uint64) []byte {
	return appendBE64([]byte{byte(cfVariableSnapshot)}, scopeKey)
}

// timestampFromVarSnapKey extracts the event timestamp from a variable-snapshot
// key, inverting the sign-flip appendOrderedInt64 applied.
func timestampFromVarSnapKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromVarSnapKey extracts the trailing log position from a
// variable-snapshot key.
func positionFromVarSnapKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

func variablePrefix(scope uint64) []byte {
	return appendBE64([]byte{byte(cfVariable)}, scope)
}

// keyVariable keys a variable by its scope and name. The name is the trailing,
// variable-length component, so a scope's variables are one prefix scan.
func keyVariable(scope uint64, name string) []byte {
	return append(variablePrefix(scope), name...)
}

func dataObjectPrefix(scope uint64) []byte {
	return appendBE64([]byte{byte(cfDataObject)}, scope)
}

// keyDataObject keys a data object by its scope and name, the same shape as a
// variable key: the name is the trailing, variable-length component, so a scope's
// data objects are one prefix scan (ADR-0053).
func keyDataObject(scope uint64, name string) []byte {
	return append(dataObjectPrefix(scope), name...)
}

// keyDataObjectSnapshot keys one retained data-object state change of a scope,
// the same (scope, ts, pos) shape as the variable-snapshot key, so the data
// object, variable, and element-step timelines fold together by log position for
// step-by-step replay and lineage (ADR-0053, mirroring ADR-0048).
func keyDataObjectSnapshot(scopeKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(dataObjectSnapshotScopePrefix(scopeKey), ts)
	return appendBE64(b, pos)
}

// dataObjectSnapshotScopePrefix scans every data-object state change recorded
// under one scope, in change order.
func dataObjectSnapshotScopePrefix(scopeKey uint64) []byte {
	return appendBE64([]byte{byte(cfDataObjectSnapshot)}, scopeKey)
}

// timestampFromDataObjSnapKey extracts the event timestamp from a data-object
// snapshot key, inverting the sign-flip appendOrderedInt64 applied.
func timestampFromDataObjSnapKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromDataObjSnapKey extracts the trailing log position from a
// data-object snapshot key.
func positionFromDataObjSnapKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// keyDecisionEvaluation keys one retained DMN decision evaluation of a scope (a
// process instance), the same (scope, ts, pos) shape as the variable-snapshot key
// so a business rule task's evaluations fold into the same instance timeline by
// log position (ADR-0066, mirroring ADR-0048). Append-only: one record per
// evaluation, never overwritten.
func keyDecisionEvaluation(scopeKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(decisionEvaluationScopePrefix(scopeKey), ts)
	return appendBE64(b, pos)
}

// decisionEvaluationScopePrefix scans every decision evaluation recorded under
// one scope, in evaluation order.
func decisionEvaluationScopePrefix(scopeKey uint64) []byte {
	return appendBE64([]byte{byte(cfDecisionEvaluation)}, scopeKey)
}

// timestampFromDecisionEvalKey extracts the event timestamp from a
// decision-evaluation key, inverting the sign-flip appendOrderedInt64 applied.
func timestampFromDecisionEvalKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromDecisionEvalKey extracts the trailing log position from a
// decision-evaluation key.
func positionFromDecisionEvalKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// scopeFromDecisionEvalKey extracts the owning scope (process instance) key from a
// decision-evaluation key: it follows the one-byte column-family tag. Used by a
// column-family-wide scan, which — unlike a single-scope scan — must recover each
// record's owning instance from the key itself.
func scopeFromDecisionEvalKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[1:9])
}

// keyVariableAudit keys one external variable override under its owning process
// instance, the same (instance, ts, pos) shape as the decision-evaluation and
// variable-snapshot keys, so the "who changed it" trail folds into the same instance
// timeline by log position (ADR-0098, mirroring ADR-0066/0048). Append-only: one
// record per variable an operator sets, never overwritten.
func keyVariableAudit(piKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(variableAuditScopePrefix(piKey), ts)
	return appendBE64(b, pos)
}

// variableAuditScopePrefix scans every external variable override recorded under one
// process instance, in change order.
func variableAuditScopePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfVariableAudit)}, piKey)
}

// keyOperatorAction keys one operator intervention under its owning process instance,
// the same (instance, ts, pos) shape as the variable-audit key, so the "who forced this
// step" trail folds into the same instance timeline by log position (ADR-0159,
// mirroring ADR-0098). Append-only: one record per intervention, never overwritten.
func keyOperatorAction(piKey uint64, ts int64, pos uint64) []byte {
	b := appendOrderedInt64(operatorActionScopePrefix(piKey), ts)
	return appendBE64(b, pos)
}

// operatorActionScopePrefix scans every operator intervention recorded under one
// process instance, in the order they happened.
func operatorActionScopePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfOperatorAction)}, piKey)
}

// timestampFromOperatorActionKey extracts the event timestamp from an operator-action
// key, inverting the sign-flip appendOrderedInt64 applied.
func timestampFromOperatorActionKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// positionFromOperatorActionKey extracts the trailing log position from an
// operator-action key.
func positionFromOperatorActionKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// timestampFromVariableAuditKey extracts the event timestamp from a variable-audit
// key, inverting the sign-flip appendOrderedInt64 applied.
func timestampFromVariableAuditKey(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[len(k)-16:]) ^ (1 << 63))
}

// keyCompensable keys one completed compensable activity under its scope, ordered by
// the completion event's log position (seq). Position is strictly increasing, so a
// forward scan yields completion order and a reverse scan yields reverse completion
// order — the order a compensation throw runs handlers in (ADR-0103). No timestamp
// component is needed: position alone totally orders the log.
func keyCompensable(scopeKey uint64, seq uint64) []byte {
	return appendBE64(compensableScopePrefix(scopeKey), seq)
}

// compensableScopePrefix scans every completed compensable activity recorded under one
// scope, in completion order.
func compensableScopePrefix(scopeKey uint64) []byte {
	return appendBE64([]byte{byte(cfCompensable)}, scopeKey)
}

// keyCanceling keys the cancelling marker for a transaction scope (ADR-0108). A point key
// (set/get/delete only), so the scope key follows the column-family byte directly.
func keyCanceling(scopeKey uint64) []byte {
	return appendBE64([]byte{byte(cfCanceling)}, scopeKey)
}

// positionFromVariableAuditKey extracts the trailing log position from a
// variable-audit key.
func positionFromVariableAuditKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// appendLenString appends a uint32 length prefix followed by s, so a
// variable-length string can be an unambiguous key component (the length marks
// where it ends, letting a later component follow).
func appendLenString(dst []byte, s string) []byte {
	return append(appendBE32(dst, uint32(len(s))), s...)
}

// messageSubscriptionPrefix is the exact-match scan prefix for all subscriptions
// waiting on a (message name, correlation key) pair — the publish access pattern.
func messageSubscriptionPrefix(name, correlationKey string) []byte {
	b := appendLenString([]byte{byte(cfMessageSubscription)}, name)
	return appendLenString(b, correlationKey)
}

// keyMessageSubscription keys a subscription by its (name, correlationKey) match
// pair with the element-instance key as the trailing disambiguator, so several
// instances can wait on the same message and key.
func keyMessageSubscription(name, correlationKey string, elKey uint64) []byte {
	return appendBE64(messageSubscriptionPrefix(name, correlationKey), elKey)
}

// signalSubscriptionPrefix is the exact-match scan prefix for all subscriptions
// waiting on a signal name — the broadcast access pattern. A signal has no
// correlation key, so the prefix is name alone (ADR-0088).
func signalSubscriptionPrefix(name string) []byte {
	return appendLenString([]byte{byte(cfSignalSubscription)}, name)
}

// keySignalSubscription keys a signal subscription by its name with the
// element-instance key as the trailing disambiguator, so several instances can
// wait on the same signal (a broadcast fans out to every one).
func keySignalSubscription(name string, elKey uint64) []byte {
	return appendBE64(signalSubscriptionPrefix(name), elKey)
}

// prefixEnd returns the smallest key strictly greater than every key beginning
// with prefix, for use as an exclusive upper bound in a range scan. It returns
// nil when prefix is all 0xff (no finite upper bound).
func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

// trailingKey extracts the final big-endian uint64 (the entity key) from an
// index key whose suffix is that key.
func trailingKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[len(k)-8:])
}

// instanceFromReplayKey extracts the process instance from an element-replay key,
// which a whole-store scan needs and a per-instance one already knew.
func instanceFromReplayKey(k []byte) uint64 {
	return binary.BigEndian.Uint64(k[1:])
}
