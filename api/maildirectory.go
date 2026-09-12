package api

import (
	"fmt"
	"strings"
)

// Addressing somebody a model only holds a reference to.
//
// The portal names people by principal id and by nothing else: a name, a mail
// address, a department or a superior is resolved from the account when a screen
// is rendered, and never copied into an order, an entitlement or a variable
// (ADR-draft-portal-personal-data). An approval notification is that rule applied
// to a message instead of a screen. The model writes `to="=approvalRef"` — the
// person or group the *product* named — and the address is looked up here, in the
// server, at the moment the mail is sent.
//
// So no address enters a process variable, an order, or the event log. It exists
// in the resolved job and in the SMTP conversation, which is the least any mail
// can be sent with.

// mailDirectory resolves a mail recipient that is not an address. It implements
// [mail.Directory].
type mailDirectory struct{ s *Server }

// Recipients answers what a reference should reach: one person's address, or every
// address in a group.
//
// A person is named by username or by principal id, a group by id or by name —
// the same four spellings [Server.holdsTask] accepts when it decides who holds a
// task. That is not a coincidence to be tidied away later: the people a
// notification reaches and the people who may act on the task it is about have to
// be the same set, or the mail goes to somebody who then cannot do anything with
// it.
//
// An account without an address contributes nothing rather than failing the whole
// send: a group of five where one has no mail should still reach four. A reference
// that resolves to nobody at all is an error, reported by the caller.
func (d mailDirectory) Recipients(ref string) ([]string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}

	// A person first. A group named like a user would otherwise shadow them, and
	// an individual recipient is the commoner case by far.
	if u, ok, err := d.s.users.byUsername(ref); err != nil {
		return nil, err
	} else if ok {
		return addressOf(u.Email), nil
	}
	if u, ok, err := d.s.users.Get(ref); err != nil {
		return nil, err
	} else if ok {
		return addressOf(u.Email), nil
	}

	g, ok, err := d.s.groups.Get(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		if g, ok, err = d.s.groups.byName(ref, ""); err != nil {
			return nil, err
		} else if !ok {
			return nil, nil
		}
	}
	out := make([]string, 0, len(g.Members))
	for _, id := range g.Members {
		u, found, err := d.s.users.Get(id)
		if err != nil {
			return nil, fmt.Errorf("member %s of group %s: %w", id, ref, err)
		}
		if found {
			out = append(out, addressOf(u.Email)...)
		}
	}
	return out, nil
}

// addressOf is one account's address as a list, empty when the account has none.
func addressOf(email string) []string {
	if e := strings.TrimSpace(email); e != "" {
		return []string{e}
	}
	return nil
}
