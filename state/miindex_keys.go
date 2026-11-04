package state

import "encoding/binary"

// The multi-instance history index (ADR-0517) is a family of fixed-width,
// big-endian keys. Every key starts with the process-instance key, so purge is a
// range tombstone per family, and every iteration key below the instance starts
// with the *concrete body activation* (body element-instance key and its
// activation position), so repeated or nested executions of one BPMN element
// never share a range.
//
// These helpers allocate and are used by read paths and tests only. The apply
// path builds the same layouts into the transaction's reusable buffer (see
// miindex_projection.go); TestMIIndexKeyLayoutsAgree holds the two together.

// miBodyPrefix scopes the body discovery index to one process instance.
// The big-endian suffix orders distinct activations, including repeated bodies.
func miBodyPrefix(instanceKey uint64) []byte {
	return appendBE64([]byte{byte(cfMIBody)}, instanceKey)
}

func keyMIBody(instanceKey, activationPosition, bodyElementKey uint64) []byte {
	return appendBE64(appendBE64(miBodyPrefix(instanceKey), activationPosition), bodyElementKey)
}

func keyMIBodyByElement(piKey, bodyKey uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIBodyByElement)}, piKey), bodyKey)
}

// miBodyByParentPrefix scopes bodies to the multi-instance iteration that encloses
// them — parentRound 0 for bodies no iteration encloses — ordered by activation.
// The Operations overview lists the top level without visiting a nested body
// seeded once per outer iteration.
func miBodyByParentPrefix(piKey, parentRound uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIBodyByParent)}, piKey), parentRound)
}

func keyMIBodyByParent(piKey, parentRound, activationPosition, bodyKey uint64) []byte {
	return appendBE64(appendBE64(miBodyByParentPrefix(piKey, parentRound), activationPosition), bodyKey)
}

// miIterationPrefix scopes iteration discovery to one specific body activation.
// Element IDs alone cannot distinguish repeated or nested multi-instance bodies.
func miIterationPrefix(instanceKey, bodyElementKey, activationPosition uint64) []byte {
	return appendBE64(appendBE64(appendBE64([]byte{byte(cfMIIteration)}, instanceKey), bodyElementKey), activationPosition)
}

func keyMIIteration(instanceKey, bodyElementKey, activationPosition, roundElementKey uint64) []byte {
	return appendBE64(miIterationPrefix(instanceKey, bodyElementKey, activationPosition), roundElementKey)
}

// keyMIRoundByElement provides an exact lookup independent of page position.
func keyMIRoundByElement(instanceKey, roundElementKey uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIRoundByElement)}, instanceKey), roundElementKey)
}

// miBodyScopedPrefix is the (family, instance, body, activation) prefix shared by
// the three ordered iteration families.
func miBodyScopedPrefix(family columnFamily, piKey, bodyKey, bodyPos uint64) []byte {
	return appendBE64(appendBE64(appendBE64([]byte{byte(family)}, piKey), bodyKey), bodyPos)
}

// miRoundOrderPrefix orders one body activation's iterations by their durable
// loopCounter, then by element-instance key. A seek to a loopCounter is how an
// operator jumps to iteration 37,421 without enumerating the 37,420 before it.
func miRoundOrderPrefix(piKey, bodyKey, bodyPos uint64) []byte {
	return miBodyScopedPrefix(cfMIRoundOrder, piKey, bodyKey, bodyPos)
}

// miRoundStatePrefix is the same ordering restricted to one lifecycle state.
func miRoundStatePrefix(piKey, bodyKey, bodyPos uint64, st MIHistoryState) []byte {
	return append(miBodyScopedPrefix(cfMIRoundByState, piKey, bodyKey, bodyPos), byte(st))
}

// miRoundIncidentPrefix is the same ordering restricted to iterations that have
// had at least one incident attributed to them — a monotonic, historical fact.
func miRoundIncidentPrefix(piKey, bodyKey, bodyPos uint64) []byte {
	return miBodyScopedPrefix(cfMIRoundIncident, piKey, bodyKey, bodyPos)
}

func miIncidentOwnerPrefix(piKey, ownerKey uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIIncidentByOwner)}, piKey), ownerKey)
}

func keyMIIncidentOpen(piKey, incidentElementKey uint64) []byte {
	return appendBE64(appendBE64([]byte{byte(cfMIIncidentOpen)}, piKey), incidentElementKey)
}

// decodeMIIterationKey rejects keys outside the fixed-width schema.
// It is intentionally strict so a corrupt index never returns a wrong lineage.
func decodeMIIterationKey(key []byte) (instanceKey, bodyElementKey, activationPosition, roundElementKey uint64, ok bool) {
	if len(key) != 33 || key[0] != byte(cfMIIteration) {
		return 0, 0, 0, 0, false
	}
	return binary.BigEndian.Uint64(key[1:9]), binary.BigEndian.Uint64(key[9:17]), binary.BigEndian.Uint64(key[17:25]), binary.BigEndian.Uint64(key[25:33]), true
}

// miOrderedTail reads the (loopCounter, roundElementKey) suffix every ordered
// iteration family ends with, after the given prefix length.
func miOrderedTail(key []byte, prefixLen int) (loopCounter, roundKey uint64, ok bool) {
	if len(key) != prefixLen+16 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint64(key[prefixLen:]), binary.BigEndian.Uint64(key[prefixLen+8:]), true
}

// miBodyCounterKind names one body counter. The values are on-disk key bytes and
// append-only.
type miBodyCounterKind byte

const (
	miCountIterations              miBodyCounterKind = 1
	miCountCompleted               miBodyCounterKind = 2
	miCountTerminated              miBodyCounterKind = 3
	miCountIncidentsRaised         miBodyCounterKind = 4
	miCountIterationsWithIncidents miBodyCounterKind = 5
	miCountChanges                 miBodyCounterKind = 6
)

var miBodyCounterKinds = [...]miBodyCounterKind{
	miCountIterations, miCountCompleted, miCountTerminated,
	miCountIncidentsRaised, miCountIterationsWithIncidents, miCountChanges,
}

// keyMIBodyCounter keys one of a body activation's counters. The counters are
// write-only signed merges, like the active-children counters, so recording an
// iteration does not read the body (invariant I1).
func keyMIBodyCounter(piKey, bodyKey uint64, kind miBodyCounterKind) []byte {
	return append(appendBE64(appendBE64([]byte{byte(cfMIBodyCounter)}, piKey), bodyKey), byte(kind))
}
