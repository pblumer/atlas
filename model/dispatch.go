package model

import "encoding/binary"

// DispatchChannel says whether a dispatch was a signal or a message.
type DispatchChannel uint8

const (
	// DispatchSignal is a signal broadcast (ADR-0088).
	DispatchSignal DispatchChannel = 1
	// DispatchMessage is a message published for correlation (ADR-0020).
	DispatchMessage DispatchChannel = 2
)

// DispatchSource says what sent a dispatch.
type DispatchSource uint8

const (
	// DispatchFromProcess is a running instance: a throw event, an end event, or a
	// send task naming a message.
	DispatchFromProcess DispatchSource = 1
	// DispatchFromPublish is a publish with no sending instance: POST /api/v1/messages,
	// or atlas's own order and fulfilment publishes.
	DispatchFromPublish DispatchSource = 2
	// DispatchFromInbound is a Worker's inbound watch (ADR-0075).
	DispatchFromInbound DispatchSource = 3
	// DispatchDirected is a directed delivery to one running instance (ADR-0428).
	DispatchDirected DispatchSource = 4
)

// DispatchValue is the fact that a signal was broadcast or a message published, by
// whom, and how far it reached (ADR-0472 §1). It is written once per send,
// on the command path and after the deliveries, so the counts are what the send did:
// Reached the subscriptions whose element still waited, Started the instances it
// started. A send that reached nobody is written with both zero.
//
// It carries no payload: what a signal or a message carries is every variable of the
// sending instance, and the log that is folded from this records names and moments,
// never values.
type DispatchValue struct {
	Channel        DispatchChannel
	Source         DispatchSource
	Name           string
	CorrelationKey string // a message's; empty for a signal
	// The sending instance, its definition and the element that sent: set for
	// DispatchFromProcess, zero otherwise.
	SenderProcessInstanceKey uint64
	SenderProcessDefKey      uint64
	SenderElementId          int32
	// The instance a directed delivery was addressed to and its definition: set for
	// DispatchDirected, zero otherwise.
	TargetProcessInstanceKey uint64
	TargetProcessDefKey      uint64
	Reached                  uint32
	Started                  uint32
}

func (*DispatchValue) ValueType() ValueType { return VTDispatch }

func (v *DispatchValue) encode(dst []byte) []byte {
	dst = append(dst, byte(v.Channel), byte(v.Source))
	dst = appendString(dst, v.Name)
	dst = appendString(dst, v.CorrelationKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.SenderProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.SenderProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.SenderElementId))
	dst = binary.LittleEndian.AppendUint64(dst, v.TargetProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.TargetProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, v.Reached)
	return binary.LittleEndian.AppendUint32(dst, v.Started)
}

// dispatchTail is the fixed-width part after the two strings.
const dispatchTail = 8 + 8 + 4 + 8 + 8 + 4 + 4

func (v *DispatchValue) decode(src []byte) error {
	if len(src) < 2 {
		return ErrShortBuffer
	}
	v.Channel, v.Source = DispatchChannel(src[0]), DispatchSource(src[1])
	name, rest, err := readString(src[2:])
	if err != nil {
		return err
	}
	key, rest, err := readString(rest)
	if err != nil {
		return err
	}
	if len(rest) < dispatchTail {
		return ErrShortBuffer
	}
	v.Name, v.CorrelationKey = name, key
	v.SenderProcessInstanceKey = binary.LittleEndian.Uint64(rest[0:])
	v.SenderProcessDefKey = binary.LittleEndian.Uint64(rest[8:])
	v.SenderElementId = int32(binary.LittleEndian.Uint32(rest[16:]))
	v.TargetProcessInstanceKey = binary.LittleEndian.Uint64(rest[20:])
	v.TargetProcessDefKey = binary.LittleEndian.Uint64(rest[28:])
	v.Reached = binary.LittleEndian.Uint32(rest[36:])
	v.Started = binary.LittleEndian.Uint32(rest[40:])
	return nil
}
