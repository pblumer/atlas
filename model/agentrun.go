package model

import "encoding/binary"

// AgentRunKind says which of the two facts an [AgentRunValue] is.
type AgentRunKind uint8

const (
	// AgentRunModelCall is one call to a language model: an ai task's one call, or one
	// round of an agent-driven ad-hoc subprocess.
	AgentRunModelCall AgentRunKind = 0
	// AgentRunToolResult is what one tool an agent called returned, paired with the call
	// by the model's own call id. Together with the model calls it makes the run a
	// transcript a later round can be put in front of the model again.
	AgentRunToolResult AgentRunKind = 1
)

// AgentRunLimit says which authored bound a model call reached, if any.
type AgentRunLimit uint8

const (
	// AgentRunLimitNone: the call stayed inside every bound.
	AgentRunLimitNone AgentRunLimit = 0
	// AgentRunLimitRounds: this call used up the rounds the container may run.
	AgentRunLimitRounds AgentRunLimit = 1
	// AgentRunLimitTokens: with this call the run crossed its token budget.
	AgentRunLimitTokens AgentRunLimit = 2
)

// How the free text of a record is stored.
const (
	// AgentRunTextClear: as the worker reported it.
	AgentRunTextClear uint8 = 0
	// AgentRunTextEnciphered: each text is an envelope under the data subject's key,
	// because the process declares personal data and a prompt may quote it (ADR-0314).
	AgentRunTextEnciphered uint8 = 1
	// AgentRunTextWithheld: the process declares personal data and the texts could not
	// be enciphered, so they were not stored at all. The numbers still are.
	AgentRunTextWithheld uint8 = 2
)

// AgentRunValue is one fact about an agent's run (ADR-0117's VTAgentRun, as built by
// ADR-0566): a call to a language model, or what a tool it called
// returned. It is append-only history keyed under its process instance, like a decision
// evaluation (ADR-0066), so an operator can read after the fact which model answered,
// what it was asked, what it chose and what that cost.
//
// Who says what matters here. The worker reports what only it can know — the model that
// answered, the token counts, the texts — and that part is an account, not a
// measurement: nothing in the engine can check a token count. The engine states the
// rest from the job it leased and its own state: which element, which round, the sums
// so far, whether a bound was reached and which tools the round activated. A report
// therefore cannot attach itself to another element, skip a round or hide one.
//
// The element is referenced by its index in the compiled graph (I5). All strings are
// length-prefixed, and the record is decoded field by field so a later field can be
// appended without a format change.
type AgentRunValue struct {
	ProcessInstanceKey uint64 // owning instance (the scope this record is keyed under)
	ElementInstanceKey uint64 // the ai task, or the agent-driven container
	JobKey             uint64 // the job the call completed; 0 for a tool result
	ProcessDefKey      uint64 // the definition, to map ElementId onto the diagram
	// ElementId is the ai task or container for a model call, and the tool activity
	// for a tool result.
	ElementId int32
	Kind      AgentRunKind
	// Round counts the model calls of this element instance, from 1. A tool result
	// carries the round that called it.
	Round uint32

	// --- a model call, as the worker reported it ---

	Model      string // the model that answered, as the provider named it
	StopReason string // why the model stopped, in the provider's own word
	// InputTokens is everything the model read for this call, cached or not;
	// CacheReadTokens and CacheWriteTokens are the parts of it served from and written
	// to the provider's prompt cache. OutputTokens is everything it wrote.
	InputTokens      uint64
	OutputTokens     uint64
	CacheReadTokens  uint64
	CacheWriteTokens uint64
	DurationMs       uint64 // how long the call took on the worker

	// --- a model call, as the engine states it ---

	// SumInputTokens and SumOutputTokens are this element instance's totals including
	// this call. They are written into the event so the fold stays a plain write.
	SumInputTokens  uint64
	SumOutputTokens uint64
	Limit           AgentRunLimit
	// ToolCalls is the canonical JSON list of the tools this round activated, each
	// {"tool": element id, "callId": the model's id}; "" when the call answered.
	ToolCalls string

	// --- free text ---

	// TextState is one of the AgentRunText constants and covers every field below.
	TextState uint8
	System    string // the standing instruction the model was given
	Prompt    string // what this call added to the conversation
	// Answer is the model's words for a model call, and the tool's result for a tool
	// result.
	Answer string
	// Wire names the provider format Turn is in ("messages", "chat-completions"). Turn
	// is the model's whole turn exactly as the provider returned it, kept so the next
	// round can replay it unchanged; another format cannot read it and falls back to
	// the texts.
	Wire string
	Turn string
	// Context is the canonical JSON object of the process variables the call was
	// given, so the next round can say what changed rather than repeat them.
	Context string
	// CallId pairs a tool result with the call that asked for it.
	CallId string
	// Fresh marks a model call that was put as a new conversation rather than as a
	// continuation of the rounds before it: the first round, and any later one that
	// restated the run in words — because the run had outgrown its context bound, the
	// provider changed, or an earlier turn could not be read back. A later round is
	// replayed from the last fresh one on. An appended field: a record written before
	// it ends after CallId and reads false.
	Fresh bool
}

func (*AgentRunValue) ValueType() ValueType { return VTAgentRun }

// agentRunFixed is the fixed-width head: four keys, the element, kind, round, the six
// counters of a call, the two sums, the limit and the text state.
const agentRunFixed = 4*8 + 4 + 1 + 4 + 7*8 + 1 + 1

func (v *AgentRunValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.JobKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = append(dst, byte(v.Kind))
	dst = binary.LittleEndian.AppendUint32(dst, v.Round)
	for _, n := range [...]uint64{
		v.InputTokens, v.OutputTokens, v.CacheReadTokens, v.CacheWriteTokens,
		v.DurationMs, v.SumInputTokens, v.SumOutputTokens,
	} {
		dst = binary.LittleEndian.AppendUint64(dst, n)
	}
	dst = append(dst, byte(v.Limit), v.TextState)
	for _, s := range [...]string{
		v.Model, v.StopReason, v.ToolCalls, v.System, v.Prompt, v.Answer,
		v.Wire, v.Turn, v.Context, v.CallId,
	} {
		dst = appendString(dst, s)
	}
	fresh := byte(0)
	if v.Fresh {
		fresh = 1
	}
	return append(dst, fresh)
}

func (v *AgentRunValue) decode(src []byte) error {
	if len(src) < agentRunFixed {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src)
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.JobKey = binary.LittleEndian.Uint64(src[16:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[24:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[32:]))
	v.Kind = AgentRunKind(src[36])
	v.Round = binary.LittleEndian.Uint32(src[37:])
	rest := src[41:]
	for _, dst := range [...]*uint64{
		&v.InputTokens, &v.OutputTokens, &v.CacheReadTokens, &v.CacheWriteTokens,
		&v.DurationMs, &v.SumInputTokens, &v.SumOutputTokens,
	} {
		*dst, rest = binary.LittleEndian.Uint64(rest), rest[8:]
	}
	v.Limit, v.TextState = AgentRunLimit(rest[0]), rest[1]
	rest = rest[2:]
	for _, dst := range [...]*string{
		&v.Model, &v.StopReason, &v.ToolCalls, &v.System, &v.Prompt, &v.Answer,
		&v.Wire, &v.Turn, &v.Context, &v.CallId,
	} {
		s, r, err := readString(rest)
		if err != nil {
			return err
		}
		*dst, rest = s, r
	}
	v.Fresh = len(rest) >= 1 && rest[0] == 1
	return nil
}

// AgentRunTotal is what an element instance's model calls add up to: how many there
// were, what they read and wrote, and whether the last one reached a bound. It is not a
// record of its own — it is folded from the AgentRun events, each of which already
// carries the sums — and it is what lets the engine weigh another round in one point
// read instead of a scan.
type AgentRunTotal struct {
	Rounds       uint32
	InputTokens  uint64
	OutputTokens uint64
	Limit        AgentRunLimit
}

// AgentRunTotalSize is the encoded width of an [AgentRunTotal].
const AgentRunTotalSize = 4 + 8 + 8 + 1

// AppendAgentRunTotal encodes t onto dst.
func AppendAgentRunTotal(dst []byte, t AgentRunTotal) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, t.Rounds)
	dst = binary.LittleEndian.AppendUint64(dst, t.InputTokens)
	dst = binary.LittleEndian.AppendUint64(dst, t.OutputTokens)
	return append(dst, byte(t.Limit))
}

// DecodeAgentRunTotal reads what AppendAgentRunTotal wrote; a short row reads as zero,
// which is "no call yet".
func DecodeAgentRunTotal(src []byte) AgentRunTotal {
	if len(src) < AgentRunTotalSize {
		return AgentRunTotal{}
	}
	return AgentRunTotal{
		Rounds:       binary.LittleEndian.Uint32(src),
		InputTokens:  binary.LittleEndian.Uint64(src[4:]),
		OutputTokens: binary.LittleEndian.Uint64(src[12:]),
		Limit:        AgentRunLimit(src[20]),
	}
}
