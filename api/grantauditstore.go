package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// This file implements the durable side of ADR-0184: an
// append-only history of the access-control changes made to a project (ADR-0071) —
// shares, revokes, visibility flips, and ownership transfers. It is design-time
// config data, exactly like a project or a release, so it stays off the six engine
// invariants: recording a grant change never enters the WAL, the event log, or
// applyToState. The shape and per-application lookups deliberately mirror
// releaseStore (ADR-0128), the other append-only per-application history.

// Grant-audit action kinds. Each names a mutation on the sharing scope, recorded on
// the handler that performs it. The set is closed and small; a later action (e.g. a
// denied attempt) can join without reshaping the record.
const (
	GrantActionShare      = "share"      // a member was added or their role changed
	GrantActionUnshare    = "unshare"    // a member was revoked
	GrantActionVisibility = "visibility" // visibility changed (From → To)
	GrantActionTransfer   = "transfer"   // ownership moved (From owner → To owner)
)

// grantAudit is one immutable access-control event on a project. Everything is
// snapshotted by value so later edits — a renamed user, a re-shared member — cannot
// rewrite what the trail already recorded.
type grantAudit struct {
	ID string `json:"id"`
	// ApplicationID is the owning application. On disk this is the ADR-0034
	// projectId — ADR-0128 renames the API/UI boundary, not the stored shape.
	ApplicationID string `json:"applicationId"`
	At            int64  `json:"at"`
	// ActorID/ActorName identify who made the change (the request principal,
	// ADR-0044). ActorName is a username snapshot kept for display so the history
	// reads without a user-store lookup even after the account is renamed or removed.
	ActorID   string `json:"actorId,omitempty"`
	ActorName string `json:"actorName,omitempty"`
	Action    string `json:"action"`
	// Subject names the affected member for share/unshare: its type is "user" or
	// "group" (ADR-0180). Empty for visibility/transfer.
	SubjectType string `json:"subjectType,omitempty"`
	SubjectID   string `json:"subjectId,omitempty"`
	// Role is the granted role on a share. From/To carry the old and new values of a
	// visibility flip or an ownership transfer.
	Role string `json:"role,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// grantAuditStore is a durable store for grant-audit events, one JSON file per event
// id under a single directory. An event belongs to an application, so the lookups
// below are by application rather than by id, exactly like releaseStore.
type grantAuditStore struct {
	*sidecar.Store[grantAudit]
}

// newGrantAuditStore opens (creating if needed) the grant-audit directory. Events
// list newest-first by timestamp, tie-broken by id so the order is deterministic
// even for two events written in the same second.
func newGrantAuditStore(dir string) (*grantAuditStore, error) {
	s, err := sidecar.NewStore(dir, "grantauditstore",
		func(rec grantAudit) string { return rec.ID },
		sidecar.Order(func(a, b grantAudit) bool {
			if a.At != b.At {
				return a.At > b.At
			}
			return a.ID > b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &grantAuditStore{s}, nil
}

// forApplication returns an application's grant history, newest first. Never nil, so
// a caller can encode the result as a JSON array without a guard.
func (s *grantAuditStore) forApplication(appID string) ([]grantAudit, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []grantAudit{}
	for _, rec := range all {
		if rec.ApplicationID == appID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// deleteForApplication removes every grant-audit event of an application — the
// cleanup that runs when the application itself is deleted. Deleting nothing is not
// an error.
func (s *grantAuditStore) deleteForApplication(appID string) error {
	all, err := s.forApplication(appID)
	if err != nil {
		return err
	}
	for _, rec := range all {
		if err := s.Delete(rec.ID); err != nil {
			return err
		}
	}
	return nil
}
