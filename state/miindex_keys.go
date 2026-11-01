package state

import "encoding/binary"

// miBodyPrefix scopes the OHS2 body discovery index to one process instance.
// The big-endian suffix orders distinct activations, including repeated bodies.
func miBodyPrefix(instanceKey uint64) []byte {
	return appendBE64([]byte{byte(cfMIBody)}, instanceKey)
}

func keyMIBody(instanceKey, activationPosition, bodyElementKey uint64) []byte {
	return appendBE64(appendBE64(miBodyPrefix(instanceKey), activationPosition), bodyElementKey)
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

// decodeMIIterationKey rejects keys outside the fixed-width OHS2 schema.
// It is intentionally strict so a corrupt index never returns a wrong lineage.
func decodeMIIterationKey(key []byte) (instanceKey, bodyElementKey, activationPosition, roundElementKey uint64, ok bool) {
	if len(key) != 33 || key[0] != byte(cfMIIteration) {
		return 0, 0, 0, 0, false
	}
	return binary.BigEndian.Uint64(key[1:9]), binary.BigEndian.Uint64(key[9:17]), binary.BigEndian.Uint64(key[17:25]), binary.BigEndian.Uint64(key[25:33]), true
}
