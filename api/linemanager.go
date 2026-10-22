package api

import (
	"fmt"
	"strings"

	"github.com/pblumer/atlas/api/order"
)

// Who somebody's line manager is (ADR-0470).
//
// The account holds it as a reference — another principal id — reported by the
// directory mirror or set by an administrator. This file is the one place that reads
// it, for the two moments it matters: a `superior` approval starting, and an
// escalation asking for the next person up.
//
// It used to be the approval model's job, through a directory search keyed by the
// orderer's principal id, which no directory knows (#1311). The server does not read a
// directory now either: it reads what the mirror reported, or what an administrator
// typed.

// lineManagerOf answers who `who`'s line manager is, as a principal id. `who` is a
// principal id or a username — an approval's current holder can be either, since a
// person reassigning it may type a name. An empty answer comes with the reason, in a
// sentence an operator can act on; err is reserved for the store failing.
//
// Run loop only: it reads the user store.
func (s *Server) lineManagerOf(who string) (manager, why string, err error) {
	person, ok, err := s.accountOf(who)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", fmt.Sprintf("there is no account %s to find a line manager for", who), nil
	}
	if person.ManagerID == "" {
		if person.ManagerDirectoryID != "" {
			return "", fmt.Sprintf("the directory names %s as the line manager of %s, and that person has no account here yet",
				person.ManagerDirectoryID, person.ID), nil
		}
		return "", fmt.Sprintf("%s has no line manager on record; set one on the account, "+
			"or let the directory mirror report it", person.ID), nil
	}
	if person.ManagerID == person.ID {
		return "", fmt.Sprintf("%s is recorded as their own line manager", person.ID), nil
	}
	boss, ok, err := s.users.Get(person.ManagerID)
	if err != nil {
		return "", "", err
	}
	switch {
	case !ok:
		return "", fmt.Sprintf("the line manager of %s is %s, which is no account here any more",
			person.ID, person.ManagerID), nil
	case boss.Disabled:
		return "", fmt.Sprintf("the line manager of %s is %s, whose account is disabled", person.ID, boss.ID), nil
	}
	return boss.ID, "", nil
}

// accountOf finds an account by principal id, and failing that by username — the two
// spellings a task holder is written in (ADR-0042).
func (s *Server) accountOf(who string) (User, bool, error) {
	if who = strings.TrimSpace(who); who == "" {
		return User{}, false, nil
	}
	if u, ok, err := s.users.Get(who); err != nil || ok {
		return u, ok, err
	}
	return s.users.byUsername(who)
}

// lineManagerStep is the escalation's answer to "whose superior is this?" when the
// caller asked for the line manager rather than naming somebody. Nobody is the
// answer "nobody", which stalls, as an empty superior always has.
//
// The manager is named in the spelling the approval's own history already uses for
// them, if it uses one: an approval that started with a username and is followed up a
// line kept in ids would otherwise hand itself back to somebody who had it, because
// [order.Escalate]'s loop guard compares names, not people.
func (s *Server) lineManagerStep(a order.Assignment) func(string) string {
	return func(current string) string {
		manager, _, err := s.lineManagerOf(current)
		if err != nil || manager == "" {
			return ""
		}
		if boss, ok, _ := s.users.Get(manager); ok && boss.Username != "" && a.Held(boss.Username) {
			return boss.Username
		}
		return manager
	}
}

// setLineManager is an administrator setting or clearing an account's line manager.
//
// conflict is the directory owning the field: an account whose manager the mirror
// reports keeps what the directory says, because a value typed here would outlive the
// directory changing it (ADR-0315). refused is a manager that cannot be one — the
// account itself, or an id that names no account. A group is not a line manager, and
// a group's id names no account, so it is refused by the same check.
//
// Run loop only.
func (s *Server) setLineManager(u *User, managerID string) (conflict, refused string, err error) {
	managerID = strings.TrimSpace(managerID)
	if managerID == u.ManagerID {
		return "", "", nil // what it already is; asking again changes nothing, so refuses nothing
	}
	if u.ManagerDirectoryID != "" {
		return "the directory reports the line manager of " + u.ID + " (" + u.ManagerDirectoryID +
			"); change it there, and the mirror brings it here", "", nil
	}
	switch {
	case managerID == "":
		u.ManagerID = ""
		return "", "", nil
	case managerID == u.ID:
		return "", "an account cannot be its own line manager", nil
	}
	if _, ok, err := s.users.Get(managerID); err != nil {
		return "", "", err
	} else if !ok {
		return "", "there is no account " + managerID + " to be a line manager", nil
	}
	u.ManagerID = managerID
	return "", "", nil
}
