package order

import "sort"

// What reaches the person who ordered.
//
// Everything else in the fulfilment chain is addressed to somebody who can act on
// it: an incident escalates to an operator, an approval waits on an approver. The
// orderer can act on exactly three things — a line was refused, a line was given
// up on, and the order finished — so those are what they are told, and a failure
// or a blockage is not. Telling an orderer that provisioning threw an error gives
// them a worry and no action, and it would arrive again on every retry.
//
// A notice is owed for a *transition*, never for a state. [Propagate] runs after
// every settled line, so a function reporting what is true rather than what
// changed would send the same message on each pass.
//
// Nothing here sends anything. Delivery is a mail task in the fulfilment process,
// which puts it after fsync where a side effect belongs (I2) and leaves the
// channel, the wording and the language to the model rather than to this package.

// NoticeKind is what happened.
type NoticeKind string

const (
	// NoticeRejected: one line's approval was refused.
	NoticeRejected NoticeKind = "rejected"
	// NoticeAbandoned: one line's incident was given up on. Without this the
	// orderer cannot tell "never coming" from "still being worked on".
	NoticeAbandoned NoticeKind = "abandoned"
	// NoticeSettled: the order finished, however it finished.
	NoticeSettled NoticeKind = "settled"
)

// Notice is one message owed to one person.
type Notice struct {
	Kind NoticeKind `json:"kind"`
	// OrderID and Recipient say which order and who is told. Recipient is a
	// principal id — the message is addressed by the sender, not by this package,
	// which never holds an address (ADR-0314).
	OrderID   string `json:"orderId"`
	Recipient string `json:"recipient"`
	// ItemID names the line, empty on a settlement notice, which is about the
	// whole order.
	ItemID string `json:"itemId,omitempty"`
	// Reason carries the approver's words on a rejection.
	Reason string `json:"reason,omitempty"`
	// Outcome, Provisioned and NotProvisioned are the settlement notice's content:
	// the closing message is the one place the orderer is told the whole result.
	Outcome        Status   `json:"outcome,omitempty"`
	Provisioned    []string `json:"provisioned,omitempty"`
	NotProvisioned []string `json:"notProvisioned,omitempty"`
}

// Notices reports what somebody must be told about the change from before to
// after. Both are the same order at two moments; an unchanged order owes nothing.
//
// Line notices come first in item order, then the settlement, so the same
// transition always produces the same messages in the same sequence.
func Notices(before, after Order) []Notice {
	was := make(map[string]LineStatus, len(before.Lines))
	for _, l := range before.Lines {
		was[l.ItemID] = l.Status
	}

	lines := append([]Line(nil), after.Lines...)
	sort.Slice(lines, func(a, b int) bool { return lines[a].ItemID < lines[b].ItemID })

	var out []Notice
	for _, l := range lines {
		if was[l.ItemID] == l.Status {
			continue
		}
		switch l.Status {
		case StatusRejected:
			out = append(out, Notice{Kind: NoticeRejected, OrderID: after.ID,
				Recipient: after.Orderer, ItemID: l.ItemID, Reason: l.Reason})
		case StatusAbandoned:
			out = append(out, Notice{Kind: NoticeAbandoned, OrderID: after.ID,
				Recipient: after.Orderer, ItemID: l.ItemID})
		}
	}

	if outcome := Derive(after.Lines); outcome != OrderRunning && Derive(before.Lines) == OrderRunning {
		got, missed := split(after.Lines)
		out = append(out, Notice{Kind: NoticeSettled, OrderID: after.ID,
			Recipient: after.Orderer, Outcome: outcome,
			Provisioned: got, NotProvisioned: missed})
	}
	return out
}

// split sorts the lines into what the recipient got and what they did not.
func split(ls []Line) (provisioned, not []string) {
	for _, l := range ls {
		if l.Status.Satisfied() {
			provisioned = append(provisioned, l.ItemID)
		} else {
			not = append(not, l.ItemID)
		}
	}
	sort.Strings(provisioned)
	sort.Strings(not)
	return provisioned, not
}
