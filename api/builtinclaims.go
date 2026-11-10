package api

import (
	"slices"
	"strings"

	calendarcatalog "github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/teamsphone"
)

// CapabilityClaimState is how far a technical capability claim is established
// (ADR-0483, *Trust and claim states*).
type CapabilityClaimState string

const (
	// CapabilityClaimCandidate is a claim the operation's own table declares and no
	// evidence has verified. It is shown with its source and is never eligible for
	// routing. Every built-in claim is one today: no contract is published and no
	// evidence record exists, so none can be verified.
	CapabilityClaimCandidate CapabilityClaimState = "candidate"
)

// WorkerTypeOperation is one operation of a Worker Type and the technical
// capabilities it declares. Only operations that declare at least one are listed:
// an operation without a claim is unknown, and ADR-0483 forbids reading that as
// unsupported.
type WorkerTypeOperation struct {
	ID           string                      `json:"id"`
	Capabilities []WorkerTypeCapabilityClaim `json:"capabilities"`
}

// WorkerTypeCapabilityClaim is one declared technical capability (ADR-0480) with the
// state it has reached. It is descriptive: it authorizes nothing, routes nothing and
// does not say that another operation with a similar identifier is interchangeable.
type WorkerTypeCapabilityClaim struct {
	ID      string               `json:"id"`
	Version string               `json:"version"`
	Profile string               `json:"profile,omitempty"`
	State   CapabilityClaimState `json:"state"`
}

// claimRow is one operation's claims as its Worker Type's own table states them.
type claimRow struct {
	op     string
	claims []WorkerTypeCapabilityClaim
}

// builtInClaimSources reads each built-in Worker Type's claims from the operation
// table it runs from (ADR-0480 §2): the claim is a field on the row, so this is a
// projection of that table and not a second operation list. A Worker Type gets an
// entry when its table first carries a claim.
var builtInClaimSources = map[string]func() []claimRow{
	connectorKindEntra: func() []claimRow {
		return rowsOf(entra.Ops, func(o entra.Op) []WorkerTypeCapabilityClaim {
			return candidates(o.Capabilities, func(c entra.CapabilityClaim) (string, string, string) { return c.ID, c.Version, c.Profile })
		})
	},
	connectorKindCalendar: func() []claimRow {
		return rowsOf(calendarcatalog.Ops, func(o calendarcatalog.Op) []WorkerTypeCapabilityClaim {
			return candidates(o.Capabilities, func(c calendarcatalog.CapabilityClaim) (string, string, string) { return c.ID, c.Version, c.Profile })
		})
	},
	connectorKindTeamsPhone: func() []claimRow {
		return rowsOf(teamsphone.Ops, func(o teamsphone.Op) []WorkerTypeCapabilityClaim {
			return candidates(o.Capabilities, func(c teamsphone.CapabilityClaim) (string, string, string) { return c.ID, c.Version, c.Profile })
		})
	},
}

func rowsOf[O any](ops map[string]O, claims func(O) []WorkerTypeCapabilityClaim) []claimRow {
	rows := make([]claimRow, 0, len(ops))
	for name, op := range ops {
		if c := claims(op); len(c) > 0 {
			rows = append(rows, claimRow{op: name, claims: c})
		}
	}
	return rows
}

func candidates[C any](in []C, fields func(C) (id, version, profile string)) []WorkerTypeCapabilityClaim {
	out := make([]WorkerTypeCapabilityClaim, 0, len(in))
	for _, c := range in {
		id, version, profile := fields(c)
		out = append(out, WorkerTypeCapabilityClaim{ID: id, Version: version, Profile: profile, State: CapabilityClaimCandidate})
	}
	return out
}

// builtInOperationClaims lists a built-in Worker Type's operations that declare a
// technical capability, sorted by operation, or nil when it declares none.
func builtInOperationClaims(connectorKind string) []WorkerTypeOperation {
	source, ok := builtInClaimSources[connectorKind]
	if !ok {
		return nil
	}
	rows := source()
	if len(rows) == 0 {
		return nil
	}
	slices.SortFunc(rows, func(a, b claimRow) int { return strings.Compare(a.op, b.op) })
	out := make([]WorkerTypeOperation, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkerTypeOperation{ID: r.op, Capabilities: r.claims})
	}
	return out
}
