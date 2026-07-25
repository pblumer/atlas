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
	cfVariableSnapshot       columnFamily = 0x0F // varSnap:<scopeKey>:<ts>:<pos> → VariableValue
	cfDataObject             columnFamily = 0x10 // do:<scopeKey>:<name> → DataObjectValue
	cfDataObjectSnapshot     columnFamily = 0x11 // doSnap:<scopeKey>:<ts>:<pos> → DataObjectValue
	cfIncident               columnFamily = 0x12 // incident:<elKey> → IncidentValue (ADR-0061)
)

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

func keyActiveChildren(scope uint64) []byte {
	return appendBE64([]byte{byte(cfActiveChildren)}, scope)
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
