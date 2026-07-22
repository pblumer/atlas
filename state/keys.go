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

func elByProcPrefix(procKey uint64) []byte {
	return appendBE64([]byte{byte(cfElByProc)}, procKey)
}

func keyJob(key uint64) []byte {
	return appendBE64([]byte{byte(cfJob)}, key)
}

func keyJobActivatable(jobType int32, key uint64) []byte {
	return appendBE64(jobActivatablePrefix(jobType), key)
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

func variablePrefix(scope uint64) []byte {
	return appendBE64([]byte{byte(cfVariable)}, scope)
}

// keyVariable keys a variable by its scope and name. The name is the trailing,
// variable-length component, so a scope's variables are one prefix scan.
func keyVariable(scope uint64, name string) []byte {
	return append(variablePrefix(scope), name...)
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
